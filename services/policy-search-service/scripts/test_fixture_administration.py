"""Policy Search fixture administration guards; unit cases never contact a cluster."""

from __future__ import annotations

import os
from unittest.mock import Mock, patch

import fixture_administration as administration
import pytest


class FakeClient:
    def __init__(self, responses: list[tuple[int, object]]) -> None:
        self.responses = responses
        self.requests: list[tuple[str, str, object]] = []

    def request(
        self, method: str, path: str, body: dict | bytes | None = None
    ) -> tuple[int, object]:
        self.requests.append((method, path, body))
        assert self.responses, f"unexpected request {method} {path}"
        return self.responses.pop(0)


def test_fixture_asset_expands_exact_bounded_dataset() -> None:
    # when
    fixture = administration.load_fixture()

    # then
    assert fixture.index == "policy-views-issue-229"
    assert len(fixture.documents) == 108
    assert fixture.mapping["properties"]["number"] == {"type": "keyword"}
    assert fixture.mapping["properties"]["dateFrom"] == {
        "type": "date",
        "format": "strict_date",
    }
    assert len({item["id"] for item in fixture.documents}) == 108


def test_setup_is_idempotent_and_uses_exact_reader_boundary() -> None:
    # given
    fixture = administration.load_fixture()
    client = FakeClient(
        [
            (200, {}),
            (200, {}),
            (404, None),
            (200, {}),
            (200, {fixture.index: {"mappings": fixture.mapping}}),
            (200, {}),
            (200, {"errors": False}),
            (200, {"count": len(fixture.documents), "_shards": {"failed": 0}}),
        ]
    )

    # when
    administration.setup_fixture(client, fixture, "controlled-password-value")

    # then
    role = client.requests[0][2]
    assert role == {
        "cluster": [],
        "indices": [
            {
                "names": ["policy-views-issue-229"],
                "privileges": ["read", "view_index_metadata"],
                "allow_restricted_indices": False,
            }
        ],
        "applications": [],
        "run_as": [],
        "metadata": {"owner": "issue-229"},
    }
    assert client.requests[4][:2] == ("GET", "/policy-views-issue-229/_mapping")
    assert client.requests[5][:2] == (
        "POST",
        "/policy-views-issue-229/_delete_by_query?refresh=true",
    )
    bulk = client.requests[6][2]
    assert isinstance(bulk, bytes)
    assert bulk.count(b'"index"') == len(fixture.documents)
    assert not client.responses


def test_setup_refuses_mapping_drift_before_fixture_reset() -> None:
    # given
    fixture = administration.load_fixture()
    client = FakeClient(
        [
            (200, {}),
            (200, {}),
            (200, {}),
            (200, {fixture.index: {"mappings": {"dynamic": True}}}),
        ]
    )

    # when / then
    with pytest.raises(administration.AdministrationError, match="mapping differs"):
        administration.setup_fixture(client, fixture, "controlled-password-value")
    assert not any("_delete_by_query" in path for _, path, _ in client.requests)


def test_verify_proves_read_only_exact_index_and_security_denial() -> None:
    # given
    fixture = administration.load_fixture()
    client = FakeClient(
        [
            (
                200,
                {
                    "cluster": {"manage_security": False, "monitor": False},
                    "index": {
                        fixture.index: {
                            "read": True,
                            "view_index_metadata": True,
                            "write": False,
                            "create_index": False,
                            "delete_index": False,
                            "manage": False,
                        },
                        "policy-views": {
                            "read": False,
                            "view_index_metadata": False,
                            "write": False,
                            "manage": False,
                        },
                        "policy-views-issue-229-other": {
                            "read": False,
                            "view_index_metadata": False,
                            "write": False,
                            "manage": False,
                        },
                    },
                },
            ),
            (403, {"error": "redacted"}),
            (200, {"count": len(fixture.documents)}),
            (200, {"hits": {"hits": [{"_id": "accepted"}]}}),
        ]
    )

    # when
    administration.verify_fixture(client, fixture)

    # then
    assert client.requests[1][:2] == ("GET", "/_security/role")
    privileges = client.requests[0][2]
    assert isinstance(privileges, dict)
    assert privileges["cluster"] == ["manage_security", "monitor"]
    assert privileges["index"][1]["names"] == [
        "policy-views",
        "policy-views-issue-229-other",
    ]


def test_setup_and_retire_require_confirmation_before_target_contact() -> None:
    for operation in ("setup", "retire"):
        # given
        target = Mock()

        # when / then
        with (
            patch.dict(os.environ, {}, clear=True),
            patch.object(administration, "Target", return_value=target) as target_constructor,
            pytest.raises(administration.AdministrationError, match="FIXTURE_CONFIRM"),
        ):
            administration.execute(operation)
        target_constructor.assert_not_called()


def test_connection_failure_redacts_credentials() -> None:
    # given
    sentinel = "synthetic-secret-sentinel"
    client = administration.ElasticsearchClient(1, "reader", sentinel)

    # when / then
    with pytest.raises(administration.AdministrationError) as failure:
        client.request("GET", "/")
    assert sentinel not in str(failure.value)


def test_retire_removes_only_ticket_owned_elasticsearch_objects() -> None:
    # given
    fixture = administration.load_fixture()
    client = FakeClient([(200, {}), (200, {}), (200, {})])

    # when
    administration.retire_fixture(client, fixture)

    # then
    assert [(method, path) for method, path, _ in client.requests] == [
        ("DELETE", "/policy-views-issue-229"),
        ("DELETE", "/_security/user/go_policy_search_reader"),
        ("DELETE", "/_security/role/go_policy_search_reader"),
    ]
