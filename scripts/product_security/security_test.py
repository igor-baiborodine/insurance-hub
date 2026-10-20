#!/usr/bin/env python3
"""Run the disposable Java gateway to Go Product security boundary."""

from __future__ import annotations

import copy
import json
import os
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
from collections.abc import Callable, Sequence
from pathlib import Path

from scripts.product_security import check_cases, check_java_results

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
SERVICE_ROOT = REPOSITORY_ROOT / "services/product-service"
SERVICE_SCRIPTS = SERVICE_ROOT / "scripts"
if str(SERVICE_SCRIPTS) not in sys.path:
    sys.path.insert(0, str(SERVICE_SCRIPTS))

from product_process import ProductCommandError, run, run_cli  # noqa: E402


class SecurityTestError(RuntimeError):
    """The disposable Product security boundary failed its contract."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SecurityTestError(message)


def terminate(process: subprocess.Popen[str], timeout: float = 10) -> None:
    if process.poll() is not None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except OSError:
        return
    try:
        process.wait(timeout=timeout / 2)
        return
    except subprocess.TimeoutExpired:
        pass
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except OSError:
        pass
    process.wait(timeout=timeout / 2)


def scan_output(logs: Sequence[Path], markers: Sequence[Path]) -> None:
    output = "\n".join(
        path.read_text(encoding="utf-8", errors="replace") for path in logs if path.exists()
    )
    protected = [
        value
        for path in markers
        if path.exists()
        for value in path.read_text(encoding="utf-8").splitlines()
        if value
    ]
    require(bool(protected), "security integration produced no private diagnostic markers")
    require(
        not any(value in output for value in protected),
        "captured output exposed a generated marker",
    )
    require(
        re.search(r"postgres(?:ql)?://|password=", output, re.IGNORECASE) is None,
        "captured output exposed a database connection string",
    )


def java_test(
    temporary: Path,
    endpoint: Path,
    qa_access: Path,
    cases: Path,
    java_markers: Path,
    java_log: Path,
    *,
    method: str = "GatewayProductSecurityIT",
    expected_failure: bool = False,
    validate_report: bool = False,
) -> None:
    report = (
        REPOSITORY_ROOT
        / "legacy/agent-portal-gateway/target/failsafe-reports/TEST-pl.altkom.asc.lab.micronaut.poc.gateway.GatewayProductSecurityIT.xml"
    )
    report.unlink(missing_ok=True)
    java_markers.unlink(missing_ok=True)
    configured_timeout = os.environ.get(
        "PRODUCT_SECURITY_TEST_JAVA_TIMEOUT",
        os.environ.get("PRODUCT_SECURITY_TEST_JAVA_TIMEOUT_SECONDS", "150"),
    )
    if configured_timeout.endswith("s"):
        configured_timeout = configured_timeout[:-1]
    try:
        timeout = int(configured_timeout)
    except ValueError as error:
        raise SecurityTestError(
            "PRODUCT_SECURITY_TEST_JAVA_TIMEOUT must be whole seconds"
        ) from error
    require(timeout > 0, "PRODUCT_SECURITY_TEST_JAVA_TIMEOUT must be positive")
    run(
        [
            "mvn",
            "-B",
            "-ntp",
            "-f",
            "legacy/agent-portal-gateway/pom.xml",
            "-Dskip.surefire.tests=true",
            f"-Dit.test={method}",
            f"-Dproduct.security.endpoint.file={endpoint}",
            f"-Dproduct.security.qa.access.file={qa_access}",
            f"-Dproduct.security.cases.file={cases}",
            f"-Dproduct.security.markers.file={java_markers}",
            f"-Dproduct.security.clock.offset.seconds={os.environ.get('PRODUCT_SECURITY_TEST_CLOCK_OFFSET_SECONDS', '0')}",
            "verify",
        ],
        cwd=REPOSITORY_ROOT,
        env=os.environ,
        timeout_seconds=timeout,
        teardown_seconds=15,
        activity="run Java gateway Product security suite",
        log_path=java_log,
        expected_failure=expected_failure,
    )
    require(
        java_markers.is_file() and java_markers.stat().st_size > 0,
        "Java test did not write private diagnostic markers",
    )
    if validate_report:
        require(
            check_java_results.main([str(report)]) == 0,
            "Java security result validation failed",
        )


def altered_case_file(
    source: Path,
    destination: Path,
    predicate: Callable[[dict[str, object]], bool],
) -> None:
    document = json.loads(source.read_text(encoding="utf-8"))
    altered = copy.deepcopy(document)
    altered["cases"] = [case for case in altered["cases"] if not predicate(case)]
    require(
        len(altered["cases"]) < len(document["cases"]),
        "controlled case mutation matched no cases",
    )
    destination.write_text(json.dumps(altered), encoding="utf-8")


def listeners_closed(record: dict[str, object]) -> bool:
    for name in ("http", "grpc", "health"):
        host, port = str(record[name]).rsplit(":", 1)
        with socket.socket() as connection:
            connection.settimeout(0.3)
            if connection.connect_ex((host, int(port))) == 0:
                return False
    return True


def main(_argv: Sequence[str] | None = None) -> int:
    helper: subprocess.Popen[str] | None = None
    helper_log_handle = None
    try:
        go = os.environ.get("GO", "go")
        for name, executable in (
            ("java", "java"),
            ("mvn", "mvn"),
            ("docker", "docker"),
            ("go", go),
        ):
            require(shutil.which(executable) is not None, f"missing prerequisite: {name}")
        require("PRODUCT_DATABASE_URL" not in os.environ, "PRODUCT_DATABASE_URL must be unset")
        cases = Path(
            os.environ.get(
                "PRODUCT_SECURITY_CASES_FILE",
                REPOSITORY_ROOT
                / "legacy/product-service/src/test/resources/product-read-baseline/access/cases.json",
            )
        )
        require(check_cases.main([str(cases)]) == 0, "Product security case validation failed")
        java_details = run(
            ["mvn", "-version"],
            cwd=REPOSITORY_ROOT,
            env=os.environ,
            timeout_seconds=15,
            activity="inspect Product security Java runtime",
            capture=True,
        )
        require("Java version: 14." in java_details, "Java 14 is required")
        try:
            run(
                ["docker", "info"],
                cwd=REPOSITORY_ROOT,
                env=os.environ,
                timeout_seconds=30,
                activity="check Docker daemon",
                capture=True,
            )
        except ProductCommandError as error:
            raise SecurityTestError("Docker daemon is unavailable") from error
        with tempfile.TemporaryDirectory(prefix="product-security-") as directory:
            temporary = Path(directory)
            os.chmod(temporary, 0o700)
            endpoint = temporary / "endpoint.json"
            helper_log = temporary / "helper.log"
            java_log = temporary / "java.log"
            database_markers = temporary / "database-markers"
            java_markers = temporary / "java-markers"
            stop_file = temporary / "stop"
            harness = temporary / "security-harness"
            run(
                [
                    "make",
                    "-C",
                    str(SERVICE_ROOT),
                    "build-security-harness",
                    f"SECURITY_HARNESS_OUTPUT={harness}",
                ],
                cwd=REPOSITORY_ROOT,
                env=os.environ,
                timeout_seconds=180,
                teardown_seconds=15,
                activity="build Product security harness",
                log_path=temporary / "build.log",
            )
            helper_log_handle = helper_log.open("w", encoding="utf-8")
            helper = subprocess.Popen(
                [
                    str(harness),
                    "-endpoint-file",
                    str(endpoint),
                    "-markers-file",
                    str(database_markers),
                    "-stop-file",
                    str(stop_file),
                ],
                cwd=REPOSITORY_ROOT,
                env=os.environ,
                text=True,
                stdout=helper_log_handle,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
            started = time.monotonic()
            while not endpoint.is_file() or endpoint.stat().st_size == 0:
                require(helper.poll() is None, "Go harness exited before publishing its endpoint")
                elapsed = time.monotonic() - started
                require(elapsed < 120, "Go harness readiness exceeded 120 seconds")
                if int(elapsed) and int(elapsed) % 30 == 0:
                    print(
                        f"product-security-test: waiting for harness after {elapsed:.0f}s",
                        file=sys.stderr,
                    )
                time.sleep(0.1)
            record = json.loads(endpoint.read_text(encoding="utf-8"))
            require(
                record.get("pid") == helper.pid
                and record.get("fixture") == "DATA-QA-001"
                and record.get("rowCount") == 4
                and record.get("reader") == "go_product_reader",
                "invalid Go endpoint handshake",
            )
            scan_output((helper_log, java_log), (database_markers, java_markers))
            if os.environ.get("PRODUCT_SECURITY_TEST_INJECT_FAILURE") == "after_helper_ready":
                time.sleep(2)
                raise SecurityTestError("injected partial startup failure after Go readiness")
            qa_access = (
                REPOSITORY_ROOT
                / "legacy/product-service/src/test/resources/product-read-baseline/access/qa.json"
            )
            if os.environ.get("PRODUCT_SECURITY_TEST_INJECT_FAILURE") == "backend_down":
                stop_file.touch()
                helper.wait(timeout=35)
                require(helper.returncode == 0, "injected backend shutdown failed")
                helper = None
            try:
                java_test(
                    temporary,
                    endpoint,
                    qa_access,
                    cases,
                    java_markers,
                    java_log,
                    validate_report=True,
                )
            except ProductCommandError as error:
                if "deadline" in str(error):
                    raise SecurityTestError("Java security suite timed out") from error
                if os.environ.get("PRODUCT_SECURITY_TEST_CLOCK_OFFSET_SECONDS", "0") != "0":
                    raise SecurityTestError(
                        "bounded token clock drift rejected the suite"
                    ) from error
                raise SecurityTestError("healthy gateway-to-Go smoke test failed") from error
            scan_output((helper_log, java_log), (database_markers, java_markers))
            require(helper is not None, "healthy gateway-to-Go smoke test failed")

            controlled_cases = (
                (
                    "rejection category",
                    lambda case: case.get("id") == "ACCESS-GATEWAY-LIST-EXPIRED-001",
                    "GatewayProductSecurityIT#rejectedCredentialsStopAtGateway",
                    "REJECTION_CASE_SET_MISMATCH",
                ),
                (
                    "rejected gateway get route",
                    lambda case: (
                        case.get("target") == "gateway"
                        and case.get("path") == "/api/products/TRI"
                        and case.get("expected", {}).get("status") == 401
                    ),
                    "GatewayProductSecurityIT#rejectedCredentialsStopAtGateway",
                    "REJECTION_CASE_SET_MISMATCH",
                ),
                (
                    "accepted category",
                    lambda case: case.get("id") == "ACCESS-GATEWAY-LIST-AUDIENCE-001",
                    "GatewayProductSecurityIT#givenAcceptedClaimVariants_thenCatalogResultsPreserved",
                    "ACCEPTED_CASE_SET_MISMATCH",
                ),
                (
                    "accepted gateway get route",
                    lambda case: (
                        case.get("target") == "gateway"
                        and case.get("path") == "/api/products/TRI"
                        and case.get("expected", {}).get("status") == 200
                    ),
                    "GatewayProductSecurityIT#givenAcceptedClaimVariants_thenCatalogResultsPreserved",
                    "ACCEPTED_CASE_SET_MISMATCH",
                ),
            )
            for index, (label, predicate, method, guard) in enumerate(controlled_cases):
                altered = temporary / f"altered-{index}.json"
                altered_case_file(cases, altered, predicate)
                java_test(
                    temporary,
                    endpoint,
                    qa_access,
                    altered,
                    java_markers,
                    java_log,
                    method=method,
                    expected_failure=True,
                )
                require(
                    guard in java_log.read_text(encoding="utf-8"),
                    f"{label} failed outside its case-set guard",
                )
                scan_output((helper_log, java_log), (database_markers, java_markers))

            stop_file.touch()
            helper.wait(timeout=35)
            require(helper.returncode == 0, "Go harness shutdown failed")
            helper = None
            container = record.get("containerId", "")
            inspection = subprocess.run(
                ["docker", "inspect", str(container)],
                capture_output=True,
                text=True,
                timeout=15,
                check=False,
            )
            require(inspection.returncode != 0, "disposable PostgreSQL container was not removed")
            require(listeners_closed(record), "Go listener remained open after shutdown")
            java_test(
                temporary,
                endpoint,
                qa_access,
                cases,
                java_markers,
                java_log,
                method="GatewayProductSecurityIT#happyPath",
                expected_failure=True,
            )
            stopped_output = java_log.read_text(encoding="utf-8")
            require(
                "PRODUCT_SECURITY_SMOKE_LIST_START" in stopped_output
                and re.search(
                    r"Tests run: 1, Failures: [1-9]|Tests run: 1, Failures: 0, Errors: [1-9]",
                    stopped_output,
                )
                is not None,
                "Go-absent run failed outside the expected test assertion",
            )
            scan_output((helper_log, java_log), (database_markers, java_markers))
    except (
        SecurityTestError,
        ProductCommandError,
        OSError,
        ValueError,
        json.JSONDecodeError,
        subprocess.SubprocessError,
    ) as error:
        print(f"product-security-test: {error}", file=sys.stderr)
        return 1
    finally:
        if helper is not None:
            try:
                terminate(helper)
            except OSError, subprocess.SubprocessError:
                print("product-security-test: helper cleanup failed", file=sys.stderr)
        if helper_log_handle is not None:
            helper_log_handle.close()
    print(
        "product-security-test: real gateway list/get, ten rejections, ten accepted claims, "
        "identity and trace observer, and anonymous gRPC passed; omission guards and Go-absent "
        "rerun failed as expected; resources released"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="product-security-test"))
