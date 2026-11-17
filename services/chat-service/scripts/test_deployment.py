"""Chat deployment guard tests; unit cases never contact a cluster."""

from __future__ import annotations

import json
import os
import signal
from copy import deepcopy
from types import SimpleNamespace
from unittest.mock import Mock, patch

import chat_deployment_checks as checks
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
        "DEPLOY_DATA_NS": "qa-data",
    }


def test_target_rejects_wrong_coordinates_before_cluster_access() -> None:
    for name, value in (("DEPLOY_ENV", "unknown"), ("DEPLOY_CONTEXT", "qa-insurance-hub")):
        # given
        environment = target_environment()
        environment[name] = value

        # when / then
        with patch.dict(os.environ, environment, clear=True), patch.object(checks, "run") as run:
            with pytest.raises(checks.DeploymentError):
                checks.Target()
            run.assert_not_called()


def test_qa_mutation_is_rejected_before_cluster_access() -> None:
    # given
    fake = Mock()
    fake.configuration = {"mode": "flux"}

    # when / then
    with patch.object(checks, "Target", return_value=fake):
        for operation in ("image-load", "deploy", "stop"):
            with pytest.raises(lifecycle.DeploymentError, match="forbidden"):
                lifecycle.execute(checks, [operation])
    fake.coordinates.assert_not_called()


@pytest.mark.parametrize("arguments", ([], ["unsupported"]))
def test_check_cli_rejects_invalid_usage_with_status_2(
    arguments: list[str], capsys: pytest.CaptureFixture[str]
) -> None:
    # given / when / then
    with pytest.raises(SystemExit) as exit_status:
        checks.main(arguments)
    assert exit_status.value.code == 2
    assert "usage:" in capsys.readouterr().err


@pytest.mark.parametrize(
    ("mutation", "message"),
    [
        (lambda value: value.replace("replicas: 1", "replicas: 2", 1), "rollout settings"),
        (
            lambda value: value.replace(
                "- containerPort: 8081\n          name: management",
                "- containerPort: 9090\n          name: grpc\n          protocol: TCP\n"
                "        - containerPort: 8081\n          name: management",
                1,
            ),
            "ports",
        ),
        (
            lambda value: value.replace(
                "automountServiceAccountToken: false", "automountServiceAccountToken: true", 1
            ),
            "Pod security",
        ),
        (
            lambda value: value.replace(
                "readOnlyRootFilesystem: true", "readOnlyRootFilesystem: false", 1
            ),
            "container security",
        ),
    ],
)
def test_render_rejects_replica_port_and_security_mutations(mutation, message: str) -> None:
    # given
    rendered = checks.render("local-dev")

    # when / then
    with pytest.raises(checks.DeploymentError, match=message):
        checks.validate_render("local-dev", mutation(rendered))


def test_render_rejects_placeholder_and_mismatched_versions() -> None:
    # given
    rendered = checks.render("local-dev")

    # when / then
    with pytest.raises(checks.DeploymentError, match="runtime version"):
        checks.validate_render(
            "local-dev", rendered.replace("git-1a5c95f7e8d5", "git-000000000000")
        )
    with pytest.raises(checks.DeploymentError, match="workload identity or version"):
        checks.validate_render(
            "local-dev",
            rendered.replace(
                "app.kubernetes.io/version: git-1a5c95f7e8d5",
                "app.kubernetes.io/version: git-aaaaaaaaaaaa",
                1,
            ),
        )


def test_qa_render_allows_only_dns_and_alloy_egress() -> None:
    # given
    rendered = checks.render("qa")
    items = checks.validate_render("qa", rendered)
    policy = checks.resource(items, "NetworkPolicy", "qa-chat-api")

    # when
    egress = policy["spec"]["egress"]

    # then
    assert egress == checks.expected_egress("qa")
    with pytest.raises(checks.DeploymentError, match="NetworkPolicy"):
        checks.validate_render("qa", rendered.replace("port: 4317", "port: 4318", 1))


def test_qa_render_accepts_canonical_future_release_tag() -> None:
    # given
    rendered = checks.render("qa")
    future_release = rendered.replace("chat-api:1.0.0", "chat-api:1.0.1", 1)
    invalid_release = rendered.replace("chat-api:1.0.0", "chat-api:2.0.0", 1)

    # when / then
    checks.validate_render("qa", future_release)
    with pytest.raises(checks.DeploymentError, match="image identity"):
        checks.validate_render("qa", invalid_release)


def test_qa_aggregate_defers_go_activation_and_requires_legacy_chat_once() -> None:
    # given
    resources = yaml.safe_load(
        (checks.ROOT / "k8s/overlays/qa/svc/kustomization.yaml").read_text()
    )["resources"]

    # when / then
    checks.validate_qa_aggregate(resources)
    with pytest.raises(checks.DeploymentError, match="inactive before release"):
        checks.validate_qa_aggregate([*resources, "./chat"])
    with pytest.raises(checks.DeploymentError, match="legacy Chat"):
        checks.validate_qa_aggregate([item for item in resources if item != "./chat/legacy"])


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
def test_vue_route_authority_must_remain_legacy(environment: str) -> None:
    # given
    legacy = checks.run(
        [
            os.environ.get("KUBECTL", "kubectl"),
            "kustomize",
            str(checks.ROOT / f"k8s/overlays/{environment}/svc/chat/legacy"),
        ]
    )
    path = checks.ROOT / f"k8s/overlays/{environment}/svc/web-vue/legacy/kustomization.yaml"
    changed = path.read_text().replace(f"{environment}-chat-api-legacy", f"{environment}-chat-api")

    # when / then
    with (
        patch.object(checks, "run", return_value=legacy),
        patch.object(checks.Path, "read_text", return_value=changed),
        pytest.raises(checks.DeploymentError, match="route authority"),
    ):
        checks.validate_legacy_and_route_authority(environment)


