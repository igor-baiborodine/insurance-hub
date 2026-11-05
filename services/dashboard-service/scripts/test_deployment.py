"""Dashboard deployment guard tests; unit cases never contact a cluster."""

from __future__ import annotations

import json
import os
import signal
from types import SimpleNamespace
from unittest.mock import Mock, patch

import dashboard_deployment_checks as checks
import pytest
import yaml
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
        "DEPLOY_DATA_NS": "qa-data",
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


def test_dashboard_has_no_administration_or_initialization_owner() -> None:
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
        checks.secret_and_image(target, "insurance-hub-dashboard-api:1.0.0-local." + "a" * 12)
    run.assert_not_called()


def test_local_image_requires_exact_source_identity() -> None:
    image = "insurance-hub-dashboard-api:1.0.0-local." + "a" * 12
    target = SimpleNamespace(configuration={"mode": "direct"})
    inspected = json.dumps(
        [{"Config": {"Labels": {"org.opencontainers.image.revision": "b" * 40}}}]
    )
    with (
        patch.dict(os.environ, {}, clear=True),
        patch.object(checks, "run", return_value=inspected),
    ):
        with pytest.raises(checks.DeploymentError, match="source revision"):
            checks.secret_and_image(target, image)


@pytest.mark.parametrize(
    "image",
    [
        "insurance-hub-dashboard-api:1.0.0-local.aaaaaaaaaaaa",
        "ghcr.io/igor-baiborodine/insurance-hub/dashboard-api:1.2.3",
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


def test_render_rejects_data_dependencies_and_incomplete_inventory() -> None:
    with pytest.raises(checks.DeploymentError, match="data dependency"):
        checks.validate_render("local-dev", "kind: Secret\nmetadata:\n  name: kafka\n")
    with pytest.raises(checks.DeploymentError, match="required resource kind"):
        checks.validate_render(
            "local-dev",
            "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: local-dev-dashboard-config\n",
        )


def test_render_requires_the_runtime_config_map_linkage() -> None:
    items = checks.documents(checks.render("local-dev"))
    deployment_item = checks.resource(items, "Deployment", "local-dev-dashboard-api")
    deployment_item["spec"]["template"]["spec"]["containers"][0].pop("envFrom")
    with pytest.raises(checks.DeploymentError, match="container security or resources"):
        checks.validate_render("local-dev", yaml.safe_dump_all(items))


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
        "metadata": {"name": "local-dev-dashboard-config", "namespace": "local-dev-all"},
    }
    with pytest.raises(checks.DeploymentError, match="identity differs"):
        target.live([desired], require_deployment=True)


def test_qa_aggregate_remains_inactive_beside_java() -> None:
    checks.validate_qa_aggregate(["./dashboard/legacy", "./product"])
    with pytest.raises(checks.DeploymentError, match="must remain inactive"):
        checks.validate_qa_aggregate(["./dashboard/legacy", "./dashboard"])
    with pytest.raises(checks.DeploymentError, match="Java Dashboard"):
        checks.validate_qa_aggregate(["./product"])


def test_deployment_adapter_maps_interrupt_to_shell_status(monkeypatch) -> None:
    def interrupted(_arguments=None):
        os.kill(os.getpid(), signal.SIGINT)
        return 0

    monkeypatch.setattr(deployment, "main", interrupted)
    assert deployment.cli(["deploy-status"]) == 130


def test_deployment_adapter_reports_guard_failure(monkeypatch, capsys) -> None:
    monkeypatch.setattr(
        deployment,
        "main",
        Mock(side_effect=checks.DeploymentError("synthetic guard")),
    )
    assert deployment.cli(["deploy-status"]) == 1
    captured = capsys.readouterr()
    assert captured.out == ""
    assert "dashboard-deployment: synthetic guard" in captured.err
