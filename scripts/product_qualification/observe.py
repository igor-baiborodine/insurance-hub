"""Read-only QA resource, readiness and established-route observations."""

from __future__ import annotations

import json
import math
import re
import subprocess
import threading
import time
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from typing import Any

from scripts.product_qualification.core import (
    QualificationError,
    compare_body,
    decode_body,
    http_request,
    validate_target,
)

POD_NAME = re.compile(r"qa-[a-z0-9-]+\Z")


def cpu_cores(value: str) -> float:
    if value.endswith("m"):
        return float(value[:-1]) / 1000
    return float(value)


def memory_bytes(value: str) -> float:
    units = {"Ki": 1024, "Mi": 1024**2, "Gi": 1024**3}
    for suffix, factor in units.items():
        if value.endswith(suffix):
            return float(value[: -len(suffix)]) * factor
    return float(value)


def pod(name: str, namespace: str) -> dict[str, Any]:
    if not POD_NAME.fullmatch(name) or namespace not in ("qa-svc", "qa-data"):
        raise QualificationError("invalid QA Pod target")
    command = [
        "kubectl",
        "--context",
        "qa-insurance-hub",
        "--namespace",
        namespace,
        "get",
        "pod",
        name,
        "-o",
        "json",
    ]
    try:
        result = subprocess.run(command, capture_output=True, text=True, timeout=8, check=True)
        value = json.loads(result.stdout)
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError, ValueError) as error:
        raise QualificationError("QA Pod status unavailable") from error
    if (
        value.get("metadata", {}).get("name") != name
        or value.get("metadata", {}).get("namespace") != namespace
    ):
        raise QualificationError("QA Pod target identity changed")
    return value


def identity(value: dict[str, Any]) -> dict[str, Any]:
    try:
        return _identity(value)
    except (KeyError, TypeError, ValueError) as error:
        raise QualificationError("QA Pod status or resource limits are malformed") from error


def _identity(value: dict[str, Any]) -> dict[str, Any]:
    metadata, spec, status = value["metadata"], value["spec"], value["status"]
    containers = spec["containers"]
    states = status.get("containerStatuses", [])
    if (
        len(containers) != 1
        or len(states) != 1
        or not states[0].get("ready")
        or status.get("phase") != "Running"
    ):
        raise QualificationError("QA Pod is not ready with one container")
    limits = containers[0].get("resources", {}).get("limits", {})
    cpu, memory = cpu_cores(limits.get("cpu", "")), memory_bytes(limits.get("memory", ""))
    if not math.isfinite(cpu) or not math.isfinite(memory) or cpu <= 0 or memory <= 0:
        raise QualificationError("QA Pod effective resource limits are missing")
    return {
        "pod": metadata["name"],
        "namespace": metadata["namespace"],
        "uid": metadata["uid"],
        "container": containers[0]["name"],
        "restarts": states[0]["restartCount"],
        "cpu_limit_cores": cpu,
        "memory_limit_bytes": memory,
    }


def metric(base: str, expression: str) -> float:
    url = base + "/api/v1/query?" + urllib.parse.urlencode({"query": expression})
    try:
        with urllib.request.urlopen(url, timeout=5) as response:
            payload = response.read(1 << 20)
        value = json.loads(payload)
        vector = value["data"]["result"]
        if value["status"] != "success" or len(vector) != 1:
            raise QualificationError("resource metric has no unique sample")
        sample = float(vector[0]["value"][1])
        if not math.isfinite(sample) or sample < 0:
            raise QualificationError("resource metric is invalid")
        return sample
    except (OSError, ValueError, KeyError, IndexError, TypeError) as error:
        raise QualificationError("resource metric unavailable") from error


def usage(prometheus: str, target: dict[str, Any]) -> dict[str, float]:
    labels = (
        f'namespace="{target["namespace"]}",pod="{target["pod"]}",container="{target["container"]}"'
    )
    cpu = metric(prometheus, f"sum(rate(container_cpu_usage_seconds_total{{{labels}}}[60s]))")
    memory = metric(
        prometheus, f"sum(avg_over_time(container_memory_working_set_bytes{{{labels}}}[60s]))"
    )
    return {
        "cpu_cores_60s": cpu,
        "memory_bytes_60s": memory,
        "cpu_fraction_of_limit": cpu / target["cpu_limit_cores"],
        "memory_fraction_of_limit": memory / target["memory_limit_bytes"],
    }


