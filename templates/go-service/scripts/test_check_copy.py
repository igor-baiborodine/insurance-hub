"""Fast tests for renamed scaffold copy decisions."""

from pathlib import Path

import pytest

import check_copy


def test_remove_make_target_removes_phony_name_and_recipe() -> None:
    # given
    content = (
        ".PHONY: check test-tooling check-copy run\n"
        "check-copy: verify-tools\n\t@run-copy\n\nrun:\n\t@run\n"
    )

    # when
    result = check_copy.remove_make_target(content, "check-copy")

    # then
    assert "check-copy" not in result
    assert "\n: verify-tools\n" not in result
    assert "run:\n\t@run" in result


def test_rewrite_readme_replaces_scaffold_only_delegate_section() -> None:
    # given
    content = (
        "# Service\n\n| `make check-copy` | proof |\n\n### Root scaffold delegates\n\n"
        "old root text\n\n## Create a repository-owned service\n\nkept\n"
    )

    # when
    result = check_copy.rewrite_readme(content)

    # then
    assert "check-copy" not in result
    assert "### Repository onboarding" in result
    assert "old root text" not in result
    assert result.endswith("## Create a repository-owned service\n\nkept\n")


def test_inventory_excludes_environments_and_caches(tmp_path: Path) -> None:
    # given
    (tmp_path / "kept.txt").write_text("kept", encoding="utf-8")
    for directory in (".tools", ".venv", "__pycache__", ".pytest_cache", ".ruff_cache"):
        path = tmp_path / directory
        path.mkdir()
        (path / "ignored.txt").write_text("ignored", encoding="utf-8")

    # when
    result = check_copy.inventory(tmp_path)

    # then
    assert set(result) == {"kept.txt"}


def test_configuration_rejects_missing_required_environment(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    monkeypatch.setenv("INSURANCE_HUB_ROOT", str(Path(__file__).resolve().parents[3]))
    monkeypatch.delenv("COMMON_GO_TOOLING_DIR", raising=False)
    monkeypatch.delenv("COMMON_GO_TOOLING_PYTHON", raising=False)

    # when / then
    with pytest.raises(check_copy.CopyError, match="COMMON_GO_TOOLING_DIR is required"):
        check_copy.configuration()


def test_created_copy_uses_repository_tooling_without_scaffold_only_surface(
    tmp_path: Path,
) -> None:
    # given
    module_root = Path(check_copy.__file__).resolve().parent.parent
    common_tooling = module_root / ".common-go-tooling"
    if not common_tooling.is_dir():
        common_tooling = module_root.parents[1] / "scripts" / "go" / "module"
    config = check_copy.Configuration(
        module_root=module_root,
        repository_root=module_root.parents[1],
        common_tooling_dir=common_tooling,
        make_arguments=("make",),
        selected_python=Path(__file__).absolute(),
        uv="uv",
    )

    # when
    copy_root = check_copy.create_copy(config, tmp_path)

    # then
    check_copy.validate_copy_surface(copy_root)
    assert not (copy_root / ".venv").exists()
    assert not (copy_root / ".tools").exists()
    assert not (copy_root / ".python-version").exists()
    assert not (copy_root / "pyproject.toml").exists()
    assert not (copy_root / "uv.lock").exists()
    assert not (copy_root / "scripts" / "test_check_copy.py").exists()
    copied_tooling = (copy_root / "scripts" / "test_tooling.py").read_text(encoding="utf-8")
    assert "api/examplecopy/v1/example_service.proto" in copied_tooling
    assert "gen/examplecopy/v1" in copied_tooling
    assert "api/scaffold/v1/example_service.proto" not in copied_tooling
    assert "gen/scaffold/v1" not in copied_tooling


@pytest.mark.parametrize(
    ("interruption", "expected"),
    [(check_copy.Terminated(143), 143), (KeyboardInterrupt(), 130)],
)
def test_main_maps_interruption_statuses(
    monkeypatch: pytest.MonkeyPatch,
    interruption: BaseException,
    expected: int,
) -> None:
    # given
    monkeypatch.setattr(check_copy, "configuration", lambda: object())

    def interrupt(_configuration: object) -> None:
        raise interruption

    monkeypatch.setattr(check_copy, "execute", interrupt)

    # when
    status = check_copy.main([])

    # then
    assert status == expected
