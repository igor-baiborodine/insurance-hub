"""Bounded subprocess execution for Go scaffold tooling."""

from __future__ import annotations

import os
import signal
import subprocess
import sys
import time
from collections.abc import Mapping, Sequence
from pathlib import Path

PASSTHROUGH_ENVIRONMENT = (
    "CC",
    "CGO_ENABLED",
    "CXX",
    "GOCACHE",
    "GOMODCACHE",
    "GONOPROXY",
    "GONOSUMDB",
    "GOPATH",
    "GOPRIVATE",
    "GOPROXY",
    "GOSUMDB",
    "GOTOOLCHAIN",
    "HOME",
    "HTTP_PROXY",
    "HTTPS_PROXY",
    "LANG",
    "LC_ALL",
    "NO_PROXY",
    "PATH",
    "SSL_CERT_DIR",
    "SSL_CERT_FILE",
    "TERM",
    "TMPDIR",
    "UV_CACHE_DIR",
    "UV_PYTHON_INSTALL_DIR",
    "http_proxy",
    "https_proxy",
    "no_proxy",
)


class CommandError(RuntimeError):
    """A scaffold command failed or exceeded its deadline."""


def child_environment(overrides: Mapping[str, str] | None = None) -> dict[str, str]:
    """Build the allowlisted environment inherited by scaffold child commands."""
    environment = {name: os.environ[name] for name in PASSTHROUGH_ENVIRONMENT if name in os.environ}
    if overrides:
        environment.update(overrides)
    return environment


def _group_exists(process: subprocess.Popen[bytes]) -> bool:
    try:
        os.killpg(process.pid, 0)
    except ProcessLookupError:
        return False
    except OSError:
        return True
    return True


def _wait_for_group(process: subprocess.Popen[bytes], deadline: float) -> bool:
    while time.monotonic() < deadline:
        if process.poll() is not None and not _group_exists(process):
            return True
        time.sleep(0.02)
    return process.poll() is not None and not _group_exists(process)


def _terminate_group(process: subprocess.Popen[bytes], teardown_seconds: float) -> bool:
    deadline = time.monotonic() + teardown_seconds
    graceful_deadline = time.monotonic() + teardown_seconds / 2
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except OSError:
        pass
    if _wait_for_group(process, graceful_deadline):
        return True
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except OSError:
        pass
    return _wait_for_group(process, deadline)


def run_command(
    arguments: Sequence[str],
    *,
    cwd: Path,
    env: Mapping[str, str],
    timeout_seconds: float,
    activity: str,
    log_path: Path | None = None,
    heartbeat_seconds: float = 30,
    teardown_seconds: float = 10,
) -> None:
    """Run one owned process group with a deadline and optional combined log."""
    if not arguments or timeout_seconds <= 0 or heartbeat_seconds <= 0 or teardown_seconds <= 0:
        raise ValueError("command and positive work, heartbeat, and teardown budgets are required")

    log = None
    output: int | None = None
    if log_path is not None:
        log_path.parent.mkdir(parents=True, exist_ok=True)
        log = log_path.open("wb")
        output = log.fileno()

    started = time.monotonic()
    deadline = started + timeout_seconds
    heartbeat = started + heartbeat_seconds
    print(f"{activity}: started", file=sys.stderr)
    try:
        process = subprocess.Popen(
            list(arguments),
            cwd=cwd,
            env=dict(env),
            stdin=subprocess.DEVNULL,
            stdout=output,
            stderr=subprocess.STDOUT if output is not None else None,
            start_new_session=True,
        )
        try:
            while process.poll() is None:
                now = time.monotonic()
                if now >= deadline:
                    raise CommandError(
                        f"{activity} exceeded its {timeout_seconds:g}-second deadline"
                    )
                if now >= heartbeat:
                    print(
                        f"{activity}: still running after {now - started:.0f}s",
                        file=sys.stderr,
                    )
                    heartbeat = now + heartbeat_seconds
                time.sleep(min(0.1, deadline - now))
        except BaseException:
            if not _terminate_group(process, teardown_seconds):
                print(
                    f"{activity}: cleanup exceeded its {teardown_seconds:g}-second deadline",
                    file=sys.stderr,
                )
            raise

        if _group_exists(process) and not _terminate_group(process, teardown_seconds):
            raise CommandError(
                f"{activity} cleanup exceeded its {teardown_seconds:g}-second deadline"
            )
        if process.returncode != 0:
            raise CommandError(f"{activity} failed with status {process.returncode}")
    finally:
        if log is not None:
            log.close()
    print(f"{activity}: completed in {time.monotonic() - started:.1f}s", file=sys.stderr)
