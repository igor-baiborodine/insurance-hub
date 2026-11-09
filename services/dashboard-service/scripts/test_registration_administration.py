"""Ordinary Dashboard registration guards; unit tests never contact a cluster."""

from __future__ import annotations

import os
from unittest.mock import Mock, patch

import pytest
import registration_administration as administration


def environment(name: str = "local-dev") -> dict[str, str]:
    local = name == "local-dev"
    return {
        "DEPLOY_ENV": name,
        "DEPLOY_CONTEXT": "kind-local-dev-insurance-hub" if local else "qa-insurance-hub",
        "DEPLOY_SVC_NS": "local-dev-all" if local else "qa-svc",
        "DEPLOY_DATA_NS": "local-dev-all" if local else "qa-data",
    }


def record() -> dict[str, object]:
    return {
        "environments": ["local-dev", "qa"],
        "registrationAdministration": {
            "owner": "issue-293",
            "asset": "services/dashboard-service/internal/administration/registration.json",
            "index": "policy_stats",
            "readerRole": "go_dashboard_total_sales_reader",
            "readerUser": "go_dashboard_total_sales_reader",
            "writerRole": "go_dashboard_policy_stats_writer",
            "writerUser": "go_dashboard_policy_stats_writer",
            "registeredTopic": "policy-registered",
            "consumerGroup": "dashboard-service",
        },
    }


def target(name: str = "local-dev") -> dict[str, object]:
    local = name == "local-dev"
    prefix = name
    common = {
        "service": f"{prefix}-elasticsearch-es-http",
        "adminSecret": f"{prefix}-elasticsearch-es-elastic-user",
        "publicCASecret": "" if local else "qa-elasticsearch-es-http-certs-public",
        "serverName": "" if local else "qa-elasticsearch-es-http.qa-data.svc",
        "kafkaCluster": f"{prefix}-kafka",
        "kafkaBootstrap": (
            "local-dev-kafka-kafka-bootstrap:9093"
            if local
            else "qa-kafka-kafka-bootstrap.qa-data.svc:9093"
        ),
        "kafkaPublicCASecret": f"{prefix}-kafka-cluster-ca-cert",
        "kafkaReplicas": 1 if local else 3,
    }
    return {
        "context": "kind-local-dev-insurance-hub" if local else "qa-insurance-hub",
        "namespace": "local-dev-all" if local else "qa-svc",
        "dataNamespace": "local-dev-all" if local else "qa-data",
        "registrationAdministration": common
        | {
            "readerSecret": f"{prefix}-dashboard-elasticsearch",
            "writerSecret": f"{prefix}-dashboard-elasticsearch-writer",
            "kafkaWorkloadCASecret": f"{prefix}-dashboard-kafka-ca",
        },
        "retirementAdministration": common
        | {
            "readerSecret": f"{prefix}-dashboard-elasticsearch-issue-290",
            "writerSecret": f"{prefix}-dashboard-elasticsearch-writer-issue-290",
            "kafkaWorkloadCASecret": f"{prefix}-dashboard-kafka-ca-issue-290",
        },
    }


@pytest.mark.parametrize("name", ["local-dev", "qa"])
def test_target_separates_registration_and_retirement_secrets(name: str) -> None:
    with (
        patch.dict(os.environ, environment(name), clear=True),
        patch.object(administration, "service_record", return_value=record()),
        patch.object(administration, "target_record", return_value=target(name)),
    ):
        selected = administration.Target()
    assert selected.reader_secret == f"{name}-dashboard-elasticsearch"
    assert selected.writer_secret == f"{name}-dashboard-elasticsearch-writer"
    assert selected.kafka_ca_secret == f"{name}-dashboard-kafka-ca"


def test_ensure_requires_confirmation_before_target_selection() -> None:
    with (
        patch.dict(os.environ, environment(), clear=True),
        patch.object(administration, "Target") as selected,
        pytest.raises(administration.AdministrationError, match="DASHBOARD_REGISTRATION_CONFIRM"),
    ):
        administration.execute("ensure")
    selected.assert_not_called()


def test_registration_writer_role_targets_only_policy_stats() -> None:
    assert administration.writer_role()["indices"] == [
        {
            "names": ["policy_stats"],
            "privileges": ["index"],
            "allow_restricted_indices": False,
        }
    ]
    assert "issue_290" not in str(administration.writer_role())


