#!/usr/bin/env python3
"""Dashboard deployment guards and service-owned verification hooks."""

from __future__ import annotations

import base64
import binascii
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

SERVICE = "dashboard"
EXPECTED_KINDS = {"ConfigMap", "Deployment", "Service", "NetworkPolicy"}
BUSINESS_CLIENT_LABEL = "insurance-hub.io/dashboard-business-client"
MANAGEMENT_CLIENT_LABEL = "insurance-hub.io/dashboard-management-client"
JAVA_NAMES = {"dashboard-api-legacy"}
DENIED_QUALIFICATION_PODS = {
    "dashboard-qualification-marker-only",
    "dashboard-qualification-marker-only-agent-denied",
    "dashboard-qualification-marker-only-grpc-denied",
    "dashboard-qualification-business-management-denied",
    "dashboard-qualification-cross-namespace-denied",
    "dashboard-qualification-cross-namespace-agent-denied",
    "dashboard-qualification-cross-namespace-grpc-denied",
}
GRPC_REACHABLE_QUALIFICATION_POD = "dashboard-qualification-business-grpc"
FORBIDDEN_TEXT = ("kafka", "postgres", "database", "insecure_skip_verify")
PROBE_IMAGE = (
    "docker.io/curlimages/curl:8.16.0@"
    "sha256:463eaf6072688fe96ac64fa623fe73e1dbe25d8ad6c34404a669ad3ce1f104b6"
)


