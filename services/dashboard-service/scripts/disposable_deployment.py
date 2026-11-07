#!/usr/bin/env python3
"""Exercise Dashboard in a uniquely owned namespace on the configured Kind cluster."""

from __future__ import annotations

import base64
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
import event_administration as event_admin
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
EVENT_READER_USERNAME = event_admin.READER
EVENT_READER_PASSWORD = "issue-290-disposable-reader-password"
EVENT_WRITER_USERNAME = event_admin.WRITER
EVENT_WRITER_PASSWORD = "issue-290-disposable-writer-password"
ELASTICSEARCH_NAME = "dashboard-disposable-elasticsearch"
KAFKA_NAME = "local-dev-kafka"
KAFKA_BOOTSTRAP = "local-dev-kafka-kafka-bootstrap"
HTTP_STATUS_MARKER = "__INSURANCE_HUB_HTTP_STATUS__="
MAX_HTTP_BODY_BYTES = 4096


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
                "ports": [{"name": "tls", "port": 9093, "targetPort": 9093}],
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
                                            "SSL://0.0.0.0:9093,CONTROLLER://0.0.0.0:9094,"
                                            "INTERNAL://0.0.0.0:9095"
                                        ),
                                    },
                                    {
                                        "name": "KAFKA_ADVERTISED_LISTENERS",
                                        "value": (
                                            f"SSL://{KAFKA_BOOTSTRAP}:9093,"
                                            "INTERNAL://localhost:9095"
                                        ),
                                    },
                                    {
                                        "name": "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP",
                                        "value": "SSL:SSL,CONTROLLER:PLAINTEXT,INTERNAL:PLAINTEXT",
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


def render(
    cluster: Cluster,
    image: str,
    source_short: str,
    elasticsearch_image: str,
    *,
    event_enabled: bool = False,
) -> tuple[str, list[dict[str, Any]]]:
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
    config = checks.resource(items, "ConfigMap", "local-dev-dashboard-config")["data"]
    config.update(
        {
            "ELASTICSEARCH_URL": f"http://{ELASTICSEARCH_NAME}:9200",
            "ELASTICSEARCH_INDEX": "policy_stats",
            "ELASTICSEARCH_USERNAME": READER_USERNAME,
            "ELASTICSEARCH_TIMEOUT": "5s",
        }
    )
    environment = deployment["spec"]["template"]["spec"]["containers"][0]["env"]
    environment[:] = [
        item
        for item in environment
        if item.get("name") not in {"ELASTICSEARCH_USERNAME", "ELASTICSEARCH_PASSWORD"}
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
    if event_enabled:
        config.update(
            {
                "EVENT_WORKERS_ENABLED": "true",
                "ELASTICSEARCH_INDEX": event_admin.INDEX,
                "KAFKA_BROKERS": f"{KAFKA_BOOTSTRAP}:9093",
                "KAFKA_GROUP": event_admin.GROUP,
                "KAFKA_REGISTERED_TOPIC": event_admin.TOPIC,
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
            ("ELASTICSEARCH_USERNAME", "local-dev-dashboard-elasticsearch-issue-290", "username"),
            ("ELASTICSEARCH_PASSWORD", "local-dev-dashboard-elasticsearch-issue-290", "password"),
            (
                "ELASTICSEARCH_WRITER_USERNAME",
                "local-dev-dashboard-elasticsearch-writer-issue-290",
                "username",
            ),
            (
                "ELASTICSEARCH_WRITER_PASSWORD",
                "local-dev-dashboard-elasticsearch-writer-issue-290",
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
                "local-dev-dashboard-elasticsearch-issue-290",
                EVENT_READER_USERNAME,
                EVENT_READER_PASSWORD,
                "elasticsearch-reader",
            ),
            (
                "local-dev-dashboard-elasticsearch-writer-issue-290",
                EVENT_WRITER_USERNAME,
                EVENT_WRITER_PASSWORD,
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
        event_admin.TOPIC,
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
) -> ImageContentIdentity:
    require(isinstance(containerd_image, dict), "containerd image inspection is invalid")
    containerd_status = containerd_image.get("status", {})
    require(isinstance(containerd_status, dict), "containerd image status is absent")
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
    definition = event_admin.load_definition()
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
            "fixture-index",
            "PUT",
            "/policy_stats",
            '{"settings":{"number_of_shards":1,"number_of_replicas":0},"mappings":{"properties":{"agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword"}}},"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},"from":{"type":"date"},"totalPremium":{"type":"double"}}}}',
        ),
        (
            "fixture-unrelated",
            "PUT",
            "/unrelated_stats",
            '{"settings":{"number_of_shards":1,"number_of_replicas":0}}',
        ),
        (
            "fixture-document",
            "POST",
            "/policy_stats/_doc/disposable?refresh=true",
            '{"agentLogin":"safe-agent","productCode":"SAFE","from":"2019-06-01","totalPremium":1000.0}',
        ),
        (
            "event-index",
            "PUT",
            f"/{event_admin.INDEX}",
            json.dumps(
                {
                    "settings": {"number_of_shards": 1, "number_of_replicas": 0},
                    "mappings": definition.mapping,
                },
                separators=(",", ":"),
            ),
        ),
        (
            "event-reader-role",
            "PUT",
            f"/_security/role/{EVENT_READER_USERNAME}",
            json.dumps(
                event_admin.role(EVENT_READER_USERNAME, ["read", "view_index_metadata"]),
                separators=(",", ":"),
            ),
        ),
        (
            "event-reader-user",
            "PUT",
            f"/_security/user/{EVENT_READER_USERNAME}",
            json.dumps(
                event_admin.user(
                    EVENT_READER_USERNAME,
                    EVENT_READER_PASSWORD,
                    "disposable reader",
                ),
                separators=(",", ":"),
            ),
        ),
        (
            "event-writer-role",
            "PUT",
            f"/_security/role/{EVENT_WRITER_USERNAME}",
            json.dumps(
                event_admin.role(EVENT_WRITER_USERNAME, ["index"]),
                separators=(",", ":"),
            ),
        ),
        (
            "event-writer-user",
            "PUT",
            f"/_security/user/{EVENT_WRITER_USERNAME}",
            json.dumps(
                event_admin.user(
                    EVENT_WRITER_USERNAME,
                    EVENT_WRITER_PASSWORD,
                    "disposable writer",
                ),
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
        [*reader_base, f"http://{ELASTICSEARCH_NAME}:9200/{event_admin.INDEX}/_count"],
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
            f"http://{ELASTICSEARCH_NAME}:9200/{event_admin.INDEX}/_doc/denied",
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
            f"http://{ELASTICSEARCH_NAME}:9200/{event_admin.INDEX}/_mapping",
        ],
    )
    require(response.status == 200, "accepted writer mapping authority was not observed")
    denied = (
        ("event-writer-read-denied", [f"/{event_admin.INDEX}/_count"]),
        (
            "event-writer-delete-denied",
            ["--request", "DELETE", f"/{event_admin.INDEX}/_doc/denied"],
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
    cluster: Cluster, version: str, image: str, local_image_id: str
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
                "_deploy-test-rpc",
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


def exercise_registration_projection(cluster: Cluster) -> None:
    oracle = json.loads(
        (ROOT / "services/dashboard-service/testdata/events/oracle.json").read_text(
            encoding="utf-8"
        )
    )
    registration = oracle.get("events", {}).get("registration", {})
    event = registration.get("value")
    expected = registration.get("expectedDocument")
    document_id = expected.get("number") if isinstance(expected, dict) else None
    require(
        registration.get("key") == "SYN-290-KEY-IGNORED"
        and isinstance(event, dict)
        and isinstance(expected, dict)
        and document_id == "SYN-290-001",
        "Dashboard event oracle differs",
    )
    kafka_cli(
        cluster,
        "kafka-console-producer.sh",
        "--bootstrap-server",
        "localhost:9093",
        "--producer.config",
        "/etc/kafka/secrets/client.properties",
        "--topic",
        event_admin.TOPIC,
        input_text=json.dumps(event, separators=(",", ":")) + "\n",
    )
    with forwarded_service(cluster, ELASTICSEARCH_NAME, 9200) as port:
        admin = event_admin.ElasticsearchClient(port, ADMIN_USERNAME, ADMIN_PASSWORD)
        projected: dict[str, Any] | None = None

        def stored() -> bool:
            nonlocal projected
            try:
                status, result = admin.request("GET", f"/{event_admin.INDEX}/_doc/{document_id}")
            except event_admin.AdministrationError:
                return False
            if status == 200 and isinstance(result, dict):
                projected = result.get("_source")
            return projected == expected

        wait_for("registration projected through deployed worker", stored, 90)
    response = http_probe(
        cluster,
        "event-total-sales",
        [
            "--header",
            "Content-Type: application/json",
            "--data",
            '{"saleDateFrom":"2026-01-01","saleDateTo":"2026-02-01"}',
            "http://local-dev-dashboard-api/dashboard/totalsales",
        ],
        labels={checks.BUSINESS_CLIENT_LABEL: "true"},
    )
    require_http_response(
        "event-total-sales",
        response,
        200,
        '{"total":{"policiesCount":1,"premiumAmount":123.45},'
        '"perProductTotal":{"HSI":{"policiesCount":1,"premiumAmount":123.45}}}',
    )
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
    require(event_admin.TOPIC in topics, "isolated registration topic is absent")
    require("policy-registered" not in topics, "canonical registration topic was created")
    require(event_admin.GROUP in groups, "isolated Dashboard group is absent")
    require("dashboard-service" not in groups, "canonical Dashboard group was joined")


def run() -> None:
    image = os.environ.get("IMAGE_REF", "")
    elasticsearch_image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    kafka_image = os.environ.get("KAFKA_IMAGE", "")
    require(
        bool(
            re.fullmatch(
                r"insurance-hub-dashboard-api:1\.[0-9]+\.[0-9]+-local\.(?!0{12})[0-9a-f]{12}",
                image,
            )
        ),
        "IMAGE_REF differs",
    )
    require(
        bool(re.fullmatch(r".+@sha256:[0-9a-f]{64}", elasticsearch_image)),
        "ELASTICSEARCH_IMAGE must be digest-qualified",
    )
    require(
        bool(re.fullmatch(r".+@sha256:[0-9a-f]{64}", kafka_image)),
        "KAFKA_IMAGE must be digest-qualified",
    )
    require(command(["kubectl", "config", "current-context"]).strip() == CONTEXT, "wrong context")
    inspected = json.loads(command(["docker", "image", "inspect", image]))
    require(len(inspected) == 1, "Dashboard image is absent")
    local_image_id = str(inspected[0].get("Id", ""))
    image_content_digest(local_image_id, "local Docker")
    revision = (
        inspected[0]
        .get("Config", {})
        .get("Labels", {})
        .get("org.opencontainers.image.revision", "")
    )
    source_short = image.rsplit(".", 1)[1]
    require(
        bool(re.fullmatch(r"(?!0{40})[0-9a-f]{40}", revision)),
        "Dashboard image source revision is invalid",
    )
    labels = inspected[0].get("Config", {}).get("Labels", {})
    require(revision[:12] == source_short, "Dashboard image source differs")
    require(
        labels.get("org.opencontainers.image.version") == f"git-{source_short}",
        "Dashboard image version differs",
    )
    namespace = f"dashboard-disposable-{os.getpid()}-{secrets.token_hex(3)}"
    cluster = Cluster(namespace)
    created = False
    try:
        command(
            ["kind", "load", "docker-image", image, "--name", KIND_CLUSTER],
            timeout=120,
            activity="load Dashboard image into existing Kind cluster",
        )
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
        rendered, _items = render(cluster, image, source_short, elasticsearch_image)
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
        sales_cases = (
            (
                "business-total-sales",
                '{"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}',
                "http://local-dev-dashboard-api/dashboard/totalsales",
                '{"total":{"policiesCount":1,"premiumAmount":1000.00},'
                '"perProductTotal":{"SAFE":{"policiesCount":1,"premiumAmount":1000.00}}}',
            ),
            (
                "business-agent-sales",
                '{"agentLogin":"safe-agent","productCode":"SAFE",'
                '"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}',
                "http://local-dev-dashboard-api/dashboard/agentssales",
                '{"perAgentTotal":{"safe-agent":{"policiesCount":1,"premiumAmount":1000.0}}}',
            ),
            (
                "business-sales-trends",
                '{"productCode":"SAFE","saleDateFrom":"2019-01-01",'
                '"saleDateTo":"2020-01-01","aggregationUnitCode":"MONTH"}',
                "http://local-dev-dashboard-api/dashboard/trends",
                '{"periodSales":[{"periodDate":[2019,6,1],'
                '"period":"2019-06-01T00:00:00.000Z","policiesCount":1,'
                '"premiumAmount":1000.00}]}',
            ),
        )
        sales_failures: list[str] = []
        for name, payload, url, expected in sales_cases:
            try:
                response = http_probe(
                    cluster,
                    name,
                    ["--header", "Content-Type: application/json", "--data", payload, url],
                    labels={checks.BUSINESS_CLIENT_LABEL: "true"},
                )
                require_http_response(name, response, 200, expected)
            except (CheckFailed, checks.DeploymentError, OSError, ValueError) as error:
                sales_failures.append(f"{name}: {error}")
        try:
            direct_rpc(cluster)
        except (CheckFailed, checks.DeploymentError, OSError, ValueError) as error:
            sales_failures.append(f"direct RPC: {error}")
        require(not sales_failures, "sales verification failed: " + " | ".join(sales_failures))
        verify_runtime_identity(cluster, f"git-{source_short}", image, local_image_id)
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
        verify_event_permissions(cluster)
        enabled_render, _enabled_items = render(
            cluster,
            image,
            source_short,
            elasticsearch_image,
            event_enabled=True,
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
            activity="wait for event-enabled Dashboard rollout",
        )
        wait_for("event-enabled Dashboard endpoint ready", lambda: endpoint_count(cluster) == 1)
        exercise_registration_projection(cluster)
        dashboard_pod = cluster.json("get", "pods", "-l", "app.kubernetes.io/name=dashboard-api")[
            "items"
        ][0]
        dashboard_ip = dashboard_pod["status"]["podIP"]
        cluster.kubectl("scale", f"deployment/{ELASTICSEARCH_NAME}", "--replicas=0")
        wait_for("Dashboard endpoint withdrawn", lambda: endpoint_count(cluster) == 0)
        response = http_probe(
            cluster,
            "dependency-ready-denied",
            [f"http://{dashboard_ip}:8081/readyz"],
            labels={checks.MANAGEMENT_CLIENT_LABEL: "true"},
        )
        require_http_response("dependency-ready-denied", response, 503, "not ready\n")
        response = http_probe(
            cluster,
            "dependency-live",
            [f"http://{dashboard_ip}:8081/livez"],
            labels={checks.MANAGEMENT_CLIENT_LABEL: "true"},
        )
        require_http_response("dependency-live", response, 200, "live\n")
        dependency_failures: list[str] = []
        for name, payload, url, _expected in sales_cases:
            dependency_name = name.replace("business-", "dependency-") + "-safe"
            try:
                response = http_probe(
                    cluster,
                    dependency_name,
                    [
                        "--header",
                        "Content-Type: application/json",
                        "--data",
                        payload,
                        url.replace(
                            "http://local-dev-dashboard-api", f"http://{dashboard_ip}:8080"
                        ),
                    ],
                    labels={checks.BUSINESS_CLIENT_LABEL: "true"},
                )
                require_http_response(
                    dependency_name,
                    response,
                    503,
                    '{"code":14,"message":"service unavailable","details":[]}',
                )
            except (CheckFailed, checks.DeploymentError, OSError, ValueError) as error:
                dependency_failures.append(f"{dependency_name}: {error}")
        require(
            not dependency_failures,
            "dependency sales checks failed: " + " | ".join(dependency_failures),
        )
        cluster.kubectl("scale", f"deployment/{ELASTICSEARCH_NAME}", "--replicas=1")
        cluster.kubectl(
            "rollout",
            "status",
            f"deployment/{ELASTICSEARCH_NAME}",
            "--timeout=180s",
            timeout=210,
            activity="wait for disposable Elasticsearch recovery",
        )
        setup_elasticsearch(cluster, "-recovery")
        wait_for(
            "Dashboard endpoint recovered after dependency", lambda: endpoint_count(cluster) == 1
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
        print(
            "dashboard-disposable: disabled queries, isolated TLS registration, reader/writer "
            "permissions, identity, dependency loss, health, CNI denial and restart passed"
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
