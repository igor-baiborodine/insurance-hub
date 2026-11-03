#!/usr/bin/env python3
"""Emit real Policy Search signals into a caller-owned disposable collector."""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
import tempfile
from collections.abc import Mapping, Sequence
from pathlib import Path

import alloy_runtime


class ProbeError(Exception):
    """A bounded Policy Search probe failure."""


def environment(args: argparse.Namespace) -> Mapping[str, str]:
    values = dict(os.environ)
    values.update(
        {
            "SERVICE_NAME": "policy-search-service",
            "DEPLOYMENT_ENVIRONMENT": "qa",
            "SERVICE_VERSION": args.version,
            "SERVICE_INSTANCE_ID": args.instance_id,
            "OTEL_ENABLED": "true",
            "OTEL_EXPORTER_OTLP_ENDPOINT": args.otlp_endpoint,
            "OTEL_EXPORTER_OTLP_TIMEOUT": "2s",
            "REQUEST_TIMEOUT": "2s",
            "SHUTDOWN_TIMEOUT": "8s",
            "EVENT_WORKERS_ENABLED": "false",
            "GOMEMLIMIT": "256MiB",
        }
    )
    return values


def run(arguments: Sequence[str], *, cwd: Path, timeout: int = 180) -> None:
    result = subprocess.run(
        list(arguments), cwd=cwd, text=True, capture_output=True, timeout=timeout, check=False
    )
    if result.returncode != 0:
        diagnostic = result.stderr.strip() or result.stdout.strip()
        raise ProbeError(f"command failed ({result.returncode}): {arguments[0]}: {diagnostic}")


def execute_in_root(args: argparse.Namespace, root: Path) -> None:
    binary = root / "policy-search-observability-probe"
    run(
        [
            "go",
            "build",
            "-mod=readonly",
            "-o",
            str(binary),
            "./internal/testing/cmd/observabilityprobe",
        ],
        cwd=args.service_root,
    )
    args.log_file.parent.mkdir(parents=True, exist_ok=True)
    process: subprocess.Popen[bytes] | None = None
    try:
        with args.log_file.open("ab", buffering=0) as output:
            process = subprocess.Popen(
                [str(binary), "--trace-id", args.trace_id],
                cwd=args.service_root,
                env=environment(args),
                stdout=output,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
            if args.ownership_file is not None:
                args.ownership_file.write_text(f"{process.pid}\n", encoding="ascii")
            try:
                return_code = process.wait(timeout=60)
            except subprocess.TimeoutExpired:
                alloy_runtime.terminate_process_group(
                    process, graceful_seconds=3, teardown_seconds=8
                )
                raise ProbeError("Policy Search observability probe timed out") from None
            if return_code != 0:
                raise ProbeError(f"Policy Search observability probe exited with {return_code}")
            process = None
    finally:
        with alloy_runtime.teardown_activity(15):
            primary = sys.exc_info()[1]
            cleanup_error: Exception | None = None
            if process is not None:
                try:
                    alloy_runtime.terminate_process_group(
                        process, graceful_seconds=2, teardown_seconds=5
                    )
                except Exception as error:
                    cleanup_error = error
            if cleanup_error is not None:
                if primary is not None:
                    primary.add_note(f"probe cleanup also failed: {cleanup_error}")
                else:
                    raise ProbeError(f"probe cleanup failed: {cleanup_error}")
            if args.ownership_file is not None and cleanup_error is None:
                args.ownership_file.unlink(missing_ok=True)


def execute(args: argparse.Namespace) -> None:
    with tempfile.TemporaryDirectory(prefix="policy-search-observability-") as temporary:
        execute_in_root(args, Path(temporary))


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--service-root", type=Path, required=True)
    parser.add_argument("--otlp-endpoint", required=True)
    parser.add_argument("--log-file", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--instance-id", required=True)
    parser.add_argument("--trace-id", required=True)
    parser.add_argument("--ownership-file", type=Path)
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        with (
            alloy_runtime.controlled_signals(),
            alloy_runtime.bounded_activity("Policy Search backend probe", 300),
        ):
            execute(args)
    except (
        alloy_runtime.DeadlineExceeded,
        alloy_runtime.Interrupted,
        alloy_runtime.TeardownExceeded,
        OSError,
        ProbeError,
        subprocess.TimeoutExpired,
    ) as error:
        print(f"Policy Search backend probe failed: {error}", file=sys.stderr)
        return getattr(error, "status", 1)
    print("Policy Search backend probe passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
