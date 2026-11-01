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

ROOT = Path(__file__).resolve().parents[3]
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


def render(environment: str) -> str:
    record = service_record(ROOT, SERVICE)
    configuration = target_record(ROOT, record, environment)
    kubectl = os.environ.get("KUBECTL", "kubectl")
    return run([kubectl, "kustomize", str(ROOT / configuration["overlay"])])


def validate_render(environment: str, text: str) -> list[dict[str, Any]]:
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
        len(containers) == 1 and containers[0].get("name") == "policy-search-service",
        "container identity differs",
    )
    container = containers[0]
    image = container.get("image", "")
    expected_image = (
        "insurance-hub-policy-search-api:1.0.0-local.000000000000"
        if environment == "local-dev"
        else "ghcr.io/igor-baiborodine/insurance-hub/policy-search-api:1.0.0"
    )
    require(image == expected_image, "staged image identity differs")
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
    require(not pod_spec.get("volumes"), "Policy Search must not mount volumes")
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
    if environment == "local-dev":
        require(policy.get("egress") == [], "local-dev egress must be denied")
    else:
        require(
            "qa-monitoring" in encoded_policy and "kube-system" in encoded_policy,
            "QA egress differs",
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
        "PROBE_TIMEOUT": "2s",
        "SHUTDOWN_TIMEOUT": "10s",
        "HTTP_MAX_HEADER_BYTES": "16384",
        "HTTP_MAX_BODY_BYTES": "4096",
        "GRPC_MAX_RECV_BYTES": "1048576",
        "GRPC_MAX_SEND_BYTES": "8388608",
    }
    require(
        all(data.get(key) == value for key, value in required_data.items()),
        "runtime budgets differ",
    )
    if environment == "local-dev":
        require(data.get("OTEL_ENABLED") == "false", "local telemetry must be disabled")
    else:
        require(
            data.get("OTEL_ENABLED") == "true"
            and data.get("OTEL_EXPORTER_OTLP_ENDPOINT")
            == "http://qa-alloy.qa-monitoring.svc.cluster.local:4317"
            and data.get("SERVICE_VERSION") == image.rsplit(":", 1)[1],
            "QA telemetry identity differs",
        )
    require(
        not any(
            "elasticsearch" in json.dumps(item).lower() or "kafka" in json.dumps(item).lower()
            for item in items
        ),
        "business dependency leaked into Echo deployment",
    )
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

    def rendered(self) -> tuple[str, list[dict[str, Any]], str]:
        text = self.kubectl("kustomize", str(ROOT / self.configuration["overlay"]))
        items = validate_render(self.environment, text)
        deployment = next(item for item in items if item.get("kind") == "Deployment")
        image = deployment["spec"]["template"]["spec"]["containers"][0]["image"]
        requested = os.environ.get("DEPLOY_IMAGE_REF", "")
        if requested:
            require(self.environment == "local-dev", "DEPLOY_IMAGE_REF is direct-local only")
            require(
                bool(
                    re.fullmatch(
                        r"insurance-hub-policy-search-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}",
                        requested,
                    )
                ),
                "DEPLOY_IMAGE_REF differs",
            )
            source_short = requested.rsplit(".", 1)[1]
            version = f"git-{source_short}"
            deployment["spec"]["template"]["spec"]["containers"][0]["image"] = requested
            deployment["metadata"].setdefault("labels", {})["app.kubernetes.io/version"] = version
            deployment["spec"]["template"]["metadata"].setdefault("labels", {})[
                "app.kubernetes.io/version"
            ] = version
            config = next(item for item in items if item.get("kind") == "ConfigMap")
            config["data"]["SERVICE_VERSION"] = version
            text = yaml.safe_dump_all(items, explicit_start=True, sort_keys=False)
            image = requested
        return text, items, image

    def live(self, items: list[dict[str, Any]], *, require_deployment: bool) -> None:
        for item in items:
            kind = str(item.get("kind", "")).lower()
            name = item.get("metadata", {}).get("name", "")
            actual = self.json("get", kind, name, "--ignore-not-found")
            if require_deployment:
                require(actual.get("metadata", {}).get("name") == name, f"{kind}/{name} is absent")


def check_render() -> None:
    validate_render(os.environ.get("DEPLOY_ENV", ""), render(os.environ.get("DEPLOY_ENV", "")))


def secret_and_image(target: Target, image: str) -> None:
    require(not os.environ.get("DEPLOY_SECRET"), "Policy Search accepts no deployment Secret")
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
        require(
            _http(f"http://127.0.0.1:{http_port}/v1/example/echo", b'{"message":"hello"}')
            == (200, b'{"message":"hello"}'),
            "deployed Echo differs",
        )
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
        require(
            "./policy-search" not in qa_root.get("resources", []),
            "QA Policy Search overlay is already active",
        )
        print("policy-search-deployment-check: local-dev and QA renders passed")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (DeploymentError, SharedDeploymentError, CommandError, OSError, ValueError) as error:
        print(f"policy-search-deployment-check: {error}", file=sys.stderr)
        raise SystemExit(1) from error
