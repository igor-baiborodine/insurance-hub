"""Import-safe example CLI with explicit subprocess and temporary-file ownership."""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import TextIO

SUCCESS = 0
OPERATIONAL_FAILURE = 1
INTERRUPTED = 130
DEFAULT_TOKEN_ENV = "EXAMPLE_API_TOKEN"


class ToolError(Exception):
    """Expected, sanitized operational failure."""


@dataclass(frozen=True)
class Config:
    source: Path
    output: Path
    inspector: Path
    token_env: str
    timeout_seconds: float


@dataclass(frozen=True)
class CommandResult:
    returncode: int
    stdout: str
    stderr: str


CommandRunner = Callable[[Sequence[str], str, float], CommandResult]


def parse_args(argv: Sequence[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Build a sanitized example inspection report.")
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--inspector", required=True, type=Path)
    parser.add_argument("--token-env", default=DEFAULT_TOKEN_ENV)
    parser.add_argument("--timeout-seconds", default=2.0, type=float)
    return parser.parse_args(argv)


def validate_config(
    arguments: argparse.Namespace,
    environ: Mapping[str, str],
) -> tuple[Config, str]:
    source = arguments.source.resolve()
    output = arguments.output.resolve()
    inspector = arguments.inspector.resolve()
    if not source.is_file():
        raise ToolError(f"source file does not exist: {source}")
    if not inspector.is_file():
        raise ToolError(f"inspector helper does not exist: {inspector}")
    if not output.parent.is_dir():
        raise ToolError(f"output directory does not exist: {output.parent}")
    if source == output:
        raise ToolError("source and output paths must differ")
    if arguments.timeout_seconds <= 0:
        raise ToolError("timeout must be greater than zero")
    token = environ.get(arguments.token_env, "")
    if not token:
        raise ToolError(f"required token environment variable is empty: {arguments.token_env}")
    return (
        Config(
            source=source,
            output=output,
            inspector=inspector,
            token_env=arguments.token_env,
            timeout_seconds=arguments.timeout_seconds,
        ),
        token,
    )


def run_subprocess(arguments: Sequence[str], input_text: str, timeout: float) -> CommandResult:
    try:
        completed = subprocess.run(
            list(arguments),
            check=False,
            capture_output=True,
            input=input_text,
            shell=False,
            text=True,
            timeout=timeout,
        )
    except subprocess.TimeoutExpired as exc:
        raise ToolError(f"inspector timed out after {timeout:g} seconds") from exc
    return CommandResult(completed.returncode, completed.stdout, completed.stderr)


def parse_report(raw_report: str) -> dict[str, object]:
    try:
        report = json.loads(raw_report)
    except json.JSONDecodeError as exc:
        raise ToolError("inspector returned malformed JSON") from exc
    if not isinstance(report, dict):
        raise ToolError("inspector report must be a JSON object")
    if report.get("status") != "ok":
        raise ToolError("inspector report status must be 'ok'")
    records = report.get("records")
    if not isinstance(records, int) or isinstance(records, bool) or records < 0:
        raise ToolError("inspector report records must be a non-negative integer")
    return {"records": records, "status": "ok"}


def redact(message: str, secret: str) -> str:
    return message.replace(secret, "[REDACTED]") if secret else message


def execute(config: Config, token: str, runner: CommandRunner) -> dict[str, object]:
    descriptor, temporary_name = tempfile.mkstemp(
        dir=config.output.parent,
        prefix=f".{config.output.name}.",
    )
    os.close(descriptor)
    temporary_output = Path(temporary_name)
    try:
        command = [sys.executable, str(config.inspector), "--source", str(config.source)]
        result = runner(command, f"{token}\n", config.timeout_seconds)
        if result.returncode != 0:
            detail = redact(result.stderr.strip(), token) or "no diagnostic"
            raise ToolError(f"inspector failed with status {result.returncode}: {detail}")
        if token in result.stdout:
            raise ToolError("inspector output contained protected input")
        report = parse_report(result.stdout)
        temporary_output.write_text(f"{json.dumps(report, sort_keys=True)}\n", encoding="utf-8")
        temporary_output.replace(config.output)
        return report
    finally:
        temporary_output.unlink(missing_ok=True)


def run(
    argv: Sequence[str] | None,
    *,
    runner: CommandRunner = run_subprocess,
    environ: Mapping[str, str] | None = None,
    stdout: TextIO = sys.stdout,
    stderr: TextIO = sys.stderr,
) -> int:
    environment = os.environ if environ is None else environ
    token = ""
    try:
        arguments = parse_args(argv)
        config, token = validate_config(arguments, environment)
        report = execute(config, token, runner)
    except KeyboardInterrupt:
        print("interrupted", file=stderr)
        return INTERRUPTED
    except ToolError as exc:
        print(f"ERROR: {redact(str(exc), token)}", file=stderr)
        return OPERATIONAL_FAILURE
    json.dump({"output": str(config.output), "report": report}, stdout, sort_keys=True)
    stdout.write("\n")
    return SUCCESS


def main(argv: Sequence[str] | None = None) -> int:
    return run(argv)


if __name__ == "__main__":
    raise SystemExit(main())
