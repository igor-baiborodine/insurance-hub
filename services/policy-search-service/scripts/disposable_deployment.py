#!/usr/bin/env python3
"""Exercise Policy Search in one uniquely owned namespace on the configured Kind cluster."""

from __future__ import annotations

import json
import os
import secrets
import subprocess
import tempfile
import time
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
MODULE = ROOT / "services/policy-search-service"
OVERLAY = ROOT / "k8s/overlays/local-dev/svc/policy-search"
CONTEXT = "kind-local-dev-insurance-hub"
KIND_CLUSTER = "local-dev-insurance-hub"
OWNER_LABEL = "insurance-hub.io/policy-search-disposable"


class CheckFailed(RuntimeError):
    """The disposable deployment differed from its owned contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise CheckFailed(message)


def command(arguments: list[str], *, input_text: str | None = None, timeout: int = 60) -> str:
    try:
        result = subprocess.run(
            arguments,
            cwd=ROOT,
            input=input_text,
            text=True,
            capture_output=True,
            timeout=timeout,
            check=False,
            env={
                name: os.environ[name]
                for name in ("HOME", "KUBECONFIG", "PATH", "TMPDIR")
                if name in os.environ
            },
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise CheckFailed(f"command unavailable or timed out: {arguments[0]}") from error
    if result.returncode:
        raise CheckFailed(f"command failed: {arguments[0]} (exit {result.returncode})")
    return result.stdout


class Cluster:
    def __init__(self, namespace: str) -> None:
        self.namespace = namespace

    def kubectl(self, *arguments: str, input_text: str | None = None, timeout: int = 60) -> str:
        return command(
            ["kubectl", "--context", CONTEXT, "--namespace", self.namespace, *arguments],
            input_text=input_text,
            timeout=timeout,
        )

    def json(self, *arguments: str) -> dict[str, Any]:
        return json.loads(self.kubectl(*arguments, "-o", "json") or "{}")


def render(cluster: Cluster, image: str, source_short: str) -> tuple[str, list[dict[str, Any]]]:
    text = cluster.kubectl("kustomize", str(OVERLAY))
    items = [item for item in yaml.safe_load_all(text) if isinstance(item, dict)]
    require(
        {item.get("kind") for item in items}
        == {"ConfigMap", "Deployment", "Service", "NetworkPolicy"},
        "disposable rendered inventory differs",
    )
    for item in items:
        item["metadata"]["namespace"] = cluster.namespace
        item["metadata"].setdefault("labels", {})[OWNER_LABEL] = cluster.namespace
    deployment = next(item for item in items if item["kind"] == "Deployment")
    deployment["spec"]["template"]["metadata"].setdefault("labels", {})[OWNER_LABEL] = (
        cluster.namespace
    )
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = image
    config = next(item for item in items if item["kind"] == "ConfigMap")
    config["data"]["SERVICE_VERSION"] = f"git-{source_short}"
    return yaml.safe_dump_all(items, explicit_start=True, sort_keys=False), items


def wait_for(cluster: Cluster, description: str, predicate, seconds: int = 90) -> None:
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if predicate():
            print(f"PASS {description}", flush=True)
            return
        time.sleep(1)
    raise CheckFailed(f"timed out: {description}")


def terminal(cluster: Cluster, name: str) -> dict[str, Any] | None:
    pod = cluster.json("get", "pod", name, "--ignore-not-found")
    return pod if pod.get("status", {}).get("phase") in {"Succeeded", "Failed"} else None


def probe(
    cluster: Cluster,
    image: str,
    name: str,
    arguments: list[str],
    labels: dict[str, str] | None = None,
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
                    "image": image,
                    "imagePullPolicy": "Never",
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
    completed: dict[str, Any] | None = None

    def finished() -> bool:
        nonlocal completed
        completed = terminal(cluster, name)
        return completed is not None

    wait_for(cluster, f"probe {name}", finished, 45)
    status = completed["status"]["containerStatuses"][0]["state"]["terminated"]
    require(status.get("exitCode") == 0, f"probe {name} failed")


def endpoint_count(cluster: Cluster) -> int:
    slices = cluster.json(
        "get", "endpointslices", "-l", "kubernetes.io/service-name=local-dev-policy-search-api"
    ).get("items", [])
    return sum(
        1
        for item in slices
        for endpoint in item.get("endpoints", [])
        if endpoint.get("conditions", {}).get("ready") is True
    )


def build_probe(image: str, source_short: str) -> str:
    probe_image = f"insurance-hub-policy-search-qualification:git-{source_short}"
    with tempfile.TemporaryDirectory(prefix="policy-search-qualification-") as directory:
        binary = Path(directory) / "qualification-client"
        command(
            [
                "make",
                "-C",
                str(MODULE),
                "build-qualification-client",
                f"QUALIFICATION_CLIENT_OUTPUT={binary}",
            ],
            timeout=180,
        )
        command(
            [
                "docker",
                "build",
                "--tag",
                probe_image,
                "--file",
                str(MODULE / "testdata/qualification/Dockerfile"),
                directory,
            ],
            timeout=180,
        )
    command(
        ["kind", "load", "docker-image", image, probe_image, "--name", KIND_CLUSTER], timeout=180
    )
    return probe_image


def exercise(cluster: Cluster, probe_image: str) -> None:
    business = "local-dev-policy-search-api"
    management = "local-dev-policy-search-api-management"
    probe(
        cluster,
        probe_image,
        "allowed-http",
        ["--mode=http", f"--address=http://{business}/v1/example/echo"],
        {"insurance-hub.io/policy-search-business-client": "true"},
    )
    probe(
        cluster,
        probe_image,
        "allowed-grpc",
        ["--mode=grpc", f"--address={business}:9090"],
        {"insurance-hub.io/policy-search-business-client": "true"},
    )
    probe(
        cluster,
        probe_image,
        "allowed-management",
        ["--mode=health", f"--address=http://{management}:8081/readyz"],
        {"insurance-hub.io/policy-search-management-client": "true"},
    )
    for name, address in (
        ("denied-http", f"{business}:80"),
        ("denied-grpc", f"{business}:9090"),
        ("denied-management", f"{management}:8081"),
    ):
        probe(
            cluster,
            probe_image,
            name,
            ["--mode=tcp", f"--address={address}", "--expect-denied"],
        )


def main() -> int:
    image = os.environ.get("IMAGE_REF", "")
    require(image.startswith("insurance-hub-policy-search-api:1.0.0-local."), "IMAGE_REF differs")
    source_short = image.rsplit(".", 1)[1]
    require(len(source_short) == 12, "IMAGE_REF source identity differs")
    namespace = f"policy-search-disposable-{secrets.token_hex(4)}"
    cluster = Cluster(namespace)
    created = False
    probe_image = ""
    try:
        command(["kubectl", "--context", CONTEXT, "get", "nodes", "-o", "name"])
        probe_image = build_probe(image, source_short)
        namespace_object = {
            "apiVersion": "v1",
            "kind": "Namespace",
            "metadata": {"name": namespace, "labels": {OWNER_LABEL: namespace}},
        }
        created = True
        command(
            ["kubectl", "--context", CONTEXT, "apply", "-f", "-"],
            input_text=json.dumps(namespace_object),
        )
        rendered, _items = render(cluster, image, source_short)
        cluster.kubectl("apply", "-f", "-", input_text=rendered)
        cluster.kubectl(
            "rollout",
            "status",
            "deployment/local-dev-policy-search-api",
            "--timeout=180s",
            timeout=200,
        )
        require(endpoint_count(cluster) == 1, "business endpoint did not become ready")
        exercise(cluster, probe_image)
        cluster.kubectl("delete", "pod", "-l", "app.kubernetes.io/name=policy-search-api")
        cluster.kubectl(
            "rollout",
            "status",
            "deployment/local-dev-policy-search-api",
            "--timeout=180s",
            timeout=200,
        )
        probe(
            cluster,
            probe_image,
            "restart-http",
            ["--mode=http", "--address=http://local-dev-policy-search-api/v1/example/echo"],
            {"insurance-hub.io/policy-search-business-client": "true"},
        )
        cluster.kubectl("scale", "deployment/local-dev-policy-search-api", "--replicas=0")
        wait_for(cluster, "scale-to-zero endpoint removal", lambda: endpoint_count(cluster) == 0)
        cluster.kubectl("apply", "-f", "-", input_text=rendered)
        cluster.kubectl(
            "rollout",
            "status",
            "deployment/local-dev-policy-search-api",
            "--timeout=180s",
            timeout=200,
        )
        require(endpoint_count(cluster) == 1, "restored endpoint did not become ready")
        print("policy-search-disposable: transport, policy, restart and restoration passed")
        return 0
    finally:
        cleanup_errors: list[str] = []
        if created:
            try:
                command(
                    [
                        "kubectl",
                        "--context",
                        CONTEXT,
                        "delete",
                        "namespace",
                        namespace,
                        "--ignore-not-found",
                        "--wait=true",
                        "--timeout=90s",
                    ],
                    timeout=100,
                )
            except CheckFailed:
                cleanup_errors.append(f"namespace {namespace}")
        if probe_image:
            removed = subprocess.run(
                ["docker", "image", "rm", "--force", probe_image],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                check=False,
            )
            if removed.returncode:
                cleanup_errors.append(f"image {probe_image}")
        if cleanup_errors:
            raise CheckFailed("disposable cleanup failed: " + ", ".join(cleanup_errors))
