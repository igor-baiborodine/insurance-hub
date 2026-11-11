"""Tests for bounded subprocess cleanup used by shared repository tooling."""

from __future__ import annotations

import os
import sys
import time
from pathlib import Path

import pytest

import process_runner


def test_child_environment_allows_only_declared_inputs_and_preferred_tools(tmp_path: Path) -> None:
    # given
    executable = tmp_path / "bin/tool"
    executable.parent.mkdir()
    executable.touch()

    # when
    environment = process_runner.child_environment(
        {"CGO_ENABLED": "0"},
        preferred_executables=(str(executable),),
        source={"PATH": "/usr/bin", "SECRET_TOKEN": "must-not-pass", "HOME": "/fixture"},
    )

    # then
    assert environment == {
        "CGO_ENABLED": "0",
        "HOME": "/fixture",
        "PATH": f"{executable.parent}:/usr/bin",
    }


def test_nonpositive_heartbeat_is_rejected_before_process_start(tmp_path: Path) -> None:
    with pytest.raises(ValueError, match="positive work, heartbeat, and teardown"):
        process_runner.run_command(
            [sys.executable, "-c", "raise SystemExit('must not run')"],
            cwd=tmp_path,
            env={"PATH": os.environ.get("PATH", "")},
            timeout_seconds=5,
            heartbeat_seconds=0,
            activity="invalid heartbeat",
        )


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


def test_failure_retains_captured_output_for_the_owning_caller(tmp_path: Path) -> None:
    # when / then
    with pytest.raises(process_runner.CommandError, match="status 7") as raised:
        process_runner.run_command(
            [
                sys.executable,
                "-c",
                "import sys; print('test detail'); print('diagnostic', file=sys.stderr); sys.exit(7)",
            ],
            cwd=tmp_path,
            env={"PATH": os.environ.get("PATH", "")},
            timeout_seconds=5,
            activity="controlled failure",
        )
    assert raised.value.stdout == "test detail\n"
    assert raised.value.stderr == "diagnostic\n"


def test_sensitive_stdin_is_redacted_from_failure_output(tmp_path: Path) -> None:
    # given
    secret = "synthetic-sensitive-value"

    # when / then
    with pytest.raises(process_runner.CommandError, match="status 9") as raised:
        process_runner.run_command(
            [
                sys.executable,
                "-c",
                "import sys; value=sys.stdin.read(); print(value); print(value, file=sys.stderr); sys.exit(9)",
            ],
            cwd=tmp_path,
            env={"PATH": os.environ.get("PATH", "")},
            timeout_seconds=5,
            activity="controlled secret failure",
            input_text=secret,
            sensitive_values=(secret,),
        )
    assert secret not in raised.value.stdout
    assert secret not in raised.value.stderr
    assert raised.value.stdout.strip() == "[REDACTED]"
    assert raised.value.stderr.strip() == "[REDACTED]"


@pytest.mark.parametrize("channel", ("argument", "environment", "activity"))
def test_sensitive_value_is_rejected_outside_stdin(tmp_path: Path, channel: str) -> None:
    # given
    secret = "synthetic-sensitive-value"
    arguments = [sys.executable, "-c", "raise SystemExit('must not run')"]
    environment = {"PATH": os.environ.get("PATH", "")}
    activity = "controlled secret validation"
    if channel == "argument":
        arguments.append(secret)
    elif channel == "environment":
        environment["SECRET"] = secret
    else:
        activity = f"controlled {secret} validation"

    # when / then
    with pytest.raises(ValueError, match="must not contain sensitive values"):
        process_runner.run_command(
            arguments,
            cwd=tmp_path,
            env=environment,
            timeout_seconds=5,
            activity=activity,
            sensitive_values=(secret,),
        )


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
