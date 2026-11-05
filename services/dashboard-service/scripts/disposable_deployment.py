#!/usr/bin/env python3
"""Exercise Dashboard in a uniquely owned namespace on the configured Kind cluster."""

from __future__ import annotations

import json
import os
import re
import secrets
import socket
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

import dashboard_deployment_checks as checks
import yaml

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

from scripts.python.process_runner import (  # noqa: E402
    CommandError,
    run_command,
    terminate_process_group,
)

CONTEXT = "kind-local-dev-insurance-hub"
KIND_CLUSTER = "local-dev-insurance-hub"
OWNER_LABEL = "insurance-hub.io/dashboard-disposable"
PROBE_IMAGE = (
    "docker.io/curlimages/curl:8.16.0@"
    "sha256:463eaf6072688fe96ac64fa623fe73e1dbe25d8ad6c34404a669ad3ce1f104b6"
)


class CheckFailed(RuntimeError):
    """The disposable Dashboard deployment differed from its owned contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise CheckFailed(message)


def safe_environment(extra: dict[str, str] | None = None) -> dict[str, str]:
    environment = {
        name: os.environ[name]
        for name in ("HOME", "KUBECONFIG", "PATH", "TMPDIR")
        if name in os.environ
    }
    environment.update(extra or {})
    return environment


def command(
    arguments: list[str],
    *,
    input_text: str | None = None,
    timeout: float = 60,
    environment: dict[str, str] | None = None,
    activity: str | None = None,
    heartbeat: float = 30,
    teardown: float = 10,
) -> str:
    try:
        result = run_command(
            arguments,
            cwd=ROOT,
            env=safe_environment(environment),
            timeout_seconds=timeout,
            teardown_seconds=teardown,
            heartbeat_seconds=heartbeat,
            activity=activity or f"{arguments[0]} operation",
            input_text=input_text,
        )
    except CommandError as error:
        diagnostic = (error.stderr or error.stdout).strip().replace("\n", " ")[:400]
        detail = f": {diagnostic}" if diagnostic else ""
        raise CheckFailed(f"{error}{detail}") from error
    except OSError as error:
        raise CheckFailed(f"command unavailable: {arguments[0]}") from error
    return result.stdout


class Cluster:
    def __init__(self, namespace: str) -> None:
        self.namespace = namespace

    def kubectl(
        self,
        *arguments: str,
        input_text: str | None = None,
        timeout: int = 60,
        activity: str | None = None,
    ) -> str:
        return command(
            ["kubectl", "--context", CONTEXT, "--namespace", self.namespace, *arguments],
            input_text=input_text,
            timeout=timeout,
            activity=activity or f"kubectl {arguments[0]}",
        )

    def json(self, *arguments: str) -> dict[str, Any]:
        return json.loads(self.kubectl(*arguments, "-o", "json") or "{}")


def render(cluster: Cluster, image: str, source_short: str) -> tuple[str, list[dict[str, Any]]]:
    text = checks.render("local-dev")
    items = checks.validate_render("local-dev", text)
    for item in items:
        item["metadata"]["namespace"] = cluster.namespace
        item["metadata"].setdefault("labels", {})[OWNER_LABEL] = cluster.namespace
    deployment = checks.resource(items, "Deployment", "local-dev-dashboard-api")
    deployment["spec"]["template"]["metadata"].setdefault("labels", {})[OWNER_LABEL] = (
        cluster.namespace
    )
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = image
    version = f"git-{source_short}"
    deployment["metadata"].setdefault("labels", {})["app.kubernetes.io/version"] = version
    deployment["spec"]["template"]["metadata"].setdefault("labels", {})[
        "app.kubernetes.io/version"
    ] = version
    checks.resource(items, "ConfigMap", "local-dev-dashboard-config")["data"]["SERVICE_VERSION"] = (
        version
    )
    return yaml.safe_dump_all(items, explicit_start=True, sort_keys=False), items


def wait_for(description: str, predicate, seconds: int = 90) -> None:
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if predicate():
            print(f"PASS {description}", flush=True)
            return
        time.sleep(1)
    raise CheckFailed(f"timed out: {description}")


def probe(
    cluster: Cluster,
    name: str,
    arguments: list[str],
    *,
    labels: dict[str, str] | None = None,
    expect_success: bool = True,
) -> None:
    pod = {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {
            "name": name,
            "namespace": cluster.namespace,
            "labels": {OWNER_LABEL: cluster.namespace, **(labels or {})},
        },
        "spec": {
            "automountServiceAccountToken": False,
            "restartPolicy": "Never",
            "securityContext": {"runAsNonRoot": True, "runAsUser": 65532, "runAsGroup": 65532},
            "containers": [
                {
                    "name": "probe",
                    "image": PROBE_IMAGE,
                    "args": arguments,
                    "securityContext": {
                        "allowPrivilegeEscalation": False,
                        "readOnlyRootFilesystem": True,
                        "capabilities": {"drop": ["ALL"]},
                    },
                }
            ],
        },
    }
    cluster.kubectl("apply", "-f", "-", input_text=json.dumps(pod))
    terminal: dict[str, Any] = {}

    def finished() -> bool:
        nonlocal terminal
        current = cluster.json("get", "pod", name, "--ignore-not-found")
        if current.get("status", {}).get("phase") in {"Succeeded", "Failed"}:
            terminal = current
            return True
        return False

    wait_for(f"probe {name}", finished, 45)
    status = terminal["status"]["containerStatuses"][0]["state"]["terminated"]
    succeeded = status.get("exitCode") == 0
    require(succeeded is expect_success, f"probe {name} returned unexpected status")


def endpoint_count(cluster: Cluster) -> int:
    items = cluster.json(
        "get", "endpointslices", "-l", "kubernetes.io/service-name=local-dev-dashboard-api"
    ).get("items", [])
    return sum(
        1
        for item in items
        for endpoint in item.get("endpoints", [])
        if endpoint.get("conditions", {}).get("ready") is True
    )


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def direct_rpc(cluster: Cluster) -> None:
    port = free_port()
    process = subprocess.Popen(
        [
            "kubectl",
            "--context",
            CONTEXT,
            "--namespace",
            cluster.namespace,
            "port-forward",
            "service/local-dev-dashboard-api",
            f"{port}:9090",
        ],
        cwd=ROOT,
        env=safe_environment(),
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    try:

        def reachable() -> bool:
            if process.poll() is not None:
                raise CheckFailed("direct-RPC port-forward exited early")
            try:
                with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                    return True
            except OSError:
                return False

        wait_for("direct-RPC port-forward", reachable, 20)
        command(
            [
                "go",
                "-C",
                "services/dashboard-service",
                "test",
                "-mod=readonly",
                "-count=1",
                "-run=^TestDeployedDirectRPC$",
                "./internal/service",
            ],
            timeout=60,
            environment={
                "GOWORK": "off",
                "DASHBOARD_DEPLOYMENT_GRPC_ADDR": f"127.0.0.1:{port}",
                "DASHBOARD_DEPLOYMENT_GRPC_MESSAGE": "disposable-dashboard-rpc",
            },
            activity="verify deployed direct Dashboard RPC",
        )
        print("PASS deployed direct RPC", flush=True)
    finally:
        require(
            terminate_process_group(process, 10),
            "direct-RPC port-forward cleanup exceeded its deadline",
        )


def run() -> None:
    image = os.environ.get("IMAGE_REF", "")
    require(
        bool(
            re.fullmatch(
                r"insurance-hub-dashboard-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}", image
            )
        ),
        "IMAGE_REF differs",
    )
    require(command(["kubectl", "config", "current-context"]).strip() == CONTEXT, "wrong context")
    inspected = json.loads(command(["docker", "image", "inspect", image]))
    require(len(inspected) == 1, "Dashboard image is absent")
    revision = (
        inspected[0]
        .get("Config", {})
        .get("Labels", {})
        .get("org.opencontainers.image.revision", "")
    )
    source_short = image.rsplit(".", 1)[1]
    require(revision[:12] == source_short, "Dashboard image source differs")
    namespace = f"dashboard-disposable-{os.getpid()}-{secrets.token_hex(3)}"
    cluster = Cluster(namespace)
    created = False
    try:
        command(
            ["kind", "load", "docker-image", image, "--name", KIND_CLUSTER],
            timeout=120,
            activity="load Dashboard image into existing Kind cluster",
        )
        namespace_item = {
            "apiVersion": "v1",
            "kind": "Namespace",
            "metadata": {"name": namespace, "labels": {OWNER_LABEL: namespace}},
        }
        command(
            ["kubectl", "--context", CONTEXT, "apply", "-f", "-"],
            input_text=json.dumps(namespace_item),
            activity="create disposable Dashboard namespace",
        )
        created = True
        rendered, _items = render(cluster, image, source_short)
        cluster.kubectl(
            "apply",
            "--server-side",
            "--field-manager=dashboard-disposable",
            "-f",
            "-",
            input_text=rendered,
        )
        cluster.kubectl(
            "rollout",
            "status",
            "deployment/local-dev-dashboard-api",
            "--timeout=180s",
            timeout=210,
            activity="wait for disposable Dashboard rollout",
        )
        wait_for("Dashboard endpoint ready", lambda: endpoint_count(cluster) == 1)
        probe(
            cluster,
            "business-echo",
            [
                "--fail-with-body",
                "--silent",
                "--show-error",
                "--header",
                "Content-Type: application/json",
                "--data",
                '{"message":"disposable-dashboard"}',
                "http://local-dev-dashboard-api/v1/example/echo",
            ],
            labels={checks.BUSINESS_CLIENT_LABEL: "true"},
        )
        direct_rpc(cluster)
        probe(
            cluster,
            "management-health",
            ["--fail", "--silent", "http://local-dev-dashboard-api-management:8081/readyz"],
            labels={checks.MANAGEMENT_CLIENT_LABEL: "true"},
        )
        probe(
            cluster,
            "unlabeled-denied",
            ["--max-time", "3", "--silent", "http://local-dev-dashboard-api/v1/example/echo"],
            expect_success=False,
        )
        pod = cluster.json("get", "pods", "-l", "app.kubernetes.io/name=dashboard-api")["items"][0][
            "metadata"
        ]["name"]
        cluster.kubectl("delete", "pod", pod, "--wait=false")
        cluster.kubectl(
            "rollout",
            "status",
            "deployment/local-dev-dashboard-api",
            "--timeout=180s",
            timeout=210,
            activity="wait for replacement Dashboard rollout",
        )
        wait_for("Dashboard endpoint recovered", lambda: endpoint_count(cluster) == 1)
        print("dashboard-disposable: Echo, health, CNI denial and restart passed")
    finally:
        if created:
            command(
                [
                    "kubectl",
                    "--context",
                    CONTEXT,
                    "delete",
                    "namespace",
                    namespace,
                    "--wait=true",
                    "--timeout=120s",
                ],
                timeout=150,
                activity="delete disposable Dashboard namespace",
            )
            require(
                not command(
                    [
                        "kubectl",
                        "--context",
                        CONTEXT,
                        "get",
                        "namespace",
                        namespace,
                        "--ignore-not-found",
                    ]
                ).strip(),
                "disposable namespace cleanup failed",
            )


if __name__ == "__main__":
    try:
        run()
    except (
        CheckFailed,
        checks.DeploymentError,
        OSError,
        ValueError,
        json.JSONDecodeError,
    ) as error:
        print(f"dashboard-disposable: {error}", file=sys.stderr)
        raise SystemExit(1) from error
