"""Policy Search cutover administration guards; unit cases never contact a cluster."""

from __future__ import annotations

import base64
import os
from unittest.mock import Mock, patch

import cutover_administration as administration
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


def canonical_definition() -> dict:
    return {
        administration.INDEX: {
            "aliases": {},
            "mappings": administration.MAPPING,
        }
    }


@pytest.mark.parametrize(
    ("environment", "context", "namespace", "data_namespace"),
    [
        ("local-dev", "kind-local-dev-insurance-hub", "local-dev-all", "local-dev-all"),
        ("qa", "qa-insurance-hub", "qa-svc", "qa-data"),
    ],
)
def test_canonical_target_uses_only_ordinary_secret_names(
    environment: str, context: str, namespace: str, data_namespace: str
) -> None:
    with patch.dict(
        os.environ,
        {
            "DEPLOY_ENV": environment,
            "DEPLOY_CONTEXT": context,
            "DEPLOY_SVC_NS": namespace,
            "DEPLOY_DATA_NS": data_namespace,
        },
        clear=True,
    ):
        target = administration.Target()

    secret_names = (
        target.reader_secret,
        target.writer_secret,
        target.elasticsearch_ca_secret,
        target.kafka_ca_secret,
    )
    assert all("cutover" not in name for name in secret_names)
    assert target.reader_secret == f"{environment}-policy-search-elasticsearch"
    assert target.writer_secret == f"{environment}-policy-search-elasticsearch-writer"
    assert target.kafka_ca_secret == f"{environment}-policy-search-kafka-ca"


def test_inspect_accepts_absent_or_exact_empty_index() -> None:
    absent = administration.inspect_index(FakeClient([(404, None)]))
    present = administration.inspect_index(
        FakeClient([(200, None), (200, canonical_definition()), (200, {"count": 0})])
    )

    assert not absent.exists and absent.documents == 0
    assert present.exists and present.documents == 0
    assert absent.mapping_sha256 == present.mapping_sha256


def test_ensure_creates_only_absent_canonical_index_and_exact_roles() -> None:
    client = FakeClient(
        [
            (404, None),
            (200, {}),
            (200, None),
            (200, canonical_definition()),
            (200, {"count": 0}),
            (200, {}),
            (200, {}),
            (200, {}),
            (200, {}),
        ]
    )

    state = administration.ensure_index_and_access(client, "reader-password", "writer-password")

    assert state.exists and state.documents == 0
    assert client.requests[1] == (
        "PUT",
        "/policy-views",
        {"mappings": administration.MAPPING},
    )
    reader_role = client.requests[5][2]
    writer_role = client.requests[7][2]
    assert reader_role["indices"][0]["privileges"] == ["read", "view_index_metadata"]
    assert writer_role["indices"][0]["privileges"] == ["index"]
    assert all(request[0:2] != ("DELETE", "/policy-views") for request in client.requests)


def test_ensure_rejects_mapping_or_documents_before_identity_mutation() -> None:
    mapping_drift = FakeClient(
        [
            (200, None),
            (
                200,
                {
                    administration.INDEX: {
                        "aliases": {},
                        "mappings": {"dynamic": True},
                    }
                },
            ),
        ]
    )
    with pytest.raises(administration.AdministrationError, match="mapping differs"):
        administration.ensure_index_and_access(mapping_drift, "reader", "writer")

    documents = FakeClient([(200, None), (200, canonical_definition()), (200, {"count": 1})])
    with pytest.raises(administration.AdministrationError, match="complete manifest"):
        administration.ensure_index_and_access(documents, "reader", "writer")
    assert not any("_security" in path for _, path, _ in documents.requests)


def test_verify_identity_requires_exact_boundary() -> None:
    privileges = {
        "cluster": {"manage_security": False, "monitor": False},
        "index": {
            "policy-views": {
                "read": True,
                "view_index_metadata": True,
                "index": False,
                "create_index": False,
                "delete_index": False,
                "manage": False,
            },
            "policy-views-issue-229": {
                "read": False,
                "view_index_metadata": False,
                "index": False,
                "manage": False,
            },
            "policy-views-other": {
                "read": False,
                "view_index_metadata": False,
                "index": False,
                "manage": False,
            },
        },
    }
    client = FakeClient([(200, privileges)])

    administration.verify_identity(client, administration.READER)

    assert client.requests[0][0:2] == ("POST", "/_security/user/_has_privileges")


def test_ensure_requires_confirmation_before_target_contact() -> None:
    with (
        patch.dict(os.environ, {}, clear=True),
        patch.object(administration, "Target", return_value=Mock()) as target,
        pytest.raises(administration.AdministrationError, match="CUTOVER_CONFIRM"),
    ):
        administration.execute("ensure")
    target.assert_not_called()


def test_migrate_secret_preserves_exact_credentials_without_exposing_data() -> None:
    encoded = {
        "username": base64.b64encode(administration.READER.encode()).decode(),
        "password": base64.b64encode(b"controlled-password-value-with-32-bytes").decode(),
    }
    target = Mock()
    target.environment = "qa"
    target.json.side_effect = [{"data": encoded}, {"data": encoded}]

    with patch.dict(
        os.environ,
        {"POLICY_SEARCH_READER_SOURCE_SECRET": "qa-policy-search-cutover-elasticsearch"},
        clear=True,
    ):
        migration = administration.migrate_secret(
            target,
            "POLICY_SEARCH_READER_SOURCE_SECRET",
            "qa-policy-search-elasticsearch",
            ("username", "password"),
            "canonical-elasticsearch-reader",
            administration.READER,
        )

    assert migration == (
        "qa-policy-search-cutover-elasticsearch",
        "qa-policy-search-elasticsearch",
    )
    target.apply_secret.assert_called_once_with(
        "qa-policy-search-elasticsearch",
        {
            "username": administration.READER,
            "password": "controlled-password-value-with-32-bytes",
        },
        "canonical-elasticsearch-reader",
        force_conflicts=True,
    )


def test_migrate_secret_rejects_wrong_identity_before_apply() -> None:
    target = Mock()
    target.environment = "qa"
    target.json.return_value = {
        "data": {
            "username": base64.b64encode(b"qualification-reader").decode(),
            "password": base64.b64encode(b"controlled-password-value-with-32-bytes").decode(),
        }
    }

    with (
        patch.dict(
            os.environ,
            {"POLICY_SEARCH_READER_SOURCE_SECRET": "qa-policy-search-cutover-elasticsearch"},
            clear=True,
        ),
        pytest.raises(administration.AdministrationError, match="username differs"),
    ):
        administration.migrate_secret(
            target,
            "POLICY_SEARCH_READER_SOURCE_SECRET",
            "qa-policy-search-elasticsearch",
            ("username", "password"),
            "canonical-elasticsearch-reader",
            administration.READER,
        )

    target.apply_secret.assert_not_called()


def test_migrate_requires_confirmation_before_target_contact() -> None:
    with (
        patch.dict(os.environ, {"DEPLOY_ENV": "qa"}, clear=True),
        patch.object(administration, "Target", return_value=Mock()) as target,
        pytest.raises(administration.AdministrationError, match="SECRET_MIGRATION_CONFIRM"),
    ):
        administration.execute("migrate-secrets")
    target.assert_not_called()