def database_connections(name: str) -> int:
    if not POD_NAME.fullmatch(name):
        raise QualificationError("invalid Product database Pod")
    command = [
        "kubectl",
        "--context",
        "qa-insurance-hub",
        "--namespace",
        "qa-data",
        "exec",
        name,
        "--",
        "psql",
        "-X",
        "-q",
        "-A",
        "-t",
        "-v",
        "ON_ERROR_STOP=1",
        "--username=postgres",
        "--dbname=product",
        "--command=SET default_transaction_read_only=on; "
        "SELECT COALESCE(sum(numbackends), 0) FROM pg_stat_database WHERE datname='product';",
    ]
    try:
        result = subprocess.run(command, capture_output=True, text=True, timeout=8, check=True)
        lines = [line.strip() for line in result.stdout.splitlines() if line.strip()]
        if len(lines) != 1 or not lines[0].isdigit():
            raise QualificationError("Product database connection count is malformed")
        return int(lines[0])
    except (OSError, subprocess.SubprocessError) as error:
        raise QualificationError("Product database connection count unavailable") from error


@dataclass
class Observer:
    prometheus: str
    established: str
    token: str
    accepted_list: Any
    gateway: dict[str, Any]
    product: dict[str, Any]
    database: dict[str, Any]
    records: list[dict[str, Any]] = field(default_factory=list)
    failure: str | None = None
    stopped: threading.Event = field(default_factory=threading.Event)
    thread: threading.Thread | None = None

    def __post_init__(self) -> None:
        validate_target(self.prometheus)
        validate_target(self.established)

    def snapshot(self) -> dict[str, Any]:
        current = [
            identity(pod(item["pod"], item["namespace"]))
            for item in (self.gateway, self.product, self.database)
        ]
        for before, after in zip((self.gateway, self.product, self.database), current, strict=True):
            if before != after:
                raise QualificationError("QA Pod readiness, identity, restart or limit changed")
        gateway_usage = usage(self.prometheus, self.gateway)
        product_usage = usage(self.prometheus, self.product)
        for observed in (gateway_usage, product_usage):
            if (
                observed["cpu_fraction_of_limit"] > 0.8
                or observed["memory_fraction_of_limit"] > 0.8
            ):
                raise QualificationError("preview or Product resource exceeded 80% of limit")
        try:
            status, body = http_request(self.established, self.token, "/api/products")
        except OSError as error:
            raise QualificationError("established gateway list control unavailable") from error
        if status != 200:
            raise QualificationError("established gateway list control failed")
        compare_body(self.accepted_list, decode_body(body), "established")
        database_usage: dict[str, float] | None
        try:
            database_usage = usage(self.prometheus, self.database)
        except QualificationError:
            database_usage = None
        connections = database_connections(self.database["pod"])
        return {
            "observed_at_unix": time.time(),
            "gateway": gateway_usage,
            "product": product_usage,
            "database": database_usage,
            "database_connections": connections,
            "established_list_status": status,
        }

    def start(self) -> None:
        self.records.append(self.snapshot())

        def loop() -> None:
            next_due = time.monotonic() + 30
            while not self.stopped.wait(max(0, next_due - time.monotonic())):
                try:
                    self.records.append(self.snapshot())
                except QualificationError as error:
                    self.failure = str(error)
                    return
                next_due += 30

        self.thread = threading.Thread(target=loop, name="product-qa-observer", daemon=True)
        self.thread.start()

    def check(self) -> None:
        if self.failure:
            raise QualificationError(self.failure)

    def close(self) -> None:
        self.stopped.set()
        if self.thread:
            self.thread.join(timeout=12)
            if self.thread.is_alive() and not self.failure:
                self.failure = "resource observer did not stop within teardown bound"
