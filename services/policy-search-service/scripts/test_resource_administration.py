"""Policy Search resource-administration guards; unit cases never contact a cluster."""

from __future__ import annotations

import json
import os
from contextlib import nullcontext
from unittest.mock import Mock, patch

import pytest
import resource_administration as administration


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


class FakeEnsureTarget:
    environment = "local-dev"
    namespace = "local-dev-all"
    data_namespace = "local-dev-all"
    admin_secret = "admin-secret"
    public_ca_secret = "public-elasticsearch-ca"
    elasticsearch_ca_secret = "policy-search-elasticsearch-ca"
    kafka_public_ca_secret = "public-kafka-ca"
    kafka_ca_secret = "policy-search-kafka-ca"
    reader_secret = "policy-search-reader"
    writer_secret = "policy-search-writer"

    def __init__(
        self,
        reader_password: str | None,
        writer_password: str | None,
        elasticsearch_ca: bytes | None,
        kafka_ca: bytes | None,
    ) -> None:
        self.passwords = {
            self.reader_secret: reader_password,
            self.writer_secret: writer_password,
        }
        self.canonical_cas = {
            self.elasticsearch_ca_secret: elasticsearch_ca,
            self.kafka_ca_secret: kafka_ca,
        }
        self.applied: list[tuple[str, dict[str, str], str]] = []
        self.boundary_verified = False

    def verify_boundary(self) -> None:
        self.boundary_verified = True

    def secret_value(self, namespace: str, name: str, key: str) -> bytes:
        del namespace, key
        return {
            self.admin_secret: b"admin-password",
            self.public_ca_secret: b"elasticsearch-ca",
            self.kafka_public_ca_secret: b"kafka-ca",
        }[name]

    def optional_secret_value(self, name: str, key: str) -> bytes | None:
        del key
        return self.canonical_cas[name]

    def optional_password(self, name: str, identity: str) -> str | None:
        del identity
        return self.passwords[name]

    def apply_secret(self, name: str, data: dict[str, str], component: str) -> None:
        self.applied.append((name, data, component))
        if "password" in data:
            self.passwords[name] = data["password"]
        if "ca.crt" in data:
            self.canonical_cas[name] = data["ca.crt"].encode()


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


def test_new_resource_metadata_is_service_owned() -> None:
    role = administration.role_definition(("read",))
    user = administration.user_definition(administration.READER, "reader", "password")
    target = object.__new__(administration.Target)
    target.namespace = "service-namespace"
    target.run = Mock(return_value="")

    target.apply_secret("reader-secret", {"password": "password"}, "reader")
    secret = json.loads(target.run.call_args.kwargs["input_text"])

    assert role["metadata"] == {"owner": administration.OWNER}
    assert user["metadata"] == {"owner": administration.OWNER}
    assert user["full_name"] == "Policy Search reader"
    assert secret["metadata"]["labels"]["insurance-hub.io/owner"] == administration.OWNER
    assert "issue-235" not in json.dumps((role, user, secret))


def test_inspect_accepts_absent_or_exact_populated_index() -> None:
    absent = administration.inspect_index(FakeClient([(404, None)]))
    present = administration.inspect_index(
        FakeClient([(200, None), (200, canonical_definition()), (200, {"count": 3})])
    )

    assert not absent.exists and absent.documents == 0
    assert present.exists and present.documents == 3
    assert absent.mapping_sha256 == present.mapping_sha256


def test_ensure_preserves_populated_index_and_existing_access() -> None:
    client = FakeClient(
        [
            (200, None),
            (200, canonical_definition()),
            (200, {"count": 3}),
            *identity_responses(administration.READER, ("read", "view_index_metadata"), "reader"),
            *identity_responses(administration.WRITER, ("index",), "writer"),
        ]
    )

    state = administration.ensure_index_and_access(
        client,
        "existing-reader-password-with-32-bytes",
        "existing-writer-password-with-32-bytes",
    )

    assert state.documents == 3
    assert all(method == "GET" for method, _, _ in client.requests[1:])


