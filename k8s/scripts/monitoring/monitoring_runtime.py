"""Signal and child-process lifecycle support for monitoring tooling."""

from __future__ import annotations

import os
import signal
import subprocess
import sys
import threading
import time
from collections.abc import Iterator
from contextlib import contextmanager
from types import FrameType


class Interrupted(RuntimeError):
    """A supported POSIX signal requested controlled monitoring cleanup."""

    def __init__(self, signum: int) -> None:
        super().__init__(f"interrupted by {signal.Signals(signum).name}")
        self.status = 130 if signum == signal.SIGINT else 143


class DeadlineExceeded(RuntimeError):
    """The finite workflow exhausted its overall work budget."""


@contextmanager
def bounded_activity(activity: str, deadline_seconds: float) -> Iterator[None]:
    """Report 30-second activity and enforce one finite POSIX work deadline."""

    if deadline_seconds <= 0:
        raise ValueError("a positive overall deadline is required")
    started = time.monotonic()
    stop = threading.Event()

    def heartbeat() -> None:
        while not stop.wait(30):
            print(f"{activity}: active after {time.monotonic() - started:.0f}s", file=sys.stderr)

    def timeout(_signum: int, _frame: FrameType | None) -> None:
        raise DeadlineExceeded(f"{activity} exceeded its {deadline_seconds:g}-second work budget")

    previous = signal.signal(signal.SIGALRM, timeout)
    timer = threading.Thread(target=heartbeat, daemon=True)
    print(f"{activity}: started with a {deadline_seconds:g}-second work budget", file=sys.stderr)
    timer.start()
    signal.setitimer(signal.ITIMER_REAL, deadline_seconds)
    try:
        yield
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, previous)
        stop.set()
        timer.join(timeout=1)
        print(f"{activity}: ended after {time.monotonic() - started:.0f}s", file=sys.stderr)


@contextmanager
def controlled_signals() -> Iterator[None]:
    """Map SIGINT and SIGTERM to exceptions so owner cleanup scopes run."""

    previous: dict[int, signal.Handlers] = {}

    def interrupt(signum: int, _frame: FrameType | None) -> None:
        raise Interrupted(signum)

    for signum in (signal.SIGINT, signal.SIGTERM):
        previous[signum] = signal.signal(signum, interrupt)
    try:
        yield
    finally:
        for signum, handler in previous.items():
            signal.signal(signum, handler)


def terminate_process_group(
    process: subprocess.Popen[object], *, graceful_seconds: float = 5, teardown_seconds: float = 10
) -> None:
    """Terminate and reap one owned POSIX process group within a teardown budget."""

    if graceful_seconds <= 0 or teardown_seconds < graceful_seconds:
        raise ValueError("teardown must include a positive graceful period")
    started = time.monotonic()

    def group_exists() -> bool:
        try:
            os.killpg(process.pid, 0)
        except ProcessLookupError:
            return False
        except OSError:
            return True
        return True

    def wait_until(deadline: float) -> bool:
        while time.monotonic() < deadline:
            try:
                process.wait(timeout=min(0.05, deadline - time.monotonic()))
            except subprocess.TimeoutExpired:
                pass
            if not group_exists():
                return True
        return not group_exists()

    if process.poll() is not None and not group_exists():
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    if wait_until(started + graceful_seconds):
        return
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    if not wait_until(started + teardown_seconds):
        raise RuntimeError("owned process group exceeded its teardown budget")
