#!/usr/bin/env python3
"""Prove Alloy delivery to pinned disposable telemetry backends.

The harness owns a uniquely named Docker network and every container it creates.
It derives the disposable Alloy configuration from the checked-in source, replacing
only Kubernetes association/discovery and in-cluster addresses. Signal policy,
queues, retries, batches, exporters, and log processing stay production-owned.
"""

from __future__ import annotations

import argparse
import json
import math
import os
import re
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Dict, Iterable, List, Mapping, Optional, Sequence, Tuple

from importlib import util as importlib_util


CHECK_PATH = Path(__file__).with_name("check-backend-config.py")
CHECK_SPEC = importlib_util.spec_from_file_location("backend_check", CHECK_PATH)
if CHECK_SPEC is None or CHECK_SPEC.loader is None:
    raise RuntimeError(f"cannot import {CHECK_PATH}")
backend_check = importlib_util.module_from_spec(CHECK_SPEC)
sys.modules[CHECK_SPEC.name] = backend_check
CHECK_SPEC.loader.exec_module(backend_check)


TIMEOUT = 90
SERVICE_VERSION = "sha-135beef"
SERVICE_INSTANCE_ID = "13500000-0000-4000-8000-000000000015"
SERVICE_TRACE_ID = "13513513513513513513513513513515"
SYNTHETIC_TRACE_ID = "51515151515151515151515151515151"
JAVA_TRACE_ID = "42424242424242424242424242424242"

METRIC_IDENTITY = {
    "service_namespace": "insurance-hub",
    "service_version": SERVICE_VERSION,
    "service_instance_id": SERVICE_INSTANCE_ID,
    "deployment_environment_name": "qa",
    "service_implementation": "go",
    "k8s_cluster_name": "qa-insurance-hub",
    "k8s_namespace_name": "qa-svc",
}

DATABASE_HISTOGRAM_BOUNDS = (
    "0.001",
    "0.005",
    "0.01",
    "0.05",
    "0.1",
    "0.5",
    "1",
    "5",
    "10",
    "+Inf",
)

REQUEST_HISTOGRAM_BOUNDS = (
    "0.005",
    "0.01",
    "0.025",
    "0.05",
    "0.075",
    "0.1",
    "0.25",
    "0.5",
    "0.75",
    "1",
    "2.5",
    "5",
    "7.5",
    "10",
    "+Inf",
)

DISPOSABLE_REMOTE_WRITE_ENDPOINT = "http://prometheus:9090/api/v1/write"
REMOTE_WRITE_FAILED_METRIC = "prometheus_remote_storage_samples_failed_total"
REMOTE_WRITE_PENDING_METRIC = "prometheus_remote_storage_samples_pending"

PROMETHEUS_SAMPLE_PATTERN = re.compile(
    r"^(?P<name>[A-Za-z_:][A-Za-z0-9_:]*)"
    r"(?:\{(?P<labels>.*)\})?\s+"
    r"(?P<value>[-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][-+]?[0-9]+)?|"
    r"NaN|[+-]Inf)(?:\s+[0-9]+)?$"
)
PROMETHEUS_LABEL_PATTERN = re.compile(
    r'(?P<name>[A-Za-z_][A-Za-z0-9_]*)="(?P<value>(?:\\.|[^"\\])*)"'
)


class HarnessError(Exception):
    """A bounded disposable-backend validation failure."""


@dataclass(frozen=True)
class PrometheusSample:
    name: str
    labels: Dict[str, str]
    value: float


def parse_prometheus_labels(text: str) -> Dict[str, str]:
    labels: Dict[str, str] = {}
    position = 0
    while position < len(text):
        match = PROMETHEUS_LABEL_PATTERN.match(text, position)
        if match is None:
            raise HarnessError(f"invalid Prometheus label set: {text!r}")
        name = match.group("name")
        if name in labels:
            raise HarnessError(f"duplicate Prometheus label: {name}")
        labels[name] = json.loads(f'"{match.group("value")}"')
        position = match.end()
        if position == len(text):
            break
        if text[position] != ",":
            raise HarnessError(f"invalid Prometheus label separator: {text!r}")
        position += 1
    return labels


