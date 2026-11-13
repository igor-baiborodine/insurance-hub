#!/usr/bin/env python3
"""Document deployment guards and service-owned verification hooks."""

from __future__ import annotations

import json
import os
import re
import socket
import sys
import time
import urllib.error
import urllib.request
from contextlib import ExitStack
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

from scripts.go.service.configuration import service_record, target_record  # noqa: E402
from scripts.go.service.deployment import (  # noqa: E402
    DeploymentError as SharedDeploymentError,
)
from scripts.go.service.tooling_contracts import (  # noqa: E402
    OwnedPortForward,
    PortForwardSpec,
    loopback_port,
)
from scripts.python.process_runner import CommandError, run_command  # noqa: E402

SERVICE = "document"
EXPECTED_KINDS = {"ConfigMap", "Deployment", "Service", "NetworkPolicy"}
PROBE_IMAGE = (
    "docker.io/curlimages/curl:8.16.0@"
    "sha256:463eaf6072688fe96ac64fa623fe73e1dbe25d8ad6c34404a669ad3ce1f104b6"
)
FORBIDDEN_TEXT = (
    "secretkeyref",
    "persistentvolumeclaim",
    "postgres",
    "database",
    "elasticsearch",
    "kafka",
    "minio",
    "bucket",
    "jsreport",
    "renderer",
    "insecure_skip_verify",
)
ROLLOUT_TIMEOUT_SECONDS = {"local-dev": 180, "qa": 600}


