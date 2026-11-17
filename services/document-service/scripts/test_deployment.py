"""Document deployment guard tests; unit cases never contact a cluster."""

from __future__ import annotations

import json
import os
import re
import signal
from copy import deepcopy
from types import SimpleNamespace
from unittest.mock import Mock, patch

import document_deployment_checks as checks
import pytest

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


def test_qa_mutation_is_rejected_before_coordinates() -> None:
    # given
    with patch.dict(os.environ, target_environment("qa"), clear=True):
        fake = Mock()
        fake.configuration = {"mode": "flux"}

        # when / then
        with patch.object(checks, "Target", return_value=fake):
            for operation in ("image-load", "deploy", "stop"):
                with pytest.raises(lifecycle.DeploymentError, match="forbidden"):
                    lifecycle.execute(checks, [operation])
        fake.coordinates.assert_not_called()


def test_unknown_operation_fails_before_target_selection() -> None:
    # when / then
    with patch.object(checks, "Target") as target:
        with pytest.raises(lifecycle.DeploymentError, match="unsupported operation"):
            lifecycle.execute(checks, ["initialize"])
        target.assert_not_called()


def test_document_has_no_administration_or_initialization_owner() -> None:
    # given
    target = Mock()

    # when / then
    for owner in (
        checks.administration_snapshot,
        checks.initialization_check,
        checks.initialization_complete,
    ):
        with pytest.raises(checks.DeploymentError, match="no (administration|initialization)"):
            owner(target)


def test_secret_is_rejected_before_image_inspection() -> None:
    # given
    target = SimpleNamespace(configuration={"mode": "direct"})

    # when / then
    with (
        patch.dict(os.environ, {"DEPLOY_SECRET": "invented"}, clear=True),
        patch.object(checks, "run") as run,
        pytest.raises(checks.DeploymentError, match="no deployment Secret"),
    ):
        checks.secret_and_image(target, "insurance-hub-document-api:1.0.0-local." + "a" * 12)
    run.assert_not_called()


def test_local_image_requires_exact_source_identity() -> None:
    # given
    image = "insurance-hub-document-api:1.0.0-local." + "a" * 12
    target = SimpleNamespace(configuration={"mode": "direct"})
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


def test_runbook_deploys_checked_in_local_candidate() -> None:
    # given
    items = checks.validate_render("local-dev", checks.render("local-dev"))
    deployment = checks.resource(items, "Deployment", "local-dev-document-api")
    expected_image = deployment["spec"]["template"]["spec"]["containers"][0]["image"]
    runbook = (checks.ROOT / "services/document-service/deployment-runbook.md").read_text()

    # when
    references = re.findall(r'^export DEPLOY_IMAGE_REF="([^"]+)"$', runbook, re.MULTILINE)

    # then
    assert references == [expected_image]


@pytest.mark.parametrize(
    "image",
    [
        "insurance-hub-document-api:1.0.0-local.aaaaaaaaaaaa",
        "ghcr.io/igor-baiborodine/insurance-hub/document-api:1.2.3",
    ],
)
def test_image_shape_is_bound_to_target_ownership(image: str) -> None:
    # given
    local = image.startswith("insurance-hub-")
    target = SimpleNamespace(configuration={"mode": "direct" if local else "flux"})
    inspected = json.dumps(
        [{"Config": {"Labels": {"org.opencontainers.image.revision": "a" * 40}}}]
    )

    # when / then
    with (
        patch.dict(os.environ, {}, clear=True),
        patch.object(checks, "run", return_value=inspected),
    ):
        checks.secret_and_image(target, image)


def test_render_rejects_admin_credentials_incomplete_inventory_and_placeholder_version() -> None:
    # when / then
    with pytest.raises(checks.DeploymentError, match="forbidden"):
        checks.validate_render("local-dev", "kind: Secret\nmetadata:\n  name: migrator-creds\n")
    with pytest.raises(checks.DeploymentError, match="resource kinds"):
        checks.validate_render(
            "local-dev",
            "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: local-dev-document-config\n",
        )
    rendered = checks.render("local-dev")
    with pytest.raises(checks.DeploymentError, match="runtime version"):
        checks.validate_render(
            "local-dev", re.sub(r"git-[0-9a-f]{12}", "git-000000000000", rendered)
        )


