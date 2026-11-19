"""Pricing render, release, target, and disposable deployment checks."""

from __future__ import annotations

import json
import os
import shutil
import uuid
from pathlib import Path

import pytest
import yaml
from scripts.go.service import release
from scripts.go.service.deployment import DeploymentError, apply

import deployment as pricing

SMOKE_LABEL = "insurance-hub.io/pricing-disposable"


def test_wrong_target_is_rejected_before_access(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setattr(pricing, "run", lambda *_args, **_kwargs: pytest.fail("unexpected access"))

    # when / then
    with pytest.raises(DeploymentError, match="DEPLOY_CONTEXT"):
        pricing.Target()


@pytest.mark.parametrize("defect", ["legacy selector", "public service", "allow Pod ingress"])
def test_render_rejects_access_or_selector_defects(defect: str) -> None:
    # given
    items = pricing.validate_render("qa", pricing.render("qa"))
    if defect == "legacy selector":
        items["Deployment"]["spec"]["selector"]["matchLabels"] = {
            "app.kubernetes.io/name": "pricing-api-legacy",
            "app.kubernetes.io/component": "api",
        }
    elif defect == "public service":
        items["Service"] = {"kind": "Service", "spec": {"type": "LoadBalancer"}}
    else:
        items["NetworkPolicy"]["spec"]["ingress"] = [{}]

    # when / then
    with pytest.raises(DeploymentError):
        pricing.validate_render("qa", yaml.safe_dump_all(items.values()))


def test_local_egress_rejects_broad_dns() -> None:
    # given
    items = pricing.validate_render("local-dev", pricing.render("local-dev"))
    dns = items["NetworkPolicy"]["spec"]["egress"][0]
    del dns["to"][0]["podSelector"]

    # when / then
    with pytest.raises(DeploymentError, match="NetworkPolicy"):
        pricing.validate_render("local-dev", yaml.safe_dump_all(items.values()))


def test_qa_render_rejects_placeholder_release_identity() -> None:
    # given
    items = pricing.validate_render("qa", pricing.render("qa"))
    deployment = items["Deployment"]
    template = deployment["spec"]["template"]
    items["ConfigMap"]["data"]["SERVICE_VERSION"] = pricing.PLACEHOLDER
    for metadata in (deployment["metadata"], template["metadata"]):
        metadata["labels"][pricing.VERSION_LABEL] = pricing.PLACEHOLDER
    template["spec"]["containers"][0]["image"] = (
        "ghcr.io/igor-baiborodine/insurance-hub/pricing-api:0.0.0-unpublished"
    )

    # when / then
    with pytest.raises(DeploymentError, match="QA image and release version"):
        pricing.validate_render("qa", yaml.safe_dump_all(items.values()))


def test_qa_staging_keeps_go_out_of_shared_owner_and_suspends_old_initialization(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    for relative in (
        "k8s/overlays/qa/svc/kustomization.yaml",
        "k8s/flux/qa/kustomization.yaml",
        "k8s/flux/qa/releases/pricing/release.yaml",
    ):
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(pricing.ROOT / relative, target)
    monkeypatch.setattr(pricing, "ROOT", tmp_path)

    # when
    pricing.qa_release_ownership()

    # then
    shared = yaml.safe_load((tmp_path / "k8s/overlays/qa/svc/kustomization.yaml").read_text())
    shared["resources"].append("./pricing")
    (tmp_path / "k8s/overlays/qa/svc/kustomization.yaml").write_text(yaml.safe_dump(shared))
    with pytest.raises(DeploymentError, match="shared QA ownership"):
        pricing.qa_release_ownership()


def _released_bundle(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> list[dict]:
    for relative in ("k8s/overlays/qa/svc/kustomization.yaml", "k8s/flux/qa/kustomization.yaml"):
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(pricing.ROOT / relative, target)
    monkeypatch.setattr(pricing, "ROOT", tmp_path)
    return [
        {"kind": "GitRepository", "metadata": {"name": "old-source"}},
        {
            "kind": "Kustomization",
            "metadata": {
                "name": "old-init",
                "annotations": {"insurance-hub.io/staged-predecessor": "true"},
            },
            "spec": {
                "suspend": True,
                "path": "./k8s/overlays/qa/svc/pricing/administration",
                "sourceRef": {"kind": "GitRepository", "name": "old-source"},
            },
        },
        {"kind": "GitRepository", "metadata": {"name": "new-source"}},
        {
            "kind": "Kustomization",
            "metadata": {"name": "new-init"},
            "spec": {
                "path": "./k8s/overlays/qa/svc/pricing/administration",
                "sourceRef": {"kind": "GitRepository", "name": "new-source"},
            },
        },
        {
            "kind": "Kustomization",
            "metadata": {"name": "qa-pricing-api"},
            "spec": {
                "path": "./k8s/overlays/qa/svc/pricing",
                "sourceRef": {"kind": "GitRepository", "name": "new-source"},
                "dependsOn": [{"name": "new-init"}],
            },
        },
    ]


def _staged_only(bundle: list[dict]) -> None:
    del bundle[2:4]
    bundle[-1]["spec"]["sourceRef"]["name"] = "old-source"
    del bundle[-1]["spec"]["dependsOn"]


@pytest.mark.parametrize(
    ("mutate", "error"),
    [
        (lambda bundle: None, None),
        (_staged_only, None),
        (lambda bundle: bundle[1]["spec"].pop("suspend"), "predecessor must not run"),
        (
            lambda bundle: bundle[-1]["spec"].update(dependsOn=[{"name": "old-init"}]),
            "own release initialization",
        ),
        (lambda bundle: bundle[-1]["spec"].pop("dependsOn"), "retain the old API"),
    ],
    ids=["released", "staged", "predecessor-active", "old-prerequisite", "no-prerequisite"],
)
def test_qa_release_bundle_requires_api_to_follow_its_own_initialization(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, mutate, error: str | None
) -> None:
    # given
    bundle = _released_bundle(tmp_path, monkeypatch)
    mutate(bundle)
    path = tmp_path / "k8s/flux/qa/releases/pricing/release.yaml"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(yaml.safe_dump_all(bundle))

    # when / then
    if error is None:
        pricing.qa_release_ownership()
    else:
        with pytest.raises(DeploymentError, match=error):
            pricing.qa_release_ownership()


@pytest.mark.parametrize("revision_matches", [True, False])
def test_qa_image_requires_published_digest_and_matching_oci_labels(
    monkeypatch: pytest.MonkeyPatch, revision_matches: bool
) -> None:
    # given
    revision = "a" * 40
    digest = "sha256:" + "b" * 64
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setenv("DEPLOY_DATA_NS", "qa-data")
    monkeypatch.setenv("DEPLOY_IMAGE_SOURCE_REVISION", revision)
    monkeypatch.setenv("DEPLOY_IMAGE_DIGEST", digest)
    target = pricing.Target()
    registry = target.record["registryImage"]

    def inspect(arguments: list[str]) -> str:
        assert arguments[1:] == ["image", "inspect", f"{registry}@{digest}"]
        return json.dumps(
            [
                {
                    "RepoDigests": [f"{registry}@{digest}"],
                    "Config": {
                        "Labels": {
                            "org.opencontainers.image.revision": revision
                            if revision_matches
                            else "c" * 40,
                            "org.opencontainers.image.version": "git-" + revision[:12],
                        }
                    },
                }
            ]
        )

    monkeypatch.setattr(pricing, "run", inspect)

    # when / then
    if revision_matches:
        assert pricing.image_id(target, f"{registry}:1.0.0") == digest
    else:
        with pytest.raises(DeploymentError, match="OCI labels"):
            pricing.image_id(target, f"{registry}:1.0.0")


@pytest.mark.parametrize(
    ("mode", "image", "running", "accepted"),
    [
        (
            "flux",
            "registry/pricing-api@sha256:" + "d" * 64,
            "registry/pricing-api@sha256:" + "d" * 64,
            True,
        ),
        ("flux", "registry/pricing-api:1.1.0", "sha256:" + "d" * 64, True),
        (
            "flux",
            "registry/pricing-api@sha256:" + "d" * 64,
            "registry/pricing-api@sha256:" + "e" * 64,
            False,
        ),
        ("direct", "pricing-api:1.0.0-local.aaaaaaaaaaaa", "sha256:" + "d" * 64, True),
        ("direct", "pricing-api:1.0.0-local.aaaaaaaaaaaa", "sha256:" + "e" * 64, False),
    ],
    ids=["qa-digest-ref", "qa-bare-digest", "qa-other-digest", "local-id", "local-other-id"],
)
def test_running_image_matches_published_digest_or_local_id(
    mode: str, image: str, running: str, accepted: bool
) -> None:
    # given
    digest = "sha256:" + "d" * 64

    # when / then
    assert pricing.running_image_matches(mode, image, digest, running) is accepted


def test_resource_snapshot_rejects_secret_bearing_or_unknown_output(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    safe = {
        "roles": [],
        "memberships": [],
        "target": None,
        "databaseCompatible": False,
        "credentials": {
            "go_pricing_reader": "missing",
            "go_pricing_migrator": "missing",
            "go_pricing_seeder": "missing",
        },
        "markerMatchesEnrollment": False,
    }
    monkeypatch.setattr(pricing, "resource_action", lambda *_args: json.dumps(safe))

    # when
    assert pricing.resource_snapshot(object()) == safe

    # then
    monkeypatch.setattr(
        pricing,
        "resource_action",
        lambda *_args: json.dumps({**safe, "database-url": "synthetic-secret"}),
    )
    with pytest.raises(DeploymentError, match="unexpected state"):
        pricing.resource_snapshot(object())


def test_release_prepares_initialization_before_stable_api(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    source = pricing.ROOT
    for relative in ("k8s/apps", "k8s/overlays/qa", "k8s/flux/qa/releases/pricing"):
        shutil.copytree(source / relative, tmp_path / relative)
    for relative in (
        "scripts/go/service/catalog.json",
        "services/pricing-service/Dockerfile",
        "services/pricing-service/internal/administration/migrations/00001_pricing.sql",
        "services/pricing-service/internal/administration/seeds/base.json",
    ):
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / relative, target)
    repository = "igor-baiborodine/insurance-hub"
    sha = "0123456789ab" + "c" * 28
    bundle = tmp_path / "k8s/flux/qa/releases/pricing/release.yaml"
    before = bundle.read_bytes()
    selection = release.select_service(
        ["services/pricing-service/Dockerfile"], tmp_path, repository
    )
    assert selection.service == "pricing"
    with pytest.raises(release.ReleaseSelectionError, match="digest"):
        release.prepare_release(tmp_path, "pricing", repository, "1.0.0", sha, "unpublished")
    assert bundle.read_bytes() == before

    # when
    paths = release.prepare_release(
        tmp_path, "pricing", repository, "1.0.0", sha, "sha256:" + "d" * 64
    )

    # then
    assert paths == [bundle]
    # Earlier released prerequisites are retained ahead of the new source, init and API owner.
    *retained, source_owner, initialize, api = list(yaml.safe_load_all(bundle.read_text()))
    staged = [
        item
        for item in retained
        if item["metadata"]["annotations"].get("insurance-hub.io/staged-predecessor") == "true"
    ]
    assert {item["kind"] for item in staged} == {"GitRepository", "Kustomization"}
    assert all(
        item["spec"]["suspend"] is True for item in staged if item["kind"] != "GitRepository"
    )
    assert source_owner["spec"]["ref"] == {"commit": sha}
    assert initialize["spec"]["path"].endswith("/pricing/administration")
    assert initialize["spec"]["postBuild"]["substitute"] == {"MIGRATION_TIMEOUT": "120s"}
    assert api["spec"]["dependsOn"] == [{"name": initialize["metadata"]["name"]}]
    assert api["spec"]["postBuild"]["substitute"] == {"PRICING_SERVICE_VERSION": "git-0123456789ab"}
    assert api["spec"]["images"][0]["digest"] == "sha256:" + "d" * 64
    monkeypatch.setattr(pricing, "ROOT", tmp_path)
    rendered = pricing.render("qa").replace("${PRICING_SERVICE_VERSION}", "git-0123456789ab")
    items = pricing.validate_render("qa", rendered)
    assert items["ConfigMap"]["data"]["SERVICE_VERSION"] == "git-0123456789ab"


def test_denial_cleanup_preserves_primary_error(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    monkeypatch.setenv("DEPLOY_DATA_NS", "local-dev-all")
    target = pricing.Target()
    reads = iter(({}, {"metadata": {"labels": {}}}))

    def create(*_args: str, **_kwargs: object) -> str:
        raise DeploymentError("probe creation failed")

    monkeypatch.setattr(target, "json", lambda *_args: next(reads))
    monkeypatch.setattr(target, "kubectl", create)

    # when / then
    with pytest.raises(DeploymentError, match="probe creation failed"):
        pricing.verify_denial(target, {"status": {"podIP": "192.0.2.10"}})
    assert "cleanup failed: Pricing denial probe ownership differs" in capsys.readouterr().err


@pytest.mark.parametrize("error, status", [(KeyboardInterrupt, 130), (pricing.Terminated, 143)])
def test_cli_signal_exit_status(
    monkeypatch: pytest.MonkeyPatch, error: type[KeyboardInterrupt], status: int
) -> None:
    # given
    def interrupted() -> int:
        raise error

    monkeypatch.setattr(pricing, "main", interrupted)

    # when / then
    assert pricing.cli() == status


def test_stop_uses_remaining_poll_budget(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    monkeypatch.setenv("DEPLOY_DATA_NS", "local-dev-all")
    target = pricing.Target()
    time_values = iter((100.0, 185.0))
    monkeypatch.setattr(pricing.time, "monotonic", lambda: next(time_values))
    monkeypatch.setattr(target, "live", lambda *_args, **_kwargs: {})
    monkeypatch.setattr(pricing, "resource_snapshot", lambda *_args: {"safe": True})
    scale_timeouts: list[float] = []
    poll_timeouts: list[float] = []

    def scale(*_args: str, timeout: float) -> None:
        scale_timeouts.append(timeout)

    def poll(*_args: str, timeout: float) -> dict:
        poll_timeouts.append(timeout)
        return {"metadata": {"generation": 1}, "status": {"observedGeneration": 1}}

    monkeypatch.setattr(target, "kubectl", scale)
    monkeypatch.setattr(target, "json", poll)

    # when
    pricing.stop(target, {})

    # then
    assert scale_timeouts == [65]
    assert poll_timeouts == [5]


@pytest.mark.integration
def test_pricing_in_owned_disposable_kind_namespace(monkeypatch: pytest.MonkeyPatch) -> None:
    # given

    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    monkeypatch.setenv("DEPLOY_DATA_NS", "local-dev-all")
    target = pricing.Target()
    require = pricing.require
    require(
        target.configuration["kindCluster"] == "local-dev-insurance-hub"
        and target.context == "kind-local-dev-insurance-hub",
        "unexpected disposable cluster",
    )
    image = os.environ.get("DEPLOY_IMAGE_REF", "")
    pricing.image_id(target, image)
    require(
        target.configuration["kindCluster"] in pricing.run(["kind", "get", "clusters"]).split(),
        "configured Kind cluster is missing",
    )
    _, items, selected = target.rendered()
    require(selected == image, "disposable image differs")
    token = uuid.uuid4().hex[:12]
    target.namespace = f"pricing-smoke-{token}"
    namespace = {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {"name": target.namespace, "labels": {SMOKE_LABEL: token}},
    }
    for item in items.values():
        item["metadata"]["namespace"] = target.namespace

    try:
        # when
        target.kubectl("create", "-f", "-", input_text=json.dumps(namespace))
        pricing.run(
            ["kind", "load", "docker-image", image, "--name", target.configuration["kindCluster"]],
            timeout=120,
        )
        apply(target, yaml.safe_dump_all(items.values()), "insurance-hub-pricing-smoke")
        pricing.rollout(target)

        # then
        monkeypatch.setenv("DEPLOY_DENIAL_PROBE", "1")
        pricing.verify(target, items)
        pricing.stop(target, items)
        assert target.json("get", "deployment", target.deployment)["spec"]["replicas"] == 0
    finally:
        actual = target.json("get", "namespace", target.namespace, "--ignore-not-found")
        if actual:
            require(
                actual["metadata"]["labels"].get(SMOKE_LABEL) == token,
                "disposable namespace ownership differs",
            )
            target.kubectl("delete", "namespace", target.namespace, "--timeout=100s", timeout=110)
            assert not target.json("get", "namespace", target.namespace, "--ignore-not-found")
            print(f"Removed disposable namespace {target.namespace}")
