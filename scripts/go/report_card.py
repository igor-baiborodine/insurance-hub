#!/usr/bin/env python3
"""Run and summarize the archived Go Report Card CLI for selected modules."""

from __future__ import annotations

import argparse
import json
import os
import sys
from collections.abc import Callable
from pathlib import Path, PurePosixPath
from typing import Any

SCRIPT_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPT_ROOT / "python"))
from process_runner import CommandError, run_command  # noqa: E402


class ReportCardError(RuntimeError):
    """A report-card result is malformed or below the required threshold."""


REPORT_CARD_ROLES = {"business-service", "service-scaffold"}


def select_modules(topology_path: Path, requested: list[str]) -> list[str]:
    try:
        topology = json.loads(topology_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        raise ReportCardError("Go module topology is malformed JSON") from error
    if not isinstance(topology, dict) or not isinstance(topology.get("modules"), list):
        raise ReportCardError("Go module topology must contain a module inventory")
    eligible: list[str] = []
    for record in topology["modules"]:
        if not isinstance(record, dict):
            raise ReportCardError("Go module topology contains a malformed module record")
        role = record.get("role")
        directory = record.get("directory")
        if not isinstance(role, str) or not isinstance(directory, str):
            raise ReportCardError("Go module topology module identity is malformed")
        path = PurePosixPath(directory)
        if (
            path.is_absolute()
            or not directory
            or not path.parts
            or ".." in path.parts
            or path.as_posix() != directory
        ):
            raise ReportCardError(f"Go module topology directory is unsafe: {directory}")
        if role in REPORT_CARD_ROLES:
            if directory in eligible:
                raise ReportCardError(f"Go module topology repeats report-card module: {directory}")
            eligible.append(directory)
    if not eligible:
        raise ReportCardError("Go module topology has no report-card modules")
    if requested:
        missing = sorted(set(eligible) - set(requested))
        unexpected = sorted(set(requested) - set(eligible))
        duplicates = sorted(module for module in set(requested) if requested.count(module) > 1)
        if missing or unexpected or duplicates:
            raise ReportCardError(
                "explicit report-card modules differ from topology: "
                f"missing={missing}, unexpected={unexpected}, duplicates={duplicates}"
            )
    return eligible


def parse_report(value: str) -> dict[str, Any]:
    try:
        report = json.loads(value)
    except json.JSONDecodeError as error:
        raise ReportCardError("report-card CLI returned malformed JSON") from error
    if not isinstance(report, dict):
        raise ReportCardError("report-card result must be an object")
    for field in ("GradeFromPercentage", "average", "issues", "checks", "did_error"):
        if field not in report:
            raise ReportCardError(f"report-card result is missing {field}")
    if not isinstance(report["checks"], list):
        raise ReportCardError("report-card checks must be an array")
    return report


def summary_row(module: str, report: dict[str, Any]) -> str:
    try:
        return (
            f"| {module} | {report['GradeFromPercentage']} | "
            f"{float(report['average']) * 100:.1f}% | {int(report['issues'])} |"
        )
    except (TypeError, ValueError) as error:
        raise ReportCardError(
            "report-card grade, score, or issue count has an invalid type"
        ) from error


def result_passes(report: dict[str, Any]) -> bool:
    try:
        return report["did_error"] is False and float(report["average"]) >= 0.75
    except (TypeError, ValueError) as error:
        raise ReportCardError("report-card score has an invalid type") from error


def check_lines(report: dict[str, Any], workspace: Path, module: str) -> list[str]:
    lines: list[str] = []
    for check in report["checks"]:
        if not isinstance(check, dict):
            raise ReportCardError("report-card check must be an object")
        name = str(check.get("name", ""))
        percentage = float(check.get("percentage", 0)) * 100
        error = str(check.get("error", ""))
        suffix = "" if not error else f" (error: {error})"
        lines.append(f"  {name}: {percentage:.0f}%{suffix}")
        summaries = check.get("file_summaries", [])
        if not isinstance(summaries, list):
            raise ReportCardError("report-card file summaries must be an array")
        for summary in summaries:
            filename = str(summary.get("filename", ""))
            if not filename:
                lines.append(f"  {name}: no recognized license file in {module} directory")
                continue
            try:
                display = Path(filename).resolve().relative_to(workspace.resolve())
            except ValueError:
                display = Path(filename)
            errors = summary.get("errors", [])
            if not errors:
                lines.append(f"  {name}: {display}")
            else:
                for item in errors:
                    lines.append(
                        f"  {name}: {display}:{item.get('line_number')}: {item.get('error_string')}"
                    )
    return lines


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser()
    result.add_argument("--workspace", required=True, type=Path)
    result.add_argument("--summary", required=True, type=Path)
    result.add_argument("--report-card-root", required=True, type=Path)
    result.add_argument("--go-bin", required=True, type=Path)
    result.add_argument("--topology", required=True, type=Path)
    result.add_argument("modules", nargs="*")
    return result


def main(
    argv: list[str] | None = None,
    runner: Callable[..., Any] = run_command,
) -> int:
    arguments = parser().parse_args(argv)
    modules = select_modules(arguments.topology, arguments.modules)
    environment = dict(os.environ)
    environment["PATH"] = f"{arguments.go_bin}:{environment.get('PATH', '')}"
    environment["GOWORK"] = "off"
    failed = False
    with arguments.summary.open("a", encoding="utf-8") as summary:
        summary.write("| Module | Grade | Score | Issues |\n| --- | --- | ---: | ---: |\n")
        for module in modules:
            result = runner(
                [
                    "go",
                    "run",
                    "./cmd/goreportcard-cli",
                    "-d",
                    str(arguments.workspace / module),
                    "-j",
                ],
                cwd=arguments.report_card_root,
                env=environment,
                timeout_seconds=600,
                activity=f"go-report-card {module}",
            )
            report = parse_report(result.stdout)
            row = summary_row(module, report)
            summary.write(f"{row}\n")
            print(row.strip("| "))
            print("\n".join(check_lines(report, arguments.workspace, module)))
            if not result_passes(report):
                print(f"{module}: report checks failed or score is below 75%", file=sys.stderr)
                failed = True
    return 1 if failed else 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (CommandError, OSError, ReportCardError) as error:
        print(f"go-report-card: {error}", file=sys.stderr)
        raise SystemExit(1) from None