def test_render_rejects_weakened_target_security() -> None:
    # given
    rendered = checks.render("local-dev")
    weakened = rendered.replace(
        "automountServiceAccountToken: false", "automountServiceAccountToken: true"
    )

    # when / then
    with pytest.raises(checks.DeploymentError, match="Pod security"):
        checks.validate_render("local-dev", weakened)


@pytest.mark.parametrize(
    "mutation",
    [
        lambda rendered: rendered.replace(
            'EVENT_WORKERS_ENABLED: "true"', 'EVENT_WORKERS_ENABLED: "false"'
        ),
        lambda rendered: rendered.replace(
            "KAFKA_GROUP: documents-service", "KAFKA_GROUP: document-service"
        ),
        lambda rendered: rendered.replace(
            "name: qa-postgres-document-writer-creds",
            "name: qa-postgres-document-reader-creds",
        ),
        lambda rendered: rendered.replace(
            "http://qa-jsreport.qa-svc.svc.cluster.local:5488",
            "http://foreign-jsreport.qa-svc.svc.cluster.local:5488",
        ),
        lambda rendered: rendered.replace(
            'insurance-hub.io/document-business-client: "true"',
            'insurance-hub.io/document-qualification-client: "true"',
        ),
    ],
)
def test_qa_render_rejects_wrong_transfer_authority(mutation) -> None:
    # QA final state requires the same single worker and writer authority as local-dev.
    rendered = checks.render("qa")

    with pytest.raises(checks.DeploymentError):
        checks.validate_render("qa", mutation(rendered))


@pytest.mark.parametrize(
    "mutation",
    [
        lambda rendered: rendered.replace(
            'EVENT_WORKERS_ENABLED: "true"', 'EVENT_WORKERS_ENABLED: "false"'
        ),
        lambda rendered: rendered.replace(
            "KAFKA_GROUP: documents-service", "KAFKA_GROUP: document-service"
        ),
        lambda rendered: rendered.replace(
            "name: local-dev-postgres-document-writer-creds",
            "name: local-dev-postgres-document-reader-creds",
        ),
        lambda rendered: rendered.replace(
            "http://local-dev-jsreport.local-dev-all.svc.cluster.local:5488",
            "http://foreign-jsreport.local-dev-all.svc.cluster.local:5488",
        ),
        lambda rendered: rendered.replace(
            'insurance-hub.io/document-business-client: "true"',
            'insurance-hub.io/document-qualification-client: "true"',
        ),
    ],
)
def test_local_render_rejects_wrong_transfer_authority(mutation) -> None:
    # given
    rendered = checks.render("local-dev")

    # when / then
    with pytest.raises(checks.DeploymentError):
        checks.validate_render("local-dev", mutation(rendered))


def test_previous_render_accepts_only_valid_final_worker_authority() -> None:
    # given
    rendered = checks.render("local-dev")

    # when
    resources = checks.previous_render_resources("local-dev", rendered)

    # then
    assert len(resources) == 5

    # when / then
    with pytest.raises(checks.DeploymentError):
        checks.previous_render_resources(
            "local-dev",
            rendered.replace("KAFKA_GROUP: documents-service", "KAFKA_GROUP: foreign-group"),
        )


def test_qa_aggregate_requires_go_only_document_ownership() -> None:
    # when / then
    checks.validate_qa_aggregate(["./document", "./product"])


