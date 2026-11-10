"""Offline Q04 release candidates. These checks do not simulate a Flux controller."""

from __future__ import annotations

import copy
import json
import os
import tempfile
from pathlib import Path

import deployment_check
import pytest
import yaml

ROOT = deployment_check.REPOSITORY_ROOT
FIXTURE = ROOT / "services/product-service/testdata/administration-design"


def administration_policy():
    catalog = json.loads((ROOT / "scripts/go/service/catalog.json").read_text())
    return catalog["services"]["product"]["administration"]


def candidate(release, attempt=1):
    """Materialize the proposed immutable-source graph in owned scratch only."""
    documents = list(yaml.safe_load_all((FIXTURE / "release.yaml").read_text()))
    encoded = (
        yaml.safe_dump_all(documents).replace("RELEASE", release).replace("ATTEMPT", str(attempt))
    )
    encoded = encoded.replace("SOURCE_COMMIT", (release * 3)[:40]).replace(
        "IMAGE_DIGEST", release * 4
    )
    return list(yaml.safe_load_all(encoded))


def validate(documents):
    source, initialize, api, job = documents
    policy = administration_policy()
    assert source["kind"] == "GitRepository"
    assert set(source["spec"]["ref"]) == {"commit"}
    assert len(source["spec"]["ref"]["commit"]) == 40
    assert initialize["spec"]["sourceRef"]["name"] == source["metadata"]["name"]
    assert api["spec"]["sourceRef"] == initialize["spec"]["sourceRef"]
    assert api["spec"]["dependsOn"] == [{"name": initialize["metadata"]["name"]}]
    assert initialize["spec"]["wait"] is True
    assert initialize["spec"]["force"] is False
    assert initialize["spec"]["healthChecks"][0]["name"] == job["metadata"]["name"]
    assert job["spec"]["backoffLimit"] == 0
    assert job["spec"]["activeDeadlineSeconds"] == policy["jobDeadlineSeconds"]
    assert (
        policy["maxMigrationSeconds"] + policy["seedSeconds"] + policy["minimumJobReserveSeconds"]
        <= job["spec"]["activeDeadlineSeconds"]
    )
    assert "ttlSecondsAfterFinished" not in job["spec"]
    pod = job["spec"]["template"]["spec"]
    assert pod["automountServiceAccountToken"] is False
    assert pod["restartPolicy"] == "Never"
    migration, seed = pod["initContainers"][0], pod["containers"][0]
    assert migration["command"] == ["/product-migrate"]
    assert seed["command"] == ["/product-seed"]
    assert migration["image"] == seed["image"]
    assert "@sha256:" in seed["image"]
    for owner in (initialize, api):
        assert owner["spec"]["images"][0]["digest"] == seed["image"].split("@", 1)[1]
    assert (
        migration["env"][0]["valueFrom"]["secretKeyRef"]["name"]
        != seed["env"][0]["valueFrom"]["secretKeyRef"]["name"]
    )
    assert seed["env"][0]["valueFrom"]["secretKeyRef"]["name"] == (
        "qa-postgres-product-seeder-creds"
    )


def test_release_candidates_render_and_isolate_sources():
    # given
    with tempfile.TemporaryDirectory(dir=FIXTURE, prefix="render-") as directory:
        root = Path(directory)
        (root / "release.yaml").write_text(yaml.safe_dump_all(candidate("a" * 16)))
        (root / "kustomization.yaml").write_text("resources:\n  - release.yaml\n")

        # when
        rendered = deployment_check.render(os.environ.get("KUBECTL", "kubectl"), root)

    # then
    by_name = {(item["kind"], item["metadata"]["name"]): item for item in rendered}
    ordered = [by_name[(item["kind"], item["metadata"]["name"])] for item in candidate("a" * 16)]
    validate(ordered)
    first, second = candidate("a" * 16), candidate("b" * 16)
    validate(second)
    assert first[0]["metadata"]["name"] != second[0]["metadata"]["name"]
    assert first[1]["metadata"]["name"] != second[1]["metadata"]["name"]
    assert first[3]["metadata"]["name"] != second[3]["metadata"]["name"]
    assert first[2]["metadata"]["name"] == second[2]["metadata"]["name"]
    # The old object's success cannot satisfy the new object's named dependency.
    second[2]["spec"]["dependsOn"] = first[2]["spec"]["dependsOn"]
    with pytest.raises(AssertionError):
        validate(second)


