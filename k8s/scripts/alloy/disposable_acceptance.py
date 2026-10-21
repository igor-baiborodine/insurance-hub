#!/usr/bin/env python3
"""Run the fixed Product telemetry profile against owned disposable containers."""

from __future__ import annotations

import argparse
import concurrent.futures
import hashlib
import http.client
import json
import math
import os
import re
import socket
import statistics
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import alloy_runtime
import backend_ingestion as backend

TIMEOUT = 90
CLEANUP_TIMEOUT = 15
POSTGRES_PASSWORD = "disposable-product-password"
READER_PASSWORD = "disposable-product-reader-password"
PATH_NAMES = ("http_list", "http_get", "grpc_list", "grpc_get")
ACCEPTED_PROFILE = {
    "healthy.requests_per_path": 1125,
    "healthy.rate_per_second": 5,
    "healthy.max_in_flight": 8,
    "healthy.request_deadline_seconds": 9,
    "healthy.warmup_seconds": 120,
    "healthy.latency_p95_ms": 250,
    "healthy.latency_p99_ms": 500,
    "overhead.runs_per_mode": 3,
    "overhead.seconds_per_run": 600,
    "overhead.latency_delta_percent": 20,
    "overhead.latency_delta_floor_ms": 10,
    "overhead.cpu_p95_delta_millicores": 50,
    "overhead.memory_max_delta_mib": 32,
    "product_resources.cpu_p95_millicores": 200,
    "product_resources.cpu_one_minute_max_millicores": 250,
    "product_resources.memory_p95_mib": 128,
    "product_resources.memory_max_mib": 192,
    "product_resources.cpu_throttling_ratio": 0.05,
    "product_resources.pool_acquire_p95_ms": 50,
    "product_resources.pool_used_p95": 6,
    "fault.seconds": 60,
    "fault.requests": 300,
    "fault.lost_logs_max": 300,
    "fault.lost_spans_max": 1200,
    "fault.duplicate_logs_max": 3,
    "fault.duplicate_spans_max": 12,
    "fault.new_trace_log_seconds": 15,
    "fault.new_metric_seconds": 30,
    "fault.queues_healthy_seconds": 120,
    "delivery.expected_logs": 4500,
    "delivery.expected_spans": 18000,
    "delivery.expected_metric_intervals": 90,
    "delivery.trace_log_p95_seconds": 10,
    "delivery.trace_log_max_seconds": 30,
    "delivery.metric_p95_seconds": 20,
    "delivery.metric_max_seconds": 45,
    "alloy_resources.baseline_seconds": 1800,
    "alloy_resources.cpu_p95_millicores": 400,
    "alloy_resources.cpu_p95_delta_millicores": 100,
    "alloy_resources.memory_max_mib": 640,
    "alloy_resources.memory_max_delta_mib": 128,
    "product_resources.pool_max": 8,
    "product_resources.shutdown_seconds": 10,
    "cardinality.application_runtime_series_per_instance_max": 800,
    "cardinality.loki_index_labels": 8,
    "cardinality.loki_streams": 8,
}
FRESHNESS_SAMPLES = 20
LOKI_TRACE_QUERY_BATCH_SIZE = 100
LOKI_TRACE_QUERY_TIMEOUT = 30
LOKI_TRACE_QUERY_RESULT_LIMIT = 1000
LOKI_TRACE_QUERY_WINDOW_NS = 2 * 60 * 60 * 1_000_000_000
LOKI_ERROR_DETAIL_MAX_CHARS = 500


class AcceptanceError(Exception):
    """A bounded disposable acceptance failure."""


class UnixHTTPConnection(http.client.HTTPConnection):
    def __init__(self, socket_path: str) -> None:
        super().__init__("localhost", timeout=15)
        self.socket_path = socket_path

    def connect(self) -> None:
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.socket_path)


@dataclass(frozen=True)
class ResourceSample:
    monotonic: float
    cpu_millicores: float
    memory_mib: float
    throttling_ratio: float


@dataclass
class ProductRun:
    container: str
    http_port: int
    grpc_port: int
    health_port: int
    instance: str
    log_process: subprocess.Popen
    log_handle: Any


def nested(profile: Mapping[str, Any], dotted: str) -> Any:
    value: Any = profile
    for part in dotted.split("."):
        if not isinstance(value, Mapping) or part not in value:
            raise AcceptanceError(f"acceptance profile is missing {dotted}")
        value = value[part]
    return value


def load_profile(path: Path) -> Mapping[str, Any]:
    profile = json.loads(path.read_text(encoding="utf-8"))
    if profile.get("schema_version") != 1:
        raise AcceptanceError("acceptance profile schema_version must be 1")
    for dotted, expected in ACCEPTED_PROFILE.items():
        actual = nested(profile, dotted)
        if actual != expected:
            raise AcceptanceError(f"accepted budget {dotted} is {actual!r}, expected {expected!r}")
    healthy = profile["healthy"]
    if healthy["requests_per_path"] * len(PATH_NAMES) != profile["delivery"]["expected_logs"]:
        raise AcceptanceError("healthy request and completion-log budgets disagree")
    if profile["fault"]["requests"] != profile["fault"]["seconds"] * healthy["rate_per_second"]:
        raise AcceptanceError("fault request count does not match its duration and rate")
    return profile


def percentile(values: Sequence[float], quantile: float) -> float:
    if not values:
        raise AcceptanceError("cannot compute a percentile without samples")
    ordered = sorted(values)
    index = max(0, min(len(ordered) - 1, math.ceil(len(ordered) * quantile) - 1))
    return ordered[index]


def verify_load(
    result: Mapping[str, Any], requests_per_path: int, profile: Mapping[str, Any]
) -> None:
    if result.get("maxInFlight", 0) > profile["healthy"]["max_in_flight"]:
        raise AcceptanceError("workload exceeded the maximum in-flight budget")
    paths = result.get("paths")
    if not isinstance(paths, Mapping) or set(paths) != set(PATH_NAMES):
        raise AcceptanceError("workload result does not contain the exact four-path matrix")
    for name in PATH_NAMES:
        value = paths[name]
        if (
            value.get("requests") != requests_per_path
            or value.get("successes") != requests_per_path
        ):
            raise AcceptanceError(f"{name} did not complete exactly {requests_per_path} reads")
        if value.get("failures") != 0:
            raise AcceptanceError(f"{name} reported request failures")
        if value.get("p95Millis", math.inf) > profile["healthy"]["latency_p95_ms"]:
            raise AcceptanceError(f"{name} exceeded the p95 request budget")
        if value.get("p99Millis", math.inf) > profile["healthy"]["latency_p99_ms"]:
            raise AcceptanceError(f"{name} exceeded the p99 request budget")


def verify_overhead(
    disabled: Sequence[Mapping[str, Any]],
    enabled: Sequence[Mapping[str, Any]],
    profile: Mapping[str, Any],
) -> Mapping[str, Any]:
    expected = profile["overhead"]["runs_per_mode"]
    if len(disabled) != expected or len(enabled) != expected:
        raise AcceptanceError("paired overhead evidence is incomplete")
    evidence: dict[str, Any] = {}
    for name in PATH_NAMES:
        disabled_p95 = statistics.median(run["paths"][name]["p95Millis"] for run in disabled)
        enabled_p95 = statistics.median(run["paths"][name]["p95Millis"] for run in enabled)
        ceiling = max(
            disabled_p95 * profile["overhead"]["latency_delta_percent"] / 100,
            profile["overhead"]["latency_delta_floor_ms"],
        )
        if enabled_p95 - disabled_p95 > ceiling:
            raise AcceptanceError(
                f"{name} telemetry p95 delta {enabled_p95 - disabled_p95:.3f}ms exceeds {ceiling:.3f}ms"
            )
        evidence[name] = {
            "disabled_p95_ms": disabled_p95,
            "enabled_p95_ms": enabled_p95,
            "delta_ms": enabled_p95 - disabled_p95,
            "allowed_delta_ms": ceiling,
        }
    return evidence


def resource_summary(samples: Sequence[ResourceSample]) -> Mapping[str, Any]:
    if len(samples) < 2:
        raise AcceptanceError("resource evidence has fewer than two samples")
    cpu = [sample.cpu_millicores for sample in samples]
    memory = [sample.memory_mib for sample in samples]
    values = cpu + memory + [sample.throttling_ratio for sample in samples]
    if not all(math.isfinite(value) and value >= 0 for value in values):
        raise AcceptanceError("resource evidence contains an unsafe numeric value")
    rolling = []
    start = 0
    for end, sample in enumerate(samples):
        while samples[start].monotonic < sample.monotonic - 60:
            start += 1
        window = samples[start : end + 1]
        if window[-1].monotonic - window[0].monotonic >= 55:
            rolling.append(statistics.mean(value.cpu_millicores for value in window))
    return {
        "samples": len(samples),
        "duration_seconds": samples[-1].monotonic - samples[0].monotonic,
        "cpu_p95_millicores": percentile(cpu, 0.95),
        "cpu_one_minute_max_millicores": max(rolling) if rolling else None,
        "memory_p95_mib": percentile(memory, 0.95),
        "memory_max_mib": max(memory),
        "cpu_throttling_ratio_max": max(sample.throttling_ratio for sample in samples),
    }


