#!/usr/bin/env python3
"""Exercise Dashboard in a uniquely owned namespace on the configured Kind cluster."""

from __future__ import annotations

import base64
import hashlib
import http.client
import json
import os
import re
import secrets
import socket
import subprocess
import sys
import tempfile
import time
from contextlib import contextmanager
from pathlib import Path
from typing import Any, NamedTuple

import dashboard_deployment_checks as checks
import registration_administration as registration_admin
import retirement_administration as retirement_admin
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
ADMIN_USERNAME = "elastic"
ADMIN_PASSWORD = "issue-287-disposable-admin-password"
READER_USERNAME = "dashboard_disposable_reader"
READER_PASSWORD = "issue-287-disposable-reader-password"
EVENT_READER_USERNAME = retirement_admin.READER
EVENT_READER_PASSWORD = "issue-290-disposable-reader-password"
EVENT_WRITER_USERNAME = retirement_admin.WRITER
EVENT_WRITER_PASSWORD = "issue-290-disposable-writer-password"
CANONICAL_WRITER_USERNAME = "dashboard_disposable_canonical_writer"
CANONICAL_WRITER_PASSWORD = "issue-293-disposable-writer-password"
ELASTICSEARCH_NAME = "dashboard-disposable-elasticsearch"
KAFKA_NAME = "local-dev-kafka"
KAFKA_BOOTSTRAP = "local-dev-kafka-kafka-bootstrap"
CANONICAL_INDEX = "policy_stats"
CANONICAL_TOPIC = "policy-registered"
CANONICAL_GROUP = "dashboard-service"
JAVA_DEPLOYMENT = "local-dev-dashboard-api-legacy"
JAVA_SERVICE = "local-dev-dashboard-api-legacy"
DASHBOARD_HTTP_SERVICE_PORT = 80
HTTP_STATUS_MARKER = "__INSURANCE_HUB_HTTP_STATUS__="
MAX_HTTP_BODY_BYTES = 4096
QUALIFICATION_ASSET = ROOT / "services/dashboard-service/testdata/qualification.json"


class CheckFailed(RuntimeError):
    """The disposable Dashboard deployment differed from its owned contract."""


class HTTPProbeResponse(NamedTuple):
    """A bounded HTTP response captured by a disposable curl Pod."""

    status: int
    body: str


class ImageContentIdentity(NamedTuple):
    """Comparable local, node-runtime and Pod image content digests."""

    local: str
    containerd: str
    pod: str


class CandidateIdentity(NamedTuple):
    """Immutable source and image identity selected for qualification."""

    revision: str
    version: str
    local_image_id: str
    registry_digest: str | None


class JavaCandidateIdentity(NamedTuple):
    """Content identity of the retained Java return candidate."""

    image: str
    local_image_id: str


class OffsetSnapshot(NamedTuple):
    """Committed and broker positions for one owned topic partition."""

    partition: int
    committed: int
    log_end: int


class StateSnapshot(NamedTuple):
    """Persistent state retained while only the Dashboard process is stopped."""

    mapping: dict[str, Any]
    documents: list[dict[str, Any]]
    offsets: list[OffsetSnapshot]


def require(condition: object, message: str) -> None:
    if not condition:
        raise CheckFailed(message)


def load_qualification() -> dict[str, Any]:
    """Load the fixed, independently calculated issue-292 qualification oracle."""

    value = json.loads(QUALIFICATION_ASSET.read_text(encoding="utf-8"))
    measurement = value.get("measurement", {})
    events = value.get("events", [])
    queries = value.get("queries", [])
    require(value.get("schemaVersion") == 1, "qualification schema differs")
    require(
        measurement
        == {
            "requestsPerQuery": 10,
            "maximumSeconds": 300,
            "eventVisibilitySeconds": 30,
            "requestDeadlineSeconds": 5,
        },
        "qualification measurement bounds differ",
    )
    require(
        [event.get("name") for event in events]
        == ["initial", "same-id-replacement", "second-policy", "post-restore"],
        "qualification event sequence differs",
    )
    require(
        [event.get("expectedDocument", {}).get("number") for event in events]
        == ["SYN-292-001", "SYN-292-001", "SYN-292-002", "SYN-292-003"],
        "qualification synthetic identities differ",
    )
    require(
        [query.get("name") for query in queries] == ["total-sales", "agent-sales", "sales-trends"],
        "qualification query set differs",
    )
    for event in events:
        require(
            isinstance(event.get("value"), dict)
            and isinstance(event.get("expectedDocument"), dict),
            "qualification event payload differs",
        )
    for query in queries:
        require(
            isinstance(query.get("request"), dict)
            and isinstance(query.get("expectedResponse"), dict),
            "qualification query oracle differs",
        )
    require(
        isinstance(value.get("postRestoreQuery"), dict),
        "qualification post-restore oracle differs",
    )
    return value


def canonical_query_oracles(qualification: dict[str, Any]) -> list[dict[str, Any]]:
    """Use the retained Java-created canonical date-key representation."""

    queries = json.loads(json.dumps(qualification["queries"]))
    trends = next(query for query in queries if query["name"] == "sales-trends")
    periods = trends["expectedResponse"]["periodSales"]
    require(
        [item["period"] for item in periods] == ["2026-01-01", "2026-01-02"],
        "isolated trends oracle differs before canonical adaptation",
    )
    periods[0]["period"] = "2026-01-01T00:00:00.000Z"
    periods[1]["period"] = "2026-01-02T00:00:00.000Z"
    return queries


def prepare_candidate_image(image: str, expected_revision: str) -> CandidateIdentity:
    """Resolve a local development image or an immutable registry candidate."""

    local_pattern = (
        r"insurance-hub-dashboard-api:1\.[0-9]+\.[0-9]+-local\."
        r"(?!0{12})[0-9a-f]{12}"
    )
    registry_match = re.fullmatch(r".+@(sha256:[0-9a-f]{64})", image)
    is_local = bool(re.fullmatch(local_pattern, image))
    require(is_local or bool(registry_match), "IMAGE_REF is not an accepted immutable candidate")
    if registry_match:
        require(
            bool(re.fullmatch(r"(?!0{40})[0-9a-f]{40}", expected_revision)),
            "QUALIFICATION_SOURCE_REVISION is required for a registry candidate",
        )
        command(
            ["docker", "pull", image],
            timeout=300,
            activity="pull immutable Dashboard qualification image",
        )
    else:
        require(
            not expected_revision,
            "QUALIFICATION_SOURCE_REVISION is only valid for a registry candidate",
        )
    inspected = json.loads(command(["docker", "image", "inspect", image]))
    require(len(inspected) == 1, "Dashboard candidate image is absent")
    item = inspected[0]
    labels = item.get("Config", {}).get("Labels", {})
    revision = str(labels.get("org.opencontainers.image.revision", ""))
    version = str(labels.get("org.opencontainers.image.version", ""))
    local_image_id = str(item.get("Id", ""))
    image_content_digest(local_image_id, "local Docker")
    require(
        bool(re.fullmatch(r"(?!0{40})[0-9a-f]{40}", revision)),
        "Dashboard candidate source revision is invalid",
    )
    require(version == f"git-{revision[:12]}", "Dashboard candidate version differs")
    if registry_match:
        require(revision == expected_revision, "Dashboard candidate source differs")
        repo_digests = item.get("RepoDigests", [])
        require(
            isinstance(repo_digests, list)
            and any(str(value).endswith(f"@{registry_match.group(1)}") for value in repo_digests),
            "local registry candidate digest differs",
        )
    else:
        require(image.endswith(revision[:12]), "Dashboard candidate source differs")
    return CandidateIdentity(
        revision,
        version,
        local_image_id,
        registry_match.group(1) if registry_match else None,
    )


def prepare_kind_candidate(image: str, candidate: CandidateIdentity) -> str:
    """Make the selected content resolvable by the existing single-node Kind cluster."""

    if candidate.registry_digest is None:
        command(
            ["kind", "load", "docker-image", image, "--name", KIND_CLUSTER],
            timeout=120,
            activity="load Dashboard image into existing Kind cluster",
        )
        return image
    runtime_image, separator, _digest = image.partition("@")
    require(bool(separator) and ":" in runtime_image.rsplit("/", 1)[-1], "candidate tag is absent")
    nodes = command(["kind", "get", "nodes", "--name", KIND_CLUSTER]).splitlines()
    require(len(nodes) == 1, "qualification requires the selected single-node Kind cluster")
    command(
        ["docker", "exec", nodes[0], "crictl", "pull", runtime_image],
        timeout=300,
        activity="pull Dashboard candidate tag into Kind containerd",
    )
    inspected = json.loads(
        command(
            ["docker", "exec", nodes[0], "crictl", "inspecti", runtime_image],
            timeout=30,
            activity="inspect Dashboard candidate tag in Kind containerd",
        )
    )
    repo_digests = inspected.get("status", {}).get("repoDigests", [])
    require(
        isinstance(repo_digests, list)
        and any(str(value).endswith(f"@{candidate.registry_digest}") for value in repo_digests),
        "Kind candidate tag does not resolve to the selected registry digest",
    )
    return runtime_image


def prepare_java_candidate(image: str) -> JavaCandidateIdentity:
    """Load and identify the retained Java candidate without inventing source labels."""

    require(
        image == "insurance-hub-dashboard-api-legacy:latest"
        or bool(re.fullmatch(r".+@sha256:[0-9a-f]{64}", image)),
        "QUALIFICATION_JAVA_IMAGE_REF is not an accepted Java candidate",
    )
    if "@sha256:" in image:
        command(
            ["docker", "pull", image],
            timeout=300,
            activity="pull immutable Java Dashboard qualification image",
        )
    details = json.loads(command(["docker", "image", "inspect", image]))
    require(len(details) == 1, "Java Dashboard candidate image is absent")
    local_image_id = str(details[0].get("Id", ""))
    image_content_digest(local_image_id, "local Java Docker")
    command(
        ["kind", "load", "docker-image", image, "--name", KIND_CLUSTER],
        timeout=120,
        activity="load retained Java Dashboard image into existing Kind cluster",
    )
    return JavaCandidateIdentity(image, local_image_id)


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