def test_foreign_writer_blocks_before_mutation() -> None:
    client = Mock()
    client.request.return_value = (
        200,
        {administration.WRITER: {"metadata": {"owner": "foreign"}}},
    )
    with pytest.raises(administration.AdministrationError, match="not owned"):
        administration.require_identity_owned_or_absent(
            client,
            administration.WRITER,
            administration.OWNER,
        )
    assert client.request.call_count == 1


def test_no_registration_retire_operation_exists() -> None:
    with (
        patch.dict(os.environ, environment(), clear=True),
        patch.object(administration, "Target") as selected,
        pytest.raises(
            administration.AdministrationError,
            match="unsupported registration operation",
        ),
    ):
        administration.execute("retire")
    selected.assert_not_called()


def test_kafka_snapshot_requires_complete_in_range_registration_progress() -> None:
    selected = object.__new__(administration.Target)
    selected.kafka_cluster = "local-dev-kafka"
    selected.data_namespace = "local-dev-all"
    selected.json = Mock(
        return_value={
            "items": [
                {
                    "metadata": {
                        "name": "local-dev-kafka-0",
                        "labels": {"strimzi.io/broker-role": "true"},
                    },
                    "status": {
                        "phase": "Running",
                        "conditions": [{"type": "Ready", "status": "True"}],
                    },
                }
            ]
        }
    )
    selected.run = Mock(
        side_effect=[
            "Topic: policy-registered Partition: 0 Leader: 0 Replicas: 0 Isr: 0\n",
            "dashboard-service\n",
            "GROUP TOPIC PARTITION CURRENT-OFFSET LOG-END-OFFSET LAG CONSUMER-ID HOST CLIENT-ID\n"
            "dashboard-service policy-registered 0 7 9 2 - - -\n",
        ]
    )
    assert selected.kafka_snapshot() == {
        "partitions": [0],
        "offsets": {0: (7, 9)},
        "activeMembers": 0,
    }


def test_stable_kafka_progress_accepts_new_arrivals_without_commit_changes() -> None:
    # given
    first = {"partitions": [0], "offsets": {0: (7, 9)}, "activeMembers": 0}
    second = {"partitions": [0], "offsets": {0: (7, 10)}, "activeMembers": 0}

    # when
    stable = administration.stable_kafka_progress(first, second)

    # then
    assert stable


def test_ensure_index_creates_empty_mapping_only_when_absent() -> None:
    definition = administration.load_definition()["mapping"]
    client = Mock()
    with patch.object(administration, "inspect_index", side_effect=[None, 0]) as inspect:
        client.request.return_value = (200, {})

        count = administration.ensure_index(client, definition)

    assert count == 0
    client.request.assert_called_once_with(
        "PUT",
        "/policy_stats",
        {"mappings": definition},
    )
    assert inspect.call_count == 2


def test_ensure_index_preserves_compatible_populated_state() -> None:
    definition = administration.load_definition()["mapping"]
    client = Mock()
    with patch.object(administration, "inspect_index", return_value=7):
        count = administration.ensure_index(client, definition)

    assert count == 7
    client.request.assert_not_called()


def test_mapping_compatibility_rejects_non_strict_existing_mapping() -> None:
    definition = administration.load_definition()["mapping"]
    actual = administration.json.loads(administration.json.dumps(definition))
    actual["dynamic"] = False

    assert not administration.mapping_is_compatible(actual, definition)


