"""Fast behavior tests for Product security result validators."""

from __future__ import annotations

import copy
import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path
from xml.etree import ElementTree

import pytest

from scripts.product_security import check_cases, check_java_results, security_test

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
ACCESS_CASES = (
    REPOSITORY_ROOT
    / "legacy/product-service/src/test/resources/product-read-baseline/access/cases.json"
)


def test_complete_access_case_set_passes() -> None:
    # when
    status = check_cases.main([str(ACCESS_CASES)])

    # then
    assert status == 0


@pytest.mark.parametrize("mutation", ["missing", "duplicate", "altered"])
def test_incomplete_or_altered_access_case_set_fails(
    tmp_path: Path, mutation: str, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    document = json.loads(ACCESS_CASES.read_text(encoding="utf-8"))
    if mutation == "missing":
        document["cases"].pop()
    elif mutation == "duplicate":
        document["cases"].append(copy.deepcopy(document["cases"][0]))
    else:
        document["cases"][0]["expected"]["status"] = 599
    candidate = tmp_path / "cases.json"
    candidate.write_text(json.dumps(document), encoding="utf-8")

    # when
    status = check_cases.main([str(candidate)])

    # then
    assert status == 1
    assert "SECURITY_CASE_SET_MISMATCH" in capsys.readouterr().err


def java_report(path: Path, *, missing: bool = False, skipped: bool = False) -> None:
    names = sorted(check_java_results.EXPECTED)
    if missing:
        names.pop()
    root = ElementTree.Element(
        "testsuite",
        name="example.GatewayProductSecurityIT",
        tests=str(len(names)),
        errors="0",
        failures="0",
        skipped="1" if skipped else "0",
    )
    for index, name in enumerate(names):
        case = ElementTree.SubElement(root, "testcase", name=name)
        if skipped and index == 0:
            ElementTree.SubElement(case, "skipped")
    ElementTree.ElementTree(root).write(path, encoding="unicode")


def test_complete_java_security_report_passes(tmp_path: Path) -> None:
    # given
    report = tmp_path / "report.xml"
    java_report(report)

    # when
    status = check_java_results.main([str(report)])

    # then
    assert status == 0


@pytest.mark.parametrize("failure", ["missing", "skipped"])
def test_incomplete_java_security_report_fails(
    tmp_path: Path, failure: str, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    report = tmp_path / "report.xml"
    java_report(report, missing=failure == "missing", skipped=failure == "skipped")

    # when
    status = check_java_results.main([str(report)])

    # then
    assert status == 1
    assert "SECURITY_JAVA_RESULT_MISMATCH" in capsys.readouterr().err


def test_security_boundary_rejects_external_database_before_commands(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    monkeypatch.setenv("PRODUCT_DATABASE_URL", "synthetic-secret-sentinel")
    monkeypatch.setattr(security_test.shutil, "which", lambda _name: "/controlled/tool")
    monkeypatch.setattr(
        security_test,
        "run",
        lambda *_args, **_kwargs: pytest.fail("external database reached a command"),
    )

    # when
    status = security_test.main([])

    # then
    assert status == 1
    diagnostic = capsys.readouterr().err
    assert "PRODUCT_DATABASE_URL must be unset" in diagnostic
    assert "synthetic-secret-sentinel" not in diagnostic


def test_security_boundary_reports_missing_prerequisite(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    monkeypatch.delenv("PRODUCT_DATABASE_URL", raising=False)
    monkeypatch.setattr(
        security_test.shutil,
        "which",
        lambda name: None if name == "java" else f"/controlled/{name}",
    )

    # when
    status = security_test.main([])

    # then
    assert status == 1
    assert "missing prerequisite: java" in capsys.readouterr().err


def test_security_boundary_reports_unavailable_docker(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    monkeypatch.delenv("PRODUCT_DATABASE_URL", raising=False)
    monkeypatch.setattr(security_test.shutil, "which", lambda name: f"/controlled/{name}")
    calls = 0

    def fake_run(*_args: object, **_kwargs: object) -> str:
        nonlocal calls
        calls += 1
        if calls == 1:
            return "Java version: 14.0.2"
        raise security_test.ProductCommandError("controlled Docker failure")

    monkeypatch.setattr(security_test, "run", fake_run)

    # when
    status = security_test.main([])

    # then
    assert status == 1
    assert "Docker daemon is unavailable" in capsys.readouterr().err


def process_is_running(pid: int) -> bool:
    try:
        state = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8").split()[2]
    except FileNotFoundError, ProcessLookupError:
        return False
    return state != "Z"


@pytest.mark.parametrize(
    ("requested_signal", "expected_status"),
    [(signal.SIGINT, 130), (signal.SIGTERM, 143)],
)
def test_security_runner_signal_cleans_partial_startup(
    tmp_path: Path, requested_signal: signal.Signals, expected_status: int
) -> None:
    # given
    fake_bin = tmp_path / "bin"
    fake_bin.mkdir()
    tool = fake_bin / "controlled-tool"
    harness_source = tmp_path / "harness"
    helper_pid = tmp_path / "helper.pid"
    helper_ready = tmp_path / "helper.ready"
    harness_source.write_text(
        f"#!{sys.executable}\n"
        "import json, os, pathlib, sys, time\n"
        "arguments = sys.argv[1:]\n"
        "def value(name): return arguments[arguments.index(name) + 1]\n"
        "pathlib.Path(value('-markers-file')).write_text('private-marker\\n')\n"
        "pathlib.Path(value('-endpoint-file')).write_text(json.dumps({"
        "'pid': os.getpid(), 'fixture': 'DATA-QA-001', 'rowCount': 4, "
        "'reader': 'go_product_reader', 'containerId': 'a' * 64, "
        "'http': '127.0.0.1:1', 'grpc': '127.0.0.1:2', 'health': '127.0.0.1:3'}))\n"
        "pathlib.Path(os.environ['SECURITY_HELPER_PID']).write_text(str(os.getpid()))\n"
        "pathlib.Path(os.environ['SECURITY_HELPER_READY']).write_text('ready')\n"
        "time.sleep(60)\n",
        encoding="utf-8",
    )
    harness_source.chmod(0o755)
    tool.write_text(
        f"#!{sys.executable}\n"
        "import os, pathlib, shutil, sys, time\n"
        "name = pathlib.Path(sys.argv[0]).name\n"
        "if name == 'mvn' and '-version' in sys.argv:\n"
        "    print('Apache Maven 3.9.9\\nJava version: 14.0.2')\n"
        "elif name == 'make':\n"
        "    output = next(value.split('=', 1)[1] for value in sys.argv if value.startswith('SECURITY_HARNESS_OUTPUT='))\n"
        "    shutil.copyfile(os.environ['SECURITY_HARNESS_SOURCE'], output)\n"
        "    pathlib.Path(output).chmod(0o755)\n"
        "elif name == 'mvn':\n"
        "    time.sleep(60)\n",
        encoding="utf-8",
    )
    tool.chmod(0o755)
    for name in ("java", "mvn", "docker", "go", "make"):
        (fake_bin / name).symlink_to(tool.name)
    environment = dict(os.environ)
    environment.pop("PRODUCT_DATABASE_URL", None)
    environment.update(
        PATH=f"{fake_bin}:{environment['PATH']}",
        SECURITY_HARNESS_SOURCE=str(harness_source),
        SECURITY_HELPER_PID=str(helper_pid),
        SECURITY_HELPER_READY=str(helper_ready),
    )
    process = subprocess.Popen(
        [sys.executable, "-m", "scripts.product_security.security_test"],
        cwd=REPOSITORY_ROOT,
        env=environment,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline and not helper_ready.is_file():
            assert process.poll() is None
            time.sleep(0.02)
        assert helper_ready.is_file()

        # when
        os.kill(process.pid, requested_signal)
        _stdout, stderr = process.communicate(timeout=15)

        # then
        assert process.returncode == expected_status
        assert requested_signal.name in stderr
        pid = int(helper_pid.read_text(encoding="utf-8"))
        deadline = time.monotonic() + 3
        while time.monotonic() < deadline and process_is_running(pid):
            time.sleep(0.02)
        assert not process_is_running(pid)
    finally:
        if process.poll() is None:
            process.kill()
            process.wait(timeout=3)