def parse_prometheus_exposition(text: str) -> List[PrometheusSample]:
    samples = []
    for line_number, raw_line in enumerate(text.splitlines(), start=1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        match = PROMETHEUS_SAMPLE_PATTERN.fullmatch(line)
        if match is None:
            raise HarnessError(
                f"invalid Prometheus sample on line {line_number}: {raw_line!r}"
            )
        samples.append(
            PrometheusSample(
                name=match.group("name"),
                labels=parse_prometheus_labels(match.group("labels") or ""),
                value=float(match.group("value")),
            )
        )
    return samples


def remote_write_sample_value(text: str, metric_name: str, endpoint: str) -> float:
    matches = [
        sample
        for sample in parse_prometheus_exposition(text)
        if sample.name == metric_name and sample.labels.get("url") == endpoint
    ]
    if len(matches) != 1:
        raise HarnessError(
            f"{metric_name} has {len(matches)} series for remote-write endpoint {endpoint}"
        )
    if not math.isfinite(matches[0].value):
        raise HarnessError(
            f"{metric_name} has a non-finite value for remote-write endpoint {endpoint}"
        )
    return matches[0].value


def block_range(config: str, marker: str) -> Tuple[int, int]:
    start = config.find(marker)
    if start < 0:
        raise HarnessError(f"missing Alloy block: {marker}")
    opening = config.find("{", start)
    depth = 0
    for index in range(opening, len(config)):
        if config[index] == "{":
            depth += 1
        elif config[index] == "}":
            depth -= 1
            if depth == 0:
                return start, index + 1
    raise HarnessError(f"unterminated Alloy block: {marker}")


def replace_block(config: str, marker: str, replacement: str) -> str:
    start, end = block_range(config, marker)
    return config[:start] + replacement + config[end:]


def association_block(name: str, legacy: bool = False) -> str:
    signal = "traces" if legacy else "traces  ="
    metric_output = "" if legacy else "\n    metrics = [otelcol.processor.transform.qa_resource_identity.input]"
    output_target = (
        "otelcol.processor.transform.legacy_java_identity.input"
        if legacy
        else "otelcol.processor.transform.qa_resource_identity.input"
    )
    conditions = ' where attributes["service.name"] != nil'
    implementation = "java" if legacy else "go"
    container_suffix = "-api-legacy" if legacy else "-api"
    pod_uid_value = (
        f'"{SERVICE_INSTANCE_ID}"' if legacy else 'attributes["service.instance.id"]'
    )
    version_value = f'"{SERVICE_VERSION}"' if legacy else 'attributes["service.version"]'
    statements = [
        f'`set(attributes["k8s.namespace.name"], "qa-svc"){conditions}`',
        f'`set(attributes["k8s.cluster.name"], "qa-insurance-hub"){conditions}`',
        f'`set(attributes["k8s.container.name"], attributes["service.name"]){conditions}`',
        f'`replace_pattern(attributes["k8s.container.name"], "-service$", "{container_suffix}"){conditions}`',
        f'`set(attributes["k8s.pod.uid"], {pod_uid_value}){conditions}`',
        f'`set(attributes["insurance_hub.k8s.service.version"], {version_value}){conditions}`',
        f'`set(attributes["insurance_hub.service.implementation"], "{implementation}") where attributes["insurance_hub.service.implementation"] == nil`',
    ]
    statement_text = ",\n      ".join(statements)
    if legacy:
        body = f"""otelcol.processor.transform \"{name}\" {{
  error_mode = \"propagate\"
  trace_statements {{
    context = \"resource\"
    statements = [
      {statement_text},
    ]
  }}
  output {{
    traces = [{output_target}]
  }}
}}"""
    else:
        body = f"""otelcol.processor.transform \"{name}\" {{
  error_mode = \"propagate\"
  trace_statements {{
    context = \"resource\"
    statements = [
      {statement_text},
    ]
  }}
  metric_statements {{
    context = \"resource\"
    statements = [
      {statement_text},
    ]
  }}
  output {{
    traces  = [{output_target}]{metric_output}
  }}
}}"""
    return body


def build_alloy_config(source: str, service_domain: str, log_path: str) -> str:
    config = replace_block(
        source,
        'otelcol.processor.k8sattributes "legacy_java"',
        association_block("legacy_java", legacy=True),
    )
    config = config.replace(
        "otelcol.processor.k8sattributes.legacy_java",
        "otelcol.processor.transform.legacy_java",
    )
    config = replace_block(
        config,
        'otelcol.processor.k8sattributes "qa_services"',
        association_block("qa_services"),
    )
    config = config.replace(
        "otelcol.processor.k8sattributes.qa_services",
        "otelcol.processor.transform.qa_services",
    )
    file_targets = f"""local.file_match \"fixture_service_logs\" {{
  path_targets = [{{
    \"__path__\" = \"{log_path}\",
    \"__meta_kubernetes_namespace\" = \"qa-svc\",
    \"__meta_kubernetes_pod_label_app_kubernetes_io_name\" = \"{service_domain}-api\",
    \"__meta_kubernetes_pod_label_environment\" = \"qa\",
    \"__meta_kubernetes_pod_container_name\" = \"{service_domain}-api\",
    \"__meta_kubernetes_pod_container_port_name\" = \"http\",
    \"__meta_kubernetes_pod_uid\" = \"{SERVICE_INSTANCE_ID}\",
    \"__meta_kubernetes_pod_label_app_kubernetes_io_version\" = \"{SERVICE_VERSION}\",
  }}]
}}"""
    config = replace_block(
        config,
        'discovery.kubernetes "qa_service_pods"',
        file_targets,
    )
    config = config.replace('loki.source.kubernetes "logs"', 'loki.source.file "logs"')
    registration_marker = f'service_json_logs "{service_domain}"'
    start, end = block_range(config, registration_marker)
    registration = config[start:end].replace(
        "discovery.kubernetes.qa_service_pods.targets",
        "local.file_match.fixture_service_logs.targets",
    )
    config = config[:start] + registration + config[end:]
    replacements = {
        "qa-tempo.qa-monitoring.svc.cluster.local:4317": "tempo:4317",
        "qa-zipkin-bridge.qa-monitoring.svc.cluster.local:4317": "zipkin-bridge:4317",
        "http://qa-loki.qa-monitoring.svc.cluster.local:3100": "http://loki:3100",
        "http://qa-prometheus-kube-prometh-prometheus.qa-monitoring.svc.cluster.local:9090": "http://prometheus:9090",
    }
    for old, new in replacements.items():
        config = config.replace(old, new)
    return config


def attributes(values: Mapping[str, str]) -> List[Dict[str, Any]]:
    return [{"key": key, "value": {"stringValue": value}} for key, value in values.items()]


def resource_identity(domain: str) -> Mapping[str, str]:
    return {
        "service.namespace": "insurance-hub",
        "service.name": f"{domain}-service",
        "service.version": SERVICE_VERSION,
        "service.instance.id": SERVICE_INSTANCE_ID,
        "deployment.environment.name": "qa",
        "insurance_hub.service.implementation": "go",
    }


def synthetic_metric_payload(value: int, timestamp_ns: int) -> Mapping[str, Any]:
    identity = resource_identity("synthetic")
    dimensions = attributes(
        {
            "insurance_hub.synthetic.operation": "list",
            "insurance_hub.synthetic.transport": "http",
            "insurance_hub.synthetic.outcome": "success",
            "http.response.status_code": "200",
        }
    )
    return {
        "resourceMetrics": [
            {
                "resource": {"attributes": attributes(identity)},
                "scopeMetrics": [
                    {
                        "scope": {"name": "insurance-hub/synthetic"},
                        "metrics": [
                            {
                                "name": "insurance_hub.synthetic.requests",
                                "unit": "{request}",
                                "sum": {
                                    "aggregationTemporality": 2,
                                    "isMonotonic": True,
                                    "dataPoints": [
                                        {
                                            "attributes": dimensions,
                                            "startTimeUnixNano": str(timestamp_ns - 1_000_000_000),
                                            "timeUnixNano": str(timestamp_ns),
                                            "asInt": str(value),
                                        }
                                    ],
                                },
                            },
                            {
                                "name": "insurance_hub.synthetic.request.duration",
                                "unit": "s",
                                "histogram": {
                                    "aggregationTemporality": 2,
                                    "dataPoints": [
                                        {
                                            "attributes": dimensions,
                                            "startTimeUnixNano": str(timestamp_ns - 1_000_000_000),
                                            "timeUnixNano": str(timestamp_ns),
                                            "count": "1",
                                            "sum": 0.02,
                                            "explicitBounds": [0.005, 0.01, 0.025],
                                            "bucketCounts": ["0", "0", "1", "0"],
                                        }
                                    ],
                                },
                            },
                        ],
                    }
                ],
            }
        ]
    }


def synthetic_trace_payload(timestamp_ns: int) -> Mapping[str, Any]:
    return {
        "resourceSpans": [
            {
                "resource": {"attributes": attributes(resource_identity("synthetic"))},
                "scopeSpans": [
                    {
                        "scope": {"name": "insurance-hub/synthetic"},
                        "spans": [
                            {
                                "traceId": SYNTHETIC_TRACE_ID,
                                "spanId": "5151515151515151",
                                "name": "GET /synthetic",
                                "kind": 2,
                                "startTimeUnixNano": str(timestamp_ns),
                                "endTimeUnixNano": str(timestamp_ns + 2_000_000),
                                "attributes": attributes(
                                    {
                                        "insurance_hub.synthetic.operation": "list",
                                        "insurance_hub.synthetic.transport": "http",
                                        "insurance_hub.synthetic.outcome": "success",
                                        "http.request.method": "GET",
                                        "http.route": "/synthetic",
                                        "http.response.status_code": "200",
                                    }
                                ),
                                "status": {"code": 1},
                            }
                        ],
                    }
                ],
            }
        ]
    }


@dataclass
class DockerHarness:
    prefix: str
    root: Path
    network: str = field(init=False)
    containers: List[str] = field(default_factory=list)

    def __post_init__(self) -> None:
        self.network = f"{self.prefix}-network"

    def command(
        self,
        arguments: Sequence[str],
        *,
        input_text: Optional[str] = None,
        timeout: int = TIMEOUT,
    ) -> str:
        result = subprocess.run(
            list(arguments),
            input=input_text,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=timeout,
            check=False,
        )
        if result.returncode != 0:
            diagnostic = result.stderr.strip() or result.stdout.strip()
            raise HarnessError(
                f"command failed ({result.returncode}): {arguments[0]}: {diagnostic}"
            )
        return result.stdout.strip()

    def create(self) -> None:
        self.command(["docker", "network", "create", self.network])

    def pull(self, image: str) -> None:
        self.command(["docker", "pull", image], timeout=300)

    def start(
        self,
        role: str,
        image: str,
        arguments: Sequence[str],
        *,
        mounts: Iterable[Tuple[Path, str, bool]] = (),
        ports: Iterable[int] = (),
    ) -> str:
        name = f"{self.prefix}-{role}"
        command = ["docker", "run", "-d", "--name", name, "--network", self.network]
        command.extend(["--network-alias", role])
        for source, target, read_only in mounts:
            mount = f"type=bind,src={source},dst={target}"
            if read_only:
                mount += ",readonly"
            command.extend(["--mount", mount])
        for port in ports:
            command.extend(["-p", f"127.0.0.1::{port}"])
        command.append(image)
        command.extend(arguments)
        self.command(command)
        self.containers.append(name)
        return name

    def remove(self, name: str) -> None:
        subprocess.run(
            ["docker", "rm", "-f", name],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        if name in self.containers:
            self.containers.remove(name)

    def host_port(self, name: str, port: int) -> int:
        output = self.command(["docker", "port", name, f"{port}/tcp"])
        return int(output.rsplit(":", 1)[1])

    def logs(self, name: str) -> str:
        return subprocess.run(
            ["docker", "logs", name],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            check=False,
        ).stdout

    def cleanup(self) -> None:
        for container in reversed(self.containers[:]):
            self.remove(container)
        subprocess.run(
            ["docker", "network", "rm", self.network],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )


def write_configs(root: Path, alloy_source: str, service_domain: str) -> None:
    (root / "service.log").touch()
    (root / "prometheus.yml").write_text(
        """global:
  scrape_interval: 1s
scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: [\"localhost:9090\"]
  - job_name: alloy
    static_configs:
      - targets: [\"alloy:12345\"]
""",
        encoding="utf-8",
    )
    (root / "loki.yml").write_text(
        """auth_enabled: false
server:
  http_listen_port: 3100
common:
  path_prefix: /tmp/loki
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory
schema_config:
  configs:
    - from: 2024-01-01
      store: tsdb
      object_store: filesystem
      schema: v13
      index:
        prefix: index_
        period: 24h
storage_config:
  filesystem:
    directory: /tmp/loki/chunks
limits_config:
  allow_structured_metadata: true
""",
        encoding="utf-8",
    )
    (root / "tempo.yml").write_text(
        """server:
  http_listen_port: 3200
distributor:
  receivers:
    otlp:
      protocols:
        grpc:
          endpoint: 0.0.0.0:4317
storage:
  trace:
    backend: local
    wal:
      path: /tmp/tempo/wal
    local:
      path: /tmp/tempo/blocks
""",
        encoding="utf-8",
    )
    (root / "bridge.yml").write_text(
        """receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
exporters:
  zipkin:
    endpoint: http://zipkin:9411/api/v2/spans
service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [zipkin]
""",
        encoding="utf-8",
    )
    (root / "alloy.config").write_text(
        build_alloy_config(alloy_source, service_domain, "/fixture/service.log"),
        encoding="utf-8",
    )


def request(
    url: str,
    *,
    data: Optional[bytes] = None,
    content_type: Optional[str] = None,
    timeout: float = 5,
) -> Tuple[int, bytes]:
    headers = {"Content-Type": content_type} if content_type else {}
    value = urllib.request.Request(url, data=data, headers=headers)
    try:
        with urllib.request.urlopen(value, timeout=timeout) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def wait_http(url: str, timeout: float = TIMEOUT) -> None:
    deadline = time.monotonic() + timeout
    last = "not attempted"
    while time.monotonic() < deadline:
        try:
            status, _ = request(url)
            if status == 200:
                return
            last = f"HTTP {status}"
        except OSError as error:
            last = str(error)
        time.sleep(0.5)
    raise HarnessError(f"timed out waiting for {url}: {last}")


def wait_json(url: str, predicate: Any, description: str, timeout: float = TIMEOUT) -> Any:
    deadline = time.monotonic() + timeout
    last: Any = None
    while time.monotonic() < deadline:
        try:
            status, body = request(url)
            if status == 200:
                last = json.loads(body)
                if predicate(last):
                    return last
        except (OSError, ValueError):
            pass
        time.sleep(1)
    raise HarnessError(f"timed out waiting for {description}; last response={last!r}")


def read_alloy_metrics(port: int) -> str:
    status, body = request(f"http://127.0.0.1:{port}/metrics")
    if status != 200:
        raise HarnessError(f"Alloy metrics returned HTTP {status}")
    return body.decode("utf-8", errors="strict")


def wait_remote_write_value(
    port: int,
    metric_name: str,
    predicate: Callable[[float], bool],
    description: str,
    timeout: float = 30,
) -> float:
    deadline = time.monotonic() + timeout
    last: Any = "not observed"
    while time.monotonic() < deadline:
        try:
            last = remote_write_sample_value(
                read_alloy_metrics(port),
                metric_name,
                DISPOSABLE_REMOTE_WRITE_ENDPOINT,
            )
            if predicate(last):
                return last
        except (HarnessError, UnicodeDecodeError) as error:
            last = str(error)
        time.sleep(0.25)
    raise HarnessError(f"timed out waiting for {description}; last observation={last!r}")


def post_otlp(port: int, signal: str, payload: Mapping[str, Any]) -> None:
    status, body = request(
        f"http://127.0.0.1:{port}/v1/{signal}",
        data=json.dumps(payload).encode("utf-8"),
        content_type="application/json",
    )
    if status != 200:
        raise HarnessError(f"OTLP {signal} returned HTTP {status}: {body[:200]!r}")


def post_zipkin(port: int) -> None:
    now_micros = time.time_ns() // 1_000
    payload = [
        {
            "traceId": JAVA_TRACE_ID,
            "id": "4242424242424242",
            "name": "get /products",
            "kind": "SERVER",
            "timestamp": now_micros,
            "duration": 2_000,
            "localEndpoint": {"serviceName": "product-service"},
            "tags": {
                "http.method": "GET",
                "http.path": "/products",
                "http.status_code": "200",
            },
        }
    ]
    status, body = request(
        f"http://127.0.0.1:{port}/api/v2/spans",
        data=json.dumps(payload).encode("utf-8"),
        content_type="application/json",
    )
    if status != 202:
        raise HarnessError(f"Zipkin ingestion returned HTTP {status}: {body[:200]!r}")


def prom_query(port: int, expression: str) -> Mapping[str, Any]:
    encoded = urllib.parse.urlencode({"query": expression})
    status, body = request(f"http://127.0.0.1:{port}/api/v1/query?{encoded}")
    if status != 200:
        raise HarnessError(f"Prometheus query returned HTTP {status}")
    return json.loads(body)


def query_results(response: Mapping[str, Any]) -> List[Mapping[str, Any]]:
    return list(response.get("data", {}).get("result", []))


def assert_metric_labels(result: Mapping[str, Any], service_name: str) -> None:
    labels = result.get("metric", {})
    expected = dict(METRIC_IDENTITY, service_name=service_name)
    expected.update(
        {
            "job": f"insurance-hub/{service_name}",
            "instance": SERVICE_INSTANCE_ID,
        }
    )
    for key, value in expected.items():
        if labels.get(key) != value:
            raise HarnessError(f"metric label {key}={labels.get(key)!r}, expected {value!r}")


def metric_series(
    prometheus_port: int,
    metric_name: str,
    service_name: str,
    expected_dimensions: Sequence[Mapping[str, str]],
) -> List[Mapping[str, Any]]:
    expression = f'{metric_name}{{service_name="{service_name}"}}'
    response = wait_json(
        f"http://127.0.0.1:{prometheus_port}/api/v1/query?"
        + urllib.parse.urlencode({"query": expression}),
        lambda value: len(query_results(value)) >= len(expected_dimensions),
        f"{len(expected_dimensions)} unique {metric_name} series",
    )
    results = query_results(response)
    if len(results) != len(expected_dimensions):
        raise HarnessError(
            f"{metric_name} has {len(results)} series, "
            f"expected {len(expected_dimensions)}"
        )
    identity_keys = set(METRIC_IDENTITY) | {
        "service_name",
        "job",
        "instance",
        "__name__",
    }
    actual_dimensions = []
    for result in results:
        assert_metric_labels(result, service_name)
        labels = result.get("metric", {})
        actual_dimensions.append(
            {key: value for key, value in labels.items() if key not in identity_keys}
        )
        value = float(result.get("value", [0, "nan"])[1])
        if not math.isfinite(value):
            raise HarnessError(f"{metric_name} has a non-finite sample: {result}")
    normalized_actual = sorted(
        tuple(sorted(value.items())) for value in actual_dimensions
    )
    normalized_expected = sorted(
        tuple(sorted(value.items())) for value in expected_dimensions
    )
    if normalized_actual != normalized_expected:
        raise HarnessError(
            f"{metric_name} dimensions are {actual_dimensions}, "
            f"expected {list(expected_dimensions)}"
        )
    return results


def validate_histogram(
    prometheus_port: int,
    metric_name: str,
    service_name: str,
    dimensions: Mapping[str, str],
    bounds: Sequence[str],
) -> None:
    bucket_dimensions = [dict(dimensions, le=bound) for bound in bounds]
    metric_series(
        prometheus_port,
        f"{metric_name}_bucket",
        service_name,
        bucket_dimensions,
    )
    count = metric_series(
        prometheus_port,
        f"{metric_name}_count",
        service_name,
        [dimensions],
    )
    metric_series(
        prometheus_port,
        f"{metric_name}_sum",
        service_name,
        [dimensions],
    )
    if float(count[0]["value"][1]) < 1:
        raise HarnessError(f"{metric_name} has no real observations")


def validate_database_pool_and_runtime_metrics(
    service_domain: str,
    prometheus_port: int,
) -> None:
    service_name = f"{service_domain}-service"
    metric_domain = service_domain.replace("-", "_")
    operation_label = f"insurance_hub_{metric_domain}_operation"
    database_stage_label = f"insurance_hub_{metric_domain}_database_stage"
    database_outcome_label = f"insurance_hub_{metric_domain}_database_outcome"

    operations = metric_series(
        prometheus_port,
        f"insurance_hub_{metric_domain}_database_operations_total",
        service_name,
        [
            {
                operation_label: "list",
                database_stage_label: stage,
                database_outcome_label: "success",
            }
            for stage in ("acquire", "query", "decode")
        ],
    )
    if any(float(result["value"][1]) != 1 for result in operations):
        raise HarnessError(f"{service_domain} database stage counters are not exactly one each")

    validate_histogram(
        prometheus_port,
        "db_client_operation_duration_seconds",
        service_name,
        {
            operation_label: "list",
            "db_system_name": "postgresql",
            "db_namespace": service_domain,
            "db_collection_name": f"public.{service_domain}",
            "db_operation_name": "SELECT",
        },
        DATABASE_HISTOGRAM_BOUNDS,
    )
    validate_histogram(
        prometheus_port,
        "db_client_connection_wait_time_seconds",
        service_name,
        {"db_client_connection_pool_name": f"{service_domain}-reader"},
        DATABASE_HISTOGRAM_BOUNDS,
    )

    pool_states = metric_series(
        prometheus_port,
        "db_client_connection_count",
        service_name,
        [
            {
                "db_client_connection_pool_name": f"{service_domain}-reader",
                "db_client_connection_state": state,
            }
            for state in ("idle", "used")
        ],
    )
    state_values = {
        result["metric"]["db_client_connection_state"]: float(result["value"][1])
        for result in pool_states
    }
    if state_values != {"idle": 1, "used": 0}:
        raise HarnessError(
            f"{service_domain} pool states are {state_values}, expected one idle and zero used"
        )
    pool_max = metric_series(
        prometheus_port,
        "db_client_connection_max",
        service_name,
        [{"db_client_connection_pool_name": f"{service_domain}-reader"}],
    )
    if float(pool_max[0]["value"][1]) != 4:
        raise HarnessError(f"{service_domain} pool maximum is not the configured value 4")

    runtime_metrics = {
        "go_memory_used_bytes": [
            {"go_memory_type": "stack"},
            {"go_memory_type": "other"},
        ],
        "go_memory_limit_bytes": [{}],
        "go_memory_allocated_bytes_total": [{}],
        "go_memory_allocations_total": [{}],
        "go_memory_gc_goal_bytes": [{}],
        "go_memory_gc_cycles_total": [{}],
        "go_goroutine_count": [{}],
        "go_processor_limit": [{}],
    }
    for metric_name, dimensions in runtime_metrics.items():
        results = metric_series(prometheus_port, metric_name, service_name, dimensions)
        values = [float(result["value"][1]) for result in results]
        if any(value < 0 for value in values):
            raise HarnessError(f"{metric_name} contains a negative runtime value")
        if metric_name == "go_memory_limit_bytes" and values != [256 * 1024 * 1024]:
            raise HarnessError(
                f"{metric_name} is {values}, expected the probe's 256MiB limit in bytes"
            )


def start_prometheus(harness: DockerHarness, enabled: bool) -> Tuple[str, int]:
    arguments = [
        "--config.file=/fixture/prometheus.yml",
        "--storage.tsdb.path=/tmp/prometheus",
        "--storage.tsdb.retention.time=1h",
    ]
    if enabled:
        arguments.append("--web.enable-remote-write-receiver")
    name = harness.start(
        "prometheus",
        backend_check.DISPOSABLE_IMAGES["prometheus"],
        arguments,
        mounts=[(harness.root, "/fixture", True)],
        ports=[9090],
    )
    port = harness.host_port(name, 9090)
    wait_http(f"http://127.0.0.1:{port}/-/ready")
    return name, port


def start_alloy(harness: DockerHarness) -> Tuple[str, int, int]:
    name = harness.start(
        "alloy",
        backend_check.DISPOSABLE_IMAGES["alloy"],
        [
            "run",
            "--server.http.listen-addr=0.0.0.0:12345",
            "--storage.path=/tmp/alloy",
            "/fixture/alloy.config",
        ],
        mounts=[(harness.root, "/fixture", False)],
        ports=[4317, 4318, 9411, 12345],
    )
    http_port = harness.host_port(name, 4318)
    metrics_port = harness.host_port(name, 12345)
    wait_http(f"http://127.0.0.1:{metrics_port}/-/ready")
    return name, http_port, metrics_port


def validate_service_backends(
    service_domain: str,
    request_histogram_buckets: int,
    prometheus_port: int,
    loki_port: int,
    tempo_port: int,
    zipkin_port: int,
) -> None:
    service_name = f"{service_domain}-service"
    metric_domain = service_domain.replace("-", "_")
    request_dimensions = {
        f"insurance_hub_{metric_domain}_operation": "list",
        f"insurance_hub_{metric_domain}_transport": "http",
        f"insurance_hub_{metric_domain}_outcome": "success",
        "http_response_status_code": "200",
    }
    request_counter = metric_series(
        prometheus_port,
        f"insurance_hub_{metric_domain}_requests_total",
        service_name,
        [request_dimensions],
    )
    if float(request_counter[0]["value"][1]) != 1:
        raise HarnessError(f"{service_domain} request counter is not exactly one")
    if request_histogram_buckets != len(REQUEST_HISTOGRAM_BOUNDS):
        raise HarnessError(
            f"{service_domain} request histogram contract has "
            f"{len(REQUEST_HISTOGRAM_BOUNDS)} buckets, configured proof expects "
            f"{request_histogram_buckets}"
        )
    validate_histogram(
        prometheus_port,
        f"insurance_hub_{metric_domain}_request_duration_seconds",
        service_name,
        request_dimensions,
        REQUEST_HISTOGRAM_BOUNDS,
    )
    validate_database_pool_and_runtime_metrics(service_domain, prometheus_port)
    target = prom_query(prometheus_port, f'target_info{{service_name="{service_name}"}}')
    if len(query_results(target)) != 1:
        raise HarnessError(f"{service_domain} target_info is missing or duplicated")
    wait_json(
        f"http://127.0.0.1:{tempo_port}/api/traces/{SERVICE_TRACE_ID}",
        lambda value: len(value.get("batches", [])) >= 1,
        f"real {service_domain} trace in Tempo",
    )
    wait_json(
        f"http://127.0.0.1:{zipkin_port}/api/v2/trace/{SERVICE_TRACE_ID}",
        lambda value: isinstance(value, list) and len(value) >= 1,
        f"real {service_domain} trace through the Zipkin bridge",
    )
    # Filter the retained JSON body without a query-time parser so the returned
    # stream map contains only stored index labels, not extracted fields.
    query = f'{{service_name="{service_name}"}} |= "{SERVICE_TRACE_ID}"'
    start = int((time.time() - 300) * 1_000_000_000)
    end = int((time.time() + 30) * 1_000_000_000)
    url = (
        f"http://127.0.0.1:{loki_port}/loki/api/v1/query_range?"
        + urllib.parse.urlencode({"query": query, "start": start, "end": end, "limit": 20})
    )
    logs = wait_json(
        url,
        lambda value: sum(
            len(stream.get("values", [])) for stream in query_results(value)
        )
        == 1,
        f"one correlated {service_domain} completion log",
    )
    streams = query_results(logs)
    returned_fields = streams[0].get("stream", {})
    structured = {
        "service_instance_id",
        "k8s_cluster_name",
        "k8s_pod_uid",
        "trace_id",
        "span_id",
    }
    if not structured.issubset(returned_fields):
        raise HarnessError(f"{service_domain} Loki structured metadata is incomplete")
    series_url = (
        f"http://127.0.0.1:{loki_port}/loki/api/v1/series?"
        + urllib.parse.urlencode(
            {
                "match[]": f'{{service_name="{service_name}"}}',
                "start": start,
                "end": end,
            }
        )
    )
    series = wait_json(
        series_url,
        lambda value: len(value.get("data", [])) == 1,
        f"one {service_domain} Loki index series",
    )
    labels = series["data"][0]
    if set(labels) != {
        "service_namespace",
        "service_name",
        "deployment_environment_name",
        "service_implementation",
        "service_version",
        "k8s_namespace_name",
        "k8s_container_name",
        "level",
    }:
        raise HarnessError(
            f"{service_domain} Loki index labels differ from the eight-field contract: {labels}"
        )
    for job in ("prometheus", "alloy"):
        up = prom_query(prometheus_port, f'up{{job="{job}"}}')
        results = query_results(up)
        if len(results) != 1 or float(results[0]["value"][1]) != 1:
            raise HarnessError(f"existing disposable scrape path {job} is not healthy")


def execute(args: argparse.Namespace) -> None:
    if not re.fullmatch(r"[a-z][a-z0-9-]*", args.service_domain):
        raise HarnessError(f"invalid service domain: {args.service_domain}")
    if args.request_histogram_buckets < 1:
        raise HarnessError("request histogram bucket-series count must be positive")
    for path, description in (
        (args.config, "Alloy source"),
        (args.service_root, "service root"),
        (args.service_probe, "service probe"),
    ):
        if not path.exists():
            raise HarnessError(f"{description} does not exist: {path}")
    source = args.config.read_text(encoding="utf-8")
    prefix = f"ih-observe-{os.getpid()}"
    with tempfile.TemporaryDirectory(prefix="insurance-hub-observability-") as temporary:
        root = Path(temporary)
        # Pinned backend images run as non-root users. The directory contains only
        # synthetic configuration and logs, so allow container traversal/read access.
        root.chmod(0o755)
        write_configs(root, source, args.service_domain)
        harness = DockerHarness(prefix, root)
        harness.create()
        try:
            for image in backend_check.DISPOSABLE_IMAGES.values():
                if image == backend_check.DISPOSABLE_IMAGES["postgres"]:
                    continue
                harness.pull(image)
            tempo = harness.start(
                "tempo",
                backend_check.DISPOSABLE_IMAGES["tempo"],
                ["-config.file=/fixture/tempo.yml"],
                mounts=[(root, "/fixture", True)],
                ports=[3200],
            )
            loki = harness.start(
                "loki",
                backend_check.DISPOSABLE_IMAGES["loki"],
                ["-config.file=/fixture/loki.yml"],
                mounts=[(root, "/fixture", True)],
                ports=[3100],
            )
            zipkin = harness.start(
                "zipkin",
                backend_check.DISPOSABLE_IMAGES["zipkin"],
                [],
                ports=[9411],
            )
            harness.start(
                "zipkin-bridge",
                backend_check.DISPOSABLE_IMAGES["zipkin_bridge"],
                ["--config=/fixture/bridge.yml"],
                mounts=[(root, "/fixture", True)],
            )
            tempo_port = harness.host_port(tempo, 3200)
            loki_port = harness.host_port(loki, 3100)
            zipkin_port = harness.host_port(zipkin, 9411)
            wait_http(f"http://127.0.0.1:{tempo_port}/ready")
            wait_http(f"http://127.0.0.1:{loki_port}/ready")
            wait_http(f"http://127.0.0.1:{zipkin_port}/health")

            prometheus, prometheus_port = start_prometheus(harness, enabled=False)
            rejection_status, _ = request(
                f"http://127.0.0.1:{prometheus_port}/api/v1/write",
                data=b"",
                content_type="application/x-protobuf",
            )
            if rejection_status not in (404, 405):
                raise HarnessError(
                    f"disabled Prometheus receiver returned HTTP {rejection_status}, expected rejection"
                )
            alloy, alloy_otlp_port, alloy_metrics_port = start_alloy(harness)
            alloy_zipkin_port = harness.host_port(alloy, 9411)
            failed_baseline = wait_remote_write_value(
                alloy_metrics_port,
                REMOTE_WRITE_FAILED_METRIC,
                lambda value: value == 0,
                "zero failed-sample baseline before receiver rejection",
            )
            now = time.time_ns()
            post_otlp(alloy_otlp_port, "metrics", synthetic_metric_payload(7, now))
            post_otlp(alloy_otlp_port, "traces", synthetic_trace_payload(now))
            post_zipkin(alloy_zipkin_port)
            failed_fault = wait_remote_write_value(
                alloy_metrics_port,
                REMOTE_WRITE_FAILED_METRIC,
                lambda value: value > failed_baseline,
                "positive failed-sample delta after receiver rejection",
            )
            print(
                "remote-write rejection evidence: "
                f"failed samples {failed_baseline:g}->{failed_fault:g}"
            )

            harness.remove(prometheus)
            prometheus, prometheus_port = start_prometheus(harness, enabled=True)
            # HTTP 404 is intentionally non-retryable: it proves a disabled receiver is
            # visible as loss. Send a fresh cumulative point once the desired receiver is on.
            post_otlp(alloy_otlp_port, "metrics", synthetic_metric_payload(7, time.time_ns()))
            synthetic = wait_json(
                f"http://127.0.0.1:{prometheus_port}/api/v1/query?"
                + urllib.parse.urlencode(
                    {
                        "query": (
                            'insurance_hub_synthetic_requests_total'
                            '{service_name="synthetic-service"}'
                        )
                    }
                ),
                lambda value: len(query_results(value)) == 1
                and float(query_results(value)[0]["value"][1]) == 7,
                "fresh cumulative synthetic counter after receiver enablement",
            )
            assert_metric_labels(query_results(synthetic)[0], "synthetic-service")
            histogram = prom_query(
                prometheus_port,
                'insurance_hub_synthetic_request_duration_seconds_bucket'
                '{service_name="synthetic-service"}',
            )
            if len(query_results(histogram)) != 4:
                raise HarnessError("synthetic explicit histogram buckets were not preserved")
            target = prom_query(prometheus_port, 'target_info{service_name="synthetic-service"}')
            if len(query_results(target)) != 1:
                raise HarnessError("synthetic target_info is missing or duplicated")
            wait_json(
                f"http://127.0.0.1:{tempo_port}/api/traces/{SYNTHETIC_TRACE_ID}",
                lambda value: len(value.get("batches", [])) == 1,
                "synthetic trace in Tempo",
            )
            wait_json(
                f"http://127.0.0.1:{zipkin_port}/api/v2/trace/{SYNTHETIC_TRACE_ID}",
                lambda value: isinstance(value, list) and len(value) == 1,
                "synthetic trace through bridge",
            )
            wait_json(
                f"http://127.0.0.1:{tempo_port}/api/traces/{JAVA_TRACE_ID}",
                lambda value: len(value.get("batches", [])) == 1,
                "legacy Zipkin trace in Tempo",
            )
            wait_json(
                f"http://127.0.0.1:{zipkin_port}/api/v2/trace/{JAVA_TRACE_ID}",
                lambda value: isinstance(value, list) and len(value) == 1,
                "legacy Zipkin trace through bridge",
            )

            # Connection refusal is retryable. Keep Alloy alive while Prometheus is absent,
            # then prove its WAL forwards the later cumulative point to a fresh backend.
            pending_baseline = wait_remote_write_value(
                alloy_metrics_port,
                REMOTE_WRITE_PENDING_METRIC,
                lambda value: value == 0,
                "zero pending-sample baseline before backend outage",
            )
            harness.remove(prometheus)
            post_otlp(alloy_otlp_port, "metrics", synthetic_metric_payload(9, time.time_ns()))
            pending_fault = wait_remote_write_value(
                alloy_metrics_port,
                REMOTE_WRITE_PENDING_METRIC,
                lambda value: value > pending_baseline,
                "positive pending-sample gauge during backend outage",
            )
            print(
                "remote-write outage evidence: "
                f"pending samples {pending_baseline:g}->{pending_fault:g}"
            )
            prometheus, prometheus_port = start_prometheus(harness, enabled=True)
            wait_json(
                f"http://127.0.0.1:{prometheus_port}/api/v1/query?"
                + urllib.parse.urlencode(
                    {
                        "query": (
                            'insurance_hub_synthetic_requests_total'
                            '{service_name="synthetic-service"}'
                        )
                    }
                ),
                lambda value: len(query_results(value)) == 1
                and float(query_results(value)[0]["value"][1]) == 9,
                "WAL-replayed cumulative synthetic counter",
            )

            harness.remove(alloy)
            alloy, alloy_otlp_port, _ = start_alloy(harness)
            post_otlp(alloy_otlp_port, "metrics", synthetic_metric_payload(11, time.time_ns()))
            wait_json(
                f"http://127.0.0.1:{prometheus_port}/api/v1/query?"
                + urllib.parse.urlencode(
                    {
                        "query": (
                            'insurance_hub_synthetic_requests_total'
                            '{service_name="synthetic-service"}'
                        )
                    }
                ),
                lambda value: len(query_results(value)) == 1
                and float(query_results(value)[0]["value"][1]) == 11,
                "new metric delivery after Alloy replacement",
            )

            grpc_port = harness.host_port(alloy, 4317)
            probe = args.service_probe.resolve()
            harness.command(
                [
                    sys.executable,
                    str(probe),
                    "--service-root",
                    str(args.service_root),
                    "--otlp-endpoint",
                    f"http://127.0.0.1:{grpc_port}",
                    "--log-file",
                    str(root / "service.log"),
                    "--version",
                    SERVICE_VERSION,
                    "--instance-id",
                    SERVICE_INSTANCE_ID,
                    "--trace-id",
                    SERVICE_TRACE_ID,
                ],
                timeout=300,
            )
            validate_service_backends(
                args.service_domain,
                args.request_histogram_buckets,
                prometheus_port,
                loki_port,
                tempo_port,
                zipkin_port,
            )
        except Exception as error:
            diagnostics = []
            for name in harness.containers:
                diagnostics.append(f"\n--- {name} ---\n{harness.logs(name)[-4000:]}")
            raise HarnessError(f"{error}{''.join(diagnostics)}") from error
        finally:
            harness.cleanup()


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--service-root", type=Path, required=True)
    parser.add_argument("--service-probe", type=Path, required=True)
    parser.add_argument("--service-domain", default="product")
    parser.add_argument("--request-histogram-buckets", type=int, required=True)
    return parser.parse_args(argv)


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = parse_args(argv)
    try:
        execute(args)
    except (OSError, HarnessError, subprocess.TimeoutExpired) as error:
        print(f"backend ingestion test failed: {error}", file=sys.stderr)
        return 1
    print(
        f"backend ingestion test passed: synthetic and real {args.service_domain} "
        "signals are queryable"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
