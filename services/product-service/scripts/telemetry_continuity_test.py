#!/usr/bin/env python3
"""Run the disposable Java-to-Go Product trace-continuity integration."""

from __future__ import annotations

import json
import os
import sys
import tempfile
from collections.abc import Sequence
from pathlib import Path

from product_process import ProductCommandError, run

MODULE_ROOT = Path(__file__).resolve().parent.parent


def main(_argv: Sequence[str] | None = None) -> int:
    go = os.environ.get("GO", "")
    try:
        if not go:
            raise ValueError("GO is required")
        java_version = run(
            ["mvn", "-version"],
            cwd=MODULE_ROOT,
            env=os.environ,
            timeout_seconds=15,
            activity="inspect Maven and Java runtime",
            capture=True,
        )
        if "Java version: 14." not in java_version:
            raise ValueError("Java 14 is required")
        run(
            ["docker", "info"],
            cwd=MODULE_ROOT,
            env=os.environ,
            timeout_seconds=30,
            activity="check Docker daemon",
            capture=True,
        )
        with tempfile.TemporaryDirectory(prefix="product-continuity-") as directory:
            result = Path(directory) / "go-test.json"
            environment = dict(os.environ, PRODUCT_CONTINUITY_REQUIRED="1", GOWORK="off")
            run(
                [
                    go,
                    "test",
                    "-mod=readonly",
                    "-count=1",
                    "-race",
                    "-tags=integration",
                    "-json",
                    "-run",
                    "^TestProductContinuity$",
                    "./internal/testing",
                ],
                cwd=MODULE_ROOT,
                env=environment,
                timeout_seconds=900,
                teardown_seconds=20,
                activity="run Product telemetry continuity integration",
                log_path=result,
            )
            records = [json.loads(line) for line in result.read_text(encoding="utf-8").splitlines()]
            if not any(
                item.get("Action") == "pass" and item.get("Test") == "TestProductContinuity"
                for item in records
            ):
                raise ValueError("continuity test was not selected or did not pass")
            sys.stdout.write(result.read_text(encoding="utf-8"))
    except (ValueError, ProductCommandError, OSError, json.JSONDecodeError) as error:
        print(f"test-telemetry-continuity: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
