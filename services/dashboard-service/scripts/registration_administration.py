#!/usr/bin/env python3
"""Inspect, ensure, and verify ordinary Dashboard registration resources."""

from __future__ import annotations

import base64
import json
import os
import re
import sys
import time
from pathlib import Path
from typing import Any

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

import administration as shared  # noqa: E402
from scripts.go.service.configuration import service_record, target_record  # noqa: E402
from scripts.python.process_runner import CommandError  # noqa: E402

OWNER = "issue-293"
INDEX = "policy_stats"
READER = "go_dashboard_total_sales_reader"
WRITER = "go_dashboard_policy_stats_writer"
TOPIC = "policy-registered"
GROUP = "dashboard-service"
ASSET = ROOT / "services/dashboard-service/internal/administration/registration.json"


class AdministrationError(RuntimeError):
    """The ordinary registration boundary differs from the accepted contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise AdministrationError(message)


def load_definition(path: Path = ASSET) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise AdministrationError("registration administration asset is unavailable") from error
    require(
        value
        == {
            "schemaVersion": 1,
            "owner": OWNER,
            "index": INDEX,
            "mapping": value.get("mapping"),
            "reader": {
                "role": READER,
                "user": READER,
                "privileges": ["read", "view_index_metadata"],
            },
            "writer": {
                "role": WRITER,
                "user": WRITER,
                "privileges": ["index"],
            },
            "topic": TOPIC,
            "consumerGroup": GROUP,
            "cleanup": False,
        },
        "registration administration definition differs",
    )
    mapping = value.get("mapping")
    require(
        isinstance(mapping, dict)
        and "dynamic" not in mapping
        and set(mapping.get("properties", {}))
        == {"number", "from", "to", "policyHolder", "productCode", "totalPremium", "agentLogin"},
        "registration mapping differs",
    )
    return value


class Target:
    """Resolve exact catalog coordinates before read or mutation effects."""

    def __init__(self) -> None:
        record = service_record(ROOT, "dashboard")
        require(
            record.get("registrationAdministration")
            == {
                "owner": OWNER,
                "asset": "services/dashboard-service/internal/administration/registration.json",
                "index": INDEX,
                "readerRole": READER,
                "readerUser": READER,
                "writerRole": WRITER,
                "writerUser": WRITER,
                "registeredTopic": TOPIC,
                "consumerGroup": GROUP,
            },
            "registration administration catalog contract differs",
        )
        self.environment = os.environ.get("DEPLOY_ENV", "")
        require(self.environment in record.get("environments", []), "DEPLOY_ENV is invalid")
        selected = target_record(ROOT, record, self.environment)
        for name, value in {
            "DEPLOY_CONTEXT": selected["context"],
            "DEPLOY_SVC_NS": selected["namespace"],
            "DEPLOY_DATA_NS": selected["dataNamespace"],
        }.items():
            require(os.environ.get(name, "") == value, f"{name} differs from the catalog")
        event = selected.get("registrationAdministration", {})
        require(
            event.get("readerSecret") == f"{self.environment}-dashboard-elasticsearch"
            and event.get("writerSecret") == f"{self.environment}-dashboard-elasticsearch-writer"
            and event.get("kafkaWorkloadCASecret") == f"{self.environment}-dashboard-kafka-ca",
            "registration Secret boundaries differ",
        )
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

    run = shared.Target.run
    json = shared.Target.json
    secret_value = shared.Target.secret_value
    verify_boundary = shared.Target.verify_boundary

    def validate_secret(
        self, value: dict[str, Any], name: str, keys: set[str], component: str, owner: str
    ) -> None:
        metadata = value.get("metadata", {})
        require(
            value.get("kind") == "Secret"
            and metadata.get("name") == name
            and metadata.get("namespace") == self.namespace
            and metadata.get("labels", {}).get("insurance-hub.io/owner") == owner
            and metadata.get("labels", {}).get("app.kubernetes.io/component") == component
            and value.get("type", "Opaque") == "Opaque"
            and set(value.get("data", {})) == keys,
            f"Secret/{name} identity or keys differ",
        )

    def optional_password(
        self,
        name: str,
        identity: str,
        component: str,
        owner: str,
    ) -> str:
        value = self.json("get", "secret", name, "--ignore-not-found")
        if not value:
            return ""
        self.validate_secret(
            value,
            name,
            {"username", "password"},
            component,
            owner,
        )
        try:
            username = base64.b64decode(value["data"]["username"], validate=True).decode()
            password = base64.b64decode(value["data"]["password"], validate=True).decode()
        except (TypeError, ValueError, UnicodeDecodeError) as error:
            raise AdministrationError(f"Secret/{name} data is malformed") from error
        require(username == identity, f"Secret/{name} username differs")
        shared.require_password(password)
        return password

    def optional_writer_password(self) -> str:
        return self.optional_password(
            self.writer_secret,
            WRITER,
            "elasticsearch-writer",
            OWNER,
        )

    def apply_secret(
        self,
        name: str,
        values: dict[str, str],
        component: str,
        *,
        owner: str = OWNER,
        force_conflicts: bool = False,
    ) -> None:
        manifest = {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": {
                "name": name,
                "namespace": self.namespace,
                "labels": {
                    "app.kubernetes.io/name": "dashboard-api",
                    "app.kubernetes.io/component": component,
                    "insurance-hub.io/owner": owner,
                },
            },
            "type": "Opaque",
            "data": {
                key: base64.b64encode(value.encode()).decode("ascii")
                for key, value in values.items()
            },
        }
        arguments = [
            "apply",
            "--server-side",
            "--field-manager=dashboard-registration-administration",
        ]
        if force_conflicts:
            arguments.append("--force-conflicts")
        self.run(*arguments, "-f", "-", input_text=json.dumps(manifest))

    def ensure_kafka_trust(self, certificate: bytes) -> str:
        existing = self.json("get", "secret", self.kafka_ca_secret, "--ignore-not-found")
        state = "absent"
        if existing:
            metadata = existing.get("metadata", {})
            owner = metadata.get("labels", {}).get("insurance-hub.io/owner")
            managers = {
                item.get("manager")
                for item in metadata.get("managedFields", [])
                if isinstance(item, dict)
            }
            require(
                existing.get("kind") == "Secret"
                and metadata.get("name") == self.kafka_ca_secret
                and metadata.get("namespace") == self.namespace
                and metadata.get("labels", {}).get("app.kubernetes.io/name") == "dashboard-api"
                and metadata.get("labels", {}).get("app.kubernetes.io/component") == "kafka-trust"
                and existing.get("type", "Opaque") == "Opaque"
                and set(existing.get("data", {})) == {"ca.crt"},
                f"Secret/{self.kafka_ca_secret} identity or keys differ",
            )
            try:
                stored = base64.b64decode(existing["data"]["ca.crt"], validate=True)
            except (TypeError, ValueError) as error:
                raise AdministrationError("registration Kafka trust data is malformed") from error
            require(stored == certificate, "registration Kafka trust differs")
            require(owner == OWNER, "registration Kafka trust is not owned")
            require(
                not managers or "dashboard-registration-administration" in managers,
                "registration Kafka trust manager differs",
            )
            state = "owned"

        self.apply_secret(
            self.kafka_ca_secret,
            {"ca.crt": certificate.decode()},
            "kafka-trust",
            force_conflicts=False,
        )
        current = self.json("get", "secret", self.kafka_ca_secret)
        self.validate_secret(current, self.kafka_ca_secret, {"ca.crt"}, "kafka-trust", OWNER)
        try:
            stored = base64.b64decode(current["data"]["ca.crt"], validate=True)
        except (TypeError, ValueError) as error:
            raise AdministrationError("registration Kafka trust data is malformed") from error
        require(stored == certificate, "registration Kafka trust differs")
        return state

    def ensure_topic(self) -> str:
        existing = self.json(
            "get",
            "kafkatopic",
            TOPIC,
            "--ignore-not-found",
            namespace=self.data_namespace,
        )
        if existing:
            self.verify_topic(existing)
            return "owned"
        if self.broker_topic_partitions(allow_absent=True):
            return "preserved"
        manifest = {
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
            "--field-manager=dashboard-registration-administration",
            "-f",
            "-",
            namespace=self.data_namespace,
            input_text=json.dumps(manifest),
        )
        self.verify_topic()
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            if self.broker_topic_partitions(allow_absent=True):
                break
            time.sleep(1)
        else:
            raise AdministrationError(f"Kafka topic {TOPIC} was not created")
        return "created"

    def verify_topic(
        self,
        value: dict[str, Any] | None = None,
        *,
        allow_absent: bool = False,
    ) -> bool:
        topic = value
        if topic is None:
            arguments = ["get", "kafkatopic", TOPIC]
            if allow_absent:
                arguments.append("--ignore-not-found")
            topic = self.json(*arguments, namespace=self.data_namespace)
        if not topic and allow_absent:
            return False
        metadata = topic.get("metadata", {})
        require(
            topic.get("kind") == "KafkaTopic"
            and metadata.get("name") == TOPIC
            and metadata.get("namespace") == self.data_namespace
            and metadata.get("labels", {}).get("strimzi.io/cluster") == self.kafka_cluster
            and metadata.get("labels", {}).get("insurance-hub.io/owner") == OWNER
            and topic.get("spec") == {"partitions": 1, "replicas": self.kafka_replicas},
            f"KafkaTopic/{TOPIC} differs",
        )
        return True

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

    def broker_topic_partitions(
        self,
        broker: str | None = None,
        *,
        allow_absent: bool = False,
    ) -> list[int]:
        selected_broker = broker or self.kafka_broker()
        if allow_absent:
            topics = set(
                self.run(
                    "exec",
                    selected_broker,
                    "-c",
                    "kafka",
                    "--",
                    "/opt/kafka/bin/kafka-topics.sh",
                    "--bootstrap-server",
                    "localhost:9092",
                    "--list",
                    namespace=self.data_namespace,
                ).splitlines()
            )
            if TOPIC not in topics:
                return []
        topic = self.run(
            "exec",
            selected_broker,
            "-c",
            "kafka",
            "--",
            "/opt/kafka/bin/kafka-topics.sh",
            "--bootstrap-server",
            "localhost:9092",
            "--describe",
            "--topic",
            TOPIC,
            namespace=self.data_namespace,
        )
        partitions = sorted({int(value) for value in re.findall(r"Partition:\s*(\d+)", topic)})
        require(
            partitions and partitions == list(range(len(partitions))), "registration topic differs"
        )
        return partitions

    def kafka_snapshot(self) -> dict[str, Any]:
        broker = self.kafka_broker()
        partitions = self.broker_topic_partitions(broker)
        groups = set(
            self.run(
                "exec",
                broker,
                "-c",
                "kafka",
                "--",
                "/opt/kafka/bin/kafka-consumer-groups.sh",
                "--bootstrap-server",
                "localhost:9092",
                "--list",
                namespace=self.data_namespace,
            ).splitlines()
        )
        if GROUP not in groups:
            return {"partitions": partitions, "offsets": {}, "activeMembers": 0}
        group = self.run(
            "exec",
            broker,
            "-c",
            "kafka",
            "--",
            "/opt/kafka/bin/kafka-consumer-groups.sh",
            "--bootstrap-server",
            "localhost:9092",
            "--describe",
            "--group",
            GROUP,
            namespace=self.data_namespace,
        )
        offsets: dict[int, tuple[int, int]] = {}
        members: set[str] = set()
        for line in group.splitlines():
            fields = line.split()
            if len(fields) < 6 or fields[0] != GROUP or fields[1] != TOPIC:
                continue
            try:
                partition, committed, log_end = map(int, fields[2:5])
            except ValueError as error:
                raise AdministrationError("registration group progress differs") from error
            require(0 <= committed <= log_end, "registration group offset is out of range")
            offsets[partition] = (committed, log_end)
            if len(fields) > 6 and fields[6] != "-":
                members.add(fields[6])
        require(
            set(offsets) == set(partitions),
            "registration group partition progress is incomplete",
        )
        return {"partitions": partitions, "offsets": offsets, "activeMembers": len(members)}


def writer_role() -> dict[str, Any]:
    return {
        "cluster": [],
        "indices": [
            {
                "names": [INDEX],
                "privileges": ["index"],
                "allow_restricted_indices": False,
            }
        ],
        "applications": [],
        "run_as": [],
        "metadata": {"owner": OWNER},
        "remote_indices": [],
        "remote_cluster": [],
    }


def mapping_is_compatible(actual: dict[str, Any], expected: dict[str, Any]) -> bool:
    if actual.get("dynamic") != expected.get("dynamic"):
        return False
    properties = actual.get("properties", {})
    expected_properties = expected.get("properties", {})
    for name, contract in expected_properties.items():
        current = properties.get(name, {})
        if current.get("type") != contract.get("type"):
            return False
        expected_keyword = contract.get("fields", {}).get("keyword")
        if (
            expected_keyword
            and current.get("fields", {}).get("keyword", {}).get("type") != "keyword"
        ):
            return False
    return True


def inspect_index(
    client: shared.ElasticsearchClient,
    definition: dict[str, Any],
    *,
    allow_absent: bool = False,
) -> int | None:
    status, mapping = client.request("GET", f"/{INDEX}/_mapping")
    if status == 404 and allow_absent:
        return None
    require(
        status == 200 and isinstance(mapping, dict) and set(mapping) == {INDEX},
        "registration index differs",
    )
    require(
        mapping_is_compatible(mapping[INDEX].get("mappings", {}), definition),
        "registration index mapping differs",
    )
    status, count = client.request("GET", f"/{INDEX}/_count")
    require(
        status == 200 and isinstance(count, dict) and isinstance(count.get("count"), int),
        "registration index count differs",
    )
    return int(count["count"])


def ensure_index(client: shared.ElasticsearchClient, definition: dict[str, Any]) -> int:
    count = inspect_index(client, definition, allow_absent=True)
    if count is not None:
        return count
    status, _ = client.request("PUT", f"/{INDEX}", {"mappings": definition})
    require(status == 200, "create registration index failed")
    created = inspect_index(client, definition)
    require(created == 0, "new registration index is not empty")
    return 0


def stable_kafka_progress(first: dict[str, Any], second: dict[str, Any]) -> bool:
    if first.get("partitions") != second.get("partitions") or first.get(
        "activeMembers"
    ) != second.get("activeMembers"):
        return False
    first_offsets = first.get("offsets", {})
    second_offsets = second.get("offsets", {})
    if set(first_offsets) != set(second_offsets):
        return False
    return all(
        first_offsets[partition][0] == second_offsets[partition][0]
        and second_offsets[partition][1] >= first_offsets[partition][1]
        for partition in first_offsets
    )


def verify_reader(client: shared.ElasticsearchClient) -> None:
    status, roles = client.request("GET", f"/_security/role/{READER}")
    require(status == 200 and isinstance(roles, dict), "registration reader role is absent")
    status, users = client.request("GET", f"/_security/user/{READER}")
    require(status == 200 and isinstance(users, dict), "registration reader user is absent")
    require(
        shared.normalize_role(roles.get(READER, {})) == reader_role(),
        "registration reader role differs",
    )
    user = users.get(READER, {})
    require(
        user.get("roles") == [READER]
        and user.get("enabled") is True
        and user.get("metadata") == {"owner": "issue-287"},
        "registration reader user differs",
    )


def reader_role() -> dict[str, Any]:
    return {
        "cluster": [],
        "indices": [
            {
                "names": [INDEX],
                "privileges": ["read", "view_index_metadata"],
                "allow_restricted_indices": False,
            }
        ],
        "applications": [],
        "run_as": [],
        "metadata": {"owner": "issue-287"},
        "remote_indices": [],
        "remote_cluster": [],
    }


def verify_writer(client: shared.ElasticsearchClient) -> None:
    status, roles = client.request("GET", f"/_security/role/{WRITER}")
    require(status == 200 and isinstance(roles, dict), "registration writer role is absent")
    status, users = client.request("GET", f"/_security/user/{WRITER}")
    require(status == 200 and isinstance(users, dict), "registration writer user is absent")
    require(
        shared.normalize_role(roles.get(WRITER, {})) == writer_role(),
        "registration writer role differs",
    )
    user = users.get(WRITER, {})
    require(
        user.get("roles") == [WRITER]
        and user.get("enabled") is True
        and user.get("metadata") == {"owner": OWNER},
        "registration writer user differs",
    )


def require_identity_owned_or_absent(
    client: shared.ElasticsearchClient,
    identity: str,
    owner: str,
) -> None:
    for path, label in (
        (f"/_security/role/{identity}", "role"),
        (f"/_security/user/{identity}", "user"),
    ):
        status, value = client.request("GET", path)
        require(status in {200, 404}, f"inspect registration {identity} {label} failed")
        if status == 200:
            require(
                isinstance(value, dict)
                and value.get(identity, {}).get("metadata") == {"owner": owner},
                f"registration {identity} {label} is not owned",
            )


def apply_writer(client: shared.ElasticsearchClient, password: str) -> None:
    status, _ = client.request("PUT", f"/_security/role/{WRITER}", writer_role())
    require(status == 200, "apply registration writer role failed")
    status, _ = client.request(
        "PUT",
        f"/_security/user/{WRITER}",
        {
            "password": password,
            "roles": [WRITER],
            "full_name": "Dashboard policy statistics writer",
            "metadata": {"owner": OWNER},
            "enabled": True,
        },
    )
    require(status == 200, "apply registration writer user failed")
    verify_writer(client)


def apply_reader(client: shared.ElasticsearchClient, password: str) -> None:
    status, _ = client.request("PUT", f"/_security/role/{READER}", reader_role())
    require(status == 200, "apply registration reader role failed")
    status, _ = client.request(
        "PUT",
        f"/_security/user/{READER}",
        {
            "password": password,
            "roles": [READER],
            "full_name": "Dashboard total-sales reader",
            "metadata": {"owner": "issue-287"},
            "enabled": True,
        },
    )
    require(status == 200, "apply registration reader user failed")
    verify_reader(client)


def verify_effective_privileges(client: shared.ElasticsearchClient, identity: str) -> None:
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
                        "manage",
                    ],
                },
                {
                    "names": ["policy_stats_other"],
                    "privileges": ["read", "index", "delete", "manage"],
                },
            ],
        },
    )
    selected = {
        "read": identity == READER,
        "view_index_metadata": identity == READER,
        "index": identity == WRITER,
        "delete": False,
        "manage": False,
    }
    require(
        status == 200
        and isinstance(value, dict)
        and value.get("cluster") == {"manage_security": False, "monitor": False}
        and value.get("index", {}).get(INDEX) == selected
        and value.get("index", {}).get("policy_stats_other")
        == {"read": False, "index": False, "delete": False, "manage": False},
        f"{identity} effective privileges differ",
    )
    status, _ = client.request("GET", "/_security/role")
    require(status == 403, f"{identity} security denial differs")
    status, _ = client.request("GET", f"/{INDEX}/_count")
    require(status == (200 if identity == READER else 403), f"{identity} read boundary differs")


def verify_secrets(target: Target) -> tuple[str, str]:
    reader = target.json("get", "secret", target.reader_secret)
    target.validate_secret(
        reader, target.reader_secret, {"username", "password"}, "elasticsearch-reader", "issue-287"
    )
    username = base64.b64decode(reader["data"]["username"], validate=True).decode()
    require(username == READER, "registration reader Secret username differs")
    reader_password = base64.b64decode(reader["data"]["password"], validate=True).decode()
    shared.require_password(reader_password)
    writer_password = target.optional_writer_password()
    require(bool(writer_password), "registration writer Secret is absent")
    trust = target.json("get", "secret", target.kafka_ca_secret)
    target.validate_secret(trust, target.kafka_ca_secret, {"ca.crt"}, "kafka-trust", OWNER)
    require(
        target.secret_value(target.namespace, target.kafka_ca_secret, "ca.crt")
        == target.secret_value(target.data_namespace, target.kafka_public_ca_secret, "ca.crt"),
        "registration Kafka trust differs",
    )
    return reader_password, writer_password


def execute(operation: str) -> None:
    require(operation in {"inspect", "ensure", "verify"}, "unsupported registration operation")
    if operation == "ensure":
        environment = os.environ.get("DEPLOY_ENV", "")
        require(
            os.environ.get("DASHBOARD_REGISTRATION_CONFIRM")
            == f"ensure-{environment}-dashboard-{OWNER}",
            "DASHBOARD_REGISTRATION_CONFIRM differs",
        )
    definition = load_definition()["mapping"]
    target = Target()
    target.verify_boundary()
    kafka_ca = target.secret_value(target.data_namespace, target.kafka_public_ca_secret, "ca.crt")
    require(b"BEGIN CERTIFICATE" in kafka_ca, "Kafka public CA differs")
    if operation == "ensure":
        target.ensure_kafka_trust(kafka_ca)
        target.ensure_topic()
    else:
        target.verify_topic(allow_absent=True)
    certificate = (
        target.secret_value(target.data_namespace, target.public_ca_secret, "ca.crt")
        if target.public_ca_secret
        else b""
    )
    password = target.secret_value(target.data_namespace, target.admin_secret, "elastic").decode()
    first = target.kafka_snapshot()
    time.sleep(1)
    second = target.kafka_snapshot()
    require(
        stable_kafka_progress(first, second),
        "registration Kafka metadata or progress is not stable",
    )
    with shared.PortForward(target) as port:
        admin = shared.client_for(target, "elastic", password, port, certificate)
        if operation == "ensure":
            count = ensure_index(admin, definition)
            require_identity_owned_or_absent(admin, READER, "issue-287")
            reader_password = (
                target.optional_password(
                    target.reader_secret,
                    READER,
                    "elasticsearch-reader",
                    "issue-287",
                )
                or shared.new_password()
            )
            apply_reader(admin, reader_password)
            target.apply_secret(
                target.reader_secret,
                {"username": READER, "password": reader_password},
                "elasticsearch-reader",
                owner="issue-287",
            )
            require_identity_owned_or_absent(admin, WRITER, OWNER)
            writer_password = target.optional_writer_password() or shared.new_password()
            apply_writer(admin, writer_password)
            target.apply_secret(
                target.writer_secret,
                {"username": WRITER, "password": writer_password},
                "elasticsearch-writer",
            )
            require(
                inspect_index(admin, definition) == count,
                "registration provisioning changed documents",
            )
        else:
            count = inspect_index(admin, definition)
            require(count is not None, "registration index is absent")
            verify_reader(admin)
        if operation in {"ensure", "verify"}:
            verify_writer(admin)
            reader_password, writer_password = verify_secrets(target)
            verify_effective_privileges(
                shared.client_for(target, READER, reader_password, port, certificate), READER
            )
            verify_effective_privileges(
                shared.client_for(target, WRITER, writer_password, port, certificate), WRITER
            )
    print(
        json.dumps(
            {
                "activeMembers": second["activeMembers"],
                "documents": count,
                "environment": target.environment,
                "index": INDEX,
                "operation": operation,
                "partitions": len(second["partitions"]),
                "status": "passed",
                "topic": TOPIC,
                "consumerGroup": GROUP,
            },
            sort_keys=True,
        )
    )


def cli(argv: list[str] | None = None) -> int:
    arguments = sys.argv[1:] if argv is None else argv
    try:
        require(len(arguments) == 1, "one registration operation is required")
        execute(arguments[0])
    except KeyboardInterrupt:
        return 130
    except (
        AdministrationError,
        shared.AdministrationError,
        CommandError,
        OSError,
        UnicodeDecodeError,
        ValueError,
    ) as error:
        print(f"dashboard-registration: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(cli())
