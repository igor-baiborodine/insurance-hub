"""Fast behavior tests for Product security result validators."""

from __future__ import annotations

import copy
import json
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
