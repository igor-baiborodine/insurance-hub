#!/usr/bin/env python3
"""Administer the issue-229 Elasticsearch fixture through exact enrolled targets."""

from __future__ import annotations

import base64
import http.client
import json
import os
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

SERVICE = "policy-search"
FIXTURE_PATH = ROOT / "services/policy-search-service/internal/administration/fixture.json"
MAX_RESPONSE_BYTES = 64 * 1024
EVENT_WRITER = "go_policy_search_writer"


class AdministrationError(RuntimeError):
    """The selected target or Elasticsearch state differs from the fixture contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise AdministrationError(message)


def safe_environment() -> dict[str, str]:
    allowed = ("HOME", "KUBECONFIG", "PATH", "SSL_CERT_DIR", "SSL_CERT_FILE", "TMPDIR")
    return {name: os.environ[name] for name in allowed if name in os.environ}


def command(arguments: list[str], *, input_text: str | None = None, timeout: int = 30) -> str:
    result = run_command(
        arguments,
        cwd=ROOT,
        env=safe_environment(),
        input_text=input_text,
        timeout_seconds=timeout,
        teardown_seconds=10,
        activity="Policy Search fixture administration",
    )
    return result.stdout


@dataclass(frozen=True)
class Fixture:
    index: str
    mapping: dict[str, Any]
    documents: tuple[dict[str, Any], ...]


def load_fixture(path: Path = FIXTURE_PATH) -> Fixture:
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise AdministrationError("fixture asset is unavailable or malformed") from error
    require(raw.get("schemaVersion") == 1, "fixture schema version differs")
    require(raw.get("index") == "policy-views-issue-229", "fixture index differs")
    mapping = raw.get("mapping")
    require(
        mapping
        == {
            "dynamic": "strict",
            "properties": {
                "number": {"type": "keyword"},
                "policyHolder": {"type": "text"},
                "dateFrom": {"type": "date", "format": "strict_date"},
                "dateTo": {"type": "date", "format": "strict_date"},
            },
        },
        "fixture mapping differs",
    )
    documents = raw.get("documents")
    series = raw.get("generatedSeries")
    require(isinstance(documents, list) and documents, "fixture documents are missing")
    require(
        isinstance(series, dict)
        and series.get("count") == 105
        and series.get("idPrefix") == "window",
        "fixture generated series differs",
    )
    expanded = [dict(item) for item in documents]
    for offset in range(series["count"]):
        expanded.append(
            {
                "id": f"{series['idPrefix']}-{offset:03d}",
                "number": f"{series['numberPrefix']}{offset:03d}",
                "policyHolder": series["policyHolder"],
                "dateFrom": series["dateFrom"],
                "dateTo": series["dateTo"],
            }
        )
    ids = [item.get("id") for item in expanded]
    require(
        len(ids) == len(set(ids)) and all(isinstance(value, str) and value for value in ids),
        "fixture document identities differ",
    )
    return Fixture(raw["index"], mapping, tuple(expanded))


class ForwardedHTTPSConnection(http.client.HTTPSConnection):
    def __init__(
        self,
        server_name: str,
        forwarded_port: int,
        context: ssl.SSLContext,
        timeout: float,
    ) -> None:
        super().__init__(server_name, 443, context=context, timeout=timeout)
        self.forwarded_port = forwarded_port

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
            self.context = ssl.create_default_context(cadata=ca_certificate.decode("utf-8"))
            self.context.minimum_version = ssl.TLSVersion.TLSv1_2

    def request(
        self,
        method: str,
        path: str,
        body: dict[str, Any] | bytes | None = None,
    ) -> tuple[int, Any]:
        payload: bytes | None
        content_type = "application/json"
        if isinstance(body, dict):
            payload = json.dumps(body, separators=(",", ":")).encode("utf-8")
        else:
            payload = body
            if isinstance(body, bytes):
                content_type = "application/x-ndjson"
        headers: dict[str, str] = {}
        if self.username or self.password:
            credential = base64.b64encode(f"{self.username}:{self.password}".encode()).decode(
                "ascii"
            )
            headers["Authorization"] = f"Basic {credential}"
        if payload is not None:
            headers["Content-Type"] = content_type
        connection: http.client.HTTPConnection
        if self.context is None:
            connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=10)
        else:
            connection = ForwardedHTTPSConnection(
                self.server_name, self.port, self.context, timeout=10
            )
        try:
            connection.request(method, path, body=payload, headers=headers)
            response = connection.getresponse()
            data = response.read(MAX_RESPONSE_BYTES + 1)
            require(len(data) <= MAX_RESPONSE_BYTES, "Elasticsearch response exceeded limit")
            if not data:
                decoded: Any = None
            else:
                try:
                    decoded = json.loads(data)
                except json.JSONDecodeError:
                    decoded = None
            return response.status, decoded
        except (OSError, http.client.HTTPException, ssl.SSLError) as error:
            raise AdministrationError("Elasticsearch request failed") from error
        finally:
            connection.close()


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
        deadline = time.monotonic() + 5
        stable_since = time.monotonic()
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise AdministrationError("Elasticsearch port-forward stopped early")
            if time.monotonic() - stable_since >= 1:
                return self.port
            time.sleep(0.1)
        raise AdministrationError("Elasticsearch port-forward did not become ready")

    def __exit__(self, *_: object) -> None:
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


class Target:
    def __init__(self) -> None:
        record = service_record(ROOT, SERVICE)
        administration = record.get("administration", {})
        require(
            administration.get("index") == "policy-views-issue-229"
            and administration.get("role") == "go_policy_search_reader"
            and administration.get("user") == "go_policy_search_reader"
            and administration.get("assets", {}).get("fixture")
            == "services/policy-search-service/internal/administration/fixture.json",
            "fixture administration catalog contract differs",
        )
        events = administration.get("events", {})
        require(
            events
            == {
                "owner": "issue-230",
                "index": "policy-views-issue-229",
                "writerRole": EVENT_WRITER,
                "writerUser": EVENT_WRITER,
                "registeredTopic": "policy-registered-issue-230",
                "terminatedTopic": "policy-terminated-issue-230",
                "consumerGroup": "policy-search-service-issue-230",
            },
            "event administration catalog contract differs",
        )
        self.environment = os.environ.get("DEPLOY_ENV", "")
        require(self.environment in record.get("environments", []), "DEPLOY_ENV is invalid")
        selected = target_record(ROOT, record, self.environment)
        expected = {
            "DEPLOY_CONTEXT": selected["context"],
            "DEPLOY_SVC_NS": selected["namespace"],
            "DEPLOY_DATA_NS": selected["dataNamespace"],
        }
        for name, value in expected.items():
            require(os.environ.get(name, "") == value, f"{name} differs from the catalog")
        fixture = selected.get("fixtureAdministration", {})
        require(fixture, "fixture administration is not configured")
        expected_event_target = {
            "local-dev": {
                "workloadSecret": "local-dev-policy-search-qualification-elasticsearch",
                "eventWriterSecret": "local-dev-policy-search-qualification-elasticsearch-writer",
                "kafkaCluster": "local-dev-kafka",
                "kafkaBootstrap": "local-dev-kafka-kafka-bootstrap:9093",
                "kafkaPublicCASecret": "local-dev-kafka-cluster-ca-cert",
                "kafkaWorkloadCASecret": "local-dev-policy-search-qualification-kafka-ca",
                "kafkaReplicas": 1,
            },
            "qa": {
                "workloadSecret": "qa-policy-search-qualification-elasticsearch",
                "workloadCASecret": "qa-policy-search-qualification-elasticsearch-ca",
                "eventWriterSecret": "qa-policy-search-qualification-elasticsearch-writer",
                "kafkaCluster": "qa-kafka",
                "kafkaBootstrap": "qa-kafka-kafka-bootstrap.qa-data.svc:9093",
                "kafkaPublicCASecret": "qa-kafka-cluster-ca-cert",
                "kafkaWorkloadCASecret": "qa-policy-search-qualification-kafka-ca",
                "kafkaReplicas": 3,
            },
        }[self.environment]
        require(
            all(fixture.get(key) == value for key, value in expected_event_target.items()),
            "event target catalog contract differs",
        )
        qualification_secrets = (
            fixture["workloadSecret"],
            fixture["eventWriterSecret"],
            fixture["kafkaWorkloadCASecret"],
            fixture.get("workloadCASecret", ""),
        )
        require(
            all(not name or "-qualification-" in name for name in qualification_secrets),
            "qualification Secret names differ",
        )
        require(
            all("cutover" not in name for name in qualification_secrets),
            "qualification Secret names must not contain cutover",
        )
        self.context = selected["context"]
        self.namespace = selected["namespace"]
        self.data_namespace = selected["dataNamespace"]
        self.elasticsearch_service = fixture["service"]
        self.admin_secret = fixture["adminSecret"]
        self.workload_secret = fixture["workloadSecret"]
        self.public_ca_secret = fixture.get("publicCASecret", "")
        self.workload_ca_secret = fixture.get("workloadCASecret", "")
        self.server_name = fixture.get("serverName", "")
        self.event_writer_secret = fixture["eventWriterSecret"]
        self.kafka_cluster = fixture["kafkaCluster"]
        self.kafka_bootstrap = fixture["kafkaBootstrap"]
        self.kafka_public_ca_secret = fixture["kafkaPublicCASecret"]
        self.kafka_workload_ca_secret = fixture["kafkaWorkloadCASecret"]
        self.kafka_replicas = fixture["kafkaReplicas"]
        self.event_topics = (
            events["registeredTopic"],
            events["terminatedTopic"],
        )
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
        output = self.run(*arguments, "-o", "json", namespace=namespace)
        try:
            value = json.loads(output or "{}")
        except json.JSONDecodeError as error:
            raise AdministrationError("kubectl returned malformed JSON") from error
        require(isinstance(value, dict), "kubectl returned unexpected JSON")
        return value

    def verify_boundary(self) -> None:
        current = command([self.kubectl, "config", "current-context"]).strip()
        require(current == self.context, "current Kubernetes context differs")
        service = self.json(
            "get", "service", self.elasticsearch_service, namespace=self.data_namespace
        )
        require(
            service.get("metadata", {}).get("namespace") == self.data_namespace,
            "Elasticsearch Service namespace differs",
        )
        selector = service.get("spec", {}).get("selector", {})
        require(
            selector.get("elasticsearch.k8s.elastic.co/cluster-name")
            == f"{self.environment}-elasticsearch",
            "Elasticsearch Service selector differs",
        )

    def verify_event_boundary(self) -> None:
        kafka = self.json("get", "kafka", self.kafka_cluster, namespace=self.data_namespace)
        require(
            kafka.get("metadata", {}).get("name") == self.kafka_cluster
            and kafka.get("metadata", {}).get("namespace") == self.data_namespace,
            "Kafka target identity differs",
        )
        listeners = kafka.get("spec", {}).get("kafka", {}).get("listeners", [])
        require(
            any(
                item.get("name") == "tls" and item.get("port") == 9093 and item.get("tls") is True
                for item in listeners
            ),
            "Kafka TLS listener differs",
        )
        service = self.json(
            "get",
            "service",
            f"{self.kafka_cluster}-kafka-bootstrap",
            namespace=self.data_namespace,
        )
        require(
            service.get("spec", {}).get("selector", {}).get("strimzi.io/cluster")
            == self.kafka_cluster
            and any(port.get("port") == 9093 for port in service.get("spec", {}).get("ports", [])),
            "Kafka bootstrap Service differs",
        )

    def secret_value(self, namespace: str, name: str, key: str) -> bytes:
        secret = self.json("get", "secret", name, namespace=namespace)
        encoded = secret.get("data", {}).get(key)
        require(isinstance(encoded, str) and encoded, f"Secret/{name} is missing {key}")
        try:
            return base64.b64decode(encoded, validate=True)
        except ValueError as error:
            raise AdministrationError(f"Secret/{name} has malformed {key}") from error

    def optional_workload_password(self, name: str) -> str:
        value = self.json("get", "secret", name, "--ignore-not-found")
        encoded = value.get("data", {}).get("password")
        if not encoded:
            return ""
        try:
            password = base64.b64decode(encoded, validate=True).decode("utf-8")
        except (ValueError, UnicodeDecodeError) as error:
            raise AdministrationError("workload Secret password is malformed") from error
        require(len(password) >= 32, "workload Secret password is too short")
        return password

    def apply_secret(self, name: str, data: dict[str, str], component: str) -> None:
        value = {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": {
                "name": name,
                "namespace": self.namespace,
                "labels": {
                    "app.kubernetes.io/name": "policy-search-api",
                    "app.kubernetes.io/component": component,
                },
            },
            "type": "Opaque",
            "stringData": data,
        }
        self.run(
            "apply",
            "--server-side",
            "--field-manager=policy-search-fixture",
            "-f",
            "-",
            input_text=json.dumps(value),
        )

    def apply_event_topics(self) -> None:
        for name in self.event_topics:
            value = {
                "apiVersion": "kafka.strimzi.io/v1beta2",
                "kind": "KafkaTopic",
                "metadata": {
                    "name": name,
                    "namespace": self.data_namespace,
                    "labels": {
                        "strimzi.io/cluster": self.kafka_cluster,
                        "insurance-hub.io/owner": "issue-230",
                    },
                },
                "spec": {"partitions": 1, "replicas": self.kafka_replicas},
            }
            self.run(
                "apply",
                "--server-side",
                "--field-manager=policy-search-event-fixture",
                "-f",
                "-",
                namespace=self.data_namespace,
                input_text=json.dumps(value),
            )

    def verify_event_topics(self) -> None:
        for name in self.event_topics:
            value = self.json("get", "kafkatopic", name, namespace=self.data_namespace)
            metadata = value.get("metadata", {})
            spec = value.get("spec", {})
            require(
                metadata.get("labels", {}).get("strimzi.io/cluster") == self.kafka_cluster
                and metadata.get("labels", {}).get("insurance-hub.io/owner") == "issue-230"
                and spec.get("partitions") == 1
                and spec.get("replicas") == self.kafka_replicas,
                f"KafkaTopic/{name} differs from the event fixture contract",
            )


def require_status(status: int, accepted: set[int], action: str) -> None:
    require(status in accepted, f"{action} returned unexpected status {status}")


def fixture_bulk(fixture: Fixture) -> bytes:
    lines: list[str] = []
    for item in fixture.documents:
        document = dict(item)
        identifier = document.pop("id")
        lines.append(json.dumps({"index": {"_index": fixture.index, "_id": identifier}}))
        lines.append(json.dumps(document, separators=(",", ":")))
    return ("\n".join(lines) + "\n").encode("utf-8")


def setup_fixture(client: ElasticsearchClient, fixture: Fixture, password: str) -> None:
    role = {
        "cluster": [],
        "indices": [
            {
                "names": [fixture.index],
                "privileges": ["read", "view_index_metadata"],
                "allow_restricted_indices": False,
            }
        ],
        "applications": [],
        "run_as": [],
        "metadata": {"owner": "issue-229"},
    }
    status, _ = client.request("PUT", "/_security/role/go_policy_search_reader", role)
    require_status(status, {200}, "create reader role")
    user = {
        "password": password,
        "roles": ["go_policy_search_reader"],
        "full_name": "Policy Search issue-229 reader",
        "metadata": {"owner": "issue-229"},
        "enabled": True,
    }
    status, _ = client.request("PUT", "/_security/user/go_policy_search_reader", user)
    require_status(status, {200}, "create reader user")
    status, _ = client.request("HEAD", f"/{fixture.index}")
    if status == 404:
        status, _ = client.request("PUT", f"/{fixture.index}", {"mappings": fixture.mapping})
        require_status(status, {200}, "create fixture index")
    else:
        require_status(status, {200}, "inspect fixture index")
    status, value = client.request("GET", f"/{fixture.index}/_mapping")
    require_status(status, {200}, "inspect fixture mapping")
    require(
        isinstance(value, dict) and value.get(fixture.index, {}).get("mappings") == fixture.mapping,
        "fixture mapping differs from the accepted mapping",
    )
    status, _ = client.request(
        "POST", f"/{fixture.index}/_delete_by_query?refresh=true", {"query": {"match_all": {}}}
    )
    require_status(status, {200}, "reset fixture documents")
    status, value = client.request("POST", "/_bulk?refresh=true", fixture_bulk(fixture))
    require_status(status, {200}, "index fixture documents")
    require(
        isinstance(value, dict) and value.get("errors") is False, "fixture bulk indexing failed"
    )
    status, value = client.request("GET", f"/{fixture.index}/_count")
    require_status(status, {200}, "count fixture documents")
    require(
        isinstance(value, dict) and value.get("count") == len(fixture.documents),
        "fixture document count differs",
    )


def verify_fixture(client: ElasticsearchClient, fixture: Fixture) -> None:
    privileges = {
        "cluster": ["manage_security", "monitor"],
        "index": [
            {
                "names": [fixture.index],
                "privileges": [
                    "read",
                    "view_index_metadata",
                    "write",
                    "create_index",
                    "delete_index",
                    "manage",
                ],
            },
            {
                "names": ["policy-views", "policy-views-issue-229-other"],
                "privileges": ["read", "view_index_metadata", "write", "manage"],
            },
        ],
    }
    status, value = client.request("POST", "/_security/user/_has_privileges", privileges)
    require_status(status, {200}, "verify reader privileges")
    require(isinstance(value, dict), "reader privilege response is malformed")
    require(
        value.get("cluster") == {"manage_security": False, "monitor": False},
        "reader cluster privileges differ",
    )
    indices = value.get("index", {})
    require(
        indices.get(fixture.index)
        == {
            "read": True,
            "view_index_metadata": True,
            "write": False,
            "create_index": False,
            "delete_index": False,
            "manage": False,
        },
        "reader fixture-index privileges differ",
    )
    for name in ("policy-views", "policy-views-issue-229-other"):
        require(
            indices.get(name)
            == {"read": False, "view_index_metadata": False, "write": False, "manage": False},
            "reader other-index privileges differ",
        )
    status, _ = client.request("GET", "/_security/role")
    require_status(status, {403}, "verify security API denial")
    status, count = client.request("GET", f"/{fixture.index}/_count")
    require_status(status, {200}, "count fixture documents")
    require(
        isinstance(count, dict) and count.get("count") == len(fixture.documents),
        "fixture document count differs",
    )
    status, result = client.request(
        "POST",
        f"/{fixture.index}/_search",
        {
            "size": 1,
            "query": {
                "query_string": {"query": "Synthetic Person", "fields": ["number", "policyHolder"]}
            },
        },
    )
    require_status(status, {200}, "query fixture")
    require(
        isinstance(result, dict) and result.get("hits", {}).get("hits"),
        "fixture query returned no documents",
    )


def setup_event_access(client: ElasticsearchClient, fixture: Fixture, password: str) -> None:
    role = {
        "cluster": [],
        "indices": [
            {
                "names": [fixture.index],
                "privileges": ["index"],
                "allow_restricted_indices": False,
            }
        ],
        "applications": [],
        "run_as": [],
        "metadata": {"owner": "issue-230"},
    }
    status, _ = client.request("PUT", f"/_security/role/{EVENT_WRITER}", role)
    require_status(status, {200}, "create event writer role")
    user = {
        "password": password,
        "roles": [EVENT_WRITER],
        "full_name": "Policy Search issue-230 event writer",
        "metadata": {"owner": "issue-230"},
        "enabled": True,
    }
    status, _ = client.request("PUT", f"/_security/user/{EVENT_WRITER}", user)
    require_status(status, {200}, "create event writer user")


def verify_event_access(client: ElasticsearchClient, fixture: Fixture) -> None:
    privileges = {
        "cluster": ["manage_security", "monitor"],
        "index": [
            {
                "names": [fixture.index],
                "privileges": [
                    "index",
                    "read",
                    "view_index_metadata",
                    "create_index",
                    "delete_index",
                    "manage",
                ],
            },
            {
                "names": ["policy-views", "policy-views-issue-229-other"],
                "privileges": ["index", "read", "view_index_metadata", "manage"],
            },
        ],
    }
    status, value = client.request("POST", "/_security/user/_has_privileges", privileges)
    require_status(status, {200}, "verify event writer privileges")
    require(isinstance(value, dict), "event writer privilege response is malformed")
    require(
        value.get("cluster") == {"manage_security": False, "monitor": False},
        "event writer cluster privileges differ",
    )
    indices = value.get("index", {})
    require(
        indices.get(fixture.index)
        == {
            "index": True,
            "read": False,
            "view_index_metadata": False,
            "create_index": False,
            "delete_index": False,
            "manage": False,
        },
        "event writer fixture-index privileges differ",
    )
    for name in ("policy-views", "policy-views-issue-229-other"):
        require(
            indices.get(name)
            == {"index": False, "read": False, "view_index_metadata": False, "manage": False},
            "event writer other-index privileges differ",
        )
    status, _ = client.request("GET", "/_security/role")
    require_status(status, {403}, "verify event writer security API denial")
    status, _ = client.request("GET", f"/{fixture.index}/_count")
    require_status(status, {403}, "verify event writer read denial")


def retire_event_access(client: ElasticsearchClient) -> None:
    for method, path, action in (
        ("DELETE", f"/_security/user/{EVENT_WRITER}", "delete event writer user"),
        ("DELETE", f"/_security/role/{EVENT_WRITER}", "delete event writer role"),
    ):
        status, _ = client.request(method, path)
        require_status(status, {200, 404}, action)


def retire_fixture(client: ElasticsearchClient, fixture: Fixture) -> None:
    for method, path, action in (
        ("DELETE", f"/{fixture.index}", "delete fixture index"),
        ("DELETE", "/_security/user/go_policy_search_reader", "delete reader user"),
        ("DELETE", "/_security/role/go_policy_search_reader", "delete reader role"),
    ):
        status, _ = client.request(method, path)
        require_status(status, {200, 404}, action)


def client_for(target: Target, username: str, password: str, port: int) -> ElasticsearchClient:
    certificate = b""
    if target.public_ca_secret:
        certificate = target.secret_value(target.data_namespace, target.public_ca_secret, "ca.crt")
    return ElasticsearchClient(
        port,
        username,
        password,
        server_name=target.server_name,
        ca_certificate=certificate,
    )


def execute(operation: str) -> None:
    fixture = load_fixture()
    base_operation = operation.removeprefix("event-")
    if base_operation in {"setup", "retire"}:
        expected = f"{operation}-{fixture.index}"
        require(
            os.environ.get("FIXTURE_CONFIRM") == expected, f"FIXTURE_CONFIRM must equal {expected}"
        )
    target = Target()
    target.verify_boundary()
    if operation.startswith("event-"):
        target.verify_event_boundary()
    if operation == "event-verify":
        target.verify_event_topics()
        username = target.secret_value(
            target.namespace, target.event_writer_secret, "username"
        ).decode("utf-8")
        password = target.secret_value(
            target.namespace, target.event_writer_secret, "password"
        ).decode("utf-8")
        require(username == EVENT_WRITER, "event writer Secret username differs")
        ca = target.secret_value(target.namespace, target.kafka_workload_ca_secret, "ca.crt")
        require(b"BEGIN CERTIFICATE" in ca, "Kafka workload CA differs")
        with PortForward(target) as port:
            verify_event_access(client_for(target, username, password, port), fixture)
        print(f"policy-search-event-fixture: environment={target.environment} verify=passed")
        return
    if operation == "verify":
        username = target.secret_value(target.namespace, target.workload_secret, "username").decode(
            "utf-8"
        )
        password = target.secret_value(target.namespace, target.workload_secret, "password").decode(
            "utf-8"
        )
        require(username == "go_policy_search_reader", "workload Secret username differs")
        with PortForward(target) as port:
            verify_fixture(client_for(target, username, password, port), fixture)
        print(f"policy-search-fixture: environment={target.environment} verify=passed")
        return
    admin_password = target.secret_value(
        target.data_namespace, target.admin_secret, "elastic"
    ).decode("utf-8")
    with PortForward(target) as port:
        admin = client_for(target, "elastic", admin_password, port)
        if operation == "setup":
            password = target.optional_workload_password(
                target.workload_secret
            ) or secrets.token_urlsafe(32)
            setup_fixture(admin, fixture, password)
            target.apply_secret(
                target.workload_secret,
                {"username": "go_policy_search_reader", "password": password},
                "elasticsearch-access",
            )
            if target.public_ca_secret:
                ca = target.secret_value(target.data_namespace, target.public_ca_secret, "ca.crt")
                target.apply_secret(
                    target.workload_ca_secret,
                    {"ca.crt": ca.decode("utf-8")},
                    "elasticsearch-trust",
                )
            print(f"policy-search-fixture: environment={target.environment} setup=passed")
            return
        if operation == "retire":
            retire_fixture(admin, fixture)
        if operation == "event-setup":
            password = target.optional_workload_password(
                target.event_writer_secret
            ) or secrets.token_urlsafe(32)
            setup_event_access(admin, fixture, password)
            target.apply_secret(
                target.event_writer_secret,
                {"username": EVENT_WRITER, "password": password},
                "elasticsearch-event-writer",
            )
            ca = target.secret_value(
                target.data_namespace,
                target.kafka_public_ca_secret,
                "ca.crt",
            )
            target.apply_secret(
                target.kafka_workload_ca_secret,
                {"ca.crt": ca.decode("utf-8")},
                "kafka-trust",
            )
            target.apply_event_topics()
            print(f"policy-search-event-fixture: environment={target.environment} setup=passed")
            return
        if operation == "event-retire":
            retire_event_access(admin)
            for name in target.event_topics:
                target.run(
                    "delete",
                    "kafkatopic",
                    name,
                    "--ignore-not-found",
                    namespace=target.data_namespace,
                )
            target.run("delete", "secret", target.event_writer_secret, "--ignore-not-found")
            target.run("delete", "secret", target.kafka_workload_ca_secret, "--ignore-not-found")
            print(f"policy-search-event-fixture: environment={target.environment} retire=passed")
            return
    target.run("delete", "secret", target.workload_secret, "--ignore-not-found")
    if target.workload_ca_secret:
        target.run("delete", "secret", target.workload_ca_secret, "--ignore-not-found")
    print(f"policy-search-fixture: environment={target.environment} retire=passed")


def main(argv: list[str] | None = None) -> int:
    arguments = sys.argv[1:] if argv is None else argv
    require(
        len(arguments) == 1
        and arguments[0]
        in {"setup", "verify", "retire", "event-setup", "event-verify", "event-retire"},
        "unsupported fixture operation",
    )
    execute(arguments[0])
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (AdministrationError, CommandError, OSError, ValueError) as error:
        print(f"policy-search-fixture: {error}", file=sys.stderr)
        raise SystemExit(1) from error
