"""Bounded process-group cleanup for legacy auxiliary tooling."""

from __future__ import annotations

import os
import signal
import subprocess
import time


def process_group_exists(process: subprocess.Popen[str]) -> bool:
    """Return whether the process group still has members."""
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
        group_exists = process_group_exists(process)
        try:
            process.wait(timeout=min(remaining, 0.05))
        except subprocess.TimeoutExpired:
            continue
        if not group_exists or not process_group_exists(process):
            return True
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return False
        time.sleep(min(remaining, 0.01))


def terminate_process_group(process: subprocess.Popen[str], *, teardown_seconds: float = 5) -> bool:
    """Terminate every live member of an owned process group within a separate budget."""
    if teardown_seconds <= 0:
        raise ValueError("a positive teardown budget is required")
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
