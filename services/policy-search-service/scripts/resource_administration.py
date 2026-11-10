#!/usr/bin/env python3
"""Manage the ordinary Policy Search resource boundary."""

from __future__ import annotations

import base64
import hashlib
import json
import os
import secrets
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

from administration import (  # noqa: E402, I001
    AdministrationError,
    ElasticsearchClient,
    PortForward,
    client_for,
    command,
    require,
    require_status,
)
from scripts.go.service.configuration import service_record, target_record  # noqa: E402
from scripts.python.process_runner import CommandError  # noqa: E402

SERVICE = "policy-search"
INDEX = "policy-views"
READER = "go_policy_search_cutover_reader"
WRITER = "go_policy_search_cutover_writer"
MAPPING = {
    "dynamic": "strict",
    "properties": {
        "number": {"type": "keyword"},
        "policyHolder": {"type": "text"},
        "dateFrom": {"type": "date", "format": "strict_date"},
        "dateTo": {"type": "date", "format": "strict_date"},
    },
}


@dataclass(frozen=True)
class IndexState:
    exists: bool
    documents: int
    mapping_sha256: str


@dataclass(frozen=True)
class IdentityState:
    identity: str
    description: str
    privileges: tuple[str, ...]
    role_exists: bool
    user_exists: bool
    password: str | None


class Target:
    """Resolve exact catalog coordinates before cluster access."""

    def __init__(self) -> None:
        record = service_record(ROOT, SERVICE)
        setup = record.get("administration", {}).get("setup", {})
        require(
            setup
            == {
                "owner": "issue-235",
                "index": INDEX,
                "readerRole": READER,
                "readerUser": READER,
                "writerRole": WRITER,
                "writerUser": WRITER,
                "registeredTopic": "policy-registered",
                "terminatedTopic": "policy-terminated",
                "consumerGroup": "policy-search-service",
            },
            "administration catalog contract differs",
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
        administration = selected.get("serviceAdministration", {})
        for name in (
            "service",
            "adminSecret",
            "canonicalReaderSecret",
            "canonicalWriterSecret",
            "canonicalElasticsearchCASecret",
            "canonicalKafkaCASecret",
            "kafkaPublicCASecret",
        ):
            require(name in administration, f"canonical target is missing {name}")
        expected_secret_target = {
            "local-dev": {
                "canonicalReaderSecret": "local-dev-policy-search-elasticsearch",
                "canonicalWriterSecret": "local-dev-policy-search-elasticsearch-writer",
                "canonicalElasticsearchCASecret": "",
                "canonicalKafkaCASecret": "local-dev-policy-search-kafka-ca",
            },
            "qa": {
                "canonicalReaderSecret": "qa-policy-search-elasticsearch",
                "canonicalWriterSecret": "qa-policy-search-elasticsearch-writer",
                "canonicalElasticsearchCASecret": "qa-policy-search-elasticsearch-ca",
                "canonicalKafkaCASecret": "qa-policy-search-kafka-ca",
            },
        }[self.environment]
        require(
            all(administration.get(key) == value for key, value in expected_secret_target.items()),
            "canonical Secret catalog contract differs",
        )
        require(
            all("cutover" not in value for value in expected_secret_target.values()),
            "canonical Secret names must not contain cutover",
        )
        self.context = selected["context"]
        self.namespace = selected["namespace"]
        self.data_namespace = selected["dataNamespace"]
        self.elasticsearch_service = administration["service"]
        self.admin_secret = administration["adminSecret"]
        self.public_ca_secret = administration.get("publicCASecret", "")
        self.server_name = administration.get("serverName", "")
        self.reader_secret = administration["canonicalReaderSecret"]
        self.writer_secret = administration["canonicalWriterSecret"]
        self.elasticsearch_ca_secret = administration["canonicalElasticsearchCASecret"]
        self.kafka_public_ca_secret = administration["kafkaPublicCASecret"]
        self.kafka_ca_secret = administration["canonicalKafkaCASecret"]
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
        current = command([self.kubectl, "config", "current-context"]).strip()
        require(current == self.context, "current Kubernetes context differs")
        service = self.json(
            "get", "service", self.elasticsearch_service, namespace=self.data_namespace
        )
        require(
            service.get("metadata", {}).get("namespace") == self.data_namespace
            and service.get("spec", {})
            .get("selector", {})
            .get("elasticsearch.k8s.elastic.co/cluster-name")
            == f"{self.environment}-elasticsearch",
            "Elasticsearch Service identity differs",
        )

    def secret_value(self, namespace: str, name: str, key: str) -> bytes:
        secret = self.json("get", "secret", name, namespace=namespace)
        encoded = secret.get("data", {}).get(key)
        require(isinstance(encoded, str) and encoded, f"Secret/{name} is missing {key}")
        try:
            return base64.b64decode(encoded, validate=True)
        except ValueError as error:
            raise AdministrationError(f"Secret/{name} has malformed {key}") from error

    def optional_password(self, name: str, expected_identity: str) -> str | None:
        secret = self.json("get", "secret", name, "--ignore-not-found")
        data = secret.get("data", {})
        if not data:
            return None
        encoded_username = data.get("username")
        encoded = data.get("password")
        require(
            isinstance(encoded_username, str) and isinstance(encoded, str),
            "canonical Secret credentials are malformed",
        )
        try:
            username = base64.b64decode(encoded_username, validate=True).decode("utf-8")
            password = base64.b64decode(encoded, validate=True).decode("utf-8")
        except (TypeError, ValueError, UnicodeDecodeError) as error:
            raise AdministrationError("canonical Secret credentials are malformed") from error
        require(username == expected_identity, "canonical Secret username differs")
        require(len(password) >= 32, "canonical Secret password is too short")
        return password

    def optional_secret_value(self, name: str, key: str) -> bytes | None:
        secret = self.json("get", "secret", name, "--ignore-not-found")
        data = secret.get("data", {})
        if not data:
            return None
        require(set(data) == {key}, f"Secret/{name} keys differ")
        encoded = data.get(key)
        require(isinstance(encoded, str) and encoded, f"Secret/{name} is missing {key}")
        try:
            return base64.b64decode(encoded, validate=True)
        except ValueError as error:
            raise AdministrationError(f"Secret/{name} has malformed {key}") from error

    def apply_secret(
        self,
        name: str,
        data: dict[str, str],
        component: str,
    ) -> None:
        secret = {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": {
                "name": name,
                "namespace": self.namespace,
                "labels": {
                    "app.kubernetes.io/name": "policy-search-api",
                    "app.kubernetes.io/component": component,
                    "insurance-hub.io/owner": "issue-235",
                },
            },
            "type": "Opaque",
            "stringData": data,
        }
        arguments = [
            "apply",
            "--server-side",
            "--field-manager=policy-search-canonical",
        ]
        self.run(
            *arguments,
            "-f",
            "-",
            input_text=json.dumps(secret),
        )


