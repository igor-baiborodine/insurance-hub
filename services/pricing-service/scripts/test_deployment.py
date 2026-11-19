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

QA = Path("k8s/overlays/qa/svc")
SMOKE_LABEL = "insurance-hub.io/pricing-disposable"


def test_wrong_target_is_rejected_before_access(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setattr(pricing, "run", lambda *_args, **_kwargs: pytest.fail("unexpected access"))

    # when / then
    with pytest.raises(DeploymentError, match="coordinates"):
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


def test_qa_refuses_placeholder_activation_and_runtime(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    items = pricing.validate_render("qa", pricing.render("qa"))
    items["ConfigMap"]["data"]["SERVICE_VERSION"] = pricing.PLACEHOLDER
    deployment = items["Deployment"]
    template = deployment["spec"]["template"]
    for metadata in (deployment["metadata"], template["metadata"]):
        metadata["labels"][pricing.VERSION_LABEL] = pricing.PLACEHOLDER
    template["spec"]["containers"][0]["image"] = (
        "ghcr.io/igor-baiborodine/insurance-hub/pricing-api:0.0.0-unpublished"
    )
    monkeypatch.setattr(pricing, "render", lambda _environment: yaml.safe_dump_all(items.values()))
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setenv("DEPLOY_IMAGE_SOURCE_REVISION", "a" * 40)
    monkeypatch.delenv("DEPLOY_IMAGE_REF", raising=False)

    # when / then
    with pytest.raises(DeploymentError, match="activation"):
        pricing.qa_activation(["./pricing/legacy", "./pricing"], items)
    with pytest.raises(DeploymentError, match="inactive or differs"):
        pricing.Target().rendered()


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


def test_qa_requires_exact_flux_inventory(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    revision = "a" * 40
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setenv("DEPLOY_SOURCE_REVISION", revision)
    target = pricing.Target()
    items = pricing.validate_render("qa", pricing.render("qa"))
    entries = [
        {"id": "qa-svc_qa-pricing-api_apps_Deployment", "v": "v1"},
        {"id": "qa-svc_qa-pricing-api_networking.k8s.io_NetworkPolicy", "v": "v1"},
        {"id": "qa-svc_qa-pricing-config__ConfigMap", "v": "v1"},
    ]

    def owner(*args: str) -> dict:
        assert args == ("get", "kustomization", "qa-svc", "-n", "flux-system")
        return {
            "status": {
                "conditions": [{"type": "Ready", "status": "True"}],
                "lastAppliedRevision": f"main@sha1:{revision}",
                "inventory": {"entries": entries},
            }
        }

    monkeypatch.setattr(target, "json", owner)

    # when / then
    target.verify_flux_inventory(items)
    entries.pop()
    with pytest.raises(DeploymentError, match="does not own"):
        target.verify_flux_inventory(items)


def test_release_prepares_published_version_and_activation(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    source = pricing.ROOT
    for relative in ("k8s/apps", "k8s/overlays/qa"):
        shutil.copytree(source / relative, tmp_path / relative)
    for relative in ("scripts/go/service/catalog.json", "services/pricing-service/Dockerfile"):
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / relative, target)
    repository = "igor-baiborodine/insurance-hub"
    sha = "0123456789ab" + "c" * 28
    parent = tmp_path / QA / "kustomization.yaml"
    aggregate = yaml.safe_load(parent.read_text())
    aggregate["resources"] = [
        resource for resource in aggregate["resources"] if resource != "./pricing"
    ]
    parent.write_text(yaml.safe_dump(aggregate))
    previous = parent.read_bytes()

    def inventory() -> dict:
        rendered = pricing.run(["kubectl", "kustomize", str(parent.parent)])
        objects = list(yaml.safe_load_all(rendered))
        result = {
            (
                item["apiVersion"],
                item["kind"],
                item["metadata"].get("namespace", ""),
                item["metadata"]["name"],
            ): item
            for item in objects
        }
        assert len(result) == len(objects)
        return result

    before = inventory()
    selection = release.select_service(
        ["services/pricing-service/Dockerfile"], tmp_path, repository
    )
    assert selection.service == "pricing"
    with pytest.raises(release.ReleaseSelectionError, match="digest"):
        release.prepare_release(tmp_path, "pricing", repository, "1.0.0", sha, "unpublished")
    assert parent.read_bytes() == previous

    # when
    paths = release.prepare_release(
        tmp_path, "pricing", repository, "1.0.0", sha, "sha256:" + "d" * 64
    )

    # then
    assert set(paths) == {parent, tmp_path / QA / "pricing/kustomization.yaml"}
    monkeypatch.setattr(pricing, "ROOT", tmp_path)
    items = pricing.validate_render("qa", pricing.render("qa"))
    assert items["ConfigMap"]["data"]["SERVICE_VERSION"] == "git-0123456789ab"
    assert items["Deployment"]["spec"]["template"]["spec"]["containers"][0]["image"] == (
        "ghcr.io/igor-baiborodine/insurance-hub/pricing-api:1.0.0"
    )
    pricing.qa_activation(yaml.safe_load(parent.read_text())["resources"], items)
    after = inventory()
    added = {key: item for key, item in after.items() if key not in before}
    assert {key: after[key] for key in before} == before
    assert {key[3] for key in added} == {
        "qa-pricing-api",
        "qa-pricing-config",
    }
    assert len(added) == 3
    assert {item["kind"]: item for item in added.values()} == items


def test_denial_cleanup_preserves_primary_error(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
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


@pytest.mark.integration
def test_pricing_in_owned_disposable_kind_namespace(monkeypatch: pytest.MonkeyPatch) -> None:
    # given

    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
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
