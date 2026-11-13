#!/usr/bin/env python3
"""Document adapter for the shared Go service deployment lifecycle."""

from __future__ import annotations

import os
import signal
import sys
from pathlib import Path

REPOSITORY_ROOT = Path(
    os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])
).resolve()
sys.path.insert(0, str(REPOSITORY_ROOT))

import document_deployment_checks as checks  # noqa: E402
from scripts.go.service import deployment as lifecycle  # noqa: E402


def main(argv: list[str] | None = None) -> int:
    return lifecycle.execute(checks, argv)


def cli(argv: list[str] | None = None) -> int:
    received: int | None = None

    def interrupt(signum: int, _frame: object) -> None:
        nonlocal received
        received = received or signum
        raise KeyboardInterrupt

    previous_interrupt = signal.signal(signal.SIGINT, interrupt)
    previous_terminate = signal.signal(signal.SIGTERM, interrupt)
    try:
        try:
            return main(argv)
        except KeyboardInterrupt:
            return 128 + (received or signal.SIGINT)
        except (lifecycle.DeploymentError, checks.DeploymentError, OSError, ValueError) as error:
            print(f"document-deployment: {error}", file=sys.stderr)
            return 1
    finally:
        signal.signal(signal.SIGINT, previous_interrupt)
        signal.signal(signal.SIGTERM, previous_terminate)


if __name__ == "__main__":
    raise SystemExit(cli())
