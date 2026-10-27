#!/usr/bin/env python3
"""Run one selected Product Go integration suite with bounded owned children."""

from __future__ import annotations

import json
import os
import sys
import tempfile
from collections.abc import Sequence
from pathlib import Path

from product_process import ProductCommandError, run, run_cli

from check_access_results import main as check_access_results

MODULE_ROOT = Path(__file__).resolve().parent.parent
PATTERNS = {
    "all": "^(TestPostgresHarness|TestPostgresReader|TestProductGRPC|TestProductHTTP|TestProductStartup|TestProductParity|TestProductCancellation|TestProductLifecycle|TestProductTelemetry|TestProductReadOnly|TestProductAccess|TestProductSchemaAdoption|TestProductMigration)$",
    "schema-adoption": "^TestProductSchemaAdoption$",
    "migration": "^TestProductMigration$",
    "harness": "^TestPostgresHarness$",
    "reader": "^TestPostgresReader$",
    "grpc": "^TestProductGRPC$",
    "http": "^TestProductHTTP$",
    "startup": "^TestProductStartup$",
    "parity": "^TestProductParity$",
    "cancellation": "^TestProductCancellation$",
    "lifecycle": "^TestProductLifecycle$",
    "telemetry": "^TestProductTelemetry$",
    "read-only": "^TestProductReadOnly$",
    "access": "^TestProductAccess$",
}
TELEMETRY_CASES = (
    "real_HTTP_and_gRPC_reads_export_one_request_observation_each",
    "replacement_process_has_new_metric_identity_and_cumulative_start",
    "disabled_mode_makes_no_exporter_connection",
    "refused_collector_does_not_affect_reads_or_probes",
    "collector_recovery_exports_without_a_Product_restart",
    "SIGTERM_bounds_an_in-flight_export",
)
SCHEMA_ADOPTION_CASES = (
    "fresh_and_sqlc_schema_parity",
    "adoption_and_replay_preserve_catalog",
    "strict_create_rejects_legacy",
    "schema_drift_is_rejected_before_history",
    "existence_only_mutation_accepts_drift",
    "inconsistent_history_is_rejected",
    "statement_failure_preserves_populated_catalog",
    "Goose_Up_bypasses_validation_on_replay",
    "simultaneous_runners_serialize",
    "cancellation_rolls_back_and_retries",
    "lock_contention_is_bounded",
    "inspection_has_no_writes",
    "Goose_inspection_initializes_history",
)
MIGRATION_CASES = (
    "fresh_schema_and_read-only_status",
    "adoption_and_replay_preserve_modified_catalog",
    "target_mismatch_makes_no_history",
    "managed_history_drift_is_rejected",
    "failed_migration_retains_zero_history_and_retries",
    "enrolled_production_migrates_without_seed_role",
    "schema_drift_creates_no_history",
    "simultaneous_runners_and_cancelled_lock",
    "enrolled_seed_role_receives_only_explicit_managed_reads",
)


