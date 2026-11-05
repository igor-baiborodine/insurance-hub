"""Fast process-lifecycle tests for disposable Dashboard commands."""

from __future__ import annotations

import os
import sys
import time
from pathlib import Path

import disposable_deployment
import pytest


def assert_reaped(pid_file: Path) -> None:
    deadline = time.monotonic() + 2
    pid = int(pid_file.read_text(encoding="utf-8"))
    while time.monotonic() < deadline:
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            return
        time.sleep(0.02)
    raise AssertionError("owned disposable descendant was not reaped")


def descendant_program(pid_file: Path, *, interrupt_parent: bool) -> str:
    action = "os.kill(os.getppid(), signal.SIGINT); " if interrupt_parent else ""
    return (
        "import os, pathlib, signal, subprocess, sys, time; "
        "child=subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)']); "
        f"pathlib.Path({str(pid_file)!r}).write_text(str(child.pid)); "
        f"{action}"
        "time.sleep(60)"
    )


def test_timeout_reports_progress_and_reaps_descendant(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    pid_file = tmp_path / "timeout-child.pid"

    with pytest.raises(disposable_deployment.CheckFailed, match="deadline"):
        disposable_deployment.command(
            [sys.executable, "-c", descendant_program(pid_file, interrupt_parent=False)],
            timeout=0.3,
            heartbeat=0.1,
            teardown=2,
            activity="disposable timeout fixture",
        )

    assert "still running" in capsys.readouterr().err
    assert_reaped(pid_file)


def test_interruption_reaps_descendant(tmp_path: Path) -> None:
    pid_file = tmp_path / "interrupt-child.pid"

    with pytest.raises(KeyboardInterrupt):
        disposable_deployment.command(
            [sys.executable, "-c", descendant_program(pid_file, interrupt_parent=True)],
            timeout=5,
            heartbeat=1,
            teardown=2,
            activity="disposable interrupt fixture",
        )

    assert_reaped(pid_file)
