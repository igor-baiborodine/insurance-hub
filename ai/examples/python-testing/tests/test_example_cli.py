from __future__ import annotations

import io
import subprocess
import sys
from collections.abc import Iterator, Mapping
from contextlib import contextmanager
from pathlib import Path

import pytest
from conftest import FakeRunner

from example_cli import (
    CommandResult,
    Config,
    ToolError,
    execute,
    main,
    parse_report,
    run,
    run_subprocess,
)


@pytest.mark.parametrize(
    ("raw_report", "expected_records"),
    [
        ('{"status": "ok", "records": 0}', 0),
        ('{"records": 3, "status": "ok", "ignored": true}', 3),
    ],
)
def test_parse_report_accepts_valid_record_counts(raw_report: str, expected_records: int) -> None:
    # when
    report = parse_report(raw_report)

    # then
    assert report == {"records": expected_records, "status": "ok"}


@pytest.mark.parametrize(
    "raw_report",
    [
        "[]",
        '{"status": "failed", "records": 1}',
        '{"status": "ok", "records": true}',
        '{"status": "ok", "records": -1}',
        "not-json",
    ],
)
def test_parse_report_rejects_invalid_contracts(raw_report: str) -> None:
    # when / then
    with pytest.raises(ToolError):
        parse_report(raw_report)


def test_execute_uses_selected_python_and_writes_atomic_report(
    cli_paths: tuple[Path, Path, Path],
    fake_runner: FakeRunner,
) -> None:
    # given
    source, output, inspector = cli_paths
    config = Config(source, output, inspector, "LANG", 1.5)
    environment = {
        "LANG": "synthetic-secret",
        "LC_ALL": "C.UTF-8",
        "PYTHONDONTWRITEBYTECODE": "1",
        "UNDECLARED_TARGET": "must-not-be-inherited",
    }

    # when
    report = execute(config, "synthetic-secret", environment, fake_runner)

    # then
    assert report == {"records": 2, "status": "ok"}
    assert output.read_text(encoding="utf-8") == '{"records": 2, "status": "ok"}\n'
    assert fake_runner.calls == [
        (
            [sys.executable, str(inspector), "--source", str(source)],
            "synthetic-secret\n",
            1.5,
            {"LC_ALL": "C.UTF-8", "PYTHONDONTWRITEBYTECODE": "1"},
        )
    ]
    assert list(output.parent.glob(f".{output.name}.*")) == []


def test_run_redacts_secret_and_cleans_partial_output(
    cli_paths: tuple[Path, Path, Path],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    source, output, inspector = cli_paths
    secret = "synthetic-secret"
    monkeypatch.setenv("EXAMPLE_API_TOKEN", secret)
    stderr = io.StringIO()

    def failing_runner(
        _arguments: list[str],
        _input_text: str,
        _timeout: float,
        environment: Mapping[str, str],
    ) -> CommandResult:
        assert "EXAMPLE_API_TOKEN" not in environment
        return CommandResult(7, "", f"remote rejected {secret}")

    # when
    status = run(
        ["--source", str(source), "--output", str(output), "--inspector", str(inspector)],
        runner=failing_runner,
        stderr=stderr,
    )

    # then
    assert status == 1
    assert secret not in stderr.getvalue()
    assert "[REDACTED]" in stderr.getvalue()
    assert not output.exists()
    assert list(output.parent.glob(f".{output.name}.*")) == []


def test_run_subprocess_reports_timeout_without_input_disclosure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    secret = "synthetic-secret"

    class TimedOutProcess:
        returncode = 1

        def communicate(
            self,
            input: str | None = None,
            timeout: float | None = None,
        ) -> tuple[str, str]:
            raise subprocess.TimeoutExpired(["helper"], timeout or 0.1, output=input)

    process = TimedOutProcess()
    reaped: list[TimedOutProcess] = []
    monkeypatch.setattr(subprocess, "Popen", lambda *_args, **_kwargs: process)
    monkeypatch.setattr("example_cli.terminate_and_reap", reaped.append)

    # when / then
    with pytest.raises(ToolError, match="timed out") as captured:
        run_subprocess(["helper"], secret, 0.1, {})

    # then
    assert secret not in str(captured.value)
    assert reaped == [process]


@contextmanager
def owned_marker(path: Path) -> Iterator[None]:
    path.write_text("owned", encoding="utf-8")
    try:
        yield
    finally:
        path.unlink(missing_ok=True)


def test_cleanup_runs_when_an_assertion_fails(tmp_path: Path) -> None:
    # given
    marker = tmp_path / "owned-resource"

    # when / then
    with pytest.raises(AssertionError):
        with owned_marker(marker):
            assert marker.name == "unexpected", "controlled failure"

    # then
    assert not marker.exists()


def test_main_is_import_safe_callable() -> None:
    assert callable(main)
