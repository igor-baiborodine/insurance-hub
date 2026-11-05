"""Dashboard deployment guard tests; unit cases never contact a cluster."""

from __future__ import annotations

import json
import os
import signal
from copy import deepcopy
from types import SimpleNamespace
from unittest.mock import Mock, patch

import dashboard_deployment_checks as checks
import pytest
import yaml

import deployment
from scripts.go.service import deployment as lifecycle


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
    for target, name, value in (
        ("local-dev", "DEPLOY_ENV", "unknown"),
        ("local-dev", "DEPLOY_CONTEXT", "qa-insurance-hub"),
        ("qa", "DEPLOY_DATA_NS", "qa-data"),
    ):
        environment = target_environment(target)
        environment[name] = value
        with patch.dict(os.environ, environment, clear=True), patch.object(checks, "run") as run:
            with pytest.raises(checks.DeploymentError):
                checks.Target()
            run.assert_not_called()


def test_qa_target_uses_only_the_non_data_service_coordinate() -> None:
    with (
        patch.dict(os.environ, target_environment("qa"), clear=True),
        patch.object(checks, "run") as run,
    ):
        target = checks.Target()

    assert target.namespace == "qa-svc"
    assert target.configuration["dataNamespace"] == target.namespace
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


@pytest.mark.parametrize(
    ("environment", "key"),
    [
        ("local-dev", "PASSWORD"),
        ("qa", "TOKEN"),
        ("local-dev", "ADMINISTRATION_MODE"),
        ("qa", "DEPENDENCY_ENDPOINT"),
    ],
)
def test_render_rejects_undeclared_runtime_configuration(environment: str, key: str) -> None:
    items = checks.documents(checks.render(environment))
    config = checks.resource(items, "ConfigMap", f"{environment}-dashboard-config")
    config["data"][key] = "private-or-undeclared-value"

    with pytest.raises(checks.DeploymentError, match="runtime configuration differs"):
        checks.validate_render(environment, yaml.safe_dump_all(items))


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


def rendered_resources(environment: str = "local-dev") -> list[dict[str, object]]:
    return checks.validate_render(environment, checks.render(environment))


def target_with_live_resources(
    items: list[dict[str, object]],
) -> tuple[checks.Target, dict[tuple[str, str], dict[str, object]]]:
    actual = {
        (str(item["kind"]).lower(), str(item["metadata"]["name"])): deepcopy(item) for item in items
    }
    target = object.__new__(checks.Target)
    target.namespace = "local-dev-all"
    target.configuration = {"mode": "direct"}
    target.json = Mock(side_effect=lambda _get, kind, name, _ignore: actual[(kind, name)])
    return target, actual


def test_live_state_accepts_the_complete_rendered_contract() -> None:
    items = rendered_resources()
    target, _actual = target_with_live_resources(items)
    target.live(items, require_deployment=True)


def test_live_state_accepts_api_server_defaulted_probe_fields() -> None:
    items = rendered_resources()
    target, actual = target_with_live_resources(items)
    deployment = actual[("deployment", "local-dev-dashboard-api")]
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    for name in ("startupProbe", "livenessProbe", "readinessProbe"):
        container[name]["httpGet"]["scheme"] = "HTTP"
        container[name]["successThreshold"] = 1

    target.live(items, require_deployment=True)


@pytest.mark.parametrize(
    ("kind", "name", "mutation", "message"),
    [
        (
            "ConfigMap",
            "local-dev-dashboard-config",
            lambda item: item["data"].__setitem__("FOREIGN_SETTING", "true"),
            "rendered configuration",
        ),
        (
            "Service",
            "local-dev-dashboard-api",
            lambda item: item["spec"].__setitem__("externalIPs", ["203.0.113.10"]),
            "external exposure",
        ),
        (
            "Deployment",
            "local-dev-dashboard-api",
            lambda item: item["metadata"].__setitem__(
                "ownerReferences", [{"kind": "Foreign", "name": "owner"}]
            ),
            "rendered identity",
        ),
        (
            "Deployment",
            "local-dev-dashboard-api",
            lambda item: item["spec"]["template"]["spec"].__setitem__("hostNetwork", True),
            "Pod security settings",
        ),
        (
            "Deployment",
            "local-dev-dashboard-api",
            lambda item: item["spec"]["template"]["spec"]["containers"][0]["resources"][
                "limits"
            ].__setitem__("cpu", "500m"),
            "container resources differs",
        ),
        (
            "NetworkPolicy",
            "local-dev-dashboard-api",
            lambda item: item["spec"].__setitem__("ingress", []),
            "rendered policy",
        ),
    ],
)
def test_live_state_rejects_foreign_or_drifted_resources(
    kind: str, name: str, mutation, message: str
) -> None:
    items = rendered_resources()
    target, actual = target_with_live_resources(items)
    mutation(actual[(kind.lower(), name)])

    with pytest.raises(checks.DeploymentError, match=message):
        target.live(items, require_deployment=True)


