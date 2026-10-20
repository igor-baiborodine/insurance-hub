from __future__ import annotations

import json
import os
import subprocess
import sys
import textwrap
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
