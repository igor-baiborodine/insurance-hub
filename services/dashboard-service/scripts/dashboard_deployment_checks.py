#!/usr/bin/env python3
"""Dashboard deployment guards and service-owned verification hooks."""

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

SERVICE = "dashboard"
EXPECTED_KINDS = {"ConfigMap", "Deployment", "Service", "NetworkPolicy"}
BUSINESS_CLIENT_LABEL = "insurance-hub.io/dashboard-business-client"
MANAGEMENT_CLIENT_LABEL = "insurance-hub.io/dashboard-management-client"
JAVA_NAMES = {"dashboard-api-legacy"}
FORBIDDEN_TEXT = ("elasticsearch", "kafka", "postgres", "database", "secretKeyRef")


class DeploymentError(RuntimeError):
    """Dashboard deployment state differs from the accepted contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise DeploymentError(message)


def safe_environment() -> dict[str, str]:
    allowed = ("HOME", "KUBECONFIG", "PATH", "SSL_CERT_DIR", "SSL_CERT_FILE", "TERM", "TMPDIR")
    return {name: os.environ[name] for name in allowed if name in os.environ}


def run(argv: list[str], *, input_text: str | None = None, timeout: int = 30) -> str:
    result = run_command(
        argv,
        cwd=ROOT,
        env=safe_environment(),
        timeout_seconds=timeout,
        teardown_seconds=10,
        activity="Dashboard deployment command",
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


def render(environment: str) -> str:
    record = service_record(ROOT, SERVICE)
    target = target_record(ROOT, record, environment)
    return run([os.environ.get("KUBECTL", "kubectl"), "kustomize", str(ROOT / target["overlay"])])


def expected_egress(environment: str) -> list[dict[str, Any]]:
    if environment == "local-dev":
        return []
    return [
        {
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
        },
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
        "OTEL_ENABLED": "false" if environment == "local-dev" else "true",
    }
    if environment == "qa":
        expected_config["OTEL_EXPORTER_OTLP_ENDPOINT"] = (
            "http://qa-alloy.qa-monitoring.svc.cluster.local:4317"
        )
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
        and not pod.get("volumes")
        and not pod.get("initContainers"),
        "Pod security or data boundary differs",
    )
    containers = pod.get("containers", [])
    require(len(containers) == 1, "container inventory differs")
    container = containers[0]
    require(
        container.get("name") == "dashboard-api"
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
        and not container.get("volumeMounts")
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
        and set(env) == {"SERVICE_INSTANCE_ID"},
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
    ingress = policy_spec.get("ingress", [])
    require(policy_spec.get("policyTypes") == ["Ingress", "Egress"], "NetworkPolicy types differ")
    require(len(ingress) == 2, "NetworkPolicy ingress differs")
    expected_ingress = {
        BUSINESS_CLIENT_LABEL: {8080, 9090},
        MANAGEMENT_CLIENT_LABEL: {8081},
    }
    actual_ingress: dict[str, set[int]] = {}
    for rule in ingress:
        peers = rule.get("from", [])
        require(len(peers) == 1, "NetworkPolicy peer differs")
        labels = peers[0].get("podSelector", {}).get("matchLabels", {})
        require(len(labels) == 1, "NetworkPolicy label differs")
        label, value = next(iter(labels.items()))
        require(value == "true", "NetworkPolicy client label value differs")
        actual_ingress[label] = {port.get("port") for port in rule.get("ports", [])}
    require(actual_ingress == expected_ingress, "NetworkPolicy admission differs")
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

    def coordinates(self) -> None:
        require(
            run([self.kubectl_binary, "config", "current-context"]).strip() == self.context,
            "active Kubernetes context differs",
        )
        namespace = self.json("get", "namespace", self.namespace)
        require(namespace.get("metadata", {}).get("name") == self.namespace, "namespace differs")

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
        for desired in items:
            kind = str(desired.get("kind", ""))
            metadata = desired.get("metadata", {})
            name = metadata.get("name", "")
            actual = self.json("get", kind.lower(), name, "--ignore-not-found")
            if not actual:
                require(not require_deployment, f"{kind}/{name} is absent")
                continue
            actual_metadata = actual.get("metadata", {})
            require(
                actual.get("kind") == kind
                and actual_metadata.get("name") == name
                and actual_metadata.get("namespace") == metadata.get("namespace") == self.namespace
                and labels_include(actual_metadata.get("labels"), metadata.get("labels"))
                and not actual_metadata.get("ownerReferences")
                and not actual_metadata.get("deletionTimestamp"),
                f"live {kind}/{name} identity differs from rendered identity",
            )

            if kind == "ConfigMap":
                require(
                    actual.get("data") == desired.get("data")
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
                    normalized_policy_spec(actual.get("spec"))
                    == normalized_policy_spec(desired.get("spec")),
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
            require(
                labels_include(
                    template.get("metadata", {}).get("labels"),
                    desired_template.get("metadata", {}).get("labels"),
                ),
                f"live Deployment/{name} Pod identity differs",
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
                and not pod.get("volumes"),
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


def secret_and_image(target: Target, image: str) -> None:
    require(not os.environ.get("DEPLOY_SECRET"), "Dashboard accepts no deployment Secret")
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


def verify(target: Target, items: list[dict[str, Any]]) -> None:
    target.live(items, require_deployment=True)
    before = role_snapshot(target)
    http_port, management_port = _free_port(), _free_port()
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
    ]
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
        status, body = _http(
            f"http://127.0.0.1:{http_port}/dashboard/totalsales",
            json.dumps({"saleDateFrom": "2019-01-01", "saleDateTo": "2020-01-01"}).encode(),
        )
        require(
            status == 200 and "total" in json.loads(body),
            "deployed total-sales response differs",
        )
    finally:
        for child in processes:
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
        print("dashboard-deployment-check: local-dev and active QA renders passed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (DeploymentError, SharedDeploymentError, CommandError, OSError, ValueError) as error:
        print(f"dashboard-deployment-check: {error}", file=sys.stderr)
        raise SystemExit(1) from error
