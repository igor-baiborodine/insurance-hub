"""Tests for generic Go release selection and QA semantic-tag updates."""

from __future__ import annotations

import json
from pathlib import Path

import pytest
import yaml

import release

REPOSITORY = "igor-baiborodine/insurance-hub"
SOURCE_COMMIT = "a" * 40
IMAGE_DIGEST = "sha256:" + "b" * 64
NEXT_SOURCE_COMMIT = "c" * 40


def create_service(
    root: Path,
    service: str,
    *,
    qa_entry: bool = True,
    environment: str = "qa",
    initialization: bool = False,
) -> None:
    module = root / "services" / f"{service}-service"
    module.mkdir(parents=True, exist_ok=True)
    (module / "Dockerfile").write_text("FROM scratch\n")
    overlay = root / "k8s" / "overlays" / environment / "svc" / service
    overlay.mkdir(parents=True, exist_ok=True)
    image = service if qa_entry else "other"
    (overlay / "kustomization.yaml").write_text(
        "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nimages:\n"
        f"  - name: insurance-hub-{image}-api\n    newName: ghcr.io/{REPOSITORY}/{image}-api\n"
        "    newTag: sha-unpublished\n"
    )
    target = {
        "mode": "flux",
        "context": environment + "-cluster",
        "namespace": environment + "-apps",
        "dataNamespace": environment + "-data",
        "overlay": str(overlay.relative_to(root)),
        "flux": {
            "namespace": "flux-system",
            "sourcePrefix": environment + "-" + service + "-release",
            "apiName": environment + "-" + service + "-api",
            "bundle": f"k8s/flux/{environment}/releases/{service}/release.yaml",
            "sharedSource": "repository",
            "sharedOwner": environment + "-apps",
            "sharedPath": f"./k8s/overlays/{environment}/svc",
            "branch": "main",
            "initializationTimeout": "6m",
            "apiTimeout": "5m",
        },
    }
    record = {
        "modulePath": str(module.relative_to(root)),
        "localImage": f"insurance-hub-{service}-api",
        "registryImage": f"ghcr.io/{REPOSITORY}/{service}-api",
        "releaseTargets": [environment],
        "deploymentTargets": {environment: target},
    }
    if initialization:
        asset = module / "migration.sql"
        asset.write_text("-- independent fixture migration\n")
        record["administration"] = {
            "assets": {"migration": str(asset.relative_to(root))},
            "maxMigrationSeconds": 180,
            "seedSeconds": 60,
            "minimumJobReserveSeconds": 60,
            "jobDeadlineSeconds": 300,
            "migrationContainer": "migrate",
        }
        target["initialization"] = {
            "overlay": str(overlay.relative_to(root)) + "/administration",
            "jobName": environment + "-" + service + "-init",
            "migrationTimeout": "120s",
            "targetRevision": "1",
            "credentialRevision": "1",
            "attempt": 1,
            "waitSeconds": 420,
        }
    path = root / "scripts/go/service/catalog.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    catalog = (
        json.loads(path.read_text()) if path.exists() else {"schemaVersion": 1, "services": {}}
    )
    catalog["services"][service] = record
    path.write_text(json.dumps(catalog))


def configure(root: Path, service: str, environment: str, **changes) -> dict:
    path = root / "scripts/go/service/catalog.json"
    catalog = json.loads(path.read_text())
    record = catalog["services"][service]
    record["deploymentTargets"][environment]["initialization"].update(changes)
    path.write_text(json.dumps(catalog))
    return record


@pytest.mark.parametrize("service", ["product", "claims"])
def test_services_use_the_same_release_convention(tmp_path: Path, service: str) -> None:
    # given
    create_service(tmp_path, service)

    # when
    selected = release.select_service(
        [f"services/{service}-service/internal/app.go"], tmp_path, REPOSITORY
    )

    # then
    assert selected.service == service
    assert selected.module_path == f"services/{service}-service"
    assert selected.registry_image == f"ghcr.io/{REPOSITORY}/{service}-api"


@pytest.mark.parametrize(
    "relative",
    [
        "Dockerfile",
        ".dockerignore",
        "go.mod",
        "go.sum",
        "cmd/server/main.go",
        "cmd/server/assets/defaults.json",
        "gen/example/v1/example.pb.go",
        "internal/application/catalog.go",
        "internal/application/assets/catalog.json",
        "internal/postgres/dbgen/queries.sql.go",
    ],
)
def test_release_input_classifies_image_build_inputs(relative: str) -> None:
    assert release.release_module(f"services/product-service/{relative}") == "product-service"


