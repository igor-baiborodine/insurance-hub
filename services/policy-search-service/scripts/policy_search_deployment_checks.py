#!/usr/bin/env python3
"""Policy Search deployment guards and service-owned verification hooks."""

from __future__ import annotations

import json
import os
import re
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

from scripts.go.service.configuration import service_record, target_record  # noqa: E402
from scripts.go.service.deployment import (  # noqa: E402
    DeploymentError as SharedDeploymentError,
)
from scripts.python.process_runner import CommandError, run_command  # noqa: E402

SERVICE = "policy-search"
EXPECTED_KINDS = {"ConfigMap", "Deployment", "Service", "NetworkPolicy"}
JAVA_NAMES = {"policy-search-api-legacy"}
BUSINESS_CLIENT_LABEL = "insurance-hub.io/policy-search-business-client"
MANAGEMENT_CLIENT_LABEL = "insurance-hub.io/policy-search-management-client"


class DeploymentError(RuntimeError):
    """Policy Search deployment state differs from the accepted contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise DeploymentError(message)


def safe_environment() -> dict[str, str]:
    allowed = (
        "HOME",
        "KUBECONFIG",
        "PATH",
        "SSL_CERT_DIR",
        "SSL_CERT_FILE",
        "TERM",
        "TMPDIR",
    )
    return {name: os.environ[name] for name in allowed if name in os.environ}


def run(argv: list[str], *, input_text: str | None = None, timeout: int = 30) -> str:
    result = run_command(
        argv,
        cwd=ROOT,
        env=safe_environment(),
        timeout_seconds=timeout,
        teardown_seconds=10,
        activity="Policy Search deployment command",
        input_text=input_text,
    )
    return result.stdout


def documents(text: str) -> list[dict[str, Any]]:
    return [item for item in yaml.safe_load_all(text) if isinstance(item, dict)]


def resource(items: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    matches = [
        item
        for item in items
        if item.get("kind") == kind and item.get("metadata", {}).get("name") == name
    ]
    require(len(matches) == 1, f"expected exactly one {kind}/{name}")
    return matches[0]


def labels_include(actual: Any, expected: Any) -> bool:
    return (
        isinstance(actual, dict)
        and isinstance(expected, dict)
        and all(actual.get(key) == value for key, value in expected.items())
    )


def matching_probe(actual: Any, expected: Any) -> bool:
    if actual == expected:
        return True
    if not isinstance(actual, dict) or not isinstance(expected, dict):
        return False
    declared_with_defaults = dict(expected)
    declared_with_defaults.setdefault("successThreshold", 1)
    return actual == declared_with_defaults


def matching_network_policy_spec(actual: Any, expected: Any) -> bool:
    if not isinstance(actual, dict) or not isinstance(expected, dict):
        return False
    actual_normalized = dict(actual)
    expected_normalized = dict(expected)
    for field in ("ingress", "egress"):
        if actual_normalized.get(field) == []:
            actual_normalized.pop(field)
        if expected_normalized.get(field) == []:
            expected_normalized.pop(field)
    return actual_normalized == expected_normalized


def matching_echo_predecessor_network_policy(actual: Any, expected: Any) -> bool:
    """Accept the Echo deny-all-egress policy before query dependencies are staged."""
    if matching_network_policy_spec(actual, expected):
        return True
    if not isinstance(expected, dict):
        return False
    predecessor = dict(expected)
    predecessor.pop("egress", None)
    return matching_network_policy_spec(actual, predecessor)


def matching_config_map_reference(actual: Any, expected: Any) -> bool:
    if actual == expected:
        return True
    if not isinstance(actual, list) or not isinstance(expected, list):
        return False
    if len(actual) != 1 or len(expected) != 1:
        return False
    actual_reference = actual[0].get("configMapRef", {})
    expected_reference = expected[0].get("configMapRef", {})
    if set(actual[0]) != {"configMapRef"} or set(expected[0]) != {"configMapRef"}:
        return False
    actual_match = re.fullmatch(
        r"(.+-policy-search-config)-[a-z0-9]{10}", actual_reference.get("name", "")
    )
    expected_match = re.fullmatch(
        r"(.+-policy-search-config)-[a-z0-9]{10}", expected_reference.get("name", "")
    )
    return (
        actual_match is not None
        and expected_match is not None
        and actual_match.group(1) == expected_match.group(1)
        and actual_reference.get("optional") in (None, False)
        and expected_reference.get("optional") in (None, False)
    )


def matching_named_entries(actual: Any, expected: Any) -> bool:
    if actual == expected:
        return True
    if not isinstance(actual, list) or not isinstance(expected, list):
        return False
    actual_names = [item.get("name") for item in actual if isinstance(item, dict)]
    expected_names = [item.get("name") for item in expected if isinstance(item, dict)]
    if (
        len(actual_names) != len(actual)
        or len(expected_names) != len(expected)
        or any(not isinstance(name, str) for name in actual_names + expected_names)
        or len(set(actual_names)) != len(actual_names)
        or len(set(expected_names)) != len(expected_names)
    ):
        return False
    return sorted(actual, key=lambda item: item["name"]) == sorted(
        expected, key=lambda item: item["name"]
    )


def matching_echo_predecessor_environment(actual: Any, expected: Any) -> bool:
    """Accept the reviewed Echo environment before its first query rollout only."""
    if matching_named_entries(actual, expected):
        return True
    if not isinstance(actual, list) or not isinstance(expected, list):
        return False
    query_only = {
        "ELASTICSEARCH_USERNAME",
        "ELASTICSEARCH_PASSWORD",
        "ELASTICSEARCH_WRITER_USERNAME",
        "ELASTICSEARCH_WRITER_PASSWORD",
    }
    predecessor = [item for item in expected if item.get("name") not in query_only]
    return matching_named_entries(actual, predecessor)


def staged_image_matches(environment: str, image: str) -> bool:
    if environment == "local-dev":
        return image == "insurance-hub-policy-search-api:1.0.0-local.000000000000"
    return bool(
        re.fullmatch(
            r"ghcr\.io/igor-baiborodine/insurance-hub/policy-search-api:1\.[0-9]+\.[0-9]+",
            image,
        )
    )


def render(environment: str) -> str:
    root = ROOT / f"k8s/overlays/{environment}/svc/policy-search"
    return run([os.environ.get("KUBECTL", "kubectl"), "kustomize", str(root)])


def expected_kafka_egress(environment: str) -> dict[str, Any]:
    data_namespace = "local-dev-all" if environment == "local-dev" else "qa-data"
    return {
        "to": [
            {
                "namespaceSelector": {
                    "matchLabels": {"kubernetes.io/metadata.name": data_namespace}
                },
                "podSelector": {
                    "matchLabels": {
                        "strimzi.io/cluster": f"{environment}-kafka",
                        "strimzi.io/component-type": "kafka",
                    }
                },
            }
        ],
        "ports": [{"port": 9093, "protocol": "TCP"}],
    }


def validate_render(
    environment: str,
    text: str,
) -> list[dict[str, Any]]:
    items = documents(text)
    require(items, "overlay rendered no resources")
    require(
        EXPECTED_KINDS <= {item.get("kind") for item in items}, "required resource kind is missing"
    )
    require(
        {item.get("kind") for item in items} <= EXPECTED_KINDS,
        "overlay owns an unsupported resource kind",
    )
    names = [item.get("metadata", {}).get("name", "") for item in items]
    prefix = environment + "-"
    require(all(name.startswith(prefix) for name in names), "resource prefix differs")
    require(not any(name.endswith("-legacy") for name in names), "Go overlay owns a Java resource")
    deployments = [item for item in items if item.get("kind") == "Deployment"]
    require(len(deployments) == 1, "overlay must own exactly one Deployment")
    deployment = deployments[0]
    expected_namespace = "local-dev-all" if environment == "local-dev" else "qa-svc"
    require(
        deployment.get("metadata", {}).get("namespace") == expected_namespace, "namespace differs"
    )
    spec = deployment["spec"]
    require(spec.get("replicas") == 1, "replica count differs")
    require(spec.get("minReadySeconds") == 5, "minimum readiness differs")
    require(spec.get("progressDeadlineSeconds") == 180, "progress deadline differs")
    require(
        spec.get("strategy", {}).get("rollingUpdate", {}).get("maxUnavailable") == 0,
        "rolling availability differs",
    )
    pod = spec["template"]
    labels = pod["metadata"].get("labels", {})
    require(labels.get("app.kubernetes.io/name") == "policy-search-api", "pod identity differs")
    containers = pod["spec"].get("containers", [])
    require(
        len(containers) == 1 and containers[0].get("name") == "policy-search-api",
        "container identity differs",
    )
    container = containers[0]
    image = container.get("image", "")
    require(staged_image_matches(environment, image), "staged image identity differs")
    require(
        container.get("resources")
        == {
            "requests": {"cpu": "100m", "memory": "64Mi"},
            "limits": {"cpu": "300m", "memory": "256Mi"},
        },
        "container resources differ",
    )
    security = container.get("securityContext", {})
    require(
        security.get("allowPrivilegeEscalation") is False
        and security.get("readOnlyRootFilesystem") is True
        and security.get("capabilities", {}).get("drop") == ["ALL"],
        "container restrictions differ",
    )
    pod_spec = pod["spec"]
    require(pod_spec.get("automountServiceAccountToken") is False, "service-account token enabled")
    require(pod_spec.get("enableServiceLinks") is False, "service links enabled")
    require(pod_spec.get("terminationGracePeriodSeconds") == 30, "termination grace differs")
    volumes = pod_spec.get("volumes", [])
    kafka_volume = {
        "name": "kafka-ca",
        "secret": {
            "secretName": f"{environment}-policy-search-kafka-ca",
            "items": [{"key": "ca.crt", "path": "ca.crt"}],
            "defaultMode": 0o444,
        },
    }
    kafka_mount = {
        "name": "kafka-ca",
        "mountPath": "/etc/policy-search/kafka",
        "readOnly": True,
    }
    if environment == "local-dev":
        require(
            volumes == [kafka_volume],
            "local-dev volumes differ",
        )
        require(
            container.get("volumeMounts", []) == [kafka_mount],
            "local-dev mounts differ",
        )
    else:
        expected_volumes = [
            {
                "name": "elasticsearch-ca",
                "secret": {
                    "secretName": "qa-policy-search-elasticsearch-ca",
                    "items": [{"key": "ca.crt", "path": "ca.crt"}],
                    "defaultMode": 0o444,
                },
            }
        ]
        expected_mounts = [
            {
                "name": "elasticsearch-ca",
                "mountPath": "/etc/policy-search/elasticsearch",
                "readOnly": True,
            }
        ]
        expected_volumes.append(kafka_volume)
        expected_mounts.append(kafka_mount)
        require(
            sorted(volumes, key=lambda item: item["name"])
            == sorted(expected_volumes, key=lambda item: item["name"]),
            "QA Elasticsearch CA volume differs",
        )
        require(
            sorted(container.get("volumeMounts", []), key=lambda item: item["name"])
            == sorted(expected_mounts, key=lambda item: item["name"]),
            "QA Elasticsearch CA mount differs",
        )
    env = {item.get("name"): item for item in container.get("env", [])}
    expected_secret = f"{environment}-policy-search-elasticsearch"
    for name, key in (
        ("ELASTICSEARCH_USERNAME", "username"),
        ("ELASTICSEARCH_PASSWORD", "password"),
    ):
        require(
            env.get(name, {}).get("valueFrom", {}).get("secretKeyRef")
            == {"name": expected_secret, "key": key},
            f"{name} Secret reference differs",
        )
    writer_secret = f"{environment}-policy-search-elasticsearch-writer"
    for name, key in (
        ("ELASTICSEARCH_WRITER_USERNAME", "username"),
        ("ELASTICSEARCH_WRITER_PASSWORD", "password"),
    ):
        require(
            env.get(name, {}).get("valueFrom", {}).get("secretKeyRef")
            == {"name": writer_secret, "key": key},
            f"{name} event Secret reference differs",
        )
    for name, path, period, failures in (
        ("startupProbe", "/livez", 2, 30),
        ("livenessProbe", "/livez", 10, 3),
        ("readinessProbe", "/readyz", 5, 2),
    ):
        probe = container.get(name, {})
        require(
            probe.get("httpGet", {}).get("path") == path
            and probe.get("httpGet", {}).get("port") == "management"
            and probe.get("periodSeconds") == period
            and probe.get("timeoutSeconds") == 2
            and probe.get("failureThreshold") == failures,
            f"{name} differs",
        )
    services = [item for item in items if item.get("kind") == "Service"]
    require(len(services) == 2, "overlay must own business and management Services")
    require(
        all(item.get("spec", {}).get("type") == "ClusterIP" for item in services),
        "Service exposure differs",
    )
    policies = [item for item in items if item.get("kind") == "NetworkPolicy"]
    require(len(policies) == 1, "overlay must own one NetworkPolicy")
    policy = policies[0]["spec"]
    encoded_policy = json.dumps(policy, sort_keys=True)
    require(BUSINESS_CLIENT_LABEL in encoded_policy, "business caller label is missing")
    require(MANAGEMENT_CLIENT_LABEL in encoded_policy, "management caller label is missing")
    peer_selectors = [
        peer.get("podSelector", {}).get("matchLabels", {})
        for rule in policy.get("ingress", [])
        for peer in rule.get("from", [])
    ]
    require(
        peer_selectors
        == [
            {BUSINESS_CLIENT_LABEL: "true"},
            {MANAGEMENT_CLIENT_LABEL: "true"},
        ],
        "caller selectors differ",
    )
    expected_data_namespace = "local-dev-all" if environment == "local-dev" else "qa-data"
    expected_elasticsearch = f"{environment}-elasticsearch"
    require(
        "kube-system" in encoded_policy
        and expected_data_namespace in encoded_policy
        and expected_elasticsearch in encoded_policy,
        "DNS or Elasticsearch egress differs",
    )
    if environment == "qa":
        require(
            "qa-monitoring" in encoded_policy and "qa-alloy" in encoded_policy,
            "QA Alloy egress differs",
        )
    config_maps = [item for item in items if item.get("kind") == "ConfigMap"]
    require(len(config_maps) == 1, "overlay must own one ConfigMap")
    data = config_maps[0].get("data", {})
    required_data = {
        "SERVICE_NAME": "policy-search-service",
        "HTTP_ADDR": "0.0.0.0:8080",
        "GRPC_ADDR": "0.0.0.0:9090",
        "HEALTH_ADDR": "0.0.0.0:8081",
        "REQUEST_TIMEOUT": "8s",
        "SHUTDOWN_TIMEOUT": "10s",
        "HTTP_MAX_HEADER_BYTES": "16384",
        "HTTP_MAX_BODY_BYTES": "4096",
        "GRPC_MAX_RECV_BYTES": "1048576",
        "GRPC_MAX_SEND_BYTES": "8388608",
        "ELASTICSEARCH_INDEX": "policy-views",
        "ELASTICSEARCH_TIMEOUT": "5s",
        "EVENT_WORKERS_ENABLED": "true",
        "PROBE_TIMEOUT": "30s",
        "KAFKA_BROKERS": (
            "local-dev-kafka-kafka-bootstrap:9093"
            if environment == "local-dev"
            else "qa-kafka-kafka-bootstrap.qa-data.svc:9093"
        ),
        "KAFKA_GROUP": "policy-search-service",
        "KAFKA_REGISTERED_TOPIC": "policy-registered",
        "KAFKA_TERMINATED_TOPIC": "policy-terminated",
        "KAFKA_CA_FILE": "/etc/policy-search/kafka/ca.crt",
    }
    require(
        all(data.get(key) == value for key, value in required_data.items()),
        "runtime budgets differ",
    )
    require(
        "OTEL_EXPORTER_OTLP_TIMEOUT" not in data,
        "exporter timeout must use the application default",
    )
    if environment == "local-dev":
        require(
            data.get("OTEL_ENABLED") == "false"
            and data.get("ELASTICSEARCH_URL")
            == "http://local-dev-elasticsearch-es-http.local-dev-all.svc:9200"
            and "ELASTICSEARCH_CA_FILE" not in data,
            "local dependency configuration differs",
        )
    else:
        require(
            data.get("OTEL_ENABLED") == "true"
            and data.get("OTEL_EXPORTER_OTLP_ENDPOINT")
            == "http://qa-alloy.qa-monitoring.svc.cluster.local:4317"
            and bool(re.fullmatch(r"git-[0-9a-f]{12}", data.get("SERVICE_VERSION", ""))),
            "QA telemetry identity differs",
        )
        require(
            data.get("ELASTICSEARCH_URL") == "https://qa-elasticsearch-es-http.qa-data.svc:9200"
            and data.get("ELASTICSEARCH_CA_FILE") == "/etc/policy-search/elasticsearch/ca.crt",
            "QA dependency configuration differs",
        )
        require(
            labels.get("environment") == "qa"
            and labels.get("app.kubernetes.io/version") == data.get("SERVICE_VERSION"),
            "QA Pod and telemetry versions differ",
        )
    encoded_items = json.dumps(items).lower()
    require(
        policy.get("egress", [])[-1:] == [expected_kafka_egress(environment)],
        "Kafka egress differs",
    )
    require("kafka" in encoded_items, "Kafka dependency is missing")
    return items


class Target:
    """Exact configured target selected before any cluster operation."""

    def __init__(self) -> None:
        self.service = SERVICE
        self.record = service_record(ROOT, SERVICE)
        self.environment = os.environ.get("DEPLOY_ENV", "")
        require(self.environment in self.record.get("environments", []), "DEPLOY_ENV is invalid")
        self.configuration = target_record(ROOT, self.record, self.environment)
        expected = {
            "DEPLOY_CONTEXT": self.configuration["context"],
            "DEPLOY_SVC_NS": self.configuration["namespace"],
            "DEPLOY_DATA_NS": self.configuration["dataNamespace"],
        }
        for name, value in expected.items():
            require(os.environ.get(name, "") == value, f"{name} differs from the catalog")
        self.context = expected["DEPLOY_CONTEXT"]
        self.namespace = expected["DEPLOY_SVC_NS"]
        self.data_namespace = expected["DEPLOY_DATA_NS"]
        self.deployment = f"{self.environment}-policy-search-api"
        self.business = f"{self.environment}-policy-search-api"
        self.management = f"{self.environment}-policy-search-api-management"
        self.kubectl_binary = os.environ.get("KUBECTL", "kubectl")

    def kubectl(
        self,
        *args: str,
        input_text: str | None = None,
        timeout: int = 30,
    ) -> str:
        return run(
            [
                self.kubectl_binary,
                "--context",
                self.context,
                "--namespace",
                self.namespace,
                *args,
            ],
            input_text=input_text,
            timeout=timeout,
        )

    def json(self, *args: str) -> dict[str, Any]:
        output = self.kubectl(*args, "-o", "json")
        return json.loads(output or "{}")

    def coordinates(self) -> None:
        current = run([self.kubectl_binary, "config", "current-context"]).strip()
        require(current == self.context, "active Kubernetes context differs")
        namespace = self.json("get", "namespace", self.namespace)
        require(namespace.get("metadata", {}).get("name") == self.namespace, "namespace differs")

    def render_contract(
        self,
        requested_image: str = "",
    ) -> tuple[str, list[dict[str, Any]], str]:
        text = render(self.environment)
        items = validate_render(self.environment, text)
        deployment = next(item for item in items if item.get("kind") == "Deployment")
        image = deployment["spec"]["template"]["spec"]["containers"][0]["image"]
        if requested_image:
            require(self.environment == "local-dev", "DEPLOY_IMAGE_REF is direct-local only")
            require(
                bool(
                    re.fullmatch(
                        r"insurance-hub-policy-search-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}",
                        requested_image,
                    )
                ),
                "DEPLOY_IMAGE_REF differs",
            )
            source_short = requested_image.rsplit(".", 1)[1]
            version = f"git-{source_short}"
            deployment["spec"]["template"]["spec"]["containers"][0]["image"] = requested_image
            deployment["metadata"].setdefault("labels", {})["app.kubernetes.io/version"] = version
            deployment["spec"]["template"]["metadata"].setdefault("labels", {})[
                "app.kubernetes.io/version"
            ] = version
            config = next(item for item in items if item.get("kind") == "ConfigMap")
            config["data"]["SERVICE_VERSION"] = version
            text = yaml.safe_dump_all(items, explicit_start=True, sort_keys=False)
            image = requested_image
        return text, items, image

    def rendered(self) -> tuple[str, list[dict[str, Any]], str]:
        return self.render_contract(os.environ.get("DEPLOY_IMAGE_REF", ""))

    def live(self, items: list[dict[str, Any]], *, require_deployment: bool) -> None:
        desired_deployment = resource(items, "Deployment", self.deployment)
        desired_container = desired_deployment["spec"]["template"]["spec"]["containers"][0]
        desired_image = desired_container["image"]
        previous_image = os.environ.get("DEPLOY_PREVIOUS_IMAGE_REF", "")
        if previous_image:
            require(
                self.configuration["mode"] == "direct"
                and bool(
                    re.fullmatch(
                        r"insurance-hub-policy-search-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}",
                        previous_image,
                    )
                ),
                "DEPLOY_PREVIOUS_IMAGE_REF is valid only for a direct local rollout",
            )
        allowed_images = {desired_image}
        allowed_versions = {
            desired_deployment.get("metadata", {})
            .get("labels", {})
            .get("app.kubernetes.io/version"),
            next(item for item in items if item.get("kind") == "ConfigMap")
            .get("data", {})
            .get("SERVICE_VERSION"),
        }
        if previous_image and not require_deployment:
            allowed_images.add(previous_image)
            allowed_versions.add(f"git-{previous_image.rsplit('.', 1)[1]}")
        allowed_versions.discard(None)

        for desired in items:
            kind = str(desired.get("kind", ""))
            lower_kind = kind.lower()
            metadata = desired.get("metadata", {})
            name = metadata.get("name", "")
            actual = self.json("get", lower_kind, name, "--ignore-not-found")
            if not actual:
                require(not require_deployment, f"{kind}/{name} is absent")
                continue
            require(actual.get("kind") == kind, f"live {kind}/{name} kind differs")
            actual_metadata = actual.get("metadata", {})
            expected_metadata_labels = metadata.get("labels", {})
            if kind == "Deployment":
                expected_metadata_labels = {
                    key: value
                    for key, value in expected_metadata_labels.items()
                    if key != "app.kubernetes.io/version"
                }
            require(
                actual_metadata.get("name") == name
                and actual_metadata.get("namespace") == metadata.get("namespace")
                and labels_include(actual_metadata.get("labels"), expected_metadata_labels),
                f"live {kind}/{name} identity differs from rendered identity",
            )
            desired_metadata_version = metadata.get("labels", {}).get("app.kubernetes.io/version")
            if kind == "Deployment" and (desired_metadata_version or previous_image):
                actual_metadata_version = actual_metadata.get("labels", {}).get(
                    "app.kubernetes.io/version"
                )
                require(
                    actual_metadata_version in allowed_versions
                    or (not require_deployment and actual_metadata_version is None),
                    f"live Deployment/{name} version identity differs",
                )

            if kind == "ConfigMap":
                desired_data = desired.get("data", {})
                actual_data = actual.get("data", {})
                require(
                    {key: value for key, value in actual_data.items() if key != "SERVICE_VERSION"}
                    == {
                        key: value
                        for key, value in desired_data.items()
                        if key != "SERVICE_VERSION"
                    }
                    and actual_data.get("SERVICE_VERSION") in allowed_versions
                    and not actual.get("binaryData")
                    and actual.get("immutable") in (None, False),
                    f"live ConfigMap/{name} differs from rendered configuration",
                )
                continue

            if kind == "Service":
                spec = actual.get("spec", {})
                desired_spec = desired.get("spec", {})
                require(
                    spec.get("type") == "ClusterIP"
                    and spec.get("selector") == desired_spec.get("selector")
                    and spec.get("ports") == desired_spec.get("ports")
                    and not any(
                        field in spec for field in ("externalIPs", "loadBalancerIP", "externalName")
                    )
                    and all("nodePort" not in port for port in spec.get("ports", [])),
                    f"live Service/{name} has drift or external exposure",
                )
                continue

            if kind == "NetworkPolicy":
                if previous_image and not require_deployment:
                    require(
                        matching_echo_predecessor_network_policy(
                            actual.get("spec"), desired.get("spec")
                        ),
                        f"live NetworkPolicy/{name} differs from rendered policy",
                    )
                    continue
                require(
                    matching_network_policy_spec(actual.get("spec"), desired.get("spec")),
                    f"live NetworkPolicy/{name} differs from rendered policy",
                )
                continue

            require(kind == "Deployment", f"unsupported live resource kind: {kind}")
            spec = actual.get("spec", {})
            desired_spec = desired["spec"]
            require(
                spec.get("selector") == desired_spec.get("selector")
                and spec.get("minReadySeconds") == desired_spec.get("minReadySeconds")
                and spec.get("progressDeadlineSeconds")
                == desired_spec.get("progressDeadlineSeconds")
                and spec.get("strategy") == desired_spec.get("strategy")
                and spec.get("replicas")
                in ({desired_spec.get("replicas")} if require_deployment else {0, 1}),
                f"live Deployment/{name} rollout settings differ",
            )
            template = spec.get("template", {})
            desired_template = desired_spec["template"]
            template_labels = template.get("metadata", {}).get("labels", {})
            desired_labels = desired_template.get("metadata", {}).get("labels", {})
            stable_labels = {
                key: value
                for key, value in desired_labels.items()
                if key != "app.kubernetes.io/version"
                and (require_deployment or key != "environment")
            }
            require(
                labels_include(template_labels, stable_labels),
                f"live Deployment/{name} pod identity differs",
            )
            if not require_deployment and "environment" in desired_labels:
                require(
                    template_labels.get("environment") in (None, desired_labels["environment"]),
                    f"live Deployment/{name} pod environment differs",
                )
            if desired_labels.get("app.kubernetes.io/version") or previous_image:
                actual_template_version = template_labels.get("app.kubernetes.io/version")
                require(
                    actual_template_version in allowed_versions
                    or (not require_deployment and actual_template_version is None),
                    f"live Deployment/{name} pod version identity differs",
                )
            pod = template.get("spec", {})
            desired_pod = desired_template["spec"]
            require(
                pod.get("automountServiceAccountToken") is False
                and pod.get("enableServiceLinks") == desired_pod.get("enableServiceLinks")
                and pod.get("securityContext") == desired_pod.get("securityContext")
                and pod.get("terminationGracePeriodSeconds")
                == desired_pod.get("terminationGracePeriodSeconds")
                and pod.get("hostNetwork") is not True
                and pod.get("hostPID") is not True
                and pod.get("hostIPC") is not True
                and pod.get("serviceAccountName") in (None, "default")
                and not pod.get("initContainers")
                and pod.get("volumes") == desired_pod.get("volumes"),
                f"live Deployment/{name} pod security settings differ",
            )
            containers = pod.get("containers", [])
            require(
                len(containers) == 1,
                f"live Deployment/{name} has an unexpected container inventory",
            )
            container = containers[0]
            for field in (
                "name",
                "securityContext",
                "ports",
                "env",
                "envFrom",
                "resources",
                "imagePullPolicy",
                "command",
                "args",
                "workingDir",
                "volumeMounts",
                "lifecycle",
            ):
                if field == "name":
                    allowed_names = {desired_container.get(field)}
                    if not require_deployment:
                        allowed_names.add("policy-search-service")
                    require(
                        container.get(field) in allowed_names,
                        f"live Deployment/{name} container {field} differs",
                    )
                    continue
                if field == "envFrom" and not require_deployment:
                    require(
                        matching_config_map_reference(
                            container.get(field), desired_container.get(field)
                        ),
                        f"live Deployment/{name} container {field} differs",
                    )
                    continue
                if field == "env" and previous_image and not require_deployment:
                    require(
                        matching_echo_predecessor_environment(
                            container.get(field), desired_container.get(field)
                        ),
                        f"live Deployment/{name} container {field} differs",
                    )
                    continue
                if field == "env":
                    require(
                        matching_named_entries(container.get(field), desired_container.get(field)),
                        f"live Deployment/{name} container {field} differs",
                    )
                    continue
                require(
                    container.get(field) == desired_container.get(field),
                    f"live Deployment/{name} container {field} differs",
                )
            for field in ("startupProbe", "livenessProbe", "readinessProbe"):
                require(
                    matching_probe(container.get(field), desired_container.get(field)),
                    f"live Deployment/{name} container {field} differs",
                )
            require(
                container.get("image") in allowed_images,
                f"live Deployment/{name} image differs; declare DEPLOY_PREVIOUS_IMAGE_REF for a local rollout",
            )

        if require_deployment and self.configuration["mode"] == "flux":
            self.require_running_image_digest(desired_deployment)

    def require_running_image_digest(self, desired_deployment: dict[str, Any]) -> None:
        expected = os.environ.get("DEPLOY_IMAGE_DIGEST", "")
        require(
            bool(re.fullmatch(r"sha256:[0-9a-f]{64}", expected)),
            "DEPLOY_IMAGE_DIGEST is required for QA live verification",
        )
        selector = desired_deployment["spec"]["selector"]["matchLabels"]
        label_selector = ",".join(f"{key}={value}" for key, value in sorted(selector.items()))
        listing = self.json("get", "pods", "-l", label_selector)
        pods = [
            pod
            for pod in listing.get("items", [])
            if not pod.get("metadata", {}).get("deletionTimestamp")
        ]
        expected_replicas = desired_deployment["spec"]["replicas"]
        require(
            len(pods) == expected_replicas,
            "running Policy Search Pod inventory differs",
        )
        container_name = desired_deployment["spec"]["template"]["spec"]["containers"][0]["name"]
        for pod in pods:
            conditions = pod.get("status", {}).get("conditions", [])
            statuses = pod.get("status", {}).get("containerStatuses", [])
            matches = [item for item in statuses if item.get("name") == container_name]
            require(
                pod.get("status", {}).get("phase") == "Running"
                and any(
                    item.get("type") == "Ready" and item.get("status") == "True"
                    for item in conditions
                )
                and len(matches) == 1
                and (
                    matches[0].get("imageID") == expected
                    or matches[0].get("imageID", "").endswith("@" + expected)
                ),
                "running Policy Search Pod image digest differs",
            )


def check_render() -> None:
    validate_render(os.environ.get("DEPLOY_ENV", ""), render(os.environ.get("DEPLOY_ENV", "")))


def secret_and_image(target: Target, image: str) -> None:
    require(not os.environ.get("DEPLOY_SECRET"), "Policy Search accepts no deployment Secret")
    if target.configuration["mode"] == "flux":
        require(staged_image_matches(target.environment, image), "image tag differs")
        return
    require(
        bool(
            re.fullmatch(
                r"insurance-hub-policy-search-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}", image
            )
        ),
        "image tag differs",
    )
    details = json.loads(run([os.environ.get("DOCKER", "docker"), "image", "inspect", image]))
    require(len(details) == 1, "local image inspect differs")
    revision = (
        details[0].get("Config", {}).get("Labels", {}).get("org.opencontainers.image.revision", "")
    )
    require(revision[:12] == image.rsplit(".", 1)[1], "image source revision differs")


def role_snapshot(target: Target) -> dict[str, Any]:
    values: dict[str, Any] = {}
    for kind in ("deployment", "service"):
        for suffix in JAVA_NAMES:
            name = f"{target.environment}-{suffix}"
            item = target.json("get", kind, name, "--ignore-not-found")
            values[f"{kind}/{name}"] = item.get("metadata", {}).get("uid", "")
    return values


def record_snapshot(stage: str, value: dict[str, Any]) -> None:
    print(json.dumps({"stage": stage, "resources": value}, sort_keys=True))


def administration_snapshot(_target: Target) -> dict[str, Any]:
    raise DeploymentError("Policy Search has no administration owner")


def initialization_check(_target: Target) -> None:
    raise DeploymentError("Policy Search has no initialization owner")


def initialization_complete(_target: Target) -> None:
    raise DeploymentError("Policy Search has no initialization owner")


def rollout(target: Target) -> None:
    target.kubectl(
        "rollout", "status", f"deployment/{target.deployment}", "--timeout=180s", timeout=210
    )


def check_requested_route(target: Target) -> None:
    resources = run(
        [
            target.kubectl_binary,
            "--context",
            target.context,
            "api-resources",
            "--verbs=list",
            "-o",
            "name",
        ]
    )
    for resource in ("ingresses.networking.k8s.io", "httproutes.gateway.networking.k8s.io"):
        if resource not in resources.splitlines():
            continue
        listing = json.loads(
            run(
                [
                    target.kubectl_binary,
                    "--context",
                    target.context,
                    "get",
                    resource,
                    "--all-namespaces",
                    "-o",
                    "json",
                ]
            )
            or "{}"
        )
        encoded = json.dumps(listing)
        require(
            target.business not in encoded and target.management not in encoded,
            "public route references Policy Search",
        )


def _free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def _http(url: str, body: bytes | None = None) -> tuple[int, bytes]:
    headers = {"Content-Type": "application/json"} if body is not None else {}
    try:
        with urllib.request.urlopen(
            urllib.request.Request(
                url, data=body, headers=headers, method="POST" if body is not None else "GET"
            ),
            timeout=3,
        ) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def validate_deployed_search(status: int, body: bytes) -> None:
    try:
        response = json.loads(body)
    except json.JSONDecodeError as error:
        raise DeploymentError("deployed search response is malformed") from error
    policies = response.get("policies", []) if isinstance(response, dict) else None
    require(
        status == 200 and isinstance(policies, list) and bool(policies),
        "deployed search differs",
    )


def verify(target: Target, items: list[dict[str, Any]]) -> None:
    target.live(items, require_deployment=True)
    before = role_snapshot(target)
    http_port, management_port = _free_port(), _free_port()
    process = subprocess.Popen(
        [
            target.kubectl_binary,
            "--context",
            target.context,
            "--namespace",
            target.namespace,
            "port-forward",
            f"service/{target.business}",
            f"{http_port}:80",
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    management = subprocess.Popen(
        [
            target.kubectl_binary,
            "--context",
            target.context,
            "--namespace",
            target.namespace,
            "port-forward",
            f"service/{target.management}",
            f"{management_port}:8081",
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    try:
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            try:
                if _http(f"http://127.0.0.1:{management_port}/readyz") == (200, b"ready\n"):
                    break
            except OSError:
                time.sleep(0.2)
        else:
            raise DeploymentError("deployed readiness was not reachable")
        status, body = _http(f"http://127.0.0.1:{http_port}/policies?q=Synthetic%20Person")
        validate_deployed_search(status, body)
    finally:
        for child in (process, management):
            child.terminate()
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)
    require(role_snapshot(target) == before, "Java resource identity changed during verification")


def stop(target: Target, _items: list[dict[str, Any]]) -> None:
    before = role_snapshot(target)
    target.kubectl("scale", f"deployment/{target.deployment}", "--replicas=0", "--timeout=60s")
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        deployment = target.json("get", "deployment", target.deployment)
        if deployment.get("status", {}).get("replicas", 0) == 0:
            break
        time.sleep(1)
    else:
        raise DeploymentError("Policy Search stop exceeded 90 seconds")
    require(role_snapshot(target) == before, "Java resource identity changed during stop")


def validate_gateway_business_admission(environment: str) -> None:
    overlay = ROOT / f"k8s/overlays/{environment}/svc/agent-portal-gateway/legacy"
    items = documents(run([os.environ.get("KUBECTL", "kubectl"), "kustomize", str(overlay)]))
    deployment = resource(
        items,
        "Deployment",
        f"{environment}-agent-portal-gateway-legacy",
    )
    labels = deployment["spec"]["template"].get("metadata", {}).get("labels", {})
    require(
        labels.get(BUSINESS_CLIENT_LABEL) == "true",
        f"{environment} gateway lacks Policy Search business admission",
    )
    application = yaml.safe_load((overlay / "application.yml").read_text(encoding="utf-8"))
    services = application.get("micronaut", {}).get("http", {}).get("services", {})
    route = services.get("policy-search-service")
    if environment == "qa":
        require(
            route == {"urls": ["http://qa-policy-search-api.qa-svc.svc.cluster.local:80"]},
            "QA gateway Policy Search route differs",
        )
    else:
        require(route is None, "local-dev gateway Policy Search route must remain inherited")


def validate_qa_aggregate(resources: Any) -> None:
    require(isinstance(resources, list), "active QA aggregate resources differ")
    require("./policy-search" in resources, "active QA Go Policy Search overlay is missing")
    require(
        "./policy-search/legacy" not in resources,
        "retired QA Policy Search legacy overlay remains active",
    )


def main(argv: list[str] | None = None) -> int:
    arguments = sys.argv[1:] if argv is None else argv
    require(
        len(arguments) == 1 and arguments[0] in {"render", "check"}, "unsupported check operation"
    )
    environments = (
        [os.environ.get("DEPLOY_ENV", "")] if arguments[0] == "render" else ["local-dev", "qa"]
    )
    for environment in environments:
        require(environment in {"local-dev", "qa"}, "DEPLOY_ENV is invalid")
        text = render(environment)
        validate_render(environment, text)
        validate_gateway_business_admission(environment)
        if arguments[0] == "render":
            sys.stdout.write(text)
    if arguments[0] == "check":
        qa_root = yaml.safe_load((ROOT / "k8s/overlays/qa/svc/kustomization.yaml").read_text())
        validate_qa_aggregate(qa_root.get("resources"))
        print("policy-search-deployment-check: local-dev and QA renders passed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (DeploymentError, SharedDeploymentError, CommandError, OSError, ValueError) as error:
        print(f"policy-search-deployment-check: {error}", file=sys.stderr)
        raise SystemExit(1) from error
