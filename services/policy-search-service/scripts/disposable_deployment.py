#!/usr/bin/env python3
"""Exercise Policy Search in one uniquely owned namespace on the configured Kind cluster."""

from __future__ import annotations

import base64
import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from contextlib import contextmanager
from pathlib import Path
from typing import Any

import policy_search_deployment_checks as deployment_checks
import resource_administration as service_admin
import yaml
from administration import AdministrationError, ElasticsearchClient, require_status

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
MODULE = ROOT / "services/policy-search-service"
CONTEXT = "kind-local-dev-insurance-hub"
KIND_CLUSTER = "local-dev-insurance-hub"
OWNER_LABEL = "insurance-hub.io/policy-search-disposable"
REGISTERED_TOPIC = "policy-registered"
TERMINATED_TOPIC = "policy-terminated"


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


def render(
    cluster: Cluster,
    image: str,
    source_short: str,
) -> tuple[str, list[dict[str, Any]]]:
    text = deployment_checks.render("local-dev")
    deployment_checks.validate_render("local-dev", text)
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
    pod = deployment["spec"]["template"]["spec"]
    container = pod["containers"][0]
    container["image"] = image
    kafka_volume = next(volume for volume in pod["volumes"] if volume["name"] == "kafka-ca")
    referenced_secrets = [
        variable.get("valueFrom", {}).get("secretKeyRef", {}).get("name", "")
        for variable in container["env"]
    ] + [kafka_volume["secret"]["secretName"]]
    require(
        all("cutover" not in name for name in referenced_secrets),
        "disposable render contains a cutover Secret reference",
    )
    config = next(item for item in items if item["kind"] == "ConfigMap")
    config["data"]["SERVICE_VERSION"] = f"git-{source_short}"
    config["data"].update(
        {
            "ELASTICSEARCH_INDEX": "policy-views",
            "KAFKA_GROUP": "policy-search-service",
            "KAFKA_REGISTERED_TOPIC": REGISTERED_TOPIC,
            "KAFKA_TERMINATED_TOPIC": TERMINATED_TOPIC,
        }
    )
    config["data"]["ELASTICSEARCH_URL"] = (
        f"http://local-dev-elasticsearch-es-http.{cluster.namespace}.svc:9200"
    )
    policy = next(item for item in items if item["kind"] == "NetworkPolicy")
    for direction, peer_key in (("ingress", "from"), ("egress", "to")):
        for rule in policy["spec"][direction]:
            for peer in rule.get(peer_key, []):
                labels = peer.get("namespaceSelector", {}).get("matchLabels", {})
                if labels.get("kubernetes.io/metadata.name") == "local-dev-all":
                    labels["kubernetes.io/metadata.name"] = cluster.namespace
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
    probe_image = f"insurance-hub-policy-search-smoke:git-{source_short}"
    with tempfile.TemporaryDirectory(prefix="policy-search-smoke-") as directory:
        binary = Path(directory) / "smoke-client"
        command(
            [
                "make",
                "-C",
                str(MODULE),
                "build-smoke-client",
                f"SMOKE_CLIENT_OUTPUT={binary}",
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
                str(MODULE / "testdata/smoke/Dockerfile"),
                directory,
            ],
            timeout=180,
        )
    elasticsearch_image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    kafka_image = os.environ.get("KAFKA_IMAGE", "")
    require(elasticsearch_image, "ELASTICSEARCH_IMAGE is required")
    require(kafka_image, "KAFKA_IMAGE is required")
    command(
        [
            "kind",
            "load",
            "docker-image",
            image,
            probe_image,
            elasticsearch_image,
            kafka_image,
            "--name",
            KIND_CLUSTER,
        ],
        timeout=300,
    )
    return probe_image


