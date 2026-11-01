"""Bounded command and signal handling for shared Go-service tooling."""

from __future__ import annotations

import signal
import sys
from collections.abc import Callable, Mapping, Sequence
from pathlib import Path

from scripts.python.process_runner import run_command


class ToolingSignal(BaseException):
    """A handled termination signal interrupted Go-service tooling."""

    def __init__(self, signum: int) -> None:
        super().__init__(signum)
        self.signum = signum


def run(
    arguments: Sequence[str],
    *,
    cwd: Path,
    env: Mapping[str, str],
    timeout_seconds: float,
    activity: str,
    input_text: str | None = None,
    heartbeat_seconds: float = 30,
    teardown_seconds: float = 5,
    capture: bool = False,
) -> str:
    """Run one owned command and optionally return its captured standard output."""
    result = run_command(
        arguments,
        cwd=cwd,
        env=env,
        timeout_seconds=timeout_seconds,
        activity=activity,
        input_text=input_text,
        heartbeat_seconds=heartbeat_seconds,
        teardown_seconds=teardown_seconds,
    )
    if capture:
        return result.stdout
    if result.stdout:
        print(result.stdout, end="")
    if result.stderr:
        print(result.stderr, end="", file=sys.stderr)
    return ""


def run_cli(main: Callable[[], int], *, name: str) -> int:
    """Run a CLI with stable interrupt and termination exit codes."""

    def terminate(signum: int, _frame: object) -> None:
        raise ToolingSignal(signum)

    previous = {
        signum: signal.signal(signum, terminate) for signum in (signal.SIGINT, signal.SIGTERM)
    }
    try:
        return main()
    except ToolingSignal as error:
        label = "interrupted" if error.signum == signal.SIGINT else "terminated"
        print(f"{name}: {label}", file=sys.stderr)
        return 128 + error.signum
    finally:
        for signum, handler in previous.items():
            signal.signal(signum, handler)
