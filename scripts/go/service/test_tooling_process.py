"""Tests for shared Go-service process adaptation."""

from __future__ import annotations

import os
import signal
from pathlib import Path

import pytest

from scripts.go.service import tooling_process


def test_run_captures_owned_command_output() -> None:
    # when
    output = tooling_process.run(
        ["/bin/sh", "-c", "printf fixture"],
        cwd=tooling_process.Path.cwd(),
        env={"PATH": os.environ["PATH"]},
        timeout_seconds=5,
        activity="capture fixture output",
        capture=True,
    )

    # then
    assert output == "fixture"


def test_run_can_reveal_owned_failure_output(capsys: pytest.CaptureFixture[str]) -> None:
    # when / then
    with pytest.raises(tooling_process.CommandError, match="status 9"):
        tooling_process.run(
            ["/bin/sh", "-c", "printf detail; printf diagnostic >&2; exit 9"],
            cwd=tooling_process.Path.cwd(),
            env={"PATH": os.environ["PATH"]},
            timeout_seconds=5,
            activity="controlled failure",
            capture=True,
            reveal_failure_output=True,
        )
    captured = capsys.readouterr()
    assert captured.out == "detail"
    assert captured.err == "diagnostic"


def test_run_accepts_expected_failure_and_retains_log(tmp_path: Path) -> None:
    # given
    log = tmp_path / "expected-failure.log"

    # when
    output = tooling_process.run(
        ["/bin/sh", "-c", "printf controlled; exit 7"],
        cwd=tmp_path,
        env={"PATH": os.environ["PATH"]},
        timeout_seconds=5,
        activity="controlled expected failure",
        log_path=log,
        expected_failure=True,
    )

    # then
    assert output == ""
    assert log.read_text(encoding="utf-8") == "controlled"


def test_run_cli_maps_termination_and_restores_handler(
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    previous = signal.getsignal(signal.SIGTERM)

    def terminate() -> int:
        os.kill(os.getpid(), signal.SIGTERM)
        return 0

    # when
    status = tooling_process.run_cli(terminate, name="fixture-tooling")

    # then
    assert status == 143
    assert signal.getsignal(signal.SIGTERM) == previous
    assert "fixture-tooling: terminated" in capsys.readouterr().err