def mapping_digest() -> str:
    payload = json.dumps(MAPPING, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(payload).hexdigest()


def inspect_index(client: ElasticsearchClient) -> IndexState:
    status, _ = client.request("HEAD", f"/{INDEX}")
    if status == 404:
        return IndexState(False, 0, mapping_digest())
    require_status(status, {200}, "inspect canonical index")
    status, value = client.request("GET", f"/{INDEX}")
    require_status(status, {200}, "inspect canonical index definition")
    definition = value.get(INDEX, {}) if isinstance(value, dict) else {}
    require(definition.get("mappings") == MAPPING, "canonical index mapping differs")
    require(definition.get("aliases", {}) == {}, "canonical index alias state differs")
    status, value = client.request("GET", f"/{INDEX}/_count")
    require_status(status, {200}, "count canonical documents")
    count = value.get("count") if isinstance(value, dict) else None
    require(isinstance(count, int) and count >= 0, "canonical document count is malformed")
    return IndexState(True, count, mapping_digest())


def inspect_index_and_access(
    client: ElasticsearchClient,
    reader_password: str | None,
    writer_password: str | None,
) -> tuple[IndexState, tuple[IdentityState, IdentityState]]:
    state = inspect_index(client)
    identities = (
        inspect_identity(
            client, READER, ("read", "view_index_metadata"), reader_password, "reader"
        ),
        inspect_identity(client, WRITER, ("index",), writer_password, "writer"),
    )
    return state, identities


def ensure_index_and_access(
    client: ElasticsearchClient,
    reader_password: str,
    writer_password: str,
) -> IndexState:
    state, identities = inspect_index_and_access(client, reader_password, writer_password)
    if not state.exists:
        status, _ = client.request("PUT", f"/{INDEX}", {"mappings": MAPPING})
        require_status(status, {200}, "create canonical index")
        state = inspect_index(client)
    for identity, password in zip(identities, (reader_password, writer_password), strict=True):
        if not identity.role_exists:
            status, _ = client.request(
                "PUT",
                f"/_security/role/{identity.identity}",
                role_definition(identity.privileges),
            )
            require_status(status, {200}, f"create {identity.description} role")
        if not identity.user_exists:
            status, _ = client.request(
                "PUT",
                f"/_security/user/{identity.identity}",
                user_definition(identity.identity, identity.description, password),
            )
            require_status(status, {200}, f"create {identity.description} user")
    return state


def persist_missing_credentials(
    target: Target,
    client: ElasticsearchClient,
    reader_password: str | None,
    writer_password: str | None,
) -> tuple[str, str]:
    inspect_index_and_access(client, reader_password, writer_password)
    passwords: list[str] = []
    for password, name, identity, component in (
        (reader_password, target.reader_secret, READER, "elasticsearch-reader"),
        (writer_password, target.writer_secret, WRITER, "elasticsearch-writer"),
    ):
        if password is None:
            password = secrets.token_urlsafe(32)
            target.apply_secret(
                name,
                {"username": identity, "password": password},
                component,
            )
        passwords.append(password)
    return passwords[0], passwords[1]


def role_definition(privileges: tuple[str, ...]) -> dict[str, Any]:
    return {
        "cluster": [],
        "indices": [
            {
                "names": [INDEX],
                "privileges": list(privileges),
                "allow_restricted_indices": False,
            }
        ],
        "applications": [],
        "run_as": [],
        "metadata": {"owner": "issue-235"},
    }


def user_definition(identity: str, description: str, password: str = "") -> dict[str, Any]:
    user: dict[str, Any] = {
        "roles": [identity],
        "full_name": f"Policy Search issue-235 {description}",
        "metadata": {"owner": "issue-235"},
        "enabled": True,
    }
    if password:
        user["password"] = password
    return user


def inspect_identity(
    client: ElasticsearchClient,
    identity: str,
    privileges: tuple[str, ...],
    password: str | None,
    description: str,
) -> IdentityState:
    status, value = client.request("GET", f"/_security/role/{identity}")
    require_status(status, {200, 404}, f"inspect {description} role")
    role_exists = status == 200
    if role_exists:
        actual = value.get(identity) if isinstance(value, dict) else None
        normalized = dict(actual) if isinstance(actual, dict) else {}
        transient = normalized.pop("transient_metadata", {})
        normalized.pop("remote_indices", None)
        normalized.pop("remote_cluster", None)
        require(
            normalized == role_definition(privileges) and transient in ({}, {"enabled": True}),
            f"existing {description} role differs",
        )

    status, value = client.request("GET", f"/_security/user/{identity}")
    require_status(status, {200, 404}, f"inspect {description} user")
    user_exists = status == 200
    if user_exists:
        actual = value.get(identity) if isinstance(value, dict) else None
        expected = user_definition(identity, description)
        require(
            isinstance(actual, dict)
            and all(actual.get(key) == expected_value for key, expected_value in expected.items()),
            f"existing {description} user differs",
        )
        require(password is not None, f"canonical {description} Secret is missing")
    return IdentityState(identity, description, privileges, role_exists, user_exists, password)


def verify_identity(client: ElasticsearchClient, identity: str) -> None:
    privilege = "read" if identity == READER else "index"
    request = {
        "cluster": ["manage_security", "monitor"],
        "index": [
            {
                "names": [INDEX],
                "privileges": [
                    "read",
                    "view_index_metadata",
                    "index",
                    "create_index",
                    "delete_index",
                    "manage",
                ],
            },
            {
                "names": ["policy-views-other"],
                "privileges": ["read", "view_index_metadata", "index", "manage"],
            },
        ],
    }
    status, value = client.request("POST", "/_security/user/_has_privileges", request)
    require_status(status, {200}, f"verify {privilege} privileges")
    require(isinstance(value, dict), "privilege response is malformed")
    require(
        value.get("cluster") == {"manage_security": False, "monitor": False},
        "cluster privileges differ",
    )
    indices = value.get("index", {})
    expected = {
        "read": identity == READER,
        "view_index_metadata": identity == READER,
        "index": identity == WRITER,
        "create_index": False,
        "delete_index": False,
        "manage": False,
    }
    require(indices.get(INDEX) == expected, "exact-index privileges differ")
    for other in ("policy-views-other",):
        require(
            indices.get(other)
            == {"read": False, "view_index_metadata": False, "index": False, "manage": False},
            "other-index privileges differ",
        )


def workload_client(target: Target, name: str, identity: str, port: int) -> ElasticsearchClient:
    username = target.secret_value(target.namespace, name, "username").decode("utf-8")
    password = target.secret_value(target.namespace, name, "password").decode("utf-8")
    require(username == identity, "workload Secret username differs")
    return client_for(target, username, password, port)


def execute(operation: str) -> None:
    if operation == "ensure":
        require(
            os.environ.get("POLICY_SEARCH_RESOURCE_CONFIRM") == "ensure-policy-search-resources",
            "POLICY_SEARCH_RESOURCE_CONFIRM must equal ensure-policy-search-resources",
        )
    target = Target()
    target.verify_boundary()
    if operation == "verify":
        with PortForward(target) as port:
            reader = workload_client(target, target.reader_secret, READER, port)
            verify_identity(reader, READER)
            state = inspect_index(reader)
            writer = workload_client(target, target.writer_secret, WRITER, port)
            verify_identity(writer, WRITER)
            status, _ = writer.request("GET", f"/{INDEX}/_count")
            require_status(status, {403}, "verify writer read denial")
        print(
            json.dumps(
                {
                    "environment": target.environment,
                    "index": INDEX,
                    "documents": state.documents,
                    "mapping_sha256": state.mapping_sha256,
                    "verify": "passed",
                },
                sort_keys=True,
            )
        )
        return
    admin_password = target.secret_value(
        target.data_namespace, target.admin_secret, "elastic"
    ).decode("utf-8")
    with PortForward(target) as port:
        admin = client_for(target, "elastic", admin_password, port)
        if operation == "inspect":
            state = inspect_index(admin)
        else:
            trust: list[tuple[str, bytes, str]] = []
            if target.public_ca_secret:
                ca = target.secret_value(target.data_namespace, target.public_ca_secret, "ca.crt")
                existing = target.optional_secret_value(target.elasticsearch_ca_secret, "ca.crt")
                require(existing in {None, ca}, "canonical Elasticsearch CA Secret differs")
                trust.append((target.elasticsearch_ca_secret, ca, "elasticsearch-trust"))
            kafka_ca = target.secret_value(
                target.data_namespace,
                target.kafka_public_ca_secret,
                "ca.crt",
            )
            existing = target.optional_secret_value(target.kafka_ca_secret, "ca.crt")
            require(existing in {None, kafka_ca}, "canonical Kafka CA Secret differs")
            trust.append((target.kafka_ca_secret, kafka_ca, "kafka-trust"))
            reader_password, writer_password = persist_missing_credentials(
                target,
                admin,
                target.optional_password(target.reader_secret, READER),
                target.optional_password(target.writer_secret, WRITER),
            )
            state = ensure_index_and_access(admin, reader_password, writer_password)
            for name, ca, component in trust:
                existing = target.optional_secret_value(name, "ca.crt")
                require(existing in {None, ca}, f"canonical Secret/{name} changed during ensure")
                if existing is None:
                    target.apply_secret(
                        name,
                        {"ca.crt": ca.decode("utf-8")},
                        component,
                    )
    print(
        json.dumps(
            {
                "environment": target.environment,
                "index": INDEX,
                "exists": state.exists,
                "documents": state.documents,
                "mapping_sha256": state.mapping_sha256,
                operation: "passed",
            },
            sort_keys=True,
        )
    )


def main(argv: list[str] | None = None) -> int:
    arguments = sys.argv[1:] if argv is None else argv
    require(
        len(arguments) == 1 and arguments[0] in {"inspect", "ensure", "verify"},
        "unsupported resource administration operation",
    )
    execute(arguments[0])
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (AdministrationError, CommandError, OSError, ValueError) as error:
        print(f"policy-search-resources: {error}", file=sys.stderr)
        raise SystemExit(1) from error
