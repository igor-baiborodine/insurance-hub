"""Policy Search deployment guards; unit cases never contact a cluster."""

from __future__ import annotations

import os
import signal
from copy import deepcopy
from unittest.mock import Mock, patch

import policy_search_deployment_checks as checks
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


def rendered_items() -> list[dict]:
    return checks.documents(checks.render("local-dev"))


def test_target_rejects_wrong_coordinates_before_cluster_access() -> None:
    for name, value in (("DEPLOY_ENV", "unknown"), ("DEPLOY_CONTEXT", "qa-insurance-hub")):
        environment = target_environment()
        environment[name] = value
        with (
            patch.dict(os.environ, environment, clear=True),
            patch.object(checks, "run") as command,
        ):
            with pytest.raises(checks.DeploymentError):
                checks.Target()
            command.assert_not_called()


def test_qa_mutation_is_rejected_before_coordinates() -> None:
    with patch.dict(os.environ, target_environment("qa"), clear=True):
        fake = Mock()
        fake.configuration = {"mode": "flux"}
        with patch.object(checks, "Target", return_value=fake):
            for operation in ("image-load", "deploy", "stop"):
                with pytest.raises(lifecycle.DeploymentError, match="forbidden"):
                    lifecycle.execute(checks, [operation])
        fake.coordinates.assert_not_called()


def test_render_rejects_java_ownership_and_business_dependencies() -> None:
    items = rendered_items()
    checks.validate_render(
        "local-dev", "\n---\n".join(checks.yaml.safe_dump(item) for item in items)
    )
    java = deepcopy(items)
    java[0]["metadata"]["name"] = "local-dev-policy-search-api-legacy"
    with pytest.raises(checks.DeploymentError, match="Java"):
        checks.validate_render(
            "local-dev", "\n---\n".join(checks.yaml.safe_dump(item) for item in java)
        )
    dependency = deepcopy(items)
    dependency[0].setdefault("data", {})["ELASTICSEARCH_URL"] = "http://synthetic"
    with pytest.raises(checks.DeploymentError, match="dependency"):
        checks.validate_render(
            "local-dev", "\n---\n".join(checks.yaml.safe_dump(item) for item in dependency)
        )


def test_render_rejects_missing_network_policy() -> None:
    items = [item for item in rendered_items() if item["kind"] != "NetworkPolicy"]
    with pytest.raises(checks.DeploymentError, match="required resource kind"):
        checks.validate_render(
            "local-dev", "\n---\n".join(checks.yaml.safe_dump(item) for item in items)
        )


@pytest.mark.parametrize(
    ("kind", "field", "value", "message"),
    [
        ("Service", "type", "NodePort", "exposure"),
        ("ConfigMap", "HTTP_ADDR", "127.0.0.1:8080", "runtime budgets"),
    ],
)
def test_render_rejects_weakened_service_or_runtime_boundary(
    kind: str, field: str, value: str, message: str
) -> None:
    items = rendered_items()
    item = next(candidate for candidate in items if candidate["kind"] == kind)
    owner = item["data"] if kind == "ConfigMap" else item["spec"]
    owner[field] = value
    with pytest.raises(checks.DeploymentError, match=message):
        checks.validate_render(
            "local-dev", "\n---\n".join(checks.yaml.safe_dump(candidate) for candidate in items)
        )


def test_render_rejects_weakened_termination_budget() -> None:
    items = rendered_items()
    deployment = next(item for item in items if item["kind"] == "Deployment")
    deployment["spec"]["template"]["spec"]["terminationGracePeriodSeconds"] = 5
    with pytest.raises(checks.DeploymentError, match="termination grace"):
        checks.validate_render(
            "local-dev", "\n---\n".join(checks.yaml.safe_dump(item) for item in items)
        )


def test_image_source_guard_rejects_mismatched_revision() -> None:
    target = Mock()
    with (
        patch.dict(os.environ, {"DOCKER": "docker"}, clear=True),
        patch.object(
            checks,
            "run",
            return_value='[{"Config":{"Labels":{"org.opencontainers.image.revision":"ffffffffffffffffffffffffffffffffffffffff"}}}]',
        ),
    ):
        with pytest.raises(checks.DeploymentError, match="source revision"):
            checks.secret_and_image(
                target, "insurance-hub-policy-search-api:1.0.0-local.123456789abc"
            )


@pytest.mark.parametrize(
    ("image", "accepted"),
    [
        ("ghcr.io/igor-baiborodine/insurance-hub/policy-search-api:1.27.305", True),
        ("ghcr.io/igor-baiborodine/insurance-hub/policy-search-api:2.0.0", False),
        ("ghcr.io/igor-baiborodine/insurance-hub/policy-search-api:1.2.3-rc.1", False),
        ("ghcr.io/igor-baiborodine/insurance-hub/policy-search-api:latest", False),
    ],
)
def test_qa_render_image_guard_accepts_release_owner_semver(image: str, accepted: bool) -> None:
    # when
    actual = checks.staged_image_matches("qa", image)

    # then
    assert actual is accepted