def dependency_resources(namespace: str, image: str) -> list[dict[str, Any]]:
    labels = {"app.kubernetes.io/name": ELASTICSEARCH_NAME, OWNER_LABEL: namespace}
    return [
        {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": {"name": ELASTICSEARCH_NAME, "namespace": namespace, "labels": labels},
            "type": "Opaque",
            "stringData": {
                "admin-password": ADMIN_PASSWORD,
                "username": READER_USERNAME,
                "reader-password": READER_PASSWORD,
                "event-reader-username": EVENT_READER_USERNAME,
                "event-reader-password": EVENT_READER_PASSWORD,
                "event-writer-username": EVENT_WRITER_USERNAME,
                "event-writer-password": EVENT_WRITER_PASSWORD,
            },
        },
        {
            "apiVersion": "v1",
            "kind": "Service",
            "metadata": {"name": ELASTICSEARCH_NAME, "namespace": namespace, "labels": labels},
            "spec": {
                "selector": {"app.kubernetes.io/name": ELASTICSEARCH_NAME},
                "ports": [{"name": "http", "port": 9200, "targetPort": 9200}],
            },
        },
        {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": {"name": ELASTICSEARCH_NAME, "namespace": namespace, "labels": labels},
            "spec": {
                "replicas": 1,
                "selector": {"matchLabels": {"app.kubernetes.io/name": ELASTICSEARCH_NAME}},
                "template": {
                    "metadata": {"labels": labels},
                    "spec": {
                        "automountServiceAccountToken": False,
                        "containers": [
                            {
                                "name": "elasticsearch",
                                "image": image,
                                "env": [
                                    {"name": "discovery.type", "value": "single-node"},
                                    {"name": "xpack.security.enabled", "value": "true"},
                                    {"name": "xpack.security.http.ssl.enabled", "value": "false"},
                                    {"name": "ES_JAVA_OPTS", "value": "-Xms512m -Xmx512m"},
                                    {
                                        "name": "ELASTIC_PASSWORD",
                                        "valueFrom": {
                                            "secretKeyRef": {
                                                "name": ELASTICSEARCH_NAME,
                                                "key": "admin-password",
                                            }
                                        },
                                    },
                                ],
                                "ports": [{"name": "http", "containerPort": 9200}],
                                "readinessProbe": {
                                    "tcpSocket": {"port": "http"},
                                    "periodSeconds": 2,
                                    "failureThreshold": 60,
                                },
                            }
                        ],
                    },
                },
            },
        },
    ]


def create_kafka_certificates(root: Path, namespace: str) -> dict[str, bytes]:
    ca_key = root / "ca.key"
    ca_certificate = root / "ca.crt"
    server_key = root / "server.key"
    server_request = root / "server.csr"
    server_certificate = root / "server.crt"
    extensions = root / "server.ext"
    keystore = root / "server.p12"
    extensions.write_text(
        "subjectAltName="
        f"DNS:{KAFKA_BOOTSTRAP},DNS:{KAFKA_BOOTSTRAP}.{namespace}.svc,"
        "DNS:localhost,IP:127.0.0.1\n"
        "extendedKeyUsage=serverAuth\n",
        encoding="utf-8",
    )
    for arguments in (
        [
            "openssl",
            "req",
            "-x509",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-days",
            "1",
            "-subj",
            "/CN=dashboard-disposable-ca",
            "-keyout",
            str(ca_key),
            "-out",
            str(ca_certificate),
        ],
        [
            "openssl",
            "req",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-subj",
            f"/CN={KAFKA_BOOTSTRAP}",
            "-keyout",
            str(server_key),
            "-out",
            str(server_request),
        ],
        [
            "openssl",
            "x509",
            "-req",
            "-days",
            "1",
            "-in",
            str(server_request),
            "-CA",
            str(ca_certificate),
            "-CAkey",
            str(ca_key),
            "-CAcreateserial",
            "-extfile",
            str(extensions),
            "-out",
            str(server_certificate),
        ],
        [
            "openssl",
            "pkcs12",
            "-export",
            "-name",
            "kafka",
            "-in",
            str(server_certificate),
            "-inkey",
            str(server_key),
            "-certfile",
            str(ca_certificate),
            "-passout",
            "pass:changeit",
            "-out",
            str(keystore),
        ],
    ):
        command(arguments, timeout=30, activity="create disposable Kafka TLS material")
    return {"ca.crt": ca_certificate.read_bytes(), "server.p12": keystore.read_bytes()}


def kafka_resources(
    namespace: str,
    image: str,
    certificates: dict[str, bytes],
) -> list[dict[str, Any]]:
    labels = {
        OWNER_LABEL: namespace,
        "strimzi.io/cluster": KAFKA_NAME,
        "strimzi.io/component-type": "kafka",
    }
    return [
        {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": {"name": "local-dev-kafka-tls", "namespace": namespace, "labels": labels},
            "type": "Opaque",
            "data": {
                name: base64.b64encode(value).decode("ascii")
                for name, value in certificates.items()
            },
            "stringData": {
                "key-password": "changeit\n",
                "keystore-password": "changeit\n",
                "client.properties": (
                    "security.protocol=SSL\n"
                    "ssl.truststore.type=PEM\n"
                    "ssl.truststore.location=/etc/kafka/secrets/ca.crt\n"
                ),
            },
        },
        {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": {
                "name": "local-dev-dashboard-kafka-ca",
                "namespace": namespace,
                "labels": {OWNER_LABEL: namespace},
            },
            "type": "Opaque",
            "data": {"ca.crt": base64.b64encode(certificates["ca.crt"]).decode("ascii")},
        },
        {
            "apiVersion": "v1",
            "kind": "Service",
            "metadata": {"name": KAFKA_BOOTSTRAP, "namespace": namespace, "labels": labels},
            "spec": {
                "selector": labels,
                "ports": [
                    {"name": "plain", "port": 9092, "targetPort": 9092},
                    {"name": "tls", "port": 9093, "targetPort": 9093},
                ],
            },
        },
        {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": {"name": KAFKA_NAME, "namespace": namespace, "labels": labels},
            "spec": {
                "replicas": 1,
                "selector": {"matchLabels": labels},
                "template": {
                    "metadata": {"labels": labels},
                    "spec": {
                        "automountServiceAccountToken": False,
                        "volumes": [
                            {
                                "name": "tls",
                                "secret": {"secretName": "local-dev-kafka-tls", "defaultMode": 292},
                            }
                        ],
                        "containers": [
                            {
                                "name": "kafka",
                                "image": image,
                                "imagePullPolicy": "IfNotPresent",
                                "ports": [
                                    {"name": "plain", "containerPort": 9092},
                                    {"name": "tls", "containerPort": 9093},
                                    {"name": "controller", "containerPort": 9094},
                                ],
                                "volumeMounts": [
                                    {
                                        "name": "tls",
                                        "mountPath": "/etc/kafka/secrets",
                                        "readOnly": True,
                                    }
                                ],
                                "readinessProbe": {
                                    "tcpSocket": {"port": 9093},
                                    "periodSeconds": 2,
                                    "timeoutSeconds": 1,
                                    "failureThreshold": 90,
                                },
                                "env": [
                                    {"name": "CLUSTER_ID", "value": "MkU3OEVBNTcwNTJENDM2Qk"},
                                    {"name": "KAFKA_NODE_ID", "value": "1"},
                                    {"name": "KAFKA_PROCESS_ROLES", "value": "broker,controller"},
                                    {
                                        "name": "KAFKA_LISTENERS",
                                        "value": (
                                            "PLAINTEXT://0.0.0.0:9092,SSL://0.0.0.0:9093,"
                                            "CONTROLLER://0.0.0.0:9094,"
                                            "INTERNAL://0.0.0.0:9095"
                                        ),
                                    },
                                    {
                                        "name": "KAFKA_ADVERTISED_LISTENERS",
                                        "value": (
                                            f"PLAINTEXT://{KAFKA_BOOTSTRAP}:9092,"
                                            f"SSL://{KAFKA_BOOTSTRAP}:9093,"
                                            "INTERNAL://localhost:9095"
                                        ),
                                    },
                                    {
                                        "name": "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP",
                                        "value": (
                                            "PLAINTEXT:PLAINTEXT,SSL:SSL,"
                                            "CONTROLLER:PLAINTEXT,INTERNAL:PLAINTEXT"
                                        ),
                                    },
                                    {
                                        "name": "KAFKA_CONTROLLER_LISTENER_NAMES",
                                        "value": "CONTROLLER",
                                    },
                                    {
                                        "name": "KAFKA_CONTROLLER_QUORUM_VOTERS",
                                        "value": "1@localhost:9094",
                                    },
                                    {
                                        "name": "KAFKA_INTER_BROKER_LISTENER_NAME",
                                        "value": "INTERNAL",
                                    },
                                    {"name": "KAFKA_SSL_KEYSTORE_FILENAME", "value": "server.p12"},
                                    {"name": "KAFKA_SSL_KEYSTORE_TYPE", "value": "PKCS12"},
                                    {
                                        "name": "KAFKA_SSL_KEYSTORE_CREDENTIALS",
                                        "value": "keystore-password",
                                    },
                                    {"name": "KAFKA_SSL_KEY_CREDENTIALS", "value": "key-password"},
                                    {"name": "KAFKA_SSL_CLIENT_AUTH", "value": "none"},
                                    {
                                        "name": "KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR",
                                        "value": "1",
                                    },
                                    {
                                        "name": "KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR",
                                        "value": "1",
                                    },
                                    {"name": "KAFKA_TRANSACTION_STATE_LOG_MIN_ISR", "value": "1"},
                                    {
                                        "name": "KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS",
                                        "value": "0",
                                    },
                                ],
                                "resources": {
                                    "requests": {"cpu": "100m", "memory": "384Mi"},
                                    "limits": {"cpu": "1", "memory": "768Mi"},
                                },
                            }
                        ],
                    },
                },
            },
        },
    ]


def java_resources(
    namespace: str,
    candidate: JavaCandidateIdentity,
) -> list[dict[str, Any]]:
    """Render the retained Java owner against the disposable canonical resources."""

    labels = {
        OWNER_LABEL: namespace,
        "app.kubernetes.io/name": "dashboard-api-legacy",
        "app.kubernetes.io/component": "api",
    }
    return [
        {
            "apiVersion": "v1",
            "kind": "ConfigMap",
            "metadata": {"name": "local-dev-dashboard-config-legacy", "namespace": namespace},
            "data": {
                "ELASTIC_SCHEME": "http",
                "ELASTIC_HOST": ELASTICSEARCH_NAME,
                "ELASTIC_PORT": "9200",
                "ELASTIC_USERNAME": ADMIN_USERNAME,
                "KAFKA_HOST": KAFKA_BOOTSTRAP,
                "KAFKA_PORT": "9092",
                "TRACING_ZIPKIN_ENABLED": "false",
            },
        },
        {
            "apiVersion": "v1",
            "kind": "Service",
            "metadata": {"name": JAVA_SERVICE, "namespace": namespace, "labels": labels},
            "spec": {
                "selector": labels,
                "ports": [{"name": "http", "port": 80, "targetPort": 8080}],
            },
        },
        {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": {"name": JAVA_DEPLOYMENT, "namespace": namespace, "labels": labels},
            "spec": {
                "replicas": 1,
                "strategy": {"type": "Recreate"},
                "selector": {"matchLabels": labels},
                "template": {
                    "metadata": {"labels": labels},
                    "spec": {
                        "automountServiceAccountToken": False,
                        "containers": [
                            {
                                "name": "dashboard-api-legacy",
                                "image": candidate.image,
                                "imagePullPolicy": "IfNotPresent",
                                "ports": [{"name": "http", "containerPort": 8080}],
                                "envFrom": [
                                    {"configMapRef": {"name": "local-dev-dashboard-config-legacy"}}
                                ],
                                "env": [
                                    {
                                        "name": "ELASTIC_PASSWORD",
                                        "valueFrom": {
                                            "secretKeyRef": {
                                                "name": ELASTICSEARCH_NAME,
                                                "key": "admin-password",
                                            }
                                        },
                                    },
                                    {
                                        "name": "KAFKA_CONSUMERS_DEFAULT_GROUP_ID",
                                        "value": CANONICAL_GROUP,
                                    },
                                    {"name": "MICRONAUT_SERVER_HOST", "value": "0.0.0.0"},
                                ],
                                "readinessProbe": {
                                    "httpGet": {"path": "/health", "port": "http"},
                                    "periodSeconds": 2,
                                    "timeoutSeconds": 2,
                                    "failureThreshold": 90,
                                },
                            }
                        ],
                    },
                },
            },
        },
    ]