@pytest.mark.parametrize(
    ("resources", "message"),
    [
        (["./product"], "Go Document"),
        (["./document", "./document"], "Go Document"),
        (["./document", "./document/legacy", "./product"], "Java Document"),
        ([{"path": "./document"}], "resources are invalid"),
    ],
)
def test_qa_aggregate_rejects_ambiguous_document_ownership(
    resources: list[object], message: str
) -> None:
    # when / then
    with pytest.raises(checks.DeploymentError, match=message):
        checks.validate_qa_aggregate(resources)


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
def test_legacy_document_manifests_remain_renderable_and_quiesced(environment: str) -> None:
    # given
    overlay = checks.ROOT / f"k8s/overlays/{environment}/svc/document/legacy"
    rendered = checks.run([os.environ.get("KUBECTL", "kubectl"), "kustomize", str(overlay)])
    deployment = checks.resource(
        checks.documents(rendered), "Deployment", f"{environment}-document-api-legacy"
    )

    # when
    checks.validate_legacy_identity(environment)

    # then
    assert deployment["spec"]["replicas"] == 0

    # when / then
    active_render = rendered.replace("replicas: 0", "replicas: 1", 1)
    with (
        patch.object(checks, "run", return_value=active_render),
        pytest.raises(checks.DeploymentError, match="must remain quiesced"),
    ):
        checks.validate_legacy_identity(environment)

    # when / then
    incomplete_render = "---\n".join(
        document for document in rendered.split("---\n") if "kind: Service\n" not in document
    )
    with (
        patch.object(checks, "run", return_value=incomplete_render),
        pytest.raises(checks.DeploymentError, match="Service"),
    ):
        checks.validate_legacy_identity(environment)


def rendered_resources() -> list[dict[str, object]]:
    return checks.validate_render("local-dev", checks.render("local-dev"))


def target_with_live_resources(
    items: list[dict[str, object]],
) -> tuple[checks.Target, dict[tuple[str, str], dict[str, object]]]:
    actual = {
        (str(item["kind"]).lower(), str(item["metadata"]["name"])): deepcopy(item) for item in items
    }
    target = object.__new__(checks.Target)
    target.environment = "local-dev"
    target.namespace = "local-dev-all"
    target.deployment = "local-dev-document-api"
    target.configuration = {"mode": "direct"}
    target.json = Mock(side_effect=lambda _get, kind, name, _ignore: actual[(kind, name)])
    return target, actual


def use_previous_identity(
    actual: dict[tuple[str, str], dict[str, object]], previous_image: str
) -> None:
    previous_version = "git-" + previous_image.rsplit(".", 1)[1]
    actual[("configmap", "local-dev-document-config")]["data"]["SERVICE_VERSION"] = previous_version
    deployment = actual[("deployment", "local-dev-document-api")]
    deployment["metadata"]["labels"]["app.kubernetes.io/version"] = previous_version
    deployment["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/version"] = (
        previous_version
    )
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = previous_image


def test_live_state_rejects_wrong_resource_identity() -> None:
    # given
    items = rendered_resources()
    target, actual = target_with_live_resources(items)
    actual[("configmap", "local-dev-document-config")]["metadata"]["name"] = "foreign"

    # when / then
    with pytest.raises(checks.DeploymentError, match="identity differs"):
        target.live(items, require_deployment=True)


def test_preflight_accepts_declared_previous_document_identity() -> None:
    # given
    items = rendered_resources()
    target, actual = target_with_live_resources(items)
    previous_image = "insurance-hub-document-api:1.0.0-local.abcdef123456"
    use_previous_identity(actual, previous_image)

    # when / then
    with (
        patch.dict(os.environ, {"DEPLOY_PREVIOUS_IMAGE_REF": previous_image}, clear=True),
        patch.object(checks, "predecessor_resources", return_value={}),
    ):
        target.live(items, require_deployment=False)


