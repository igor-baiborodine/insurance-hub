"""Focused disposable tests for the Chat module's owned validation targets."""

from __future__ import annotations

import hashlib
import json
import os
import shlex
import shutil
import sys
from dataclasses import dataclass
from pathlib import Path

import pytest

from scripts.python.process_runner import CommandError, child_environment, run_command

pytestmark = pytest.mark.integration
TOOL_NAMES = ("golangci-lint", "govulncheck")
IGNORED_DIRECTORIES = {".git", ".tools", ".venv", "__pycache__", ".pytest_cache"}


@dataclass(frozen=True)
class Tooling:
    make: tuple[str, ...]
    module_root: Path
    repository_root: Path
    tools_bin: Path
    go: str
    uv: str


def inventory(root: Path) -> dict[str, str]:
    """Hash source files while excluding disposable caches and local tools."""
    result: dict[str, str] = {}
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root)
        if not path.is_file() or any(part in IGNORED_DIRECTORIES for part in relative.parts):
            continue
        result[relative.as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


@pytest.fixture(scope="module")
def tooling() -> Tooling:
    module_root = Path(os.environ["MODULE_ROOT"]).resolve(strict=True)
    repository_root = Path(os.environ["INSURANCE_HUB_ROOT"]).resolve(strict=True)
    tools_bin = Path(os.environ["TOOLS_BIN"]).resolve(strict=True)
    for tool in TOOL_NAMES:
        assert (tools_bin / tool).is_file(), f"missing pinned tool: {tool}"
    make = tuple(shlex.split(os.environ.get("MAKE_COMMAND", "make")))
    assert make
    return Tooling(
        make=make,
        module_root=module_root,
        repository_root=repository_root,
        tools_bin=tools_bin,
        go=os.environ.get("GO", "go"),
        uv=os.environ.get("UV", "uv"),
    )


def fixture_copy(tooling: Tooling, tmp_path: Path) -> Path:
    fixture = tmp_path / "services" / "chat-service"
    shutil.copytree(
        tooling.module_root,
        fixture,
        ignore=shutil.ignore_patterns(*IGNORED_DIRECTORIES),
    )
    private_bin = fixture / ".tools" / "bin"
    private_bin.mkdir(parents=True)
    for tool in TOOL_NAMES:
        (private_bin / tool).symlink_to(tooling.tools_bin / tool)
    return fixture


def mutate(fixture: Path, case: str) -> tuple[str, str, tuple[str, ...]]:
    if case == "dockerfile-config":
        path = fixture / "go-service-tooling.json"
        data = json.loads(path.read_text(encoding="utf-8"))
        data["dockerfile"]["instruction"] = "RUN controlled"  # type: ignore[index]
        path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")
        return "dockerfile-check", "unknown keys: instruction", ()
    if case == "format":
        path = fixture / "tooling_format_defect.go"
        path.write_text("package tooling\nfunc formatDefect( ){}\n", encoding="utf-8")
        return (
            "format-check",
            "tooling_format_defect.go",
            ("FORMAT_FILES=tooling_format_defect.go",),
        )
    if case == "lint":
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
            'func TestTooling_ControlledFailure(t *testing.T) { t.Fatal("controlled failure") }\n',
            encoding="utf-8",
        )
        return "test", ": test] Error", ("TEST_PACKAGES=./internal/config",)
    if case == "dependency":
        path = fixture / "go.mod"
        path.write_text(
            path.read_text(encoding="utf-8").replace(
                "libs/servicekit v1.1.0", "libs/servicekit v1.0.0", 1
            ),
            encoding="utf-8",
        )
        return "check-deps", "go.mod is not synchronized with update-deps", ()
    raise AssertionError(f"unknown controlled defect: {case}")


@pytest.mark.parametrize("case", ("dockerfile-config", "format", "lint", "test", "dependency"))
def test_owned_target_rejects_controlled_defect(
    tooling: Tooling,
    tmp_path: Path,
    case: str,
) -> None:
    # given
    fixture = fixture_copy(tooling, tmp_path)
    target, expected, variables = mutate(fixture, case)
    before = inventory(fixture)
    log = tmp_path / f"{case}.log"
    arguments = [
        *tooling.make,
        "--no-print-directory",
        "-C",
        str(fixture),
        target,
        *variables,
        f"GO={tooling.go}",
        f"UV={tooling.uv}",
        f"INSURANCE_HUB_ROOT={tooling.repository_root}",
        f"TOOLING_REPOSITORY_ROOT={fixture.parents[1]}",
    ]
    environment = child_environment(
        {"PYTHON": sys.executable, "PYTHONDONTWRITEBYTECODE": "1"},
        preferred_executables=(tooling.go,),
    )

    # when / then
    with pytest.raises(CommandError):
        run_command(
            arguments,
            cwd=fixture,
            env=environment,
            timeout_seconds=1_200,
            teardown_seconds=15,
            activity=f"Chat tooling {case}",
            log_path=log,
        )

    # then
    assert expected in log.read_text(encoding="utf-8")
    assert inventory(fixture) == before
