"""Owned disposable Dashboard deployment acceptance."""

from __future__ import annotations

import disposable_deployment
import pytest


def test_canonical_render_uses_only_canonical_coordinates() -> None:
    cluster = disposable_deployment.Cluster("issue-293-unit")

    _text, items = disposable_deployment.render(
        cluster,
        "insurance-hub-dashboard-api:1.0.0-local.123456789abc",
        "123456789abc",
        "docker.elastic.co/elasticsearch/elasticsearch@sha256:" + "1" * 64,
        event_mode="canonical",
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
    assert config["EVENT_PROCESSING_MODE"] == "canonical"
    assert config["ELASTICSEARCH_INDEX"] == "policy_stats"
    assert config["KAFKA_REGISTERED_TOPIC"] == "policy-registered"
    assert config["KAFKA_GROUP"] == "dashboard-service"
    assert environment["ELASTICSEARCH_WRITER_USERNAME"]["valueFrom"]["secretKeyRef"] == {
        "name": "local-dev-dashboard-elasticsearch-writer",
        "key": "username",
    }
    assert "issue-290" not in disposable_deployment.yaml.safe_dump([config, deployment])


def test_canonical_offset_parser_rejects_other_group_rows() -> None:
    output = """GROUP TOPIC PARTITION CURRENT-OFFSET LOG-END-OFFSET LAG
dashboard-service policy-registered 0 4 4 0
dashboard-service-issue-290 policy-registered-issue-290 0 9 9 0
"""

    assert disposable_deployment.parse_consumer_group_offsets(
        output,
        disposable_deployment.CANONICAL_GROUP,
        disposable_deployment.CANONICAL_TOPIC,
    ) == [disposable_deployment.OffsetSnapshot(0, 4, 4)]


def test_java_owner_uses_plaintext_only_inside_disposable_fixture() -> None:
    candidate = disposable_deployment.JavaCandidateIdentity(
        "insurance-hub-dashboard-api-legacy:latest", "sha256:" + "2" * 64
    )

    resources = disposable_deployment.java_resources("issue-293-unit", candidate)
    deployment = disposable_deployment.checks.resource(
        resources, "Deployment", disposable_deployment.JAVA_DEPLOYMENT
    )
    config = disposable_deployment.checks.resource(
        resources, "ConfigMap", "local-dev-dashboard-config-legacy"
    )

    assert config["data"]["KAFKA_PORT"] == "9092"
    assert deployment["spec"]["replicas"] == 1
    assert deployment["spec"]["strategy"] == {"type": "Recreate"}
    assert deployment["spec"]["template"]["spec"]["automountServiceAccountToken"] is False


def test_canonical_query_oracle_preserves_java_date_key_compatibility() -> None:
    qualification = disposable_deployment.load_qualification()

    queries = disposable_deployment.canonical_query_oracles(qualification)

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
