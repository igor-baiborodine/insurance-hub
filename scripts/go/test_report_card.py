"""Tests for Go Report Card result validation and summaries."""

from __future__ import annotations

import json
import shutil
import subprocess
from pathlib import Path

import pytest

import report_card


def report(*, average: float = 0.8, did_error: bool = False) -> dict[str, object]:
    return {
        "GradeFromPercentage": "B",
        "average": average,
        "issues": 2,
        "did_error": did_error,
        "checks": [],
    }


def test_valid_report_generates_stable_summary_row() -> None:
    # given
    value = report()

    # when
    row = report_card.summary_row("services/product-service", value)

    # then
    assert row == "| services/product-service | B | 80.0% | 2 |"
    assert report_card.result_passes(value)


@pytest.mark.parametrize(
    "value",
    [report(average=0.749), report(did_error=True)],
)
def test_error_or_below_threshold_fails(value: dict[str, object]) -> None:
    assert not report_card.result_passes(value)


def test_malformed_json_is_rejected() -> None:
    with pytest.raises(report_card.ReportCardError, match="malformed JSON"):
        report_card.parse_report("not-json")


def test_missing_required_field_is_rejected() -> None:
    with pytest.raises(report_card.ReportCardError, match="missing checks"):
        report_card.parse_report(
            json.dumps({"GradeFromPercentage": "A", "average": 1, "issues": 0, "did_error": False})
        )


def test_main_runs_each_module_and_appends_summary(tmp_path: Path) -> None:
    # given
    calls: list[list[str]] = []

    def runner(arguments: list[str], **_: object) -> subprocess.CompletedProcess[str]:
        calls.append(arguments)
        return subprocess.CompletedProcess(arguments, 0, json.dumps(report()), "")

    summary = tmp_path / "summary.md"
    report_root = tmp_path / "report-card"
    report_root.mkdir()
    go_bin = tmp_path / "go-bin"
    go_bin.mkdir()

    # when
    status = report_card.main(
        [
            "--workspace",
            str(tmp_path),
            "--summary",
            str(summary),
            "--report-card-root",
            str(report_root),
            "--go-bin",
            str(go_bin),
            "services/product-service",
            "templates/go-service",
        ],
        runner=runner,
    )

    # then
    assert status == 0
    assert len(calls) == 2
    text = summary.read_text(encoding="utf-8")
    assert "| services/product-service | B | 80.0% | 2 |" in text
    assert "| templates/go-service | B | 80.0% | 2 |" in text


def test_main_preserves_go_runtime_environment(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # given
    captured_environment: dict[str, str] = {}

    def runner(
        arguments: list[str], *, env: dict[str, str], **_: object
    ) -> subprocess.CompletedProcess[str]:
        captured_environment.update(env)
        return subprocess.CompletedProcess(arguments, 0, json.dumps(report()), "")

    summary = tmp_path / "summary.md"
    report_root = tmp_path / "report-card"
    report_root.mkdir()
    go_bin = tmp_path / "go-bin"
    go_bin.mkdir()
    monkeypatch.setenv("HOME", "/managed/home")
    monkeypatch.setenv("GOCACHE", "/managed/go-cache")
    monkeypatch.setenv("GOROOT", "/managed/go-root")
    monkeypatch.setenv("PATH", "/existing/bin")
    monkeypatch.setenv("XDG_CACHE_HOME", "/managed/xdg-cache")

    # when
    status = report_card.main(
        [
            "--workspace",
            str(tmp_path),
            "--summary",
            str(summary),
            "--report-card-root",
            str(report_root),
            "--go-bin",
            str(go_bin),
            "services/product-service",
        ],
        runner=runner,
    )

    # then
    assert status == 0
    assert captured_environment["HOME"] == "/managed/home"
    assert captured_environment["GOCACHE"] == "/managed/go-cache"
    assert captured_environment["GOROOT"] == "/managed/go-root"
    assert captured_environment["PATH"] == f"{go_bin}:/existing/bin"
    assert captured_environment["XDG_CACHE_HOME"] == "/managed/xdg-cache"
    assert captured_environment["GOWORK"] == "off"


@pytest.mark.integration
def test_main_runs_disposable_go_cli_with_preserved_environment(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # given
    go_path = shutil.which("go")
    assert go_path is not None
    report_root = tmp_path / "report-card"
    command_root = report_root / "cmd" / "goreportcard-cli"
    command_root.mkdir(parents=True)
    (report_root / "go.mod").write_text("module example.com/report-card\n\ngo 1.20\n")
    (command_root / "main.go").write_text(
        """package main

import "fmt"

func main() {
	fmt.Print(`{"GradeFromPercentage":"A","average":1,"issues":0,"checks":[],"did_error":false}`)
}
""",
        encoding="utf-8",
    )
    workspace = tmp_path / "workspace"
    (workspace / "services" / "product-service").mkdir(parents=True)
    summary = tmp_path / "summary.md"
    managed_home = tmp_path / "home"
    managed_home.mkdir()
    monkeypatch.setenv("HOME", str(managed_home))
    monkeypatch.delenv("GOCACHE", raising=False)
    monkeypatch.delenv("XDG_CACHE_HOME", raising=False)

    # when
    status = report_card.main(
        [
            "--workspace",
            str(workspace),
            "--summary",
            str(summary),
            "--report-card-root",
            str(report_root),
            "--go-bin",
            str(Path(go_path).parent),
            "services/product-service",
        ]
    )

    # then
    assert status == 0
    assert "| services/product-service | A | 100.0% | 0 |" in summary.read_text(encoding="utf-8")
    assert (managed_home / ".cache" / "go-build").is_dir()


def test_main_accumulates_below_threshold_failure(tmp_path: Path) -> None:
    # given
    def runner(arguments: list[str], **_: object) -> subprocess.CompletedProcess[str]:
        return subprocess.CompletedProcess(arguments, 0, json.dumps(report(average=0.7)), "")

    summary = tmp_path / "summary.md"
    report_root = tmp_path / "report-card"
    report_root.mkdir()
    go_bin = tmp_path / "go-bin"
    go_bin.mkdir()

    # when
    status = report_card.main(
        [
            "--workspace",
            str(tmp_path),
            "--summary",
            str(summary),
            "--report-card-root",
            str(report_root),
            "--go-bin",
            str(go_bin),
            "services/product-service",
        ],
        runner=runner,
    )

    # then
    assert status == 1
