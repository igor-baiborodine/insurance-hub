"""Policy Search deployment guards; unit cases never contact a cluster."""

from __future__ import annotations  # noqa: I001

import os
import shutil
import signal
from copy import deepcopy
from unittest.mock import Mock, patch

import policy_search_deployment_checks as checks
import pytest
from scripts.go.service import deployment as lifecycle
from scripts.go.service import release as release_owner

import deployment


def test_projection_requires_an_advanced_sequence_number() -> None:
    import disposable_deployment

    expected = {"number": "SYN-230-001", "dateTo": "2026-12-31"}

    advanced, seq_no = disposable_deployment.projection_advanced(
        {"_source": expected, "_seq_no": 1}, expected, 0
    )
    stale, stale_seq_no = disposable_deployment.projection_advanced(
        {"_source": expected, "_seq_no": 1}, expected, seq_no
    )

    assert advanced and seq_no == 1
    assert not stale and stale_seq_no == seq_no


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


def rendered_items(environment: str = "local-dev") -> list[dict]:
    return checks.documents(checks.render(environment))


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


def test_render_rejects_java_ownership() -> None:
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


@pytest.mark.parametrize(("environment", "egress_count"), [("local-dev", 3), ("qa", 4)])
def test_steady_state_render_uses_canonical_resources(environment: str, egress_count: int) -> None:
    items = checks.validate_render(environment, checks.render(environment))
    config = next(item for item in items if item["kind"] == "ConfigMap")
    deployment = next(item for item in items if item["kind"] == "Deployment")
    policy = next(item for item in items if item["kind"] == "NetworkPolicy")
    pod = deployment["spec"]["template"]["spec"]
    env = {item["name"]: item for item in pod["containers"][0]["env"]}

    assert (
        config["data"]
        | {
            "ELASTICSEARCH_INDEX": "policy-views",
            "EVENT_WORKERS_ENABLED": "true",
            "KAFKA_GROUP": "policy-search-service",
            "KAFKA_REGISTERED_TOPIC": "policy-registered",
            "KAFKA_TERMINATED_TOPIC": "policy-terminated",
        }
        == config["data"]
    )
    assert env["ELASTICSEARCH_USERNAME"]["valueFrom"]["secretKeyRef"]["name"] == (
        f"{environment}-policy-search-elasticsearch"
    )
    assert env["ELASTICSEARCH_WRITER_USERNAME"]["valueFrom"]["secretKeyRef"]["name"] == (
        f"{environment}-policy-search-elasticsearch-writer"
    )
    referenced_secrets = [
        value["valueFrom"]["secretKeyRef"]["name"]
        for value in env.values()
        if "valueFrom" in value and "secretKeyRef" in value["valueFrom"]
    ] + [volume["secret"]["secretName"] for volume in pod["volumes"] if "secret" in volume]
    assert all("cutover" not in name for name in referenced_secrets)
    assert any(volume["name"] == "kafka-ca" for volume in pod["volumes"])
    assert len(policy["spec"]["egress"]) == egress_count
    assert policy["spec"]["egress"][-1] == checks.expected_kafka_egress(environment)


def test_render_entrypoint_returns_steady_state(capsys) -> None:
    with patch.dict(os.environ, {"DEPLOY_ENV": "qa", "PATH": os.environ["PATH"]}, clear=True):
        assert checks.main(["render"]) == 0

    items = checks.documents(capsys.readouterr().out)
    config = next(item for item in items if item["kind"] == "ConfigMap")
    assert config["data"]["EVENT_WORKERS_ENABLED"] == "true"
    assert config["data"]["KAFKA_GROUP"] == "policy-search-service"


def test_qa_aggregate_uses_only_go_policy_search() -> None:
    # given
    resources = ["./policy-search", "./product"]

    # when / then
    checks.validate_qa_aggregate(resources)
    with pytest.raises(checks.DeploymentError, match="legacy overlay remains active"):
        checks.validate_qa_aggregate(resources + ["./policy-search/legacy"])
    with pytest.raises(checks.DeploymentError, match="Go Policy Search overlay is missing"):
        checks.validate_qa_aggregate(["./product"])


