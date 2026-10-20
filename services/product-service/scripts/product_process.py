"""Bounded child-process ownership for Product service tooling."""

from __future__ import annotations

import os
import signal
import subprocess
import sys
import time
from collections.abc import Mapping, Sequence
from pathlib import Path


class ProductCommandError(RuntimeError):
    """A Product tooling child failed, timed out, or could not be cleaned up."""


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
    stdout: int | object | None = subprocess.PIPE if capture else None
    stderr: int | object | None = subprocess.PIPE if capture else None
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
            stdin=subprocess.PIPE if input_text is not None else None,
            stdout=stdout,
            stderr=stderr,
            start_new_session=True,
        )
        if input_text is not None and process.stdin is not None:
            process.stdin.write(input_text)
            process.stdin.close()
            process.stdin = None
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
        captured_stdout, captured_stderr = process.communicate() if capture else ("", "")
    except BaseException:
        if "process" in locals() and not _terminate(process, teardown_seconds):
            print(f"{activity}: cleanup exceeded {teardown_seconds:g}s", file=sys.stderr)
        raise
    finally:
        if log is not None:
            log.close()
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