def render(
    cluster: Cluster,
    image: str,
    source_short: str,
    elasticsearch_image: str,
    registry_digest: str | None = None,
    *,
    event_mode: str = "disabled",
) -> tuple[str, list[dict[str, Any]]]:
    require(event_mode in {"disabled", "isolated", "canonical"}, "event mode differs")
    text = checks.render("local-dev")
    items = checks.validate_render("local-dev", text)
    for item in items:
        item["metadata"]["namespace"] = cluster.namespace
        item["metadata"].setdefault("labels", {})[OWNER_LABEL] = cluster.namespace
    deployment = checks.resource(items, "Deployment", "local-dev-dashboard-api")
    deployment["spec"]["template"]["metadata"].setdefault("labels", {})[OWNER_LABEL] = (
        cluster.namespace
    )
    dashboard_container = deployment["spec"]["template"]["spec"]["containers"][0]
    dashboard_container["image"] = image
    if registry_digest or re.fullmatch(r".+@sha256:[0-9a-f]{64}", image):
        dashboard_container["imagePullPolicy"] = "IfNotPresent"
    version = f"git-{source_short}"
    deployment["metadata"].setdefault("labels", {})["app.kubernetes.io/version"] = version
    deployment["spec"]["template"]["metadata"].setdefault("labels", {})[
        "app.kubernetes.io/version"
    ] = version
    checks.resource(items, "ConfigMap", "local-dev-dashboard-config")["data"]["SERVICE_VERSION"] = (
        version
    )
    config = checks.resource(items, "ConfigMap", "local-dev-dashboard-config")["data"]
    for name in (
        "EVENT_WORKERS_ENABLED",
        "KAFKA_BROKERS",
        "KAFKA_GROUP",
        "KAFKA_REGISTERED_TOPIC",
        "KAFKA_CA_FILE",
    ):
        config.pop(name, None)
    config.update(
        {
            "ELASTICSEARCH_URL": f"http://{ELASTICSEARCH_NAME}:9200",
            "ELASTICSEARCH_INDEX": "policy_stats",
            "ELASTICSEARCH_USERNAME": READER_USERNAME,
            "ELASTICSEARCH_TIMEOUT": "5s",
            "EVENT_PROCESSING_MODE": event_mode,
            "EVENT_WORKERS_ENABLED": "false" if event_mode == "disabled" else "true",
        }
    )
    environment = dashboard_container["env"]
    environment[:] = [
        item
        for item in environment
        if item.get("name")
        not in {
            "ELASTICSEARCH_USERNAME",
            "ELASTICSEARCH_PASSWORD",
            "ELASTICSEARCH_WRITER_USERNAME",
            "ELASTICSEARCH_WRITER_PASSWORD",
        }
    ]
    environment.extend(
        [
            {
                "name": "ELASTICSEARCH_USERNAME",
                "valueFrom": {"secretKeyRef": {"name": ELASTICSEARCH_NAME, "key": "username"}},
            },
            {
                "name": "ELASTICSEARCH_PASSWORD",
                "valueFrom": {
                    "secretKeyRef": {"name": ELASTICSEARCH_NAME, "key": "reader-password"}
                },
            },
        ]
    )
    dashboard_container["volumeMounts"] = [
        item
        for item in dashboard_container.get("volumeMounts", [])
        if item.get("name") != "kafka-ca"
    ]
    pod_spec = deployment["spec"]["template"]["spec"]
    pod_spec["volumes"] = [
        item for item in pod_spec.get("volumes", []) if item.get("name") != "kafka-ca"
    ]
    policy = checks.resource(items, "NetworkPolicy", "local-dev-dashboard-api")
    for rule in policy["spec"]["ingress"]:
        for source in rule["from"]:
            source["namespaceSelector"]["matchLabels"]["kubernetes.io/metadata.name"] = (
                cluster.namespace
            )
    policy["spec"]["egress"] = [
        {
            "to": [
                {
                    "namespaceSelector": {
                        "matchLabels": {"kubernetes.io/metadata.name": "kube-system"}
                    },
                    "podSelector": {"matchLabels": {"k8s-app": "kube-dns"}},
                }
            ],
            "ports": [{"port": 53, "protocol": "UDP"}, {"port": 53, "protocol": "TCP"}],
        },
        {
            "to": [
                {"podSelector": {"matchLabels": {"app.kubernetes.io/name": ELASTICSEARCH_NAME}}}
            ],
            "ports": [{"port": 9200, "protocol": "TCP"}],
        },
    ]
    if event_mode != "disabled":
        canonical = event_mode == "canonical"
        selected_index = CANONICAL_INDEX if canonical else retirement_admin.INDEX
        selected_group = CANONICAL_GROUP if canonical else retirement_admin.GROUP
        selected_topic = CANONICAL_TOPIC if canonical else retirement_admin.TOPIC
        reader_secret = (
            "local-dev-dashboard-elasticsearch"
            if canonical
            else "local-dev-dashboard-elasticsearch-issue-290"
        )
        writer_secret = (
            "local-dev-dashboard-elasticsearch-writer"
            if canonical
            else "local-dev-dashboard-elasticsearch-writer-issue-290"
        )
        reader_username = READER_USERNAME if canonical else EVENT_READER_USERNAME
        reader_password = READER_PASSWORD if canonical else EVENT_READER_PASSWORD
        writer_username = CANONICAL_WRITER_USERNAME if canonical else EVENT_WRITER_USERNAME
        writer_password = CANONICAL_WRITER_PASSWORD if canonical else EVENT_WRITER_PASSWORD
        config.update(
            {
                "ELASTICSEARCH_INDEX": selected_index,
                "KAFKA_BROKERS": f"{KAFKA_BOOTSTRAP}:9093",
                "KAFKA_GROUP": selected_group,
                "KAFKA_REGISTERED_TOPIC": selected_topic,
                "KAFKA_CA_FILE": "/etc/dashboard/kafka/ca.crt",
            }
        )
        environment[:] = [
            item
            for item in environment
            if item.get("name")
            not in {
                "ELASTICSEARCH_USERNAME",
                "ELASTICSEARCH_PASSWORD",
                "ELASTICSEARCH_WRITER_USERNAME",
                "ELASTICSEARCH_WRITER_PASSWORD",
            }
        ]
        for name, secret, key in (
            ("ELASTICSEARCH_USERNAME", reader_secret, "username"),
            ("ELASTICSEARCH_PASSWORD", reader_secret, "password"),
            (
                "ELASTICSEARCH_WRITER_USERNAME",
                writer_secret,
                "username",
            ),
            (
                "ELASTICSEARCH_WRITER_PASSWORD",
                writer_secret,
                "password",
            ),
        ):
            environment.append(
                {
                    "name": name,
                    "valueFrom": {"secretKeyRef": {"name": secret, "key": key}},
                }
            )
        container = deployment["spec"]["template"]["spec"]["containers"][0]
        container.setdefault("volumeMounts", []).append(
            {"name": "kafka-ca", "mountPath": "/etc/dashboard/kafka", "readOnly": True}
        )
        deployment["spec"]["template"]["spec"].setdefault("volumes", []).append(
            {
                "name": "kafka-ca",
                "secret": {
                    "secretName": "local-dev-dashboard-kafka-ca",
                    "items": [{"key": "ca.crt", "path": "ca.crt"}],
                    "defaultMode": 292,
                },
            }
        )
        policy["spec"]["egress"].append(
            {
                "to": [
                    {
                        "podSelector": {
                            "matchLabels": {
                                "strimzi.io/cluster": KAFKA_NAME,
                                "strimzi.io/component-type": "kafka",
                            }
                        }
                    }
                ],
                "ports": [{"port": 9093, "protocol": "TCP"}],
            }
        )
        for name, username, password, component in (
            (
                reader_secret,
                reader_username,
                reader_password,
                "elasticsearch-reader",
            ),
            (
                writer_secret,
                writer_username,
                writer_password,
                "elasticsearch-writer",
            ),
        ):
            items.append(
                {
                    "apiVersion": "v1",
                    "kind": "Secret",
                    "metadata": {
                        "name": name,
                        "namespace": cluster.namespace,
                        "labels": {
                            OWNER_LABEL: cluster.namespace,
                            "app.kubernetes.io/component": component,
                        },
                    },
                    "type": "Opaque",
                    "stringData": {"username": username, "password": password},
                }
            )
    items.extend(dependency_resources(cluster.namespace, elasticsearch_image))
    return yaml.safe_dump_all(items, explicit_start=True, sort_keys=False), items


def wait_for(description: str, predicate, seconds: int = 90) -> None:
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if predicate():
            print(f"PASS {description}", flush=True)
            return
        time.sleep(1)
    raise CheckFailed(f"timed out: {description}")


def kafka_cli(
    cluster: Cluster,
    executable: str,
    *arguments: str,
    input_text: str | None = None,
    timeout: int = 60,
) -> str:
    invocation = ["exec"]
    if input_text is not None:
        invocation.append("--stdin")
    return cluster.kubectl(
        *invocation,
        f"deployment/{KAFKA_NAME}",
        "--",
        f"/opt/kafka/bin/{executable}",
        *arguments,
        input_text=input_text,
        timeout=timeout,
    )