def elasticsearch_resources(cluster: Cluster, image: str, admin_password: str) -> str:
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
    bootstrap_secret = {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {
            "name": "local-dev-elasticsearch-bootstrap",
            "namespace": cluster.namespace,
            "labels": labels,
        },
        "type": "Opaque",
        "stringData": {"password": admin_password},
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
                                "tcpSocket": {"port": 9200},
                                "periodSeconds": 2,
                                "timeoutSeconds": 1,
                                "failureThreshold": 90,
                            },
                            "env": [
                                {"name": "discovery.type", "value": "single-node"},
                                {"name": "xpack.security.enabled", "value": "true"},
                                {
                                    "name": "xpack.security.http.ssl.enabled",
                                    "value": "false",
                                },
                                {
                                    "name": "ELASTIC_PASSWORD",
                                    "valueFrom": {
                                        "secretKeyRef": {
                                            "name": "local-dev-elasticsearch-bootstrap",
                                            "key": "password",
                                        }
                                    },
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
    return yaml.safe_dump_all(
        [bootstrap_secret, service, deployment], explicit_start=True, sort_keys=False
    )


@contextmanager
def port_forward(
    cluster: Cluster,
    service: str = "local-dev-elasticsearch-es-http",
    remote_port: int = 9200,
):
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
            f"service/{service}",
            f"{port}:{remote_port}",
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
                raise CheckFailed(f"{service} port-forward stopped early")
            if time.monotonic() - stable_since >= 1:
                break
            time.sleep(0.1)
        else:
            raise CheckFailed(f"{service} port-forward did not become ready")
        yield port
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)


def setup_elasticsearch(
    cluster: Cluster,
    image: str,
    admin_password: str,
    reader_password: str,
    writer_password: str,
) -> None:
    cluster.kubectl(
        "apply",
        "-f",
        "-",
        input_text=elasticsearch_resources(cluster, image, admin_password),
    )
    cluster.kubectl(
        "rollout",
        "status",
        "deployment/local-dev-elasticsearch",
        "--timeout=180s",
        timeout=200,
    )
    with port_forward(cluster) as port:
        admin = ElasticsearchClient(port, "elastic", admin_password)
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
            except AdministrationError:
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
        service_admin.ensure_index_and_access(admin, reader_password, writer_password)
        status, _ = admin.request(
            "PUT",
            f"/{service_admin.INDEX}/_doc/disposable-search?refresh=true",
            {
                "number": "POLICY-SYNTHETIC-001",
                "policyHolder": "Synthetic Person",
                "dateFrom": "2026-01-01",
                "dateTo": "2026-12-31",
            },
        )
        require_status(status, {200, 201}, "create disposable search document")
        reader = ElasticsearchClient(port, service_admin.READER, reader_password)
        service_admin.verify_identity(reader, service_admin.READER)
        writer = ElasticsearchClient(port, service_admin.WRITER, writer_password)
        service_admin.verify_identity(writer, service_admin.WRITER)
    credentials = []
    for name, username, password in (
        (
            "local-dev-policy-search-elasticsearch",
            service_admin.READER,
            reader_password,
        ),
        (
            "local-dev-policy-search-elasticsearch-writer",
            service_admin.WRITER,
            writer_password,
        ),
    ):
        credentials.append(
            {
                "apiVersion": "v1",
                "kind": "Secret",
                "metadata": {
                    "name": name,
                    "namespace": cluster.namespace,
                    "labels": {OWNER_LABEL: cluster.namespace},
                },
                "type": "Opaque",
                "stringData": {"username": username, "password": password},
            }
        )
    cluster.kubectl(
        "apply",
        "-f",
        "-",
        input_text=yaml.safe_dump_all(credentials, explicit_start=True, sort_keys=False),
    )


def create_kafka_certificates(root: Path, namespace: str) -> dict[str, bytes]:
    ca_key = root / "ca.key"
    ca_certificate = root / "ca.crt"
    server_key = root / "server.key"
    server_request = root / "server.csr"
    server_certificate = root / "server.crt"
    extensions = root / "server.ext"
    keystore = root / "server.p12"
    bootstrap = "local-dev-kafka-kafka-bootstrap"
    extensions.write_text(
        "subjectAltName="
        f"DNS:{bootstrap},DNS:{bootstrap}.{namespace}.svc,DNS:localhost,IP:127.0.0.1\n"
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
            "/CN=policy-search-disposable-ca",
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
            f"/CN={bootstrap}",
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
        command(arguments, timeout=30)
    return {
        "ca.crt": ca_certificate.read_bytes(),
        "server.p12": keystore.read_bytes(),
    }


def kafka_resources(
    cluster: Cluster,
    image: str,
    certificates: dict[str, bytes],
) -> str:
    labels = {
        OWNER_LABEL: cluster.namespace,
        "strimzi.io/cluster": "local-dev-kafka",
        "strimzi.io/component-type": "kafka",
    }
    server_secret = {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {
            "name": "local-dev-kafka-tls",
            "namespace": cluster.namespace,
            "labels": labels,
        },
        "type": "Opaque",
        "data": {
            name: base64.b64encode(value).decode("ascii") for name, value in certificates.items()
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
    }
    workload_ca = {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {
            "name": "local-dev-policy-search-kafka-ca",
            "namespace": cluster.namespace,
            "labels": {OWNER_LABEL: cluster.namespace},
        },
        "type": "Opaque",
        "data": {"ca.crt": base64.b64encode(certificates["ca.crt"]).decode("ascii")},
    }
    service = {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {
            "name": "local-dev-kafka-kafka-bootstrap",
            "namespace": cluster.namespace,
            "labels": labels,
        },
        "spec": {
            "selector": labels,
            "ports": [{"name": "tls", "port": 9093, "targetPort": 9093}],
        },
    }
    deployment = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {
            "name": "local-dev-kafka",
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
                    "volumes": [
                        {
                            "name": "tls",
                            "secret": {
                                "secretName": "local-dev-kafka-tls",
                                "defaultMode": 0o444,
                            },
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
                                {
                                    "name": "KAFKA_PROCESS_ROLES",
                                    "value": "broker,controller",
                                },
                                {
                                    "name": "KAFKA_LISTENERS",
                                    "value": (
                                        "SSL://0.0.0.0:9093,"
                                        "CONTROLLER://0.0.0.0:9094,"
                                        "INTERNAL://0.0.0.0:9095"
                                    ),
                                },
                                {
                                    "name": "KAFKA_ADVERTISED_LISTENERS",
                                    "value": (
                                        "SSL://local-dev-kafka-kafka-bootstrap:9093,"
                                        "INTERNAL://localhost:9095"
                                    ),
                                },
                                {
                                    "name": "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP",
                                    "value": ("SSL:SSL,CONTROLLER:PLAINTEXT,INTERNAL:PLAINTEXT"),
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
                                {
                                    "name": "KAFKA_SSL_KEYSTORE_FILENAME",
                                    "value": "server.p12",
                                },
                                {
                                    "name": "KAFKA_SSL_KEYSTORE_TYPE",
                                    "value": "PKCS12",
                                },
                                {
                                    "name": "KAFKA_SSL_KEYSTORE_CREDENTIALS",
                                    "value": "keystore-password",
                                },
                                {
                                    "name": "KAFKA_SSL_KEY_CREDENTIALS",
                                    "value": "key-password",
                                },
                                {"name": "KAFKA_SSL_CLIENT_AUTH", "value": "none"},
                                {
                                    "name": "KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR",
                                    "value": "1",
                                },
                                {
                                    "name": "KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR",
                                    "value": "1",
                                },
                                {
                                    "name": "KAFKA_TRANSACTION_STATE_LOG_MIN_ISR",
                                    "value": "1",
                                },
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
    }
    return yaml.safe_dump_all(
        [server_secret, workload_ca, service, deployment],
        explicit_start=True,
        sort_keys=False,
    )


def kafka_cli(
    cluster: Cluster,
    executable: str,
    *arguments: str,
    input_text: str | None = None,
    timeout: int = 60,
) -> str:
    exec_arguments = ["exec"]
    if input_text is not None:
        exec_arguments.append("--stdin")
    return cluster.kubectl(
        *exec_arguments,
        "deployment/local-dev-kafka",
        "--",
        f"/opt/kafka/bin/{executable}",
        *arguments,
        input_text=input_text,
        timeout=timeout,
    )


def setup_kafka(cluster: Cluster, image: str) -> None:
    with tempfile.TemporaryDirectory(prefix="policy-search-kind-kafka-") as directory:
        certificates = create_kafka_certificates(Path(directory), cluster.namespace)
        cluster.kubectl(
            "apply",
            "-f",
            "-",
            input_text=kafka_resources(cluster, image, certificates),
        )
    cluster.kubectl(
        "rollout",
        "status",
        "deployment/local-dev-kafka",
        "--timeout=180s",
        timeout=200,
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

    wait_for(cluster, "TLS Kafka readiness", ready, seconds=120)
    for topic in (REGISTERED_TOPIC, TERMINATED_TOPIC):
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
            topic,
            "--partitions",
            "1",
            "--replication-factor",
            "1",
        )


def verify_network_denial(cluster: Cluster, probe_image: str) -> None:
    business = "local-dev-policy-search-api"
    management = "local-dev-policy-search-api-management"
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


def verify_business_transports(cluster: Cluster, probe_image: str) -> None:
    labels = {
        "app.kubernetes.io/name": "agent-portal-gateway-legacy",
        "app.kubernetes.io/instance": "insurance-hub-agent-portal-gateway-legacy",
        "app.kubernetes.io/component": "gateway",
        "environment": "local-dev",
        "insurance-hub.io/policy-search-business-client": "true",
    }
    probe(
        cluster,
        probe_image,
        "allowed-http",
        [
            "--mode=http",
            "--address=http://local-dev-policy-search-api/policies",
            "--query=Synthetic Person",
        ],
        labels,
    )
    probe(
        cluster,
        probe_image,
        "allowed-grpc",
        [
            "--mode=grpc",
            "--address=local-dev-policy-search-api:9090",
            "--query=POLICY-SYNTHETIC-001",
        ],
        labels,
    )


def verify_search(cluster: Cluster, query: str) -> None:
    with port_forward(cluster, "local-dev-policy-search-api", 80) as port:
        encoded_query = urllib.parse.urlencode({"q": query})
        try:
            with urllib.request.urlopen(
                f"http://127.0.0.1:{port}/policies?{encoded_query}", timeout=5
            ) as response:
                status, body = response.status, response.read()
        except OSError as error:
            raise CheckFailed("Policy Search HTTP verification failed") from error
    deployment_checks.validate_deployed_search(status, body)


def verify_http_edges(cluster: Cluster) -> None:
    with port_forward(cluster, "local-dev-policy-search-api", 80) as port:
        base = f"http://127.0.0.1:{port}/policies"
        try:
            urllib.request.urlopen(base, timeout=5)
        except urllib.error.HTTPError as error:
            require(error.code == 400, "missing HTTP q status differs")
        else:
            raise CheckFailed("missing HTTP q was accepted")
        with urllib.request.urlopen(base + "?q=NO_MATCH_TOKEN", timeout=5) as response:
            require(
                response.status == 200 and response.read() == b"{}", "no-match response differs"
            )


def exercise_event_projection(
    cluster: Cluster,
    admin_password: str,
) -> None:
    oracle = json.loads((MODULE / "testdata/events/oracle.json").read_text(encoding="utf-8"))
    registration = oracle.get("events", {}).get("registration", {})
    termination = oracle.get("events", {}).get("termination", {})
    event = registration.get("value")
    registered = registration.get("expectedDocument")
    terminated = termination.get("expectedDocument")
    require(
        oracle.get("ownership") == "policy-search-disposable"
        and registration.get("key") == "SYN-230-001"
        and termination.get("key") == registration.get("key")
        and isinstance(event, dict)
        and isinstance(registered, dict)
        and isinstance(terminated, dict),
        "event oracle differs",
    )
    with port_forward(cluster) as port:
        admin = ElasticsearchClient(port, "elastic", admin_password)
        for event_number, (topic, payload, expected) in enumerate(
            (
                (REGISTERED_TOPIC, registration["value"], registered),
                (REGISTERED_TOPIC, registration["value"], registered),
                (TERMINATED_TOPIC, termination["value"], terminated),
            ),
            start=1,
        ):
            kafka_cli(
                cluster,
                "kafka-console-producer.sh",
                "--bootstrap-server",
                "localhost:9093",
                "--producer.config",
                "/etc/kafka/secrets/client.properties",
                "--topic",
                topic,
                input_text=json.dumps(payload, separators=(",", ":")) + "\n",
            )
            projected: dict[str, Any] | None = None

            def stored(expected: dict[str, Any] = expected) -> bool:
                nonlocal projected
                try:
                    status, result = admin.request(
                        "GET", f"/{service_admin.INDEX}/_doc/{registration['key']}"
                    )
                except AdministrationError:
                    return False
                if status == 200 and isinstance(result, dict):
                    projected = result.get("_source")
                return projected == expected

            wait_for(cluster, f"deployed event projection {event_number}", stored, seconds=90)
        require(
            registered.get("dateTo") == "2026-12-31" and terminated.get("dateTo") == "2026-06-30",
            "event date transition differs",
        )
        status, _ = admin.request("POST", f"/{service_admin.INDEX}/_refresh")
        require_status(status, {200}, "refresh projected event document")
        status, result = admin.request(
            "POST",
            f"/{service_admin.INDEX}/_count",
            {"query": {"term": {"number": registration["key"]}}},
        )
        require_status(status, {200}, "count projected event document")
        require(
            isinstance(result, dict) and result.get("count") == 1,
            "duplicate event created more than one document",
        )


def verify_setup_rerun(
    cluster: Cluster,
    admin_password: str,
    reader_password: str,
    writer_password: str,
) -> None:
    with port_forward(cluster) as port:
        admin = ElasticsearchClient(port, "elastic", admin_password)
        before = service_admin.inspect_index(admin)
        ensured = service_admin.ensure_index_and_access(admin, reader_password, writer_password)
        after = service_admin.inspect_index(admin)
    require(before.documents >= 2, "populated disposable state is missing")
    require(ensured == before and after == before, "setup rerun changed populated state")


def main() -> int:
    image = os.environ.get("IMAGE_REF", "")
    elasticsearch_image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    kafka_image = os.environ.get("KAFKA_IMAGE", "")
    require(image.startswith("insurance-hub-policy-search-api:1.0.0-local."), "IMAGE_REF differs")
    require(elasticsearch_image, "ELASTICSEARCH_IMAGE is required")
    require(kafka_image, "KAFKA_IMAGE is required")
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
        admin_password = secrets.token_urlsafe(32)
        reader_password = secrets.token_urlsafe(32)
        writer_password = secrets.token_urlsafe(32)
        setup_elasticsearch(
            cluster,
            elasticsearch_image,
            admin_password,
            reader_password,
            writer_password,
        )
        setup_kafka(cluster, kafka_image)
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
        verify_network_denial(cluster, probe_image)
        verify_business_transports(cluster, probe_image)
        verify_search(cluster, "Synthetic Person")
        verify_http_edges(cluster)
        exercise_event_projection(cluster, admin_password)
        verify_search(cluster, 'number:"SYN-230-001"')
        verify_setup_rerun(cluster, admin_password, reader_password, writer_password)
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
        verify_search(cluster, "Synthetic Person")
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
            "policy-search-disposable: TLS event projection, writer permissions, search, policy, "
            "dependency recovery, restart and restoration passed"
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
