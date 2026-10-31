"""Tests for bounded subprocess cleanup used by shared repository tooling."""

from __future__ import annotations

import os
import signal
import sys
import time
from pathlib import Path

import pytest

import process_runner


def test_success_returns_captured_output(tmp_path: Path) -> None:
    # when
    result = process_runner.run_command(
        [sys.executable, "-c", "print('ready')"],
        cwd=tmp_path,
        env={"PATH": os.environ.get("PATH", "")},
        timeout_seconds=5,
        activity="controlled child",
    )

    # then
    assert result.stdout == "ready\n"


def test_timeout_terminates_and_reaps_owned_process_group(tmp_path: Path) -> None:
    # given
    child_pid = tmp_path / "child.pid"
    program = (
        "import pathlib, subprocess, sys, time; "
        "child=subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)']); "
        f"pathlib.Path({str(child_pid)!r}).write_text(str(child.pid)); "
        "time.sleep(60)"
    )

    # when / then
    with pytest.raises(process_runner.CommandError, match="deadline"):
        process_runner.run_command(
            [sys.executable, "-c", program],
            cwd=tmp_path,
            env={"PATH": os.environ.get("PATH", "")},
            timeout_seconds=0.2,
            teardown_seconds=2,
            activity="controlled timeout",
            heartbeat_seconds=0.1,
        )
    deadline = time.monotonic() + 2
    pid = int(child_pid.read_text(encoding="utf-8"))
    while time.monotonic() < deadline:
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            break
        time.sleep(0.02)
    else:
        raise AssertionError("owned descendant was not reaped")


def test_timeout_kills_term_resistant_descendant_after_leader_exits(tmp_path: Path) -> None:
    # given
    descendant_pid = tmp_path / "descendant.pid"
    descendant = (
        "import os, pathlib, signal, time; "
        "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
        f"pathlib.Path({str(descendant_pid)!r}).write_text(str(os.getpid())); "
        "time.sleep(60)"
    )
    leader = (
        "import pathlib, subprocess, sys, time; "
        f"ready=pathlib.Path({str(descendant_pid)!r}); "
        f"subprocess.Popen([sys.executable, '-c', {descendant!r}]); "
        "deadline=time.monotonic()+5; "
        "\nwhile not ready.is_file() and time.monotonic() < deadline: time.sleep(0.01)"
    )
    started = time.monotonic()

    # when / then
    with pytest.raises(process_runner.CommandError, match="deadline"):
        process_runner.run_command(
            [sys.executable, "-c", leader],
            cwd=tmp_path,
            env={"PATH": os.environ.get("PATH", "")},
            timeout_seconds=0.2,
            teardown_seconds=1,
            activity="exited leader",
            heartbeat_seconds=0.1,
        )
    assert time.monotonic() - started < 2
    pid = int(descendant_pid.read_text(encoding="utf-8"))
    with pytest.raises(ProcessLookupError):
        os.kill(pid, 0)


def test_run_captures_success_and_merges_log_streams(tmp_path: Path) -> None:
    # given
    log = tmp_path / "child.log"

    # when
    output = process_runner.run(
        [sys.executable, "-c", "print('captured')"],
        cwd=tmp_path,
        env=os.environ,
        timeout_seconds=5,
        activity="capture child",
        capture=True,
    )
    process_runner.run(
        [
            sys.executable,
            "-c",
            "import sys; print('out', flush=True); print('err', file=sys.stderr)",
        ],
        cwd=tmp_path,
        env=os.environ,
        timeout_seconds=5,
        activity="log child",
        log_path=log,
    )

    # then
    assert output == "captured\n"
    assert log.read_text(encoding="utf-8").splitlines() == ["out", "err"]


def test_run_terminates_timed_out_process_group(tmp_path: Path) -> None:
    # given
    started = time.monotonic()

    # when / then
    with pytest.raises(process_runner.CommandError, match="deadline"):
        process_runner.run(
            [sys.executable, "-c", "import time; time.sleep(30)"],
            cwd=tmp_path,
            env=os.environ,
            timeout_seconds=0.1,
            teardown_seconds=2,
            activity="timed child",
            capture=True,
        )
    assert time.monotonic() - started < 3


def test_run_cli_maps_handled_signal_to_shell_status(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    def interrupted() -> int:
        raise process_runner.CommandSignal(signal.SIGTERM)

    monkeypatch.setattr(signal, "signal", lambda *_args: None)
    monkeypatch.setattr(signal, "getsignal", lambda _signum: signal.SIG_DFL)

    # when
    status = process_runner.run_cli(interrupted, name="fixture")

    # then
    assert status == 143
