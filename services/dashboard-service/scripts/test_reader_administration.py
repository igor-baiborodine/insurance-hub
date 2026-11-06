"""Dashboard reader administration guard tests; unit cases never contact a cluster."""

from __future__ import annotations

import base64
import json
from types import SimpleNamespace
from unittest.mock import Mock, call, patch

import pytest
import reader_administration as administration
from scripts.python.process_runner import CommandError


def target_environment(name: str = "local-dev") -> dict[str, str]:
    if name == "local-dev":
        return {
            "DEPLOY_ENV": name,
            "DEPLOY_CONTEXT": "kind-local-dev-insurance-hub",
            "DEPLOY_SVC_NS": "local-dev-all",
            "DEPLOY_DATA_NS": "local-dev-all",
        }
    return {
        "DEPLOY_ENV": name,
        "DEPLOY_CONTEXT": "qa-insurance-hub",
        "DEPLOY_SVC_NS": "qa-svc",
        "DEPLOY_DATA_NS": "qa-data",
    }


def service_record() -> dict[str, object]:
    return {
        "environments": ["local-dev", "qa"],
        "readerAdministration": {
            "owner": administration.OWNER,
            "index": administration.INDEX,
            "role": administration.IDENTITY,
            "user": administration.IDENTITY,
        },
    }


def target_record(name: str = "local-dev") -> dict[str, object]:
    local = name == "local-dev"
    return {
        "context": "kind-local-dev-insurance-hub" if local else "qa-insurance-hub",
        "namespace": "local-dev-all" if local else "qa-svc",
        "dataNamespace": "local-dev-all" if local else "qa-data",
        "readerAdministration": {
            "service": ("local-dev-elasticsearch-es-http" if local else "qa-elasticsearch-es-http"),
            "adminSecret": (
                "local-dev-elasticsearch-es-elastic-user"
                if local
                else "qa-elasticsearch-es-elastic-user"
            ),
            "workloadSecret": (
                "local-dev-dashboard-elasticsearch" if local else "qa-dashboard-elasticsearch"
            ),
            "publicCASecret": "" if local else "qa-elasticsearch-es-http-certs-public",
            "workloadCASecret": "" if local else "qa-dashboard-elasticsearch-ca",
            "serverName": "" if local else "qa-elasticsearch-es-http.qa-data.svc",
        },
    }


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
def test_target_accepts_only_the_cataloged_reader_boundary(environment: str) -> None:
    with (
        patch.dict(administration.os.environ, target_environment(environment), clear=True),
        patch.object(administration, "service_record", return_value=service_record()),
        patch.object(administration, "target_record", return_value=target_record(environment)),
    ):
        target = administration.Target()

    assert target.environment == environment
    assert target.workload_secret == f"{environment}-dashboard-elasticsearch"
    assert bool(target.workload_ca_secret) is (environment == "qa")


def test_target_rejects_wrong_coordinates_before_cluster_access() -> None:
    environment = target_environment()
    environment["DEPLOY_SVC_NS"] = "qa-svc"
    with (
        patch.dict(administration.os.environ, environment, clear=True),
        patch.object(administration, "service_record", return_value=service_record()),
        patch.object(administration, "target_record", return_value=target_record()),
        pytest.raises(administration.AdministrationError, match="DEPLOY_SVC_NS"),
    ):
        administration.Target()


def test_mutation_confirmation_is_required_before_target_selection() -> None:
    with (
        patch.dict(administration.os.environ, target_environment(), clear=True),
        patch.object(administration, "Target") as target,
        pytest.raises(administration.AdministrationError, match="DASHBOARD_READER_CONFIRM"),
    ):
        administration.execute("ensure")

    target.assert_not_called()


