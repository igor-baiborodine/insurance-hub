#!/usr/bin/env python3
"""Guard the issue-235 canonical Elasticsearch index and workload identities."""

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

from fixture_administration import (  # noqa: E402
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


class Target:
    """Resolve exact catalog coordinates before cluster access."""

    def __init__(self) -> None:
        record = service_record(ROOT, SERVICE)
        cutover = record.get("administration", {}).get("cutover", {})
        require(
            cutover
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
            "cutover administration catalog contract differs",
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
        administration = selected.get("fixtureAdministration", {})
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

    def optional_password(self, name: str, expected_identity: str) -> str:
        secret = self.json("get", "secret", name, "--ignore-not-found")
        data = secret.get("data", {})
        encoded_username = data.get("username")
        encoded = secret.get("data", {}).get("password")
        if not encoded:
            return ""
        try:
            username = base64.b64decode(encoded_username, validate=True).decode("utf-8")
            password = base64.b64decode(encoded, validate=True).decode("utf-8")
        except (TypeError, ValueError, UnicodeDecodeError) as error:
            raise AdministrationError("canonical Secret credentials are malformed") from error
        require(username == expected_identity, "canonical Secret username differs")
        require(len(password) >= 32, "canonical Secret password is too short")
        return password

    def apply_secret(
        self,
        name: str,
        data: dict[str, str],
        component: str,
        *,
        force_conflicts: bool = False,
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
        if force_conflicts:
            arguments.append("--force-conflicts")
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


def ensure_index_and_access(
    client: ElasticsearchClient,
    reader_password: str,
    writer_password: str,
) -> IndexState:
    state = inspect_index(client)
    require(
        not state.exists or state.documents == 0,
        "canonical index contains documents; record an accepted complete manifest before continuing",
    )
    if not state.exists:
        status, _ = client.request("PUT", f"/{INDEX}", {"mappings": MAPPING})
        require_status(status, {200}, "create canonical index")
        state = inspect_index(client)
    for identity, privileges, password, description in (
        (READER, ["read", "view_index_metadata"], reader_password, "reader"),
        (WRITER, ["index"], writer_password, "writer"),
    ):
        role = {
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
            "metadata": {"owner": "issue-235"},
        }
        status, _ = client.request("PUT", f"/_security/role/{identity}", role)
        require_status(status, {200}, f"create cutover {description} role")
        user = {
            "password": password,
            "roles": [identity],
            "full_name": f"Policy Search issue-235 {description}",
            "metadata": {"owner": "issue-235"},
            "enabled": True,
        }
        status, _ = client.request("PUT", f"/_security/user/{identity}", user)
        require_status(status, {200}, f"create cutover {description} user")
    return state


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
                "names": ["policy-views-issue-229", "policy-views-other"],
                "privileges": ["read", "view_index_metadata", "index", "manage"],
            },
        ],
    }
    status, value = client.request("POST", "/_security/user/_has_privileges", request)
    require_status(status, {200}, f"verify cutover {privilege} privileges")
    require(isinstance(value, dict), "cutover privilege response is malformed")
    require(
        value.get("cluster") == {"manage_security": False, "monitor": False},
        "cutover cluster privileges differ",
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
    require(indices.get(INDEX) == expected, "cutover exact-index privileges differ")
    for other in ("policy-views-issue-229", "policy-views-other"):
        require(
            indices.get(other)
            == {"read": False, "view_index_metadata": False, "index": False, "manage": False},
            "cutover other-index privileges differ",
        )


def workload_client(target: Target, name: str, identity: str, port: int) -> ElasticsearchClient:
    username = target.secret_value(target.namespace, name, "username").decode("utf-8")
    password = target.secret_value(target.namespace, name, "password").decode("utf-8")
    require(username == identity, "cutover workload Secret username differs")
    return client_for(target, username, password, port)


def migrate_secret(
    target: Target,
    source_variable: str,
    target_name: str,
    keys: tuple[str, ...],
    component: str,
    identity: str = "",
) -> tuple[str, str]:
    source_name = os.environ.get(source_variable, "")
    require(source_name, f"{source_variable} is required")
    require(
        source_name.startswith(f"{target.environment}-policy-search-")
        and "-cutover-" in source_name,
        f"{source_variable} is outside the obsolete Policy Search Secret boundary",
    )
    require(source_name != target_name, f"{source_variable} must differ from its target")
    source = target.json("get", "secret", source_name)
    encoded = source.get("data", {})
    require(
        isinstance(encoded, dict) and set(encoded) == set(keys),
        f"Secret/{source_name} keys differ",
    )
    decoded: dict[str, str] = {}
    try:
        for key in keys:
            decoded[key] = base64.b64decode(encoded[key], validate=True).decode("utf-8")
    except (TypeError, ValueError, UnicodeDecodeError) as error:
        raise AdministrationError(f"Secret/{source_name} data is malformed") from error
    if identity:
        require(decoded["username"] == identity, f"Secret/{source_name} username differs")
        require(len(decoded["password"]) >= 32, f"Secret/{source_name} password is too short")
    target.apply_secret(
        target_name,
        decoded,
        component,
        force_conflicts=True,
    )
    migrated = target.json("get", "secret", target_name)
    require(migrated.get("data") == encoded, f"Secret/{target_name} data differs after migration")
    return source_name, target_name


def migrate_secrets(target: Target) -> list[tuple[str, str]]:
    specifications = [
        (
            "POLICY_SEARCH_READER_SOURCE_SECRET",
            target.reader_secret,
            ("username", "password"),
            "canonical-elasticsearch-reader",
            READER,
        ),
        (
            "POLICY_SEARCH_WRITER_SOURCE_SECRET",
            target.writer_secret,
            ("username", "password"),
            "canonical-elasticsearch-writer",
            WRITER,
        ),
        (
            "POLICY_SEARCH_KAFKA_CA_SOURCE_SECRET",
            target.kafka_ca_secret,
            ("ca.crt",),
            "canonical-kafka-trust",
            "",
        ),
    ]
    elasticsearch_ca_source = os.environ.get("POLICY_SEARCH_ELASTICSEARCH_CA_SOURCE_SECRET", "")
    if target.elasticsearch_ca_secret:
        require(
            elasticsearch_ca_source,
            "POLICY_SEARCH_ELASTICSEARCH_CA_SOURCE_SECRET is required",
        )
        specifications.append(
            (
                "POLICY_SEARCH_ELASTICSEARCH_CA_SOURCE_SECRET",
                target.elasticsearch_ca_secret,
                ("ca.crt",),
                "canonical-elasticsearch-trust",
                "",
            )
        )
    else:
        require(
            not elasticsearch_ca_source,
            "POLICY_SEARCH_ELASTICSEARCH_CA_SOURCE_SECRET must be empty for this target",
        )
    sources = [os.environ.get(specification[0], "") for specification in specifications]
    destinations = [specification[1] for specification in specifications]
    for specification, source in zip(specifications, sources, strict=True):
        require(source, f"{specification[0]} is required")
    require(
        len(set(sources)) == len(sources),
        "canonical Secret migration source names collide",
    )
    require(
        len(set(destinations)) == len(destinations),
        "canonical Secret migration target names collide",
    )
    return [migrate_secret(target, *specification) for specification in specifications]


def execute(operation: str) -> None:
    if operation == "ensure":
        require(
            os.environ.get("CUTOVER_CONFIRM") == "ensure-policy-views",
            "CUTOVER_CONFIRM must equal ensure-policy-views",
        )
    if operation == "migrate-secrets":
        environment = os.environ.get("DEPLOY_ENV", "")
        require(environment in {"local-dev", "qa"}, "DEPLOY_ENV is invalid")
        expected = f"migrate-{environment}-policy-search-secrets"
        require(
            os.environ.get("SECRET_MIGRATION_CONFIRM") == expected,
            f"SECRET_MIGRATION_CONFIRM must equal {expected}",
        )
    target = Target()
    target.verify_boundary()
    if operation == "migrate-secrets":
        migrations = migrate_secrets(target)
        print(
            json.dumps(
                {
                    "environment": target.environment,
                    "migrations": [
                        {"source": source, "target": destination}
                        for source, destination in migrations
                    ],
                    "migrate-secrets": "passed",
                },
                sort_keys=True,
            )
        )
        return
    if operation == "verify":
        with PortForward(target) as port:
            reader = workload_client(target, target.reader_secret, READER, port)
            verify_identity(reader, READER)
            state = inspect_index(reader)
            writer = workload_client(target, target.writer_secret, WRITER, port)
            verify_identity(writer, WRITER)
            status, _ = writer.request("GET", f"/{INDEX}/_count")
            require_status(status, {403}, "verify cutover writer read denial")
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
            reader_password = target.optional_password(
                target.reader_secret, READER
            ) or secrets.token_urlsafe(32)
            writer_password = target.optional_password(
                target.writer_secret, WRITER
            ) or secrets.token_urlsafe(32)
            state = ensure_index_and_access(admin, reader_password, writer_password)
            target.apply_secret(
                target.reader_secret,
                {"username": READER, "password": reader_password},
                "cutover-elasticsearch-reader",
            )
            target.apply_secret(
                target.writer_secret,
                {"username": WRITER, "password": writer_password},
                "cutover-elasticsearch-writer",
            )
            if target.public_ca_secret:
                ca = target.secret_value(target.data_namespace, target.public_ca_secret, "ca.crt")
                target.apply_secret(
                    target.elasticsearch_ca_secret,
                    {"ca.crt": ca.decode("utf-8")},
                    "cutover-elasticsearch-trust",
                )
            kafka_ca = target.secret_value(
                target.data_namespace,
                target.kafka_public_ca_secret,
                "ca.crt",
            )
            target.apply_secret(
                target.kafka_ca_secret,
                {"ca.crt": kafka_ca.decode("utf-8")},
                "cutover-kafka-trust",
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
        len(arguments) == 1 and arguments[0] in {"inspect", "ensure", "verify", "migrate-secrets"},
        "unsupported cutover operation",
    )
    execute(arguments[0])
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (AdministrationError, CommandError, OSError, ValueError) as error:
        print(f"policy-search-cutover: {error}", file=sys.stderr)
        raise SystemExit(1) from error