@pytest.mark.parametrize(
    ("expected", "image_id", "pod_count", "message"),
    [
        (
            "sha256:" + "a" * 64,
            "ghcr.io/igor-baiborodine/insurance-hub/dashboard-api@sha256:" + "a" * 64,
            1,
            "",
        ),
        (
            "sha256:" + "a" * 64,
            "ghcr.io/igor-baiborodine/insurance-hub/dashboard-api@sha256:" + "b" * 64,
            1,
            "image digest differs",
        ),
        ("", "sha256:" + "a" * 64, 1, "DEPLOY_IMAGE_DIGEST is required"),
        ("sha256:short", "sha256:" + "a" * 64, 1, "DEPLOY_IMAGE_DIGEST is required"),
        ("sha256:" + "a" * 64, "sha256:" + "a" * 64, 0, "Pod inventory differs"),
    ],
)
def test_qa_live_state_requires_running_pod_digest(
    expected: str, image_id: str, pod_count: int, message: str
) -> None:
    items = rendered_resources("qa")
    deployment_item = checks.resource(items, "Deployment", "qa-dashboard-api")
    actual = {
        (str(item["kind"]).lower(), str(item["metadata"]["name"])): deepcopy(item) for item in items
    }
    pod = {
        "metadata": {"name": "qa-dashboard-api-abc"},
        "status": {
            "phase": "Running",
            "conditions": [{"type": "Ready", "status": "True"}],
            "containerStatuses": [{"name": "dashboard-service", "imageID": image_id}],
        },
    }

    def live_json(_operation, kind, *arguments):
        if kind == "pods":
            return {"items": [pod] * pod_count}
        return actual[(kind, arguments[0])]

    target = object.__new__(checks.Target)
    target.namespace = "qa-svc"
    target.deployment = deployment_item["metadata"]["name"]
    target.configuration = {"mode": "flux"}
    target.json = Mock(side_effect=live_json)
    environment = {"DEPLOY_IMAGE_DIGEST": expected} if expected else {}

    with patch.dict(os.environ, environment, clear=True):
        if message:
            with pytest.raises(checks.DeploymentError, match=message):
                target.live(items, require_deployment=True)
        else:
            target.live(items, require_deployment=True)
            target.json.assert_any_call(
                "get",
                "pods",
                "-l",
                "app.kubernetes.io/component=api,app.kubernetes.io/name=dashboard-api",
            )


def test_live_rejection_prevents_server_side_apply() -> None:
    target = Mock()
    target.configuration = {"mode": "direct"}
    target.rendered.return_value = ("rendered", [], "image")
    target.live.side_effect = checks.DeploymentError("live ConfigMap drift")
    with (
        patch.object(checks, "Target", return_value=target),
        patch.object(checks, "check_render"),
        patch.object(checks, "secret_and_image"),
        patch.object(lifecycle, "apply") as apply,
        pytest.raises(checks.DeploymentError, match="ConfigMap drift"),
    ):
        lifecycle.execute(checks, ["deploy"])
    apply.assert_not_called()


def java_resources(
    deployment_uid: str = "deployment-uid",
    service_uid: str = "service-uid",
    available: int = 1,
) -> dict[str, dict[str, object]]:
    labels = {
        "app.kubernetes.io/name": "dashboard-api-legacy",
        "app.kubernetes.io/instance": "insurance-hub-dashboard-api-legacy",
        "app.kubernetes.io/component": "api",
    }
    return {
        "deployment": {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": {
                "name": "local-dev-dashboard-api-legacy",
                "namespace": "local-dev-all",
                "uid": deployment_uid,
                "labels": labels,
            },
            "spec": {"replicas": 1},
            "status": {"availableReplicas": available, "readyReplicas": available},
        },
        "service": {
            "apiVersion": "v1",
            "kind": "Service",
            "metadata": {
                "name": "local-dev-dashboard-api-legacy",
                "namespace": "local-dev-all",
                "uid": service_uid,
                "labels": labels,
            },
            "spec": {
                "type": "ClusterIP",
                "selector": {
                    "app.kubernetes.io/name": "dashboard-api-legacy",
                    "app.kubernetes.io/component": "api",
                },
            },
        },
    }


def java_target(resources: dict[str, dict[str, object]]) -> Mock:
    target = Mock()
    target.environment = "local-dev"
    target.namespace = "local-dev-all"
    target.configuration = {"mode": "direct"}
    target.rendered.return_value = ("rendered", [], "image")
    target.json.side_effect = lambda _get, kind, _name, _ignore: resources.get(kind, {})
    return target


def test_java_snapshot_requires_declared_stable_available_resources() -> None:
    target = java_target(java_resources())
    with patch.dict(
        os.environ,
        {
            "DEPLOY_JAVA_DEPLOYMENT_UID": "deployment-uid",
            "DEPLOY_JAVA_SERVICE_UID": "service-uid",
        },
        clear=True,
    ):
        assert checks.role_snapshot(target) == {
            "deployment/local-dev-dashboard-api-legacy": "deployment-uid",
            "service/local-dev-dashboard-api-legacy": "service-uid",
        }


@pytest.mark.parametrize("defect", ["absent", "replaced", "unavailable"])
def test_invalid_java_owner_prevents_go_apply(defect: str) -> None:
    resources = java_resources()
    expected_deployment_uid = "deployment-uid"
    if defect == "absent":
        resources["deployment"] = {}
    elif defect == "replaced":
        resources["deployment"]["metadata"]["uid"] = "replacement-uid"
    else:
        resources["deployment"]["status"] = {
            "availableReplicas": 0,
            "readyReplicas": 0,
            "unavailableReplicas": 1,
        }
    target = java_target(resources)
    with (
        patch.dict(
            os.environ,
            {
                "DEPLOY_JAVA_DEPLOYMENT_UID": expected_deployment_uid,
                "DEPLOY_JAVA_SERVICE_UID": "service-uid",
            },
            clear=True,
        ),
        patch.object(checks, "Target", return_value=target),
        patch.object(checks, "check_render"),
        patch.object(checks, "secret_and_image"),
        patch.object(lifecycle, "apply") as apply,
        pytest.raises(checks.DeploymentError, match="authoritative Java"),
    ):
        lifecycle.execute(checks, ["deploy"])
    apply.assert_not_called()


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