@pytest.mark.parametrize(
    "relative",
    [
        ".gitignore",
        ".golangci.yml",
        "Makefile",
        "README.md",
        "api/example/v1/example.proto",
        "buf.gen.yaml",
        "buf.lock",
        "buf.yaml",
        "cmd/server/main_test.go",
        "internal/application/catalog_test.go",
        "internal/postgres/sql/queries.sql",
        "internal/testing/fixture.go",
        "puppeteer-config.json",
        "scripts/check_ci.py",
        "sqlc.yaml",
        "testdata/contract-baseline/buf.yaml",
    ],
)
def test_release_input_excludes_non_image_inputs(relative: str) -> None:
    assert release.release_module(f"services/product-service/{relative}") is None


def test_selection_ignores_non_release_changes_in_other_services(tmp_path: Path) -> None:
    # given
    create_service(tmp_path, "product")
    create_service(tmp_path, "claims")

    # when
    selected = release.select_service(
        [
            "services/product-service/internal/application/catalog.go",
            "services/claims-service/README.md",
            "services/claims-service/scripts/check_ci.py",
        ],
        tmp_path,
        REPOSITORY,
    )

    # then
    assert selected.service == "product"


@pytest.mark.parametrize(
    "paths",
    [
        [],
        ["services/product-service/Makefile", "services/product-service/README.md"],
        [
            "services/product-service/internal/app.go",
            "services/claims-service/internal/app.go",
        ],
    ],
)
def test_zero_and_multiple_service_selections_fail(tmp_path: Path, paths: list[str]) -> None:
    # given
    create_service(tmp_path, "product")
    create_service(tmp_path, "claims")

    # when / then
    with pytest.raises(release.ReleaseSelectionError):
        release.select_service(paths, tmp_path, REPOSITORY)


def test_missing_standard_qa_entry_fails(tmp_path: Path) -> None:
    # given
    create_service(tmp_path, "product", qa_entry=False)

    # when / then
    with pytest.raises(release.ReleaseSelectionError, match="exactly one"):
        release.select_service(
            ["services/product-service/internal/application/catalog.go"], tmp_path, REPOSITORY
        )


@pytest.mark.parametrize(("service", "environment"), [("product", "qa"), ("claims", "staging")])
@pytest.mark.parametrize("initialization", [False, True])
def test_prepare_respects_target_capabilities(
    tmp_path: Path, service: str, environment: str, initialization: bool
) -> None:
    # given
    create_service(tmp_path, service, environment=environment, initialization=initialization)
    overlay = tmp_path / f"k8s/overlays/{environment}/svc/{service}/kustomization.yaml"
    before = overlay.read_bytes()

    # when
    paths = release.prepare_release(
        tmp_path, service, REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST
    )

    # then
    assert len(paths) == 1
    if not initialization:
        assert paths == [overlay]
        assert "newTag: 1.2.3" in overlay.read_text()
    else:
        assert overlay.read_bytes() == before  # An active owner cannot bypass initialization.
        source, initialize, api = yaml.safe_load_all(paths[0].read_text())
        assert source["spec"]["ref"] == {"commit": SOURCE_COMMIT}
        assert source["metadata"]["name"].startswith(f"{environment}-{service}-release-")
        assert api["spec"]["dependsOn"] == [{"name": initialize["metadata"]["name"]}]
        assert api["metadata"]["name"] == f"{environment}-{service}-api"
        assert api["spec"]["sourceRef"] == initialize["spec"]["sourceRef"]
        assert api["spec"]["images"] == initialize["spec"]["images"]
        assert api["spec"]["images"][0]["digest"] == IMAGE_DIGEST
        assert (
            initialize["spec"]["path"]
            == f"./k8s/overlays/{environment}/svc/{service}/administration"
        )
        assert initialize["spec"]["postBuild"]["substitute"] == {"MIGRATION_TIMEOUT": "120s"}
        assert initialize["spec"]["healthChecks"][0]["namespace"] == environment + "-apps"