def test_deployed_search_requires_nonempty_canonical_results() -> None:
    checks.validate_deployed_search(200, b'{"policies":[{"number":"SYN-236"}]}')

    for body in (b"{}", b'{"policies":[]}'):
        with pytest.raises(checks.DeploymentError, match="deployed search differs"):
            checks.validate_deployed_search(200, body)


def test_target_renders_steady_state_contract() -> None:
    target = object.__new__(checks.Target)
    target.environment = "local-dev"

    text, items, _ = target.render_contract()

    checks.validate_render("local-dev", text)
    config = next(item for item in items if item["kind"] == "ConfigMap")
    assert config["data"]["EVENT_WORKERS_ENABLED"] == "true"
    assert config["data"]["KAFKA_GROUP"] == "policy-search-service"


def test_render_rejects_wrong_elasticsearch_secret_and_egress() -> None:
    items = rendered_items()
    deployment = next(item for item in items if item["kind"] == "Deployment")
    username = next(
        item
        for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]
        if item["name"] == "ELASTICSEARCH_USERNAME"
    )
    username["valueFrom"]["secretKeyRef"]["name"] = "foreign-secret"
    with pytest.raises(checks.DeploymentError, match="Secret reference"):
        checks.validate_render(
            "local-dev", "\n---\n".join(checks.yaml.safe_dump(item) for item in items)
        )

    items = rendered_items()
    policy = next(item for item in items if item["kind"] == "NetworkPolicy")
    policy["spec"]["egress"] = policy["spec"]["egress"][:1]
    with pytest.raises(checks.DeploymentError, match="Elasticsearch egress"):
        checks.validate_render(
            "local-dev", "\n---\n".join(checks.yaml.safe_dump(item) for item in items)
        )


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
def test_render_rejects_obsolete_cutover_secret_reference(environment: str) -> None:
    items = rendered_items(environment)
    deployment = next(item for item in items if item["kind"] == "Deployment")
    username = next(
        item
        for item in deployment["spec"]["template"]["spec"]["containers"][0]["env"]
        if item["name"] == "ELASTICSEARCH_USERNAME"
    )
    username["valueFrom"]["secretKeyRef"]["name"] = (
        f"{environment}-policy-search-cutover-elasticsearch"
    )

    with pytest.raises(checks.DeploymentError, match="Secret reference"):
        checks.validate_render(environment, checks.yaml.safe_dump_all(items))


@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("ELASTICSEARCH_INDEX", "foreign-index"),
        ("KAFKA_GROUP", "foreign-group"),
        ("KAFKA_REGISTERED_TOPIC", "foreign-topic"),
    ],
)
def test_render_rejects_noncanonical_state_coordinates(field: str, value: str) -> None:
    items = rendered_items()
    config = next(item for item in items if item["kind"] == "ConfigMap")
    config["data"][field] = value

    with pytest.raises(checks.DeploymentError, match="runtime budgets"):
        checks.validate_render("local-dev", checks.yaml.safe_dump_all(items))


def test_render_rejects_wrong_kafka_egress() -> None:
    items = rendered_items()
    policy = next(item for item in items if item["kind"] == "NetworkPolicy")
    policy["spec"]["egress"][-1]["ports"][0]["port"] = 9092

    with pytest.raises(checks.DeploymentError, match="Kafka egress"):
        checks.validate_render("local-dev", checks.yaml.safe_dump_all(items))


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
    target = Mock(configuration={"mode": "direct"}, environment="local-dev")
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


def test_qa_preflight_accepts_staged_registry_image_without_local_inspection() -> None:
    # given
    target = Mock(configuration={"mode": "flux"}, environment="qa")
    command = Mock()

    # when
    with patch.object(checks, "run", command):
        checks.secret_and_image(
            target, "ghcr.io/igor-baiborodine/insurance-hub/policy-search-api:1.0.0"
        )

    # then
    command.assert_not_called()


