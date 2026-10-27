"""Offline Q04 release candidates. These checks do not simulate a Flux controller."""

from __future__ import annotations

import copy
import os
import tempfile
from pathlib import Path

import deployment_check
import pytest
import yaml

ROOT = deployment_check.REPOSITORY_ROOT
FIXTURE = ROOT / "services/product-service/testdata/administration-design"


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
    assert job["spec"]["activeDeadlineSeconds"] == 300
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
    # given/when: real Kustomize renders, without contacting any cluster
    with tempfile.TemporaryDirectory(dir=FIXTURE, prefix="render-") as directory:
        root = Path(directory)
        (root / "release.yaml").write_text(yaml.safe_dump_all(candidate("a" * 16)))
        (root / "kustomization.yaml").write_text("resources:\n  - release.yaml\n")
        rendered = deployment_check.render(os.environ.get("KUBECTL", "kubectl"), root)
    # then: validate by kind/name, independent of Kustomize sorting
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
    "defect", ["mutable-source", "no-wait", "ttl", "shared-credential", "retry"]
)
def test_release_lifecycle_defects_are_rejected(defect):
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
    else:
        pod = job["spec"]["template"]["spec"]
        pod["containers"][0]["env"] = copy.deepcopy(pod["initContainers"][0]["env"])
    with pytest.raises(AssertionError):
        validate(documents)


def test_explicit_retry_changes_only_prerequisite_identity():
    first, retry = candidate("a" * 16), candidate("a" * 16, 2)
    validate(retry)
    assert first[0] == retry[0]
    assert first[3]["spec"] == retry[3]["spec"]
    assert first[3]["metadata"]["name"] != retry[3]["metadata"]["name"]
    assert first[2]["spec"]["dependsOn"] != retry[2]["spec"]["dependsOn"]


def test_product_transfer_preserves_unrelated_inventory():
    # Java Product is absent at this migration stage. Checked-in legacy references are stale.
    # Only Go Product transfers owners; unrelated services retain their exact resources.
    source = ROOT / "k8s/overlays/qa/svc/kustomization.yaml"
    original = yaml.safe_load(source.read_text())
    assert original["resources"].count("./product") == 1
    kubectl = os.environ.get("KUBECTL", "kubectl")
    baseline = deployment_check.render(kubectl, source.parent)
    product = deployment_check.render(kubectl, source.parent / "product")
    legacy = deployment_check.render(kubectl, source.parent / "product/legacy")
    # Relative references must stay inside the repository's Kustomize tree.
    with tempfile.TemporaryDirectory(dir=source.parent, prefix="q04-render-") as directory:
        scratch = Path(directory)
        remaining = {
            "resources": [
                "../" + item.removeprefix("./")
                for item in original["resources"]
                if item not in {"./product", "./product/legacy"}
            ]
        }
        (scratch / "kustomization.yaml").write_text(yaml.safe_dump(remaining))
        rendered = deployment_check.render(kubectl, scratch)

    def identity(item):
        return (
            item["apiVersion"],
            item["kind"],
            item["metadata"].get("namespace"),
            item["metadata"]["name"],
        )

    legacy_ids = {identity(item) for item in legacy}
    go_only_baseline = [item for item in baseline if identity(item) not in legacy_ids]
    moved_ids = {identity(item) for item in product}
    expected = {
        identity(item): item for item in go_only_baseline if identity(item) not in moved_ids
    }
    assert {identity(item): item for item in rendered} == expected
    assert {identity(item): item for item in product} == {
        identity(item): item for item in go_only_baseline if identity(item) in moved_ids
    }

    def require_java_absent(items):
        assert not ({identity(item) for item in items} & legacy_ids), "Java Product reintroduced"

    require_java_absent(rendered + product)
    # Reintroducing the historical Java desired state must fail the candidate check.
    with pytest.raises(AssertionError, match="Java Product reintroduced"):
        require_java_absent(rendered + product + legacy)