def test_prepare_retains_prior_release_prerequisites_and_replaces_only_api(
    tmp_path: Path,
) -> None:
    # given
    create_service(tmp_path, "claims", environment="staging", initialization=True)
    [bundle] = release.prepare_release(
        tmp_path, "claims", REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST
    )
    initial = list(yaml.safe_load_all(bundle.read_text()))
    initial_source, initial_prerequisite, initial_api = initial

    # when
    release.prepare_release(
        tmp_path, "claims", REPOSITORY, "1.2.4", NEXT_SOURCE_COMMIT, IMAGE_DIGEST
    )

    # then
    documents = list(yaml.safe_load_all(bundle.read_text()))
    assert len(documents) == 5
    assert documents[:2] == [initial_source, initial_prerequisite]
    assert documents[-1]["metadata"]["name"] == initial_api["metadata"]["name"]
    assert documents[-1] != initial_api
    assert documents[-1]["spec"]["dependsOn"] == [{"name": documents[-2]["metadata"]["name"]}]
    assert {
        item["metadata"]["annotations"]["insurance-hub.io/source-revision"]
        for item in documents[:-1]
    } == {SOURCE_COMMIT, NEXT_SOURCE_COMMIT}


def test_prepare_retains_failed_attempt_and_is_idempotent(tmp_path: Path) -> None:
    # given
    create_service(tmp_path, "claims", environment="staging", initialization=True)
    [bundle] = release.prepare_release(
        tmp_path, "claims", REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST
    )
    initial = list(yaml.safe_load_all(bundle.read_text()))
    configure(tmp_path, "claims", "staging", attempt=2)

    # when
    release.prepare_release(tmp_path, "claims", REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST)
    first_retry = bundle.read_bytes()
    release.prepare_release(tmp_path, "claims", REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST)

    # then
    documents = list(yaml.safe_load_all(bundle.read_text()))
    assert bundle.read_bytes() == first_retry
    assert len(documents) == 4
    assert documents[0] == initial[0]
    assert documents[1] == initial[1]
    assert documents[2]["metadata"]["name"].endswith("-a2")
    assert documents[3]["spec"]["dependsOn"] == [{"name": documents[2]["metadata"]["name"]}]


@pytest.mark.parametrize("corruption", ["unmanaged", "missing-source", "missing-api"])
def test_prepare_rejects_corrupt_retained_bundle_without_writing(
    tmp_path: Path, corruption: str
) -> None:
    # given
    create_service(tmp_path, "claims", environment="staging", initialization=True)
    [bundle] = release.prepare_release(
        tmp_path, "claims", REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST
    )
    documents = list(yaml.safe_load_all(bundle.read_text()))
    if corruption == "unmanaged":
        documents.insert(
            -1,
            {
                "apiVersion": "v1",
                "kind": "ConfigMap",
                "metadata": {"name": "manual", "namespace": "flux-system"},
            },
        )
    elif corruption == "missing-source":
        documents.pop(0)
    else:
        documents.pop()
    bundle.write_text(yaml.safe_dump_all(documents, explicit_start=True, sort_keys=False))
    before = bundle.read_bytes()

    # when / then
    with pytest.raises(release.ReleaseSelectionError):
        release.prepare_release(
            tmp_path, "claims", REPOSITORY, "1.2.4", NEXT_SOURCE_COMMIT, IMAGE_DIGEST
        )
    assert bundle.read_bytes() == before


def test_release_normalizes_timeout_and_retry_changes_only_attempt(tmp_path: Path) -> None:
    # given
    create_service(tmp_path, "claims", environment="staging", initialization=True)

    def identity():
        return release.service_release(tmp_path, "claims", "staging", SOURCE_COMMIT, IMAGE_DIGEST)

    initial = identity()

    # when / then
    for duration in ("2m", "120000000000ns"):
        configure(tmp_path, "claims", "staging", migrationTimeout=duration)
        assert identity() == initial
    configure(tmp_path, "claims", "staging", attempt=2)
    assert identity() == initial
    configure(tmp_path, "claims", "staging", migrationTimeout="90s")
    assert identity().release_id != initial.release_id


@pytest.mark.parametrize(
    "value", ["", "0s", "-1s", "181s", "forever", "0.0000000001s", "0.5ns0.5ns"]
)
def test_invalid_timeout_fails_before_output(tmp_path: Path, value: str) -> None:
    # given
    create_service(tmp_path, "claims", environment="staging", initialization=True)
    configure(tmp_path, "claims", "staging", migrationTimeout=value)

    # when / then
    with pytest.raises(release.ReleaseSelectionError):
        release.prepare_release(
            tmp_path, "claims", REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST
        )
    assert not (tmp_path / "k8s/flux").exists()