@pytest.mark.parametrize(
    "defect",
    ["mutable-source", "no-wait", "ttl", "shared-credential", "retry", "job-budget"],
)
def test_release_lifecycle_defects_are_rejected(defect):
    # given
    documents = candidate("a" * 16)
    validate(documents)
    source, initialize, _, job = documents
    if defect == "mutable-source":
        source["spec"]["ref"] = {"branch": "main"}
    elif defect == "no-wait":
        initialize["spec"]["wait"] = False
    elif defect == "ttl":
        job["spec"]["ttlSecondsAfterFinished"] = 60
    elif defect == "retry":
        job["spec"]["backoffLimit"] = 6
    elif defect == "job-budget":
        job["spec"]["activeDeadlineSeconds"] = 299
    else:
        pod = job["spec"]["template"]["spec"]
        pod["containers"][0]["env"] = copy.deepcopy(pod["initContainers"][0]["env"])

    # when / then
    with pytest.raises(AssertionError):
        validate(documents)


def test_explicit_retry_changes_only_prerequisite_identity():
    # given
    first = candidate("a" * 16)

    # when
    retry = candidate("a" * 16, 2)
    validate(retry)

    # then
    assert first[0] == retry[0]
    assert first[3]["spec"] == retry[3]["spec"]
    assert first[3]["metadata"]["name"] != retry[3]["metadata"]["name"]
    assert first[2]["spec"]["dependsOn"] != retry[2]["spec"]["dependsOn"]


def test_product_release_owner_preserves_unrelated_inventory():
    # given
    source = ROOT / "k8s/overlays/qa/svc/kustomization.yaml"
    original = yaml.safe_load(source.read_text())
    assert "./product" not in original["resources"]
    kubectl = os.environ.get("KUBECTL", "kubectl")
    shared = deployment_check.render(kubectl, source.parent)
    product = deployment_check.render(kubectl, source.parent / "product")

    def identity(item):
        return (
            item["apiVersion"],
            item["kind"],
            item["metadata"].get("namespace"),
            item["metadata"]["name"],
        )

    # then
    shared_ids = {identity(item) for item in shared}
    product_ids = {identity(item) for item in product}
    assert shared_ids.isdisjoint(product_ids)
    assert all(
        "kustomize.toolkit.fluxcd.io/prune" not in item["metadata"].get("annotations", {})
        for item in product
    )
    for relative in original["resources"]:
        child_ids = {
            identity(item)
            for item in deployment_check.render(
                kubectl, source.parent / relative.removeprefix("./")
            )
        }
        assert child_ids <= shared_ids


