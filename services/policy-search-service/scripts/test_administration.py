"""Policy Search administration guards; unit cases never contact a cluster."""

from __future__ import annotations

import os
from unittest.mock import Mock, patch

import administration
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


def identity_responses(
    identity: str, privileges: tuple[str, ...], description: str
) -> list[tuple[int, object]]:
    return [
        (200, {identity: administration.role_definition(privileges)}),
        (200, {identity: administration.user_definition(identity, description)}),
    ]


@pytest.mark.parametrize(
    ("environment", "context", "namespace", "data_namespace"),
    [
        ("local-dev", "kind-local-dev-insurance-hub", "local-dev-all", "local-dev-all"),
        ("qa", "qa-insurance-hub", "qa-svc", "qa-data"),
    ],
)
def test_target_uses_only_ordinary_secret_names(
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


def test_inspect_accepts_absent_or_exact_populated_index() -> None:
    absent = administration.inspect_index(FakeClient([(404, None)]))
    present = administration.inspect_index(
        FakeClient([(200, None), (200, canonical_definition()), (200, {"count": 3})])
    )

    assert not absent.exists and absent.documents == 0
    assert present.exists and present.documents == 3
    assert absent.mapping_sha256 == present.mapping_sha256


def test_setup_preserves_populated_index_and_existing_access() -> None:
    client = FakeClient(
        [
            (200, None),
            (200, canonical_definition()),
            (200, {"count": 3}),
            *identity_responses(administration.READER, ("read", "view_index_metadata"), "reader"),
            *identity_responses(administration.WRITER, ("index",), "writer"),
        ]
    )

    setup = administration.ensure_index_and_access(
        client,
        "existing-reader-password-with-32-bytes",
        "existing-writer-password-with-32-bytes",
    )

    assert setup.index.documents == 3
    assert not setup.reader_secret_missing and not setup.writer_secret_missing
    assert all(method == "GET" for method, _, _ in client.requests[1:])


def test_setup_creates_only_missing_index_and_access() -> None:
    client = FakeClient(
        [
            (404, None),
            (404, None),
            (404, None),
            (404, None),
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

    setup = administration.ensure_index_and_access(client, None, None)

    assert setup.index.exists and setup.index.documents == 0
    assert setup.reader_secret_missing and setup.writer_secret_missing
    assert len(setup.reader_password) >= 32 and len(setup.writer_password) >= 32
    assert client.requests[5] == (
        "PUT",
        "/policy-views",
        {"mappings": administration.MAPPING},
    )
    assert all(method != "DELETE" for method, _, _ in client.requests)


@pytest.mark.parametrize(
    "responses,message",
    [
        (
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
            ],
            "mapping differs",
        ),
        (
            [
                (200, None),
                (200, canonical_definition()),
                (200, {"count": 1}),
                (200, {administration.READER: {"cluster": ["all"]}}),
            ],
            "reader role differs",
        ),
    ],
)
def test_setup_rejects_incompatible_state_before_mutation(responses, message: str) -> None:
    client = FakeClient(responses)

    with pytest.raises(administration.AdministrationError, match=message):
        administration.ensure_index_and_access(client, "reader-password", "writer-password")

    assert all(method != "PUT" for method, _, _ in client.requests)


def test_setup_rejects_existing_user_without_canonical_secret() -> None:
    client = FakeClient(
        [
            (200, None),
            (200, canonical_definition()),
            (200, {"count": 1}),
            *identity_responses(administration.READER, ("read", "view_index_metadata"), "reader"),
        ]
    )

    with pytest.raises(administration.AdministrationError, match="reader Secret is missing"):
        administration.ensure_index_and_access(client, None, "writer-password")

    assert all(method != "PUT" for method, _, _ in client.requests)


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


def test_setup_requires_confirmation_before_target_contact() -> None:
    with (
        patch.dict(os.environ, {}, clear=True),
        patch.object(administration, "Target", return_value=Mock()) as target,
        pytest.raises(administration.AdministrationError, match="POLICY_SEARCH_SETUP_CONFIRM"),
    ):
        administration.execute("setup")
    target.assert_not_called()
