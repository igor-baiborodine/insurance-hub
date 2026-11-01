"""Tests for shared Go-service process adaptation."""

from __future__ import annotations

import os
import signal

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
