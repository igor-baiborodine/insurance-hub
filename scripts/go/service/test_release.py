"""Tests for generic Go release selection and QA semantic-tag updates."""

from __future__ import annotations

from pathlib import Path

import pytest

import release

REPOSITORY = "igor-baiborodine/insurance-hub"


def create_service(root: Path, service: str, *, qa_entry: bool = True) -> None:
    module = root / "services" / f"{service}-service"
    module.mkdir(parents=True)
    (module / "Dockerfile").write_text("FROM scratch\n", encoding="utf-8")
    overlay = root / "k8s" / "overlays" / "qa" / "svc" / service
    overlay.mkdir(parents=True)
    image = service if qa_entry else "other"
    (overlay / "kustomization.yaml").write_text(
        "apiVersion: kustomize.config.k8s.io/v1beta1\n"
        "kind: Kustomization\n"
        "images:\n"
        f"  - name: insurance-hub-{image}-api\n"
        f"    newName: ghcr.io/{REPOSITORY}/{image}-api\n"
        "    newTag: sha-unpublished\n",
        encoding="utf-8",
    )


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
    assert selected.qa_kustomization == f"k8s/overlays/qa/svc/{service}/kustomization.yaml"


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


def test_update_changes_only_the_selected_semantic_tag(tmp_path: Path) -> None:
    # given
    create_service(tmp_path, "product")

    # when
    path = release.update_qa_tag(tmp_path, "product", REPOSITORY, "1.2.3")

    # then
    text = path.read_text(encoding="utf-8")
    assert "newTag: 1.2.3" in text
    assert "sha-unpublished" not in text
    with pytest.raises(release.ReleaseSelectionError):
        release.update_qa_tag(tmp_path, "product", REPOSITORY, "2.0.0")
