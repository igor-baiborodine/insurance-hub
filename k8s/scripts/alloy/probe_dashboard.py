#!/usr/bin/env python3
"""Emit real Dashboard sales-trends signals into a caller-owned collector."""

from __future__ import annotations

import argparse
import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from collections.abc import Mapping, Sequence
from pathlib import Path

import alloy_runtime

ELASTICSEARCH_IMAGE = (
    "docker.elastic.co/elasticsearch/elasticsearch:8.19.6@"
    "sha256:bfb2108076735fbfc5dcd5d49f12f5aa93fab9759034c55f7da3b4345606c903"
)


class ProbeError(Exception):
    """A bounded Dashboard probe failure."""


def run(arguments: Sequence[str], *, cwd: Path | None = None, timeout: int = 180) -> str:
    result = subprocess.run(
        list(arguments), cwd=cwd, text=True, capture_output=True, timeout=timeout, check=False
    )
    if result.returncode != 0:
        diagnostic = result.stderr.strip() or result.stdout.strip()
        raise ProbeError(f"command failed ({result.returncode}): {arguments[0]}: {diagnostic}")
    return result.stdout.strip()


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def request(url: str, method: str = "GET", body: Mapping[str, object] | None = None) -> object:
    data = None if body is None else json.dumps(body, separators=(",", ":")).encode()
    request_value = urllib.request.Request(
        url,
        method=method,
        data=data,
        headers={"Content-Type": "application/json"} if data is not None else {},
    )
    with urllib.request.urlopen(request_value, timeout=5) as response:
        if response.status not in (200, 201):
            raise ProbeError(f"request returned HTTP {response.status}")
        return json.load(response)


def wait_http(url: str, timeout: float = 60) -> None:
    deadline = time.monotonic() + timeout
    last_error = "not attempted"
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                if response.status == 200:
                    return
                last_error = f"HTTP {response.status}"
        except (OSError, urllib.error.URLError) as error:
            last_error = str(error)
        time.sleep(0.25)
    raise ProbeError(f"timed out waiting for {url}: {last_error}")


def dashboard_environment(
    args: argparse.Namespace,
    elasticsearch_url: str,
    http_port: int,
    grpc_port: int,
    health_port: int,
) -> Mapping[str, str]:
    environment = dict(os.environ)
    environment.pop("OTEL_EXPORTER_OTLP_TIMEOUT", None)
    environment.update(
        {
            "SERVICE_NAME": "dashboard-service",
            "SERVICE_NAMESPACE": "insurance-hub",
            "IMPLEMENTATION": "go",
            "DEPLOYMENT_ENVIRONMENT": "qa",
            "SERVICE_VERSION": args.version,
            "SERVICE_INSTANCE_ID": args.instance_id,
            "HTTP_ADDR": f"127.0.0.1:{http_port}",
            "GRPC_ADDR": f"127.0.0.1:{grpc_port}",
            "HEALTH_ADDR": f"127.0.0.1:{health_port}",
            "ELASTICSEARCH_URL": elasticsearch_url,
            "ELASTICSEARCH_INDEX": "policy_stats",
            "ELASTICSEARCH_TIMEOUT": "5s",
            "SALES_TRENDS_TIME_ZONE": "GMT",
            "OTEL_ENABLED": "true",
            "OTEL_EXPORTER_OTLP_ENDPOINT": args.otlp_endpoint,
            "REQUEST_TIMEOUT": "2s",
            "SHUTDOWN_TIMEOUT": "8s",
            "GOMEMLIMIT": "256MiB",
            "LOG_LEVEL": "info",
        }
    )
    return environment


def create_fixture(url: str) -> None:
    request(
        f"{url}/policy_stats",
        "PUT",
        {
            "settings": {"number_of_shards": 1, "number_of_replicas": 0},
            "mappings": {
                "properties": {
                    "productCode": {
                        "type": "text",
                        "fields": {"keyword": {"type": "keyword"}},
                    },
                    "agentLogin": {
                        "type": "text",
                        "fields": {"keyword": {"type": "keyword"}},
                    },
                    "from": {"type": "date"},
                    "totalPremium": {"type": "double"},
                }
            },
        },
    )
    request(
        f"{url}/policy_stats/_doc/observability-fixture?refresh=true",
        "PUT",
        {
            "agentLogin": "probe-agent",
            "productCode": "PROBE",
            "from": "2020-02-01",
            "totalPremium": 1000.0,
        },
    )


