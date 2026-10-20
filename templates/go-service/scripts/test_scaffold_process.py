"""Fast lifecycle tests for scaffold-owned subprocesses."""

from __future__ import annotations

import os
import sys
import time
from pathlib import Path

import pytest

import scaffold_process


def test_command_failure_records_output(tmp_path: Path) -> None:
    # given
    log = tmp_path / "command.log"

    # when / then
    with pytest.raises(scaffold_process.CommandError, match="status 7"):
        scaffold_process.run_command(
            [sys.executable, "-c", "print('controlled output'); raise SystemExit(7)"],
            cwd=tmp_path,
            env={"PATH": os.environ.get("PATH", "")},
            timeout_seconds=5,
            activity="controlled failure",
            log_path=log,
        )
    assert log.read_text(encoding="utf-8") == "controlled output\n"


def test_child_environment_excludes_unrelated_secret(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    monkeypatch.setenv("SCAFFOLD_SECRET_SENTINEL", "must-not-reach-child")

    # when
    environment = scaffold_process.child_environment({"CONTROLLED": "value"})

    # then
    assert environment["CONTROLLED"] == "value"
    assert "SCAFFOLD_SECRET_SENTINEL" not in environment


def test_timeout_terminates_owned_descendant(tmp_path: Path) -> None:
    # given
    child_pid = tmp_path / "child.pid"
    program = (
        "import pathlib, subprocess, sys, time; "
        "child=subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)']); "
        f"pathlib.Path({str(child_pid)!r}).write_text(str(child.pid)); "
        "time.sleep(60)"
    )

    # when / then
    with pytest.raises(scaffold_process.CommandError, match="deadline"):
        scaffold_process.run_command(
            [sys.executable, "-c", program],
            cwd=tmp_path,
            env={"PATH": os.environ.get("PATH", "")},
            timeout_seconds=0.2,
            teardown_seconds=2,
            heartbeat_seconds=0.1,
            activity="controlled timeout",
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
