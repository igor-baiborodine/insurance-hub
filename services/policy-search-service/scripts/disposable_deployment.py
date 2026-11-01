#!/usr/bin/env python3
"""Exercise Policy Search in one uniquely owned namespace on the configured Kind cluster."""

from __future__ import annotations

import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time
from contextlib import contextmanager
from pathlib import Path
from typing import Any

import fixture_administration as fixture_admin
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
    config["data"]["ELASTICSEARCH_URL"] = (
        f"http://local-dev-elasticsearch-es-http.{cluster.namespace}.svc:9200"
    )
    policy = next(item for item in items if item["kind"] == "NetworkPolicy")
    for rule in policy["spec"]["egress"]:
        if any(port.get("port") == 9200 for port in rule.get("ports", [])):
            rule["to"][0]["namespaceSelector"]["matchLabels"]["kubernetes.io/metadata.name"] = (
                cluster.namespace
            )
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
    if status.get("exitCode") != 0:
        diagnostic = cluster.kubectl("logs", name).strip().replace("\n", " ")[:300]
        raise CheckFailed(f"probe {name} failed: {diagnostic}")


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
    elasticsearch_image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    require(elasticsearch_image, "ELASTICSEARCH_IMAGE is required")
    command(
        [
            "kind",
            "load",
            "docker-image",
            image,
            probe_image,
            elasticsearch_image,
            "--name",
            KIND_CLUSTER,
        ],
        timeout=300,
    )
    return probe_image


def elasticsearch_resources(cluster: Cluster, image: str) -> str:
    labels = {
        OWNER_LABEL: cluster.namespace,
        "elasticsearch.k8s.elastic.co/cluster-name": "local-dev-elasticsearch",
    }
    service = {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {
            "name": "local-dev-elasticsearch-es-http",
            "namespace": cluster.namespace,
            "labels": labels,
        },
        "spec": {
            "selector": labels,
            "ports": [{"name": "https", "port": 9200, "targetPort": 9200}],
        },
    }
    deployment = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {
            "name": "local-dev-elasticsearch",
            "namespace": cluster.namespace,
            "labels": labels,
        },
        "spec": {
            "replicas": 1,
            "selector": {"matchLabels": labels},
            "template": {
                "metadata": {"labels": labels},
                "spec": {
                    "automountServiceAccountToken": False,
                    "securityContext": {"fsGroup": 1000},
                    "volumes": [{"name": "data", "emptyDir": {}}],
                    "containers": [
                        {
                            "name": "elasticsearch",
                            "image": image,
                            "imagePullPolicy": "IfNotPresent",
                            "ports": [{"containerPort": 9200}],
                            "volumeMounts": [
                                {
                                    "name": "data",
                                    "mountPath": "/usr/share/elasticsearch/data",
                                }
                            ],
                            "readinessProbe": {
                                "httpGet": {"path": "/", "port": 9200},
                                "periodSeconds": 2,
                                "timeoutSeconds": 1,
                                "failureThreshold": 90,
                            },
                            "env": [
                                {"name": "discovery.type", "value": "single-node"},
                                {"name": "xpack.security.enabled", "value": "false"},
                                {
                                    "name": "xpack.security.http.ssl.enabled",
                                    "value": "false",
                                },
                                {
                                    "name": "ES_JAVA_OPTS",
                                    "value": "-Xms512m -Xmx512m",
                                },
                            ],
                            "resources": {
                                "requests": {"cpu": "100m", "memory": "768Mi"},
                                "limits": {"cpu": "1", "memory": "1Gi"},
                            },
                        }
                    ],
                },
            },
        },
    }
    return yaml.safe_dump_all([service, deployment], explicit_start=True, sort_keys=False)