@pytest.mark.parametrize(
    "managed_fields",
    [[], [{"manager": "dashboard-event-administration"}]],
    ids=["historical-managerless", "managed"],
)
def test_kafka_trust_adopts_exact_issue_290_predecessor(
    managed_fields: list[dict[str, str]],
) -> None:
    # given
    certificate = b"-----BEGIN CERTIFICATE-----\nsynthetic\n-----END CERTIFICATE-----\n"
    encoded = administration.base64.b64encode(certificate).decode("ascii")
    selected = object.__new__(administration.Target)
    selected.namespace = "local-dev-all"
    selected.kafka_ca_secret = "local-dev-dashboard-kafka-ca"
    predecessor = {
        "kind": "Secret",
        "metadata": {
            "name": selected.kafka_ca_secret,
            "namespace": selected.namespace,
            "labels": {
                "app.kubernetes.io/name": "dashboard-api",
                "app.kubernetes.io/component": "kafka-trust",
                "insurance-hub.io/owner": "issue-290",
            },
            "managedFields": managed_fields,
        },
        "type": "Opaque",
        "data": {"ca.crt": encoded},
    }
    adopted = {
        **predecessor,
        "metadata": {
            **predecessor["metadata"],
            "labels": {
                **predecessor["metadata"]["labels"],
                "insurance-hub.io/owner": administration.OWNER,
            },
            "managedFields": [{"manager": "dashboard-registration-administration"}],
        },
    }
    selected.json = Mock(side_effect=[predecessor, adopted])
    selected.run = Mock()

    # when
    state = selected.ensure_kafka_trust(certificate)

    # then
    assert state == "adopted-issue-290"
    assert "--force-conflicts" in selected.run.call_args.args


def test_kafka_trust_rejects_issue_290_predecessor_with_foreign_manager() -> None:
    # given
    certificate = b"-----BEGIN CERTIFICATE-----\nsynthetic\n-----END CERTIFICATE-----\n"
    selected = object.__new__(administration.Target)
    selected.namespace = "local-dev-all"
    selected.kafka_ca_secret = "local-dev-dashboard-kafka-ca"
    selected.json = Mock(
        return_value={
            "kind": "Secret",
            "metadata": {
                "name": selected.kafka_ca_secret,
                "namespace": selected.namespace,
                "labels": {
                    "app.kubernetes.io/name": "dashboard-api",
                    "app.kubernetes.io/component": "kafka-trust",
                    "insurance-hub.io/owner": "issue-290",
                },
                "managedFields": [{"manager": "foreign-manager"}],
            },
            "type": "Opaque",
            "data": {"ca.crt": administration.base64.b64encode(certificate).decode("ascii")},
        }
    )
    selected.run = Mock()

    # when / then
    with pytest.raises(administration.AdministrationError, match="predecessor manager"):
        selected.ensure_kafka_trust(certificate)
    selected.run.assert_not_called()


def test_kafka_trust_rejects_foreign_predecessor_before_apply() -> None:
    # given
    certificate = b"-----BEGIN CERTIFICATE-----\nsynthetic\n-----END CERTIFICATE-----\n"
    selected = object.__new__(administration.Target)
    selected.namespace = "local-dev-all"
    selected.kafka_ca_secret = "local-dev-dashboard-kafka-ca"
    selected.json = Mock(
        return_value={
            "kind": "Secret",
            "metadata": {
                "name": selected.kafka_ca_secret,
                "namespace": selected.namespace,
                "labels": {
                    "app.kubernetes.io/name": "dashboard-api",
                    "app.kubernetes.io/component": "kafka-trust",
                    "insurance-hub.io/owner": "foreign-owner",
                },
                "managedFields": [{"manager": "foreign-manager"}],
            },
            "type": "Opaque",
            "data": {"ca.crt": administration.base64.b64encode(certificate).decode("ascii")},
        }
    )
    selected.run = Mock()

    # when / then
    with pytest.raises(administration.AdministrationError, match="not adoptable"):
        selected.ensure_kafka_trust(certificate)
    selected.run.assert_not_called()


def test_effective_writer_permission_rejects_mapping_admin_and_isolated_index() -> None:
    client = Mock()
    response = {
        "cluster": {"manage_security": False, "monitor": False},
        "index": {
            "policy_stats": {
                "read": False,
                "view_index_metadata": False,
                "index": True,
                "delete": False,
                "manage": False,
            },
            "policy_stats_issue_290": {
                "read": False,
                "index": False,
                "delete": False,
                "manage": False,
            },
        },
    }
    client.request.side_effect = [(200, response), (403, {}), (403, {})]
    administration.verify_effective_privileges(client, administration.WRITER)

    response["index"]["policy_stats"]["manage"] = True
    client.request.side_effect = [(200, response)]
    with pytest.raises(administration.AdministrationError, match="effective privileges"):
        administration.verify_effective_privileges(client, administration.WRITER)
