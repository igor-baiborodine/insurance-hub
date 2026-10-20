"""Bounded child-process ownership for Product service tooling."""

from __future__ import annotations

import os
import signal
import subprocess
import sys
import tempfile
import time
from collections.abc import Callable, Mapping, Sequence
from pathlib import Path


class ProductCommandError(RuntimeError):
    """A Product tooling child failed, timed out, or could not be cleaned up."""


class ProductSignal(BaseException):
    """A handled POSIX signal requested controlled Product tooling teardown."""

    def __init__(self, signum: int) -> None:
        self.signum = signum
        super().__init__(signal.Signals(signum).name)


CAPTURE_LIMIT = 4 * 1024 * 1024


def run_cli(main: Callable[[], int], *, name: str) -> int:
    """Run a CLI with SIGINT/SIGTERM converted into protected teardown unwinding."""
    handled = (signal.SIGINT, signal.SIGTERM)
    previous = {signum: signal.getsignal(signum) for signum in handled}

    def interrupt(signum: int, _frame: object) -> None:
        for protected in handled:
            signal.signal(protected, signal.SIG_IGN)
        raise ProductSignal(signum)

    try:
        for signum in handled:
            signal.signal(signum, interrupt)
        try:
            return main()
        except ProductSignal as interruption:
            print(
                f"{name}: interrupted by {signal.Signals(interruption.signum).name}",
                file=sys.stderr,
            )
            return 128 + interruption.signum
    finally:
        for signum, handler in previous.items():
            signal.signal(signum, handler)


def _group_exists(pid: int) -> bool:
    try:
        os.killpg(pid, 0)
    except ProcessLookupError:
        return False
    except OSError:
        return True
    return True


def _terminate(process: subprocess.Popen[str], teardown_seconds: float) -> bool:
    deadline = time.monotonic() + teardown_seconds
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except OSError:
        pass
    graceful = time.monotonic() + teardown_seconds / 2
    while time.monotonic() < graceful and _group_exists(process.pid):
        time.sleep(0.05)
    if _group_exists(process.pid):
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except OSError:
            pass
    while time.monotonic() < deadline and _group_exists(process.pid):
        time.sleep(0.05)
    try:
        process.wait(timeout=max(0.01, deadline - time.monotonic()))
    except subprocess.TimeoutExpired, OSError:
        return False
    return not _group_exists(process.pid)


def run(
    arguments: Sequence[str],
    *,
    cwd: Path,
    env: Mapping[str, str],
    timeout_seconds: float,
    activity: str,
    input_text: str | None = None,
    capture: bool = False,
    log_path: Path | None = None,
    teardown_seconds: float = 10,
    expected_failure: bool = False,
) -> str:
    """Run one command with a deadline, heartbeat, and owned process group."""
    if not arguments or timeout_seconds <= 0 or teardown_seconds <= 0:
        raise ValueError("command and positive work/teardown deadlines are required")
    if capture and log_path is not None:
        raise ValueError("capture and log_path are mutually exclusive")
    log = None
    captured_output = None
    captured_error = None
    command_input = None
    stdout: int | object | None = None
    stderr: int | object | None = None
    if capture:
        captured_output = tempfile.TemporaryFile(mode="w+", encoding="utf-8")
        captured_error = tempfile.TemporaryFile(mode="w+", encoding="utf-8")
        stdout = captured_output
        stderr = captured_error
    if input_text is not None:
        command_input = tempfile.TemporaryFile(mode="w+", encoding="utf-8")
        command_input.write(input_text)
        command_input.seek(0)
    if log_path is not None:
        log_path.parent.mkdir(parents=True, exist_ok=True)
        log = log_path.open("w", encoding="utf-8")
        stdout = log
        stderr = subprocess.STDOUT
    started = time.monotonic()
    deadline = started + timeout_seconds
    heartbeat = started + 30
    print(f"{activity}: started", file=sys.stderr)
    try:
        process = subprocess.Popen(
            list(arguments),
            cwd=cwd,
            env=dict(env),
            text=True,
            stdin=command_input,
            stdout=stdout,
            stderr=stderr,
            start_new_session=True,
        )
        while process.poll() is None:
            now = time.monotonic()
            if now >= deadline:
                raise ProductCommandError(
                    f"{activity} exceeded its {timeout_seconds:g}-second deadline"
                )
            if now >= heartbeat:
                print(f"{activity}: still running after {now - started:.0f}s", file=sys.stderr)
                heartbeat = now + 30
            time.sleep(min(0.1, deadline - now))
        captured_stdout = ""
        captured_stderr = ""
        for stream, label in (
            (captured_output, "stdout"),
            (captured_error, "stderr"),
        ):
            if stream is None:
                continue
            stream.seek(0)
            value = stream.read(CAPTURE_LIMIT + 1)
            if len(value) > CAPTURE_LIMIT:
                raise ProductCommandError(
                    f"{activity} {label} exceeded the {CAPTURE_LIMIT}-character capture limit"
                )
            if label == "stdout":
                captured_stdout = value
            else:
                captured_stderr = value
    except BaseException:
        if "process" in locals() and not _terminate(process, teardown_seconds):
            print(f"{activity}: cleanup exceeded {teardown_seconds:g}s", file=sys.stderr)
        raise
    finally:
        if log is not None:
            log.close()
        if captured_output is not None:
            captured_output.close()
        if captured_error is not None:
            captured_error.close()
        if command_input is not None:
            command_input.close()
    if _group_exists(process.pid) and not _terminate(process, teardown_seconds):
        raise ProductCommandError(
            f"{activity} cleanup exceeded its {teardown_seconds:g}-second deadline"
        )
    if expected_failure and process.returncode == 0:
        raise ProductCommandError(f"{activity} unexpectedly succeeded")
    if not expected_failure and process.returncode != 0:
        detail = (captured_stderr or captured_stdout).strip() if capture else ""
        suffix = f": {detail}" if detail else ""
        raise ProductCommandError(f"{activity} failed with status {process.returncode}{suffix}")
    print(f"{activity}: completed in {time.monotonic() - started:.1f}s", file=sys.stderr)
    return captured_stdout