class DeploymentError(RuntimeError):
    """Document deployment state differs from the accepted contract."""


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
        activity="Document deployment command",
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
    require(not any(value in lower for value in FORBIDDEN_TEXT), "forbidden dependency is present")
    items = documents(text)
    require(bool(items), "render produced no resources")
    require({item.get("kind") for item in items} == EXPECTED_KINDS, "resource kinds differ")
    require(len(items) == 4, "Document resource inventory differs")
    prefix = f"{environment}-"
    expected_names = {
        "ConfigMap": {prefix + "document-config"},
        "Deployment": {prefix + "document-api"},
        "Service": {prefix + "document-api-management"},
        "NetworkPolicy": {prefix + "document-api"},
    }
    for kind, names in expected_names.items():
        actual = {
            item.get("metadata", {}).get("name") for item in items if item.get("kind") == kind
        }
        require(actual == names, f"{kind} identity differs")
    namespace = "local-dev-all" if environment == "local-dev" else "qa-svc"
    for item in items:
        metadata = item.get("metadata", {})
        require(metadata.get("namespace") in (None, namespace), "resource namespace differs")
        require(
            labels_include(
                metadata.get("labels", {}),
                {
                    "app.kubernetes.io/name": "document-api",
                    "app.kubernetes.io/part-of": "insurance-hub",
                },
            ),
            "resource labels differ",
        )

    config = resource(items, "ConfigMap", prefix + "document-config")
    data = config.get("data", {})
    expected_config = {
        "SERVICE_NAME": "document-service",
        "DEPLOYMENT_ENVIRONMENT": environment,
        "HTTP_ADDR": "0.0.0.0:8080",
        "GRPC_ADDR": "0.0.0.0:9090",
        "HEALTH_ADDR": "0.0.0.0:8081",
        "REQUEST_TIMEOUT": "8s",
        "PROBE_TIMEOUT": "2s",
        "SHUTDOWN_TIMEOUT": "10s",
        "OTEL_EXPORTER_OTLP_TIMEOUT": "2s",
        "HTTP_READ_HEADER_TIMEOUT": "5s",
        "HTTP_READ_TIMEOUT": "10s",
        "HTTP_WRITE_TIMEOUT": "10s",
        "HTTP_IDLE_TIMEOUT": "30s",
        "HTTP_MAX_HEADER_BYTES": "16384",
        "HTTP_MAX_BODY_BYTES": "4096",
        "GRPC_MAX_RECV_BYTES": "1048576",
        "GRPC_MAX_SEND_BYTES": "8388608",
        "LOG_LEVEL": "info",
        "OTEL_ENABLED": "false" if environment == "local-dev" else "true",
        "SERVICE_VERSION": data.get("SERVICE_VERSION"),
    }
    if environment == "qa":
        expected_config["OTEL_EXPORTER_OTLP_ENDPOINT"] = (
            "http://qa-alloy.qa-monitoring.svc.cluster.local:4317"
        )
    require(data == expected_config, "runtime configuration differs")
    version = data.get("SERVICE_VERSION", "")
    require(
        bool(re.fullmatch(r"git-[0-9a-f]{12}", version)) and set(version[4:]) != {"0"},
        "runtime version is unresolved",
    )

    deployment = resource(items, "Deployment", prefix + "document-api")
    spec = deployment.get("spec", {})
    require(
        spec.get("replicas") == 1
        and spec.get("minReadySeconds") == 5
        and spec.get("progressDeadlineSeconds") == 180
        and spec.get("strategy")
        == {"type": "RollingUpdate", "rollingUpdate": {"maxSurge": 1, "maxUnavailable": 0}},
        "rollout settings differ",
    )
    selector = {
        "app.kubernetes.io/name": "document-api",
        "app.kubernetes.io/component": "api",
    }
    require(spec.get("selector", {}).get("matchLabels") == selector, "workload selector differs")
    template = spec.get("template", {})
    require(
        labels_include(template.get("metadata", {}).get("labels"), selector)
        and deployment.get("metadata", {}).get("labels", {}).get("app.kubernetes.io/version")
        == version
        and template.get("metadata", {}).get("labels", {}).get("app.kubernetes.io/version")
        == version,
        "workload identity or version differs",
    )
    pod = template.get("spec", {})
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
        and not pod.get("initContainers")
        and pod.get("serviceAccountName") in (None, "default"),
        "Pod security or data boundary differs",
    )
    containers = pod.get("containers", [])
    require(len(containers) == 1, "container inventory differs")
    container = containers[0]
    expected_image = (
        r"insurance-hub-document-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}"
        if environment == "local-dev"
        else r"ghcr\.io/igor-baiborodine/insurance-hub/document-api:1\.[0-9]+\.[0-9]+"
    )
    require(
        bool(re.fullmatch(expected_image, container.get("image", ""))), "image identity differs"
    )
    require(
        container.get("name") == "document-api"
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
        and not container.get("volumeMounts"),
        "container security or resources differ",
    )
    require(
        container.get("env")
        == [
            {
                "name": "SERVICE_INSTANCE_ID",
                "valueFrom": {"fieldRef": {"apiVersion": "v1", "fieldPath": "metadata.uid"}},
            }
        ]
        and container.get("envFrom") == [{"configMapRef": {"name": prefix + "document-config"}}],
        "runtime identity or configuration linkage differs",
    )
    ports = {(item.get("name"), item.get("containerPort")) for item in container.get("ports", [])}
    require(
        ports == {("http", 8080), ("grpc", 9090), ("management", 8081)},
        "container ports differ",
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

    management = resource(items, "Service", prefix + "document-api-management")
    require(
        management.get("spec", {}).get("type") == "ClusterIP"
        and management.get("spec", {}).get("selector") == selector
        and management.get("spec", {}).get("ports")
        == [{"name": "management", "port": 8081, "protocol": "TCP", "targetPort": "management"}],
        "management Service differs",
    )
    policy = resource(items, "NetworkPolicy", prefix + "document-api")
    require(
        policy.get("spec", {}).get("podSelector", {}).get("matchLabels") == selector
        and policy.get("spec", {}).get("policyTypes") == ["Ingress", "Egress"]
        and policy.get("spec", {}).get("ingress", []) == []
        and policy.get("spec", {}).get("egress", []) == expected_egress(environment),
        "NetworkPolicy differs",
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
        self.deployment = f"{self.environment}-document-api"
        self.management = f"{self.environment}-document-api-management"
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
                        r"insurance-hub-document-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}",
                        requested,
                    )
                ),
                "DEPLOY_IMAGE_REF differs",
            )
            version = "git-" + requested.rsplit(".", 1)[1]
            deployment["spec"]["template"]["spec"]["containers"][0]["image"] = requested
            deployment["metadata"]["labels"]["app.kubernetes.io/version"] = version
            deployment["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/version"] = (
                version
            )
            resource(items, "ConfigMap", f"{self.environment}-document-config")["data"][
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
            require(
                actual.get("kind") == kind
                and actual.get("metadata", {}).get("name") == name
                and actual.get("metadata", {}).get("namespace") == self.namespace
                and not actual.get("metadata", {}).get("ownerReferences")
                and not actual.get("metadata", {}).get("deletionTimestamp"),
                f"live {kind}/{name} identity differs",
            )
            if kind == "ConfigMap":
                require(actual.get("data") == desired.get("data"), f"live ConfigMap/{name} differs")
            elif kind == "Service":
                actual_spec = actual.get("spec", {})
                desired_spec = desired.get("spec", {})
                require(
                    actual_spec.get("type") == "ClusterIP"
                    and actual_spec.get("selector") == desired_spec.get("selector")
                    and actual_spec.get("ports") == desired_spec.get("ports")
                    and not any(
                        field in actual_spec
                        for field in ("externalIPs", "loadBalancerIP", "externalName")
                    ),
                    f"live Service/{name} differs or is externally exposed",
                )
            elif kind == "NetworkPolicy":
                require(
                    normalized_policy_spec(actual.get("spec"))
                    == normalized_policy_spec(desired.get("spec")),
                    f"live NetworkPolicy/{name} differs",
                )
            else:
                require(kind == "Deployment", f"unsupported live resource kind: {kind}")
                desired_spec = desired.get("spec", {})
                actual_spec = actual.get("spec", {})
                desired_template = desired_spec.get("template", {})
                actual_template = actual_spec.get("template", {})
                desired_pod = desired_template.get("spec", {})
                actual_pod = actual_template.get("spec", {})
                require(
                    actual_spec.get("selector") == desired_spec.get("selector")
                    and actual_spec.get("strategy") == desired_spec.get("strategy")
                    and actual_spec.get("minReadySeconds") == desired_spec.get("minReadySeconds")
                    and actual_spec.get("progressDeadlineSeconds")
                    == desired_spec.get("progressDeadlineSeconds")
                    and actual_spec.get("replicas")
                    in ({desired_spec.get("replicas")} if require_deployment else {0, 1})
                    and labels_include(
                        actual.get("metadata", {}).get("labels"),
                        desired.get("metadata", {}).get("labels"),
                    )
                    and labels_include(
                        actual_template.get("metadata", {}).get("labels"),
                        desired_template.get("metadata", {}).get("labels"),
                    )
                    and actual_pod.get("automountServiceAccountToken") is False
                    and actual_pod.get("enableServiceLinks") is False
                    and actual_pod.get("terminationGracePeriodSeconds") == 30
                    and actual_pod.get("securityContext") == desired_pod.get("securityContext")
                    and actual_pod.get("serviceAccountName") in (None, "default")
                    and not actual_pod.get("initContainers")
                    and actual_pod.get("volumes", []) == desired_pod.get("volumes", []),
                    f"live Deployment/{name} differs",
                )
                desired_containers = desired_pod.get("containers", [])
                actual_containers = actual_pod.get("containers", [])
                require(
                    len(desired_containers) == len(actual_containers) == 1,
                    f"live Deployment/{name} container inventory differs",
                )
                desired_container = desired_containers[0]
                actual_container = actual_containers[0]
                for field in (
                    "name",
                    "image",
                    "imagePullPolicy",
                    "securityContext",
                    "ports",
                    "env",
                    "envFrom",
                    "resources",
                    "volumeMounts",
                ):
                    require(
                        actual_container.get(field) == desired_container.get(field),
                        f"live Deployment/{name} container {field} differs",
                    )
                for field in ("startupProbe", "livenessProbe", "readinessProbe"):
                    require(
                        matching_probe(actual_container.get(field), desired_container.get(field)),
                        f"live Deployment/{name} container {field} differs",
                    )
                if require_deployment:
                    require(
                        actual.get("status", {}).get("availableReplicas")
                        == desired_spec.get("replicas"),
                        f"live Deployment/{name} is not available",
                    )
        if require_deployment and self.configuration["mode"] == "flux":
            self.require_running_image_digest(resource(items, "Deployment", self.deployment))

    def require_running_image_digest(self, desired: dict[str, Any]) -> None:
        expected = os.environ.get("DEPLOY_IMAGE_DIGEST", "")
        require(
            bool(re.fullmatch(r"sha256:[0-9a-f]{64}", expected)),
            "DEPLOY_IMAGE_DIGEST is required for QA live verification",
        )
        selector = desired["spec"]["selector"]["matchLabels"]
        listing = self.json(
            "get",
            "pods",
            "-l",
            ",".join(f"{key}={value}" for key, value in sorted(selector.items())),
        )
        pods = [
            item
            for item in listing.get("items", [])
            if not item.get("metadata", {}).get("deletionTimestamp")
        ]
        require(len(pods) == desired["spec"]["replicas"], "running Document Pod inventory differs")
        for pod in pods:
            statuses = pod.get("status", {}).get("containerStatuses", [])
            matches = [item for item in statuses if item.get("name") == "document-api"]
            require(
                pod.get("status", {}).get("phase") == "Running"
                and len(matches) == 1
                and (
                    matches[0].get("imageID") == expected
                    or matches[0].get("imageID", "").endswith("@" + expected)
                ),
                "running Document Pod image digest differs",
            )


def check_render() -> None:
    environment = os.environ.get("DEPLOY_ENV", "")
    validate_render(environment, render(environment))


def secret_and_image(target: Target, image: str) -> None:
    require(not os.environ.get("DEPLOY_SECRET"), "Document accepts no deployment Secret")
    if target.configuration["mode"] == "flux":
        require(
            bool(
                re.fullmatch(
                    r"ghcr\.io/igor-baiborodine/insurance-hub/document-api:1\.[0-9]+\.[0-9]+",
                    image,
                )
            ),
            "QA image tag differs",
        )
        return
    require(
        bool(
            re.fullmatch(r"insurance-hub-document-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}", image)
        ),
        "local image tag differs",
    )
    details = json.loads(run([os.environ.get("DOCKER", "docker"), "image", "inspect", image]))
    require(len(details) == 1, "local image inspect differs")
    revision = (
        details[0].get("Config", {}).get("Labels", {}).get("org.opencontainers.image.revision", "")
    )
    require(
        bool(re.fullmatch(r"[0-9a-f]{40}", revision)) and revision[:12] == image.rsplit(".", 1)[1],
        "image source revision differs",
    )


def record_snapshot(stage: str, value: dict[str, Any]) -> None:
    print(f"document-deployment: {stage}: {json.dumps(value, sort_keys=True)}", file=sys.stderr)


def administration_snapshot(_target: Target) -> dict[str, Any]:
    raise DeploymentError("Document has no administration owner")


def initialization_check(_target: Target) -> None:
    raise DeploymentError("Document has no initialization owner")


def initialization_complete(_target: Target) -> None:
    raise DeploymentError("Document has no initialization owner")


def rollout(target: Target) -> None:
    seconds = ROLLOUT_TIMEOUT_SECONDS[target.environment]
    target.kubectl(
        "rollout",
        "status",
        f"deployment/{target.deployment}",
        f"--timeout={seconds}s",
        timeout=seconds + 30,
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
        ).lower()
        require(
            target.deployment not in listing and target.management not in listing,
            "public route references Go Document",
        )


def _http(url: str, body: bytes | None = None) -> tuple[int, bytes]:
    request = urllib.request.Request(
        url,
        data=body,
        headers={"Content-Type": "application/json", "Accept": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=3) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def _wait_for_port(port: int, name: str) -> None:
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=1):
                return
        except OSError:
            time.sleep(0.2)
    raise DeploymentError(f"{name} port-forward did not become ready")


def verify_untrusted_access_denied(target: Target) -> None:
    name = "document-untrusted-probe"
    require(
        not target.json("get", "pod", name, "--ignore-not-found"),
        f"unowned Pod/{name} already exists",
    )
    created = False
    try:
        target.kubectl(
            "run",
            name,
            "--restart=Never",
            f"--image={PROBE_IMAGE}",
            "--labels=insurance-hub.io/document-probe=untrusted",
            "--command",
            "--",
            "curl",
            "--fail",
            "--max-time",
            "3",
            f"http://{target.management}:8081/livez",
        )
        created = True
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            pod = target.json("get", "pod", name)
            statuses = pod.get("status", {}).get("containerStatuses", [])
            terminated = statuses[0].get("state", {}).get("terminated", {}) if statuses else {}
            if terminated:
                require(
                    terminated.get("exitCode") == 28,
                    "untrusted Pod failed for a reason other than denied network access",
                )
                return
            time.sleep(1)
        raise DeploymentError("untrusted Pod denial probe exceeded 45 seconds")
    finally:
        if created:
            target.kubectl(
                "delete",
                "pod",
                name,
                "--ignore-not-found",
                "--wait=true",
                "--timeout=30s",
                timeout=40,
            )


def verify(target: Target, items: list[dict[str, Any]]) -> None:
    target.live(items, require_deployment=True)
    http_port, management_port = loopback_port(), loopback_port()
    with ExitStack() as forwards:
        for local_port, remote_port in ((http_port, 8080), (management_port, 8081)):
            forwards.enter_context(
                OwnedPortForward(
                    PortForwardSpec(
                        context=target.context,
                        namespace=target.namespace,
                        resource=f"deployment/{target.deployment}",
                        local_port=local_port,
                        remote_port=remote_port,
                    ),
                    cwd=ROOT,
                    env=safe_environment(),
                    startup_seconds=20,
                    teardown_seconds=5,
                    kubectl=target.kubectl_binary,
                )
            )
        _wait_for_port(http_port, "Echo HTTP")
        _wait_for_port(management_port, "management")
        require(
            _http(f"http://127.0.0.1:{management_port}/livez") == (200, b"live\n")
            and _http(f"http://127.0.0.1:{management_port}/readyz") == (200, b"ready\n"),
            "deployed management health differs",
        )
        status, body = _http(
            f"http://127.0.0.1:{http_port}/v1/example/echo",
            b'{"message":"foundation"}',
        )
        require(status == 200 and json.loads(body) == {"message": "foundation"}, "Echo differs")
    verify_untrusted_access_denied(target)


def stop(target: Target, _items: list[dict[str, Any]]) -> None:
    target.kubectl("scale", f"deployment/{target.deployment}", "--replicas=0", "--timeout=60s")
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        if (
            target.json("get", "deployment", target.deployment).get("status", {}).get("replicas", 0)
            == 0
        ):
            return
        time.sleep(1)
    raise DeploymentError("Document stop exceeded 90 seconds")


def validate_qa_aggregate(resources: Any) -> None:
    require(isinstance(resources, list), "QA service aggregate resources are invalid")
    require(
        all(isinstance(resource, str) for resource in resources),
        "QA service aggregate resources are invalid",
    )
    require(resources.count("./document/legacy") == 1, "Java Document must remain active once")
    require(resources.count("./document") <= 1, "Go Document must be activated at most once")


def validate_legacy_identity(environment: str) -> None:
    overlay = ROOT / f"k8s/overlays/{environment}/svc/document/legacy"
    text = run([os.environ.get("KUBECTL", "kubectl"), "kustomize", str(overlay)])
    items = documents(text)
    deployment = resource(items, "Deployment", f"{environment}-document-api-legacy")
    selector = deployment.get("spec", {}).get("selector", {}).get("matchLabels", {})
    require(
        selector.get("app.kubernetes.io/name") == "document-api-legacy"
        and selector.get("app.kubernetes.io/name") != "document-api",
        "Java Document selector is not disjoint",
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
            validate_legacy_identity(environment)
        print("document-deployment-check: local-dev/QA renders and legacy separation passed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (DeploymentError, SharedDeploymentError, CommandError, OSError, ValueError) as error:
        print(f"document-deployment-check: {error}", file=sys.stderr)
        raise SystemExit(1) from error