def test_apply_secret_passes_values_only_through_stdin() -> None:
    target = object.__new__(administration.Target)
    target.namespace = "qa-svc"
    target.run = Mock(return_value="")
    password = "a" * 64

    target.apply_secret(
        "qa-dashboard-elasticsearch",
        {"username": administration.IDENTITY, "password": password},
        "elasticsearch-reader",
    )

    arguments = target.run.call_args.args
    manifest = json.loads(target.run.call_args.kwargs["input_text"])
    assert password not in arguments
    assert manifest["stringData"] == {
        "username": administration.IDENTITY,
        "password": password,
    }
    assert manifest["metadata"]["labels"]["insurance-hub.io/owner"] == "issue-287"


def test_optional_credentials_require_owned_exact_key_secret() -> None:
    target = object.__new__(administration.Target)
    target.workload_secret = "qa-dashboard-elasticsearch"
    target.namespace = "qa-svc"
    password = "b" * 64
    value = {
        "kind": "Secret",
        "metadata": {
            "name": target.workload_secret,
            "namespace": target.namespace,
            "labels": {
                "insurance-hub.io/owner": "issue-287",
                "app.kubernetes.io/component": "elasticsearch-reader",
            },
        },
        "type": "Opaque",
        "data": {
            "username": base64.b64encode(administration.IDENTITY.encode()).decode(),
            "password": base64.b64encode(password.encode()).decode(),
        },
    }
    target.json = Mock(return_value=value)

    assert target.optional_credentials() == (administration.IDENTITY, password)

    value["data"]["extra"] = base64.b64encode(b"unexpected").decode()
    with pytest.raises(administration.AdministrationError, match="identity or keys"):
        target.optional_credentials()


def test_ensure_creates_exact_identity_and_secret_then_verifies() -> None:
    target = SimpleNamespace(
        workload_secret="local-dev-dashboard-elasticsearch",
        optional_credentials=Mock(return_value=None),
        ensure_ca_secret=Mock(),
        apply_secret=Mock(),
    )
    admin = Mock()
    password = "c" * 64
    with (
        patch.object(administration, "inspect_identity", return_value=(None, None)),
        patch.object(administration, "new_password", return_value=password),
        patch.object(administration, "put_identity") as put_identity,
        patch.object(administration, "verify_reader") as verify_reader,
    ):
        administration.ensure_reader(target, admin, 19200, b"")

    target.ensure_ca_secret.assert_called_once_with(b"")
    put_identity.assert_called_once_with(admin, password)
    target.apply_secret.assert_called_once_with(
        target.workload_secret,
        {"username": administration.IDENTITY, "password": password},
        "elasticsearch-reader",
    )
    verify_reader.assert_called_once_with(target, 19200, b"", (administration.IDENTITY, password))


def test_ensure_rejects_identity_without_recoverable_secret() -> None:
    target = SimpleNamespace(optional_credentials=Mock(return_value=None))
    with (
        patch.object(
            administration,
            "inspect_identity",
            return_value=({"metadata": {"owner": "issue-287"}}, None),
        ),
        pytest.raises(administration.AdministrationError, match="explicit rotation recovery"),
    ):
        administration.ensure_reader(target, Mock(), 19200, b"")


def test_rotation_restores_previous_identity_after_secret_apply_failure() -> None:
    previous = (administration.IDENTITY, "d" * 64)
    replacement = "e" * 64
    target = SimpleNamespace(
        workload_secret="qa-dashboard-elasticsearch",
        optional_credentials=Mock(return_value=previous),
        ensure_ca_secret=Mock(),
        apply_secret=Mock(
            side_effect=[administration.AdministrationError("synthetic apply failure"), None]
        ),
    )
    admin = Mock()
    with (
        patch.object(administration, "inspect_identity", return_value=(None, None)),
        patch.object(administration, "new_password", return_value=replacement),
        patch.object(administration, "put_identity") as put_identity,
        pytest.raises(administration.AdministrationError, match="synthetic apply failure"),
    ):
        administration.rotate_reader(target, admin, 19200, b"ca")

    assert put_identity.call_args_list == [call(admin, replacement), call(admin, previous[1])]
    assert target.apply_secret.call_args_list[-1] == call(
        target.workload_secret,
        {"username": administration.IDENTITY, "password": previous[1]},
        "elasticsearch-reader",
    )