def invoke_sales_trends(url: str, trace_id: str) -> None:
    data = json.dumps(
        {
            "saleDateFrom": "2020-01-01",
            "saleDateTo": "2020-03-01",
            "aggregationUnitCode": "MONTH",
        },
        separators=(",", ":"),
    ).encode()
    request_value = urllib.request.Request(
        f"{url}/dashboard/trends",
        method="POST",
        data=data,
        headers={
            "Content-Type": "application/json",
            "traceparent": f"00-{trace_id}-0123456789abcdef-01",
        },
    )
    with urllib.request.urlopen(request_value, timeout=5) as response:
        body = json.load(response)
    periods = body.get("periodSales") if isinstance(body, dict) else None
    if not isinstance(periods, list) or len(periods) != 1:
        raise ProbeError("Dashboard sales trends did not return one period")
    period = periods[0]
    if period.get("policiesCount") != 1 or period.get("period") != "2020-02-01T00:00:00.000Z":
        raise ProbeError("Dashboard sales-trends response differs from the fixture")


def execute_in_root(args: argparse.Namespace, root: Path) -> None:
    container = f"ih-dashboard-probe-{os.getpid()}"
    process: subprocess.Popen[bytes] | None = None
    try:
        run(
            [
                "docker",
                "run",
                "-d",
                "--name",
                container,
                "-e",
                "discovery.type=single-node",
                "-e",
                "xpack.security.enabled=false",
                "-e",
                "ES_JAVA_OPTS=-Xms512m -Xmx512m",
                "-p",
                "127.0.0.1::9200",
                ELASTICSEARCH_IMAGE,
            ],
            timeout=300,
        )
        port = int(run(["docker", "port", container, "9200/tcp"]).rsplit(":", 1)[1])
        elasticsearch_url = f"http://127.0.0.1:{port}"
        wait_http(f"{elasticsearch_url}/_cluster/health?wait_for_status=yellow&timeout=1s", 120)
        create_fixture(elasticsearch_url)

        http_port, grpc_port, health_port = free_port(), free_port(), free_port()
        binary = root / "dashboard-service"
        run(
            ["go", "build", "-mod=readonly", "-o", str(binary), "./cmd/server"],
            cwd=args.service_root,
        )
        args.log_file.parent.mkdir(parents=True, exist_ok=True)
        with args.log_file.open("ab", buffering=0) as output:
            process = subprocess.Popen(
                [str(binary)],
                cwd=args.service_root,
                env=dashboard_environment(
                    args, elasticsearch_url, http_port, grpc_port, health_port
                ),
                stdout=output,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
            if args.ownership_file is not None:
                args.ownership_file.write_text(f"{process.pid}\n", encoding="ascii")
            wait_http(f"http://127.0.0.1:{health_port}/readyz", 60)
            invoke_sales_trends(f"http://127.0.0.1:{http_port}", args.trace_id)
            time.sleep(12)
            alloy_runtime.terminate_process_group(process, graceful_seconds=12, teardown_seconds=17)
            if process.returncode != 0:
                raise ProbeError(f"Dashboard exited with status {process.returncode}")
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
            try:
                removal = subprocess.run(
                    ["docker", "rm", "-f", container],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    timeout=alloy_runtime.teardown_timeout(15),
                    check=False,
                )
                if removal.returncode != 0:
                    raise ProbeError(f"could not remove disposable Elasticsearch: {container}")
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
    with tempfile.TemporaryDirectory(prefix="dashboard-observability-") as temporary:
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
            alloy_runtime.bounded_activity("Dashboard backend probe", 600),
        ):
            execute(args)
    except alloy_runtime.Interrupted as error:
        print(f"Dashboard backend probe {error}", file=sys.stderr)
        return error.status
    except (
        alloy_runtime.DeadlineExceeded,
        alloy_runtime.TeardownExceeded,
        OSError,
        ProbeError,
        subprocess.TimeoutExpired,
        ValueError,
    ) as error:
        print(f"Dashboard backend probe failed: {error}", file=sys.stderr)
        return 1
    print("Dashboard backend probe passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
