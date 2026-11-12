"""Document deployment guard tests; unit cases never contact a cluster."""

from __future__ import annotations

import json
import os
import signal
from types import SimpleNamespace
from unittest.mock import Mock, patch

import document_deployment_checks as checks
import pytest
from scripts.go.service import deployment as lifecycle

import deployment


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
        "DEPLOY_DATA_NS": "qa-svc",
    }


def test_target_rejects_wrong_coordinates_before_cluster_access() -> None:
    for name, value in (("DEPLOY_ENV", "unknown"), ("DEPLOY_CONTEXT", "qa-insurance-hub")):
        environment = target_environment()
        environment[name] = value
        with patch.dict(os.environ, environment, clear=True), patch.object(checks, "run") as run:
            with pytest.raises(checks.DeploymentError):
                checks.Target()
            run.assert_not_called()


def test_qa_mutation_is_rejected_before_coordinates() -> None:
    with patch.dict(os.environ, target_environment("qa"), clear=True):
        fake = Mock()
        fake.configuration = {"mode": "flux"}
        with patch.object(checks, "Target", return_value=fake):
            for operation in ("image-load", "deploy", "stop"):
                with pytest.raises(lifecycle.DeploymentError, match="forbidden"):
                    lifecycle.execute(checks, [operation])
        fake.coordinates.assert_not_called()


def test_unknown_operation_fails_before_target_selection() -> None:
    with patch.object(checks, "Target") as target:
        with pytest.raises(lifecycle.DeploymentError, match="unsupported operation"):
            lifecycle.execute(checks, ["initialize"])
        target.assert_not_called()


def test_document_has_no_administration_or_initialization_owner() -> None:
    target = Mock()
    for owner in (
        checks.administration_snapshot,
        checks.initialization_check,
        checks.initialization_complete,
    ):
        with pytest.raises(checks.DeploymentError, match="no (administration|initialization)"):
            owner(target)


def test_secret_is_rejected_before_image_inspection() -> None:
    target = SimpleNamespace(configuration={"mode": "direct"})
    with (
        patch.dict(os.environ, {"DEPLOY_SECRET": "invented"}, clear=True),
        patch.object(checks, "run") as run,
        pytest.raises(checks.DeploymentError, match="no deployment Secret"),
    ):
        checks.secret_and_image(target, "insurance-hub-document-api:1.0.0-local." + "a" * 12)
    run.assert_not_called()


def test_local_image_requires_exact_source_identity() -> None:
    image = "insurance-hub-document-api:1.0.0-local." + "a" * 12
    target = SimpleNamespace(configuration={"mode": "direct"})
    inspected = json.dumps(
        [{"Config": {"Labels": {"org.opencontainers.image.revision": "b" * 40}}}]
    )
    with (
        patch.dict(os.environ, {}, clear=True),
        patch.object(checks, "run", return_value=inspected),
        pytest.raises(checks.DeploymentError, match="source revision"),
    ):
        checks.secret_and_image(target, image)


@pytest.mark.parametrize(
    "image",
    [
        "insurance-hub-document-api:1.0.0-local.aaaaaaaaaaaa",
        "ghcr.io/igor-baiborodine/insurance-hub/document-api:1.2.3",
    ],
)
def test_image_shape_is_bound_to_target_ownership(image: str) -> None:
    local = image.startswith("insurance-hub-")
    target = SimpleNamespace(configuration={"mode": "direct" if local else "flux"})
    inspected = json.dumps(
        [{"Config": {"Labels": {"org.opencontainers.image.revision": "a" * 40}}}]
    )
    with (
        patch.dict(os.environ, {}, clear=True),
        patch.object(checks, "run", return_value=inspected),
    ):
        checks.secret_and_image(target, image)


def test_render_rejects_dependencies_incomplete_inventory_and_placeholder_version() -> None:
    with pytest.raises(checks.DeploymentError, match="forbidden"):
        checks.validate_render("local-dev", "kind: Secret\nmetadata:\n  name: database\n")
    with pytest.raises(checks.DeploymentError, match="resource kinds"):
        checks.validate_render(
            "local-dev",
            "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: local-dev-document-config\n",
        )
    rendered = checks.render("local-dev")
    with pytest.raises(checks.DeploymentError, match="runtime version"):
        checks.validate_render(
            "local-dev", rendered.replace("git-0a56b0ea8af1", "git-000000000000")
        )


def test_render_rejects_weakened_target_security() -> None:
    rendered = checks.render("local-dev")
    weakened = rendered.replace(
        "automountServiceAccountToken: false", "automountServiceAccountToken: true"
    )
    with pytest.raises(checks.DeploymentError, match="Pod security"):
        checks.validate_render("local-dev", weakened)


def test_qa_aggregate_preserves_java_and_waits_for_release_activation() -> None:
    checks.validate_qa_aggregate(["./document/legacy", "./product"])
    with pytest.raises(checks.DeploymentError, match="inactive"):
        checks.validate_qa_aggregate(["./document/legacy", "./document"])
    with pytest.raises(checks.DeploymentError, match="Java Document"):
        checks.validate_qa_aggregate(["./product"])


def test_live_state_rejects_wrong_resource_identity() -> None:
    target = object.__new__(checks.Target)
    target.namespace = "local-dev-all"
    target.json = Mock(
        return_value={
            "kind": "ConfigMap",
            "metadata": {"name": "foreign", "namespace": "local-dev-all"},
        }
    )
    desired = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": "local-dev-document-config", "namespace": "local-dev-all"},
    }
    with pytest.raises(checks.DeploymentError, match="identity differs"):
        target.live([desired], require_deployment=True)


def test_untrusted_access_probe_requires_network_timeout_and_cleans_up() -> None:
    target = Mock()
    target.management = "local-dev-document-api-management"
    target.json.side_effect = [
        {},
        {
            "status": {
                "containerStatuses": [
                    {"state": {"terminated": {"exitCode": 28}}},
                ]
            }
        },
    ]

    checks.verify_untrusted_access_denied(target)

    assert target.kubectl.call_count == 2
    assert target.kubectl.call_args_list[-1].args == (
        "delete",
        "pod",
        "document-untrusted-probe",
        "--ignore-not-found",
        "--wait=false",
    )


def test_untrusted_access_probe_never_deletes_preexisting_pod() -> None:
    target = Mock()
    target.json.return_value = {"metadata": {"name": "document-untrusted-probe"}}
    with pytest.raises(checks.DeploymentError, match="unowned"):
        checks.verify_untrusted_access_denied(target)
    target.kubectl.assert_not_called()


def test_deployment_adapter_maps_interrupt_to_shell_status(monkeypatch) -> None:
    def interrupted(_arguments=None):
        os.kill(os.getpid(), signal.SIGINT)
        return 0

    monkeypatch.setattr(deployment, "main", interrupted)
    assert deployment.cli([]) == 128 + signal.SIGINT