def effective_privileges() -> dict[str, object]:
    role = administration.accepted_role()
    role.pop("metadata")
    return role


def test_effective_privileges_require_exact_index_only_authority() -> None:
    client = Mock()
    client.request.return_value = (200, effective_privileges())

    administration.verify_effective_privileges(client)

    broadened = effective_privileges()
    broadened["cluster"] = ["monitor"]
    client.request.return_value = (200, broadened)
    with pytest.raises(administration.AdministrationError, match="effective privileges differ"):
        administration.verify_effective_privileges(client)


def test_stored_role_allows_only_elasticsearch_transient_metadata() -> None:
    role = administration.accepted_role()
    role["transient_metadata"] = {"enabled": True}

    administration.validate_role(role)

    role["cluster"] = ["monitor"]
    with pytest.raises(administration.AdministrationError, match="reader role differs"):
        administration.validate_role(role)


def test_index_inspection_accepts_only_the_total_sales_mapping() -> None:
    client = Mock()
    mapping = {
        administration.INDEX: {
            "mappings": {
                "properties": {
                    "productCode": {
                        "type": "text",
                        "fields": {"keyword": {"type": "keyword"}},
                    },
                    "from": {"type": "date"},
                    "totalPremium": {"type": "float"},
                }
            }
        }
    }
    client.request.return_value = (200, mapping)

    administration.inspect_index(client)

    mapping[administration.INDEX]["mappings"]["properties"]["totalPremium"] = {"type": "keyword"}
    with pytest.raises(administration.AdministrationError, match="mapping differs"):
        administration.inspect_index(client)


def test_qa_ca_secret_must_equal_the_eck_public_ca() -> None:
    target = object.__new__(administration.Target)
    target.workload_ca_secret = "qa-dashboard-elasticsearch-ca"
    target.namespace = "qa-svc"
    target.json = Mock(
        return_value={
            "kind": "Secret",
            "metadata": {
                "name": target.workload_ca_secret,
                "namespace": target.namespace,
                "labels": {
                    "insurance-hub.io/owner": "issue-287",
                    "app.kubernetes.io/component": "elasticsearch-trust",
                },
            },
            "type": "Opaque",
            "data": {"ca.crt": base64.b64encode(b"other-ca").decode()},
        }
    )

    with pytest.raises(administration.AdministrationError, match="differs from ECK"):
        target.verify_ca_secret(b"accepted-ca")


def test_qa_ca_ensure_rejects_foreign_secret_before_apply() -> None:
    target = object.__new__(administration.Target)
    target.workload_ca_secret = "qa-dashboard-elasticsearch-ca"
    target.namespace = "qa-svc"
    target.json = Mock(
        return_value={
            "kind": "Secret",
            "metadata": {
                "name": target.workload_ca_secret,
                "namespace": target.namespace,
                "labels": {
                    "insurance-hub.io/owner": "another-owner",
                    "app.kubernetes.io/component": "elasticsearch-trust",
                },
            },
            "type": "Opaque",
            "data": {"ca.crt": base64.b64encode(b"accepted-ca").decode()},
        }
    )
    target.apply_secret = Mock()

    with pytest.raises(administration.AdministrationError, match="identity or keys"):
        target.ensure_ca_secret(b"accepted-ca")

    target.apply_secret.assert_not_called()


def test_command_failure_does_not_surface_captured_secret_material() -> None:
    error = CommandError("failed", stdout="secret-value", stderr="secret-value")
    with (
        patch.object(administration, "run_command", side_effect=error),
        pytest.raises(administration.AdministrationError) as captured,
    ):
        administration.command(["kubectl", "apply", "-f", "-"], input_text="secret-value")

    assert "secret-value" not in str(captured.value)
