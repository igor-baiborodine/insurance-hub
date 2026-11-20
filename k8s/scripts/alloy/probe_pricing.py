#!/usr/bin/env python3
"""Emit real Pricing telemetry, including one tariff-read outage, into a caller-owned collector.

The probe owns only its temporary Pricing process, its TLS PostgreSQL container and the
certificate, enrollment and credential files in its temporary root. It prepares the database
through Pricing's own migrate and seed commands, then sends a healthy quote, stops the
database for one bounded quote, restarts it and repeats the healthy quote. The generic backend
harness supplies signal identity, collector coordinates, the failure trace ID and a synthetic
sentinel, then validates the resulting backend records.
"""

from __future__ import annotations

import argparse
import copy
import json
import os
import re
import secrets
import shlex
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

import alloy_runtime

POSTGRES_IMAGE = (
    "postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193"
)
POSTGRES_PASSWORD = "disposable-pricing-password"
ENROLLMENT_CONTEXT = "pricing-observability"
FAILURE_TRACE_ENVIRONMENT = "PRICING_PROBE_FAILURE_TRACE_ID"
SENTINEL_ENVIRONMENT = "PRICING_PROBE_SENTINEL"
TLS_DIRECTORY = "/var/lib/postgresql/tls"
CONTAINER_ID = re.compile(r"^[0-9a-f]{64}$")
TRI_REQUEST: Mapping[str, Any] = {
    "productCode": "TRI",
    "policyFrom": "2017-04-16",
    "policyTo": "2018-04-15",
    "selectedCovers": ["C1", "C2"],
    "answers": [
        {"questionCode": "NUM_OF_ADULTS", "answer": 1},
        {"questionCode": "NUM_OF_CHILDREN", "answer": 1},
        {"questionCode": "DESTINATION", "answer": "EUR"},
    ],
}
# Independent oracle from the accepted TRI fixture, not from the Pricing response mapper.
TRI_QUOTE = {"totalPrice": 78, "coversPrices": {"C1": 26, "C2": 52}}
UNAVAILABLE_BODY = {"code": 14, "message": "service unavailable", "details": []}


class ProbeError(Exception):
    """A bounded Pricing probe failure."""


def run(
    arguments: Sequence[str],
    *,
    cwd: Path | None = None,
    input_text: str | None = None,
    timeout: int = 120,
    redact: bool = False,
) -> str:
    result = subprocess.run(
        list(arguments),
        cwd=cwd,
        input=input_text,
        text=True,
        capture_output=True,
        timeout=timeout,
        check=False,
    )
    if result.returncode != 0:
        # Redacted commands receive credentials on stdin and may echo them in diagnostics.
        diagnostic = (
            "diagnostic redacted" if redact else result.stderr.strip() or result.stdout.strip()
        )
        raise ProbeError(f"command failed ({result.returncode}): {arguments[0]}: {diagnostic}")
    return result.stdout.strip()


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def write_private(path: Path, content: str) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as output:
        output.write(content)


def wait_http(url: str, expected: int = 200, timeout: float = 30) -> None:
    deadline = time.monotonic() + timeout
    last_error = "not attempted"
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                if response.status == expected:
                    return
                last_error = f"HTTP {response.status}"
        except (OSError, urllib.error.URLError) as error:
            last_error = str(error)
        time.sleep(0.25)
    raise ProbeError(f"timed out waiting for {url}: {last_error}")


