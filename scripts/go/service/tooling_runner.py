"""Generic bounded Make and pytest runners for Go-service tooling suites."""

from __future__ import annotations

import argparse
import signal
import sys
from collections.abc import Callable, Mapping, Sequence
from pathlib import Path

import pytest

from scripts.go.service.tooling_config import (
    NAME,
    RunnerConfig,
    ToolingConfigError,
    load_tooling_config,
)
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


def suite_arguments(module_root: Path, repository_root: Path, runner: RunnerConfig) -> list[str]:
    """Build the constrained pytest arguments for one module-owned integration suite."""
    suite = (module_root / runner.suite_path).resolve(strict=True)
    if not suite.is_relative_to(module_root) or not suite.is_file():
        raise ToolingConfigError("runner suite path escapes the module")
    pyproject = repository_root / "pyproject.toml"
    if not pyproject.is_file():
        raise ToolingConfigError("repository pyproject.toml is missing")
    return [
        "-q",
        "-p",
        "no:cacheprovider",
        "-c",
        str(pyproject),
        "--rootdir",
        str(module_root),
        "-o",
        "tmp_path_retention_policy=none",
        "-m",
        "integration",
        str(suite),
    ]


def run_suite(
    module_root: Path,
    repository_root: Path,
    runner: RunnerConfig,
    *,
    pytest_main: Callable[[Sequence[str]], int | pytest.ExitCode] = pytest.main,
) -> int:
    """Run one declared pytest suite and translate owned termination signals."""
    received_signal: int | None = None

    def handle_signal(signum: int, _frame: object) -> None:
        nonlocal received_signal
        if received_signal is None:
            received_signal = signum
        raise KeyboardInterrupt

    previous_interrupt = signal.signal(signal.SIGINT, handle_signal)
    previous_terminate = signal.signal(signal.SIGTERM, handle_signal)
    try:
        try:
            status = int(pytest_main(suite_arguments(module_root, repository_root, runner)))
        except KeyboardInterrupt:
            received_signal = received_signal or signal.SIGINT
            status = 2
    finally:
        signal.signal(signal.SIGINT, previous_interrupt)
        signal.signal(signal.SIGTERM, previous_terminate)
    if received_signal is not None:
        print(
            f"test-tooling: interrupted by {signal.Signals(received_signal).name}",
            file=sys.stderr,
        )
        return 128 + received_signal
    return status


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True, type=Path)
    parser.add_argument("--repository-root", required=True, type=Path)
    parser.add_argument("--tooling-root", type=Path)
    parser.add_argument("action", choices=("suite", "target"))
    arguments = parser.parse_args(argv)
    try:
        config = load_tooling_config(
            arguments.config,
            repository_root=arguments.repository_root,
        )
        if config.contracts is None:
            raise ToolingConfigError("configuration does not declare runner contracts")
        if arguments.action == "suite":
            return run_suite(
                config.module_root,
                arguments.tooling_root or arguments.repository_root,
                config.contracts.runner,
            )
        execute_target(config.module_root, config.contracts.runner)
    except (OSError, ToolingConfigError, ValueError) as error:
        print(f"test-tooling: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
