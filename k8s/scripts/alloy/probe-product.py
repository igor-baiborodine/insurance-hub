#!/usr/bin/env python3
"""Emit real Product telemetry into a caller-owned disposable collector.

The probe owns only its temporary Product process and PostgreSQL container. The
generic backend harness supplies signal identity, collector coordinates, and a
log file, then validates the resulting backend records.
"""

from __future__ import annotations

import argparse
import json
import os
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Mapping, Optional, Sequence


POSTGRES_IMAGE = (
    "postgres:17.10-alpine@sha256:"
    "742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193"
)
POSTGRES_PASSWORD = "disposable-product-password"


class ProbeError(Exception):
    """A bounded Product probe failure."""


def run(
    arguments: Sequence[str],
    *,
    cwd: Optional[Path] = None,
    input_text: Optional[str] = None,
    timeout: int = 120,
) -> str:
    result = subprocess.run(
        list(arguments),
        cwd=cwd,
        input=input_text,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=timeout,
        check=False,
    )
    if result.returncode != 0:
        diagnostic = result.stderr.strip() or result.stdout.strip()
        raise ProbeError(f"command failed ({result.returncode}): {arguments[0]}: {diagnostic}")
    return result.stdout.strip()


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


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
            ["docker", "exec", container, "pg_isready", "-U", "postgres", "-d", "product"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
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


def request(url: str, trace_id: str) -> None:
    request_value = urllib.request.Request(
        url,
        headers={"traceparent": f"00-{trace_id}-0123456789abcdef-01"},
    )
    with urllib.request.urlopen(request_value, timeout=5) as response:
        if response.status != 200:
            raise ProbeError(f"Product read returned HTTP {response.status}")
        body = json.load(response)
        if not isinstance(body, list) or not body:
            raise ProbeError("Product read returned no catalog rows")


def product_environment(
    database_url: str,
    otlp_endpoint: str,
    version: str,
    instance_id: str,
    http_port: int,
    grpc_port: int,
    health_port: int,
) -> Mapping[str, str]:
    environment = dict(os.environ)
    environment.update(
        {
            "PRODUCT_DATABASE_URL": database_url,
            "DB_MAX_CONNS": "4",
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


def execute(args: argparse.Namespace) -> None:
    container = f"ih-product-probe-{os.getpid()}"
    process: Optional[subprocess.Popen[bytes]] = None
    try:
        run(
            [
                "docker",
                "run",
                "-d",
                "--name",
                container,
                "-e",
                f"POSTGRES_PASSWORD={POSTGRES_PASSWORD}",
                "-e",
                "POSTGRES_DB=product",
                "-p",
                "127.0.0.1::5432",
                POSTGRES_IMAGE,
            ]
        )
        wait_postgres(container)
        port_text = run(["docker", "port", container, "5432/tcp"])
        postgres_port = int(port_text.rsplit(":", 1)[1])
        fixture_path = (
            args.service_root
            / "../../legacy/product-service/src/test/resources/"
            "product-read-baseline/catalog/qa.json"
        ).resolve()
        fixture = json.loads(fixture_path.read_text(encoding="utf-8"))
        row = fixture["rows"][0]
        setup_sql = """
CREATE TABLE public.product (code varchar(255) PRIMARY KEY, definition jsonb NOT NULL);
INSERT INTO public.product (code, definition) VALUES (
  :'code', :'definition'::jsonb
);
"""
        run(
            [
                "docker",
                "exec",
                "-i",
                container,
                "psql",
                "-v",
                "ON_ERROR_STOP=1",
                "-v",
                f"code={row['code']}",
                "-v",
                f"definition={row['rawLosslessDefinitionJson']}",
                "-U",
                "postgres",
                "-d",
                "product",
            ],
            input_text=setup_sql,
        )

        http_port, grpc_port, health_port = free_port(), free_port(), free_port()
        with tempfile.TemporaryDirectory(prefix="product-observability-") as build_dir:
            binary = Path(build_dir) / "product-service"
            run(
                ["go", "build", "-mod=readonly", "-o", str(binary), "./cmd/server"],
                cwd=args.service_root,
                timeout=180,
            )
            database_url = (
                f"postgres://postgres:{POSTGRES_PASSWORD}@127.0.0.1:{postgres_port}/"
                "product?sslmode=disable"
            )
            args.log_file.parent.mkdir(parents=True, exist_ok=True)
            with args.log_file.open("ab", buffering=0) as log_output:
                process = subprocess.Popen(
                    [str(binary)],
                    cwd=args.service_root,
                    env=product_environment(
                        database_url,
                        args.otlp_endpoint,
                        args.version,
                        args.instance_id,
                        http_port,
                        grpc_port,
                        health_port,
                    ),
                    stdout=log_output,
                    stderr=subprocess.STDOUT,
                )
                wait_http(f"http://127.0.0.1:{health_port}/readyz", timeout=30)
                request(f"http://127.0.0.1:{http_port}/products", args.trace_id)
                # The metric reader exports every ten seconds. Give one full interval plus
                # the collector's one-second batch enough time before graceful shutdown.
                time.sleep(12)
                process.send_signal(signal.SIGTERM)
                try:
                    return_code = process.wait(timeout=12)
                except subprocess.TimeoutExpired as error:
                    process.kill()
                    process.wait(timeout=5)
                    raise ProbeError("Product did not stop inside its shared shutdown budget") from error
                if return_code != 0:
                    raise ProbeError(f"Product exited with status {return_code}")
                process = None
    finally:
        if process is not None and process.poll() is None:
            process.kill()
            process.wait(timeout=5)
        subprocess.run(
            ["docker", "rm", "-f", container],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--service-root", type=Path, required=True)
    parser.add_argument("--otlp-endpoint", required=True)
    parser.add_argument("--log-file", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--instance-id", required=True)
    parser.add_argument("--trace-id", required=True)
    return parser.parse_args(argv)


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = parse_args(argv)
    try:
        execute(args)
    except (OSError, KeyError, ValueError, ProbeError, subprocess.TimeoutExpired) as error:
        print(f"Product backend probe failed: {error}", file=sys.stderr)
        return 1
    print("Product backend probe passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