@contextmanager
def port_forward(cluster: Cluster):
    port = 0
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = int(listener.getsockname()[1])
    process = subprocess.Popen(
        [
            "kubectl",
            "--context",
            CONTEXT,
            "--namespace",
            cluster.namespace,
            "port-forward",
            "service/local-dev-elasticsearch-es-http",
            f"{port}:9200",
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    try:
        deadline = time.monotonic() + 5
        stable_since = time.monotonic()
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise CheckFailed("Elasticsearch port-forward stopped early")
            if time.monotonic() - stable_since >= 1:
                break
            time.sleep(0.1)
        else:
            raise CheckFailed("Elasticsearch port-forward did not become ready")
        yield port
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)


def setup_elasticsearch(cluster: Cluster, image: str) -> None:
    cluster.kubectl(
        "apply",
        "-f",
        "-",
        input_text=elasticsearch_resources(cluster, image),
    )
    cluster.kubectl(
        "rollout",
        "status",
        "deployment/local-dev-elasticsearch",
        "--timeout=180s",
        timeout=200,
    )
    fixture = fixture_admin.load_fixture()
    with port_forward(cluster) as port:
        admin = fixture_admin.ElasticsearchClient(port, "", "")
        started = time.monotonic()
        deadline = started + 180
        heartbeat = started + 30
        last_status = "no response"
        while time.monotonic() < deadline:
            try:
                status, _ = admin.request("GET", "/")
                last_status = f"HTTP {status}"
                if status == 200:
                    break
            except fixture_admin.AdministrationError:
                last_status = "connection failure"
            now = time.monotonic()
            if now >= heartbeat:
                print(
                    "policy-search-disposable: waiting for Elasticsearch "
                    f"after {now - started:.0f}s",
                    file=sys.stderr,
                    flush=True,
                )
                heartbeat = now + 30
            time.sleep(1)
        else:
            pods = cluster.json(
                "get",
                "pods",
                "-l",
                "elasticsearch.k8s.elastic.co/cluster-name=local-dev-elasticsearch",
            ).get("items", [])
            restarts = sum(
                status.get("restartCount", 0)
                for pod in pods
                for status in pod.get("status", {}).get("containerStatuses", [])
            )
            logs = cluster.kubectl(
                "logs", "deployment/local-dev-elasticsearch", "--tail=200"
            ).lower()
            observations = {
                "started": '"message":"started"' in logs,
                "bootstrap": "bootstrap checks failed" in logs,
                "fatal": '"log.level":"fatal"' in logs,
                "exception": "exception" in logs,
            }
            raise CheckFailed(
                f"disposable Elasticsearch did not become ready: {last_status}; restarts={restarts}"
                f"; observations={json.dumps(observations, sort_keys=True)}"
            )
        status, _ = admin.request("PUT", f"/{fixture.index}", {"mappings": fixture.mapping})
        fixture_admin.require_status(status, {200}, "create disposable fixture index")
        status, result = admin.request(
            "POST", "/_bulk?refresh=true", fixture_admin.fixture_bulk(fixture)
        )
        fixture_admin.require_status(status, {200}, "index disposable fixture documents")
        require(
            isinstance(result, dict) and result.get("errors") is False,
            "disposable fixture bulk indexing failed",
        )
        status, result = admin.request(
            "POST",
            f"/{fixture.index}/_search",
            {
                "size": 1,
                "query": {
                    "query_string": {
                        "query": "Synthetic Person",
                        "fields": ["number", "policyHolder"],
                    }
                },
            },
        )
        fixture_admin.require_status(status, {200}, "query disposable fixture")
        require(
            isinstance(result, dict) and result.get("hits", {}).get("hits"),
            "disposable fixture query returned no documents",
        )
    secret = {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {
            "name": "local-dev-policy-search-elasticsearch",
            "namespace": cluster.namespace,
            "labels": {OWNER_LABEL: cluster.namespace},
        },
        "type": "Opaque",
        "stringData": {
            "username": "go_policy_search_reader",
            "password": "disposable-reader-password",
        },
    }
    cluster.kubectl("apply", "-f", "-", input_text=json.dumps(secret))


def exercise(cluster: Cluster, probe_image: str) -> None:
    business = "local-dev-policy-search-api"
    management = "local-dev-policy-search-api-management"
    probe(
        cluster,
        probe_image,
        "allowed-http",
        ["--mode=http", f"--address=http://{business}/policies"],
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
    elasticsearch_image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    require(image.startswith("insurance-hub-policy-search-api:1.0.0-local."), "IMAGE_REF differs")
    require(elasticsearch_image, "ELASTICSEARCH_IMAGE is required")
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
        setup_elasticsearch(cluster, elasticsearch_image)
        rendered, items = render(cluster, image, source_short)
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
        elasticsearch_pods = cluster.json(
            "get",
            "pods",
            "-l",
            "elasticsearch.k8s.elastic.co/cluster-name=local-dev-elasticsearch",
        ).get("items", [])
        require(len(elasticsearch_pods) == 1, "disposable Elasticsearch Pod differs")
        elasticsearch_pod = elasticsearch_pods[0]["metadata"]["name"]
        before_restarts = elasticsearch_pods[0]["status"]["containerStatuses"][0]["restartCount"]
        subprocess.run(
            [
                "kubectl",
                "--context",
                CONTEXT,
                "--namespace",
                cluster.namespace,
                "exec",
                elasticsearch_pod,
                "--",
                "kill",
                "1",
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=15,
            check=False,
            env={
                name: os.environ[name]
                for name in ("HOME", "KUBECONFIG", "PATH", "TMPDIR")
                if name in os.environ
            },
        )
        wait_for(
            cluster,
            "dependency-loss readiness",
            lambda: endpoint_count(cluster) == 0,
        )
        policy_pods = cluster.json(
            "get", "pods", "-l", "app.kubernetes.io/name=policy-search-api"
        ).get("items", [])
        require(
            len(policy_pods) == 1
            and policy_pods[0]["status"]["containerStatuses"][0]["restartCount"] == 0,
            "dependency loss restarted the Policy Search process",
        )
        wait_for(
            cluster,
            "Elasticsearch container restart",
            lambda: (
                cluster.json("get", "pod", elasticsearch_pod)["status"]["containerStatuses"][0][
                    "restartCount"
                ]
                > before_restarts
            ),
            seconds=180,
        )
        wait_for(
            cluster,
            "dependency-recovery readiness",
            lambda: endpoint_count(cluster) == 1,
            seconds=180,
        )
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
            ["--mode=http", "--address=http://local-dev-policy-search-api/policies"],
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
        print(
            "policy-search-disposable: search, policy, dependency recovery, "
            "restart and restoration passed"
        )
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