class DeploymentError(RuntimeError):
    """Dashboard deployment state differs from the accepted contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise DeploymentError(message)


def safe_environment() -> dict[str, str]:
    allowed = ("HOME", "KUBECONFIG", "PATH", "SSL_CERT_DIR", "SSL_CERT_FILE", "TERM", "TMPDIR")
    return {name: os.environ[name] for name in allowed if name in os.environ}


def run(
    argv: list[str],
    *,
    input_text: str | None = None,
    timeout: int = 30,
    environment: dict[str, str] | None = None,
    activity: str = "Dashboard deployment command",
) -> str:
    command_environment = safe_environment()
    command_environment.update(environment or {})
    result = run_command(
        argv,
        cwd=ROOT,
        env=command_environment,
        timeout_seconds=timeout,
        teardown_seconds=10,
        activity=activity,
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
    normalized = dict(expected)
    normalized.setdefault("successThreshold", 1)
    return actual == normalized


def normalized_policy_spec(value: Any) -> Any:
    if not isinstance(value, dict):
        return value
    result = dict(value)
    for field in ("ingress", "egress"):
        if result.get(field) == []:
            result.pop(field)
    return result


def matching_reader_predecessor_config(actual: Any, expected: Any) -> bool:
    """Accept the reviewed pre-reader local ConfigMap before its first replacement only."""
    if not isinstance(actual, dict) or not isinstance(expected, dict):
        return False
    predecessor = {
        key: value
        for key, value in expected.items()
        if key not in {"ELASTICSEARCH_INDEX", "ELASTICSEARCH_TIMEOUT", "ELASTICSEARCH_URL"}
    }
    predecessor["OTEL_EXPORTER_OTLP_TIMEOUT"] = "2s"
    return actual == predecessor


def matching_reader_predecessor_environment(actual: Any, expected: Any) -> bool:
    """Accept the reviewed pre-reader container environment before replacement only."""
    if actual == expected:
        return True
    if not isinstance(actual, list) or not isinstance(expected, list):
        return False
    predecessor = [
        item
        for item in expected
        if item.get("name") not in {"ELASTICSEARCH_USERNAME", "ELASTICSEARCH_PASSWORD"}
    ]
    return actual == predecessor


def matching_reader_predecessor_network_policy(actual: Any, expected: Any) -> bool:
    """Accept the reviewed pre-reader deny-all-egress policy before replacement only."""
    if normalized_policy_spec(actual) == normalized_policy_spec(expected):
        return True
    if not isinstance(expected, dict):
        return False
    predecessor = {
        "podSelector": expected.get("podSelector"),
        "policyTypes": expected.get("policyTypes"),
        "ingress": [
            {
                "from": [{"podSelector": {"matchLabels": {BUSINESS_CLIENT_LABEL: "true"}}}],
                "ports": [
                    {"port": 8080, "protocol": "TCP"},
                    {"port": 9090, "protocol": "TCP"},
                ],
            },
            {
                "from": [{"podSelector": {"matchLabels": {MANAGEMENT_CLIENT_LABEL: "true"}}}],
                "ports": [{"port": 8081, "protocol": "TCP"}],
            },
        ],
    }
    return normalized_policy_spec(actual) == normalized_policy_spec(predecessor)


def render(environment: str) -> str:
    record = service_record(ROOT, SERVICE)
    target = target_record(ROOT, record, environment)
    return run([os.environ.get("KUBECTL", "kubectl"), "kustomize", str(ROOT / target["overlay"])])


def expected_ingress(environment: str) -> list[dict[str, Any]]:
    namespace = "local-dev-all" if environment == "local-dev" else "qa-svc"
    peer_namespace = {
        "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": namespace}}
    }
    return [
        {
            "from": [
                peer_namespace
                | {
                    "podSelector": {
                        "matchLabels": {
                            "app.kubernetes.io/name": "agent-portal-gateway-legacy",
                            "app.kubernetes.io/instance": (
                                "insurance-hub-agent-portal-gateway-legacy"
                            ),
                            "app.kubernetes.io/component": "gateway",
                            "environment": environment,
                            BUSINESS_CLIENT_LABEL: "true",
                        }
                    }
                }
            ],
            "ports": [
                {"port": 8080, "protocol": "TCP"},
                {"port": 9090, "protocol": "TCP"},
            ],
        },
        {
            "from": [
                peer_namespace
                | {
                    "podSelector": {
                        "matchLabels": {
                            "app.kubernetes.io/name": "dashboard-qualification",
                            "app.kubernetes.io/component": "probe",
                            "insurance-hub.io/owner": "issue-287",
                            BUSINESS_CLIENT_LABEL: "true",
                        }
                    }
                }
            ],
            "ports": [
                {"port": 8080, "protocol": "TCP"},
                {"port": 9090, "protocol": "TCP"},
            ],
        },
        {
            "from": [
                peer_namespace
                | {
                    "podSelector": {
                        "matchLabels": {
                            "app.kubernetes.io/name": "dashboard-qualification",
                            "app.kubernetes.io/component": "probe",
                            "insurance-hub.io/owner": "issue-287",
                            MANAGEMENT_CLIENT_LABEL: "true",
                        }
                    }
                }
            ],
            "ports": [{"port": 8081, "protocol": "TCP"}],
        },
    ]


def dns_egress() -> dict[str, Any]:
    return {
        "to": [
            {
                "namespaceSelector": {
                    "matchLabels": {"kubernetes.io/metadata.name": "kube-system"}
                },
                "podSelector": {"matchLabels": {"k8s-app": "kube-dns"}},
            }
        ],
        "ports": [
            {"port": 53, "protocol": "UDP"},
            {"port": 53, "protocol": "TCP"},
        ],
    }


def elasticsearch_egress(environment: str) -> dict[str, Any]:
    namespace = "local-dev-all" if environment == "local-dev" else "qa-data"
    return {
        "to": [
            {
                "namespaceSelector": {"matchLabels": {"kubernetes.io/metadata.name": namespace}},
                "podSelector": {
                    "matchLabels": {
                        "elasticsearch.k8s.elastic.co/cluster-name": f"{environment}-elasticsearch"
                    }
                },
            }
        ],
        "ports": [{"port": 9200, "protocol": "TCP"}],
    }


def expected_egress(environment: str) -> list[dict[str, Any]]:
    if environment == "local-dev":
        return [dns_egress(), elasticsearch_egress(environment)]
    return [
        elasticsearch_egress(environment),
        dns_egress(),
        {
            "to": [
                {
                    "namespaceSelector": {
                        "matchLabels": {"kubernetes.io/metadata.name": "qa-monitoring"}
                    },
                    "podSelector": {
                        "matchLabels": {
                            "app.kubernetes.io/name": "alloy",
                            "app.kubernetes.io/instance": "qa-alloy",
                        }
                    },
                }
            ],
            "ports": [{"port": 4317, "protocol": "TCP"}],
        },
    ]


def validate_render(environment: str, text: str) -> list[dict[str, Any]]:
    require(environment in {"local-dev", "qa"}, "deployment environment is invalid")
    lower = text.lower()
    require(
        not any(value.lower() in lower for value in FORBIDDEN_TEXT), "data dependency is forbidden"
    )
    items = documents(text)
    require(bool(items), "render produced no resources")
    require(
        {item.get("kind") for item in items} == EXPECTED_KINDS, "required resource kind differs"
    )
    require(len(items) == 5, "Dashboard resource inventory differs")
    prefix = f"{environment}-"
    expected_names = {
        "ConfigMap": {prefix + "dashboard-config"},
        "Deployment": {prefix + "dashboard-api"},
        "Service": {prefix + "dashboard-api", prefix + "dashboard-api-management"},
        "NetworkPolicy": {prefix + "dashboard-api"},
    }
    for kind, names in expected_names.items():
        actual = {
            item.get("metadata", {}).get("name") for item in items if item.get("kind") == kind
        }
        require(actual == names, f"{kind} identity differs")
    for item in items:
        metadata = item.get("metadata", {})
        require(
            metadata.get("namespace")
            in (None, f"{environment}-all" if environment == "local-dev" else "qa-svc"),
            "resource namespace differs",
        )
        require(
            labels_include(
                metadata.get("labels", {}),
                {
                    "app.kubernetes.io/name": "dashboard-api",
                    "app.kubernetes.io/part-of": "insurance-hub",
                },
            ),
            "resource labels differ",
        )

    config = resource(items, "ConfigMap", prefix + "dashboard-config")
    data = config.get("data", {})
    version = data.get("SERVICE_VERSION", "")
    require(
        bool(re.fullmatch(r"git-[0-9a-f]{12}", version)),
        "runtime version is unresolved",
    )
    expected_config = {
        "SERVICE_NAME": "dashboard-service",
        "DEPLOYMENT_ENVIRONMENT": environment,
        "SERVICE_VERSION": version,
        "HTTP_ADDR": "0.0.0.0:8080",
        "GRPC_ADDR": "0.0.0.0:9090",
        "HEALTH_ADDR": "0.0.0.0:8081",
        "LOG_LEVEL": "info",
        "REQUEST_TIMEOUT": "8s",
        "PROBE_TIMEOUT": "2s",
        "SHUTDOWN_TIMEOUT": "10s",
        "HTTP_MAX_HEADER_BYTES": "16384",
        "HTTP_MAX_BODY_BYTES": "4096",
        "HTTP_READ_HEADER_TIMEOUT": "5s",
        "HTTP_READ_TIMEOUT": "10s",
        "HTTP_WRITE_TIMEOUT": "10s",
        "HTTP_IDLE_TIMEOUT": "30s",
        "GRPC_MAX_RECV_BYTES": "1048576",
        "GRPC_MAX_SEND_BYTES": "8388608",
        "ELASTICSEARCH_URL": (
            "http://local-dev-elasticsearch-es-http.local-dev-all.svc:9200"
            if environment == "local-dev"
            else "https://qa-elasticsearch-es-http.qa-data.svc:9200"
        ),
        "ELASTICSEARCH_INDEX": "policy_stats",
        "ELASTICSEARCH_TIMEOUT": "5s",
        "OTEL_ENABLED": "false" if environment == "local-dev" else "true",
    }
    if environment == "qa":
        expected_config["OTEL_EXPORTER_OTLP_ENDPOINT"] = (
            "http://qa-alloy.qa-monitoring.svc.cluster.local:4317"
        )
        expected_config["ELASTICSEARCH_CA_FILE"] = "/etc/dashboard/elasticsearch/ca.crt"
    require(
        "OTEL_EXPORTER_OTLP_TIMEOUT" not in data,
        "exporter timeout must use the application default",
    )
    require(data == expected_config, "runtime configuration differs")

    deployment = resource(items, "Deployment", prefix + "dashboard-api")
    spec = deployment.get("spec", {})
    require(
        spec.get("replicas") == 1
        and spec.get("minReadySeconds") == 5
        and spec.get("progressDeadlineSeconds") == 180
        and spec.get("strategy")
        == {"type": "RollingUpdate", "rollingUpdate": {"maxSurge": 1, "maxUnavailable": 0}},
        "rollout settings differ",
    )
    pod = spec.get("template", {}).get("spec", {})
    require(
        pod.get("automountServiceAccountToken") is False
        and pod.get("enableServiceLinks") is False
        and pod.get("terminationGracePeriodSeconds") == 30
        and pod.get("securityContext")
        == {
            "runAsNonRoot": True,
            "runAsUser": 65532,
            "runAsGroup": 65532,
            "fsGroup": 65532,
            "seccompProfile": {"type": "RuntimeDefault"},
        }
        and pod.get("volumes", [])
        == (
            []
            if environment == "local-dev"
            else [
                {
                    "name": "elasticsearch-ca",
                    "secret": {
                        "secretName": "qa-dashboard-elasticsearch-ca",
                        "items": [{"key": "ca.crt", "path": "ca.crt"}],
                        "defaultMode": 292,
                    },
                }
            ]
        )
        and not pod.get("initContainers"),
        "Pod security or data boundary differs",
    )
    containers = pod.get("containers", [])
    require(len(containers) == 1, "container inventory differs")
    container = containers[0]
    require(
        container.get("name") == "dashboard-api"
        and container.get("imagePullPolicy")
        == ("Never" if environment == "local-dev" else "Always")
        and container.get("securityContext")
        == {
            "allowPrivilegeEscalation": False,
            "readOnlyRootFilesystem": True,
            "capabilities": {"drop": ["ALL"]},
        }
        and container.get("resources")
        == {
            "requests": {"cpu": "100m", "memory": "64Mi"},
            "limits": {"cpu": "300m", "memory": "256Mi"},
        }
        and container.get("volumeMounts", [])
        == (
            []
            if environment == "local-dev"
            else [
                {
                    "name": "elasticsearch-ca",
                    "mountPath": "/etc/dashboard/elasticsearch",
                    "readOnly": True,
                }
            ]
        )
        and container.get("envFrom") == [{"configMapRef": {"name": prefix + "dashboard-config"}}],
        "container security or resources differ",
    )
    ports = {(item.get("name"), item.get("containerPort")) for item in container.get("ports", [])}
    require(
        ports == {("http", 8080), ("grpc", 9090), ("management", 8081)}, "container ports differ"
    )
    env = {item.get("name"): item for item in container.get("env", [])}
    require(
        env.get("SERVICE_INSTANCE_ID", {}).get("valueFrom", {}).get("fieldRef", {}).get("fieldPath")
        == "metadata.uid"
        and env.get("ELASTICSEARCH_USERNAME", {}).get("valueFrom", {}).get("secretKeyRef")
        == {"name": f"{environment}-dashboard-elasticsearch", "key": "username"}
        and env.get("ELASTICSEARCH_PASSWORD", {}).get("valueFrom", {}).get("secretKeyRef")
        == {"name": f"{environment}-dashboard-elasticsearch", "key": "password"}
        and set(env)
        == {
            "SERVICE_INSTANCE_ID",
            "ELASTICSEARCH_USERNAME",
            "ELASTICSEARCH_PASSWORD",
        },
        "runtime identity environment differs",
    )
    probes = {
        "startupProbe": ({"path": "/livez", "port": "management", "scheme": "HTTP"}, 2, 2, 30),
        "livenessProbe": ({"path": "/livez", "port": "management", "scheme": "HTTP"}, 10, 2, 3),
        "readinessProbe": ({"path": "/readyz", "port": "management", "scheme": "HTTP"}, 5, 2, 2),
    }
    for name, (http_get, period, timeout, failures) in probes.items():
        require(
            container.get(name)
            == {
                "httpGet": http_get,
                "periodSeconds": period,
                "timeoutSeconds": timeout,
                "failureThreshold": failures,
            },
            f"{name} differs",
        )

    business = resource(items, "Service", prefix + "dashboard-api")
    management = resource(items, "Service", prefix + "dashboard-api-management")
    require(
        business.get("spec", {}).get("type") == "ClusterIP"
        and business.get("spec", {}).get("ports")
        == [
            {"name": "http", "port": 80, "protocol": "TCP", "targetPort": "http"},
            {"name": "grpc", "port": 9090, "protocol": "TCP", "targetPort": "grpc"},
        ]
        and management.get("spec", {}).get("type") == "ClusterIP"
        and management.get("spec", {}).get("ports")
        == [{"name": "management", "port": 8081, "protocol": "TCP", "targetPort": "management"}],
        "private Service exposure differs",
    )
    require(
        business.get("spec", {}).get("selector")
        == management.get("spec", {}).get("selector")
        == spec.get("selector", {}).get("matchLabels"),
        "Service selector differs",
    )

    policy = resource(items, "NetworkPolicy", prefix + "dashboard-api")
    policy_spec = policy.get("spec", {})
    require(policy_spec.get("policyTypes") == ["Ingress", "Egress"], "NetworkPolicy types differ")
    require(
        policy_spec.get("ingress", []) == expected_ingress(environment),
        "NetworkPolicy admission differs",
    )
    require(
        policy_spec.get("egress", []) == expected_egress(environment),
        "NetworkPolicy egress differs",
    )
    return items


class Target:
    """Exact catalog target selected before cluster access."""

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
        self.deployment = f"{self.environment}-dashboard-api"
        self.business = f"{self.environment}-dashboard-api"
        self.management = f"{self.environment}-dashboard-api-management"
        self.kubectl_binary = os.environ.get("KUBECTL", "kubectl")

    def kubectl(self, *args: str, input_text: str | None = None, timeout: int = 30) -> str:
        return run(
            [self.kubectl_binary, "--context", self.context, "--namespace", self.namespace, *args],
            input_text=input_text,
            timeout=timeout,
        )

    def json(self, *args: str) -> dict[str, Any]:
        return json.loads(self.kubectl(*args, "-o", "json") or "{}")

    def data_json(self, *args: str) -> dict[str, Any]:
        return json.loads(
            run(
                [
                    self.kubectl_binary,
                    "--context",
                    self.context,
                    "--namespace",
                    self.data_namespace,
                    *args,
                    "-o",
                    "json",
                ]
            )
            or "{}"
        )

    def namespace_kubectl(
        self,
        namespace: str,
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
                namespace,
                *args,
            ],
            input_text=input_text,
            timeout=timeout,
        )

    def namespace_json(self, namespace: str, *args: str) -> dict[str, Any]:
        return json.loads(self.namespace_kubectl(namespace, *args, "-o", "json") or "{}")

    def coordinates(self) -> None:
        require(
            run([self.kubectl_binary, "config", "current-context"]).strip() == self.context,
            "active Kubernetes context differs",
        )
        namespace = self.json("get", "namespace", self.namespace)
        require(namespace.get("metadata", {}).get("name") == self.namespace, "namespace differs")
        if self.data_namespace != self.namespace:
            data_namespace = self.data_json("get", "namespace", self.data_namespace)
            require(
                data_namespace.get("metadata", {}).get("name") == self.data_namespace,
                "data namespace differs",
            )

    def rendered(self) -> tuple[str, list[dict[str, Any]], str]:
        text = render(self.environment)
        items = validate_render(self.environment, text)
        deployment = resource(items, "Deployment", self.deployment)
        image = deployment["spec"]["template"]["spec"]["containers"][0]["image"]
        requested = os.environ.get("DEPLOY_IMAGE_REF", "")
        if requested:
            require(self.environment == "local-dev", "DEPLOY_IMAGE_REF is direct-local only")
            require(
                bool(
                    re.fullmatch(
                        r"insurance-hub-dashboard-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}",
                        requested,
                    )
                ),
                "DEPLOY_IMAGE_REF differs",
            )
            version = "git-" + requested.rsplit(".", 1)[1]
            deployment["spec"]["template"]["spec"]["containers"][0]["image"] = requested
            deployment["metadata"].setdefault("labels", {})["app.kubernetes.io/version"] = version
            deployment["spec"]["template"]["metadata"].setdefault("labels", {})[
                "app.kubernetes.io/version"
            ] = version
            resource(items, "ConfigMap", f"{self.environment}-dashboard-config")["data"][
                "SERVICE_VERSION"
            ] = version
            text = yaml.safe_dump_all(items, explicit_start=True, sort_keys=False)
            image = requested
        return text, items, image

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
                        r"insurance-hub-dashboard-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}",
                        previous_image,
                    )
                ),
                "DEPLOY_PREVIOUS_IMAGE_REF is valid only for a direct local rollout",
            )
        desired_config = resource(items, "ConfigMap", f"{self.environment}-dashboard-config")
        allowed_images = {desired_image}
        allowed_versions = {
            desired_deployment.get("metadata", {})
            .get("labels", {})
            .get("app.kubernetes.io/version"),
            desired_config.get("data", {}).get("SERVICE_VERSION"),
        }
        if previous_image and not require_deployment:
            allowed_images.add(previous_image)
            allowed_versions.add("git-" + previous_image.rsplit(".", 1)[1])
        allowed_versions.discard(None)

        for desired in items:
            kind = str(desired.get("kind", ""))
            metadata = desired.get("metadata", {})
            name = metadata.get("name", "")
            actual = self.json("get", kind.lower(), name, "--ignore-not-found")
            if not actual:
                require(not require_deployment, f"{kind}/{name} is absent")
                continue
            actual_metadata = actual.get("metadata", {})
            expected_labels = metadata.get("labels", {})
            if kind == "Deployment":
                expected_labels = {
                    key: value
                    for key, value in expected_labels.items()
                    if key != "app.kubernetes.io/version"
                }
            require(
                actual.get("kind") == kind
                and actual_metadata.get("name") == name
                and actual_metadata.get("namespace") == metadata.get("namespace") == self.namespace
                and labels_include(actual_metadata.get("labels"), expected_labels)
                and not actual_metadata.get("ownerReferences")
                and not actual_metadata.get("deletionTimestamp"),
                f"live {kind}/{name} identity differs from rendered identity",
            )
            if kind == "Deployment" and metadata.get("labels", {}).get("app.kubernetes.io/version"):
                require(
                    actual_metadata.get("labels", {}).get("app.kubernetes.io/version")
                    in allowed_versions,
                    f"live Deployment/{name} version identity differs",
                )

            if kind == "ConfigMap":
                desired_data = desired.get("data", {})
                actual_data = actual.get("data", {})
                actual_stable_data = {
                    key: value for key, value in actual_data.items() if key != "SERVICE_VERSION"
                }
                desired_stable_data = {
                    key: value for key, value in desired_data.items() if key != "SERVICE_VERSION"
                }
                require(
                    (
                        actual_stable_data == desired_stable_data
                        or (
                            previous_image
                            and not require_deployment
                            and matching_reader_predecessor_config(
                                actual_stable_data, desired_stable_data
                            )
                        )
                    )
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
                require(
                    (
                        normalized_policy_spec(actual.get("spec"))
                        == normalized_policy_spec(desired.get("spec"))
                        or (
                            previous_image
                            and not require_deployment
                            and matching_reader_predecessor_network_policy(
                                actual.get("spec"), desired.get("spec")
                            )
                        )
                    ),
                    f"live NetworkPolicy/{name} differs from rendered policy",
                )
                continue

            require(kind == "Deployment", f"unsupported live resource kind: {kind}")
            desired_spec = desired.get("spec", {})
            actual_spec = actual.get("spec", {})
            require(
                actual_spec.get("selector") == desired_spec.get("selector")
                and actual_spec.get("strategy") == desired_spec.get("strategy")
                and actual_spec.get("minReadySeconds") == desired_spec.get("minReadySeconds")
                and actual_spec.get("progressDeadlineSeconds")
                == desired_spec.get("progressDeadlineSeconds")
                and actual_spec.get("replicas")
                in ({desired_spec.get("replicas")} if require_deployment else {0, 1}),
                f"live Deployment/{name} rollout settings differ",
            )
            desired_template = desired_spec.get("template", {})
            template = actual_spec.get("template", {})
            desired_template_labels = desired_template.get("metadata", {}).get("labels", {})
            stable_template_labels = {
                key: value
                for key, value in desired_template_labels.items()
                if key != "app.kubernetes.io/version"
            }
            require(
                labels_include(
                    template.get("metadata", {}).get("labels"),
                    stable_template_labels,
                ),
                f"live Deployment/{name} Pod identity differs",
            )
            if desired_template_labels.get("app.kubernetes.io/version"):
                require(
                    template.get("metadata", {}).get("labels", {}).get("app.kubernetes.io/version")
                    in allowed_versions,
                    f"live Deployment/{name} Pod version identity differs",
                )
            desired_pod = desired_template.get("spec", {})
            pod = template.get("spec", {})
            require(
                pod.get("automountServiceAccountToken") is False
                and pod.get("enableServiceLinks") == desired_pod.get("enableServiceLinks")
                and pod.get("terminationGracePeriodSeconds")
                == desired_pod.get("terminationGracePeriodSeconds")
                and pod.get("securityContext") == desired_pod.get("securityContext")
                and pod.get("hostNetwork") is not True
                and pod.get("hostPID") is not True
                and pod.get("hostIPC") is not True
                and pod.get("serviceAccountName") in (None, "default")
                and not pod.get("initContainers")
                and pod.get("volumes", []) == desired_pod.get("volumes", []),
                f"live Deployment/{name} Pod security settings differ",
            )
            desired_containers = desired_pod.get("containers", [])
            containers = pod.get("containers", [])
            require(
                len(desired_containers) == len(containers) == 1,
                f"live Deployment/{name} has an unexpected container inventory",
            )
            desired_container = desired_containers[0]
            container = containers[0]
            for field in (
                "name",
                "image",
                "imagePullPolicy",
                "securityContext",
                "ports",
                "env",
                "envFrom",
                "resources",
                "command",
                "args",
                "workingDir",
                "volumeMounts",
                "lifecycle",
            ):
                if field == "image":
                    require(
                        container.get(field) in allowed_images,
                        f"live Deployment/{name} container image differs",
                    )
                elif field == "name" and previous_image and not require_deployment:
                    require(
                        container.get(field) in {desired_container.get(field), "dashboard-service"},
                        f"live Deployment/{name} container name differs",
                    )
                elif field == "env" and previous_image and not require_deployment:
                    require(
                        matching_reader_predecessor_environment(
                            container.get(field), desired_container.get(field)
                        ),
                        f"live Deployment/{name} container env differs",
                    )
                elif field == "imagePullPolicy" and previous_image and not require_deployment:
                    require(
                        container.get(field) in {desired_container.get(field), "IfNotPresent"},
                        f"live Deployment/{name} container imagePullPolicy differs",
                    )
                else:
                    require(
                        container.get(field) == desired_container.get(field),
                        f"live Deployment/{name} container {field} differs",
                    )
            for field in ("startupProbe", "livenessProbe", "readinessProbe"):
                require(
                    matching_probe(container.get(field), desired_container.get(field)),
                    f"live Deployment/{name} container {field} differs",
                )

        if require_deployment and self.configuration["mode"] == "flux":
            self.require_running_image_digest(resource(items, "Deployment", self.deployment))

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
        require(len(pods) == expected_replicas, "running Dashboard Pod inventory differs")
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
                "running Dashboard Pod image digest differs",
            )


def check_render() -> None:
    environment = os.environ.get("DEPLOY_ENV", "")
    validate_render(environment, render(environment))


def secret_values(item: dict[str, Any], name: str, keys: set[str]) -> dict[str, bytes]:
    metadata = item.get("metadata", {})
    data = item.get("data", {})
    require(
        item.get("kind") == "Secret"
        and metadata.get("name") == name
        and isinstance(data, dict)
        and set(data) == keys,
        f"Secret/{name} keys or identity differ",
    )
    values: dict[str, bytes] = {}
    try:
        for key, encoded in data.items():
            require(isinstance(encoded, str) and bool(encoded), f"Secret/{name} data is empty")
            values[key] = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError) as error:
        raise DeploymentError(f"Secret/{name} data is malformed") from error
    require(all(values.values()), f"Secret/{name} data is empty")
    return values


def secret_value(item: dict[str, Any], name: str, key: str) -> bytes:
    metadata = item.get("metadata", {})
    data = item.get("data", {})
    require(
        item.get("kind") == "Secret"
        and metadata.get("name") == name
        and isinstance(data, dict)
        and key in data,
        f"Secret/{name} key or identity differs",
    )
    encoded = data[key]
    try:
        require(isinstance(encoded, str) and bool(encoded), f"Secret/{name} data is empty")
        value = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError) as error:
        raise DeploymentError(f"Secret/{name} data is malformed") from error
    require(bool(value), f"Secret/{name} data is empty")
    return value


def workload_dependencies(target: Target) -> None:
    reader_name = f"{target.environment}-dashboard-elasticsearch"
    reader = target.json("get", "secret", reader_name, "--ignore-not-found")
    require(
        reader.get("metadata", {}).get("namespace") == target.namespace,
        f"Secret/{reader_name} namespace differs",
    )
    credentials = secret_values(reader, reader_name, {"username", "password"})
    require(
        credentials["username"] == b"go_dashboard_total_sales_reader"
        and len(credentials["password"]) >= 32,
        f"Secret/{reader_name} reader identity differs",
    )

    service_name = f"{target.environment}-elasticsearch-es-http"
    service = target.data_json("get", "service", service_name, "--ignore-not-found")
    service_spec = service.get("spec", {})
    require(
        service.get("kind") == "Service"
        and service.get("metadata", {}).get("name") == service_name
        and service.get("metadata", {}).get("namespace") == target.data_namespace
        and service_spec.get("type") == "ClusterIP"
        and service_spec.get("selector", {}).get("elasticsearch.k8s.elastic.co/cluster-name")
        == f"{target.environment}-elasticsearch"
        and any(
            port.get("port") == 9200 and port.get("protocol", "TCP") == "TCP"
            for port in service_spec.get("ports", [])
        ),
        f"Service/{service_name} dependency identity differs",
    )

    if target.environment == "local-dev":
        return
    source_name = "qa-elasticsearch-es-http-certs-public"
    source = target.data_json("get", "secret", source_name, "--ignore-not-found")
    require(
        source.get("metadata", {}).get("namespace") == target.data_namespace,
        f"Secret/{source_name} namespace differs",
    )
    source_ca = secret_value(source, source_name, "ca.crt")
    workload_name = "qa-dashboard-elasticsearch-ca"
    workload = target.json("get", "secret", workload_name, "--ignore-not-found")
    require(
        workload.get("metadata", {}).get("namespace") == target.namespace,
        f"Secret/{workload_name} namespace differs",
    )
    workload_ca = secret_values(workload, workload_name, {"ca.crt"})["ca.crt"]
    require(workload_ca == source_ca, "Dashboard Elasticsearch CA differs from the ECK public CA")


def secret_and_image(target: Target, image: str) -> None:
    require(not os.environ.get("DEPLOY_SECRET"), "Dashboard accepts no deployment Secret")
    workload_dependencies(target)
    if target.configuration["mode"] == "flux":
        require(
            bool(
                re.fullmatch(
                    r"ghcr\.io/igor-baiborodine/insurance-hub/dashboard-api:1\.[0-9]+\.[0-9]+",
                    image,
                )
            ),
            "QA image tag differs",
        )
        return
    require(
        bool(
            re.fullmatch(
                r"insurance-hub-dashboard-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}", image
            )
        ),
        "local image tag differs",
    )
    details = json.loads(run([os.environ.get("DOCKER", "docker"), "image", "inspect", image]))
    require(len(details) == 1, "local image inspect differs")
    revision = (
        details[0].get("Config", {}).get("Labels", {}).get("org.opencontainers.image.revision", "")
    )
    require(revision[:12] == image.rsplit(".", 1)[1], "image source revision differs")


def role_snapshot(target: Target) -> dict[str, Any]:
    values: dict[str, Any] = {}
    expected_uids = {
        "deployment": os.environ.get("DEPLOY_JAVA_DEPLOYMENT_UID", ""),
        "service": os.environ.get("DEPLOY_JAVA_SERVICE_UID", ""),
    }
    for kind in ("deployment", "service"):
        for suffix in JAVA_NAMES:
            name = f"{target.environment}-{suffix}"
            item = target.json("get", kind, name, "--ignore-not-found")
            metadata = item.get("metadata", {})
            uid = metadata.get("uid", "")
            require(
                item.get("kind") == kind.title()
                and metadata.get("name") == name
                and metadata.get("namespace") == target.namespace
                and isinstance(uid, str)
                and bool(uid)
                and uid == expected_uids[kind]
                and not metadata.get("deletionTimestamp")
                and labels_include(
                    metadata.get("labels"),
                    {
                        "app.kubernetes.io/name": suffix,
                        "app.kubernetes.io/instance": f"insurance-hub-{suffix}",
                        "app.kubernetes.io/component": "api",
                    },
                ),
                f"authoritative Java {kind}/{name} identity differs",
            )
            if kind == "deployment":
                replicas = item.get("spec", {}).get("replicas")
                status = item.get("status", {})
                require(
                    isinstance(replicas, int)
                    and replicas > 0
                    and status.get("availableReplicas") == replicas
                    and status.get("readyReplicas") == replicas
                    and status.get("unavailableReplicas", 0) == 0,
                    f"authoritative Java Deployment/{name} is unavailable",
                )
            else:
                require(
                    item.get("spec", {}).get("type") == "ClusterIP"
                    and item.get("spec", {}).get("selector")
                    == {
                        "app.kubernetes.io/component": "api",
                        "app.kubernetes.io/managed-by": "kustomize",
                        "app.kubernetes.io/name": suffix,
                        "app.kubernetes.io/part-of": "insurance-hub",
                        "environment": target.environment,
                    },
                    f"authoritative Java Service/{name} differs",
                )
            values[f"{kind}/{name}"] = uid
    return values


def record_snapshot(stage: str, value: dict[str, Any]) -> None:
    print(json.dumps({"stage": stage, "resources": value}, sort_keys=True))


def administration_snapshot(_target: Target) -> dict[str, Any]:
    raise DeploymentError("Dashboard has no administration owner")


def initialization_check(_target: Target) -> None:
    raise DeploymentError("Dashboard has no initialization owner")


def initialization_complete(_target: Target) -> None:
    raise DeploymentError("Dashboard has no initialization owner")


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
    for kind in ("ingresses.networking.k8s.io", "httproutes.gateway.networking.k8s.io"):
        if kind not in resources.splitlines():
            continue
        listing = run(
            [
                target.kubectl_binary,
                "--context",
                target.context,
                "get",
                kind,
                "--all-namespaces",
                "-o",
                "json",
            ]
        )
        encoded = listing.lower()
        require(
            target.business not in encoded and target.management not in encoded,
            "public route references Dashboard",
        )


def _free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def _http(url: str, body: bytes | None = None) -> tuple[int, bytes]:
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    try:
        with urllib.request.urlopen(
            urllib.request.Request(
                url, data=body, headers=headers, method="POST" if body else "GET"
            ),
            timeout=3,
        ) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def qualification_pod(
    target: Target,
    name: str,
    labels: dict[str, str],
    script: str,
    *,
    namespace: str | None = None,
    elasticsearch: bool = False,
) -> dict[str, Any]:
    selected_namespace = namespace or target.namespace
    container: dict[str, Any] = {
        "name": "probe",
        "image": PROBE_IMAGE,
        "imagePullPolicy": "IfNotPresent",
        "command": ["/bin/sh", "-ec"],
        "args": [script],
        "securityContext": {
            "allowPrivilegeEscalation": False,
            "readOnlyRootFilesystem": True,
            "capabilities": {"drop": ["ALL"]},
        },
        "resources": {
            "requests": {"cpu": "10m", "memory": "16Mi"},
            "limits": {"cpu": "100m", "memory": "64Mi"},
        },
        "volumeMounts": [{"name": "tmp", "mountPath": "/tmp"}],
    }
    volumes: list[dict[str, Any]] = [{"name": "tmp", "emptyDir": {}}]
    if elasticsearch:
        container["env"] = [
            {
                "name": "ES_USER",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": f"{target.environment}-dashboard-elasticsearch",
                        "key": "username",
                    }
                },
            },
            {
                "name": "ES_PASSWORD",
                "valueFrom": {
                    "secretKeyRef": {
                        "name": f"{target.environment}-dashboard-elasticsearch",
                        "key": "password",
                    }
                },
            },
        ]
        if target.environment == "qa":
            container["volumeMounts"].append(
                {
                    "name": "elasticsearch-ca",
                    "mountPath": "/etc/dashboard/elasticsearch",
                    "readOnly": True,
                }
            )
            volumes.append(
                {
                    "name": "elasticsearch-ca",
                    "secret": {
                        "secretName": "qa-dashboard-elasticsearch-ca",
                        "items": [{"key": "ca.crt", "path": "ca.crt"}],
                        "defaultMode": 292,
                    },
                }
            )
    return {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {
            "name": name,
            "namespace": selected_namespace,
            "labels": labels | {"insurance-hub.io/qualification": "issue-287"},
        },
        "spec": {
            "activeDeadlineSeconds": 60,
            "automountServiceAccountToken": False,
            "enableServiceLinks": False,
            "restartPolicy": "Never",
            "securityContext": {
                "runAsNonRoot": True,
                "runAsUser": 65532,
                "runAsGroup": 65532,
                "seccompProfile": {"type": "RuntimeDefault"},
            },
            "containers": [container],
            "volumes": volumes,
        },
    }


def permission_probe_script(target: Target) -> str:
    url = (
        "http://local-dev-elasticsearch-es-http.local-dev-all.svc:9200"
        if target.environment == "local-dev"
        else "https://qa-elasticsearch-es-http.qa-data.svc:9200"
    )
    trust = "--cacert /etc/dashboard/elasticsearch/ca.crt" if target.environment == "qa" else ""
    missing_ca = ""
    if target.environment == "qa":
        missing_ca = f"""