def test_combined_job_budget_rejects_valid_maximum_timeout(tmp_path: Path) -> None:
    # given
    create_service(tmp_path, "claims", environment="staging", initialization=True)
    configure(tmp_path, "claims", "staging", migrationTimeout="180s")
    path = tmp_path / "scripts/go/service/catalog.json"
    catalog = json.loads(path.read_text())
    catalog["services"]["claims"]["administration"]["jobDeadlineSeconds"] = 299
    path.write_text(json.dumps(catalog))

    # when / then
    with pytest.raises(
        release.ConfigurationError,
        match="migration, seed and minimum reserve budgets exceed the Job deadline",
    ):
        release.prepare_release(
            tmp_path, "claims", REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST
        )
    assert not (tmp_path / "k8s/flux").exists()


def test_normalized_timeout_rejects_combined_job_budget() -> None:
    # given
    policy = {
        "maxMigrationSeconds": 180,
        "seedSeconds": 60,
        "minimumJobReserveSeconds": 60,
        "jobDeadlineSeconds": 299,
    }

    # when / then
    with pytest.raises(
        release.ReleaseSelectionError,
        match="migration, seed and minimum reserve budgets exceed the Job deadline",
    ):
        release.normalized_timeout("180s", policy)


def test_target_validation_precedes_all_writes(tmp_path: Path) -> None:
    # given
    create_service(tmp_path, "claims", environment="staging")
    path = tmp_path / "scripts/go/service/catalog.json"
    catalog = json.loads(path.read_text())
    catalog["services"]["claims"]["releaseTargets"].append("unknown")
    path.write_text(json.dumps(catalog))
    overlay = tmp_path / "k8s/overlays/staging/svc/claims/kustomization.yaml"
    before = overlay.read_bytes()

    # when / then
    with pytest.raises(release.ConfigurationError, match="not registered"):
        release.prepare_release(
            tmp_path, "claims", REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST
        )
    assert overlay.read_bytes() == before


def test_collision_rejected_and_read_only_identity_cli(tmp_path: Path, monkeypatch, capsys) -> None:
    # given
    create_service(tmp_path, "claims", environment="staging", initialization=True)
    monkeypatch.setattr(release, "ROOT", tmp_path)
    expected = release.service_release(tmp_path, "claims", "staging", SOURCE_COMMIT, IMAGE_DIGEST)

    # when
    status = release.main(
        [
            "identity",
            "--service=claims",
            "--target=staging",
            f"--source-commit={SOURCE_COMMIT}",
            f"--image-digest={IMAGE_DIGEST}",
        ]
    )

    # then
    assert status == 0
    assert capsys.readouterr().out.strip() == expected.identity_sha256
    assert not (tmp_path / "k8s/flux").exists()
    paths = release.prepare_release(
        tmp_path, "claims", REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST
    )
    docs = list(yaml.safe_load_all(paths[0].read_text()))
    docs[0]["metadata"]["annotations"]["insurance-hub.io/release-identity"] = "f" * 64
    paths[0].write_text(yaml.safe_dump_all(docs))
    with pytest.raises(release.ReleaseSelectionError, match="collision"):
        release.prepare_release(
            tmp_path, "claims", REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST
        )


@pytest.mark.parametrize("bad", ["../outside.yaml", "/tmp/outside.yaml"])
def test_catalog_output_path_cannot_escape_checkout(tmp_path: Path, bad: str) -> None:
    # given
    create_service(tmp_path, "claims", environment="staging", initialization=True)
    path = tmp_path / "scripts/go/service/catalog.json"
    catalog = json.loads(path.read_text())
    catalog["services"]["claims"]["deploymentTargets"]["staging"]["flux"]["bundle"] = bad
    path.write_text(json.dumps(catalog))

    # when / then
    with pytest.raises(release.ConfigurationError, match="relative"):
        release.prepare_release(
            tmp_path, "claims", REPOSITORY, "1.2.3", SOURCE_COMMIT, IMAGE_DIGEST
        )


def test_prepare_cli_emits_only_selected_paths(tmp_path, monkeypatch):
    # given
    create_service(tmp_path, "claims", environment="staging", initialization=True)
    create_service(tmp_path, "product", initialization=False)
    output = tmp_path / "paths"
    other = tmp_path / "k8s/overlays/qa/svc/product/kustomization.yaml"
    before = other.read_bytes()
    monkeypatch.setattr(release, "ROOT", tmp_path)

    # when
    result = release.main(
        [
            "prepare",
            "--service=claims",
            f"--repository={REPOSITORY}",
            "--version=1.2.3",
            f"--source-commit={SOURCE_COMMIT}",
            f"--image-digest={IMAGE_DIGEST}",
            f"--output={output}",
        ]
    )

    # then
    assert result == 0
    assert output.read_bytes() == b"k8s/flux/staging/releases/claims/release.yaml\0"
    assert other.read_bytes() == before