def test_live_state_accepts_api_server_normalized_resources() -> None:
    # given API server output with omitted empty lists and explicit defaults.
    items = rendered_items()
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    policy = next(item for item in items if item["kind"] == "NetworkPolicy")
    actual[("networkpolicy", policy["metadata"]["name"])]["spec"].pop("egress")
    deployment = next(item for item in items if item["kind"] == "Deployment")
    live_pod = actual[("deployment", deployment["metadata"]["name"])]["spec"]["template"]["spec"]
    live_pod["serviceAccountName"] = "default"
    for probe_name in ("startupProbe", "livenessProbe", "readinessProbe"):
        live_pod["containers"][0][probe_name]["successThreshold"] = 1
    target = object.__new__(checks.Target)
    target.deployment = deployment["metadata"]["name"]
    target.configuration = {"mode": "direct"}
    target.json = Mock(side_effect=lambda _, kind, name, _ignore: actual[(kind, name)])

    # when live state is compared to the rendered contract.
    with patch.dict(os.environ, {}, clear=True):
        # then normalization preserves semantic equality.
        target.live(items, require_deployment=True)

        # given a security-relevant selector differs after normalization.
        actual[("networkpolicy", policy["metadata"]["name"])]["spec"]["ingress"][0]["from"][0][
            "podSelector"
        ]["matchLabels"][checks.BUSINESS_CLIENT_LABEL] = "false"

        # when / then the live-state guard still rejects the drift.
        with pytest.raises(checks.DeploymentError, match="differs from rendered policy"):
            target.live(items, require_deployment=True)


def test_live_state_rejects_stale_deployment_after_rollout() -> None:
    items = rendered_items()
    deployment = next(item for item in items if item["kind"] == "Deployment")
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    actual[("deployment", deployment["metadata"]["name"])]["spec"]["template"]["spec"][
        "containers"
    ][0]["image"] = "insurance-hub-policy-search-api:1.0.0-local.ffffffffffff"
    target = object.__new__(checks.Target)
    target.deployment = deployment["metadata"]["name"]
    target.configuration = {"mode": "direct"}
    target.json = Mock(side_effect=lambda _, kind, name, _ignore: actual[(kind, name)])

    with (
        patch.dict(os.environ, {}, clear=True),
        pytest.raises(checks.DeploymentError, match="image differs"),
    ):
        target.live(items, require_deployment=True)


def test_live_state_accepts_declared_predecessor_only_before_rollout() -> None:
    items = rendered_items()
    deployment = next(item for item in items if item["kind"] == "Deployment")
    config = next(item for item in items if item["kind"] == "ConfigMap")
    desired_image = "insurance-hub-policy-search-api:1.0.0-local.123456789abc"
    previous_image = "insurance-hub-policy-search-api:1.0.0-local.abcdef123456"
    deployment["metadata"].setdefault("labels", {})["app.kubernetes.io/version"] = (
        "git-123456789abc"
    )
    deployment["spec"]["template"]["metadata"].setdefault("labels", {})[
        "app.kubernetes.io/version"
    ] = "git-123456789abc"
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = desired_image
    config["data"]["SERVICE_VERSION"] = "git-123456789abc"
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    live_deployment = actual[("deployment", deployment["metadata"]["name"])]
    live_deployment["metadata"]["labels"]["app.kubernetes.io/version"] = "git-abcdef123456"
    live_deployment["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/version"] = (
        "git-abcdef123456"
    )
    live_deployment["spec"]["template"]["spec"]["containers"][0]["image"] = previous_image
    actual[("configmap", config["metadata"]["name"])]["data"]["SERVICE_VERSION"] = (
        "git-abcdef123456"
    )
    target = object.__new__(checks.Target)
    target.deployment = deployment["metadata"]["name"]
    target.configuration = {"mode": "direct"}
    target.json = Mock(side_effect=lambda _, kind, name, _ignore: actual[(kind, name)])

    with patch.dict(os.environ, {"DEPLOY_PREVIOUS_IMAGE_REF": previous_image}, clear=True):
        target.live(items, require_deployment=False)
        with pytest.raises(checks.DeploymentError, match="differs from rendered configuration"):
            target.live(items, require_deployment=True)


def test_deploy_rejects_foreign_same_name_resource_before_apply() -> None:
    items = rendered_items()
    deployment = next(item for item in items if item["kind"] == "Deployment")
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    actual[("deployment", deployment["metadata"]["name"])]["metadata"]["labels"][
        "app.kubernetes.io/instance"
    ] = "foreign-policy-search"
    target = object.__new__(checks.Target)
    target.service = "policy-search"
    target.environment = "local-dev"
    target.deployment = deployment["metadata"]["name"]
    target.configuration = {"mode": "direct"}
    target.coordinates = Mock()
    target.rendered = Mock(return_value=("rendered", items, "image"))
    target.json = Mock(side_effect=lambda _, kind, name, _ignore: actual[(kind, name)])

    with (
        patch.dict(os.environ, {}, clear=True),
        patch.object(checks, "Target", return_value=target),
        patch.object(checks, "check_render"),
        patch.object(checks, "secret_and_image"),
        patch.object(lifecycle, "apply") as apply,
        pytest.raises(checks.DeploymentError, match="identity differs"),
    ):
        lifecycle.execute(checks, ["deploy"])
    apply.assert_not_called()


def test_cli_preserves_interrupt_status() -> None:
    for requested, expected in ((signal.SIGINT, 130), (signal.SIGTERM, 143)):

        def interrupted(_argv=None, selected=requested):
            os.kill(os.getpid(), selected)
            return 0

        with patch.object(deployment, "main", interrupted):
            assert deployment.cli([]) == expected