def test_preflight_accepts_source_verified_previous_configuration() -> None:
    # given
    items = rendered_resources()
    previous = deepcopy(items)
    previous_config = next(item for item in previous if item["kind"] == "ConfigMap")
    previous_config["data"]["REQUEST_TIMEOUT"] = "8s"
    target, actual = target_with_live_resources(previous)
    previous_image = "insurance-hub-document-api:1.0.0-local.abcdef123456"
    use_previous_identity(actual, previous_image)
    indexed = {(str(item["kind"]), str(item["metadata"]["name"])): item for item in previous}

    # when / then
    with (
        patch.dict(os.environ, {"DEPLOY_PREVIOUS_IMAGE_REF": previous_image}, clear=True),
        patch.object(checks, "predecessor_resources", return_value=indexed),
    ):
        target.live(items, require_deployment=False)


def test_preflight_accepts_previous_image_with_final_configuration() -> None:
    # given
    items = rendered_resources()
    previous = deepcopy(items)
    previous_config = next(item for item in previous if item["kind"] == "ConfigMap")
    previous_config["data"]["EVENT_WORKERS_ENABLED"] = "false"
    target, actual = target_with_live_resources(items)
    previous_image = "insurance-hub-document-api:1.4.0-local.abcdef123456"
    use_previous_identity(actual, previous_image)
    indexed = {(str(item["kind"]), str(item["metadata"]["name"])): item for item in previous}

    # when / then
    with (
        patch.dict(os.environ, {"DEPLOY_PREVIOUS_IMAGE_REF": previous_image}, clear=True),
        patch.object(checks, "predecessor_resources", return_value=indexed),
    ):
        target.live(items, require_deployment=False)


def test_final_status_rejects_declared_previous_document_identity() -> None:
    # given
    items = rendered_resources()
    target, actual = target_with_live_resources(items)
    previous_image = "insurance-hub-document-api:1.0.0-local.abcdef123456"
    use_previous_identity(actual, previous_image)

    # when / then
    with (
        patch.dict(os.environ, {"DEPLOY_PREVIOUS_IMAGE_REF": previous_image}, clear=True),
        pytest.raises(checks.DeploymentError, match="ConfigMap"),
    ):
        target.live(items, require_deployment=True)


@pytest.mark.parametrize(
    "previous_image",
    [
        "insurance-hub-document-api:latest",
        "insurance-hub-dashboard-api:1.0.0-local.abcdef123456",
    ],
)
def test_live_state_rejects_invalid_predecessor(previous_image: str) -> None:
    # given
    items = rendered_resources()
    target, _actual = target_with_live_resources(items)

    # when / then
    with (
        patch.dict(os.environ, {"DEPLOY_PREVIOUS_IMAGE_REF": previous_image}, clear=True),
        pytest.raises(checks.DeploymentError, match="valid only for a direct local rollout"),
    ):
        target.live(items, require_deployment=False)


@pytest.mark.parametrize("exit_code", [7, 28])
def test_untrusted_access_probe_accepts_network_denial_and_cleans_up(exit_code: int) -> None:
    # given
    target = Mock()
    target.business = "local-dev-document-api"
    target.management = "local-dev-document-api-management"
    terminated = {
        "status": {
            "containerStatuses": [
                {"state": {"terminated": {"exitCode": exit_code}}},
            ]
        }
    }
    target.json.side_effect = [{}, terminated, {}, terminated, {}, terminated]

    # when
    checks.verify_untrusted_access_denied(target)

    # then
    assert target.kubectl.call_count == 6
    assert target.kubectl.call_args_list[-1].args == (
        "delete",
        "pod",
        "document-untrusted-management",
        "--ignore-not-found",
        "--wait=true",
        "--timeout=30s",
    )
    assert target.kubectl.call_args_list[-1].kwargs == {"timeout": 40}


def test_untrusted_access_probe_rejects_unrelated_failure_and_cleans_up() -> None:
    # given
    target = Mock()
    target.business = "local-dev-document-api"
    target.management = "local-dev-document-api-management"
    target.json.side_effect = [
        {},
        {"status": {"containerStatuses": [{"state": {"terminated": {"exitCode": 6}}}]}},
    ]

    # when / then
    with pytest.raises(checks.DeploymentError, match="other than denied network access"):
        checks.verify_untrusted_access_denied(target)

    # then
    assert target.kubectl.call_args_list[-1].args[:3] == (
        "delete",
        "pod",
        "document-untrusted-http",
    )


