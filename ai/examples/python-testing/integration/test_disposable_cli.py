from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
import textwrap
import time
from pathlib import Path

import pytest

pytestmark = pytest.mark.integration

EXAMPLE_ROOT = Path(__file__).resolve().parents[1]
EXAMPLE_CLI = EXAMPLE_ROOT / "example_cli.py"


@pytest.fixture(autouse=True)
def require_explicit_integration_selection() -> None:
    if os.environ.get("PYTHON_EXAMPLE_INTEGRATION") != "1":
        pytest.skip("run through make python-examples-test-integration")


def write_inspector(path: Path, *, fail: bool) -> None:
    body = textwrap.dedent("""\
        import argparse
        import json
        import sys
        from pathlib import Path

        parser = argparse.ArgumentParser()
        parser.add_argument("--source", required=True, type=Path)
        arguments = parser.parse_args()
        token = sys.stdin.readline().strip()
        """)
    if fail:
        body += textwrap.dedent("""\
            print(f"rejected token {token}", file=sys.stderr)
            raise SystemExit(7)
            """)
    else:
        body += textwrap.dedent("""\
            source = json.loads(arguments.source.read_text(encoding="utf-8"))
            print(json.dumps({"status": "ok", "records": len(source["records"])}))
            """)
    path.write_text(body, encoding="utf-8")


def run_cli(tmp_path: Path, inspector: Path, secret: str) -> subprocess.CompletedProcess[str]:
    source = tmp_path / "source.json"
    source.write_text('{"records": [{"id": 1}, {"id": 2}]}', encoding="utf-8")
    environment = os.environ.copy()
    environment["EXAMPLE_API_TOKEN"] = secret
    environment["PYTHONDONTWRITEBYTECODE"] = "1"
    return subprocess.run(
        [
            sys.executable,
            str(EXAMPLE_CLI),
            "--source",
            str(source),
            "--output",
            str(tmp_path / "report.json"),
            "--inspector",
            str(inspector),
        ],
        capture_output=True,
        check=False,
        env=environment,
        shell=False,
        text=True,
        timeout=10,
    )


def start_cli(tmp_path: Path, inspector: Path, secret: str) -> subprocess.Popen[str]:
    source = tmp_path / "source.json"
    source.write_text('{"records": [{"id": 1}, {"id": 2}]}', encoding="utf-8")
    environment = os.environ.copy()
    environment["EXAMPLE_API_TOKEN"] = secret
    environment["PYTHONDONTWRITEBYTECODE"] = "1"
    return subprocess.Popen(
        [
            sys.executable,
            str(EXAMPLE_CLI),
            "--source",
            str(source),
            "--output",
            str(tmp_path / "report.json"),
            "--inspector",
            str(inspector),
        ],
        env=environment,
        shell=False,
        stderr=subprocess.PIPE,
        stdout=subprocess.PIPE,
        text=True,
    )


def write_blocking_inspector(path: Path) -> Path:
    process_record = path.with_suffix(".pids.json")
    body = textwrap.dedent(f"""\
        import json
        import os
        import signal
        import subprocess
        import sys
        from pathlib import Path

        descendant = subprocess.Popen(
            [sys.executable, "-c", "import signal; signal.pause()"],
        )
        Path({str(process_record)!r}).write_text(
            json.dumps({{"helper": os.getpid(), "descendant": descendant.pid}}),
            encoding="utf-8",
        )
        signal.pause()
        """)
    path.write_text(body, encoding="utf-8")
    return process_record


def wait_for_process_record(path: Path) -> dict[str, int]:
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        try:
            return json.loads(path.read_text(encoding="utf-8"))
        except FileNotFoundError, json.JSONDecodeError:
            time.sleep(0.01)
    raise AssertionError(f"helper did not record its processes: {path}")


def process_exists(pid: int) -> bool:
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    return True


def process_is_running(pid: int) -> bool:
    if not process_exists(pid):
        return False
    process_status = Path(f"/proc/{pid}/stat")
    if process_status.exists() and process_status.read_text(encoding="utf-8").split()[2] == "Z":
        return False
    return True


def wait_until_process_stops(pid: int) -> None:
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        if not process_is_running(pid):
            return
        time.sleep(0.01)
    raise AssertionError(f"owned process remained active: {pid}")


def wait_until_process_is_reaped(pid: int) -> None:
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        if not process_exists(pid):
            return
        time.sleep(0.01)
    raise AssertionError(f"owned child was not reaped: {pid}")


def test_cli_runs_disposable_python_child(tmp_path: Path) -> None:
    # given
    secret = "synthetic-integration-secret"
    inspector = tmp_path / "inspector.py"
    write_inspector(inspector, fail=False)

    # when
    completed = run_cli(tmp_path, inspector, secret)

    # then
    assert completed.returncode == 0
    assert json.loads(completed.stdout)["report"] == {"records": 2, "status": "ok"}
    assert json.loads((tmp_path / "report.json").read_text(encoding="utf-8")) == {
        "records": 2,
        "status": "ok",
    }
    assert secret not in completed.stdout
    assert secret not in completed.stderr


def test_cli_redacts_child_failure_and_cleans_partial_file(tmp_path: Path) -> None:
    # given
    secret = "synthetic-integration-secret"
    inspector = tmp_path / "inspector.py"
    write_inspector(inspector, fail=True)

    # when
    completed = run_cli(tmp_path, inspector, secret)

    # then
    assert completed.returncode == 1
    assert secret not in completed.stderr
    assert "[REDACTED]" in completed.stderr
    assert not (tmp_path / "report.json").exists()
    assert list(tmp_path.glob(".report.json.*")) == []


@pytest.mark.skipif(os.name != "posix", reason="the CLI process-group contract is POSIX-specific")
@pytest.mark.parametrize(
    ("interrupt_signal", "expected_status"),
    [(signal.SIGINT, 130), (signal.SIGTERM, 143)],
)
def test_cli_interrupt_terminates_owned_processes_and_cleans_partial_file(
    tmp_path: Path,
    interrupt_signal: signal.Signals,
    expected_status: int,
) -> None:
    # given
    secret = "synthetic-integration-secret"
    inspector = tmp_path / "blocking_inspector.py"
    process_record = write_blocking_inspector(inspector)
    cli = start_cli(tmp_path, inspector, secret)

    try:
        owned_processes = wait_for_process_record(process_record)

        # when
        os.kill(cli.pid, interrupt_signal)
        stdout, stderr = cli.communicate(timeout=5)

        # then
        assert cli.returncode == expected_status
        assert signal.Signals(interrupt_signal).name in stderr
        assert secret not in stdout
        assert secret not in stderr
        assert not (tmp_path / "report.json").exists()
        assert list(tmp_path.glob(".report.json.*")) == []
        wait_until_process_is_reaped(owned_processes["helper"])
        wait_until_process_stops(owned_processes["descendant"])
    finally:
        if cli.poll() is None:
            os.kill(cli.pid, signal.SIGTERM)
            try:
                cli.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                cli.kill()
                cli.wait(timeout=5)