def test_qa_preflight_rejects_unreleased_registry_image() -> None:
    # given
    target = Mock(configuration={"mode": "flux"}, environment="qa")

    # when / then
    with pytest.raises(checks.DeploymentError, match="image tag differs"):
        checks.secret_and_image(
            target, "ghcr.io/igor-baiborodine/insurance-hub/policy-search-api:latest"
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


def test_release_preparation_produces_valid_qa_render(tmp_path) -> None:
    # given the active canonical event Policy Search QA overlay and release catalog entry.
    catalog = tmp_path / "scripts/go/service/catalog.json"
    catalog.parent.mkdir(parents=True)
    shutil.copy2(checks.ROOT / "scripts/go/service/catalog.json", catalog)
    for relative in (
        "k8s/apps/svc/policy-search/base",
        "k8s/overlays/qa/svc/policy-search",
    ):
        shutil.copytree(checks.ROOT / relative, tmp_path / relative)
    release_owner.prepare_release(
        tmp_path,
        "policy-search",
        "igor-baiborodine/insurance-hub",
        "1.27.305",
        "a" * 40,
        "sha256:" + "b" * 64,
    )

    # when the prepared overlay is rendered and checked by the deployment owner.
    rendered = checks.run(
        [
            os.environ.get("KUBECTL", "kubectl"),
            "kustomize",
            str(tmp_path / "k8s/overlays/qa/svc/policy-search"),
        ]
    )
    items = checks.validate_render("qa", rendered)

    # then release and telemetry identities satisfy the same QA render contract.
    deployment = next(item for item in items if item["kind"] == "Deployment")
    config = next(item for item in items if item["kind"] == "ConfigMap")
    assert (
        deployment["spec"]["template"]["spec"]["containers"][0]["image"]
        == "ghcr.io/igor-baiborodine/insurance-hub/policy-search-api:1.27.305"
    )
    assert config["data"]["SERVICE_VERSION"] == "git-aaaaaaaaaaaa"
    assert config["data"]["ELASTICSEARCH_INDEX"] == "policy-views"
    assert config["data"]["EVENT_WORKERS_ENABLED"] == "true"
    assert (
        deployment["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/version"]
        == "git-aaaaaaaaaaaa"
    )
    assert deployment["spec"]["template"]["metadata"]["labels"]["environment"] == "qa"
    assert "OTEL_EXPORTER_OTLP_TIMEOUT" not in config["data"]


def test_live_state_accepts_api_server_normalized_resources() -> None:
    # given API server output with omitted empty lists and explicit defaults.
    items = rendered_items()
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    policy = next(item for item in items if item["kind"] == "NetworkPolicy")
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


def test_qa_live_state_accepts_required_ca_volume_and_rejects_drift() -> None:
    # given the QA render and an identical live resource inventory.
    items = rendered_items("qa")
    deployment = next(item for item in items if item["kind"] == "Deployment")
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    target = object.__new__(checks.Target)
    target.deployment = deployment["metadata"]["name"]
    target.configuration = {"mode": "flux"}

    def live_json(_operation, kind, *arguments):
        if kind == "pods":
            return {"items": []}
        return actual[(kind, arguments[0])]

    target.json = Mock(side_effect=live_json)

    # when live state is checked before rollout digest verification.
    with patch.dict(os.environ, {}, clear=True):
        # then the exact required CA volume passes.
        target.live(items, require_deployment=False)

        # given the mounted Secret drifts from the rendered contract.
        actual[("deployment", deployment["metadata"]["name"])]["spec"]["template"]["spec"][
            "volumes"
        ][0]["secret"]["secretName"] = "foreign-ca"

        # when / then the live-state guard rejects the drift.
        with pytest.raises(checks.DeploymentError, match="pod security settings differ"):
            target.live(items, require_deployment=False)


@pytest.mark.parametrize(
    ("expected", "image_id", "message"),
    [
        (
            "sha256:" + "a" * 64,
            "ghcr.io/igor-baiborodine/insurance-hub/policy-search-api@sha256:" + "a" * 64,
            "",
        ),
        (
            "sha256:" + "a" * 64,
            "ghcr.io/igor-baiborodine/insurance-hub/policy-search-api@sha256:" + "b" * 64,
            "image digest differs",
        ),
        (
            "",
            "ghcr.io/igor-baiborodine/insurance-hub/policy-search-api@sha256:" + "a" * 64,
            "DEPLOY_IMAGE_DIGEST is required",
        ),
    ],
)
def test_qa_live_state_requires_running_pod_digest(
    expected: str, image_id: str, message: str
) -> None:
    # given a ready QA Pod selected by the rendered Deployment.
    items = rendered_items()
    deployment = next(item for item in items if item["kind"] == "Deployment")
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    pod = {
        "metadata": {"name": "qa-policy-search-api-abc"},
        "status": {
            "phase": "Running",
            "conditions": [{"type": "Ready", "status": "True"}],
            "containerStatuses": [
                {"name": "policy-search-api", "imageID": image_id},
            ],
        },
    }

    def live_json(_operation, kind, *arguments):
        if kind == "pods":
            return {"items": [pod]}
        return actual[(kind, arguments[0])]

    target = object.__new__(checks.Target)
    target.deployment = deployment["metadata"]["name"]
    target.configuration = {"mode": "flux"}
    target.json = Mock(side_effect=live_json)
    environment = {"DEPLOY_IMAGE_DIGEST": expected} if expected else {}

    # when / then live verification checks the runtime-resolved image identity.
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
                "app.kubernetes.io/component=api,app.kubernetes.io/name=policy-search-api",
            )