def test_untrusted_access_probe_never_deletes_preexisting_pod() -> None:
    # given
    target = Mock()
    target.json.return_value = {"metadata": {"name": "document-untrusted-http"}}

    # when / then
    with pytest.raises(checks.DeploymentError, match="unowned"):
        checks.verify_untrusted_endpoint_denied(
            target, "document-untrusted-http", "http://local-dev-document-api:80/documents/SYN-362"
        )

    # then
    target.kubectl.assert_not_called()


@pytest.mark.parametrize(
    ("environment", "business", "expected_labels"),
    [
        (
            "local-dev",
            "local-dev-document-api",
            (
                "app.kubernetes.io/name=agent-portal-gateway-legacy,"
                "app.kubernetes.io/instance=insurance-hub-agent-portal-gateway-legacy,"
                "app.kubernetes.io/component=gateway,environment=local-dev,"
                "insurance-hub.io/document-business-client=true"
            ),
        ),
        (
            "qa",
            "qa-document-api",
            (
                "app.kubernetes.io/name=agent-portal-gateway-legacy,"
                "app.kubernetes.io/instance=insurance-hub-agent-portal-gateway-legacy,"
                "app.kubernetes.io/component=gateway,environment=qa,"
                "insurance-hub.io/document-business-client=true"
            ),
        ),
    ],
)
@pytest.mark.parametrize(
    "payload",
    [
        {"documents": []},
        {
            "documents": [
                {"policyNumber": "SYN-362", "content": "JVBERi0zNjI="},
                {"policyNumber": "SYN-362", "content": "JVBERi0zNjI="},
                {"policyNumber": "SYN-362", "content": "JVBERi1PQkpFQ1QtMzYy"},
            ]
        },
    ],
)
def test_qualified_access_probe_accepts_empty_or_owned_fixture_and_waits_for_policy(
    environment: str,
    business: str,
    expected_labels: str,
    payload: dict,
) -> None:
    # given
    target = Mock()
    target.environment = environment
    target.business = business
    target.json.side_effect = [
        {},
        {"status": {"containerStatuses": [{"state": {"terminated": {"exitCode": 0}}}]}},
    ]
    target.kubectl.side_effect = [
        None,
        json.dumps(payload),
        None,
    ]

    # when
    checks.verify_qualification_access(target)

    # then
    assert target.kubectl.call_args_list[0].args[4] == f"--labels={expected_labels}"
    assert target.kubectl.call_args_list[0].args[-5:] == (
        "/bin/sh",
        "-ec",
        (
            f"sleep {checks.NETWORK_POLICY_CONVERGENCE_SECONDS}\n"
            'exec curl --fail --silent --max-time 5 "$1"'
        ),
        "document-qualified-http",
        f"http://{business}:80/documents/SYN-362",
    )
    assert target.kubectl.call_args_list[-1].args[:3] == (
        "delete",
        "pod",
        "document-qualified-http",
    )


@pytest.mark.parametrize(
    "payload",
    [
        {},
        {"documents": "invalid"},
        {"documents": [{"policyNumber": "FOREIGN", "content": "JVBERi0zNjI="}]},
    ],
)
def test_shared_lookup_rejects_malformed_or_foreign_results(payload: object) -> None:
    # when
    accepted = checks.accepted_shared_lookup(payload)

    # then
    assert not accepted


def test_deployment_adapter_maps_interrupt_to_shell_status(monkeypatch) -> None:
    # given
    def interrupted(_arguments=None):
        os.kill(os.getpid(), signal.SIGINT)
        return 0

    monkeypatch.setattr(deployment, "main", interrupted)

    # when
    status = deployment.cli([])

    # then
    assert status == 128 + signal.SIGINT
