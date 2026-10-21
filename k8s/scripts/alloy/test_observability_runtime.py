"""Pytest coverage for observability signal, deadline, and process-group ownership."""

from __future__ import annotations

import os
import signal
import stat
import subprocess
import sys
import time
from pathlib import Path

import pytest

import alloy_runtime
import monitoring_runtime

RUNTIMES = (alloy_runtime, monitoring_runtime)


def test_protected_environment_file_is_private_and_keeps_secret_out_of_caller_argv(
    tmp_path: Path,
) -> None:
    # given
    destination = tmp_path / "postgres.env"
    sentinel = "SENTINEL-protected-database-value"

    # when
    alloy_runtime.write_protected_env_file(destination, {"POSTGRES_PASSWORD": sentinel})
    docker_arguments = ["--env-file", str(destination)]

    # then
    assert stat.S_IMODE(destination.stat().st_mode) == 0o600
    assert destination.read_text(encoding="utf-8") == f"POSTGRES_PASSWORD={sentinel}\n"
    assert sentinel not in docker_arguments


@pytest.mark.parametrize("value", ("line one\nline two", "line one\rline two"))
def test_protected_environment_file_rejects_multiline_values(tmp_path: Path, value: str) -> None:
    # when / then
    with pytest.raises(ValueError, match="line break"):
        alloy_runtime.write_protected_env_file(tmp_path / "unsafe.env", {"VALUE": value})
    assert not (tmp_path / "unsafe.env").exists()


@pytest.mark.parametrize("runtime", RUNTIMES)
@pytest.mark.parametrize(("signum", "status"), ((signal.SIGINT, 130), (signal.SIGTERM, 143)))
def test_interruption_status_matches_supported_posix_contract(
    runtime, signum: int, status: int
) -> None:
    # when
    interrupted = runtime.Interrupted(signum)

    # then
    assert interrupted.status == status
    assert signal.Signals(signum).name in str(interrupted)


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_bounded_activity_interrupts_expired_work(runtime) -> None:
    # when / then
    with pytest.raises(runtime.DeadlineExceeded, match="work budget"):
        with runtime.bounded_activity("controlled deadline test", 0.02):
            time.sleep(0.2)


@pytest.mark.parametrize("runtime", RUNTIMES)
def test_process_group_cleanup_escalates_and_reaps_descendants(runtime) -> None:
    # given
    program = """
import signal
import subprocess
import sys
import time

subprocess.Popen([
    sys.executable,
    "-c",
    "import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); time.sleep(60)",
])
signal.signal(signal.SIGTERM, lambda *_args: raise_exit())

def raise_exit():
    raise SystemExit(0)

print("ready", flush=True)
time.sleep(60)
"""
    process = subprocess.Popen(
        [sys.executable, "-c", program],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        start_new_session=True,
    )
    assert process.stdout is not None
    assert process.stdout.readline().strip() == "ready"

    # when
    runtime.terminate_process_group(process, graceful_seconds=0.1, teardown_seconds=2)

    # then
    assert process.poll() is not None
    with pytest.raises(ProcessLookupError):
        os.killpg(process.pid, 0)
