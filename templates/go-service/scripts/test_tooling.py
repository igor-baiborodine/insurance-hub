"""Disposable integration tests for Go scaffold validation targets."""

from __future__ import annotations

import hashlib
import os
import shutil
import sys
import time
from dataclasses import dataclass
from pathlib import Path

import pytest

from scaffold_process import CommandError, child_environment, run_command

pytestmark = pytest.mark.integration
TOOL_NAMES = ("buf", "protoc-gen-go", "protoc-gen-go-grpc", "golangci-lint", "govulncheck")
IGNORED_DIRECTORIES = {".git", ".tools", ".venv", "__pycache__", ".pytest_cache", ".ruff_cache"}


@dataclass(frozen=True)
class Tooling:
    make_arguments: tuple[str, ...]
    repository_root: Path
    temporary_root: Path
    base_fixture: Path
    deadline: float


def _ignore(_: str, names: list[str]) -> set[str]:
    return {name for name in names if name in IGNORED_DIRECTORIES}


def inventory(root: Path) -> dict[str, str]:
    result: dict[str, str] = {}
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root)
        if not path.is_file() or any(part in IGNORED_DIRECTORIES for part in relative.parts):
            continue
        result[relative.as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


@pytest.fixture(scope="module")
def tooling(tmp_path_factory: pytest.TempPathFactory) -> Tooling:
    module_root = Path(os.environ["MODULE_ROOT"]).resolve()
    repository_root = Path(os.environ["INSURANCE_HUB_ROOT"]).resolve()
    common_tooling = Path(os.environ["COMMON_GO_TOOLING_DIR"]).resolve()
    tools_bin = Path(os.environ["TOOLS_BIN"]).resolve()
    make_arguments = tuple(os.environ.get("MAKE_COMMAND", "make").split())
    assert make_arguments
    for tool in TOOL_NAMES:
        assert (tools_bin / tool).is_file(), f"missing pinned tool: {tools_bin / tool}"

    temporary_root = tmp_path_factory.mktemp("go-service-tooling")
    base = temporary_root / "base"
    shutil.copytree(module_root, base, ignore=_ignore)
    shutil.copytree(common_tooling, base / ".common-go-tooling", ignore=_ignore)
    private_bin = base / ".tools" / "bin"
    private_bin.mkdir(parents=True)
    for tool in TOOL_NAMES:
        (private_bin / tool).symlink_to(tools_bin / tool)
    return Tooling(make_arguments, repository_root, temporary_root, base, time.monotonic() + 3_600)


def new_fixture(tooling: Tooling, name: str) -> Path:
    fixture = tooling.temporary_root / name
    shutil.copytree(tooling.base_fixture, fixture, symlinks=True)
    return fixture


def make_environment() -> dict[str, str]:
    return child_environment(
        {
            "PYTHON": sys.executable,
            "COMMON_GO_TOOLING_PYTHON": sys.executable,
            "PYTHONDONTWRITEBYTECODE": "1",
        }
    )


def run_target(
    tooling: Tooling,
    fixture: Path,
    name: str,
    target: str,
    *variables: str,
    expect_failure: str | None = None,
) -> None:
    before = inventory(fixture)
    log = tooling.temporary_root / "logs" / f"{name}.log"
    arguments = [
        *tooling.make_arguments,
        "--no-print-directory",
        "-C",
        str(fixture),
        target,
        *variables,
        f"INSURANCE_HUB_ROOT={tooling.repository_root}",
        f"COMMON_GO_TOOLING_DIR={fixture / '.common-go-tooling'}",
    ]
    remaining = tooling.deadline - time.monotonic()
    if remaining <= 0:
        pytest.fail("test-tooling exceeded its 3600-second overall deadline")
    if expect_failure is None:
        try:
            run_command(
                arguments,
                cwd=fixture,
                env=make_environment(),
                timeout_seconds=min(1_200, remaining),
                teardown_seconds=15,
                activity=f"test-tooling {name}",
                log_path=log,
            )
        except CommandError as error:
            pytest.fail(
                f"{name} could not establish a passing {target} baseline: {error}\n"
                f"{log.read_text(encoding='utf-8')}"
            )
    else:
        with pytest.raises(CommandError):
            run_command(
                arguments,
                cwd=fixture,
                env=make_environment(),
                timeout_seconds=min(1_200, remaining),
                teardown_seconds=15,
                activity=f"test-tooling {name}",
                log_path=log,
            )
        assert expect_failure in log.read_text(encoding="utf-8")
    assert before == inventory(fixture), f"{name} mutated its disposable checkout"


def mutate_fixture(fixture: Path, case: str) -> tuple[str, str, tuple[str, ...]]:
    if case == "format-new-file":
        (fixture / "tooling_format_defect.go").write_text(
            "package tooling\nfunc formatDefect( ){}\n", encoding="utf-8"
        )
        return (
            "format-check",
            "tooling_format_defect.go",
            ("FORMAT_FILES=tooling_format_defect.go",),
        )
    if case == "lint-only":
        path = fixture / "internal" / "config" / "tooling_lint_defect.go"
        path.write_text(
            'package config\n\nimport "os"\n\nfunc toolingLintDefect() { os.Chdir(".") }\n',
            encoding="utf-8",
        )
        return "lint", "errcheck", ()
    if case == "test":
        path = fixture / "internal" / "config" / "tooling_failure_test.go"
        path.write_text(
            'package config\n\nimport "testing"\n\n'
            'func TestToolingControlledFailure(t *testing.T) { t.Fatal("controlled test failure") }\n',
            encoding="utf-8",
        )
        return "test", ": test] Error", ()
    if case == "dependency":
        path = fixture / "go.mod"
        path.write_text(
            path.read_text(encoding="utf-8").replace(
                "google.golang.org/grpc v1.83.2", "google.golang.org/grpc v1.84.0"
            ),
            encoding="utf-8",
        )
        return "check-deps", "go.mod is not synchronized with update-deps", ()
    if case == "proto-format":
        path = fixture / "api" / "scaffold" / "v1" / "example_service.proto"
        path.write_text(
            path.read_text(encoding="utf-8").replace("  rpc Echo", "rpc Echo"),
            encoding="utf-8",
        )
        return "format-proto-check", "rpc Echo", ()
    if case == "proto-lint":
        path = fixture / "api" / "scaffold" / "v1" / "example_service.proto"
        path.write_text(
            path.read_text(encoding="utf-8").replace("string message = 1", "string BadName = 1", 1),
            encoding="utf-8",
        )
        return "lint-proto", "should be lower_snake_case", ()
    if case == "proto-breaking":
        path = fixture / "api" / "scaffold" / "v1" / "example_service.proto"
        content = path.read_text(encoding="utf-8")
        start = content.index("message EchoResponse {")
        path.write_text(
            content[:start]
            + content[start:].replace("string message = 1;", "string message = 2;", 1),
            encoding="utf-8",
        )
        return "check-proto-breaking", 'Previously present field "1"', ()
    generated = fixture / "gen" / "scaffold" / "v1"
    if case == "generated-modified":
        path = generated / "example_service.pb.go"
        path.write_text(
            path.read_text(encoding="utf-8") + "\n// controlled drift\n", encoding="utf-8"
        )
        return "check-proto-drift", "generated output differs from regeneration", ()
    if case == "generated-unexpected":
        (generated / "unexpected.txt").write_text(
            "controlled unexpected output\n", encoding="utf-8"
        )
        return "check-proto-drift", "unexpected output in generated tree", ()
    if case == "generated-missing":
        (generated / "example_service_grpc.pb.go").unlink()
        return "check-proto-drift", "missing expected output", ()
    if case == "generated-stale":
        (generated / "removed_service.pb.go").write_text(
            "// Code generated by controlled tooling test. DO NOT EDIT.\n\npackage scaffoldv1\n",
            encoding="utf-8",
        )
        return "check-proto-drift", "stale generated output is no longer declared", ()
    raise AssertionError(f"unknown controlled case: {case}")


def test_non_race_test_target_passes_without_mutation(tooling: Tooling) -> None:
    fixture = new_fixture(tooling, "test-baseline")
    run_target(tooling, fixture, "test-baseline", "test")


@pytest.mark.parametrize(
    "case",
    [
        "format-new-file",
        "lint-only",
        "test",
        "dependency",
        "proto-format",
        "proto-lint",
        "proto-breaking",
        "generated-modified",
        "generated-unexpected",
        "generated-missing",
        "generated-stale",
    ],
)
def test_controlled_defect_is_rejected_without_mutation(tooling: Tooling, case: str) -> None:
    # given
    fixture = new_fixture(tooling, case)
    target, diagnostic, variables = mutate_fixture(fixture, case)

    # when / then
    run_target(tooling, fixture, case, target, *variables, expect_failure=diagnostic)


def test_scaffold_pr_check_passes_without_coverage(tooling: Tooling) -> None:
    # given
    fixture = new_fixture(tooling, "scaffold-pr-check")

    # when
    run_target(tooling, fixture, "scaffold-pr-check", "check-go-pr", "FORMAT_SCOPE=all")

    # then
    log = (tooling.temporary_root / "logs" / "scaffold-pr-check.log").read_text(encoding="utf-8")
    assert "-coverprofile=" not in log
    assert not (fixture / "coverage.txt").exists()