def test_inspect_accepts_legacy_descriptive_identity_metadata() -> None:
    reader_role = administration.role_definition(("read", "view_index_metadata"))
    reader_role["metadata"] = {"owner": "issue-235"}
    reader_user = administration.user_definition(administration.READER, "reader")
    reader_user["full_name"] = "Policy Search issue-235 reader"
    reader_user["metadata"] = {"owner": "issue-235"}
    writer_role = administration.role_definition(("index",))
    writer_role["metadata"] = {"owner": "issue-235"}
    writer_user = administration.user_definition(administration.WRITER, "writer")
    writer_user["full_name"] = "Policy Search issue-235 writer"
    writer_user["metadata"] = {"owner": "issue-235"}
    client = FakeClient(
        [
            (200, None),
            (200, canonical_definition()),
            (200, {"count": 3}),
            (200, {administration.READER: reader_role}),
            (200, {administration.READER: reader_user}),
            (200, {administration.WRITER: writer_role}),
            (200, {administration.WRITER: writer_user}),
        ]
    )

    state, identities = administration.inspect_index_and_access(
        client,
        "existing-reader-password-with-32-bytes",
        "existing-writer-password-with-32-bytes",
    )

    assert state.documents == 3
    assert all(identity.role_exists and identity.user_exists for identity in identities)
    assert all(method != "PUT" for method, _, _ in client.requests)


def test_ensure_creates_only_missing_index_and_access() -> None:
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

    state = administration.ensure_index_and_access(
        client,
        "generated-reader-password-with-32-bytes",
        "generated-writer-password-with-32-bytes",
    )

    assert state.exists and state.documents == 0
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
def test_ensure_rejects_incompatible_state_before_mutation(responses, message: str) -> None:
    client = FakeClient(responses)

    with pytest.raises(administration.AdministrationError, match=message):
        administration.ensure_index_and_access(client, "reader-password", "writer-password")

    assert all(method != "PUT" for method, _, _ in client.requests)


def test_ensure_rejects_existing_user_without_canonical_secret() -> None:
    client = FakeClient(
        [
            (200, None),
            (200, canonical_definition()),
            (200, {"count": 1}),
            *identity_responses(administration.READER, ("read", "view_index_metadata"), "reader"),
        ]
    )

    with pytest.raises(administration.AdministrationError, match="reader Secret is missing"):
        administration.inspect_index_and_access(client, None, "writer-password")

    assert all(method != "PUT" for method, _, _ in client.requests)


