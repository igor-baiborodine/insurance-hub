#!/usr/bin/env python3
"""Atomically accept one reviewed Product baseline capture."""

from __future__ import annotations

import argparse
import os
import shutil
import sys
import tempfile
from collections.abc import Sequence
from pathlib import Path

from scripts.product_baseline import fixtures

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
DESTINATIONS = {
    "access": fixtures.FIXTURE_ROOT / "access",
    "catalog": fixtures.FIXTURE_ROOT / "catalog",
    "http": fixtures.FIXTURE_ROOT / "http",
}


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("part", choices=DESTINATIONS)
    parser.add_argument("run_file", type=Path)
    arguments = parser.parse_args(argv)
    try:
        source = arguments.run_file.resolve()
        if not source.is_file():
            raise fixtures.FixtureError(f"Provide an existing captured {arguments.part} run file.")
        environment = fixtures.validate_part(arguments.part, explicit_file=str(source))[0]
        destination = DESTINATIONS[arguments.part] / f"{environment}.json"
        destination.parent.mkdir(parents=True, exist_ok=True)
        descriptor, temporary_name = tempfile.mkstemp(
            prefix=f".{destination.name}.", dir=destination.parent
        )
        temporary = Path(temporary_name)
        try:
            with os.fdopen(descriptor, "wb") as output, source.open("rb") as captured:
                shutil.copyfileobj(captured, output)
                output.flush()
                os.fsync(output.fileno())
            os.chmod(temporary, 0o644)
            fixtures.validate_part(arguments.part, explicit_file=str(temporary))
            os.replace(temporary, destination)
        finally:
            temporary.unlink(missing_ok=True)
    except (OSError, fixtures.FixtureError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2
    try:
        display_path = destination.relative_to(REPOSITORY_ROOT)
    except ValueError:
        display_path = destination
    print(f"Accepted reviewed {environment} Product {arguments.part} fixture at {display_path}.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
