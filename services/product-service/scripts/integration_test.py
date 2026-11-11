#!/usr/bin/env python3
"""Run one selected Product Go integration suite with bounded owned children."""

from __future__ import annotations

import json
import os
import sys
import tempfile
from collections import Counter
from collections.abc import Sequence
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
if str(REPOSITORY_ROOT) not in sys.path:
    sys.path.insert(0, str(REPOSITORY_ROOT))

from scripts.go.service.tooling_process import run, run_cli  # noqa: E402
from scripts.python.process_runner import CommandError as ProductCommandError  # noqa: E402

MODULE_ROOT = Path(__file__).resolve().parent.parent
PATTERNS = {
    "all": "^(TestPostgresHarness|TestPostgresReader|TestProductGRPC|TestProductHTTP|TestProductStartup|TestProductParity|TestProductCancellation|TestProductLifecycle|TestProductTelemetry|TestProductReadOnly|TestProductAccess|TestProductMigration|TestProductSeed|TestProductMarkerColumns)$",
    "marker-columns": "^TestProductMarkerColumns$",
    "migration": "^TestProductMigration$",
    "seed": "^TestProductSeed$",
    "seed-replay": "^TestProductSeed/repeat_reports_every_accepted_product_as_existing$",
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
MIGRATION_CASES = (
    "grant_failure_is_atomic_and_retryable",
    "owner-only_adoption_applies_reader_grants",
    "ordinary_indexes_and_constraint_names_are_compatible",
    "replay_restores_intended_grants",
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
SEED_CASES = (
    "empty_catalog_matches_accepted_storage_and_transports",
    "repeat_reports_every_accepted_product_as_existing",
    "same_engine_applies_a_supplemental_environment_catalog",
    "partial_modified_catalog_preserves_existing_and_unrelated_rows",
    "concurrent_seeds_serialize_without_duplicates",
    "cancelled_lock_leaves_catalog_empty",
    "lost_acknowledgement_replay_is_insert_only",
    "disabled_target_rejects_before_writes",
)
MARKER_COLUMN_CASES = tuple(
    f"{state}/{scenario}"
    for state in ("fresh", "managed")
    for scenario in (
        "owner_update",
        "migrator_update",
        "seeder_update",
        "seeder_insert",
        "reader_select",
        "seeder_grant_option",
    )
)
ACCESS_CATEGORIES = (
    "NONE",
    "VALID",
    "MALFORMED",
    "SIGNATURE",
    "EXPIRED",
    "NBF",
    "ISSUER",
    "AUDIENCE",
    "ROLE",
)
ACCESS_PREFIXES = (
    "TestProductAccess/accepted_HTTP_credential_matrix_remains_anonymous/",
    "TestProductAccess/generated_gRPC_client_ignores_identity_metadata/",
)
EXPECTED_ACCESS_CASES = {
    prefix + f"ACCESS-DIRECT-{route}-{category}-001"
    for prefix in ACCESS_PREFIXES
    for route in ("LIST", "GET")
    for category in ACCESS_CATEGORIES
}


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


def validate_access_results(path: Path) -> None:
    """Require every direct Product access case in the selected Go JSON report."""
    actions: Counter[str] = Counter()
    case_actions: Counter[tuple[str, str]] = Counter()
    for line in path.read_text(encoding="utf-8").splitlines():
        record = json.loads(line)
        action = record.get("Action")
        name = record.get("Test", "")
        if action in {"fail", "skip"} and (name.startswith("TestProductAccess") or not name):
            raise IntegrationError("Product access test failed or skipped")
        if name == "TestProductAccess":
            actions[action] += 1
        if name in EXPECTED_ACCESS_CASES or any(
            name.startswith(prefix) for prefix in ACCESS_PREFIXES
        ):
            case_actions[(name, action)] += 1
    require(
        actions["run"] == 1 and actions["pass"] == 1,
        "Product access test did not run and pass exactly once",
    )
    require(
        {name for name, _action in case_actions} == EXPECTED_ACCESS_CASES,
        "direct access case set is incomplete or altered",
    )
    require(
        all(
            case_actions[(name, "run")] == 1 and case_actions[(name, "pass")] == 1
            for name in EXPECTED_ACCESS_CASES
        ),
        "direct access case did not run and pass exactly once",
    )


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
            output = ""
            try:
                output = run(
                    arguments,
                    cwd=MODULE_ROOT,
                    env=environment,
                    timeout_seconds=1_800,
                    teardown_seconds=20,
                    activity=f"run Product {suite} integration suite",
                    capture=True,
                )
            except ProductCommandError as error:
                output = error.stdout
                if error.stderr:
                    print(error.stderr, end="", file=sys.stderr)
                raise
            finally:
                result.write_text(output, encoding="utf-8")
                sys.stdout.write(output)
            passed = passed_tests(result)
            selected = {
                name.split("/", 1)[0]
                for name in passed
                if name.startswith("TestPostgres") or name.startswith("TestProduct")
            }
            require(bool(selected), f"suite {suite} selected zero tests")
            if suite in {"all", "marker-columns"}:
                for scenario in MARKER_COLUMN_CASES:
                    require(
                        f"TestProductMarkerColumns/{scenario}" in passed,
                        f"suite {suite} did not pass marker column case {scenario}",
                    )
            if suite in {"all", "migration"}:
                for scenario in MIGRATION_CASES:
                    require(
                        f"TestProductMigration/{scenario}" in passed,
                        f"suite {suite} did not pass migration case {scenario}",
                    )
            if suite in {"all", "seed"}:
                for scenario in SEED_CASES:
                    require(
                        f"TestProductSeed/{scenario}" in passed,
                        f"suite {suite} did not pass seed case {scenario}",
                    )
            if suite == "seed-replay":
                scenario = "repeat_reports_every_accepted_product_as_existing"
                require(
                    f"TestProductSeed/{scenario}" in passed,
                    f"suite {suite} did not pass seed case {scenario}",
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
                validate_access_results(result)
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
