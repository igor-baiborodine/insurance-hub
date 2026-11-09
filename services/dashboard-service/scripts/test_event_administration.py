"""Dashboard event administration guard tests; unit cases never contact a cluster."""

from __future__ import annotations

import base64
import json
from types import SimpleNamespace
from unittest.mock import Mock, call, patch

import event_administration as administration
import pytest


def target_environment(name: str = "local-dev") -> dict[str, str]:
    local = name == "local-dev"
    return {
        "DEPLOY_ENV": name,
        "DEPLOY_CONTEXT": "kind-local-dev-insurance-hub" if local else "qa-insurance-hub",
        "DEPLOY_SVC_NS": "local-dev-all" if local else "qa-svc",
        "DEPLOY_DATA_NS": "local-dev-all" if local else "qa-data",
    }


def service_record() -> dict[str, object]:
    return {
        "environments": ["local-dev", "qa"],
        "eventAdministration": {
            "owner": administration.OWNER,
            "asset": "services/dashboard-service/internal/administration/registration.json",
            "index": administration.INDEX,
            "readerRole": administration.READER,
            "readerUser": administration.READER,
            "writerRole": administration.WRITER,
            "writerUser": administration.WRITER,
            "registeredTopic": administration.TOPIC,
            "consumerGroup": administration.GROUP,
        },
    }


def target_record(name: str = "local-dev") -> dict[str, object]:
    local = name == "local-dev"
    prefix = "local-dev" if local else "qa"
    return {
        "context": "kind-local-dev-insurance-hub" if local else "qa-insurance-hub",
        "namespace": "local-dev-all" if local else "qa-svc",
        "dataNamespace": "local-dev-all" if local else "qa-data",
        "eventAdministration": {
            "service": f"{prefix}-elasticsearch-es-http",
            "adminSecret": f"{prefix}-elasticsearch-es-elastic-user",
            "readerSecret": f"{prefix}-dashboard-elasticsearch-issue-290",
            "writerSecret": f"{prefix}-dashboard-elasticsearch-writer-issue-290",
            "publicCASecret": "" if local else "qa-elasticsearch-es-http-certs-public",
            "serverName": "" if local else "qa-elasticsearch-es-http.qa-data.svc",
            "kafkaCluster": f"{prefix}-kafka",
            "kafkaBootstrap": (
                "local-dev-kafka-kafka-bootstrap:9093"
                if local
                else "qa-kafka-kafka-bootstrap.qa-data.svc:9093"
            ),
            "kafkaPublicCASecret": f"{prefix}-kafka-cluster-ca-cert",
            "kafkaWorkloadCASecret": f"{prefix}-dashboard-kafka-ca-issue-290",
            "kafkaReplicas": 1 if local else 3,
        },
    }


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
def test_target_accepts_only_issue_290_catalog_coordinates(environment: str) -> None:
    with (
        patch.dict(administration.os.environ, target_environment(environment), clear=True),
        patch.object(administration, "service_record", return_value=service_record()),
        patch.object(administration, "target_record", return_value=target_record(environment)),
    ):
        target = administration.Target()

    assert target.environment == environment
    assert target.reader_secret == f"{environment}-dashboard-elasticsearch-issue-290"
    assert target.writer_secret == f"{environment}-dashboard-elasticsearch-writer-issue-290"
    assert target.kafka_ca_secret == f"{environment}-dashboard-kafka-ca-issue-290"


def test_target_rejects_wrong_coordinates_before_cluster_access() -> None:
    environment = target_environment()
    environment["DEPLOY_DATA_NS"] = "qa-data"
    with (
        patch.dict(administration.os.environ, environment, clear=True),
        patch.object(administration, "service_record", return_value=service_record()),
        patch.object(administration, "target_record", return_value=target_record()),
        pytest.raises(administration.AdministrationError, match="DEPLOY_DATA_NS"),
    ):
        administration.Target()


