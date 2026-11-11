"""Tests for explicit disposable-resource ownership and cleanup."""

from __future__ import annotations

import os
import signal
import sys
import time
from pathlib import Path

import pytest

from scripts.go.service import tooling_resources
from scripts.go.service.tooling_resources import OwnedProcess, ResourceError, ResourceOwner
from scripts.python.process_runner import child_environment


def test_partial_startup_cleans_only_registered_resources_in_reverse_order() -> None:
    # given
    events: list[str] = []
    owner = ResourceOwner()
    owner.start("first", lambda: (object(), lambda: events.append("first")))
    owner.start("second", lambda: (object(), lambda: events.append("second")))

    def fail() -> tuple[object, object]:
        raise RuntimeError("startup failed")

    # when / then
    with pytest.raises(RuntimeError, match="startup failed"):
        owner.start("third", fail)  # type: ignore[arg-type]
    assert events == ["second", "first"]
    owner.close()
    assert events == ["second", "first"]


def test_owned_process_reports_incomplete_cleanup(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    process = OwnedProcess.start(
        [sys.executable, "-c", "pass"],
        cwd=Path("."),
        env=child_environment(),
        teardown_seconds=1,
    )
    process.process.wait(timeout=2)
    monkeypatch.setattr(tooling_resources, "terminate_process_group", lambda *_args: False)

    # when / then
    with pytest.raises(ResourceError, match="cleanup exceeded"):
        process.close()


@pytest.mark.integration
def test_owned_process_reaps_resistant_descendant_process_group(tmp_path: Path) -> None:
    # given
    child_pid_file = tmp_path / "child.pid"
    script = (
        "import os, signal, subprocess, sys, time; "
        "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
        "child=subprocess.Popen([sys.executable, '-c', "
        "'import signal,time; signal.signal(signal.SIGTERM, signal.SIG_IGN); time.sleep(60)']); "
        "open(sys.argv[1], 'w').write(str(child.pid)); time.sleep(60)"
    )
    owned = OwnedProcess.start(
        [sys.executable, "-c", script, str(child_pid_file)],
        cwd=tmp_path,
        env=child_environment(),
        teardown_seconds=2,
    )
    deadline = time.monotonic() + 5
    while not child_pid_file.exists() and time.monotonic() < deadline:
        time.sleep(0.01)
    assert child_pid_file.exists()
    process_group = owned.process.pid

    # when
    owned.close()

    # then
    with pytest.raises(ProcessLookupError):
        os.killpg(process_group, signal.SIGCONT)
