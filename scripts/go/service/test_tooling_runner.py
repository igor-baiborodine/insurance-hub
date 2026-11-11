"""Tests for the generic Go-service Make runner."""

from __future__ import annotations

from pathlib import Path

import pytest

from scripts.go.service.tooling_config import RunnerConfig, ToolingConfigError
from scripts.go.service.tooling_runner import execute_target


def test_execute_target_uses_bounded_module_owned_make_command(tmp_path: Path) -> None:
    # given
    module = tmp_path / "example-service"
    module.mkdir()
    (module / "Makefile").write_text("test-tooling:\n\t@true\n", encoding="utf-8")
    calls: list[tuple[tuple[object, ...], dict[str, object]]] = []

    def fake_executor(*arguments: object, **options: object) -> str:
        calls.append((arguments, options))
        return ""

    # when
    execute_target(
        module,
        RunnerConfig(target="test-tooling", timeout_seconds=45),
        environment={"GOTOOLCHAIN": "local"},
        make="/tools/make",
        executor=fake_executor,
    )

    # then
    assert calls[0][0] == (
        ["/tools/make", "--no-print-directory", "-C", str(module), "test-tooling"],
    )
    assert calls[0][1]["cwd"] == module
    assert calls[0][1]["timeout_seconds"] == 45
    assert calls[0][1]["activity"] == "example-service test-tooling"
    assert calls[0][1]["env"]["GOTOOLCHAIN"] == "local"  # type: ignore[index]


def test_execute_target_rejects_missing_makefile_before_executor(tmp_path: Path) -> None:
    # given
    calls = 0

    def fake_executor(*_arguments: object, **_options: object) -> str:
        nonlocal calls
        calls += 1
        return ""

    # when / then
    with pytest.raises(ToolingConfigError, match="does not own a Makefile"):
        execute_target(
            tmp_path,
            RunnerConfig(target="test-tooling", timeout_seconds=45),
            executor=fake_executor,
        )
    assert calls == 0


def test_execute_target_rejects_unvalidated_runner_before_executor(tmp_path: Path) -> None:
    # given
    (tmp_path / "Makefile").write_text("safe:\n\t@true\n", encoding="utf-8")
    calls = 0

    def fake_executor(*_arguments: object, **_options: object) -> str:
        nonlocal calls
        calls += 1
        return ""

    # when / then
    with pytest.raises(ToolingConfigError, match="target is invalid"):
        execute_target(
            tmp_path,
            RunnerConfig(target="unsafe target", timeout_seconds=45),
            executor=fake_executor,
        )
    assert calls == 0