def verify_resources(
    samples: Sequence[ResourceSample],
    limits: Mapping[str, Any],
    label: str,
) -> None:
    summary = resource_summary(samples)
    if summary["cpu_p95_millicores"] > limits["cpu_p95_millicores"]:
        raise AcceptanceError(f"{label} CPU p95 exceeds its budget")
    if summary["memory_max_mib"] > limits["memory_max_mib"]:
        raise AcceptanceError(f"{label} memory maximum exceeds its budget")
    memory_p95 = limits.get("memory_p95_mib")
    if memory_p95 is not None and summary["memory_p95_mib"] > memory_p95:
        raise AcceptanceError(f"{label} memory p95 exceeds its budget")
    if summary["cpu_throttling_ratio_max"] >= limits.get("cpu_throttling_ratio", 1):
        raise AcceptanceError(f"{label} CPU throttling reached its budget")
    one_minute = limits.get("cpu_one_minute_max_millicores")
    observed_one_minute = summary["cpu_one_minute_max_millicores"]
    if (
        one_minute is not None
        and observed_one_minute is not None
        and observed_one_minute > one_minute
    ):
        raise AcceptanceError(f"{label} one-minute CPU maximum exceeds its budget")


def verify_alloy_resource_delta(
    baseline: Sequence[ResourceSample],
    loaded: Sequence[ResourceSample],
    limits: Mapping[str, Any],
) -> Mapping[str, Any]:
    baseline_summary = resource_summary(baseline)
    loaded_summary = resource_summary(loaded)
    baseline_cpu = baseline_summary["cpu_p95_millicores"]
    loaded_cpu = loaded_summary["cpu_p95_millicores"]
    baseline_memory = baseline_summary["memory_max_mib"]
    loaded_memory = loaded_summary["memory_max_mib"]
    cpu_delta = loaded_cpu - baseline_cpu
    memory_delta = loaded_memory - baseline_memory
    cpu_limit = limits["cpu_p95_delta_millicores"]
    memory_limit = limits["memory_max_delta_mib"]
    if cpu_delta > cpu_limit:
        raise AcceptanceError(
            f"Alloy CPU p95 delta {cpu_delta:.3f}m exceeds {cpu_limit:.3f}m "
            f"(baseline {baseline_cpu:.3f}m, loaded {loaded_cpu:.3f}m)"
        )
    if memory_delta > memory_limit:
        raise AcceptanceError(
            f"Alloy memory maximum delta {memory_delta:.3f}MiB exceeds {memory_limit:.3f}MiB "
            f"(baseline {baseline_memory:.3f}MiB, loaded {loaded_memory:.3f}MiB)"
        )
    return {
        "alloy_baseline": baseline_summary,
        "alloy_loaded": loaded_summary,
        "alloy_cpu_p95_delta_millicores": cpu_delta,
        "alloy_memory_max_delta_mib": memory_delta,
    }


def verify_resource_overhead(
    disabled: Sequence[Sequence[ResourceSample]],
    enabled: Sequence[Sequence[ResourceSample]],
    profile: Mapping[str, Any],
) -> Mapping[str, Any]:
    disabled_cpu = statistics.median(
        percentile([sample.cpu_millicores for sample in run], 0.95) for run in disabled
    )
    enabled_cpu = statistics.median(
        percentile([sample.cpu_millicores for sample in run], 0.95) for run in enabled
    )
    disabled_memory = statistics.median(
        max(sample.memory_mib for sample in run) for run in disabled
    )
    enabled_memory = statistics.median(max(sample.memory_mib for sample in run) for run in enabled)
    if enabled_cpu - disabled_cpu > profile["overhead"]["cpu_p95_delta_millicores"]:
        raise AcceptanceError("telemetry CPU p95 delta exceeds its budget")
    if enabled_memory - disabled_memory > profile["overhead"]["memory_max_delta_mib"]:
        raise AcceptanceError("telemetry working-set maximum delta exceeds its budget")
    return {
        "disabled_cpu_p95_millicores": disabled_cpu,
        "enabled_cpu_p95_millicores": enabled_cpu,
        "cpu_p95_delta_millicores": enabled_cpu - disabled_cpu,
        "disabled_memory_max_mib": disabled_memory,
        "enabled_memory_max_mib": enabled_memory,
        "memory_max_delta_mib": enabled_memory - disabled_memory,
    }