def wait_postgres(container: str) -> None:
    deadline = time.monotonic() + 60
    consecutive_ready = 0
    while time.monotonic() < deadline:
        result = subprocess.run(
            ["docker", "exec", container, "pg_isready", "-U", "postgres", "-d", "pricing"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=alloy_runtime.teardown_timeout(5),
            check=False,
        )
        if result.returncode == 0:
            consecutive_ready += 1
            # The image briefly accepts connections before stopping its bootstrap
            # server. Require a stable sequence from the final runtime server.
            if consecutive_ready >= 3:
                return
        else:
            consecutive_ready = 0
        time.sleep(0.5)
    raise ProbeError("disposable PostgreSQL did not become ready")


def create_certificate(root: Path, openssl: str = "openssl") -> tuple[Path, Path]:
    """Create a one-day self-signed server certificate that also serves as the trust root."""
    certificate, key = root / "server.crt", root / "server.key"
    run(
        [
            openssl,
            "req",
            "-x509",
            "-newkey",
            "ec",
            "-pkeyopt",
            "ec_paramgen_curve:prime256v1",
            "-nodes",
            "-days",
            "1",
            "-subj",
            "/CN=localhost",
            "-addext",
            "subjectAltName=IP:127.0.0.1,DNS:localhost",
            "-addext",
            "extendedKeyUsage=serverAuth",
            "-keyout",
            str(key),
            "-out",
            str(certificate),
        ]
    )
    # Pricing administration accepts only protected CA, credential and enrollment files.
    certificate.chmod(0o600)
    key.chmod(0o600)
    return certificate, key


def write_ownership(
    path: Path | None,
    *,
    container_id: str,
    process_group: int | None = None,
) -> None:
    if path is None:
        return
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(
        json.dumps({"container_id": container_id, "process_group": process_group}) + "\n",
        encoding="ascii",
    )
    temporary.chmod(0o600)
    os.replace(temporary, path)


def remove_postgres(container_id: str) -> None:
    removal = subprocess.run(
        ["docker", "rm", "-f", "-v", container_id],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        timeout=alloy_runtime.teardown_timeout(15),
        check=False,
    )
    if removal.returncode != 0:
        raise ProbeError(f"could not remove disposable PostgreSQL: {container_id}")


def start_postgres(
    container: str,
    root: Path,
    certificate: Path,
    key: Path,
    ownership_file: Path | None,
    owned_containers: list[str],
) -> tuple[str, int]:
    """Start TLS PostgreSQL on a fixed loopback port that survives the outage restart."""
    environment = root / "postgres.env"
    alloy_runtime.write_protected_env_file(
        environment,
        {"POSTGRES_PASSWORD": POSTGRES_PASSWORD, "POSTGRES_DB": "pricing"},
    )
    port = free_port()
    # PostgreSQL requires a server key owned by its account; install the copied files
    # before handing control to the image entrypoint.
    startup = (
        f"install -d -o postgres -g postgres -m 700 {TLS_DIRECTORY} && "
        f"install -o postgres -g postgres -m 600 /tmp/server.key {TLS_DIRECTORY}/server.key && "
        f"install -o postgres -g postgres -m 644 /tmp/server.crt {TLS_DIRECTORY}/server.crt && "
        "exec docker-entrypoint.sh postgres -c ssl=on "
        f"-c ssl_cert_file={TLS_DIRECTORY}/server.crt -c ssl_key_file={TLS_DIRECTORY}/server.key"
    )
    container_id = run(
        [
            "docker",
            "create",
            "--name",
            container,
            "--env-file",
            str(environment),
            "-p",
            f"127.0.0.1:{port}:5432",
            "--entrypoint",
            "sh",
            POSTGRES_IMAGE,
            "-c",
            startup,
        ]
    )
    if not CONTAINER_ID.fullmatch(container_id):
        raise ProbeError("Docker returned an invalid disposable PostgreSQL identity")
    owned_containers.append(container_id)
    write_ownership(ownership_file, container_id=container_id)
    run(["docker", "cp", str(certificate), f"{container_id}:/tmp/server.crt"])
    run(["docker", "cp", str(key), f"{container_id}:/tmp/server.key"])
    run(["docker", "start", container_id])
    wait_postgres(container_id)
    return container_id, port


def database_url(role: str, password: str, port: int, certificate: Path) -> str:
    return (
        f"postgresql://{role}:{password}@127.0.0.1:{port}/pricing?"
        f"sslmode=verify-full&sslrootcert={certificate}"
    )


def prepare_database(
    args: argparse.Namespace,
    root: Path,
    binaries: Path,
    container: str,
    port: int,
    certificate: Path,
) -> str:
    """Create the service roles, migrate and seed through Pricing's own commands."""
    passwords = {role: secrets.token_hex(16) for role in ("migrator", "reader", "seeder")}
    instance_id, cluster_uid = str(uuid.uuid4()), str(uuid.uuid4())
    setup = (
        args.service_root
        / "internal/administration/testdata/sql/setup-migration-target-identity.sql"
    ).read_text(encoding="utf-8")
    run(
        [
            "docker",
            "exec",
            "-i",
            container,
            "psql",
            "-v",
            "ON_ERROR_STOP=1",
            "-U",
            "postgres",
            "-d",
            "pricing",
        ],
        input_text=setup
        % (
            passwords["migrator"],
            passwords["reader"],
            passwords["seeder"],
            instance_id,
            cluster_uid,
        ),
        redact=True,
    )
    target = root / "target.json"
    write_private(
        target,
        json.dumps(
            {
                "environment": "disposable",
                "context": ENROLLMENT_CONTEXT,
                "dataNamespace": "disposable",
                "serviceNamespace": "disposable",
                "cluster": ENROLLMENT_CONTEXT,
                "clusterUID": cluster_uid,
                "instanceID": instance_id,
                "database": "pricing",
                "host": "127.0.0.1",
                "seedAllowed": True,
                "disposable": True,
            }
        ),
    )
    migrator, seeder = root / "migrator-url", root / "seeder-url"
    write_private(
        migrator,
        database_url("go_pricing_migrator", passwords["migrator"], port, certificate),
    )
    write_private(seeder, database_url("go_pricing_seeder", passwords["seeder"], port, certificate))
    enrollment = [
        "-environment",
        "disposable",
        "-context",
        ENROLLMENT_CONTEXT,
        "-target-file",
        str(target),
        "-ca-file",
        str(certificate),
    ]
    run(
        [
            str(binaries / "migrate"),
            "-operation",
            "migrate",
            *enrollment,
            "-database-url-file",
            str(migrator),
        ],
        cwd=args.service_root,
    )
    run(
        [str(binaries / "seed"), *enrollment, "-database-url-file", str(seeder)],
        cwd=args.service_root,
    )
    return database_url("go_pricing_reader", passwords["reader"], port, certificate)


def calculate(url: str, trace_id: str, body: Mapping[str, Any]) -> tuple[int, Any]:
    request_value = urllib.request.Request(
        url,
        data=json.dumps(body).encode("utf-8"),
        method="POST",
        headers={
            "Accept": "application/json",
            "Content-Type": "application/json",
            "traceparent": f"00-{trace_id}-0123456789abcdef-01",
        },
    )
    try:
        with urllib.request.urlopen(request_value, timeout=15) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as error:
        with error:
            return error.code, json.load(error)


def build_binaries(service_root: Path, binaries: Path) -> None:
    make_command = shlex.split(os.environ.get("MAKE_COMMAND", "make"))
    if not make_command:
        raise ProbeError("MAKE_COMMAND must identify the Make executable")
    run(
        [
            *make_command,
            "--no-print-directory",
            "-C",
            str(service_root),
            "observability-probe-build",
            f"PRICING_OBSERVABILITY_BIN_DIR={binaries}",
        ],
        timeout=240,
    )


def expect_quote(url: str, trace_id: str, description: str) -> None:
    status, body = calculate(url, trace_id, TRI_REQUEST)
    if status != 200 or body != TRI_QUOTE:
        raise ProbeError(
            f"{description} TRI quote did not match the expected response (HTTP {status})"
        )


def failure_request(sentinel: str) -> Mapping[str, Any]:
    body = copy.deepcopy(dict(TRI_REQUEST))
    body["answers"][2]["answer"] = sentinel
    return body


def pricing_environment(
    reader_url: str,
    otlp_endpoint: str,
    version: str,
    instance_id: str,
    http_port: int,
    grpc_port: int,
    health_port: int,
) -> Mapping[str, str]:
    environment = {
        key: value
        for key, value in os.environ.items()
        if key not in {FAILURE_TRACE_ENVIRONMENT, SENTINEL_ENVIRONMENT}
    }
    environment.update(
        {
            "PRICING_DATABASE_URL": reader_url,
            "DB_MAX_CONNS": "4",
            "GOMEMLIMIT": "256MiB",
            "HTTP_ADDR": f"127.0.0.1:{http_port}",
            "GRPC_ADDR": f"127.0.0.1:{grpc_port}",
            "HEALTH_ADDR": f"127.0.0.1:{health_port}",
            "OTEL_ENABLED": "true",
            "OTEL_EXPORTER_OTLP_ENDPOINT": otlp_endpoint,
            "OTEL_EXPORTER_OTLP_TIMEOUT": "2s",
            "DEPLOYMENT_ENVIRONMENT": "qa",
            "SERVICE_VERSION": version,
            "SERVICE_INSTANCE_ID": instance_id,
            "LOG_LEVEL": "info",
        }
    )
    return environment


def exercise_outage(
    container: str,
    calculate_url: str,
    readiness_url: str,
    failure_trace_id: str,
    sentinel: str,
) -> None:
    """Stop only the disposable database for one bounded quote, then restart it.

    A stopped server refuses connections, which Pricing reports as an unavailable reader.
    A hung server would instead exhaust the reader deadline and return DEADLINE_EXCEEDED.
    """
    run(["docker", "stop", "--time", "5", container], timeout=30)
    try:
        status, body = calculate(calculate_url, failure_trace_id, failure_request(sentinel))
    finally:
        run(["docker", "start", container], timeout=30)
        wait_postgres(container)
    if status != 503 or body != UNAVAILABLE_BODY:
        raise ProbeError(
            f"stopped database response did not match the expected unavailable envelope "
            f"(HTTP {status})"
        )
    wait_http(readiness_url, timeout=30)


def execute_in_root(args: argparse.Namespace, root: Path) -> None:
    failure_trace_id = os.environ.get(FAILURE_TRACE_ENVIRONMENT, "")
    sentinel = os.environ.get(SENTINEL_ENVIRONMENT, "")
    if len(failure_trace_id) != 32 or not sentinel:
        raise ProbeError("the harness must supply the failure trace ID and synthetic sentinel")
    container_name = f"ih-pricing-probe-{os.getpid()}"
    container_id: str | None = None
    owned_containers: list[str] = []
    process: subprocess.Popen[bytes] | None = None
    try:
        certificate, key = create_certificate(root)
        container_id, postgres_port = start_postgres(
            container_name,
            root,
            certificate,
            key,
            args.ownership_file,
            owned_containers,
        )
        binaries = root / "bin"
        binaries.mkdir()
        build_binaries(args.service_root, binaries)
        reader_url = prepare_database(
            args, root, binaries, container_id, postgres_port, certificate
        )

        http_port, grpc_port, health_port = free_port(), free_port(), free_port()
        calculate_url = f"http://127.0.0.1:{http_port}/pricing/calculate"
        readiness_url = f"http://127.0.0.1:{health_port}/readyz"
        args.log_file.parent.mkdir(parents=True, exist_ok=True)
        with args.log_file.open("ab", buffering=0) as log_output:
            process = subprocess.Popen(
                [str(binaries / "server")],
                cwd=args.service_root,
                env=pricing_environment(
                    reader_url,
                    args.otlp_endpoint,
                    args.version,
                    args.instance_id,
                    http_port,
                    grpc_port,
                    health_port,
                ),
                stdout=log_output,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
            write_ownership(
                args.ownership_file,
                container_id=container_id,
                process_group=process.pid,
            )
            wait_http(readiness_url, timeout=30)
            expect_quote(calculate_url, args.trace_id, "healthy")
            exercise_outage(container_id, calculate_url, readiness_url, failure_trace_id, sentinel)
            expect_quote(calculate_url, secrets.token_hex(16), "recovered")
            # The metric reader exports every ten seconds. Give one full interval plus
            # the collector's one-second batch enough time before graceful shutdown.
            time.sleep(12)
            alloy_runtime.terminate_process_group(process, graceful_seconds=12, teardown_seconds=17)
            return_code = process.returncode
            if return_code != 0:
                raise ProbeError(f"Pricing exited with status {return_code}")
            process = None
    finally:
        with alloy_runtime.teardown_activity(30):
            primary = sys.exc_info()[1]
            errors: list[Exception] = []
            if process is not None:
                try:
                    alloy_runtime.terminate_process_group(
                        process, graceful_seconds=2, teardown_seconds=5
                    )
                except Exception as error:
                    errors.append(error)
            if owned_containers:
                try:
                    remove_postgres(owned_containers[0])
                except Exception as error:
                    errors.append(error)
            if errors:
                if primary is not None:
                    primary.add_note(f"probe cleanup also failed: {errors}")
                else:
                    raise ProbeError(f"probe cleanup failed: {errors}")
            if args.ownership_file is not None and not errors:
                args.ownership_file.unlink(missing_ok=True)


def execute(args: argparse.Namespace) -> None:
    with tempfile.TemporaryDirectory(prefix="pricing-observability-") as temporary:
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
            alloy_runtime.bounded_activity("Pricing backend probe", 600),
        ):
            execute(args)
    except alloy_runtime.Interrupted as error:
        print(f"Pricing backend probe {error}", file=sys.stderr)
        for note in getattr(error, "__notes__", ()):
            print(note, file=sys.stderr)
        return error.status
    except (
        alloy_runtime.DeadlineExceeded,
        alloy_runtime.TeardownExceeded,
        OSError,
        KeyError,
        ValueError,
        ProbeError,
        subprocess.TimeoutExpired,
    ) as error:
        print(f"Pricing backend probe failed: {error}", file=sys.stderr)
        return 1
    print("Pricing backend probe passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