def test_local_image_requires_exact_source_identity() -> None:
    # given
    target = SimpleNamespace(configuration={"mode": "direct"})
    image = "insurance-hub-chat-api:1.0.0-local.1a5c95f7e8d5"
    inspected = json.dumps(
        [{"Config": {"Labels": {"org.opencontainers.image.revision": "b" * 40}}}]
    )

    # when / then
    with (
        patch.dict(os.environ, {}, clear=True),
        patch.object(checks, "run", return_value=inspected),
        pytest.raises(checks.DeploymentError, match="source revision"),
    ):
        checks.secret_and_image(target, image)


def test_live_state_rejects_foreign_same_name_resource_before_apply() -> None:
    # given
    items = checks.validate_render("local-dev", checks.render("local-dev"))
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    deployment_item = checks.resource(items, "Deployment", "local-dev-chat-api")
    actual[("deployment", "local-dev-chat-api")]["metadata"]["ownerReferences"] = [
        {"kind": "Deployment", "name": "foreign"}
    ]
    target = object.__new__(checks.Target)
    target.service = "chat"
    target.environment = "local-dev"
    target.namespace = "local-dev-all"
    target.deployment = "local-dev-chat-api"
    target.configuration = {"mode": "direct"}
    target.coordinates = Mock()
    target.rendered = Mock(
        return_value=(
            "rendered",
            items,
            deployment_item["spec"]["template"]["spec"]["containers"][0]["image"],
        )
    )
    target.json = Mock(side_effect=lambda _get, kind, name, _ignore: actual[(kind, name)])

    # when / then
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


def test_live_state_accepts_kubernetes_api_defaults() -> None:
    # given
    items = checks.validate_render("local-dev", checks.render("local-dev"))
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    deployment_item = actual[("deployment", "local-dev-chat-api")]
    deployment_item["status"] = {"availableReplicas": 1}
    pod = deployment_item["spec"]["template"]["spec"]
    pod.update(
        {
            "dnsPolicy": "ClusterFirst",
            "restartPolicy": "Always",
            "schedulerName": "default-scheduler",
            "serviceAccountName": "default",
        }
    )
    container = pod["containers"][0]
    container.update(
        {
            "terminationMessagePath": "/dev/termination-log",
            "terminationMessagePolicy": "File",
        }
    )
    for probe in ("startupProbe", "livenessProbe", "readinessProbe"):
        container[probe]["successThreshold"] = 1
    policy = actual[("networkpolicy", "local-dev-chat-api")]
    policy["spec"].pop("ingress")
    policy["spec"].pop("egress")
    target = object.__new__(checks.Target)
    target.namespace = "local-dev-all"
    target.json = Mock(side_effect=lambda _get, kind, name, _ignore: actual[(kind, name)])

    # when / then
    target.live(items, require_deployment=True)


def test_stop_scales_only_selected_deployment_and_observes_cleanup() -> None:
    # given
    target = Mock()
    target.deployment = "local-dev-chat-api"
    target.json.return_value = {"status": {"replicas": 0}}

    # when
    checks.stop(target, [])

    # then
    target.kubectl.assert_called_once_with(
        "scale", "deployment/local-dev-chat-api", "--replicas=0", "--timeout=60s"
    )
    target.json.assert_called_once_with("get", "deployment", "local-dev-chat-api")


def test_wait_for_readiness_delays_after_unready_response() -> None:
    # given
    responses = Mock(side_effect=[(503, b"not ready\n"), (200, b"ready\n")])

    # when
    with patch.object(checks, "_http", responses), patch.object(checks.time, "sleep") as sleep:
        checks.wait_for_readiness("http://127.0.0.1:8081/readyz")

    # then
    assert responses.call_count == 2
    sleep.assert_called_once_with(0.2)


def test_stop_is_bounded_when_cleanup_does_not_converge() -> None:
    # given
    target = Mock()
    target.deployment = "local-dev-chat-api"
    target.json.return_value = {"status": {"replicas": 1}}

    # when / then
    with (
        patch.object(checks.time, "monotonic", side_effect=[0, 0, 91]),
        patch.object(checks.time, "sleep"),
        pytest.raises(checks.DeploymentError, match="exceeded 90 seconds"),
    ):
        checks.stop(target, [])


def test_cli_preserves_interrupt_status() -> None:
    for requested, expected in ((signal.SIGINT, 130), (signal.SIGTERM, 143)):
        # given
        def interrupted(_argv=None, selected=requested):
            os.kill(os.getpid(), selected)
            return 0

        # when / then
        with patch.object(deployment, "main", interrupted):
            assert deployment.cli([]) == expected
