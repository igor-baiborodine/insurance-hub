"""Generic bounded Make runner for Go-service tooling suites."""

from __future__ import annotations

from collections.abc import Callable, Mapping
from pathlib import Path

from scripts.go.service.tooling_config import NAME, RunnerConfig, ToolingConfigError
from scripts.go.service.tooling_process import run
from scripts.python.process_runner import child_environment

Executor = Callable[..., str]


def execute_target(
    module_root: Path,
    runner: RunnerConfig,
    *,
    environment: Mapping[str, str] | None = None,
    make: str = "make",
    executor: Executor = run,
) -> None:
    """Execute one validated module-owned Make target with bounded process behavior."""
    try:
        root = module_root.resolve(strict=True)
    except OSError as error:
        raise ToolingConfigError("runner module root is missing") from error
    if not (root / "Makefile").is_file():
        raise ToolingConfigError("runner module root does not own a Makefile")
    if not NAME.fullmatch(runner.target):
        raise ToolingConfigError("runner target is invalid")
    if not isinstance(runner.timeout_seconds, int) or not 1 <= runner.timeout_seconds <= 3600:
        raise ToolingConfigError("runner timeout is invalid")
    command = [make, "--no-print-directory", "-C", str(root), runner.target]
    child_env = child_environment(environment, preferred_executables=(make,))
    executor(
        command,
        cwd=root,
        env=child_env,
        timeout_seconds=runner.timeout_seconds,
        activity=f"{root.name} {runner.target}",
    )