def command(arguments: Sequence[str], *, timeout: int = TIMEOUT, input_text: str = "") -> str:
    try:
        result = subprocess.run(
            list(arguments),
            input=input_text or None,
            text=True,
            capture_output=True,
            timeout=alloy_runtime.teardown_timeout(timeout),
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise AcceptanceError(f"command failed: {arguments[0]}: {error}") from error
    if result.returncode != 0:
        diagnostic = result.stderr.strip() or result.stdout.strip()
        raise AcceptanceError(
            f"command failed ({result.returncode}): {arguments[0]}: {diagnostic[-2000:]}"
        )
    return result.stdout.strip()


def wait_postgres(harness: Any, container: str) -> None:
    deadline = time.monotonic() + 60
    stable = 0
    while time.monotonic() < deadline:
        result = subprocess.run(
            ["docker", "exec", container, "pg_isready", "-U", "postgres", "-d", "product"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        stable = stable + 1 if result.returncode == 0 else 0
        if stable >= 3:
            return
        time.sleep(0.5)
    raise AcceptanceError("disposable PostgreSQL did not become ready")


def seed_postgres(harness: Any, container: str, fixture_path: Path) -> None:
    setup = f"""\
\\set reader_password '{READER_PASSWORD}'
CREATE TABLE public.product (code varchar(255) PRIMARY KEY, definition jsonb NOT NULL);
CREATE ROLE go_product_reader LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION PASSWORD :'reader_password';
REVOKE ALL ON DATABASE product FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE product TO go_product_reader;
GRANT USAGE ON SCHEMA public TO go_product_reader;
GRANT SELECT ON TABLE public.product TO go_product_reader;
"""
    harness.command(
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
            "product",
        ],
        input_text=setup,
    )
    fixture = json.loads(fixture_path.read_text(encoding="utf-8"))
    for row in fixture["rows"]:
        harness.command(
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
            input_text=(
                "INSERT INTO public.product (code, definition) "
                "VALUES (:'code', :'definition'::jsonb);"
            ),
        )


def container_stats(container: str) -> ResourceSample:
    payload = json.loads(
        command(["docker", "stats", "--no-stream", "--format", "{{json .}}", container], timeout=15)
    )
    cpu = float(payload["CPUPerc"].rstrip("%")) * 10
    memory_text = payload["MemUsage"].split("/", 1)[0].strip()
    memory = parse_memory_mib(memory_text)
    return ResourceSample(time.monotonic(), cpu, memory, docker_throttling_ratio(container))


def docker_throttling_ratio(container: str) -> float:
    connection = UnixHTTPConnection("/var/run/docker.sock")
    try:
        connection.request(
            "GET",
            "/containers/" + urllib.parse.quote(container, safe="") + "/stats?stream=false",
        )
        response = connection.getresponse()
        if response.status != 200:
            raise AcceptanceError(f"Docker stats API returned HTTP {response.status}")
        payload = json.loads(response.read())
    finally:
        connection.close()
    throttling = payload.get("cpu_stats", {}).get("throttling_data", {})
    periods = float(throttling.get("periods", 0))
    throttled = float(throttling.get("throttled_periods", 0))
    return throttled / periods if periods else 0


def parse_memory_mib(value: str) -> float:
    units = (("GiB", 1024), ("MiB", 1), ("KiB", 1 / 1024), ("B", 1 / 1024 / 1024))
    for unit, multiplier in units:
        if value.endswith(unit):
            return float(value[: -len(unit)]) * multiplier
    raise AcceptanceError(f"unsupported Docker memory value: {value!r}")


class Sampler:
    def __init__(self, container: str) -> None:
        self.container = container
        self.samples: list[ResourceSample] = []
        self.stop_event = threading.Event()
        self.failure: BaseException | None = None
        self.thread = threading.Thread(target=self._run, daemon=True)

    def _run(self) -> None:
        while not self.stop_event.wait(1):
            try:
                self.samples.append(container_stats(self.container))
            except BaseException as error:
                self.failure = error
                return

    def __enter__(self) -> Sampler:
        self.thread.start()
        return self

    def __exit__(self, *_args: Any) -> None:
        self.stop_event.set()
        self.thread.join(timeout=5)
        if self.thread.is_alive():
            raise AcceptanceError(f"resource sampler did not stop for {self.container}")
        if self.failure is not None:
            raise AcceptanceError(f"resource sampler failed for {self.container}: {self.failure}")


def accepted_metric_points(prometheus_port: int) -> float:
    response = backend.prom_query(
        prometheus_port,
        'sum(otelcol_receiver_accepted_metric_points_total{transport="grpc"})',
    )
    results = backend.query_results(response)
    if not results:
        return 0
    if len(results) != 1:
        raise AcceptanceError("Prometheus did not return Alloy accepted metric points")
    return float(results[0]["value"][1])


class MetricIntervalSampler:
    def __init__(self, prometheus_port: int) -> None:
        self.prometheus_port = prometheus_port
        self.values: list[float] = []
        self.stop_event = threading.Event()
        self.failure: BaseException | None = None
        self.thread = threading.Thread(target=self._run, daemon=True)

    def _sample(self) -> None:
        self.values.append(accepted_metric_points(self.prometheus_port))

    def _run(self) -> None:
        while not self.stop_event.wait(1):
            try:
                self._sample()
            except BaseException as error:
                self.failure = error
                return

    def __enter__(self) -> MetricIntervalSampler:
        self._sample()
        self.thread.start()
        return self

    def __exit__(self, *_args: Any) -> None:
        self.stop_event.set()
        self.thread.join(timeout=5)
        if self.thread.is_alive():
            raise AcceptanceError("metric interval sampler did not stop")
        if self.failure is not None:
            raise AcceptanceError(f"metric interval sampler failed: {self.failure}")
        time.sleep(2)
        self._sample()


def wait_product(port: int) -> None:
    backend.wait_http(f"http://127.0.0.1:{port}/readyz", timeout=60)


def cleanup_log_follower(log_process: subprocess.Popen, log_handle: Any) -> list[Exception]:
    errors: list[Exception] = []
    try:
        if log_process.poll() is None:
            alloy_runtime.terminate_process_group(
                log_process, graceful_seconds=5, teardown_seconds=10
            )
        else:
            log_process.wait(timeout=5)
    except Exception as error:
        errors.append(error)
    try:
        if not log_handle.closed:
            log_handle.close()
    except Exception as error:
        errors.append(error)
    return errors


def raise_with_cleanup(
    primary: BaseException | None, cleanup_errors: Sequence[Exception], label: str
) -> None:
    if cleanup_errors:
        details = "; ".join(f"{type(error).__name__}: {error}" for error in cleanup_errors)
        cleanup_error = AcceptanceError(f"{label}: {details}")
        if primary is None:
            raise cleanup_error from cleanup_errors[0]
        if isinstance(primary, (alloy_runtime.Interrupted, alloy_runtime.DeadlineExceeded)):
            primary.add_note(f"cleanup also failed: {cleanup_error}")
            raise primary.with_traceback(primary.__traceback__)
        if isinstance(primary, Exception):
            raise AcceptanceError(f"{primary}; cleanup also failed: {cleanup_error}") from primary
        raise primary from cleanup_error
    if primary is not None:
        raise primary.with_traceback(primary.__traceback__)


def start_product(
    harness: Any,
    root: Path,
    image: str,
    role: str,
    enabled: bool,
    instance: str,
) -> ProductRun:
    log_path = root / "service.log"
    log_path.touch(exist_ok=True)
    environment_file = root / f"{role}.env"
    alloy_runtime.write_protected_env_file(
        environment_file,
        {
            "PRODUCT_DATABASE_URL": (
                "postgres://go_product_reader:"
                f"{READER_PASSWORD}@postgres:5432/product?sslmode=disable"
            ),
            "DB_MAX_CONNS": "8",
            "GOMEMLIMIT": "256MiB",
            "HTTP_ADDR": "0.0.0.0:8081",
            "GRPC_ADDR": "0.0.0.0:9090",
            "HEALTH_ADDR": "0.0.0.0:8080",
            "OTEL_ENABLED": "true" if enabled else "false",
            "OTEL_EXPORTER_OTLP_ENDPOINT": "http://alloy:4317",
            "OTEL_EXPORTER_OTLP_TIMEOUT": "2s",
            "DEPLOYMENT_ENVIRONMENT": "qa",
            "SERVICE_VERSION": backend.SERVICE_VERSION,
            "SERVICE_INSTANCE_ID": instance,
            "LOG_LEVEL": "info",
            "SHUTDOWN_TIMEOUT": "10s",
        },
    )
    docker_arguments = [
        "--cpus",
        "0.30",
        "--memory",
        "256m",
        "--memory-swap",
        "256m",
        "--env-file",
        str(environment_file),
    ]
    container = harness.start(
        role,
        image,
        [],
        ports=[8080, 8081, 9090],
        docker_arguments=docker_arguments,
    )
    log_handle = log_path.open("ab", buffering=0)
    try:
        log_process = subprocess.Popen(
            ["docker", "logs", "--follow", container],
            stdout=log_handle,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
    except BaseException as error:
        cleanup_errors = []
        try:
            log_handle.close()
        except Exception as cleanup_error:
            cleanup_errors.append(cleanup_error)
        raise_with_cleanup(error, cleanup_errors, "Product startup cleanup failed")
        raise AssertionError("unreachable") from error
    try:
        health_port = harness.host_port(container, 8080)
        wait_product(health_port)
        return ProductRun(
            container=container,
            http_port=harness.host_port(container, 8081),
            grpc_port=harness.host_port(container, 9090),
            health_port=health_port,
            instance=instance,
            log_process=log_process,
            log_handle=log_handle,
        )
    except BaseException as error:
        raise_with_cleanup(
            error,
            cleanup_log_follower(log_process, log_handle),
            "Product startup cleanup failed",
        )
        raise AssertionError("unreachable") from error


def stop_product(harness: Any, product: ProductRun, expect_shutdown_seconds: float) -> float:
    started = time.monotonic()
    primary: BaseException | None = None
    containers = getattr(harness, "containers", None)
    container_owned = containers is None or product.container in containers
    if container_owned:
        try:
            harness.command(
                [
                    "docker",
                    "stop",
                    "--time",
                    str(math.ceil(expect_shutdown_seconds)),
                    product.container,
                ],
                timeout=math.ceil(expect_shutdown_seconds) + 5,
            )
        except BaseException as error:
            primary = error
    elapsed = time.monotonic() - started
    cleanup_errors = cleanup_log_follower(product.log_process, product.log_handle)
    if container_owned:
        try:
            harness.remove(product.container)
        except Exception as error:
            cleanup_errors.append(error)
    if primary is None and elapsed > expect_shutdown_seconds:
        primary = AcceptanceError(f"Product shutdown took {elapsed:.3f}s")
    raise_with_cleanup(primary, cleanup_errors, "Product shutdown cleanup failed")
    return elapsed


def cleanup_products(
    harness: Any, products: list[ProductRun], expect_shutdown_seconds: float
) -> list[Exception]:
    errors: list[Exception] = []
    for product in products[:]:
        try:
            stop_product(harness, product, expect_shutdown_seconds)
        except Exception as error:
            errors.append(error)
        finally:
            if product in products:
                products.remove(product)
    return errors


def run_load(
    helper: Path,
    product: ProductRun,
    requests_per_path: int,
    rate: float,
    max_in_flight: int,
    deadline_seconds: int,
    trace_prefix: str,
    timeout: int,
) -> Mapping[str, Any]:
    output = command(
        [
            str(helper),
            "load",
            "-http",
            f"http://127.0.0.1:{product.http_port}",
            "-grpc",
            f"127.0.0.1:{product.grpc_port}",
            "-requests-per-path",
            str(requests_per_path),
            "-rate",
            str(rate),
            "-max-in-flight",
            str(max_in_flight),
            "-deadline",
            f"{deadline_seconds}s",
            "-trace-prefix",
            trace_prefix,
        ],
        timeout=timeout,
    )
    return json.loads(output.splitlines()[-1])


def deterministic_trace_ids(prefix: str, requests: int) -> list[str]:
    return [
        hashlib.sha256(f"{prefix}:{index}".encode()).hexdigest()[:32] for index in range(requests)
    ]


def evaluate_delivery_counts(
    expected_ids: Sequence[str],
    counts: Mapping[str, int],
    units_per_request: int,
    lost_max: int,
    duplicate_max: int,
    label: str,
) -> Mapping[str, int]:
    missing = sum(max(0, units_per_request - counts.get(trace_id, 0)) for trace_id in expected_ids)
    duplicates = sum(
        max(0, counts.get(trace_id, 0) - units_per_request) for trace_id in expected_ids
    )
    unexpected = sum(value for trace_id, value in counts.items() if trace_id not in expected_ids)
    if missing > lost_max:
        raise AcceptanceError(f"{label} lost {missing}, above {lost_max}")
    if duplicates > duplicate_max:
        raise AcceptanceError(f"{label} duplicated {duplicates}, above {duplicate_max}")
    if unexpected:
        raise AcceptanceError(f"{label} included {unexpected} unexpected units")
    return {
        "expected": len(expected_ids) * units_per_request,
        "observed": sum(counts.get(trace_id, 0) for trace_id in expected_ids),
        "lost": missing,
        "duplicates": duplicates,
    }


def tempo_span_count(
    tempo_port: int,
    trace_id: str,
    start_epoch: int,
    end_epoch: int,
    timeout: float,
) -> int | None:
    try:
        status, body = backend.request(
            f"http://127.0.0.1:{tempo_port}/api/traces/{trace_id}?"
            + urllib.parse.urlencode({"start": start_epoch, "end": end_epoch}),
            timeout=timeout,
        )
        if status != 200:
            return None
        payload = json.loads(body)
    except OSError, ValueError, json.JSONDecodeError:
        return None
    batches = payload.get("batches", [])
    return sum(
        len(scope.get("spans", [])) for batch in batches for scope in batch.get("scopeSpans", [])
    )


def verify_trace_delivery(
    tempo_port: int,
    trace_prefix: str,
    request_count: int,
    lost_max: int,
    duplicate_max: int,
    timeout: float,
) -> Mapping[str, int]:
    expected_ids = deterministic_trace_ids(trace_prefix, request_count)
    counts: dict[str, int] = {}
    remaining = set(expected_ids)
    deadline = time.monotonic() + timeout
    now = time.time()
    start_epoch = int(now - 7200)
    end_epoch = int(now + 30)
    stable_since = time.monotonic()
    while remaining and time.monotonic() < deadline:
        previous = len(counts)
        pending = list(remaining)
        try:
            request_timeout = min(10, max(0.01, deadline - time.monotonic()))
            executor = concurrent.futures.ThreadPoolExecutor(max_workers=32)
            futures = {
                executor.submit(
                    tempo_span_count,
                    tempo_port,
                    trace_id,
                    start_epoch,
                    end_epoch,
                    request_timeout,
                ): trace_id
                for trace_id in pending
            }
            completed, incomplete = concurrent.futures.wait(
                futures, timeout=max(0, deadline - time.monotonic())
            )
            for future in completed:
                trace_id = futures[future]
                count = future.result()
                if count is not None:
                    counts[trace_id] = count
            for future in incomplete:
                future.cancel()
        finally:
            executor.shutdown(wait=False, cancel_futures=True)
        remaining.difference_update(counts)
        if len(counts) != previous:
            stable_since = time.monotonic()
        if time.monotonic() - stable_since >= 5 and len(remaining) * 4 <= lost_max:
            break
        if remaining and time.monotonic() < deadline:
            time.sleep(min(1, deadline - time.monotonic()))
    return evaluate_delivery_counts(
        expected_ids,
        counts,
        4,
        lost_max,
        duplicate_max,
        "Tempo spans",
    )


def bounded_response_detail(body: bytes) -> str:
    decoded = body.decode("utf-8", errors="replace")
    printable = "".join(character if character.isprintable() else " " for character in decoded)
    detail = " ".join(printable.split())
    return detail[:LOKI_ERROR_DETAIL_MAX_CHARS]


def query_loki_trace_counts(
    loki_port: int,
    instance: str,
    trace_ids: Sequence[str],
    deadline: float,
) -> Mapping[str, int]:
    counts: dict[str, int] = {}
    query_end = time.time_ns() + 30_000_000_000
    query_start = query_end - LOKI_TRACE_QUERY_WINDOW_NS
    for offset in range(0, len(trace_ids), LOKI_TRACE_QUERY_BATCH_SIZE):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        batch = trace_ids[offset : offset + LOKI_TRACE_QUERY_BATCH_SIZE]
        trace_pattern = "^(?:" + "|".join(re.escape(trace_id) for trace_id in batch) + ")$"
        query = (
            '{service_name="product-service"} '
            '|= "Product request completed" | json | service_instance_id='
            + json.dumps(instance)
            + " | trace_id=~"
            + json.dumps(trace_pattern)
        )
        status, body = backend.request(
            f"http://127.0.0.1:{loki_port}/loki/api/v1/query_range?"
            + urllib.parse.urlencode(
                {
                    "query": query,
                    "start": query_start,
                    "end": query_end,
                    "limit": LOKI_TRACE_QUERY_RESULT_LIMIT,
                }
            ),
            timeout=min(LOKI_TRACE_QUERY_TIMEOUT, remaining),
        )
        if status != 200:
            detail = bounded_response_detail(body)
            suffix = f": {detail}" if detail else ""
            raise AcceptanceError(f"Loki trace-log query returned HTTP {status}{suffix}")
        response = json.loads(body)
        returned = 0
        for result in backend.query_results(response):
            trace_id = result.get("stream", {}).get("trace_id", "")
            values = result.get("values", [])
            if not trace_id or not isinstance(values, list):
                raise AcceptanceError("Loki trace-log query returned an invalid stream")
            counts[trace_id] = counts.get(trace_id, 0) + len(values)
            returned += len(values)
        if returned >= LOKI_TRACE_QUERY_RESULT_LIMIT:
            raise AcceptanceError("Loki trace-log query reached its per-batch result limit")
    return counts


def verify_log_delivery(
    loki_port: int,
    instance: str,
    trace_prefix: str,
    request_count: int,
    lost_max: int,
    duplicate_max: int,
    timeout: float,
) -> Mapping[str, int]:
    expected_ids = deterministic_trace_ids(trace_prefix, request_count)
    counts: Mapping[str, int] = {}
    deadline = time.monotonic() + timeout
    stable_since = time.monotonic()
    previous: Mapping[str, int] = {}
    while time.monotonic() < deadline:
        counts = query_loki_trace_counts(loki_port, instance, expected_ids, deadline)
        selected = {trace_id: counts.get(trace_id, 0) for trace_id in expected_ids}
        if selected != previous:
            stable_since = time.monotonic()
            previous = selected
        missing = sum(1 for value in selected.values() if value == 0)
        if missing == 0 or (missing <= lost_max and time.monotonic() - stable_since >= 5):
            break
        time.sleep(1)
    selected = {trace_id: counts[trace_id] for trace_id in expected_ids if trace_id in counts}
    return evaluate_delivery_counts(
        expected_ids,
        selected,
        1,
        lost_max,
        duplicate_max,
        f"Loki {trace_prefix} completion logs",
    )


def prometheus_scalar(prometheus_port: int, expression: str, description: str) -> float:
    response = backend.wait_json(
        f"http://127.0.0.1:{prometheus_port}/api/v1/query?"
        + urllib.parse.urlencode({"query": expression}),
        lambda value: len(backend.query_results(value)) == 1,
        description,
    )
    return float(backend.query_results(response)[0]["value"][1])


def verify_pool_budgets(prometheus_port: int, profile: Mapping[str, Any]) -> Mapping[str, float]:
    identity = (
        'service_name="product-service",service_instance_id="' + backend.SERVICE_INSTANCE_ID + '"'
    )
    pool = 'db_client_connection_pool_name="product-reader"'
    acquire_seconds = prometheus_scalar(
        prometheus_port,
        f"histogram_quantile(0.95, sum by (le) "
        f"(db_client_connection_wait_time_seconds_bucket{{{identity},{pool}}}))",
        "database acquisition p95",
    )
    used_p95 = prometheus_scalar(
        prometheus_port,
        f"quantile_over_time(0.95, db_client_connection_count{{{identity},{pool},"
        'db_client_connection_state="used"}[20m])',
        "database used-connection p95",
    )
    pool_max = prometheus_scalar(
        prometheus_port,
        f"db_client_connection_max{{{identity},{pool}}}",
        "database pool maximum",
    )
    timeout_response = backend.prom_query(
        prometheus_port,
        f"sum(db_client_connection_timeouts_total{{{identity},{pool}}})",
    )
    timeout_results = backend.query_results(timeout_response)
    timeouts = float(timeout_results[0]["value"][1]) if timeout_results else 0
    acquire_ms = acquire_seconds * 1000
    if acquire_ms > profile["product_resources"]["pool_acquire_p95_ms"]:
        raise AcceptanceError("database acquisition p95 exceeds its budget")
    if used_p95 > profile["product_resources"]["pool_used_p95"]:
        raise AcceptanceError("database used-connection p95 exceeds its budget")
    if pool_max != profile["product_resources"]["pool_max"] or timeouts != 0:
        raise AcceptanceError("database pool maximum or timeout total violates its budget")
    return {
        "acquire_p95_ms": acquire_ms,
        "used_connections_p95": used_p95,
        "maximum_connections": pool_max,
        "timeouts": timeouts,
    }


def verify_metric_intervals(values: Sequence[float], expected: int, exact: bool) -> int:
    intervals = sum(
        current > previous for previous, current in zip(values, values[1:], strict=False)
    )
    if exact and intervals != expected:
        raise AcceptanceError(f"observed {intervals} scheduled metric exports, expected {expected}")
    if not exact and not 0 <= intervals <= expected:
        raise AcceptanceError(f"smoke observed {intervals} metric exports, above scaled {expected}")
    return intervals


def verify_alloy_healthy_delivery(
    metrics_port: int,
    expected_spans: int,
) -> Mapping[str, Any]:
    samples = backend.parse_prometheus_exposition(backend.read_alloy_metrics(metrics_port))
    zero_names = {
        "loki_write_dropped_entries_total",
        "otelcol_exporter_enqueue_failed_spans_total",
        "otelcol_receiver_failed_metric_points_total",
        "otelcol_receiver_refused_metric_points_total",
        "otelcol_receiver_failed_spans_total",
        "otelcol_receiver_refused_spans_total",
        "otelcol_exporter_loki_entries_failed",
        "otelcol_exporter_send_failed_spans_total",
        "prometheus_remote_storage_samples_dropped_total",
        "prometheus_remote_storage_samples_failed_total",
        "prometheus_remote_storage_samples_pending",
    }
    failures = {
        name: sum(sample.value for sample in samples if sample.name == name)
        for name in sorted(zero_names)
    }
    queues = [
        sample.value
        for sample in samples
        if sample.name == "otelcol_exporter_queue_size"
        and sample.labels.get("data_type") == "traces"
    ]
    sent = {
        sample.labels.get("component_id", "unknown"): sample.value
        for sample in samples
        if sample.name == "otelcol_exporter_sent_spans_total"
    }
    if any(value != 0 for value in failures.values()) or any(value != 0 for value in queues):
        raise AcceptanceError("healthy Alloy delivery reported a failure, pending sample, or queue")
    for component in ("otelcol.exporter.otlp.tempo", "otelcol.exporter.otlp.zipkin_bridge"):
        if sent.get(component) != expected_spans:
            raise AcceptanceError(
                f"{component} sent {sent.get(component)} spans, expected {expected_spans}"
            )
    return {
        "zero_failure_counters": failures,
        "trace_queue_max": max(queues, default=0),
        "sent_spans": sent,
    }


def verify_container_state(container: str, label: str) -> Mapping[str, Any]:
    state = json.loads(command(["docker", "inspect", "--format", "{{json .State}}", container]))
    if not state.get("Running") or state.get("OOMKilled") or state.get("Restarting"):
        raise AcceptanceError(f"{label} container state is unhealthy: {state}")
    return {
        "running": True,
        "oom_killed": False,
        "restart_count": int(
            command(["docker", "inspect", "--format", "{{.RestartCount}}", container])
        ),
    }


def start_backends(harness: Any, root: Path) -> Mapping[str, Any]:
    tempo = harness.start(
        "tempo",
        backend.backend_check.DISPOSABLE_IMAGES["tempo"],
        ["-config.file=/fixture/tempo.yml"],
        mounts=[(root, "/fixture", True)],
        ports=[3200],
    )
    loki = harness.start(
        "loki",
        backend.backend_check.DISPOSABLE_IMAGES["loki"],
        ["-config.file=/fixture/loki.yml"],
        mounts=[(root, "/fixture", True)],
        ports=[3100],
    )
    zipkin = harness.start(
        "zipkin", backend.backend_check.DISPOSABLE_IMAGES["zipkin"], [], ports=[9411]
    )
    harness.start(
        "zipkin-bridge",
        backend.backend_check.DISPOSABLE_IMAGES["zipkin_bridge"],
        ["--config=/fixture/bridge.yml"],
        mounts=[(root, "/fixture", True)],
    )
    prometheus, prometheus_port = backend.start_prometheus(harness, enabled=True)
    alloy, _, alloy_metrics = backend.start_alloy(harness)
    ports = {
        "tempo": harness.host_port(tempo, 3200),
        "loki": harness.host_port(loki, 3100),
        "zipkin": harness.host_port(zipkin, 9411),
        "prometheus": prometheus_port,
        "alloy_metrics": alloy_metrics,
    }
    backend.wait_http(f"http://127.0.0.1:{ports['tempo']}/ready")
    backend.wait_http(f"http://127.0.0.1:{ports['loki']}/ready")
    backend.wait_http(f"http://127.0.0.1:{ports['zipkin']}/health")
    return dict(ports, prometheus_container=prometheus, alloy_container=alloy)


def verify_backend_counts(
    ports: Mapping[str, Any],
    instance: str,
    profile: Mapping[str, Any],
    trace_ids: Iterable[str],
    requests_per_path: int,
    time_scale: float,
    metric_interval_values: Sequence[float],
) -> Mapping[str, Any]:
    prometheus = ports["prometheus"]
    total = 0
    for operation, transport in (
        ("list", "http"),
        ("get", "http"),
        ("list", "grpc"),
        ("get", "grpc"),
    ):
        expression = (
            'insurance_hub_product_requests_total{service_name="product-service",'
            f'service_instance_id="{instance}",insurance_hub_product_operation="{operation}",'
            f'insurance_hub_product_transport="{transport}",insurance_hub_product_outcome="success"}}'
        )
        response = backend.wait_json(
            f"http://127.0.0.1:{prometheus}/api/v1/query?"
            + urllib.parse.urlencode({"query": expression}),
            lambda value: (
                len(backend.query_results(value)) == 1
                and float(backend.query_results(value)[0]["value"][1]) >= requests_per_path
            ),
            f"{transport} {operation} request counter",
            timeout=profile["delivery"]["metric_max_seconds"],
        )
        value = int(float(backend.query_results(response)[0]["value"][1]))
        if value != requests_per_path:
            raise AcceptanceError(f"{transport} {operation} counter is {value}")
        total += value
    series_url = f"http://127.0.0.1:{prometheus}/api/v1/series?" + urllib.parse.urlencode(
        {"match[]": f'{{service_name="product-service",service_instance_id="{instance}"}}'}
    )
    series = backend.wait_json(
        series_url, lambda value: len(value.get("data", [])) > 0, "Product metric series"
    )
    count = len(series["data"])
    if count > profile["cardinality"]["application_runtime_series_per_instance_max"]:
        raise AcceptanceError(f"Product emitted {count} metric series")
    sampled_ids = set(trace_ids)
    expected_ids = set(deterministic_trace_ids("healthy", requests_per_path * 4))
    if not sampled_ids or not sampled_ids.issubset(expected_ids):
        raise AcceptanceError("workload trace samples do not match the deterministic healthy set")
    spans = verify_trace_delivery(
        ports["tempo"],
        "healthy",
        requests_per_path * 4,
        0,
        0,
        120,
    )
    logs = verify_log_delivery(
        ports["loki"],
        instance,
        "healthy",
        requests_per_path * 4,
        0,
        0,
        120,
    )
    expected_spans = requests_per_path * len(PATH_NAMES) * 4
    if time_scale == 1 and expected_spans != profile["delivery"]["expected_spans"]:
        raise AcceptanceError("scaled healthy span expectation does not match the workload")
    expected_intervals = max(
        1, math.ceil(profile["delivery"]["expected_metric_intervals"] * time_scale)
    )
    intervals = verify_metric_intervals(
        metric_interval_values,
        expected_intervals,
        exact=time_scale == 1,
    )
    pool = verify_pool_budgets(prometheus, profile)
    now = time.time()
    series_response = backend.wait_json(
        f"http://127.0.0.1:{ports['loki']}/loki/api/v1/series?"
        + urllib.parse.urlencode(
            {
                "match[]": '{service_name="product-service"}',
                "start": int((now - 7200) * 1_000_000_000),
                "end": int((now + 30) * 1_000_000_000),
            }
        ),
        lambda value: len(value.get("data", [])) > 0,
        "Product Loki index streams",
    )
    streams = series_response["data"]
    if len(streams) > profile["cardinality"]["loki_streams"]:
        raise AcceptanceError("Product Loki stream count exceeds its budget")
    if any(len(stream) != profile["cardinality"]["loki_index_labels"] for stream in streams):
        raise AcceptanceError("Product Loki index label count differs from its budget")
    return {
        "request_counter_total": total,
        "metric_series": count,
        "metric_export_intervals": intervals,
        "spans": spans,
        "completion_logs": logs,
        "database_pool": pool,
        "loki_streams": len(streams),
        "loki_index_labels_per_stream": sorted({len(stream) for stream in streams}),
    }


def metric_request_total(prometheus_port: int, instance: str) -> float:
    response = backend.prom_query(
        prometheus_port,
        (
            'sum(insurance_hub_product_requests_total{service_name="product-service",'
            f'service_instance_id="{instance}"}})'
        ),
    )
    results = backend.query_results(response)
    return float(results[0]["value"][1]) if len(results) == 1 else 0


def wait_metric_request_total(
    prometheus_port: int, instance: str, expected: float, timeout: float
) -> float:
    response = backend.wait_json(
        f"http://127.0.0.1:{prometheus_port}/api/v1/query?"
        + urllib.parse.urlencode(
            {
                "query": (
                    'sum(insurance_hub_product_requests_total{service_name="product-service",'
                    f'service_instance_id="{instance}"}})'
                )
            }
        ),
        lambda value: (
            len(backend.query_results(value)) == 1
            and float(backend.query_results(value)[0]["value"][1]) >= expected
        ),
        f"request counter total {expected}",
        timeout=timeout,
    )
    return float(backend.query_results(response)[0]["value"][1])


def metric_histogram_count(prometheus_port: int, instance: str) -> float:
    return prometheus_scalar(
        prometheus_port,
        (
            "sum(insurance_hub_product_request_duration_seconds_count{"
            f'service_name="product-service",service_instance_id="{instance}"}})'
        ),
        "request histogram observation total",
    )


def wait_metric_histogram_count(
    prometheus_port: int,
    instance: str,
    expected: float,
    timeout: float,
) -> float:
    expression = (
        "sum(insurance_hub_product_request_duration_seconds_count{"
        f'service_name="product-service",service_instance_id="{instance}"}})'
    )
    response = backend.wait_json(
        f"http://127.0.0.1:{prometheus_port}/api/v1/query?"
        + urllib.parse.urlencode({"query": expression}),
        lambda value: (
            len(backend.query_results(value)) == 1
            and float(backend.query_results(value)[0]["value"][1]) >= expected
        ),
        f"request histogram total {expected}",
        timeout=timeout,
    )
    return float(backend.query_results(response)[0]["value"][1])


def wait_platform_metric_after(
    prometheus_port: int,
    instance: str,
    after_epoch: float,
    timeout: float,
) -> float:
    expression = f'go_goroutine_count{{service_instance_id="{instance}"}}'
    response = backend.wait_json(
        f"http://127.0.0.1:{prometheus_port}/api/v1/query?"
        + urllib.parse.urlencode({"query": expression}),
        lambda value: (
            len(backend.query_results(value)) == 1
            and float(backend.query_results(value)[0]["value"][0]) >= after_epoch
        ),
        "fresh platform metric after Alloy replacement",
        timeout=timeout,
    )
    return float(backend.query_results(response)[0]["value"][0]) - after_epoch


def loki_trace_logs(loki_port: int, trace_id: str, timeout: float) -> Mapping[str, Any]:
    start = int((time.time() - 300) * 1_000_000_000)
    end = int((time.time() + 30) * 1_000_000_000)
    url = f"http://127.0.0.1:{loki_port}/loki/api/v1/query_range?" + urllib.parse.urlencode(
        {
            "query": f'{{service_name="product-service"}} |= "{trace_id}"',
            "start": start,
            "end": end,
            "limit": 10,
        }
    )
    deadline = time.monotonic() + timeout
    last: Any = None
    while time.monotonic() < deadline:
        try:
            status, body = backend.request(url, timeout=5)
            if status == 200:
                last = json.loads(body)
                if (
                    sum(len(stream.get("values", [])) for stream in backend.query_results(last))
                    >= 1
                ):
                    return last
        except OSError, ValueError, json.JSONDecodeError:
            pass
        time.sleep(0.1)
    raise AcceptanceError(
        f"timed out waiting for completion log for trace {trace_id}; last={last!r}"
    )


def wait_trace(tempo_port: int, trace_id: str, timeout: float) -> Mapping[str, Any]:
    deadline = time.monotonic() + timeout
    last: Any = None
    while time.monotonic() < deadline:
        try:
            status, body = backend.request(
                f"http://127.0.0.1:{tempo_port}/api/traces/{trace_id}",
                timeout=5,
            )
            if status == 200:
                last = json.loads(body)
                if len(last.get("batches", [])) == 1:
                    return last
        except OSError, ValueError, json.JSONDecodeError:
            pass
        time.sleep(0.1)
    raise AcceptanceError(f"timed out waiting for trace {trace_id}; last={last!r}")


def verify_sentinels_absent(inspected: Sequence[Any], sentinels: Sequence[str]) -> int:
    encoded = json.dumps(inspected, sort_keys=True)
    leaked = [sentinel for sentinel in sentinels if sentinel in encoded]
    if leaked:
        raise AcceptanceError(f"sensitive varied-input sentinel reached telemetry: {leaked}")
    return len(sentinels)


def metric_series_identity(series: Sequence[Mapping[str, Any]]) -> set[tuple[tuple[str, str], ...]]:
    identity: set[tuple[tuple[str, str], ...]] = set()
    for labels in series:
        if not isinstance(labels, Mapping) or not all(
            isinstance(name, str) and isinstance(value, str) for name, value in labels.items()
        ):
            raise AcceptanceError("Product metric series response is malformed")
        identity.add(tuple(sorted(labels.items())))
    return identity


def verify_metric_series_stability(
    baseline: set[tuple[tuple[str, str], ...]],
    observed: set[tuple[tuple[str, str], ...]],
    ceiling: int,
) -> int:
    if len(observed) > ceiling:
        raise AcceptanceError("varied inputs exceeded the Product series ceiling")
    if observed != baseline:
        added = len(observed - baseline)
        removed = len(baseline - observed)
        raise AcceptanceError(
            "varied product codes or trace IDs changed Product metric cardinality "
            f"(added {added}, removed {removed})"
        )
    return len(observed)


def verify_freshness(
    helper: Path,
    product: ProductRun,
    ports: Mapping[str, Any],
    profile: Mapping[str, Any],
    time_scale: float,
) -> Mapping[str, float]:
    samples = max(1, math.ceil(FRESHNESS_SAMPLES * time_scale))
    trace_values: list[float] = []
    log_values: list[float] = []
    metric_values: list[float] = []
    for index in range(samples):
        baseline = metric_request_total(ports["prometheus"], product.instance)
        marker = run_load(
            helper,
            product,
            1,
            profile["healthy"]["rate_per_second"],
            profile["healthy"]["max_in_flight"],
            profile["healthy"]["request_deadline_seconds"],
            f"freshness-{index}",
            60,
        )
        completed = time.monotonic()
        trace_ids = list(dict.fromkeys(marker["traceIds"]))
        for trace_id in trace_ids:
            wait_trace(
                ports["tempo"],
                trace_id,
                profile["delivery"]["trace_log_max_seconds"],
            )
        trace_values.append(time.monotonic() - completed)
        loki_trace_logs(ports["loki"], trace_ids[-1], profile["delivery"]["trace_log_max_seconds"])
        log_values.append(time.monotonic() - completed)
        wait_metric_request_total(
            ports["prometheus"],
            product.instance,
            baseline + 4,
            profile["delivery"]["metric_max_seconds"],
        )
        metric_values.append(time.monotonic() - completed)
    evidence = {
        "samples": samples,
        "trace_p95_seconds": percentile(trace_values, 0.95),
        "trace_max_seconds": max(trace_values),
        "log_p95_seconds": percentile(log_values, 0.95),
        "log_max_seconds": max(log_values),
        "metric_p95_seconds": percentile(metric_values, 0.95),
        "metric_max_seconds": max(metric_values),
    }
    if (
        max(evidence["trace_p95_seconds"], evidence["log_p95_seconds"])
        > profile["delivery"]["trace_log_p95_seconds"]
    ):
        raise AcceptanceError(
            f"trace/log freshness p95 exceeds "
            f"{profile['delivery']['trace_log_p95_seconds']:.3f}s "
            f"(trace {evidence['trace_p95_seconds']:.3f}s, "
            f"log {evidence['log_p95_seconds']:.3f}s)"
        )
    if (
        max(evidence["trace_max_seconds"], evidence["log_max_seconds"])
        > profile["delivery"]["trace_log_max_seconds"]
    ):
        raise AcceptanceError("trace or log freshness maximum exceeded its budget")
    if evidence["metric_p95_seconds"] > profile["delivery"]["metric_p95_seconds"]:
        raise AcceptanceError("metric freshness p95 exceeded its budget")
    if evidence["metric_max_seconds"] > profile["delivery"]["metric_max_seconds"]:
        raise AcceptanceError("metric freshness maximum exceeded its budget")
    return evidence


def verify_recovery_marker(
    helper: Path,
    product: ProductRun,
    ports: Mapping[str, Any],
    profile: Mapping[str, Any],
    trace_prefix: str,
) -> Mapping[str, float]:
    baseline = metric_request_total(ports["prometheus"], product.instance)
    marker = run_load(
        helper,
        product,
        1,
        profile["healthy"]["rate_per_second"],
        profile["healthy"]["max_in_flight"],
        profile["healthy"]["request_deadline_seconds"],
        trace_prefix,
        60,
    )
    verify_load(marker, 1, profile)
    completed = time.monotonic()
    verify_trace_delivery(
        ports["tempo"],
        trace_prefix,
        len(PATH_NAMES),
        0,
        0,
        profile["fault"]["new_trace_log_seconds"],
    )
    trace_seconds = time.monotonic() - completed
    verify_log_delivery(
        ports["loki"],
        product.instance,
        trace_prefix,
        len(PATH_NAMES),
        0,
        0,
        profile["fault"]["new_trace_log_seconds"],
    )
    log_seconds = time.monotonic() - completed
    wait_metric_request_total(
        ports["prometheus"],
        product.instance,
        baseline + len(PATH_NAMES),
        profile["fault"]["new_metric_seconds"],
    )
    metric_seconds = time.monotonic() - completed
    if max(trace_seconds, log_seconds) > profile["fault"]["new_trace_log_seconds"]:
        raise AcceptanceError("recovered trace or log exceeded its freshness budget")
    if metric_seconds > profile["fault"]["new_metric_seconds"]:
        raise AcceptanceError("recovered metric exceeded its freshness budget")
    return {
        "trace_seconds": trace_seconds,
        "log_seconds": log_seconds,
        "metric_seconds": metric_seconds,
    }


def verify_varied_input_safety(
    product: ProductRun,
    ports: Mapping[str, Any],
    root: Path,
    profile: Mapping[str, Any],
) -> Mapping[str, Any]:
    baseline_total = metric_request_total(ports["prometheus"], product.instance)
    baseline_series = backend.wait_json(
        f"http://127.0.0.1:{ports['prometheus']}/api/v1/series?"
        + urllib.parse.urlencode(
            {
                "match[]": (
                    f'{{service_name="product-service",service_instance_id="{product.instance}"}}'
                )
            }
        ),
        lambda value: len(value.get("data", [])) > 0,
        "baseline Product metric series",
    )
    initial_count = len(metric_series_identity(baseline_series["data"]))
    baseline_identity: set[tuple[tuple[str, str], ...]] | None = None
    baseline_count = 0
    sentinels = [
        "acceptance-authorization-secret",
        "acceptance-cookie-secret",
        "acceptance-baggage-secret",
        "acceptance-customer-policy-secret",
    ]
    trace_ids = []
    for index in range(32):
        trace_id = hashlib.sha256(f"safety:{index}".encode()).hexdigest()[:32]
        trace_ids.append(trace_id)
        code = f"MISSING-{index}-{sentinels[index % len(sentinels)]}"
        request_value = urllib.request.Request(
            f"http://127.0.0.1:{product.http_port}/products/" + urllib.parse.quote(code),
            headers={
                "traceparent": f"00-{trace_id}-0123456789abcdef-01",
                "Authorization": "Bearer " + sentinels[0],
                "Cookie": "session=" + sentinels[1],
                "Baggage": "unsafe=" + sentinels[2],
            },
        )
        try:
            urllib.request.urlopen(request_value, timeout=10)
        except urllib.error.HTTPError as error:
            if error.code != 404:
                raise AcceptanceError(f"varied missing read returned HTTP {error.code}") from error
        else:
            raise AcceptanceError("varied missing read unexpectedly succeeded")
        if index == 0:
            wait_metric_request_total(
                ports["prometheus"],
                product.instance,
                baseline_total + 1,
                profile["delivery"]["metric_max_seconds"],
            )
            first_series = backend.wait_json(
                f"http://127.0.0.1:{ports['prometheus']}/api/v1/series?"
                + urllib.parse.urlencode(
                    {
                        "match[]": (
                            f'{{service_name="product-service",'
                            f'service_instance_id="{product.instance}"}}'
                        )
                    }
                ),
                lambda value: len(value.get("data", [])) >= initial_count,
                "first varied-input Product metric series",
            )
            baseline_identity = metric_series_identity(first_series["data"])
            baseline_count = len(baseline_identity)
    if baseline_identity is None:
        raise AcceptanceError("varied-input metric baseline was not captured")
    wait_metric_request_total(
        ports["prometheus"],
        product.instance,
        baseline_total + 32,
        profile["delivery"]["metric_max_seconds"],
    )
    series = backend.wait_json(
        f"http://127.0.0.1:{ports['prometheus']}/api/v1/series?"
        + urllib.parse.urlencode(
            {
                "match[]": (
                    f'{{service_name="product-service",service_instance_id="{product.instance}"}}'
                )
            }
        ),
        lambda value: len(value.get("data", [])) >= baseline_count,
        "varied-input Product metric series",
    )
    series_identity = metric_series_identity(series["data"])
    series_count = verify_metric_series_stability(
        baseline_identity,
        series_identity,
        profile["cardinality"]["application_runtime_series_per_instance_max"],
    )
    inspected: list[Any] = [series, (root / "service.log").read_text(encoding="utf-8")]
    for trace_id in (trace_ids[0], trace_ids[-1]):
        trace = backend.wait_json(
            f"http://127.0.0.1:{ports['tempo']}/api/traces/{trace_id}",
            lambda value: len(value.get("batches", [])) == 1,
            f"varied-input trace {trace_id}",
        )
        inspected.extend((trace, loki_trace_logs(ports["loki"], trace_id, 30)))
    absent = verify_sentinels_absent(inspected, sentinels)
    return {
        "inputs": 32,
        "series_before": baseline_count,
        "series_after": series_count,
        "series_delta": 0,
        "sentinels_absent": absent,
    }


def execute(args: argparse.Namespace) -> None:
    profile = load_profile(args.profile)
    if args.time_scale <= 0 or args.time_scale > 1:
        raise AcceptanceError("time-scale must be greater than zero and at most one")
    source = args.config.read_text(encoding="utf-8")
    prefix = f"ih-accept-{os.getpid()}"
    with tempfile.TemporaryDirectory(prefix="insurance-hub-acceptance-") as temporary:
        root = Path(temporary)
        root.chmod(0o755)
        backend.write_configs(root, source, "product")
        helper = root / "telemetry-acceptance"
        command(
            [
                "make",
                "-C",
                str(args.service_root),
                "build-telemetry-acceptance",
                f"TELEMETRY_ACCEPTANCE_OUTPUT={helper}",
            ],
            timeout=300,
        )
        harness = backend.DockerHarness(prefix, root)
        products: list[ProductRun] = []
        primary: BaseException | None = None
        try:
            harness.create()
            for image in set(backend.backend_check.DISPOSABLE_IMAGES.values()):
                harness.pull(image)
            command(["docker", "image", "inspect", args.product_image])
            image_ids = {
                reference: json.loads(command(["docker", "image", "inspect", reference]))[0]["Id"]
                for reference in sorted(set(backend.backend_check.DISPOSABLE_IMAGES.values()))
            }
            postgres_environment = root / "postgres.env"
            alloy_runtime.write_protected_env_file(
                postgres_environment,
                {"POSTGRES_PASSWORD": POSTGRES_PASSWORD, "POSTGRES_DB": "product"},
            )
            postgres = harness.start(
                "postgres",
                backend.backend_check.DISPOSABLE_IMAGES["postgres"],
                [],
                ports=[5432],
                docker_arguments=["--env-file", str(postgres_environment)],
            )
            wait_postgres(harness, postgres)
            seed_postgres(harness, postgres, args.catalog_fixture)
            ports = start_backends(harness, root)

            disabled_results: list[Mapping[str, Any]] = []
            enabled_results: list[Mapping[str, Any]] = []
            overhead_resources: dict[str, list[list[ResourceSample]]] = {
                "disabled": [],
                "enabled": [],
            }
            overhead_seconds = max(2, profile["overhead"]["seconds_per_run"] * args.time_scale)
            overhead_requests = max(
                1, int(overhead_seconds * profile["healthy"]["rate_per_second"] / 4)
            )
            warmup_seconds = max(1, profile["healthy"]["warmup_seconds"] * args.time_scale)
            warmup_requests = max(
                1, int(warmup_seconds * profile["healthy"]["rate_per_second"] / 4)
            )
            for run_index in range(profile["overhead"]["runs_per_mode"]):
                for mode in (
                    ("disabled", "enabled") if run_index % 2 == 0 else ("enabled", "disabled")
                ):
                    enabled = mode == "enabled"
                    product = start_product(
                        harness,
                        root,
                        args.product_image,
                        f"product-{mode}-{run_index}",
                        enabled,
                        backend.SERVICE_INSTANCE_ID,
                    )
                    products.append(product)
                    run_load(
                        helper,
                        product,
                        warmup_requests,
                        profile["healthy"]["rate_per_second"],
                        profile["healthy"]["max_in_flight"],
                        profile["healthy"]["request_deadline_seconds"],
                        f"warmup-{mode}-{run_index}",
                        math.ceil(warmup_seconds) + 120,
                    )
                    with Sampler(product.container) as product_sampler:
                        result = run_load(
                            helper,
                            product,
                            overhead_requests,
                            profile["healthy"]["rate_per_second"],
                            profile["healthy"]["max_in_flight"],
                            profile["healthy"]["request_deadline_seconds"],
                            f"overhead-{mode}-{run_index}",
                            math.ceil(overhead_seconds) + 120,
                        )
                    verify_load(result, overhead_requests, profile)
                    (enabled_results if enabled else disabled_results).append(result)
                    overhead_resources[mode].append(product_sampler.samples)
                    verify_resources(
                        product_sampler.samples,
                        profile["product_resources"],
                        f"Product {mode} overhead run {run_index + 1}",
                    )
                    stop_product(harness, product, profile["product_resources"]["shutdown_seconds"])
                    products.remove(product)
            overhead_latency_evidence = verify_overhead(disabled_results, enabled_results, profile)
            overhead_resource_evidence = verify_resource_overhead(
                overhead_resources["disabled"], overhead_resources["enabled"], profile
            )

            # Reset cumulative backends and the file-source position so the fixed healthy
            # counts belong only to the following candidate run.
            for name in (
                ports["alloy_container"],
                ports["prometheus_container"],
                f"{prefix}-zipkin-bridge",
                f"{prefix}-zipkin",
                f"{prefix}-loki",
                f"{prefix}-tempo",
            ):
                harness.remove(name)
            (root / "service.log").write_text("", encoding="utf-8")
            ports = start_backends(harness, root)

            # Measure the collector that will handle the healthy workload immediately before
            # loading it, so the delta excludes startup and cross-container variation.
            alloy_baseline: list[ResourceSample] = []
            baseline_seconds = max(
                2, profile["alloy_resources"]["baseline_seconds"] * args.time_scale
            )
            baseline_deadline = time.monotonic() + baseline_seconds
            while time.monotonic() < baseline_deadline:
                alloy_baseline.append(container_stats(ports["alloy_container"]))
                time.sleep(1)

            instance = backend.SERVICE_INSTANCE_ID
            product = start_product(
                harness, root, args.product_image, "product-healthy", True, instance
            )
            products.append(product)
            healthy_requests = max(
                1, math.ceil(profile["healthy"]["requests_per_path"] * args.time_scale)
            )
            with (
                Sampler(product.container) as product_sampler,
                Sampler(ports["alloy_container"]) as alloy_sampler,
                MetricIntervalSampler(ports["prometheus"]) as metric_interval_sampler,
            ):
                healthy = run_load(
                    helper,
                    product,
                    healthy_requests,
                    profile["healthy"]["rate_per_second"],
                    profile["healthy"]["max_in_flight"],
                    profile["healthy"]["request_deadline_seconds"],
                    "healthy",
                    math.ceil(healthy_requests * 4 / profile["healthy"]["rate_per_second"]) + 180,
                )
            verify_load(healthy, healthy_requests, profile)
            verify_resources(product_sampler.samples, profile["product_resources"], "Product")
            verify_resources(alloy_sampler.samples, profile["alloy_resources"], "Alloy")
            alloy_resource_evidence = verify_alloy_resource_delta(
                alloy_baseline, alloy_sampler.samples, profile["alloy_resources"]
            )
            backend_evidence = verify_backend_counts(
                ports,
                instance,
                profile,
                healthy["traceIds"],
                healthy_requests,
                args.time_scale,
                metric_interval_sampler.values,
            )
            alloy_delivery_evidence = verify_alloy_healthy_delivery(
                ports["alloy_metrics"],
                healthy_requests * len(PATH_NAMES) * 4,
            )
            healthy_container_evidence = {
                "product": verify_container_state(product.container, "Product"),
                "alloy": verify_container_state(ports["alloy_container"], "Alloy"),
            }
            freshness_evidence = verify_freshness(
                helper,
                product,
                ports,
                profile,
                args.time_scale,
            )
            safety_evidence = verify_varied_input_safety(product, ports, root, profile)

            # Keep Product serving while every backend is unavailable for exactly sixty seconds.
            pre_fault_counter = metric_request_total(ports["prometheus"], instance)
            pre_fault_histogram = metric_histogram_count(ports["prometheus"], instance)
            backend_names = [
                ports["prometheus_container"],
                f"{prefix}-loki",
                f"{prefix}-tempo",
                f"{prefix}-zipkin",
                f"{prefix}-zipkin-bridge",
            ]
            for name in backend_names:
                harness.command(["docker", "pause", name], timeout=30)
            fault_seconds = max(1, profile["fault"]["seconds"] * args.time_scale)
            fault_requests_per_path = max(
                1, math.ceil(profile["fault"]["requests"] * args.time_scale / 4)
            )
            fault_started = time.monotonic()
            fault = run_load(
                helper,
                product,
                fault_requests_per_path,
                profile["healthy"]["rate_per_second"],
                profile["healthy"]["max_in_flight"],
                profile["healthy"]["request_deadline_seconds"],
                "backend-outage",
                math.ceil(fault_seconds) + 120,
            )
            verify_load(fault, fault_requests_per_path, profile)
            remaining = fault_seconds - (time.monotonic() - fault_started)
            if remaining > 0:
                time.sleep(remaining)
            if time.monotonic() - fault_started < fault_seconds - 0.1:
                raise AcceptanceError("backend outage workload did not cover sixty seconds")
            for name in reversed(backend_names):
                harness.command(["docker", "unpause", name], timeout=30)
            backend.wait_http(f"http://127.0.0.1:{ports['prometheus']}/-/ready", timeout=60)
            backend.wait_http(f"http://127.0.0.1:{ports['loki']}/ready", timeout=60)
            backend.wait_http(f"http://127.0.0.1:{ports['tempo']}/ready", timeout=60)

            pending = backend.wait_remote_write_value(
                ports["alloy_metrics"],
                backend.REMOTE_WRITE_PENDING_METRIC,
                lambda value: value == 0,
                "remote-write queue recovery",
                timeout=profile["fault"]["queues_healthy_seconds"],
            )
            fault_requests = fault_requests_per_path * len(PATH_NAMES)
            post_fault_counter = wait_metric_request_total(
                ports["prometheus"],
                instance,
                pre_fault_counter + fault_requests,
                profile["fault"]["queues_healthy_seconds"],
            )
            post_fault_histogram = wait_metric_histogram_count(
                ports["prometheus"],
                instance,
                pre_fault_histogram + fault_requests,
                profile["fault"]["queues_healthy_seconds"],
            )
            if (
                post_fault_counter - pre_fault_counter != fault_requests
                or post_fault_histogram - pre_fault_histogram != fault_requests
            ):
                raise AcceptanceError("backend outage did not preserve cumulative metric deltas")
            fault_loss_scale = args.time_scale if args.time_scale < 1 else 1
            backend_fault_spans = verify_trace_delivery(
                ports["tempo"],
                "backend-outage",
                fault_requests,
                math.ceil(profile["fault"]["lost_spans_max"] * fault_loss_scale),
                math.ceil(profile["fault"]["duplicate_spans_max"] * fault_loss_scale),
                profile["fault"]["queues_healthy_seconds"],
            )
            backend_fault_logs = verify_log_delivery(
                ports["loki"],
                instance,
                "backend-outage",
                fault_requests,
                math.ceil(profile["fault"]["lost_logs_max"] * fault_loss_scale),
                math.ceil(profile["fault"]["duplicate_logs_max"] * fault_loss_scale),
                profile["fault"]["queues_healthy_seconds"],
            )
            backend_recovery = verify_recovery_marker(
                helper,
                product,
                ports,
                profile,
                "backend-recovery",
            )

            # Replace ephemeral Alloy during the same bounded workload rate and report its
            # non-durable loss separately from the backend-outage WAL result.
            pre_replacement_counter = metric_request_total(ports["prometheus"], instance)
            pre_replacement_histogram = metric_histogram_count(ports["prometheus"], instance)
            old_alloy = ports["alloy_container"]
            harness.remove(old_alloy)
            replacement_started_epoch = time.time()
            with concurrent.futures.ThreadPoolExecutor(max_workers=1) as executor:
                replacement_future = executor.submit(
                    run_load,
                    helper,
                    product,
                    fault_requests_per_path,
                    profile["healthy"]["rate_per_second"],
                    profile["healthy"]["max_in_flight"],
                    profile["healthy"]["request_deadline_seconds"],
                    "collector-replacement",
                    math.ceil(fault_seconds) + 120,
                )
                time.sleep(max(1, min(10, fault_seconds / 2)))
                replacement, _, replacement_metrics = backend.start_alloy(harness)
                replacement_fault = replacement_future.result()
            ports["alloy_container"] = replacement
            ports["alloy_metrics"] = replacement_metrics
            verify_load(replacement_fault, fault_requests_per_path, profile)
            replacement_pending = backend.wait_remote_write_value(
                replacement_metrics,
                backend.REMOTE_WRITE_PENDING_METRIC,
                lambda value: value == 0,
                "replacement remote-write queue recovery",
                timeout=profile["fault"]["queues_healthy_seconds"],
            )
            post_replacement_counter = wait_metric_request_total(
                ports["prometheus"],
                instance,
                pre_replacement_counter + fault_requests,
                profile["fault"]["new_metric_seconds"],
            )
            post_replacement_histogram = wait_metric_histogram_count(
                ports["prometheus"],
                instance,
                pre_replacement_histogram + fault_requests,
                profile["fault"]["new_metric_seconds"],
            )
            if (
                post_replacement_counter - pre_replacement_counter != fault_requests
                or post_replacement_histogram - pre_replacement_histogram != fault_requests
            ):
                raise AcceptanceError("Alloy replacement did not converge cumulative metric deltas")
            replacement_spans = verify_trace_delivery(
                ports["tempo"],
                "collector-replacement",
                fault_requests,
                math.ceil(profile["fault"]["lost_spans_max"] * fault_loss_scale),
                math.ceil(profile["fault"]["duplicate_spans_max"] * fault_loss_scale),
                profile["fault"]["queues_healthy_seconds"],
            )
            replacement_logs = verify_log_delivery(
                ports["loki"],
                instance,
                "collector-replacement",
                fault_requests,
                math.ceil(profile["fault"]["lost_logs_max"] * fault_loss_scale),
                math.ceil(profile["fault"]["duplicate_logs_max"] * fault_loss_scale),
                profile["fault"]["queues_healthy_seconds"],
            )
            replacement_recovery = verify_recovery_marker(
                helper,
                product,
                ports,
                profile,
                "collector-recovery",
            )
            platform_metric_seconds = wait_platform_metric_after(
                ports["prometheus"],
                instance,
                replacement_started_epoch,
                profile["fault"]["new_metric_seconds"],
            )
            replacement_state = verify_container_state(replacement, "replacement Alloy")
            shutdown_seconds = stop_product(
                harness, product, profile["product_resources"]["shutdown_seconds"]
            )
            products.remove(product)

            evidence = {
                "schema_version": 1,
                "time_scale": args.time_scale,
                "source_revision": command(["git", "rev-parse", "HEAD"]),
                "product_image": json.loads(
                    command(["docker", "image", "inspect", args.product_image])
                )[0]["Id"],
                "backend_images": image_ids,
                "config_sha256": hashlib.sha256(args.config.read_bytes()).hexdigest(),
                "catalog_fixture_sha256": hashlib.sha256(
                    args.catalog_fixture.read_bytes()
                ).hexdigest(),
                "profile_sha256": command(["sha256sum", str(args.profile)]).split()[0],
                "healthy": healthy,
                "backend": backend_evidence,
                "alloy_delivery": alloy_delivery_evidence,
                "container_state": healthy_container_evidence,
                "freshness": freshness_evidence,
                "safety": safety_evidence,
                "overhead": {
                    "disabled": disabled_results,
                    "enabled": enabled_results,
                    "latency": overhead_latency_evidence,
                    "resources": overhead_resource_evidence,
                },
                "fault": {
                    "backend_outage": {
                        "requests": fault_requests,
                        "pending_after_recovery": pending,
                        "counter_delta": post_fault_counter - pre_fault_counter,
                        "histogram_count_delta": post_fault_histogram - pre_fault_histogram,
                        "spans": backend_fault_spans,
                        "logs": backend_fault_logs,
                        "recovery": backend_recovery,
                    },
                    "collector_replacement": {
                        "requests": fault_requests,
                        "pending_after_recovery": replacement_pending,
                        "counter_delta": post_replacement_counter - pre_replacement_counter,
                        "histogram_count_delta": (
                            post_replacement_histogram - pre_replacement_histogram
                        ),
                        "spans": replacement_spans,
                        "logs": replacement_logs,
                        "recovery": replacement_recovery,
                        "platform_metric_seconds": platform_metric_seconds,
                        "container_state": replacement_state,
                    },
                },
                "shutdown_seconds": shutdown_seconds,
                "resources": {
                    "product": resource_summary(product_sampler.samples),
                    **alloy_resource_evidence,
                },
            }
            print(json.dumps(evidence, indent=2, sort_keys=True))
        except BaseException as error:
            primary = error
        with alloy_runtime.teardown_activity(90):
            cleanup_errors = cleanup_products(
                harness, products, profile["product_resources"]["shutdown_seconds"]
            )
            try:
                harness.cleanup()
            except Exception as error:
                cleanup_errors.append(error)
            raise_with_cleanup(primary, cleanup_errors, "disposable acceptance cleanup failed")


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--profile", type=Path, required=True)
    parser.add_argument("--service-root", type=Path, required=True)
    parser.add_argument("--catalog-fixture", type=Path, required=True)
    parser.add_argument("--product-image", required=True)
    parser.add_argument("--time-scale", type=float, default=1.0)
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        with (
            alloy_runtime.controlled_signals(),
            alloy_runtime.bounded_activity("disposable Product acceptance", 14400),
        ):
            execute(args)
    except alloy_runtime.Interrupted as error:
        print(f"disposable acceptance {error}", file=sys.stderr)
        for note in getattr(error, "__notes__", ()):
            print(note, file=sys.stderr)
        return error.status
    except (
        AcceptanceError,
        alloy_runtime.DeadlineExceeded,
        alloy_runtime.TeardownExceeded,
        OSError,
        ValueError,
        json.JSONDecodeError,
        backend.HarnessError,
        subprocess.TimeoutExpired,
    ) as error:
        print(f"disposable acceptance failed: {error}", file=sys.stderr)
        return 1
    print("disposable Product telemetry acceptance passed", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
