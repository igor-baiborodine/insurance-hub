#!/usr/bin/env python3
"""Restore an accepted Product catalog in disposable PostgreSQL and compare identity."""

from __future__ import annotations

import os
import secrets
import sys
import tempfile
import time
from collections.abc import Sequence
from pathlib import Path

from scripts.product_baseline import fixtures
from scripts.python.process_runner import CommandError, run_command

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
IMAGE = "postgres:16.4-alpine"


def command(arguments: list[str], *, timeout: float, activity: str) -> str:
    return run_command(
        arguments,
        cwd=REPOSITORY_ROOT,
        env=os.environ,
        timeout_seconds=timeout,
        teardown_seconds=10,
        activity=activity,
    ).stdout


def main(_argv: Sequence[str] | None = None) -> int:
    environment = os.environ.get("BASELINE_ENV", "")
    container = f"product-baseline-roundtrip-{environment}-{secrets.token_hex(4)}"
    created = False
    try:
        if environment not in fixtures.ENVIRONMENTS:
            raise fixtures.FixtureError("BASELINE_ENV is required; use local-dev or qa.")
        fixture = fixtures.FIXTURE_ROOT / "catalog" / f"{environment}.json"
        fixtures.validate_part("catalog", environment)
        document = fixtures.load_document(fixture)
        with tempfile.TemporaryDirectory(prefix="product-baseline-roundtrip-") as directory:
            temporary = Path(directory)
            catalog = temporary / "catalog.tsv"
            catalog.write_text(
                "".join(
                    f"{row['code']}\t{row['rawLosslessDefinitionJson']}\n"
                    for row in document["rows"]
                ),
                encoding="utf-8",
            )
            os.chmod(temporary, 0o755)
            os.chmod(catalog, 0o644)
            command(
                [
                    "docker",
                    "run",
                    "--detach",
                    "--name",
                    container,
                    "-e",
                    "POSTGRES_PASSWORD=baseline_roundtrip",
                    "-e",
                    "POSTGRES_DB=product_test",
                    "-v",
                    f"{temporary}:/baseline:ro",
                    IMAGE,
                ],
                timeout=60,
                activity="start disposable Product catalog PostgreSQL",
            )
            created = True
            started = time.monotonic()
            print("catalog roundtrip: waiting for disposable PostgreSQL", file=sys.stderr)
            while time.monotonic() - started < 60:
                try:
                    command(
                        [
                            "docker",
                            "exec",
                            container,
                            "pg_isready",
                            "-U",
                            "postgres",
                            "-d",
                            "product_test",
                        ],
                        timeout=5,
                        activity="probe disposable Product catalog PostgreSQL",
                    )
                    break
                except CommandError:
                    time.sleep(1)
            else:
                raise fixtures.FixtureError("Disposable PostgreSQL did not become ready.")
            command(
                [
                    "docker",
                    "exec",
                    container,
                    "psql",
                    "-X",
                    "-v",
                    "ON_ERROR_STOP=1",
                    "-U",
                    "postgres",
                    "-d",
                    "product_test",
                    "-c",
                    "CREATE TABLE public.product (code varchar(255) PRIMARY KEY, definition jsonb NOT NULL); COPY public.product (code, definition) FROM '/baseline/catalog.tsv';",
                ],
                timeout=30,
                activity="restore Product catalog fixture",
            )
            observed = command(
                [
                    "docker",
                    "exec",
                    container,
                    "psql",
                    "-X",
                    "-q",
                    "-A",
                    "-t",
                    "-v",
                    "ON_ERROR_STOP=1",
                    "--field-separator=\t",
                    "-U",
                    "postgres",
                    "-d",
                    "product_test",
                    "-c",
                    "SELECT code, md5(definition::text) FROM public.product ORDER BY code",
                ],
                timeout=30,
                activity="read restored Product catalog identity",
            )
            expected = "".join(
                f"{row['code']}\t{row['checksum']['value']}\n" for row in document["rows"]
            )
            if observed != expected:
                raise fixtures.FixtureError(
                    "Disposable PostgreSQL changed row identity or raw JSON numeric representation."
                )
    except (fixtures.FixtureError, CommandError, OSError, KeyError, TypeError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2
    finally:
        if created:
            try:
                command(
                    ["docker", "rm", "-f", container],
                    timeout=30,
                    activity="remove disposable Product catalog PostgreSQL",
                )
            except CommandError, OSError:
                pass
    print(f"Validated {environment} catalog restore/read round trip in disposable PostgreSQL.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