def setup_kafka(cluster: Cluster, image: str) -> bytes:
    with tempfile.TemporaryDirectory(prefix="dashboard-kind-kafka-") as directory:
        certificates = create_kafka_certificates(Path(directory), cluster.namespace)
        cluster.kubectl(
            "apply",
            "-f",
            "-",
            input_text=yaml.safe_dump_all(
                kafka_resources(cluster.namespace, image, certificates),
                explicit_start=True,
                sort_keys=False,
            ),
        )
    cluster.kubectl(
        "rollout",
        "status",
        f"deployment/{KAFKA_NAME}",
        "--timeout=180s",
        timeout=210,
        activity="wait for disposable TLS Kafka rollout",
    )

    def ready() -> bool:
        try:
            kafka_cli(
                cluster,
                "kafka-topics.sh",
                "--bootstrap-server",
                "localhost:9093",
                "--command-config",
                "/etc/kafka/secrets/client.properties",
                "--list",
                timeout=15,
            )
            return True
        except CheckFailed:
            return False

    wait_for("TLS Kafka ready", ready, 120)
    kafka_cli(
        cluster,
        "kafka-topics.sh",
        "--bootstrap-server",
        "localhost:9093",
        "--command-config",
        "/etc/kafka/secrets/client.properties",
        "--create",
        "--if-not-exists",
        "--topic",
        retirement_admin.TOPIC,
        "--partitions",
        "1",
        "--replication-factor",
        "1",
    )
    return certificates["ca.crt"]


@contextmanager
def forwarded_service(cluster: Cluster, service: str, remote_port: int):
    port = free_port()
    process = subprocess.Popen(
        [
            "kubectl",
            "--context",
            CONTEXT,
            "--namespace",
            cluster.namespace,
            "port-forward",
            f"service/{service}",
            f"{port}:{remote_port}",
        ],
        cwd=ROOT,
        env=safe_environment(),
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    try:
        wait_for(
            f"{service} port-forward",
            lambda: process.poll() is None and socket_connectable(port),
            20,
        )
        yield port
    finally:
        require(
            terminate_process_group(process, 10),
            f"{service} port-forward cleanup exceeded its deadline",
        )


def socket_connectable(port: int) -> bool:
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=0.2):
            return True
    except OSError:
        return False


def verify_kafka_tls(cluster: Cluster, certificate: bytes) -> None:
    with tempfile.TemporaryDirectory(prefix="dashboard-kafka-tls-") as directory:
        ca_path = Path(directory) / "ca.crt"
        ca_path.write_bytes(certificate)
        with forwarded_service(cluster, KAFKA_BOOTSTRAP, 9093) as port:
            accepted = subprocess.run(
                [
                    "openssl",
                    "s_client",
                    "-connect",
                    f"127.0.0.1:{port}",
                    "-servername",
                    KAFKA_BOOTSTRAP,
                    "-verify_hostname",
                    KAFKA_BOOTSTRAP,
                    "-verify_return_error",
                    "-CAfile",
                    str(ca_path),
                ],
                input="",
                text=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=15,
                check=False,
                env=safe_environment(),
            )
            rejected = subprocess.run(
                [
                    "openssl",
                    "s_client",
                    "-connect",
                    f"127.0.0.1:{port}",
                    "-servername",
                    KAFKA_BOOTSTRAP,
                    "-verify_hostname",
                    "canonical-kafka.invalid",
                    "-verify_return_error",
                    "-CAfile",
                    str(ca_path),
                ],
                input="",
                text=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=15,
                check=False,
                env=safe_environment(),
            )
    require(accepted.returncode == 0, "selected Kafka CA/hostname TLS verification failed")
    require(rejected.returncode != 0, "wrong Kafka hostname was accepted")