def test_live_state_rejects_stale_deployment_after_rollout() -> None:
    # given
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

    # when / then
    with (
        patch.dict(os.environ, {}, clear=True),
        pytest.raises(checks.DeploymentError, match="image differs"),
    ):
        target.live(items, require_deployment=True)


def test_live_state_allows_version_label_enrollment_only_before_rollout() -> None:
    # given a previously accepted Deployment that predates the required source-version labels.
    items = rendered_items()
    deployment = next(item for item in items if item["kind"] == "Deployment")
    config = next(item for item in items if item["kind"] == "ConfigMap")
    version = "git-123456789abc"
    deployment["metadata"].setdefault("labels", {})["app.kubernetes.io/version"] = version
    deployment["spec"]["template"]["metadata"].setdefault("labels", {})[
        "app.kubernetes.io/version"
    ] = version
    config["data"]["SERVICE_VERSION"] = version
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    live_deployment = actual[("deployment", deployment["metadata"]["name"])]
    live_deployment["metadata"]["labels"].pop("app.kubernetes.io/version")
    live_deployment["spec"]["template"]["metadata"]["labels"].pop("app.kubernetes.io/version")
    target = object.__new__(checks.Target)
    target.deployment = deployment["metadata"]["name"]
    target.configuration = {"mode": "direct"}
    target.json = Mock(side_effect=lambda _, kind, name, _ignore: actual[(kind, name)])

    with patch.dict(os.environ, {}, clear=True):
        # when / then the missing labels may be repaired, but cannot pass post-rollout status.
        target.live(items, require_deployment=False)
        with pytest.raises(checks.DeploymentError, match="version identity differs"):
            target.live(items, require_deployment=True)


def test_live_state_allows_container_identity_enrollment_only_before_rollout() -> None:
    # given the previously accepted container name and otherwise identical desired state.
    items = rendered_items()
    deployment = next(item for item in items if item["kind"] == "Deployment")
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    live_deployment = actual[("deployment", deployment["metadata"]["name"])]
    live_deployment["spec"]["template"]["spec"]["containers"][0]["name"] = "policy-search-service"
    target = object.__new__(checks.Target)
    target.deployment = deployment["metadata"]["name"]
    target.configuration = {"mode": "direct"}
    target.json = Mock(side_effect=lambda _, kind, name, _ignore: actual[(kind, name)])

    with patch.dict(os.environ, {}, clear=True):
        # when / then the old name may be repaired, but cannot pass post-rollout status.
        target.live(items, require_deployment=False)
        with pytest.raises(checks.DeploymentError, match="container name differs"):
            target.live(items, require_deployment=True)


