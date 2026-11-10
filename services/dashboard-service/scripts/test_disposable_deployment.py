"""Owned disposable Dashboard deployment acceptance."""

from __future__ import annotations

from unittest.mock import Mock, patch

import disposable_deployment
import pytest


def test_registration_render_uses_only_ordinary_coordinates() -> None:
    cluster = disposable_deployment.Cluster("issue-293-unit")

    _text, items = disposable_deployment.render(
        cluster,
        "insurance-hub-dashboard-api:1.0.0-local.123456789abc",
        "123456789abc",
        "docker.elastic.co/elasticsearch/elasticsearch@sha256:" + "1" * 64,
        workers_enabled=True,
    )

    config = disposable_deployment.checks.resource(
        items, "ConfigMap", "local-dev-dashboard-config"
    )["data"]
    deployment = disposable_deployment.checks.resource(
        items, "Deployment", "local-dev-dashboard-api"
    )
    environment = {
        item["name"]: item
        for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]
    }
    assert config["EVENT_WORKERS_ENABLED"] == "true"
    assert config["ELASTICSEARCH_INDEX"] == "policy_stats"
    assert config["KAFKA_REGISTERED_TOPIC"] == "policy-registered"
    assert config["KAFKA_GROUP"] == "dashboard-service"
    assert environment["ELASTICSEARCH_WRITER_USERNAME"]["valueFrom"]["secretKeyRef"] == {
        "name": "local-dev-dashboard-elasticsearch-writer",
        "key": "username",
    }


def test_registration_offset_parser_rejects_other_group_rows() -> None:
    output = """GROUP TOPIC PARTITION CURRENT-OFFSET LOG-END-OFFSET LAG
dashboard-service policy-registered 0 4 4 0
foreign-dashboard policy-registered 0 9 9 0
"""

    assert disposable_deployment.parse_consumer_group_offsets(
        output,
        disposable_deployment.REGISTRATION_GROUP,
        disposable_deployment.REGISTRATION_TOPIC,
    ) == [disposable_deployment.OffsetSnapshot(0, 4, 4)]


def test_registration_progress_initializes_exact_empty_group() -> None:
    cluster = Mock()
    describe = """GROUP TOPIC PARTITION CURRENT-OFFSET LOG-END-OFFSET LAG
dashboard-service policy-registered 0 0 0 0
"""
    with patch.object(
        disposable_deployment,
        "kafka_cli",
        side_effect=["", describe],
    ) as kafka_cli:
        disposable_deployment.initialize_registration_progress(cluster)

    assert kafka_cli.call_args_list[0].args[2:] == (
        "--bootstrap-server",
        "localhost:9093",
        "--command-config",
        "/etc/kafka/secrets/client.properties",
        "--group",
        "dashboard-service",
        "--topic",
        "policy-registered",
        "--reset-offsets",
        "--to-earliest",
        "--execute",
    )


def test_query_oracle_preserves_accepted_date_key_compatibility() -> None:
    qualification = disposable_deployment.load_qualification()

    queries = disposable_deployment.query_oracles(qualification)

    trends = next(query for query in queries if query["name"] == "sales-trends")
    assert [item["period"] for item in trends["expectedResponse"]["periodSales"]] == [
        "2026-01-01T00:00:00.000Z",
        "2026-01-02T00:00:00.000Z",
    ]
    original = next(query for query in qualification["queries"] if query["name"] == "sales-trends")
    assert [item["period"] for item in original["expectedResponse"]["periodSales"]] == [
        "2026-01-01",
        "2026-01-02",
    ]


@pytest.mark.integration
def test_dashboard_disposable_deployment() -> None:
    disposable_deployment.run()