def probe(
    cluster: Cluster,
    name: str,
    arguments: list[str],
    *,
    labels: dict[str, str] | None = None,
    expect_success: bool = True,
    seconds: int = 45,
) -> str:
    pod = {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {
            "name": name,
            "namespace": cluster.namespace,
            "labels": {
                OWNER_LABEL: cluster.namespace,
                "app.kubernetes.io/name": "dashboard-qualification",
                "app.kubernetes.io/component": "probe",
                "insurance-hub.io/owner": "issue-287",
                **(labels or {}),
            },
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

    wait_for(f"probe {name}", finished, seconds)
    status = terminal["status"]["containerStatuses"][0]["state"]["terminated"]
    logs = cluster.kubectl("logs", name)
    succeeded = status.get("exitCode") == 0
    diagnostic = logs.strip().replace("\n", " ")[:400]
    require(
        succeeded is expect_success,
        f"probe {name} returned exit {status.get('exitCode')}; output={diagnostic!r}",
    )
    return logs


def http_probe(
    cluster: Cluster,
    name: str,
    arguments: list[str],
    *,
    labels: dict[str, str] | None = None,
    seconds: int = 45,
) -> HTTPProbeResponse:
    logs = probe(
        cluster,
        name,
        [
            "--silent",
            "--show-error",
            "--max-filesize",
            str(MAX_HTTP_BODY_BYTES),
            "--write-out",
            f"\n{HTTP_STATUS_MARKER}%{{http_code}}",
            *arguments,
        ],
        labels=labels,
        seconds=seconds,
    )
    body, separator, raw_status = logs.rpartition(f"\n{HTTP_STATUS_MARKER}")
    require(bool(separator), f"HTTP probe {name} did not report a response status")
    raw_status = raw_status.strip()
    require(bool(re.fullmatch(r"[1-5][0-9]{2}", raw_status)), f"HTTP probe {name} status differs")
    require(
        len(body.encode("utf-8")) <= MAX_HTTP_BODY_BYTES,
        f"HTTP probe {name} response body exceeded its bound",
    )
    return HTTPProbeResponse(int(raw_status), body)


def require_http_response(
    name: str, response: HTTPProbeResponse, expected_status: int, expected_body: str
) -> None:
    require(
        response.status == expected_status and response.body == expected_body,
        f"HTTP probe {name} response differs: status={response.status}; body={response.body!r}",
    )


def require_elasticsearch_permission_denied(name: str, response: HTTPProbeResponse) -> None:
    try:
        payload = json.loads(response.body)
    except json.JSONDecodeError as error:
        raise CheckFailed(f"Elasticsearch probe {name} denial body is not JSON") from error
    error = payload.get("error", {}) if isinstance(payload, dict) else {}
    require(
        response.status == 403
        and payload.get("status") == 403
        and isinstance(error, dict)
        and error.get("type") == "security_exception",
        f"Elasticsearch probe {name} did not return the expected permission denial",
    )


def image_content_digest(value: str, surface: str) -> str:
    match = re.search(r"(?:^|@)(sha256:[0-9a-f]{64})$", value)
    require(bool(match), f"{surface} image content digest is invalid")
    return match.group(1) if match else ""


def require_matching_image_content(
    local_image_id: str,
    image: str,
    containerd_image: dict[str, Any],
    pod_image_id: str,
    registry_digest: str | None = None,
) -> ImageContentIdentity:
    require(isinstance(containerd_image, dict), "containerd image inspection is invalid")
    containerd_status = containerd_image.get("status", {})
    require(isinstance(containerd_status, dict), "containerd image status is absent")
    registry_match = re.fullmatch(r".+@(sha256:[0-9a-f]{64})", image)
    selected_digest = registry_digest or (registry_match.group(1) if registry_match else None)
    if selected_digest:
        repo_digests = containerd_status.get("repoDigests", [])
        available_digests = {
            image_content_digest(str(value), "containerd repository")
            for value in repo_digests
            if isinstance(value, str)
        }
        require(
            isinstance(repo_digests, list) and selected_digest in available_digests,
            "containerd image digest differs from the selected Dashboard image",
        )
        local_digest = image_content_digest(local_image_id, "local Docker")
        containerd_id = image_content_digest(str(containerd_status.get("id", "")), "containerd")
        pod_digest = image_content_digest(pod_image_id, "Pod runtime")
        require(
            local_digest == containerd_id,
            "Kind containerd content differs from the selected Dashboard image",
        )
        require(
            pod_digest in available_digests,
            "running Pod identity is absent from the selected containerd image",
        )
        return ImageContentIdentity(local_digest, selected_digest, pod_digest)
    expected_tag = f"docker.io/library/{image}"
    repo_tags = containerd_status.get("repoTags", [])
    require(
        isinstance(repo_tags, list) and expected_tag in repo_tags,
        "containerd image tag differs from the loaded Dashboard image",
    )
    identity = ImageContentIdentity(
        image_content_digest(local_image_id, "local Docker"),
        image_content_digest(str(containerd_status.get("id", "")), "containerd"),
        image_content_digest(pod_image_id, "Pod runtime"),
    )
    require(
        identity.local == identity.containerd,
        "Kind containerd content differs from the local Dashboard image",
    )
    require(
        identity.pod == identity.containerd,
        "running Pod content differs from the loaded Dashboard image",
    )
    return identity


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


def setup_elasticsearch(cluster: Cluster, suffix: str = "") -> None:
    definition = registration_admin.load_definition()["mapping"]
    base = [
        "--fail-with-body",
        "--silent",
        "--show-error",
        "--retry",
        "60",
        "--retry-all-errors",
        "--retry-delay",
        "1",
        "--user",
        f"{ADMIN_USERNAME}:{ADMIN_PASSWORD}",
        "--header",
        "Content-Type: application/json",
    ]
    requests = (
        (
            "fixture-role",
            "PUT",
            "/_security/role/dashboard_disposable_reader",
            '{"cluster":[],"indices":[{"names":["policy_stats"],"privileges":["read","view_index_metadata"]}]}',
        ),
        (
            "fixture-user",
            "POST",
            "/_security/user/dashboard_disposable_reader",
            json.dumps({"password": READER_PASSWORD, "roles": ["dashboard_disposable_reader"]}),
        ),
        (
            "canonical-writer-role",
            "PUT",
            f"/_security/role/{CANONICAL_WRITER_USERNAME}",
            json.dumps(
                {
                    "cluster": [],
                    "indices": [{"names": [CANONICAL_INDEX], "privileges": ["index"]}],
                },
                separators=(",", ":"),
            ),
        ),
        (
            "canonical-writer-user",
            "PUT",
            f"/_security/user/{CANONICAL_WRITER_USERNAME}",
            json.dumps(
                {
                    "password": CANONICAL_WRITER_PASSWORD,
                    "roles": [CANONICAL_WRITER_USERNAME],
                },
                separators=(",", ":"),
            ),
        ),
    )
    for name, method, path, body in requests:
        probe(
            cluster,
            name + suffix,
            [*base, "--request", method, "--data", body, f"http://{ELASTICSEARCH_NAME}:9200{path}"],
            seconds=150,
        )
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        client = retirement_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        status, value = client.request("GET", f"/{CANONICAL_INDEX}/_mapping")
        if status == 404:
            status, _ = client.request(
                "PUT",
                f"/{CANONICAL_INDEX}",
                {
                    "settings": {"number_of_shards": 1, "number_of_replicas": 0},
                    "mappings": definition,
                },
            )
            require(status == 200, "create ordinary registration index failed")
        else:
            require(
                status == 200
                and isinstance(value, dict)
                and set(value) == {CANONICAL_INDEX}
                and registration_admin.mapping_is_compatible(
                    value[CANONICAL_INDEX].get("mappings", {}),
                    definition,
                ),
                "ordinary registration index differs",
            )
        status, _ = client.request("GET", "/unrelated_stats/_mapping")
        if status == 404:
            status, _ = client.request(
                "PUT",
                "/unrelated_stats",
                {"settings": {"number_of_shards": 1, "number_of_replicas": 0}},
            )
            require(status == 200, "create unrelated permission fixture failed")
        else:
            require(status == 200, "unrelated permission fixture differs")


def verify_reader_permissions(cluster: Cluster) -> None:
    base = [
        "--user",
        f"{READER_USERNAME}:{READER_PASSWORD}",
        "--header",
        "Content-Type: application/json",
    ]
    requests = (
        ("reader-write-denied", ["--request", "PUT", "--data", "{}", "/policy_stats/_doc/denied"]),
        ("reader-admin-denied", ["/_cluster/settings"]),
        ("reader-index-denied", ["/unrelated_stats/_search"]),
    )
    failures: list[str] = []
    for name, arguments in requests:
        try:
            path = arguments[-1]
            response = http_probe(
                cluster,
                name,
                [*base, *arguments[:-1], f"http://{ELASTICSEARCH_NAME}:9200{path}"],
            )
            require_elasticsearch_permission_denied(name, response)
        except (CheckFailed, checks.DeploymentError, OSError, ValueError) as error:
            failures.append(f"{name}: {error}")
    require(not failures, "reader permission checks failed: " + " | ".join(failures))


def verify_registration_writer_permissions(cluster: Cluster) -> None:
    base = [
        "--user",
        f"{CANONICAL_WRITER_USERNAME}:{CANONICAL_WRITER_PASSWORD}",
        "--header",
        "Content-Type: application/json",
    ]
    denied = (
        ("registration-writer-read-denied", [f"/{CANONICAL_INDEX}/_count"]),
        (
            "registration-writer-delete-denied",
            ["--request", "DELETE", f"/{CANONICAL_INDEX}/_doc/denied"],
        ),
        (
            "registration-writer-other-index-denied",
            ["--request", "PUT", "--data", "{}", "/unrelated_stats/_doc/denied"],
        ),
        ("registration-writer-security-denied", ["/_security/role"]),
    )
    failures: list[str] = []
    for name, arguments in denied:
        try:
            path = arguments[-1]
            response = http_probe(
                cluster,
                name,
                [*base, *arguments[:-1], f"http://{ELASTICSEARCH_NAME}:9200{path}"],
            )
            require_elasticsearch_permission_denied(name, response)
        except (CheckFailed, checks.DeploymentError, OSError, ValueError) as error:
            failures.append(f"{name}: {error}")
    require(not failures, "registration writer permission checks failed: " + " | ".join(failures))


def verify_event_permissions(cluster: Cluster) -> None:
    reader_base = [
        "--user",
        f"{EVENT_READER_USERNAME}:{EVENT_READER_PASSWORD}",
        "--header",
        "Content-Type: application/json",
    ]
    response = http_probe(
        cluster,
        "event-reader-count",
        [*reader_base, f"http://{ELASTICSEARCH_NAME}:9200/{retirement_admin.INDEX}/_count"],
    )
    require(response.status == 200, "event reader could not count the isolated index")
    response = http_probe(
        cluster,
        "event-reader-write-denied",
        [
            *reader_base,
            "--request",
            "PUT",
            "--data",
            "{}",
            f"http://{ELASTICSEARCH_NAME}:9200/{retirement_admin.INDEX}/_doc/denied",
        ],
    )
    require_elasticsearch_permission_denied("event-reader-write-denied", response)

    writer_base = [
        "--user",
        f"{EVENT_WRITER_USERNAME}:{EVENT_WRITER_PASSWORD}",
        "--header",
        "Content-Type: application/json",
    ]
    response = http_probe(
        cluster,
        "event-writer-mapping-limit",
        [
            *writer_base,
            "--request",
            "PUT",
            "--data",
            '{"properties":{"number":{"type":"keyword"}}}',
            f"http://{ELASTICSEARCH_NAME}:9200/{retirement_admin.INDEX}/_mapping",
        ],
    )
    require(response.status == 200, "accepted writer mapping authority was not observed")
    denied = (
        ("event-writer-read-denied", [f"/{retirement_admin.INDEX}/_count"]),
        (
            "event-writer-delete-denied",
            ["--request", "DELETE", f"/{retirement_admin.INDEX}/_doc/denied"],
        ),
        (
            "event-writer-other-index-denied",
            [
                "--request",
                "PUT",
                "--data",
                "{}",
                "/policy_stats/_doc/denied",
            ],
        ),
        ("event-writer-security-denied", ["/_security/role"]),
    )
    failures: list[str] = []
    for name, arguments in denied:
        try:
            path = arguments[-1]
            response = http_probe(
                cluster,
                name,
                [*writer_base, *arguments[:-1], f"http://{ELASTICSEARCH_NAME}:9200{path}"],
            )
            require_elasticsearch_permission_denied(name, response)
        except (CheckFailed, checks.DeploymentError, OSError, ValueError) as error:
            failures.append(f"{name}: {error}")
    require(not failures, "event permission checks failed: " + " | ".join(failures))


def verify_runtime_identity(
    cluster: Cluster,
    version: str,
    image: str,
    local_image_id: str,
    registry_digest: str | None = None,
) -> None:
    pod = cluster.json("get", "pods", "-l", "app.kubernetes.io/name=dashboard-api")["items"][0]
    configured_image = pod["spec"]["containers"][0]["image"]
    pod_status = pod["status"]["containerStatuses"][0]
    require(configured_image == image, "deployed Dashboard image reference differs")
    node = pod["spec"].get("nodeName", "")
    require(bool(node), "deployed Dashboard node identity is absent")
    containerd_image = json.loads(
        command(
            ["docker", "exec", node, "crictl", "inspecti", configured_image],
            timeout=30,
            activity="inspect loaded Dashboard image in Kind containerd",
        )
    )
    content = require_matching_image_content(
        local_image_id,
        image,
        containerd_image,
        str(pod_status.get("imageID", "")),
        registry_digest,
    )
    logs = cluster.kubectl("logs", pod["metadata"]["name"])
    entries = []
    for line in logs.splitlines():
        try:
            entry = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(entry, dict):
            entries.append(entry)
    require(
        any(
            entry.get("msg") == "service started" and entry.get("service_version") == version
            for entry in entries
        ),
        "deployed runtime identity differs from the source version",
    )
    print(
        f"PASS runtime identity {version}; configured={configured_image}; "
        f"node={node}; runtime={pod_status.get('image')}; "
        f"localID={content.local}; containerdID={content.containerd}; podID={content.pod}",
        flush=True,
    )


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def require_go_test_passed(output: str, test_name: str) -> None:
    passed = False
    for line in output.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if event.get("Test") == test_name and event.get("Action") == "pass":
            passed = True
    require(passed, f"selected Go test did not execute and pass: {test_name}")


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
        test_name = "TestDeployedDirectRPC"
        output = command(
            [
                os.environ.get("MAKE_COMMAND", "make"),
                "--no-print-directory",
                "-C",
                "services/dashboard-service",
                "_deploy-smoke-rpc",
                f"TOOLS_DIR={os.environ.get('TOOLS_DIR', '')}",
                f"COMMON_GO_TOOLING_PYTHON={sys.executable}",
            ],
            timeout=60,
            environment={
                "GO": os.environ.get("GO", "go"),
                "DASHBOARD_DEPLOYMENT_GRPC_ADDR": f"127.0.0.1:{port}",
                "DASHBOARD_DEPLOYMENT_RPC_SCENARIO": "owned-fixture",
            },
            activity="verify deployed direct Dashboard RPC",
        )
        require_go_test_passed(output, test_name)
        print("PASS deployed direct RPC", flush=True)
    finally:
        require(
            terminate_process_group(process, 10),
            "direct-RPC port-forward cleanup exceeded its deadline",
        )


def produce_qualification_event(
    cluster: Cluster,
    event: dict[str, Any],
    topic: str = retirement_admin.TOPIC,
) -> None:
    kafka_cli(
        cluster,
        "kafka-console-producer.sh",
        "--bootstrap-server",
        "localhost:9093",
        "--producer.config",
        "/etc/kafka/secrets/client.properties",
        "--topic",
        topic,
        input_text=json.dumps(event["value"], separators=(",", ":")) + "\n",
        timeout=20,
    )


def wait_for_qualification_document(
    client: retirement_admin.ElasticsearchClient,
    event: dict[str, Any],
    visibility_seconds: int,
    index: str = retirement_admin.INDEX,
    *,
    java_null_compatibility: bool = False,
) -> float:
    expected = dict(event["expectedDocument"])
    if java_null_compatibility and expected.get("agentLogin") is None:
        expected.pop("agentLogin")
    document_id = expected["number"]
    started = time.monotonic()
    deadline = started + visibility_seconds
    last_result: Any = None
    while time.monotonic() < deadline:
        try:
            status, result = client.request("GET", f"/{index}/_doc/{document_id}?refresh=true")
        except retirement_admin.AdministrationError:
            status, result = 0, None
        last_result = result
        if status == 200 and isinstance(result, dict) and result.get("_source") == expected:
            return time.monotonic() - started
        time.sleep(0.25)
    raise CheckFailed(
        f"qualification event {event['name']} was not visible within its bound; "
        f"last synthetic document={last_result!r}"
    )


def run_qualification_events(
    cluster: Cluster,
    client: retirement_admin.ElasticsearchClient,
    events: list[dict[str, Any]],
    visibility_seconds: int,
    *,
    topic: str = retirement_admin.TOPIC,
    index: str = retirement_admin.INDEX,
    java_null_compatibility: bool = False,
) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []
    for event in events:
        produce_qualification_event(cluster, event, topic)
        elapsed = wait_for_qualification_document(
            client,
            event,
            visibility_seconds,
            index,
            java_null_compatibility=java_null_compatibility,
        )
        results.append({"name": event["name"], "visibilitySeconds": round(elapsed, 3)})
        print(f"PASS qualification event {event['name']} visible in {elapsed:.3f}s", flush=True)
    return results


def post_dashboard_query(
    connection: http.client.HTTPConnection,
    query: dict[str, Any],
) -> None:
    payload = json.dumps(query["request"], separators=(",", ":"))
    try:
        connection.request(
            "POST",
            query["path"],
            body=payload,
            headers={"Content-Type": "application/json"},
        )
        response = connection.getresponse()
        data = response.read(MAX_HTTP_BODY_BYTES + 1)
    except (OSError, http.client.HTTPException) as error:
        raise CheckFailed(f"qualification query {query['name']} failed") from error
    require(len(data) <= MAX_HTTP_BODY_BYTES, "qualification query response exceeded its bound")
    try:
        body = json.loads(data)
    except json.JSONDecodeError as error:
        raise CheckFailed(f"qualification query {query['name']} response is not JSON") from error
    require(
        response.status == 200 and body == query["expectedResponse"],
        f"qualification query {query['name']} response differs",
    )


def run_query_workload(
    cluster: Cluster,
    queries: list[dict[str, Any]],
    repetitions: int,
    request_deadline: int,
    service: str = "local-dev-dashboard-api",
) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []
    with forwarded_service(cluster, service, DASHBOARD_HTTP_SERVICE_PORT) as port:
        connection = http.client.HTTPConnection("127.0.0.1", port, timeout=request_deadline)
        try:
            for query in queries:
                started = time.monotonic()
                for _request_number in range(repetitions):
                    post_dashboard_query(connection, query)
                elapsed = time.monotonic() - started
                results.append(
                    {
                        "name": query["name"],
                        "requests": repetitions,
                        "elapsedSeconds": round(elapsed, 3),
                    }
                )
                print(
                    f"PASS qualification query {query['name']} x{repetitions} in {elapsed:.3f}s",
                    flush=True,
                )
        finally:
            connection.close()
    return results


def parse_consumer_group_offsets(
    output: str,
    group: str = retirement_admin.GROUP,
    topic: str = retirement_admin.TOPIC,
) -> list[OffsetSnapshot]:
    offsets: list[OffsetSnapshot] = []
    for line in output.splitlines():
        fields = line.split()
        if len(fields) < 5 or fields[0] == "GROUP":
            continue
        if fields[0] != group or fields[1] != topic:
            continue
        try:
            offsets.append(OffsetSnapshot(int(fields[2]), int(fields[3]), int(fields[4])))
        except ValueError as error:
            raise CheckFailed("owned Kafka offset output differs") from error
    require(bool(offsets), "owned Kafka offsets are absent")
    require(
        len({offset.partition for offset in offsets}) == len(offsets),
        "owned Kafka offsets contain duplicate partitions",
    )
    return sorted(offsets)


def consumer_group_offsets(
    cluster: Cluster,
    group: str = retirement_admin.GROUP,
    topic: str = retirement_admin.TOPIC,
) -> list[OffsetSnapshot]:
    output = kafka_cli(
        cluster,
        "kafka-consumer-groups.sh",
        "--bootstrap-server",
        "localhost:9093",
        "--command-config",
        "/etc/kafka/secrets/client.properties",
        "--describe",
        "--group",
        group,
    )
    return parse_consumer_group_offsets(output, group, topic)


def wait_for_committed_offsets(
    cluster: Cluster,
    minimum: dict[int, int],
    seconds: int = 30,
    *,
    group: str = retirement_admin.GROUP,
    topic: str = retirement_admin.TOPIC,
) -> list[OffsetSnapshot]:
    deadline = time.monotonic() + seconds
    observed: list[OffsetSnapshot] = []
    while time.monotonic() < deadline:
        try:
            observed = consumer_group_offsets(cluster, group, topic)
        except CheckFailed:
            observed = []
        by_partition = {offset.partition: offset for offset in observed}
        if minimum and all(
            partition in by_partition and by_partition[partition].committed >= value
            for partition, value in minimum.items()
        ):
            return observed
        time.sleep(0.5)
    raise CheckFailed("owned Kafka committed offsets did not reach the expected positions")


def capture_state(
    client: retirement_admin.ElasticsearchClient,
    offsets: list[OffsetSnapshot],
    index: str = retirement_admin.INDEX,
) -> StateSnapshot:
    status, mapping_result = client.request("GET", f"/{index}/_mapping")
    require(status == 200 and isinstance(mapping_result, dict), "qualification mapping is absent")
    mapping = mapping_result.get(index, {}).get("mappings")
    require(isinstance(mapping, dict), "qualification mapping differs")
    status, search_result = client.request(
        "POST",
        f"/{index}/_search",
        {"size": 100, "query": {"prefix": {"number": "SYN-292-"}}},
    )
    require(status == 200 and isinstance(search_result, dict), "qualification documents are absent")
    hits = search_result.get("hits", {}).get("hits", [])
    require(isinstance(hits, list), "qualification document response differs")
    documents = sorted(
        [
            {"id": str(hit.get("_id", "")), "source": hit.get("_source")}
            for hit in hits
            if isinstance(hit, dict)
        ],
        key=lambda item: item["id"],
    )
    return StateSnapshot(mapping, documents, offsets)


def require_same_state(name: str, expected: StateSnapshot, actual: StateSnapshot) -> None:
    require(actual.mapping == expected.mapping, f"{name} mapping changed")
    require(actual.documents == expected.documents, f"{name} documents changed")
    require(actual.offsets == expected.offsets, f"{name} committed offsets changed")


def safe_state_record(state: StateSnapshot) -> dict[str, Any]:
    mapping_text = json.dumps(state.mapping, separators=(",", ":"), sort_keys=True)
    return {
        "mappingSha256": hashlib.sha256(mapping_text.encode()).hexdigest(),
        "documents": state.documents,
        "offsets": [offset._asdict() for offset in state.offsets],
    }


def deployment_configuration_sha256(cluster: Cluster) -> str:
    deployment = cluster.json("get", "deployment", "local-dev-dashboard-api")
    config = cluster.json("get", "configmap", "local-dev-dashboard-config")
    selected = {
        "template": deployment.get("spec", {}).get("template"),
        "config": config.get("data"),
    }
    encoded = json.dumps(selected, separators=(",", ":"), sort_keys=True).encode()
    return hashlib.sha256(encoded).hexdigest()


def dashboard_pod_state(cluster: Cluster) -> dict[str, Any]:
    items = cluster.json("get", "pods", "-l", "app.kubernetes.io/name=dashboard-api").get(
        "items", []
    )
    items = [item for item in items if not item.get("metadata", {}).get("deletionTimestamp")]
    require(len(items) == 1, "qualification expected one Dashboard Pod")
    pod = items[0]
    status = pod.get("status", {}).get("containerStatuses", [{}])[0]
    return {
        "uid": pod.get("metadata", {}).get("uid"),
        "image": pod.get("spec", {}).get("containers", [{}])[0].get("image"),
        "imageID": status.get("imageID"),
        "restartCount": status.get("restartCount"),
    }


def create_canonical_topic(cluster: Cluster) -> None:
    kafka_cli(
        cluster,
        "kafka-topics.sh",
        "--bootstrap-server",
        "localhost:9093",
        "--command-config",
        "/etc/kafka/secrets/client.properties",
        "--create",
        "--if-not-exists",
        "--topic",
        CANONICAL_TOPIC,
        "--partitions",
        "1",
        "--replication-factor",
        "1",
    )


def canonical_member_count(cluster: Cluster) -> int:
    output = kafka_cli(
        cluster,
        "kafka-consumer-groups.sh",
        "--bootstrap-server",
        "localhost:9093",
        "--command-config",
        "/etc/kafka/secrets/client.properties",
        "--describe",
        "--group",
        CANONICAL_GROUP,
        "--members",
        "--verbose",
    )
    return sum(
        1 for line in output.splitlines() if line.split() and line.split()[0] == CANONICAL_GROUP
    )


def wait_for_canonical_members(cluster: Cluster, expected: int, seconds: int = 90) -> None:
    wait_for(
        f"canonical group member count {expected}",
        lambda: canonical_member_count(cluster) == expected,
        seconds,
    )


def scale_owner(cluster: Cluster, deployment: str, replicas: int) -> None:
    cluster.kubectl("scale", f"deployment/{deployment}", f"--replicas={replicas}")
    if replicas:
        cluster.kubectl(
            "rollout",
            "status",
            f"deployment/{deployment}",
            "--timeout=180s",
            timeout=210,
            activity=f"wait for {deployment} rollout",
        )
        return

    def stopped() -> bool:
        item = cluster.json("get", "deployment", deployment)
        pods = cluster.json(
            "get",
            "pods",
            "-l",
            f"app.kubernetes.io/name={'dashboard-api-legacy' if deployment == JAVA_DEPLOYMENT else 'dashboard-api'}",
        ).get("items", [])
        return item.get("status", {}).get("replicas", 0) == 0 and not pods

    wait_for(f"{deployment} stopped without terminating Pods", stopped, 180)


def java_pod_state(
    cluster: Cluster,
    candidate: JavaCandidateIdentity,
) -> dict[str, Any]:
    items = cluster.json("get", "pods", "-l", "app.kubernetes.io/name=dashboard-api-legacy").get(
        "items", []
    )
    require(len(items) == 1, "qualification expected one Java Dashboard Pod")
    pod = items[0]
    status = pod.get("status", {}).get("containerStatuses", [{}])[0]
    node = str(pod.get("spec", {}).get("nodeName", ""))
    require(bool(node), "Java Dashboard node identity is absent")
    inspected = json.loads(
        command(
            ["docker", "exec", node, "crictl", "inspecti", candidate.image],
            timeout=30,
            activity="inspect loaded Java Dashboard image in Kind containerd",
        )
    )
    content = require_matching_image_content(
        candidate.local_image_id,
        candidate.image,
        inspected,
        str(status.get("imageID", "")),
    )
    return {
        "uid": pod.get("metadata", {}).get("uid"),
        "image": pod.get("spec", {}).get("containers", [{}])[0].get("image"),
        "imageID": status.get("imageID"),
        "restartCount": status.get("restartCount"),
        "localImageID": content.local,
        "containerdImageID": content.containerd,
        "podImageID": content.pod,
    }


def apply_java_owner(cluster: Cluster, candidate: JavaCandidateIdentity) -> None:
    cluster.kubectl(
        "apply",
        "--server-side",
        "--field-manager=dashboard-disposable",
        "-f",
        "-",
        input_text=yaml.safe_dump_all(
            java_resources(cluster.namespace, candidate),
            explicit_start=True,
            sort_keys=False,
        ),
    )
    scale_owner(cluster, JAVA_DEPLOYMENT, 1)
    java_pod_state(cluster, candidate)
    wait_for_canonical_members(cluster, 1)


def apply_go_owner(
    cluster: Cluster,
    candidate: CandidateIdentity,
    runtime_image: str,
    elasticsearch_image: str,
    *,
    fail_readiness: bool = False,
) -> None:
    rendered, items = render(
        cluster,
        runtime_image,
        candidate.revision[:12],
        elasticsearch_image,
        candidate.registry_digest,
        event_mode="canonical",
    )
    if fail_readiness:
        deployment = checks.resource(items, "Deployment", "local-dev-dashboard-api")
        deployment["spec"]["template"]["spec"]["containers"][0]["readinessProbe"]["httpGet"][
            "path"
        ] = "/issue-293-controlled-abort"
        rendered = yaml.safe_dump_all(items, explicit_start=True, sort_keys=False)
    cluster.kubectl(
        "apply",
        "--server-side",
        "--field-manager=dashboard-disposable",
        "-f",
        "-",
        input_text=rendered,
    )
    if fail_readiness:
        wait_for_canonical_members(cluster, 1)
        deployment = cluster.json("get", "deployment", "local-dev-dashboard-api")
        require(
            deployment.get("status", {}).get("availableReplicas", 0) == 0,
            "controlled-abort successor unexpectedly became ready",
        )
        return
    cluster.kubectl(
        "rollout",
        "status",
        "deployment/local-dev-dashboard-api",
        "--timeout=180s",
        timeout=210,
        activity="wait for canonical Go Dashboard rollout",
    )
    wait_for("canonical Go Dashboard endpoint ready", lambda: endpoint_count(cluster) == 1)
    wait_for_canonical_members(cluster, 1)
    verify_runtime_identity(
        cluster,
        candidate.version,
        runtime_image,
        candidate.local_image_id,
        candidate.registry_digest,
    )


def run_bounded_smoke(
    cluster: Cluster,
    qualification: dict[str, Any],
) -> dict[str, Any]:
    measurement = qualification["measurement"]
    started = time.monotonic()
    pod_before = dashboard_pod_state(cluster)
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        client = retirement_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        events = run_qualification_events(
            cluster,
            client,
            qualification["events"][:3],
            measurement["eventVisibilitySeconds"],
        )
    verify_isolated_kafka_names(cluster)
    queries = run_query_workload(
        cluster,
        qualification["queries"],
        measurement["requestsPerQuery"],
        measurement["requestDeadlineSeconds"],
    )
    offsets = wait_for_committed_offsets(cluster, {0: 3})
    elapsed = time.monotonic() - started
    pod_after = dashboard_pod_state(cluster)
    require(elapsed <= measurement["maximumSeconds"], "qualification smoke exceeded five minutes")
    require(pod_after == pod_before, "Dashboard restarted during qualification smoke")
    return {
        "elapsedSeconds": round(elapsed, 3),
        "events": events,
        "queries": queries,
        "offsets": [offset._asdict() for offset in offsets],
        "pod": pod_after,
    }


def run_stop_restore(
    cluster: Cluster,
    qualification: dict[str, Any],
    candidate: CandidateIdentity,
    image: str,
) -> dict[str, Any]:
    configuration = deployment_configuration_sha256(cluster)
    before_offsets = wait_for_committed_offsets(
        cluster,
        {0: 3},
        group=CANONICAL_GROUP,
        topic=CANONICAL_TOPIC,
    )
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        client = retirement_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        before = capture_state(client, before_offsets, CANONICAL_INDEX)
    cluster.kubectl("scale", "deployment/local-dev-dashboard-api", "--replicas=0")
    wait_for("Dashboard stopped for retained-state check", lambda: endpoint_count(cluster) == 0)
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        client = retirement_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        stopped = capture_state(
            client,
            consumer_group_offsets(cluster, CANONICAL_GROUP, CANONICAL_TOPIC),
            CANONICAL_INDEX,
        )
    require_same_state("stopped Dashboard", before, stopped)
    cluster.kubectl("scale", "deployment/local-dev-dashboard-api", "--replicas=1")
    cluster.kubectl(
        "rollout",
        "status",
        "deployment/local-dev-dashboard-api",
        "--timeout=180s",
        timeout=210,
        activity="restore qualified Dashboard candidate",
    )
    wait_for("restored Dashboard endpoint ready", lambda: endpoint_count(cluster) == 1)
    require(
        deployment_configuration_sha256(cluster) == configuration,
        "restored Dashboard configuration differs",
    )
    verify_runtime_identity(
        cluster,
        candidate.version,
        image,
        candidate.local_image_id,
        candidate.registry_digest,
    )
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        client = retirement_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        restored = capture_state(
            client,
            consumer_group_offsets(cluster, CANONICAL_GROUP, CANONICAL_TOPIC),
            CANONICAL_INDEX,
        )
        require_same_state("restored Dashboard", before, restored)
        event_result = run_qualification_events(
            cluster,
            client,
            [qualification["events"][3]],
            qualification["measurement"]["eventVisibilitySeconds"],
            topic=CANONICAL_TOPIC,
            index=CANONICAL_INDEX,
        )[0]
        expected_offsets = {offset.partition: offset.committed + 1 for offset in before.offsets}
        after_offsets = wait_for_committed_offsets(
            cluster,
            expected_offsets,
            group=CANONICAL_GROUP,
            topic=CANONICAL_TOPIC,
        )
        after = capture_state(client, after_offsets, CANONICAL_INDEX)
    require(after.mapping == before.mapping, "post-restore mapping changed")
    require(
        after.documents
        == before.documents
        + [
            {
                "id": qualification["events"][3]["expectedDocument"]["number"],
                "source": qualification["events"][3]["expectedDocument"],
            }
        ],
        "post-restore document advancement differs",
    )
    for old, new in zip(before.offsets, after.offsets, strict=True):
        require(
            new.partition == old.partition
            and new.committed == old.committed + 1
            and new.log_end == old.log_end + 1,
            "post-restore offset advancement differs",
        )
    run_query_workload(
        cluster,
        [qualification["postRestoreQuery"]],
        1,
        qualification["measurement"]["requestDeadlineSeconds"],
    )
    return {
        "configurationSha256": configuration,
        "before": safe_state_record(before),
        "stopped": safe_state_record(stopped),
        "restored": safe_state_record(restored),
        "after": safe_state_record(after),
        "postRestoreEvent": event_result,
        "pod": dashboard_pod_state(cluster),
    }


def run_go_only_smoke(
    cluster: Cluster,
    qualification: dict[str, Any],
    candidate: CandidateIdentity,
    image: str,
) -> dict[str, Any]:
    visibility_seconds = qualification["measurement"]["eventVisibilitySeconds"]
    started = time.monotonic()
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        client = retirement_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        registration_events = run_qualification_events(
            cluster,
            client,
            [qualification["events"][0], qualification["events"][2]],
            visibility_seconds,
            topic=CANONICAL_TOPIC,
            index=CANONICAL_INDEX,
        )
        replacement = run_qualification_events(
            cluster,
            client,
            [qualification["events"][1]],
            visibility_seconds,
            topic=CANONICAL_TOPIC,
            index=CANONICAL_INDEX,
        )[0]
        offsets = wait_for_committed_offsets(
            cluster,
            {0: 3},
            group=CANONICAL_GROUP,
            topic=CANONICAL_TOPIC,
        )
        before_rerun = capture_state(client, offsets, CANONICAL_INDEX)
    require(
        before_rerun.documents
        == [
            {
                "id": qualification["events"][1]["expectedDocument"]["number"],
                "source": qualification["events"][1]["expectedDocument"],
            },
            {
                "id": qualification["events"][2]["expectedDocument"]["number"],
                "source": qualification["events"][2]["expectedDocument"],
            },
        ],
        "same-ID registration replacement retained stale or duplicate state",
    )
    queries = run_query_workload(
        cluster,
        canonical_query_oracles(qualification),
        1,
        qualification["measurement"]["requestDeadlineSeconds"],
    )
    setup_elasticsearch(cluster, "-rerun")
    create_canonical_topic(cluster)
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        client = retirement_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        after_rerun = capture_state(
            client,
            consumer_group_offsets(cluster, CANONICAL_GROUP, CANONICAL_TOPIC),
            CANONICAL_INDEX,
        )
    require_same_state("ordinary setup rerun", before_rerun, after_rerun)
    restart = run_stop_restore(cluster, qualification, candidate, image)
    elapsed = time.monotonic() - started
    require(
        elapsed <= qualification["measurement"]["maximumSeconds"],
        "Go-only deployment smoke exceeded five minutes",
    )
    return {
        "elapsedSeconds": round(elapsed, 3),
        "registrations": registration_events,
        "replacement": replacement,
        "queries": queries,
        "setupRerun": safe_state_record(after_rerun),
        "restart": restart,
    }


def run_java_go_java_rehearsal(
    cluster: Cluster,
    qualification: dict[str, Any],
    candidate: CandidateIdentity,
    runtime_image: str,
    java_candidate: JavaCandidateIdentity,
    elasticsearch_image: str,
) -> dict[str, Any]:
    """Exercise exclusive canonical ownership, controlled abort and Java return."""

    visibility_seconds = qualification["measurement"]["eventVisibilitySeconds"]
    transition_record: list[dict[str, Any]] = []
    create_canonical_topic(cluster)
    scale_owner(cluster, "local-dev-dashboard-api", 0)
    wait_for_canonical_members(cluster, 0)
    apply_java_owner(cluster, java_candidate)
    java_initial = java_pod_state(cluster, java_candidate)
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        client = retirement_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        baseline_events = run_qualification_events(
            cluster,
            client,
            [qualification["events"][0], qualification["events"][2]],
            visibility_seconds,
            topic=CANONICAL_TOPIC,
            index=CANONICAL_INDEX,
            java_null_compatibility=True,
        )
        baseline_offsets = wait_for_committed_offsets(
            cluster,
            {0: 2},
            group=CANONICAL_GROUP,
            topic=CANONICAL_TOPIC,
        )
        baseline = capture_state(client, baseline_offsets, CANONICAL_INDEX)
    require(
        [item["id"] for item in baseline.documents] == ["SYN-292-001", "SYN-292-002"],
        "canonical baseline does not contain exactly two synthetic documents",
    )
    require(
        all(offset.committed > 0 for offset in baseline.offsets),
        "canonical baseline commits are not nonzero",
    )
    transition_record.append({"phase": "java-baseline", "members": 1, "goPods": 0})

    abort_started = time.monotonic()
    scale_owner(cluster, JAVA_DEPLOYMENT, 0)
    wait_for_canonical_members(cluster, 0)
    apply_go_owner(
        cluster,
        candidate,
        runtime_image,
        elasticsearch_image,
        fail_readiness=True,
    )
    transition_record.append({"phase": "failed-go-not-ready", "members": 1, "javaPods": 0})
    scale_owner(cluster, "local-dev-dashboard-api", 0)
    wait_for_canonical_members(cluster, 0)
    apply_java_owner(cluster, java_candidate)
    abort_seconds = time.monotonic() - abort_started
    require(abort_seconds <= 600, "controlled abort and Java return exceeded ten minutes")
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        client = retirement_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        abort_return = capture_state(
            client,
            consumer_group_offsets(cluster, CANONICAL_GROUP, CANONICAL_TOPIC),
            CANONICAL_INDEX,
        )
    require_same_state("controlled-abort Java return", baseline, abort_return)
    transition_record.append({"phase": "abort-java-return", "members": 1, "goPods": 0})

    forward_started = time.monotonic()
    scale_owner(cluster, JAVA_DEPLOYMENT, 0)
    wait_for_canonical_members(cluster, 0)
    apply_go_owner(cluster, candidate, runtime_image, elasticsearch_image)
    forward_seconds = time.monotonic() - forward_started
    require(forward_seconds <= 180, "canonical Go activation exceeded three minutes")
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        client = retirement_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        replacement_result = run_qualification_events(
            cluster,
            client,
            [qualification["events"][1]],
            visibility_seconds,
            topic=CANONICAL_TOPIC,
            index=CANONICAL_INDEX,
        )[0]
        expected_offsets = {offset.partition: offset.committed + 1 for offset in baseline.offsets}
        go_offsets = wait_for_committed_offsets(
            cluster,
            expected_offsets,
            group=CANONICAL_GROUP,
            topic=CANONICAL_TOPIC,
        )
        go_state = capture_state(client, go_offsets, CANONICAL_INDEX)
    require(go_state.mapping == baseline.mapping, "canonical Go replacement changed the mapping")
    require(
        go_state.documents
        == [
            {
                "id": qualification["events"][1]["expectedDocument"]["number"],
                "source": qualification["events"][1]["expectedDocument"],
            },
            {
                "id": qualification["events"][2]["expectedDocument"]["number"],
                "source": {
                    name: value
                    for name, value in qualification["events"][2]["expectedDocument"].items()
                    if name != "agentLogin"
                },
            },
        ],
        "canonical same-ID replacement did not retain exactly two documents",
    )
    run_query_workload(
        cluster,
        canonical_query_oracles(qualification),
        qualification["measurement"]["requestsPerQuery"],
        qualification["measurement"]["requestDeadlineSeconds"],
    )
    transition_record.append({"phase": "go-owned", "members": 1, "javaPods": 0})

    return_started = time.monotonic()
    scale_owner(cluster, "local-dev-dashboard-api", 0)
    wait_for_canonical_members(cluster, 0)
    apply_java_owner(cluster, java_candidate)
    return_seconds = time.monotonic() - return_started
    require(return_seconds <= 600, "successful Java return exceeded ten minutes")
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        client = retirement_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        post_return_event = run_qualification_events(
            cluster,
            client,
            [qualification["events"][3]],
            visibility_seconds,
            topic=CANONICAL_TOPIC,
            index=CANONICAL_INDEX,
            java_null_compatibility=True,
        )[0]
        after_offsets = wait_for_committed_offsets(
            cluster,
            {offset.partition: offset.committed + 1 for offset in go_state.offsets},
            group=CANONICAL_GROUP,
            topic=CANONICAL_TOPIC,
        )
        after = capture_state(client, after_offsets, CANONICAL_INDEX)
    require(after.mapping == baseline.mapping, "post-return Java event changed the mapping")
    require(
        after.documents
        == go_state.documents
        + [
            {
                "id": qualification["events"][3]["expectedDocument"]["number"],
                "source": {
                    name: value
                    for name, value in qualification["events"][3]["expectedDocument"].items()
                    if name != "agentLogin"
                },
            }
        ],
        "post-return Java document advancement differs",
    )
    run_query_workload(
        cluster,
        [qualification["postRestoreQuery"]],
        1,
        qualification["measurement"]["requestDeadlineSeconds"],
        JAVA_SERVICE,
    )
    java_return = java_pod_state(cluster, java_candidate)
    transition_record.append({"phase": "java-returned", "members": 1, "goPods": 0})

    scale_owner(cluster, JAVA_DEPLOYMENT, 0)
    wait_for_canonical_members(cluster, 0)
    isolated_render, _items = render(
        cluster,
        runtime_image,
        candidate.revision[:12],
        elasticsearch_image,
        candidate.registry_digest,
        event_mode="isolated",
    )
    cluster.kubectl(
        "apply",
        "--server-side",
        "--field-manager=dashboard-disposable",
        "-f",
        "-",
        input_text=isolated_render,
    )
    cluster.kubectl(
        "rollout",
        "status",
        "deployment/local-dev-dashboard-api",
        "--timeout=180s",
        timeout=210,
        activity="restore isolated Go Dashboard after canonical rehearsal",
    )
    wait_for("restored isolated Dashboard endpoint ready", lambda: endpoint_count(cluster) == 1)

    return {
        "baselineEvents": baseline_events,
        "baseline": safe_state_record(baseline),
        "controlledAbort": {
            "returnSeconds": round(abort_seconds, 3),
            "returned": safe_state_record(abort_return),
        },
        "forwardSeconds": round(forward_seconds, 3),
        "replacementEvent": replacement_result,
        "goState": safe_state_record(go_state),
        "returnSeconds": round(return_seconds, 3),
        "postReturnEvent": post_return_event,
        "after": safe_state_record(after),
        "javaInitial": java_initial,
        "javaReturn": java_return,
        "transitions": transition_record,
    }


def write_qualification_report(report: dict[str, Any]) -> None:
    raw_path = os.environ.get("QUALIFICATION_REPORT_FILE", "")
    if not raw_path:
        return
    path = Path(raw_path)
    require(path.is_absolute() and path.suffix == ".json", "qualification report path differs")
    require(path.parent.is_dir(), "qualification report directory is absent")
    path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def verify_isolated_kafka_names(cluster: Cluster) -> None:
    topics = set(
        kafka_cli(
            cluster,
            "kafka-topics.sh",
            "--bootstrap-server",
            "localhost:9093",
            "--command-config",
            "/etc/kafka/secrets/client.properties",
            "--list",
        ).splitlines()
    )
    groups = set(
        kafka_cli(
            cluster,
            "kafka-consumer-groups.sh",
            "--bootstrap-server",
            "localhost:9093",
            "--command-config",
            "/etc/kafka/secrets/client.properties",
            "--list",
        ).splitlines()
    )
    require(retirement_admin.TOPIC in topics, "isolated registration topic is absent")
    require("policy-registered" not in topics, "canonical registration topic was created")
    require(retirement_admin.GROUP in groups, "isolated Dashboard group is absent")
    require("dashboard-service" not in groups, "canonical Dashboard group was joined")


def run() -> None:
    image = os.environ.get("IMAGE_REF", "")
    expected_revision = os.environ.get("QUALIFICATION_SOURCE_REVISION", "")
    elasticsearch_image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    kafka_image = os.environ.get("KAFKA_IMAGE", "")
    require(
        bool(re.fullmatch(r".+@sha256:[0-9a-f]{64}", elasticsearch_image)),
        "ELASTICSEARCH_IMAGE must be digest-qualified",
    )
    require(
        bool(re.fullmatch(r".+@sha256:[0-9a-f]{64}", kafka_image)),
        "KAFKA_IMAGE must be digest-qualified",
    )
    require(command(["kubectl", "config", "current-context"]).strip() == CONTEXT, "wrong context")
    qualification = load_qualification()
    candidate = prepare_candidate_image(image, expected_revision)
    runtime_image = prepare_kind_candidate(image, candidate)
    source_short = candidate.revision[:12]
    namespace = f"dashboard-disposable-{os.getpid()}-{secrets.token_hex(3)}"
    cluster = Cluster(namespace)
    created = False
    report: dict[str, Any] | None = None
    try:
        command(
            ["docker", "pull", elasticsearch_image],
            timeout=300,
            activity="pull pinned disposable Elasticsearch image",
        )
        command(
            ["kind", "load", "docker-image", elasticsearch_image, "--name", KIND_CLUSTER],
            timeout=300,
            activity="load pinned Elasticsearch image into existing Kind cluster",
        )
        command(
            ["docker", "pull", kafka_image],
            timeout=300,
            activity="pull pinned disposable Kafka image",
        )
        command(
            ["kind", "load", "docker-image", kafka_image, "--name", KIND_CLUSTER],
            timeout=300,
            activity="load pinned Kafka image into existing Kind cluster",
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
        rendered, _items = render(
            cluster,
            runtime_image,
            source_short,
            elasticsearch_image,
            candidate.registry_digest,
        )
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
            f"deployment/{ELASTICSEARCH_NAME}",
            "--timeout=180s",
            timeout=210,
            activity="wait for disposable Elasticsearch rollout",
        )
        setup_elasticsearch(cluster)
        cluster.kubectl(
            "rollout",
            "status",
            "deployment/local-dev-dashboard-api",
            "--timeout=180s",
            timeout=210,
            activity="wait for disposable Dashboard rollout",
        )
        wait_for("Dashboard endpoint ready", lambda: endpoint_count(cluster) == 1)
        verify_reader_permissions(cluster)
        verify_runtime_identity(
            cluster,
            candidate.version,
            runtime_image,
            candidate.local_image_id,
            candidate.registry_digest,
        )
        response = http_probe(
            cluster,
            "management-health",
            ["http://local-dev-dashboard-api-management:8081/readyz"],
            labels={checks.MANAGEMENT_CLIENT_LABEL: "true"},
        )
        require_http_response("management-health", response, 200, "ready\n")
        denial_failures: list[str] = []
        for name, path in (
            ("unlabeled-denied", "/dashboard/totalsales"),
            ("unlabeled-agent-denied", "/dashboard/agentssales"),
            ("unlabeled-trends-denied", "/dashboard/trends"),
        ):
            try:
                probe(
                    cluster,
                    name,
                    ["--max-time", "3", "--silent", f"http://local-dev-dashboard-api{path}"],
                    expect_success=False,
                )
            except (CheckFailed, checks.DeploymentError, OSError, ValueError) as error:
                denial_failures.append(f"{name}: {error}")
        require(not denial_failures, "CNI denial checks failed: " + " | ".join(denial_failures))
        kafka_ca = setup_kafka(cluster, kafka_image)
        verify_kafka_tls(cluster, kafka_ca)
        create_canonical_topic(cluster)
        enabled_render, _enabled_items = render(
            cluster,
            runtime_image,
            source_short,
            elasticsearch_image,
            candidate.registry_digest,
            event_mode="canonical",
        )
        cluster.kubectl(
            "apply",
            "--server-side",
            "--field-manager=dashboard-disposable",
            "-f",
            "-",
            input_text=enabled_render,
        )
        cluster.kubectl(
            "rollout",
            "status",
            "deployment/local-dev-dashboard-api",
            "--timeout=180s",
            timeout=210,
            activity="wait for registration-enabled Dashboard rollout",
        )
        wait_for(
            "registration-enabled Dashboard endpoint ready",
            lambda: endpoint_count(cluster) == 1,
        )
        verify_registration_writer_permissions(cluster)
        smoke = run_go_only_smoke(cluster, qualification, candidate, runtime_image)
        report = {
            "schemaVersion": 3,
            "candidate": {
                "sourceRevision": candidate.revision,
                "version": candidate.version,
                "image": image,
                "runtimeImage": runtime_image,
                "registryDigest": candidate.registry_digest,
            },
            "target": {"context": CONTEXT, "namespace": namespace},
            "smoke": smoke,
        }
        print(
            "dashboard-disposable: ordinary setup, three queries, replacement, setup rerun, "
            "permissions, health, CNI denial and restart passed"
        )
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
    require(report is not None, "qualification report was not produced")
    write_qualification_report(report)


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
