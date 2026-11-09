#!/usr/bin/env python3
"""Inspect and retire Dashboard issue-290 resources on exact enrolled targets."""

from __future__ import annotations

import base64
import http.client
import json
import os
import re
import secrets
import signal
import socket
import ssl
import subprocess
import sys
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

from scripts.go.service.configuration import service_record, target_record  # noqa: E402
from scripts.python.process_runner import CommandError, run_command  # noqa: E402

SERVICE = "dashboard"
OWNER = "issue-290"
INDEX = "policy_stats_issue_290"
READER = "go_dashboard_issue_290_reader"
WRITER = "go_dashboard_issue_290_writer"
TOPIC = "policy-registered-issue-290"
GROUP = "dashboard-service-issue-290"
ASSET = ROOT / "services/dashboard-service/internal/administration/retirement-registration.json"
MAX_RESPONSE_BYTES = 64 * 1024
PASSWORD_BYTES = 48


class AdministrationError(RuntimeError):
    """The selected target or resource state differs from the accepted contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise AdministrationError(message)


def safe_environment() -> dict[str, str]:
    allowed = ("HOME", "KUBECONFIG", "PATH", "SSL_CERT_DIR", "SSL_CERT_FILE", "TMPDIR")
    return {name: os.environ[name] for name in allowed if name in os.environ}


def command(arguments: list[str], *, input_text: str | None = None, timeout: int = 30) -> str:
    try:
        result = run_command(
            arguments,
            cwd=ROOT,
            env=safe_environment(),
            input_text=input_text,
            timeout_seconds=timeout,
            teardown_seconds=10,
            activity="Dashboard retirement administration",
        )
    except CommandError as error:
        raise AdministrationError(
            f"command failed: {arguments[0]}; inspect the selected target safely"
        ) from error
    return result.stdout


@dataclass(frozen=True)
class Definition:
    mapping: dict[str, Any]


def load_definition(path: Path = ASSET) -> Definition:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise AdministrationError("retirement administration asset is unavailable") from error
    require(
        value.get("schemaVersion") == 1
        and value.get("owner") == OWNER
        and value.get("index") == INDEX,
        "retirement administration identity differs",
    )
    mapping = value.get("mapping")
    require(
        isinstance(mapping, dict)
        and mapping.get("dynamic") == "strict"
        and set(mapping.get("properties", {}))
        == {"number", "from", "to", "policyHolder", "productCode", "totalPremium", "agentLogin"},
        "retirement mapping differs",
    )
    return Definition(mapping)


class ForwardedHTTPSConnection(http.client.HTTPSConnection):
    def __init__(self, server_name: str, port: int, context: ssl.SSLContext) -> None:
        super().__init__(server_name, 443, context=context, timeout=10)
        self.forwarded_port = port

    def connect(self) -> None:
        plain = socket.create_connection(("127.0.0.1", self.forwarded_port), self.timeout)
        try:
            self.sock = self._context.wrap_socket(plain, server_hostname=self.host)
        except BaseException:
            plain.close()
            raise


class ElasticsearchClient:
    def __init__(
        self,
        port: int,
        username: str,
        password: str,
        *,
        server_name: str = "",
        ca_certificate: bytes = b"",
    ) -> None:
        self.port = port
        self.username = username
        self.password = password
        self.server_name = server_name
        self.context: ssl.SSLContext | None = None
        if server_name:
            require(bool(ca_certificate), "QA public CA is required")
            try:
                self.context = ssl.create_default_context(cadata=ca_certificate.decode("utf-8"))
            except (UnicodeDecodeError, ssl.SSLError) as error:
                raise AdministrationError("QA public CA is malformed") from error
            self.context.minimum_version = ssl.TLSVersion.TLSv1_2

    def request(
        self,
        method: str,
        path: str,
        body: dict[str, Any] | None = None,
    ) -> tuple[int, Any]:
        payload = None if body is None else json.dumps(body, separators=(",", ":")).encode()
        token = base64.b64encode(f"{self.username}:{self.password}".encode()).decode("ascii")
        headers = {"Authorization": f"Basic {token}"}
        if payload is not None:
            headers["Content-Type"] = "application/json"
        connection: http.client.HTTPConnection
        if self.context is None:
            connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=10)
        else:
            connection = ForwardedHTTPSConnection(self.server_name, self.port, self.context)
        try:
            connection.request(method, path, body=payload, headers=headers)
            response = connection.getresponse()
            data = response.read(MAX_RESPONSE_BYTES + 1)
            require(len(data) <= MAX_RESPONSE_BYTES, "Elasticsearch response exceeded limit")
            try:
                decoded: Any = json.loads(data) if data else None
            except json.JSONDecodeError:
                decoded = None
            return response.status, decoded
        except (OSError, http.client.HTTPException, ssl.SSLError) as error:
            raise AdministrationError("Elasticsearch request failed") from error
        finally:
            connection.close()


class Target:
    """Resolve and guard the exact catalog target before any effect."""

    def __init__(self) -> None:
        record = service_record(ROOT, SERVICE)
        contract = record.get("retirementAdministration", {})
        require(
            contract
            == {
                "owner": OWNER,
                "asset": "services/dashboard-service/internal/administration/retirement-registration.json",
                "index": INDEX,
                "readerRole": READER,
                "readerUser": READER,
                "writerRole": WRITER,
                "writerUser": WRITER,
                "registeredTopic": TOPIC,
                "consumerGroup": GROUP,
            },
            "retirement administration catalog contract differs",
        )
        self.environment = os.environ.get("DEPLOY_ENV", "")
        require(self.environment in record.get("environments", []), "DEPLOY_ENV is invalid")
        selected = target_record(ROOT, record, self.environment)
        expected_coordinates = {
            "DEPLOY_CONTEXT": selected["context"],
            "DEPLOY_SVC_NS": selected["namespace"],
            "DEPLOY_DATA_NS": selected["dataNamespace"],
        }
        for name, value in expected_coordinates.items():
            require(os.environ.get(name, "") == value, f"{name} differs from the catalog")
        event = selected.get("retirementAdministration", {})
        expected = {
            "local-dev": {
                "service": "local-dev-elasticsearch-es-http",
                "adminSecret": "local-dev-elasticsearch-es-elastic-user",
                "readerSecret": "local-dev-dashboard-elasticsearch-issue-290",
                "writerSecret": "local-dev-dashboard-elasticsearch-writer-issue-290",
                "publicCASecret": "",
                "serverName": "",
                "kafkaCluster": "local-dev-kafka",
                "kafkaBootstrap": "local-dev-kafka-kafka-bootstrap:9093",
                "kafkaPublicCASecret": "local-dev-kafka-cluster-ca-cert",
                "kafkaWorkloadCASecret": "local-dev-dashboard-kafka-ca-issue-290",
                "kafkaReplicas": 1,
            },
            "qa": {
                "service": "qa-elasticsearch-es-http",
                "adminSecret": "qa-elasticsearch-es-elastic-user",
                "readerSecret": "qa-dashboard-elasticsearch-issue-290",
                "writerSecret": "qa-dashboard-elasticsearch-writer-issue-290",
                "publicCASecret": "qa-elasticsearch-es-http-certs-public",
                "serverName": "qa-elasticsearch-es-http.qa-data.svc",
                "kafkaCluster": "qa-kafka",
                "kafkaBootstrap": "qa-kafka-kafka-bootstrap.qa-data.svc:9093",
                "kafkaPublicCASecret": "qa-kafka-cluster-ca-cert",
                "kafkaWorkloadCASecret": "qa-dashboard-kafka-ca-issue-290",
                "kafkaReplicas": 3,
            },
        }[self.environment]
        require(event == expected, "retirement target catalog contract differs")
        self.context = selected["context"]
        self.namespace = selected["namespace"]
        self.data_namespace = selected["dataNamespace"]
        self.elasticsearch_service = event["service"]
        self.admin_secret = event["adminSecret"]
        self.reader_secret = event["readerSecret"]
        self.writer_secret = event["writerSecret"]
        self.public_ca_secret = event["publicCASecret"]
        self.server_name = event["serverName"]
        self.kafka_cluster = event["kafkaCluster"]
        self.kafka_bootstrap = event["kafkaBootstrap"]
        self.kafka_public_ca_secret = event["kafkaPublicCASecret"]
        self.kafka_ca_secret = event["kafkaWorkloadCASecret"]
        self.kafka_replicas = event["kafkaReplicas"]
        self.kubectl = os.environ.get("KUBECTL", "kubectl")

    def run(
        self,
        *arguments: str,
        namespace: str | None = None,
        input_text: str | None = None,
    ) -> str:
        return command(
            [
                self.kubectl,
                "--context",
                self.context,
                "--namespace",
                namespace or self.namespace,
                *arguments,
            ],
            input_text=input_text,
        )

    def json(self, *arguments: str, namespace: str | None = None) -> dict[str, Any]:
        try:
            value = json.loads(self.run(*arguments, "-o", "json", namespace=namespace) or "{}")
        except json.JSONDecodeError as error:
            raise AdministrationError("kubectl returned malformed JSON") from error
        require(isinstance(value, dict), "kubectl returned unexpected JSON")
        return value

    def verify_boundary(self) -> None:
        require(
            command([self.kubectl, "config", "current-context"]).strip() == self.context,
            "current Kubernetes context differs",
        )
        service = self.json(
            "get", "service", self.elasticsearch_service, namespace=self.data_namespace
        )
        require(
            service.get("metadata", {}).get("name") == self.elasticsearch_service
            and service.get("metadata", {}).get("namespace") == self.data_namespace
            and service.get("spec", {}).get("type") == "ClusterIP"
            and service.get("spec", {})
            .get("selector", {})
            .get("elasticsearch.k8s.elastic.co/cluster-name")
            == f"{self.environment}-elasticsearch"
            and any(item.get("port") == 9200 for item in service.get("spec", {}).get("ports", [])),
            "Elasticsearch target identity differs",
        )
        kafka = self.json("get", "kafka", self.kafka_cluster, namespace=self.data_namespace)
        listeners = kafka.get("spec", {}).get("kafka", {}).get("listeners", [])
        require(
            kafka.get("metadata", {}).get("name") == self.kafka_cluster
            and kafka.get("metadata", {}).get("namespace") == self.data_namespace
            and any(
                item.get("name") == "tls" and item.get("port") == 9093 and item.get("tls") is True
                for item in listeners
            ),
            "Kafka TLS target identity differs",
        )
        bootstrap = self.json(
            "get", "service", f"{self.kafka_cluster}-kafka-bootstrap", namespace=self.data_namespace
        )
        require(
            bootstrap.get("spec", {}).get("selector", {}).get("strimzi.io/cluster")
            == self.kafka_cluster
            and any(
                item.get("port") == 9093 for item in bootstrap.get("spec", {}).get("ports", [])
            ),
            "Kafka bootstrap Service differs",
        )

    def secret_value(self, namespace: str, name: str, key: str) -> bytes:
        encoded = self.json("get", "secret", name, namespace=namespace).get("data", {}).get(key)
        require(isinstance(encoded, str) and encoded, f"Secret/{name} is missing {key}")
        try:
            return base64.b64decode(encoded, validate=True)
        except ValueError as error:
            raise AdministrationError(f"Secret/{name} has malformed {key}") from error

    def optional_password(self, name: str, username: str, component: str) -> str:
        value = self.json("get", "secret", name, "--ignore-not-found")
        if not value:
            return ""
        self.validate_secret(value, name, {"username", "password"}, component)
        try:
            actual_username = base64.b64decode(value["data"]["username"], validate=True).decode(
                "utf-8"
            )
            password = base64.b64decode(value["data"]["password"], validate=True).decode("utf-8")
        except (TypeError, ValueError, UnicodeDecodeError) as error:
            raise AdministrationError(f"Secret/{name} data is malformed") from error
        require(actual_username == username, f"Secret/{name} username differs")
        require_password(password)
        return password

    def validate_secret(
        self, value: dict[str, Any], name: str, keys: set[str], component: str
    ) -> None:
        metadata = value.get("metadata", {})
        require(
            value.get("kind") == "Secret"
            and metadata.get("name") == name
            and metadata.get("namespace") == self.namespace
            and metadata.get("labels", {}).get("insurance-hub.io/owner") == OWNER
            and metadata.get("labels", {}).get("app.kubernetes.io/component") == component
            and value.get("type", "Opaque") == "Opaque"
            and set(value.get("data", {})) == keys,
            f"Secret/{name} identity or keys differ",
        )

    def apply_secret(self, name: str, values: dict[str, str], component: str) -> None:
        value = {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": {
                "name": name,
                "namespace": self.namespace,
                "labels": {
                    "app.kubernetes.io/name": "dashboard-api",
                    "app.kubernetes.io/component": component,
                    "insurance-hub.io/owner": OWNER,
                },
            },
            "type": "Opaque",
            "data": {
                key: base64.b64encode(item.encode("utf-8")).decode("ascii")
                for key, item in values.items()
            },
        }
        self.run(
            "apply",
            "--server-side",
            "--field-manager=dashboard-event-administration",
            "-f",
            "-",
            input_text=json.dumps(value),
        )

    def apply_topic(self) -> None:
        value = {
            "apiVersion": "kafka.strimzi.io/v1beta2",
            "kind": "KafkaTopic",
            "metadata": {
                "name": TOPIC,
                "namespace": self.data_namespace,
                "labels": {
                    "strimzi.io/cluster": self.kafka_cluster,
                    "insurance-hub.io/owner": OWNER,
                },
            },
            "spec": {"partitions": 1, "replicas": self.kafka_replicas},
        }
        self.run(
            "apply",
            "--server-side",
            "--field-manager=dashboard-event-administration",
            "-f",
            "-",
            namespace=self.data_namespace,
            input_text=json.dumps(value),
        )

    def verify_topic(self) -> None:
        value = self.json("get", "kafkatopic", TOPIC, namespace=self.data_namespace)
        require(
            value.get("metadata", {}).get("labels", {}).get("strimzi.io/cluster")
            == self.kafka_cluster
            and value.get("metadata", {}).get("labels", {}).get("insurance-hub.io/owner") == OWNER
            and value.get("spec") == {"partitions": 1, "replicas": self.kafka_replicas},
            f"KafkaTopic/{TOPIC} differs",
        )

    def kafka_broker(self) -> str:
        pods = self.json(
            "get",
            "pods",
            "-l",
            f"strimzi.io/cluster={self.kafka_cluster}",
            namespace=self.data_namespace,
        ).get("items", [])
        ready = sorted(
            pod.get("metadata", {}).get("name", "")
            for pod in pods
            if not pod.get("metadata", {}).get("deletionTimestamp")
            and pod.get("metadata", {}).get("labels", {}).get("strimzi.io/broker-role") == "true"
            and pod.get("status", {}).get("phase") == "Running"
            and any(
                condition.get("type") == "Ready" and condition.get("status") == "True"
                for condition in pod.get("status", {}).get("conditions", [])
            )
        )
        require(bool(ready) and ready[0], "no ready Kafka broker Pod is available")
        return ready[0]

    def consumer_group_state(self) -> dict[str, Any]:
        broker = self.kafka_broker()
        base = (
            "exec",
            broker,
            "-c",
            "kafka",
            "--",
            "/opt/kafka/bin/kafka-consumer-groups.sh",
            "--bootstrap-server",
            "localhost:9092",
        )
        groups = set(self.run(*base, "--list", namespace=self.data_namespace).splitlines())
        if GROUP not in groups:
            return {"present": False, "activeMembers": 0, "offsets": []}
        output = self.run(*base, "--describe", "--group", GROUP, namespace=self.data_namespace)
        offsets: list[dict[str, int]] = []
        members: set[str] = set()
        for line in output.splitlines():
            fields = line.split()
            if len(fields) < 6 or fields[0] != GROUP:
                continue
            require(fields[1] == TOPIC, "retirement group contains an unexpected topic")
            try:
                partition, committed, log_end = map(int, fields[2:5])
            except ValueError as error:
                raise AdministrationError("retirement group progress differs") from error
            require(0 <= committed <= log_end, "retirement group offset is out of range")
            offsets.append({"partition": partition, "committed": committed, "logEnd": log_end})
            if len(fields) > 6 and fields[6] != "-":
                members.add(fields[6])
        require(bool(offsets), "retirement group partition progress is incomplete")
        require(
            len({item["partition"] for item in offsets}) == len(offsets),
            "retirement group contains duplicate partitions",
        )
        return {
            "present": True,
            "activeMembers": len(members),
            "offsets": sorted(offsets, key=lambda item: item["partition"]),
        }

    def delete_consumer_group(self) -> None:
        state = self.consumer_group_state()
        require(state["activeMembers"] == 0, "retirement group still has active members")
        if state["present"]:
            broker = self.kafka_broker()
            self.run(
                "exec",
                broker,
                "-c",
                "kafka",
                "--",
                "/opt/kafka/bin/kafka-consumer-groups.sh",
                "--bootstrap-server",
                "localhost:9092",
                "--delete",
                "--group",
                GROUP,
                namespace=self.data_namespace,
            )
        require(not self.consumer_group_state()["present"], "retirement group was not deleted")


class PortForward:
    def __init__(self, target: Target) -> None:
        self.target = target
        self.port = free_port()
        self.process: subprocess.Popen[bytes] | None = None

    def __enter__(self) -> int:
        self.process = subprocess.Popen(
            [
                self.target.kubectl,
                "--context",
                self.target.context,
                "--namespace",
                self.target.data_namespace,
                "port-forward",
                f"service/{self.target.elasticsearch_service}",
                f"{self.port}:9200",
            ],
            env=safe_environment(),
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
        try:
            deadline = time.monotonic() + 10
            stable_since = time.monotonic()
            while time.monotonic() < deadline:
                if self.process.poll() is not None:
                    raise AdministrationError("Elasticsearch port-forward stopped early")
                if time.monotonic() - stable_since >= 1:
                    return self.port
                time.sleep(0.1)
            raise AdministrationError("Elasticsearch port-forward did not become ready")
        except BaseException:
            self._stop()
            raise

    def __exit__(self, *_: object) -> None:
        self._stop()

    def _stop(self) -> None:
        if self.process is None or self.process.poll() is not None:
            return
        os.killpg(self.process.pid, signal.SIGTERM)
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(self.process.pid, signal.SIGKILL)
            self.process.wait(timeout=5)


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def require_password(password: str) -> None:
    require(bool(re.fullmatch(r"[A-Za-z0-9_-]{64}", password)), "password is not canonical")


def new_password() -> str:
    value = secrets.token_urlsafe(PASSWORD_BYTES)
    require_password(value)
    return value


def role(identity: str, privileges: list[str]) -> dict[str, Any]:
    return {
        "cluster": [],
        "indices": [
            {
                "names": [INDEX],
                "privileges": privileges,
                "allow_restricted_indices": False,
            }
        ],
        "applications": [],
        "run_as": [],
        "metadata": {"owner": OWNER},
        "remote_indices": [],
        "remote_cluster": [],
    }


def user(identity: str, password: str, label: str) -> dict[str, Any]:
    return {
        "password": password,
        "roles": [identity],
        "full_name": f"Dashboard issue-290 {label}",
        "metadata": {"owner": OWNER},
        "enabled": True,
    }


def require_status(status: int, accepted: set[int], action: str) -> None:
    require(status in accepted, f"{action} returned unexpected status {status}")


def inspect_index(client: ElasticsearchClient, definition: Definition) -> int | None:
    status, value = client.request("GET", f"/{INDEX}/_mapping")
    if status == 404:
        return None
    require_status(status, {200}, "inspect isolated index mapping")
    require(
        isinstance(value, dict)
        and set(value) == {INDEX}
        and value[INDEX].get("mappings") == definition.mapping,
        "isolated index mapping differs",
    )
    status, value = client.request("GET", f"/{INDEX}/_count")
    require_status(status, {200}, "count isolated documents")
    require(isinstance(value, dict) and isinstance(value.get("count"), int), "index count differs")
    return int(value["count"])


def inspect_identity(client: ElasticsearchClient, identity: str) -> tuple[Any, Any]:
    status, roles = client.request("GET", f"/_security/role/{identity}")
    require_status(status, {200, 404}, f"inspect {identity} role")
    stored_role = roles.get(identity) if status == 200 and isinstance(roles, dict) else None
    status, users = client.request("GET", f"/_security/user/{identity}")
    require_status(status, {200, 404}, f"inspect {identity} user")
    stored_user = users.get(identity) if status == 200 and isinstance(users, dict) else None
    for value in (stored_role, stored_user):
        if value is not None:
            require(value.get("metadata") == {"owner": OWNER}, f"{identity} is not owned")
    return stored_role, stored_user


def normalize_role(value: dict[str, Any]) -> dict[str, Any]:
    normalized = dict(value)
    normalized.pop("transient_metadata", None)
    normalized.setdefault("remote_indices", [])
    normalized.setdefault("remote_cluster", [])
    return normalized


def verify_identity(client: ElasticsearchClient, identity: str, privileges: list[str]) -> None:
    stored_role, stored_user = inspect_identity(client, identity)
    require(stored_role is not None and stored_user is not None, f"{identity} is absent")
    require(normalize_role(stored_role) == role(identity, privileges), f"{identity} role differs")
    require(
        stored_user.get("roles") == [identity]
        and stored_user.get("enabled") is True
        and stored_user.get("metadata") == {"owner": OWNER},
        f"{identity} user differs",
    )


def put_identity(
    client: ElasticsearchClient,
    identity: str,
    privileges: list[str],
    password: str,
    label: str,
) -> None:
    status, _ = client.request("PUT", f"/_security/role/{identity}", role(identity, privileges))
    require_status(status, {200}, f"apply {identity} role")
    status, _ = client.request(
        "PUT", f"/_security/user/{identity}", user(identity, password, label)
    )
    require_status(status, {200}, f"apply {identity} user")
    verify_identity(client, identity, privileges)


def verify_privileges(client: ElasticsearchClient, identity: str) -> None:
    status, value = client.request(
        "POST",
        "/_security/user/_has_privileges",
        {
            "cluster": ["manage_security", "monitor"],
            "index": [
                {
                    "names": [INDEX],
                    "privileges": [
                        "read",
                        "view_index_metadata",
                        "index",
                        "delete",
                        "delete_index",
                        "manage",
                    ],
                },
                {
                    "names": ["policy_stats", "policy_stats_issue_290_other"],
                    "privileges": ["read", "index", "delete", "manage"],
                },
            ],
        },
    )
    require_status(status, {200}, f"verify {identity} privileges")
    require(isinstance(value, dict), f"{identity} privilege response differs")
    expected_selected = (
        {
            "read": True,
            "view_index_metadata": True,
            "index": False,
            "delete": False,
            "delete_index": False,
            "manage": False,
        }
        if identity == READER
        else {
            "read": False,
            "view_index_metadata": False,
            "index": True,
            "delete": False,
            "delete_index": False,
            "manage": False,
        }
    )
    require(
        value.get("cluster") == {"manage_security": False, "monitor": False}
        and value.get("index", {}).get(INDEX) == expected_selected,
        f"{identity} selected-index privileges differ",
    )
    for name in ("policy_stats", "policy_stats_issue_290_other"):
        require(
            value.get("index", {}).get(name)
            == {"read": False, "index": False, "delete": False, "manage": False},
            f"{identity} other-index privileges differ",
        )
    status, _ = client.request("GET", "/_security/role")
    require_status(status, {403}, f"verify {identity} security denial")
    status, _ = client.request("GET", f"/{INDEX}/_count")
    require_status(
        status, {200} if identity == READER else {403}, f"verify {identity} read boundary"
    )


def client_for(
    target: Target, username: str, password: str, port: int, certificate: bytes
) -> ElasticsearchClient:
    return ElasticsearchClient(
        port,
        username,
        password,
        server_name=target.server_name,
        ca_certificate=certificate,
    )


def apply_setup(
    target: Target,
    admin: ElasticsearchClient,
    definition: Definition,
    certificate: bytes,
) -> int:
    count = inspect_index(admin, definition)
    if count is None:
        status, _ = admin.request("PUT", f"/{INDEX}", {"mappings": definition.mapping})
        require_status(status, {200}, "create isolated index")
        count = inspect_index(admin, definition)
        require(count == 0, "new isolated index is not empty")
    passwords = {
        READER: target.optional_password(target.reader_secret, READER, "elasticsearch-reader")
        or new_password(),
        WRITER: target.optional_password(target.writer_secret, WRITER, "elasticsearch-writer")
        or new_password(),
    }
    put_identity(admin, READER, ["read", "view_index_metadata"], passwords[READER], "reader")
    put_identity(admin, WRITER, ["index"], passwords[WRITER], "writer")
    target.apply_secret(
        target.reader_secret,
        {"username": READER, "password": passwords[READER]},
        "elasticsearch-reader",
    )
    target.apply_secret(
        target.writer_secret,
        {"username": WRITER, "password": passwords[WRITER]},
        "elasticsearch-writer",
    )
    kafka_ca = target.secret_value(target.data_namespace, target.kafka_public_ca_secret, "ca.crt")
    require(b"BEGIN CERTIFICATE" in kafka_ca, "Kafka public CA differs")
    target.apply_secret(
        target.kafka_ca_secret,
        {"ca.crt": kafka_ca.decode("utf-8")},
        "kafka-trust",
    )
    target.apply_topic()
    require(inspect_index(admin, definition) == count, "setup changed isolated documents")
    return count


def verify_setup(
    target: Target,
    admin: ElasticsearchClient,
    definition: Definition,
    certificate: bytes,
) -> int:
    count = inspect_index(admin, definition)
    require(count is not None, "isolated index is absent")
    target.verify_topic()
    for identity, name, component, privileges in (
        (READER, target.reader_secret, "elasticsearch-reader", ["read", "view_index_metadata"]),
        (WRITER, target.writer_secret, "elasticsearch-writer", ["index"]),
    ):
        verify_identity(admin, identity, privileges)
        password = target.optional_password(name, identity, component)
        require(bool(password), f"Secret/{name} is absent")
        verify_privileges(client_for(target, identity, password, admin.port, certificate), identity)
    kafka = target.json("get", "secret", target.kafka_ca_secret)
    target.validate_secret(kafka, target.kafka_ca_secret, {"ca.crt"}, "kafka-trust")
    stored_ca = target.secret_value(target.namespace, target.kafka_ca_secret, "ca.crt")
    public_ca = target.secret_value(target.data_namespace, target.kafka_public_ca_secret, "ca.crt")
    require(stored_ca == public_ca, "Kafka workload CA differs from the selected broker")
    return count


def retirement_secrets(target: Target) -> tuple[tuple[str, str, set[str]], ...]:
    return (
        (target.reader_secret, "elasticsearch-reader", {"username", "password"}),
        (target.writer_secret, "elasticsearch-writer", {"username", "password"}),
        (target.kafka_ca_secret, "kafka-trust", {"ca.crt"}),
    )


def inspect_retirement(
    target: Target,
    admin: ElasticsearchClient,
    definition: Definition,
) -> dict[str, Any]:
    count = inspect_index(admin, definition)
    identities: dict[str, dict[str, bool]] = {}
    for identity in (WRITER, READER):
        stored_role, stored_user = inspect_identity(admin, identity)
        identities[identity] = {
            "role": stored_role is not None,
            "user": stored_user is not None,
        }
    topic = target.json(
        "get", "kafkatopic", TOPIC, "--ignore-not-found", namespace=target.data_namespace
    )
    if topic:
        target.verify_topic()
    secrets: dict[str, bool] = {}
    for name, component, keys in retirement_secrets(target):
        value = target.json("get", "secret", name, "--ignore-not-found")
        if value:
            target.validate_secret(value, name, keys, component)
        secrets[name] = bool(value)
    return {
        "documents": count,
        "identities": identities,
        "topicPresent": bool(topic),
        "secrets": secrets,
        "consumerGroup": target.consumer_group_state(),
    }


def verify_absent(target: Target, admin: ElasticsearchClient) -> None:
    for path, label in (
        (f"/{INDEX}", "index"),
        (f"/_security/role/{READER}", "reader role"),
        (f"/_security/user/{READER}", "reader user"),
        (f"/_security/role/{WRITER}", "writer role"),
        (f"/_security/user/{WRITER}", "writer user"),
    ):
        status, _ = admin.request("GET", path)
        require_status(status, {404}, f"verify absent retirement {label}")
    require(
        not target.json(
            "get", "kafkatopic", TOPIC, "--ignore-not-found", namespace=target.data_namespace
        ),
        f"KafkaTopic/{TOPIC} still exists",
    )
    for name, _component, _keys in retirement_secrets(target):
        require(
            not target.json("get", "secret", name, "--ignore-not-found"),
            f"Secret/{name} still exists",
        )
    require(not target.consumer_group_state()["present"], "retirement group still exists")


def retire(
    target: Target,
    admin: ElasticsearchClient,
    definition: Definition,
) -> dict[str, Any]:
    require(
        OWNER == "issue-290"
        and INDEX == "policy_stats_issue_290"
        and TOPIC == "policy-registered-issue-290"
        and GROUP == "dashboard-service-issue-290"
        and target.reader_secret.endswith("-issue-290")
        and target.writer_secret.endswith("-issue-290")
        and target.kafka_ca_secret.endswith("-issue-290"),
        "retirement boundary differs",
    )
    state = inspect_retirement(target, admin, definition)
    require(
        state["consumerGroup"]["activeMembers"] == 0,
        "retirement group still has active members",
    )
    target.delete_consumer_group()
    for identity in (WRITER, READER):
        for path, label in (
            (f"/_security/user/{identity}", "user"),
            (f"/_security/role/{identity}", "role"),
        ):
            status, _ = admin.request("DELETE", path)
            require_status(status, {200, 404}, f"delete {identity} {label}")
    target.run("delete", "kafkatopic", TOPIC, "--ignore-not-found", namespace=target.data_namespace)
    for name, _component, _keys in retirement_secrets(target):
        target.run("delete", "secret", name, "--ignore-not-found")
    if state["documents"] is not None:
        status, _ = admin.request("DELETE", f"/{INDEX}")
        require_status(status, {200}, "delete retirement index")
    verify_absent(target, admin)
    return state


def execute(operation: str) -> None:
    require(operation in {"inspect", "retire"}, "unsupported retirement operation")
    if operation == "retire":
        environment = os.environ.get("DEPLOY_ENV", "")
        expected = f"retire-{environment}-dashboard-{OWNER}"
        require(
            os.environ.get("DASHBOARD_RETIREMENT_CONFIRM") == expected,
            f"DASHBOARD_RETIREMENT_CONFIRM must equal {expected}",
        )
    definition = load_definition()
    target = Target()
    target.verify_boundary()
    certificate = (
        target.secret_value(target.data_namespace, target.public_ca_secret, "ca.crt")
        if target.public_ca_secret
        else b""
    )
    admin_password = target.secret_value(
        target.data_namespace, target.admin_secret, "elastic"
    ).decode("utf-8")
    with PortForward(target) as port:
        admin = client_for(target, "elastic", admin_password, port, certificate)
        state = (
            retire(target, admin, definition)
            if operation == "retire"
            else inspect_retirement(target, admin, definition)
        )
    print(
        json.dumps(
            {
                "consumerGroup": GROUP,
                "activeMembers": state["consumerGroup"]["activeMembers"],
                "documents": state["documents"],
                "environment": target.environment,
                "index": INDEX,
                "operation": operation,
                "status": "passed",
                "topic": TOPIC,
            },
            sort_keys=True,
        )
    )


def cli(argv: list[str] | None = None) -> int:
    arguments = sys.argv[1:] if argv is None else argv
    try:
        require(len(arguments) == 1, "one retirement operation is required")
        execute(arguments[0])
    except KeyboardInterrupt:
        return 130
    except (
        AdministrationError,
        CommandError,
        OSError,
        UnicodeDecodeError,
        ValueError,
    ) as error:
        print(f"dashboard-retirement: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(cli())
