"""Bounded subprocess execution for repository tooling."""

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
    """A bounded child command failed or exceeded its deadline."""

    def __init__(self, message: str, *, stdout: str = "", stderr: str = "") -> None:
        super().__init__(message)
        self.stdout = stdout
        self.stderr = stderr


def child_environment(
    overrides: Mapping[str, str] | None = None,
    *,
    preferred_executables: Sequence[str] = (),
    source: Mapping[str, str] = os.environ,
) -> dict[str, str]:
    """Build an allowlisted child environment with explicit executable precedence."""
    environment = {name: source[name] for name in PASSTHROUGH_ENVIRONMENT if name in source}
    if overrides:
        if not all(
            isinstance(key, str) and key and isinstance(value, str)
            for key, value in overrides.items()
        ):
            raise ValueError("environment overrides must contain non-empty string names and values")
        environment.update(overrides)
    for executable in reversed(preferred_executables):
        path = Path(executable)
        if path.name == executable:
            continue
        directory = str(path.resolve().parent)
        current = environment.get("PATH", "")
        environment["PATH"] = f"{directory}{os.pathsep}{current}" if current else directory
    return environment


def _redact(value: str, sensitive_values: Sequence[str]) -> str:
    for sensitive in sensitive_values:
        value = value.replace(sensitive, "[REDACTED]")
    return value


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


def terminate_process_group(process: subprocess.Popen[str], teardown_seconds: float) -> bool:
    """Terminate and reap one process group within a bounded teardown budget."""
    if teardown_seconds <= 0:
        raise ValueError("a positive teardown deadline is required")
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


def _run_logged_command(
    arguments: Sequence[str],
    *,
    cwd: str | os.PathLike[str],
    env: Mapping[str, str],
    timeout_seconds: float,
    activity: str,
    log_path: Path,
    heartbeat_seconds: float,
    teardown_seconds: float,
) -> subprocess.CompletedProcess[str]:
    log_path.parent.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    deadline = started + timeout_seconds
    heartbeat = started + heartbeat_seconds
    print(f"{activity}: started", file=sys.stderr)
    with log_path.open("w", encoding="utf-8") as output:
        process = subprocess.Popen(
            list(arguments),
            cwd=cwd,
            env=dict(env),
            text=True,
            stdin=subprocess.DEVNULL,
            stdout=output,
            stderr=subprocess.STDOUT,
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
                    print(f"{activity}: still running after {now - started:.0f}s", file=sys.stderr)
                    heartbeat = now + heartbeat_seconds
                time.sleep(min(0.1, deadline - now))
        except BaseException:
            terminate_process_group(process, teardown_seconds)
            raise
        if _process_group_exists(process) and not terminate_process_group(
            process, teardown_seconds
        ):
            raise CommandError(
                f"{activity} cleanup exceeded its {teardown_seconds:g}-second deadline"
            )
    result = subprocess.CompletedProcess(list(arguments), process.returncode, "", "")
    if result.returncode != 0:
        raise CommandError(f"{activity} failed with status {result.returncode}")
    print(f"{activity}: completed in {time.monotonic() - started:.1f}s", file=sys.stderr)
    return result


def run_command(
    arguments: Sequence[str],
    *,
    cwd: str | os.PathLike[str],
    env: Mapping[str, str],
    timeout_seconds: float,
    activity: str,
    input_text: str | None = None,
    sensitive_values: Sequence[str] = (),
    log_path: Path | None = None,
    heartbeat_seconds: float = 30,
    teardown_seconds: float = 5,
) -> subprocess.CompletedProcess[str]:
    """Capture a bounded child while reporting progress and owning its process group."""
    if not arguments or timeout_seconds <= 0 or heartbeat_seconds <= 0 or teardown_seconds <= 0:
        raise ValueError(
            "command and positive work, heartbeat, and teardown deadlines are required"
        )
    if any(not isinstance(value, str) or not value for value in sensitive_values):
        raise ValueError("sensitive values must be non-empty strings")
    if any(value in activity for value in sensitive_values):
        raise ValueError("activity must not contain sensitive values")
    if any(value in argument for value in sensitive_values for argument in arguments):
        raise ValueError("command arguments must not contain sensitive values")
    if any(value in item for value in sensitive_values for item in env.values()):
        raise ValueError("command environment must not contain sensitive values")
    if log_path is not None:
        if input_text is not None or sensitive_values:
            raise ValueError("logged commands cannot accept sensitive input")
        return _run_logged_command(
            arguments,
            cwd=cwd,
            env=env,
            timeout_seconds=timeout_seconds,
            activity=activity,
            log_path=log_path,
            heartbeat_seconds=heartbeat_seconds,
            teardown_seconds=teardown_seconds,
        )
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
        terminate_process_group(process, teardown_seconds)
        raise
    if _process_group_exists(process) and not terminate_process_group(process, teardown_seconds):
        raise CommandError(f"{activity} cleanup exceeded its {teardown_seconds:g}-second deadline")
    stdout = _redact(stdout, sensitive_values)
    stderr = _redact(stderr, sensitive_values)
    result = subprocess.CompletedProcess(list(arguments), process.returncode, stdout, stderr)
    if result.returncode != 0:
        raise CommandError(
            f"{activity} failed with status {result.returncode}",
            stdout=result.stdout,
            stderr=result.stderr,
        )
    return result
