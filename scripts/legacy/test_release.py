"""Common legacy CI selection and structured update contracts."""

from __future__ import annotations

from pathlib import Path

import pytest

import release


@pytest.mark.parametrize(
    ("paths", "kind", "expected"),
    [
        (["legacy/policy-service/src/A.java"], "service", "policy-service"),
        (["legacy/policy-service-api/pom.xml"], "api", "policy-service-api"),
        (["docs/README.md"], "pr", None),
    ],
)
def test_select_single_module(paths: list[str], kind: str, expected: str | None) -> None:
    # when
    selected = release.select(paths, kind)
    # then
    assert selected == expected


def test_select_rejects_multiple_modules() -> None:
    # when / then
    with pytest.raises(ValueError, match="multiple legacy modules"):
        release.select(["legacy/auth-service/pom.xml", "legacy/policy-service/pom.xml"], "pr")


def test_select_jar_rejects_ambiguous_artifacts(tmp_path: Path) -> None:
    # given
    (tmp_path / "command-bus-1.jar").touch()
    (tmp_path / "command-bus-2.jar").touch()
    (tmp_path / "command-bus-1-sources.jar").touch()
    # when / then
    with pytest.raises(ValueError, match="expected one deployable JAR"):
        release.select_jar("command-bus", tmp_path)


def test_select_jar_ignores_sources(tmp_path: Path) -> None:
    # given
    expected = tmp_path / "policy-service-api-1.0.1.jar"
    expected.touch()
    (tmp_path / "policy-service-api-1.0.1-sources.jar").touch()
    # when
    selected = release.select_jar("policy-service-api", tmp_path)
    # then
    assert selected == expected


def test_manifest_updates_only_expected_container(tmp_path: Path) -> None:
    # given
    path = tmp_path / "deployment.yaml"
    path.write_text(
        "kind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n"
        "        - name: web-vue\n          image: ghcr.io/owner/repo/web-vue-legacy:1.0.1\n"
        "          # keep comment\n",
        encoding="utf-8",
    )
    # when
    changed = release.update_manifest(path, "ghcr.io/owner/repo/web-vue-legacy", "1.0.2")
    # then
    assert changed
    assert "web-vue-legacy:1.0.2" in path.read_text(encoding="utf-8")
    assert "# keep comment" in path.read_text(encoding="utf-8")


def test_pom_updates_only_matching_dependency() -> None:
    # given
    source = (
        '<project xmlns="http://maven.apache.org/POM/4.0.0"><dependencies>'
        "<dependency><artifactId>policy-service-api</artifactId><version>1.0.1</version></dependency>"
        "<dependency><artifactId>other</artifactId><version>2.0.0</version></dependency>"
        "</dependencies></project>"
    )
    # when
    revised = release.update_pom(source, "policy-service-api", "1.0.2")
    # then
    assert revised is not None
    assert "<version>1.0.2</version>" in revised
    assert "<artifactId>other</artifactId><version>2.0.0</version>" in revised
    assert release.update_pom(revised, "policy-service-api", "1.0.2") is None


@pytest.mark.parametrize(
    "module,image",
    [
        ("documents-service", "document-api-legacy"),
        ("agent-portal-gateway", "agent-portal-gateway-legacy"),
        ("policy-service", "policy-api-legacy"),
    ],
)
def test_image_mapping(module: str, image: str) -> None:
    assert release.image_name(module) == image