if curl --config "$config" --silent --max-time 5 \
  "{url}/policy_stats/_count" >/dev/null 2>&1; then
  exit 21
fi
"""
    return f"""set -eu
config=/tmp/elasticsearch-curl.conf
umask 077
printf 'user = "%s:%s"\\n' "$ES_USER" "$ES_PASSWORD" >"$config"
trap 'rm -f "$config" /tmp/response /tmp/privileges' EXIT
request() {{
  curl --config "$config" --silent --show-error --max-time 5 {trust} \
    --output /tmp/response --write-out '%{{http_code}}' "$@"
}}
{missing_ca}failed=0
check_status() {{
  expected=$1
  shift
  actual=$(request "$@") || {{ failed=1; return; }}
  test "$actual" = "$expected" || failed=1
}}
check_status 200 "{url}/policy_stats/_count"
check_status 200 "{url}/policy_stats/_mapping"
check_status 200 "{url}/_security/user/_privileges"
cp /tmp/response /tmp/privileges
check_status 403 "{url}/issue-287-unrelated/_search"
check_status 403 "{url}/_cluster/health"
check_status 403 "{url}/_security/role/go_dashboard_total_sales_reader"
cat /tmp/privileges
exit "$failed"
"""


def validate_effective_reader_privileges(output: str) -> None:
    try:
        privileges = json.loads(output)
    except json.JSONDecodeError as error:
        raise DeploymentError("reader effective privileges are malformed") from error
    require(isinstance(privileges, dict), "reader effective privileges differ")
    allowed_fields = {
        "cluster",
        "global",
        "indices",
        "applications",
        "run_as",
        "remote_indices",
        "remote_cluster",
    }
    require(set(privileges).issubset(allowed_fields), "reader effective privileges differ")
    for field in (
        "cluster",
        "global",
        "applications",
        "run_as",
        "remote_indices",
        "remote_cluster",
    ):
        require(privileges.get(field, []) == [], "reader effective privileges differ")
    indices = privileges.get("indices")
    require(isinstance(indices, list) and len(indices) == 1, "reader effective privileges differ")
    index = indices[0]
    require(
        isinstance(index, dict)
        and set(index).issubset({"names", "privileges", "allow_restricted_indices"})
        and index.get("names") == ["policy_stats"]
        and isinstance(index.get("privileges"), list)
        and len(index["privileges"]) == 2
        and set(index["privileges"]) == {"read", "view_index_metadata"}
        and index.get("allow_restricted_indices", False) is False,
        "reader effective privileges differ",
    )


def validate_cni_denial(output: str) -> None:
    try:
        result = json.loads(output)
    except json.JSONDecodeError as error:
        raise DeploymentError("CNI denial result is malformed") from error
    require(
        result == {"exit_code": 28, "http_status": "000", "response_bytes": 0},
        "CNI denial result differs",
    )


def validate_grpc_reachability(output: str) -> None:
    try:
        result = json.loads(output)
    except json.JSONDecodeError as error:
        raise DeploymentError("gRPC reachability result is malformed") from error
    require(
        isinstance(result, dict)
        and set(result) == {"exit_code", "remote_ip", "remote_port", "num_connects"}
        and isinstance(result.get("exit_code"), int)
        and isinstance(result.get("remote_ip"), str)
        and bool(result["remote_ip"])
        and result.get("remote_port") == 9090
        and result.get("num_connects") == 1,
        "gRPC reachability result differs",
    )


def validate_qualification_output(name: str, output: str) -> None:
    if name == "dashboard-qualification-reader":
        validate_effective_reader_privileges(output)
    elif name == GRPC_REACHABLE_QUALIFICATION_POD:
        validate_grpc_reachability(output)
    elif name in DENIED_QUALIFICATION_PODS:
        validate_cni_denial(output)


def qualification_pods(target: Target, denied_namespace: str) -> list[dict[str, Any]]:
    total_sales_url = f"http://{target.business}.{target.namespace}.svc/dashboard/totalsales"
    agent_sales_url = f"http://{target.business}.{target.namespace}.svc/dashboard/agentssales"
    grpc_url = f"http://{target.business}.{target.namespace}.svc:9090"
    management_url = f"http://{target.management}.{target.namespace}.svc:8081"
    full_business = {
        "app.kubernetes.io/name": "dashboard-qualification",
        "app.kubernetes.io/component": "probe",
        "insurance-hub.io/owner": "issue-287",
        BUSINESS_CLIENT_LABEL: "true",
    }
    full_management = {
        "app.kubernetes.io/name": "dashboard-qualification",
        "app.kubernetes.io/component": "probe",
        "insurance-hub.io/owner": "issue-287",
        MANAGEMENT_CLIENT_LABEL: "true",
    }
    total_payload = '{"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}'
    agent_payload = (
        '{"agentLogin":"safe-agent","productCode":"SAFE",'
        '"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}'
    )
    allowed_total_sales = (
        "set -eu\n"
        f"curl --fail --silent --show-error --max-time 8 -H 'Content-Type: application/json' "
        f"--data '{total_payload}' '{total_sales_url}' | grep -q '\"total\"'\n"
    )
    allowed_agent_sales = (
        "set -eu\n"
        f"curl --fail --silent --show-error --max-time 8 -H 'Content-Type: application/json' "
        f"--data '{agent_payload}' '{agent_sales_url}' | grep -q '\"perAgentTotal\"'\n"
    )
    allowed_management = (
        "set -eu\n"
        f"test \"$(curl --fail --silent --show-error --max-time 5 '{management_url}/livez')\" "
        '= "live"\n'
        f"test \"$(curl --fail --silent --show-error --max-time 5 '{management_url}/readyz')\" "
        '= "ready"\n'
    )

    def denied(url: str) -> str:
        return f"""set -eu