class IntegrationError(ValueError):
    """The selected Product integration contract was not satisfied."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise IntegrationError(message)


def passed_tests(path: Path) -> set[str]:
    passed: set[str] = set()
    for line in path.read_text(encoding="utf-8").splitlines():
        try:
            record = json.loads(line)
        except json.JSONDecodeError:
            continue
        if record.get("Action") == "pass" and isinstance(record.get("Test"), str):
            passed.add(record["Test"])
    return passed


def main(_argv: Sequence[str] | None = None) -> int:
    suite = os.environ.get("INTEGRATION_SUITE", "")
    fixture_set = os.environ.get("FIXTURE_SET", "")
    coverage = os.environ.get("INTEGRATION_COVERAGE_FILE", "")
    go = os.environ.get("GO", "")
    docker = os.environ.get("DOCKER", "")
    postgres_image = os.environ.get("POSTGRES_IMAGE", "")
    try:
        require(bool(go), "GO is required")
        require(bool(docker), "DOCKER is required")
        require(bool(postgres_image), "POSTGRES_IMAGE is required")
        require(bool(suite), "INTEGRATION_SUITE must not be empty")
        require(suite in PATTERNS, f"unknown INTEGRATION_SUITE: {suite}")
        require(bool(fixture_set), "FIXTURE_SET must not be empty")
        require(fixture_set == "qa", f"unknown FIXTURE_SET: {fixture_set}")
        require(
            "PRODUCT_DATABASE_URL" not in os.environ,
            "PRODUCT_DATABASE_URL must be unset; integration tests own their disposable database",
        )
        run(
            [docker, "info"],
            cwd=MODULE_ROOT,
            env=os.environ,
            timeout_seconds=30,
            activity="check Docker daemon",
            capture=True,
        )
        with tempfile.TemporaryDirectory(prefix="product-service-integration-") as directory:
            temporary = Path(directory)
            result = temporary / "go-test.json"
            environment = dict(os.environ)
            environment.update(
                PRODUCT_TEST_POSTGRES_IMAGE=postgres_image,
                PRODUCT_INTEGRATION_FIXTURE_SET=fixture_set,
                GOWORK="off",
            )
            server_binary = ""
            if suite in {"all", "lifecycle", "telemetry"}:
                server = temporary / "product-service"
                run(
                    [go, "build", "-mod=readonly", "-race", "-o", str(server), "./cmd/server"],
                    cwd=MODULE_ROOT,
                    env=environment,
                    timeout_seconds=600,
                    teardown_seconds=15,
                    activity="build Product integration server",
                    log_path=temporary / "build.log",
                )
                server_binary = str(server)
            environment["PRODUCT_TEST_SERVER_BINARY"] = server_binary
            pattern = (
                "^NoSuchIntegrationTest$"
                if os.environ.get("PRODUCT_TEST_FORCE_ZERO_SELECTION") == "1"
                else PATTERNS[suite]
            )
            arguments = [
                go,
                "test",
                "-mod=readonly",
                "-count=1",
                "-race",
                "-tags=integration",
            ]
            if coverage:
                coverage_path = MODULE_ROOT / coverage
                coverage_path.unlink(missing_ok=True)
                arguments.extend(("-covermode=atomic", f"-coverprofile={coverage}"))
            arguments.extend(
                ("-json", "-run", pattern, "./internal/testing", "./internal/postgres")
            )
            try:
                run(
                    [
                        "bash",
                        str(MODULE_ROOT / "scripts/stream-test-log.sh"),
                        str(result),
                        *arguments,
                    ],
                    cwd=MODULE_ROOT,
                    env=environment,
                    timeout_seconds=1_800,
                    teardown_seconds=20,
                    activity=f"run Product {suite} integration suite",
                )
            finally:
                if result.is_file():
                    sys.stdout.write(result.read_text(encoding="utf-8"))
            passed = passed_tests(result)
            selected = {
                name.split("/", 1)[0]
                for name in passed
                if name.startswith("TestPostgres") or name.startswith("TestProduct")
            }
            require(bool(selected), f"suite {suite} selected zero tests")
            if suite in {"all", "schema-adoption"}:
                for scenario in SCHEMA_ADOPTION_CASES:
                    require(
                        f"TestProductSchemaAdoption/{scenario}" in passed,
                        f"suite {suite} did not pass schema adoption case {scenario}",
                    )
            if suite in {"all", "migration"}:
                for scenario in MIGRATION_CASES:
                    require(
                        f"TestProductMigration/{scenario}" in passed,
                        f"suite {suite} did not pass migration case {scenario}",
                    )
            if suite in {"all", "reader"}:
                require(
                    "TestPostgresReader/real_reads_emit_bounded_database_and_pool_signals"
                    in passed,
                    f"suite {suite} did not pass database telemetry case",
                )
            if suite in {"all", "telemetry"}:
                for scenario in TELEMETRY_CASES:
                    require(
                        f"TestProductTelemetry/{scenario}" in passed,
                        f"suite {suite} did not pass telemetry case {scenario}",
                    )
            if suite in {"all", "access"}:
                require(
                    check_access_results([str(result)]) == 0,
                    "Product access result validation failed",
                )
            if coverage:
                coverage_path = MODULE_ROOT / coverage
                require(
                    coverage_path.is_file() and coverage_path.stat().st_size > 0,
                    f"integration coverage profile was not created: {coverage}",
                )
    except (IntegrationError, ProductCommandError, OSError) as error:
        print(f"test-integration: {error}", file=sys.stderr)
        return 1
    print(
        f"test-integration: suite={suite} fixture-set={fixture_set} "
        f"executed-tests={len(selected)} image={postgres_image}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="test-integration"))
