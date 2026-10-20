"""Bounded subprocess execution for repository tooling."""

from __future__ import annotations

import os
import signal
import subprocess
import sys
import time
from collections.abc import Mapping, Sequence


class CommandError(RuntimeError):
    """A bounded child command failed or exceeded its deadline."""


def _terminate(process: subprocess.Popen[str], teardown_seconds: float) -> None:
    if process.poll() is not None:
        process.communicate()
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        process.communicate(timeout=teardown_seconds)
        return
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.communicate(timeout=teardown_seconds)


def run_command(
    arguments: Sequence[str],
    *,
    cwd: str | os.PathLike[str],
    env: Mapping[str, str],
    timeout_seconds: float,
    activity: str,
    heartbeat_seconds: float = 30,
    teardown_seconds: float = 5,
) -> subprocess.CompletedProcess[str]:
    """Capture a bounded child while reporting progress and owning its process group."""
    if not arguments or timeout_seconds <= 0 or teardown_seconds <= 0:
        raise ValueError("command and positive work/teardown deadlines are required")
    started = time.monotonic()
    deadline = started + timeout_seconds
    heartbeat = started + heartbeat_seconds
    process = subprocess.Popen(
        list(arguments),
        cwd=cwd,
        env=dict(env),
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        start_new_session=True,
    )
    try:
        while True:
            now = time.monotonic()
            remaining = deadline - now
            if remaining <= 0:
                _terminate(process, teardown_seconds)
                raise CommandError(f"{activity} exceeded its {timeout_seconds:g}-second deadline")
            try:
                stdout, stderr = process.communicate(
                    timeout=min(remaining, max(0.1, heartbeat - now))
                )
                break
            except subprocess.TimeoutExpired:
                if time.monotonic() >= heartbeat:
                    elapsed = time.monotonic() - started
                    print(f"{activity}: still running after {elapsed:.0f}s", file=sys.stderr)
                    heartbeat = time.monotonic() + heartbeat_seconds
    except BaseException:
        _terminate(process, teardown_seconds)
        raise
    result = subprocess.CompletedProcess(list(arguments), process.returncode, stdout, stderr)
    if result.returncode != 0:
        raise CommandError(f"{activity} failed with status {result.returncode}")
    return result
