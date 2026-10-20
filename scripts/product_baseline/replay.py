#!/usr/bin/env python3
"""Replay one accepted Product baseline through disposable Java integrations."""

from __future__ import annotations

import os
import sys
from collections.abc import Sequence
from pathlib import Path

from scripts.product_baseline import fixtures
from scripts.python.process_runner import CommandError, run_command

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]


def run(arguments: list[str], activity: str, timeout: float) -> None:
    result = run_command(
        arguments,
        cwd=REPOSITORY_ROOT,
        env=os.environ,
        timeout_seconds=timeout,
        teardown_seconds=15,
        activity=activity,
    )
    if result.stdout:
        sys.stdout.write(result.stdout)
    if result.stderr:
        sys.stderr.write(result.stderr)


def main(_argv: Sequence[str] | None = None) -> int:
    fixture_set = os.environ.get("BASELINE_FIXTURE_SET", "")
    try:
        if fixture_set not in fixtures.ENVIRONMENTS:
            raise fixtures.FixtureError("BASELINE_FIXTURE_SET is required; use local-dev or qa.")
        for part in ("inventory", "catalog", "http"):
            fixtures.validate_part(part, fixture_set)
        run(
            [str(REPOSITORY_ROOT / "scripts/product-baseline/resolve-java.sh")],
            "resolve Product baseline Java dependencies",
            600,
        )
        print(
            f"Replaying {fixture_set} direct Product observations against fresh disposable PostgreSQL..."
        )
        run(
            [
                "mvn",
                "-B",
                "-ntp",
                "-f",
                "legacy/product-service/pom.xml",
                "-Dskip.surefire.tests=true",
                "-Dit.test=ProductBaselineReplayIT",
                f"-Dbaseline.fixture.set={fixture_set}",
                "verify",
            ],
            "replay direct Product baseline",
            1_200,
        )
        http_fixture = fixtures.FIXTURE_ROOT / "http" / f"{fixture_set}.json"
        print(
            f"Replaying {fixture_set} gateway observations through a real isolated gateway listener..."
        )
        run(
            [
                "mvn",
                "-B",
                "-ntp",
                "-f",
                "legacy/agent-portal-gateway/pom.xml",
                "-Dskip.surefire.tests=true",
                "-Dit.test=GatewayBaselineReplayIT",
                f"-Dbaseline.fixture.set={fixture_set}",
                f"-Dbaseline.fixture.file={http_fixture}",
                "verify",
            ],
            "replay gateway Product baseline",
            1_200,
        )
    except (fixtures.FixtureError, CommandError, OSError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