@pytest.mark.parametrize("operation", ["setup", "retire"])
def test_mutations_require_exact_confirmation_before_target_selection(operation: str) -> None:
    with (
        patch.dict(administration.os.environ, target_environment(), clear=True),
        patch.object(administration, "Target") as target,
        pytest.raises(administration.AdministrationError, match="DASHBOARD_EVENT_CONFIRM"),
    ):
        administration.execute(operation)

    target.assert_not_called()


def owned_secret(name: str, username: str, password: str, component: str) -> dict[str, object]:
    return {
        "kind": "Secret",
        "metadata": {
            "name": name,
            "namespace": "qa-svc",
            "labels": {
                "insurance-hub.io/owner": administration.OWNER,
                "app.kubernetes.io/component": component,
            },
        },
        "type": "Opaque",
        "data": {
            "username": base64.b64encode(username.encode()).decode(),
            "password": base64.b64encode(password.encode()).decode(),
        },
    }


def test_secret_reuse_requires_owned_exact_identity_and_keys() -> None:
    target = object.__new__(administration.Target)
    target.namespace = "qa-svc"
    name = "qa-dashboard-elasticsearch-issue-290"
    value = owned_secret(name, administration.READER, "a" * 64, "elasticsearch-reader")
    target.json = Mock(return_value=value)

    assert target.optional_password(name, administration.READER, "elasticsearch-reader") == (
        "a" * 64
    )

    value["metadata"]["labels"]["insurance-hub.io/owner"] = "foreign"
    with pytest.raises(administration.AdministrationError, match="identity or keys"):
        target.optional_password(name, administration.READER, "elasticsearch-reader")


def test_secret_apply_passes_values_only_through_stdin() -> None:
    target = object.__new__(administration.Target)
    target.namespace = "qa-svc"
    target.run = Mock(return_value="")
    password = "b" * 64

    target.apply_secret(
        "qa-dashboard-elasticsearch-writer-issue-290",
        {"username": administration.WRITER, "password": password},
        "elasticsearch-writer",
    )

    manifest = json.loads(target.run.call_args.kwargs["input_text"])
    assert password not in target.run.call_args.args
    assert manifest["metadata"]["labels"]["insurance-hub.io/owner"] == "issue-290"
    assert set(manifest["data"]) == {"username", "password"}


def test_setup_creates_absent_index_and_preserves_resulting_count() -> None:
    target = SimpleNamespace(
        reader_secret="reader",
        writer_secret="writer",
        data_namespace="data",
        namespace="service",
        kafka_public_ca_secret="public-kafka-ca",
        kafka_ca_secret="workload-kafka-ca",
        optional_password=Mock(side_effect=["", ""]),
        apply_secret=Mock(),
        secret_value=Mock(return_value=b"-----BEGIN CERTIFICATE-----\nca"),
        apply_topic=Mock(),
    )
    admin = Mock(port=19200)
    admin.request.return_value = (200, {})
    definition = administration.load_definition()
    with (
        patch.object(administration, "inspect_index", side_effect=[None, 0, 0]),
        patch.object(administration, "new_password", side_effect=["c" * 64, "d" * 64]),
        patch.object(administration, "put_identity") as put_identity,
    ):
        count = administration.apply_setup(target, admin, definition, b"")

    assert count == 0
    assert admin.request.call_args_list[0] == call(
        "PUT", f"/{administration.INDEX}", {"mappings": definition.mapping}
    )
    assert put_identity.call_count == 2
    target.apply_topic.assert_called_once_with()


def test_setup_adopts_populated_compatible_index_without_document_mutation() -> None:
    target = SimpleNamespace(
        reader_secret="reader",
        writer_secret="writer",
        data_namespace="data",
        namespace="service",
        kafka_public_ca_secret="public-kafka-ca",
        kafka_ca_secret="workload-kafka-ca",
        optional_password=Mock(side_effect=["e" * 64, "f" * 64]),
        apply_secret=Mock(),
        secret_value=Mock(return_value=b"-----BEGIN CERTIFICATE-----\nca"),
        apply_topic=Mock(),
    )
    admin = Mock(port=19200)
    with (
        patch.object(administration, "inspect_index", side_effect=[7, 7]),
        patch.object(administration, "put_identity"),
    ):
        assert administration.apply_setup(target, admin, administration.load_definition(), b"") == 7

    assert not any(item.args[0] == "DELETE" for item in admin.request.call_args_list)
    assert not any("_delete_by_query" in item.args[1] for item in admin.request.call_args_list)