def test_credential_persistence_recovers_before_access_creation() -> None:
    class CredentialTarget:
        reader_secret = "reader-secret"
        writer_secret = "writer-secret"

        def __init__(self) -> None:
            self.passwords: dict[str, str] = {}
            self.fail_writer_once = True

        def apply_secret(self, name: str, data: dict[str, str], component: str) -> None:
            del component
            if name == self.writer_secret and self.fail_writer_once:
                self.fail_writer_once = False
                raise administration.AdministrationError("simulated Secret apply failure")
            self.passwords[name] = data["password"]

    absent_boundary = [(404, None)] * 5
    target = CredentialTarget()
    interrupted_client = FakeClient(absent_boundary.copy())

    with pytest.raises(administration.AdministrationError, match="simulated Secret apply failure"):
        administration.persist_missing_credentials(target, interrupted_client, None, None)

    persisted_reader = target.passwords[target.reader_secret]
    assert target.writer_secret not in target.passwords
    assert all(method != "PUT" for method, _, _ in interrupted_client.requests)

    reader_password, writer_password = administration.persist_missing_credentials(
        target,
        FakeClient(absent_boundary.copy()),
        persisted_reader,
        None,
    )
    assert reader_password == persisted_reader
    assert writer_password == target.passwords[target.writer_secret]

    completion_client = FakeClient(
        [
            *absent_boundary,
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
    state = administration.ensure_index_and_access(
        completion_client,
        reader_password,
        writer_password,
    )

    assert state.exists and state.documents == 0
    assert [method for method, _, _ in completion_client.requests].count("PUT") == 5


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


def test_execute_ensure_preserves_existing_credentials_and_cas() -> None:
    target = FakeEnsureTarget(
        "existing-reader-password",
        "existing-writer-password",
        b"elasticsearch-ca",
        b"kafka-ca",
    )
    admin = Mock()
    state = administration.IndexState(True, 3, administration.mapping_digest())
    with (
        patch.dict(
            os.environ,
            {"POLICY_SEARCH_RESOURCE_CONFIRM": "ensure-policy-search-resources"},
            clear=True,
        ),
        patch.object(administration, "Target", return_value=target),
        patch.object(administration, "PortForward", return_value=nullcontext(9200)),
        patch.object(administration, "client_for", return_value=admin),
        patch.object(administration, "inspect_index_and_access") as inspect,
        patch.object(administration, "ensure_index_and_access", return_value=state) as ensure,
    ):
        administration.execute("ensure")

    assert target.boundary_verified
    assert target.applied == []
    inspect.assert_called_once()
    ensure.assert_called_once_with(admin, "existing-reader-password", "existing-writer-password")


def test_execute_ensure_creates_missing_credentials_and_cas() -> None:
    target = FakeEnsureTarget(None, None, None, None)
    admin = Mock()
    state = administration.IndexState(True, 0, administration.mapping_digest())
    with (
        patch.dict(
            os.environ,
            {"POLICY_SEARCH_RESOURCE_CONFIRM": "ensure-policy-search-resources"},
            clear=True,
        ),
        patch.object(administration, "Target", return_value=target),
        patch.object(administration, "PortForward", return_value=nullcontext(9200)),
        patch.object(administration, "client_for", return_value=admin),
        patch.object(administration, "inspect_index_and_access"),
        patch.object(administration, "ensure_index_and_access", return_value=state) as ensure,
        patch.object(
            administration.secrets,
            "token_urlsafe",
            side_effect=("generated-reader", "generated-writer"),
        ),
    ):
        administration.execute("ensure")

    assert [name for name, _, _ in target.applied] == [
        target.reader_secret,
        target.writer_secret,
        target.elasticsearch_ca_secret,
        target.kafka_ca_secret,
    ]
    assert target.applied[0][1] == {
        "username": administration.READER,
        "password": "generated-reader",
    }
    assert target.applied[1][1] == {
        "username": administration.WRITER,
        "password": "generated-writer",
    }
    assert target.applied[2][1] == {"ca.crt": "elasticsearch-ca"}
    assert target.applied[3][1] == {"ca.crt": "kafka-ca"}
    ensure.assert_called_once_with(admin, "generated-reader", "generated-writer")


def test_execute_ensure_rejects_ca_drift_before_mutation() -> None:
    target = FakeEnsureTarget(None, None, b"elasticsearch-ca", b"different-kafka-ca")
    with (
        patch.dict(
            os.environ,
            {"POLICY_SEARCH_RESOURCE_CONFIRM": "ensure-policy-search-resources"},
            clear=True,
        ),
        patch.object(administration, "Target", return_value=target),
        patch.object(administration, "PortForward", return_value=nullcontext(9200)),
        patch.object(administration, "client_for", return_value=Mock()),
        patch.object(administration, "inspect_index_and_access") as inspect,
        patch.object(administration, "ensure_index_and_access") as ensure,
        pytest.raises(administration.AdministrationError, match="Kafka CA Secret differs"),
    ):
        administration.execute("ensure")

    assert target.applied == []
    inspect.assert_not_called()
    ensure.assert_not_called()


def test_ensure_requires_confirmation_before_target_contact() -> None:
    with (
        patch.dict(os.environ, {}, clear=True),
        patch.object(administration, "Target", return_value=Mock()) as target,
        pytest.raises(administration.AdministrationError, match="POLICY_SEARCH_RESOURCE_CONFIRM"),
    ):
        administration.execute("ensure")
    target.assert_not_called()
