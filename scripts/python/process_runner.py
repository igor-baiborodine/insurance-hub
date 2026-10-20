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


def _process_group_exists(process: subprocess.Popen[str]) -> bool:
    try:
        os.killpg(process.pid, 0)
    except ProcessLookupError:
        return False
    except OSError:
        return True
    return True


def _wait_for_process_group(process: subprocess.Popen[str], deadline: float) -> bool:
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return False
        group_exists = _process_group_exists(process)
        try:
            process.communicate(timeout=min(remaining, 0.05))
        except subprocess.TimeoutExpired:
            continue
        except OSError, ValueError:
            return False
        if not group_exists or not _process_group_exists(process):
            return True
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return False
        time.sleep(min(remaining, 0.01))


def _terminate(process: subprocess.Popen[str], teardown_seconds: float) -> bool:
    deadline = time.monotonic() + teardown_seconds
    term_deadline = time.monotonic() + teardown_seconds / 2
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except OSError:
        pass
    if _wait_for_process_group(process, term_deadline):
        return True
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except OSError:
        pass
    return _wait_for_process_group(process, deadline)


def run_command(
    arguments: Sequence[str],
    *,
    cwd: str | os.PathLike[str],
    env: Mapping[str, str],
    timeout_seconds: float,
    activity: str,
    input_text: str | None = None,
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
        stdin=subprocess.PIPE if input_text is not None else None,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        start_new_session=True,
    )
    try:
        pending_input = input_text
        while True:
            now = time.monotonic()
            remaining = deadline - now
            if remaining <= 0:
                raise CommandError(f"{activity} exceeded its {timeout_seconds:g}-second deadline")
            try:
                stdout, stderr = process.communicate(
                    input=pending_input, timeout=min(remaining, max(0.1, heartbeat - now))
                )
                break
            except subprocess.TimeoutExpired:
                pending_input = None
                if time.monotonic() >= heartbeat:
                    elapsed = time.monotonic() - started
                    print(f"{activity}: still running after {elapsed:.0f}s", file=sys.stderr)
                    heartbeat = time.monotonic() + heartbeat_seconds
    except BaseException:
        _terminate(process, teardown_seconds)
        raise
    if _process_group_exists(process) and not _terminate(process, teardown_seconds):
        raise CommandError(f"{activity} cleanup exceeded its {teardown_seconds:g}-second deadline")
    result = subprocess.CompletedProcess(list(arguments), process.returncode, stdout, stderr)
    if result.returncode != 0:
        raise CommandError(f"{activity} failed with status {result.returncode}")
    return result