def privilege_response(identity: str) -> dict[str, object]:
    selected = (
        {
            "read": True,
            "view_index_metadata": True,
            "index": False,
            "delete": False,
            "delete_index": False,
            "manage": False,
        }
        if identity == administration.READER
        else {
            "read": False,
            "view_index_metadata": False,
            "index": True,
            "delete": False,
            "delete_index": False,
            "manage": False,
        }
    )
    denied = {"read": False, "index": False, "delete": False, "manage": False}
    return {
        "cluster": {"manage_security": False, "monitor": False},
        "index": {
            administration.INDEX: selected,
            "policy_stats": denied,
            "policy_stats_issue_290_other": denied,
        },
    }


@pytest.mark.parametrize("identity", [administration.READER, administration.WRITER])
def test_effective_permissions_require_exact_reader_writer_split(identity: str) -> None:
    client = Mock()
    client.request.side_effect = [
        (200, privilege_response(identity)),
        (403, {}),
        (200 if identity == administration.READER else 403, {}),
    ]

    administration.verify_privileges(client, identity)

    broadened = privilege_response(identity)
    broadened["cluster"]["monitor"] = True
    client.request.side_effect = [(200, broadened)]
    with pytest.raises(administration.AdministrationError, match="privileges differ"):
        administration.verify_privileges(client, identity)


def test_topic_contract_records_exact_cluster_owner_and_replication() -> None:
    target = object.__new__(administration.Target)
    target.data_namespace = "qa-data"
    target.kafka_cluster = "qa-kafka"
    target.kafka_replicas = 3
    target.run = Mock(return_value="")

    target.apply_topic()

    manifest = json.loads(target.run.call_args.kwargs["input_text"])
    assert manifest["metadata"]["name"] == "policy-registered-issue-290"
    assert manifest["metadata"]["labels"] == {
        "strimzi.io/cluster": "qa-kafka",
        "insurance-hub.io/owner": "issue-290",
    }
    assert manifest["spec"] == {"partitions": 1, "replicas": 3}


def test_retire_refuses_foreign_topic_before_any_topic_or_secret_deletion() -> None:
    target = SimpleNamespace(
        data_namespace="qa-data",
        reader_secret="qa-dashboard-elasticsearch-issue-290",
        writer_secret="qa-dashboard-elasticsearch-writer-issue-290",
        kafka_ca_secret="qa-dashboard-kafka-ca-issue-290",
        json=Mock(return_value={"metadata": {"labels": {"insurance-hub.io/owner": "foreign"}}}),
        verify_topic=Mock(side_effect=administration.AdministrationError("topic differs")),
        run=Mock(),
    )
    admin = Mock()
    admin.request.return_value = (200, {})
    with (
        patch.object(administration, "inspect_index", return_value=0),
        patch.object(administration, "inspect_identity", return_value=(None, None)),
        pytest.raises(administration.AdministrationError, match="topic differs"),
    ):
        administration.retire(target, admin, administration.load_definition())

    admin.request.assert_not_called()
    target.run.assert_not_called()


def test_retire_rejects_every_canonical_resource_name_before_inspection() -> None:
    definition = administration.load_definition()
    target = SimpleNamespace(
        reader_secret="local-dev-dashboard-elasticsearch",
        writer_secret="local-dev-dashboard-elasticsearch-writer",
        kafka_ca_secret="local-dev-dashboard-kafka-ca",
    )
    admin = Mock()
    with (
        patch.object(administration, "INDEX", "policy_stats"),
        patch.object(administration, "TOPIC", "policy-registered"),
        patch.object(administration, "GROUP", "dashboard-service"),
        patch.object(administration, "inspect_index") as inspect_index,
        pytest.raises(administration.AdministrationError, match="retirement boundary"),
    ):
        administration.retire(target, admin, definition)
    inspect_index.assert_not_called()
    admin.request.assert_not_called()
