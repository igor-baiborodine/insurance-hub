#!/usr/bin/env python3
"""Run the scaffold tooling integration suite with controlled cancellation."""

from __future__ import annotations

import argparse
import os
import signal
import sys
from pathlib import Path

import pytest


class RunnerError(RuntimeError):
    """The scaffold tooling runner configuration is invalid."""


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    return parser.parse_args(argv)


def repository_root(module_root: Path) -> Path:
    configured = os.environ.get("INSURANCE_HUB_ROOT")
    root = Path(configured).resolve() if configured else module_root.parents[1]
    if not (root / "pyproject.toml").is_file():
        raise RunnerError(f"repository pyproject.toml is missing under {root}")
    return root


def run_suite(module_root: Path, root: Path) -> int:
    received_signal: int | None = None

    def handle_signal(signum: int, _frame: object) -> None:
        nonlocal received_signal
        if received_signal is not None:
            return
        received_signal = signum
        raise KeyboardInterrupt

    previous_interrupt = signal.signal(signal.SIGINT, handle_signal)
    previous_terminate = signal.signal(signal.SIGTERM, handle_signal)
    try:
        try:
            status = int(
                pytest.main(
                    [
                        "-q",
                        "-p",
                        "no:cacheprovider",
                        "-c",
                        str(root / "pyproject.toml"),
                        "--rootdir",
                        str(module_root),
                        "-o",
                        "tmp_path_retention_policy=none",
                        "-m",
                        "integration",
                        str(module_root / "scripts" / "test_tooling.py"),
                    ]
                )
            )
        except KeyboardInterrupt:
            received_signal = received_signal or signal.SIGINT
            status = 2
    finally:
        signal.signal(signal.SIGINT, previous_interrupt)
        signal.signal(signal.SIGTERM, previous_terminate)

    if received_signal is not None:
        signal_name = signal.Signals(received_signal).name
        print(f"test-tooling: interrupted by {signal_name}", file=sys.stderr)
        return 128 + received_signal
    return status


def main(argv: list[str] | None = None) -> int:
    parse_args(sys.argv[1:] if argv is None else argv)
    module_root = Path(__file__).resolve().parent.parent
    try:
        return run_suite(module_root, repository_root(module_root))
    except (OSError, RunnerError, ValueError) as error:
        print(f"test-tooling: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
