"""Fast tests for renamed scaffold copy decisions."""

import subprocess
import time
from pathlib import Path

import pytest
import shared_phase

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
    assert "check-foundations" not in result
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
    assert "check-foundations" not in result
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
        go="go",
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
    assert not (copy_root / "scripts" / "check_foundations.py").exists()
    assert not (copy_root / "scripts" / "test_check_foundations.py").exists()
    copied_tooling = (copy_root / "scripts" / "test_tooling.py").read_text(encoding="utf-8")
    assert "api/examplecopy/v1/example_service.proto" in copied_tooling
    assert "gen/examplecopy/v1" in copied_tooling
    assert "api/scaffold/v1/example_service.proto" not in copied_tooling
    assert "gen/scaffold/v1" not in copied_tooling
    assert "github.com/igor-baiborodine/insurance-hub/libs/servicekit v1.0.1" in (
        copy_root / "go.mod"
    ).read_text(encoding="utf-8")


def test_copy_rejects_missing_shared_dependency(tmp_path: Path) -> None:
    # given
    module_root = Path(check_copy.__file__).resolve().parent.parent
    config = check_copy.Configuration(
        module_root=module_root,
        repository_root=module_root.parents[1],
        common_tooling_dir=module_root.parents[1] / "scripts" / "go" / "module",
        make_arguments=("make",),
        selected_python=Path(__file__).absolute(),
        go="go",
        uv="uv",
    )
    copy_root = check_copy.create_copy(config, tmp_path)
    manifest = copy_root / "go.mod"
    manifest.write_text(
        manifest.read_text(encoding="utf-8").replace(
            "github.com/igor-baiborodine/insurance-hub/libs/servicekit v1.0.1",
            "github.com/igor-baiborodine/insurance-hub/libs/servicekit v0.1.0",
        ),
        encoding="utf-8",
    )

    # when / then
    with pytest.raises(check_copy.CopyError, match="exact shared dependency"):
        check_copy.validate_copy_surface(copy_root)


def test_run_copy_make_forwards_selected_tool_overrides(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    config = check_copy.Configuration(
        module_root=tmp_path,
        repository_root=tmp_path,
        common_tooling_dir=tmp_path,
        make_arguments=("make",),
        selected_python=Path(__file__).absolute(),
        go="/controlled/go",
        uv="/controlled/uv",
    )
    captured: dict[str, object] = {}

    def capture(arguments: list[str], **options: object) -> None:
        captured["arguments"] = arguments
        captured["options"] = options

    monkeypatch.setattr(check_copy, "run_command", capture)

    # when
    check_copy.run_copy_make(
        config,
        tmp_path,
        time.monotonic() + 10,
        "controlled Make",
        "check",
    )

    # then
    assert "GO=/controlled/go" in captured["arguments"]
    assert "UV=/controlled/uv" in captured["arguments"]


def test_run_copy_make_selects_copied_candidate_manifest(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    library = tmp_path / "libs" / "servicekit"
    library.mkdir(parents=True)
    copy_root = tmp_path / "services" / "example-copy"
    copy_root.mkdir(parents=True)
    config = check_copy.Configuration(
        module_root=tmp_path,
        repository_root=tmp_path,
        common_tooling_dir=tmp_path,
        make_arguments=("make",),
        selected_python=Path(__file__).absolute(),
        go="go",
        uv="uv",
        candidate_library=library,
    )
    captured: dict[str, object] = {}

    def capture(arguments: list[str], **options: object) -> None:
        captured["arguments"] = arguments
        captured["options"] = options

    monkeypatch.setattr(check_copy, "run_command", capture)

    # when
    check_copy.run_copy_make(
        config,
        copy_root,
        time.monotonic() + 10,
        "candidate Make",
        "test-race",
        candidate=True,
    )

    # then
    environment = captured["options"]["env"]
    assert environment["GOFLAGS"] == f"-mod=readonly -modfile={copy_root / 'candidate.mod'}"
    assert environment["SERVICEKIT_CANDIDATE_LIBRARY"] == str(library)


def test_documented_copy_removes_complete_check_copy_target(tmp_path: Path) -> None:
    # given
    module_root = Path(check_copy.__file__).resolve().parent.parent
    copying = (module_root / "COPYING.md").read_text(encoding="utf-8")
    start = copying.index("sed -i 's/ check-copy//g; s/ check-foundations//g' Makefile")
    end = copying.index("\nrm scripts/check_copy.py", start)
    documented_commands = copying[start:end]
    makefile = tmp_path / "Makefile"
    makefile.write_text((module_root / "Makefile").read_text(encoding="utf-8"), encoding="utf-8")

    # when
    subprocess.run(
        ["sh", "-eu", "-c", documented_commands],
        cwd=tmp_path,
        capture_output=True,
        text=True,
        timeout=10,
        check=True,
    )

    # then
    result = makefile.read_text(encoding="utf-8")
    assert "check-copy" not in result
    assert "check-foundations" not in result
    assert '"$(PYTHON)" ./scripts/check_copy.py' not in result
    assert '"$(PYTHON)" ./scripts/tooling_runner.py' in result


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


def test_candidate_phase_uses_issued_environment_and_receipts(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    calls: list[tuple[str, str]] = []
    captured: dict[str, object] = {}

    def request(operation: str, phase: str) -> dict:
        calls.append((operation, phase))
        return {"environment": {"GOFLAGS": "-mod=readonly -modfile=/issued/candidate.mod"}}

    def run(_arguments: list[str], **options: object) -> None:
        captured.update(options)

    monkeypatch.setattr(shared_phase, "context_request", request)
    monkeypatch.setattr(shared_phase, "run_command", run)

    # when
    status = shared_phase.main(["copy"])

    # then
    assert status == 0
    assert calls == [("begin", "copy"), ("end", "copy")]
    environment = captured["env"]
    assert environment["GOFLAGS"] == "-mod=readonly -modfile=/issued/candidate.mod"
    assert environment["SERVICEKIT_CANDIDATE_LIBRARY"] == str(
        Path.cwd().parents[1] / "libs" / "servicekit"
    )


def test_candidate_phase_does_not_receipt_failed_child(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    calls: list[tuple[str, str]] = []

    def request(operation: str, phase: str) -> dict:
        calls.append((operation, phase))
        return {"environment": {}}

    def fail(_arguments: list[str], **_options: object) -> None:
        raise RuntimeError("owned child failed")

    monkeypatch.setattr(shared_phase, "context_request", request)
    monkeypatch.setattr(shared_phase, "run_command", fail)

    # when
    status = shared_phase.main(["generated"])

    # then
    assert status == 1
    assert calls == [("begin", "generated")]


def test_candidate_phase_maps_termination_and_withholds_receipt(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    calls: list[tuple[str, str]] = []

    def request(operation: str, phase: str) -> dict:
        calls.append((operation, phase))
        return {"environment": {}}

    def terminate(_arguments: list[str], **_options: object) -> None:
        shared_phase._terminate(15, None)

    monkeypatch.setattr(shared_phase, "context_request", request)
    monkeypatch.setattr(shared_phase, "run_command", terminate)

    # when
    status = shared_phase.main(["breaking"])

    # then
    assert status == 143
    assert calls == [("begin", "breaking")]
