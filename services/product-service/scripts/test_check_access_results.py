"""Fast behavior tests for Product Go access-result validation."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

import check_access_results


def write_report(path: Path, *, missing: bool = False, skipped: bool = False) -> None:
    records = [
        {"Action": "run", "Test": "TestProductAccess"},
        {"Action": "pass", "Test": "TestProductAccess"},
    ]
    names = sorted(check_access_results.EXPECTED)
    if missing:
        names.pop()
    for name in names:
        records.extend(({"Action": "run", "Test": name}, {"Action": "pass", "Test": name}))
    if skipped:
        records.append({"Action": "skip", "Test": names[0]})
    path.write_text("\n".join(json.dumps(record) for record in records) + "\n", encoding="utf-8")


def test_complete_go_access_report_passes(tmp_path: Path) -> None:
    # given
    report = tmp_path / "go.json"
    write_report(report)

    # when
    status = check_access_results.main([str(report)])

    # then
    assert status == 0


@pytest.mark.parametrize("failure", ["missing", "skipped"])
def test_incomplete_go_access_report_fails(
    tmp_path: Path, failure: str, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    report = tmp_path / "go.json"
    write_report(report, missing=failure == "missing", skipped=failure == "skipped")

    # when
    status = check_access_results.main([str(report)])

    # then
    assert status == 1
    assert "PRODUCT_ACCESS_RESULT_MISMATCH" in capsys.readouterr().err