response=/tmp/denied-response
trap 'rm -f "$response"' EXIT
: >"$response"
set +e
status=$(curl --silent --connect-timeout 2 --max-time 4 \
  --output "$response" --write-out '%{{http_code}}' '{url}' 2>/dev/null)
exit_code=$?
set -e
response_bytes=$(wc -c <"$response" | tr -d '[:space:]')
printf '{{"exit_code":%s,"http_status":"%s","response_bytes":%s}}\n' \
  "$exit_code" "$status" "$response_bytes"
"""

    grpc_reachable = f"""set -eu
set +e
result=$(curl --silent --connect-timeout 2 --max-time 4 --output /dev/null \
  --write-out '{{"exit_code":%{{exitcode}},"remote_ip":"%{{remote_ip}}",\
"remote_port":%{{remote_port}},"num_connects":%{{num_connects}}}}' \
  '{grpc_url}' 2>/dev/null)
set -e
printf '%s\n' "$result"
"""

    return [
        qualification_pod(
            target,
            "dashboard-qualification-business",
            full_business,
            allowed_total_sales,
        ),
        qualification_pod(
            target,
            "dashboard-qualification-business-agent-sales",
            full_business,
            allowed_agent_sales,
        ),
        qualification_pod(
            target,
            "dashboard-qualification-management",
            full_management,
            allowed_management,
        ),
        qualification_pod(
            target,
            GRPC_REACHABLE_QUALIFICATION_POD,
            full_business,
            grpc_reachable,
        ),
        qualification_pod(
            target,
            "dashboard-qualification-marker-only",
            {BUSINESS_CLIENT_LABEL: "true"},
            denied(total_sales_url),
        ),
        qualification_pod(
            target,
            "dashboard-qualification-marker-only-agent-denied",
            {BUSINESS_CLIENT_LABEL: "true"},
            denied(agent_sales_url),
        ),
        qualification_pod(
            target,
            "dashboard-qualification-marker-only-grpc-denied",
            {BUSINESS_CLIENT_LABEL: "true"},
            denied(grpc_url),
        ),
        qualification_pod(
            target,
            "dashboard-qualification-business-management-denied",
            full_business,
            denied(management_url + "/livez"),
        ),
        qualification_pod(
            target,
            "dashboard-qualification-cross-namespace-denied",
            full_business,
            denied(total_sales_url),
            namespace=denied_namespace,
        ),
        qualification_pod(
            target,
            "dashboard-qualification-cross-namespace-agent-denied",
            full_business,
            denied(agent_sales_url),
            namespace=denied_namespace,
        ),
        qualification_pod(
            target,
            "dashboard-qualification-cross-namespace-grpc-denied",
            full_business,
            denied(grpc_url),
            namespace=denied_namespace,
        ),
        qualification_pod(
            target,
            "dashboard-qualification-reader",
            full_business,
            permission_probe_script(target),
            elasticsearch=True,
        ),
    ]


def wait_for_probe(target: Target, namespace: str, name: str) -> None:
    deadline = time.monotonic() + 75
    while time.monotonic() < deadline:
        pod = target.namespace_json(namespace, "get", "pod", name)
        phase = pod.get("status", {}).get("phase")
        if phase == "Succeeded":
            return
        if phase == "Failed":
            raise DeploymentError(f"qualification Pod/{name} failed")
        time.sleep(1)
    raise DeploymentError(f"qualification Pod/{name} exceeded 75 seconds")


def run_access_qualification(target: Target) -> None:
    denied_namespace = f"issue-287-dashboard-denied-{target.environment}"
    namespace = {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {
            "name": denied_namespace,
            "labels": {"insurance-hub.io/qualification": "issue-287"},
        },
    }
    existing = run(
        [
            target.kubectl_binary,
            "--context",
            target.context,
            "get",
            "namespace",
            denied_namespace,
            "--ignore-not-found",
            "-o",
            "json",
        ]
    )
    require(not existing.strip(), f"qualification namespace/{denied_namespace} already exists")
    failures: list[str] = []
    try:
        target.kubectl("create", "-f", "-", input_text=yaml.safe_dump(namespace, sort_keys=False))
        for pod in qualification_pods(target, denied_namespace):
            metadata = pod["metadata"]
            name = metadata["name"]
            namespace_name = metadata["namespace"]
            created = False
            try:
                require(
                    not target.namespace_kubectl(
                        namespace_name,
                        "get",
                        "pod",
                        name,
                        "--ignore-not-found",
                        "-o",
                        "name",
                    ).strip(),
                    f"qualification Pod/{name} already exists",
                )
                target.namespace_kubectl(
                    namespace_name,
                    "create",
                    "-f",
                    "-",
                    input_text=yaml.safe_dump(pod, sort_keys=False),
                )
                created = True
                wait_for_probe(target, namespace_name, name)
                if (
                    name == "dashboard-qualification-reader"
                    or name == GRPC_REACHABLE_QUALIFICATION_POD
                    or name in DENIED_QUALIFICATION_PODS
                ):
                    output = target.namespace_kubectl(
                        namespace_name, "logs", f"pod/{name}", timeout=10
                    )
                    validate_qualification_output(name, output)
            except Exception as error:
                if created:
                    try:
                        logs = target.namespace_kubectl(
                            namespace_name, "logs", f"pod/{name}", timeout=10
                        )
                        diagnostic = logs[-1000:]
                    except Exception as log_error:
                        diagnostic = f"logs unavailable ({type(log_error).__name__})"
                else:
                    diagnostic = "probe output unavailable because the Pod was not created"
                failures.append(f"Pod/{name}: {error}; sanitized probe output: {diagnostic}")
            finally:
                try:
                    if created:
                        current = target.namespace_json(
                            namespace_name, "get", "pod", name, "--ignore-not-found"
                        )
                        if current:
                            require(
                                current.get("metadata", {})
                                .get("labels", {})
                                .get("insurance-hub.io/qualification")
                                == "issue-287",
                                f"qualification Pod/{name} cleanup ownership differs",
                            )
                            target.namespace_kubectl(
                                namespace_name,
                                "delete",
                                "pod",
                                name,
                                "--wait=true",
                                "--timeout=30s",
                                timeout=40,
                            )
                except Exception as cleanup_error:
                    failures.append(f"Pod/{name} cleanup: {cleanup_error}")
        require(not failures, "qualification probes failed: " + " | ".join(failures))
    finally:
        current = json.loads(
            run(
                [
                    target.kubectl_binary,
                    "--context",
                    target.context,
                    "get",
                    "namespace",
                    denied_namespace,
                    "--ignore-not-found",
                    "-o",
                    "json",
                ]
            )
            or "{}"
        )
        if current:
            require(
                current.get("metadata", {}).get("labels", {}).get("insurance-hub.io/qualification")
                == "issue-287",
                f"qualification namespace/{denied_namespace} cleanup ownership differs",
            )
            run(
                [
                    target.kubectl_binary,
                    "--context",
                    target.context,
                    "delete",
                    "namespace",
                    denied_namespace,
                    "--wait=true",
                    "--timeout=60s",
                ],
                timeout=70,
            )


def validate_direct_rpc_output(output: str, test_name: str = "TestDeployedDirectRPC") -> None:
    selected_runs = 0
    selected_passes = 0
    selected_failed = False
    for line in output.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(event, dict) or event.get("Test") != test_name:
            continue
        if event.get("Action") == "run":
            selected_runs += 1
        elif event.get("Action") == "pass":
            selected_passes += 1
        elif event.get("Action") == "fail":
            selected_failed = True
    require(
        selected_runs == 1 and selected_passes == 1 and not selected_failed,
        "deployed direct-RPC scenario did not execute and pass exactly once",
    )


def run_deployed_direct_rpc(port: int) -> None:
    test_name = "TestDeployedDirectRPCSharedSmoke"
    output = run(
        [
            os.environ.get("MAKE_COMMAND", "make"),
            "--no-print-directory",
            "-C",
            "services/dashboard-service",
            "_deploy-test-rpc",
        ],
        timeout=60,
        environment={
            "GO": os.environ.get("GO", "go"),
            "DASHBOARD_DEPLOYMENT_GRPC_ADDR": f"127.0.0.1:{port}",
            "DASHBOARD_DEPLOYMENT_RPC_SCENARIO": "shared-smoke",
        },
        activity="verify deployed direct Dashboard RPC",
    )
    validate_direct_rpc_output(output, test_name)


def verify_shared_http_operation(port: int, path: str, payload: dict[str, str], field: str) -> None:
    status, body = _http(
        f"http://127.0.0.1:{port}{path}",
        json.dumps(payload).encode(),
    )
    try:
        response = json.loads(body)
    except json.JSONDecodeError as error:
        raise DeploymentError(f"deployed {path} response is malformed") from error
    require(
        status == 200 and isinstance(response, dict) and isinstance(response.get(field), dict),
        f"deployed {path} response differs",
    )


def _wait_for_port_forward(process: subprocess.Popen[bytes], port: int, name: str) -> None:
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        require(process.poll() is None, f"{name} port-forward exited early")
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                return
        except OSError:
            time.sleep(0.2)
    raise DeploymentError(f"{name} port-forward was not reachable")


def verify(target: Target, items: list[dict[str, Any]]) -> None:
    workload_dependencies(target)
    target.live(items, require_deployment=True)
    before = role_snapshot(target)
    http_port, management_port, grpc_port = _free_port(), _free_port(), _free_port()
    processes = [
        subprocess.Popen(
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
        ),
        subprocess.Popen(
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
        ),
        subprocess.Popen(
            [
                target.kubectl_binary,
                "--context",
                target.context,
                "--namespace",
                target.namespace,
                "port-forward",
                f"service/{target.business}",
                f"{grpc_port}:9090",
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        ),
    ]
    failures: list[str] = []
    try:
        _wait_for_port_forward(processes[2], grpc_port, "direct-RPC")
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            try:
                if _http(f"http://127.0.0.1:{management_port}/readyz") == (200, b"ready\n"):
                    break
            except OSError:
                time.sleep(0.2)
        else:
            raise DeploymentError("deployed readiness was not reachable")
        for name, path, payload, field in (
            (
                "total-sales HTTP",
                "/dashboard/totalsales",
                {"saleDateFrom": "2019-01-01", "saleDateTo": "2020-01-01"},
                "total",
            ),
            (
                "agent-sales HTTP",
                "/dashboard/agentssales",
                {
                    "saleDateFrom": "2019-01-01",
                    "saleDateTo": "2020-01-01",
                },
                "perAgentTotal",
            ),
        ):
            try:
                verify_shared_http_operation(http_port, path, payload, field)
            except Exception as error:
                failures.append(f"{name}: {error}")
        try:
            run_deployed_direct_rpc(grpc_port)
        except Exception as error:
            failures.append(f"direct RPC: {error}")
    finally:
        for child in processes:
            child.terminate()
            try:
                child.wait(timeout=5)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=5)
    try:
        run_access_qualification(target)
    except Exception as error:
        failures.append(f"access qualification: {error}")
    try:
        require(
            role_snapshot(target) == before, "Java resource identity changed during verification"
        )
    except Exception as error:
        failures.append(f"Java identity: {error}")
    require(not failures, "Dashboard verification failed: " + " | ".join(failures))


def stop(target: Target, _items: list[dict[str, Any]]) -> None:
    before = role_snapshot(target)
    target.kubectl("scale", f"deployment/{target.deployment}", "--replicas=0", "--timeout=60s")
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        item = target.json("get", "deployment", target.deployment)
        if item.get("status", {}).get("replicas", 0) == 0:
            break
        time.sleep(1)
    else:
        raise DeploymentError("Dashboard stop exceeded 90 seconds")
    require(role_snapshot(target) == before, "Java resource identity changed during stop")


def validate_qa_aggregate(resources: Any) -> None:
    require(isinstance(resources, list), "active QA aggregate resources differ")
    require("./dashboard/legacy" in resources, "QA Java Dashboard overlay is missing")
    require("./dashboard" in resources, "QA Go Dashboard overlay is missing")


def validate_java_edge_identity(environment: str) -> None:
    overlay = ROOT / f"k8s/overlays/{environment}/svc/agent-portal-gateway/legacy"
    items = documents(run([os.environ.get("KUBECTL", "kubectl"), "kustomize", str(overlay)]))
    deployment = resource(
        items,
        "Deployment",
        f"{environment}-agent-portal-gateway-legacy",
    )
    labels = deployment.get("spec", {}).get("template", {}).get("metadata", {}).get("labels", {})
    require(
        labels_include(
            labels,
            {
                "app.kubernetes.io/name": "agent-portal-gateway-legacy",
                "app.kubernetes.io/instance": "insurance-hub-agent-portal-gateway-legacy",
                "app.kubernetes.io/component": "gateway",
                "environment": environment,
                BUSINESS_CLIENT_LABEL: "true",
            },
        ),
        "authoritative Java edge Dashboard caller identity differs",
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
        if arguments[0] == "render":
            sys.stdout.write(text)
    if arguments[0] == "check":
        qa_root = yaml.safe_load((ROOT / "k8s/overlays/qa/svc/kustomization.yaml").read_text())
        validate_qa_aggregate(qa_root.get("resources"))
        for environment in ("local-dev", "qa"):
            validate_java_edge_identity(environment)
        print("dashboard-deployment-check: local-dev and active QA renders passed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (DeploymentError, SharedDeploymentError, CommandError, OSError, ValueError) as error:
        print(f"dashboard-deployment-check: {error}", file=sys.stderr)
        raise SystemExit(1) from error