@pytest.mark.parametrize(
    ("environment", "namespace", "context", "timeout"),
    [
        ("local-dev", "local-dev-all", "kind-local-dev-insurance-hub", "120s"),
        ("qa", "qa-svc", "qa-insurance-hub", "${MIGRATION_TIMEOUT}"),
    ],
)
def test_real_administration_overlay_is_separate_and_bounded(
    environment, namespace, context, timeout
):
    # given
    kubectl = os.environ.get("KUBECTL", "kubectl")
    overlay = ROOT / f"k8s/overlays/{environment}/svc/product/administration"

    # when
    documents = deployment_check.render(kubectl, overlay)

    # then
    policy = administration_policy()
    assert len(documents) == 1
    job = documents[0]
    assert job["kind"] == "Job"
    assert job["metadata"]["namespace"] == namespace
    assert job["spec"]["backoffLimit"] == 0
    assert job["spec"]["activeDeadlineSeconds"] == policy["jobDeadlineSeconds"]
    assert (
        policy["maxMigrationSeconds"] + policy["seedSeconds"] + policy["minimumJobReserveSeconds"]
        <= job["spec"]["activeDeadlineSeconds"]
    )
    assert "ttlSecondsAfterFinished" not in job["spec"]
    pod = job["spec"]["template"]["spec"]
    assert pod["automountServiceAccountToken"] is False
    assert pod["restartPolicy"] == "Never"
    assert pod["securityContext"]["runAsUser"] == 65532
    migrate = pod["initContainers"]
    seed = pod["containers"]
    assert len(migrate) == len(seed) == 1
    assert migrate[0]["command"] == ["/product-migrate"]
    assert seed[0]["command"] == ["/product-seed"]
    assert f"--environment={environment}" in migrate[0]["args"]
    assert f"--context={context}" in migrate[0]["args"]
    assert f"--timeout={timeout}" in migrate[0]["args"]
    assert f"--environment={environment}" in seed[0]["args"]
    assert migrate[0]["image"] == seed[0]["image"]
    assert all(
        container["securityContext"]
        == {
            "allowPrivilegeEscalation": False,
            "readOnlyRootFilesystem": True,
            "capabilities": {"drop": ["ALL"]},
        }
        for container in (migrate[0], seed[0])
    )
    secret_names = {volume["secret"]["secretName"] for volume in pod["volumes"]}
    assert secret_names == {
        f"{environment}-postgres-product-admin-target",
        f"{environment}-postgres-product-migrator-creds",
        f"{environment}-postgres-product-seeder-creds",
    }


def test_api_and_shared_qa_owner_separate_release_and_credentials():
    # given
    kubectl = os.environ.get("KUBECTL", "kubectl")

    # when
    api = deployment_check.render(kubectl, ROOT / "k8s/overlays/qa/svc/product")
    aggregate = deployment_check.render(kubectl, ROOT / "k8s/overlays/qa/svc")

    # then
    deployment = next(item for item in api if item["kind"] == "Deployment")
    rendered = json.dumps(deployment, sort_keys=True)
    assert "postgres-product-reader-creds" in rendered
    assert "migrator-creds" not in rendered
    assert "seeder-creds" not in rendered
    identities = {(item["kind"], item["metadata"]["name"]) for item in aggregate}
    assert ("Deployment", "qa-product-api") not in identities
    assert not any("product-init" in name for _, name in identities)


def test_product_release_bundle_is_the_active_owner():
    # given / when
    root = yaml.safe_load((ROOT / "k8s/flux/qa/kustomization.yaml").read_text())
    documents = list(
        yaml.safe_load_all((ROOT / "k8s/flux/qa/releases/product/release.yaml").read_text())
    )
    api = next(
        item
        for item in documents
        if item.get("kind") == "Kustomization"
        and item.get("metadata", {}).get("name") == "qa-product-api"
    )
    source_name = api["spec"]["sourceRef"]["name"]
    source = next(
        item
        for item in documents
        if item.get("kind") == "GitRepository"
        and item.get("metadata", {}).get("name") == source_name
    )
    revision = source["spec"]["ref"]["commit"]
    pinned_overlay = yaml.safe_load(
        deployment_check.run_command(
            [
                "git",
                "show",
                f"{revision}:k8s/overlays/qa/svc/product/kustomization.yaml",
            ],
            cwd=ROOT,
            env=os.environ,
            timeout_seconds=30,
            teardown_seconds=5,
            activity="read active QA Product release overlay",
        ).stdout
    )

    # then
    assert root["resources"] == ["./flux-system", "./releases/product/release.yaml"]
    assert source["metadata"]["name"] == source_name
    assert api["metadata"]["annotations"]["insurance-hub.io/source-revision"] == revision
    assert (
        pinned_overlay.get("commonAnnotations", {}).get("kustomize.toolkit.fluxcd.io/prune") is None
    )