def test_live_state_allows_generated_config_transition_only_before_rollout() -> None:
    # given the prior generated ConfigMap reference and otherwise identical desired state.
    items = rendered_items()
    deployment = next(item for item in items if item["kind"] == "Deployment")
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    live_deployment = actual[("deployment", deployment["metadata"]["name"])]
    live_deployment["spec"]["template"]["spec"]["containers"][0]["envFrom"][0]["configMapRef"][
        "name"
    ] = "local-dev-policy-search-config-aaaaaaaaaa"
    target = object.__new__(checks.Target)
    target.deployment = deployment["metadata"]["name"]
    target.configuration = {"mode": "direct"}
    target.json = Mock(side_effect=lambda _, kind, name, _ignore: actual[(kind, name)])

    with patch.dict(os.environ, {}, clear=True):
        # when / then only the generated name may transition before the rollout.
        target.live(items, require_deployment=False)
        with pytest.raises(checks.DeploymentError, match="container envFrom differs"):
            target.live(items, require_deployment=True)


def test_live_state_allows_environment_label_enrollment_only_before_rollout() -> None:
    # given a previously accepted Pod template that predates the QA discovery label.
    items = rendered_items()
    deployment = next(item for item in items if item["kind"] == "Deployment")
    deployment["spec"]["template"]["metadata"].setdefault("labels", {})["environment"] = "qa"
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    live_deployment = actual[("deployment", deployment["metadata"]["name"])]
    live_deployment["spec"]["template"]["metadata"]["labels"].pop("environment")
    target = object.__new__(checks.Target)
    target.deployment = deployment["metadata"]["name"]
    target.configuration = {"mode": "direct"}
    target.json = Mock(side_effect=lambda _, kind, name, _ignore: actual[(kind, name)])

    with patch.dict(os.environ, {}, clear=True):
        # when / then the missing label may be repaired, but cannot pass post-rollout status.
        target.live(items, require_deployment=False)
        with pytest.raises(checks.DeploymentError, match="pod identity differs"):
            target.live(items, require_deployment=True)


def test_live_state_accepts_declared_predecessor_only_before_rollout() -> None:
    # given
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
        # when / then the declared predecessor is accepted only before apply.
        target.live(items, require_deployment=False)

        # when / then post-rollout verification requires the desired state.
        with pytest.raises(checks.DeploymentError, match="differs from rendered configuration"):
            target.live(items, require_deployment=True)


def test_live_state_rejects_obsolete_environment_and_egress_before_rollout() -> None:
    items = rendered_items()
    deployment = next(item for item in items if item["kind"] == "Deployment")
    policy = next(item for item in items if item["kind"] == "NetworkPolicy")
    actual = {(item["kind"].lower(), item["metadata"]["name"]): deepcopy(item) for item in items}
    target = object.__new__(checks.Target)
    target.deployment = deployment["metadata"]["name"]
    target.configuration = {"mode": "direct"}
    target.json = Mock(side_effect=lambda _, kind, name, _ignore: actual[(kind, name)])
    previous_image = "insurance-hub-policy-search-api:1.0.0-local.abcdef123456"
    container = actual[("deployment", deployment["metadata"]["name"])]["spec"]["template"]["spec"][
        "containers"
    ][0]
    container["image"] = previous_image
    container["env"] = [item for item in container["env"] if item["name"] == "SERVICE_INSTANCE_ID"]

    with (
        patch.dict(os.environ, {"DEPLOY_PREVIOUS_IMAGE_REF": previous_image}, clear=True),
        pytest.raises(checks.DeploymentError, match="container env differs"),
    ):
        target.live(items, require_deployment=False)

    container["env"] = deployment["spec"]["template"]["spec"]["containers"][0]["env"]
    actual[("networkpolicy", policy["metadata"]["name"])]["spec"].pop("egress")
    with (
        patch.dict(os.environ, {"DEPLOY_PREVIOUS_IMAGE_REF": previous_image}, clear=True),
        pytest.raises(checks.DeploymentError, match="NetworkPolicy/.+ differs"),
    ):
        target.live(items, require_deployment=False)


def test_deploy_rejects_foreign_same_name_resource_before_apply() -> None:
    # given
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

    # then
    apply.assert_not_called()


def test_cli_preserves_interrupt_status() -> None:
    for requested, expected in ((signal.SIGINT, 130), (signal.SIGTERM, 143)):

        def interrupted(_argv=None, selected=requested):
            os.kill(os.getpid(), selected)
            return 0

        with patch.object(deployment, "main", interrupted):
            assert deployment.cli([]) == expected
