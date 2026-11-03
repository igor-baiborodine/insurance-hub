#!/usr/bin/env python3
"""Read-only service observability verification against an explicit QA target."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import socket
import subprocess
import sys
import time
import urllib.parse
import urllib.request
from collections.abc import Callable, Iterator, Mapping, Sequence
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import monitoring_runtime
from monitoring_assets import (
    AssetError,
    MonitoringAssets,
    ServiceVerification,
    grafana_dashboard_resource,
    load_assets,
    prometheus_rule_resource,
)

COMMAND_TIMEOUT = 30
FORWARD_TIMEOUT = 15
QUERY_TIMEOUT = 45
REVISION = re.compile(r"^[0-9a-f]{40}$")


class VerificationError(Exception):
    """A safe read-only verification failure."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise VerificationError(message)


def run(arguments: Sequence[str], timeout: int = COMMAND_TIMEOUT) -> str:
    try:
        result = subprocess.run(
            list(arguments), capture_output=True, text=True, timeout=timeout, check=False
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise VerificationError(f"command unavailable or timed out: {arguments[0]}") from error
    require(
        result.returncode == 0,
        f"command failed: {arguments[0]} (exit {result.returncode})",
    )
    return result.stdout


def request_json(url: str, timeout: float = 10) -> dict[str, Any]:
    try:
        with urllib.request.urlopen(url, timeout=timeout) as response:
            require(
                response.status == 200, f"verification endpoint returned HTTP {response.status}"
            )
            value = json.load(response)
    except VerificationError:
        raise
    except (OSError, ValueError, json.JSONDecodeError) as error:
        raise VerificationError("verification endpoint was unavailable or malformed") from error
    require(isinstance(value, dict), "verification endpoint returned a non-object response")
    return value


def request_text(url: str, headers: Mapping[str, str] | None = None) -> str:
    request = urllib.request.Request(url, headers=dict(headers or {}))
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            require(response.status == 200, f"Product endpoint returned HTTP {response.status}")
            return response.read().decode("utf-8")
    except VerificationError:
        raise
    except OSError as error:
        raise VerificationError("Product endpoint was unavailable") from error


def verification_config(assets: MonitoringAssets) -> ServiceVerification:
    value = assets.environment.service_verification.get(assets.service)
    require(value is not None, f"environment has no {assets.service} verification coordinates")
    return value


class KubernetesTarget:
    def __init__(self, assets: MonitoringAssets, source_revision: str) -> None:
        require(
            assets.environment_name == "qa", "live service observability verification is QA-only"
        )
        require(
            bool(REVISION.fullmatch(source_revision)),
            "source revision must be 40 lowercase hex characters",
        )
        self.assets = assets
        self.source_revision = source_revision
        self.context = assets.environment.kube_context
        self.monitoring_namespace = assets.environment.monitoring_namespace
        self.config = verification_config(assets)
        self.service_namespace = self.config.service_namespace

    def kubectl(self, namespace: str, *arguments: str) -> str:
        return run(["kubectl", "--context", self.context, "--namespace", namespace, *arguments])

    def json(self, namespace: str, *arguments: str) -> dict[str, Any]:
        try:
            value = json.loads(self.kubectl(namespace, *arguments, "-o", "json"))
        except json.JSONDecodeError as error:
            raise VerificationError("Kubernetes returned malformed JSON") from error
        require(isinstance(value, dict), "Kubernetes returned a non-object resource")
        return value

    def coordinates(self) -> None:
        active = run(["kubectl", "config", "current-context"]).strip()
        require(
            active == self.context, "active Kubernetes context differs from the requested target"
        )
        namespace = (
            run(["kubectl", "config", "view", "--minify", "-o", "jsonpath={..namespace}"]).strip()
            or "default"
        )
        require(
            namespace == self.service_namespace,
            "active Kubernetes namespace differs from the selected service namespace",
        )
        for selected in (self.service_namespace, self.monitoring_namespace):
            self.json(selected, "get", "namespace", selected)

    def flux_revision(self) -> str:
        namespace = self.config.flux_namespace
        source = self.json(namespace, "get", "gitrepository", self.config.flux_source)
        reconciler = self.json(namespace, "get", "kustomization", self.config.flux_kustomization)
        for item in (source, reconciler):
            require(
                any(
                    condition.get("type") == "Ready" and condition.get("status") == "True"
                    for condition in item.get("status", {}).get("conditions", [])
                ),
                "Flux source or selected service Kustomization is not ready",
            )
        source_revision = source.get("status", {}).get("artifact", {}).get("revision", "")
        applied_revision = reconciler.get("status", {}).get("lastAppliedRevision", "")
        require(
            source_revision.endswith(self.source_revision) and applied_revision == source_revision,
            "Flux source/applied revision differs from the requested source revision",
        )
        return source_revision

    def deployment(self, mode: str) -> dict[str, Any]:
        deployment = self.json(
            self.service_namespace,
            "get",
            "deployment",
            self.config.deployment,
        )
        desired = deployment.get("spec", {}).get("replicas", 0)
        ready = deployment.get("status", {}).get("readyReplicas", 0)
        require(desired > 0 and ready == desired, "selected service Deployment is not fully ready")
        containers = (
            deployment.get("spec", {}).get("template", {}).get("spec", {}).get("containers", [])
        )
        require(
            len(containers) == 1,
            "selected service Deployment has an unexpected container inventory",
        )
        settings: dict[str, str] = {}
        for source in containers[0].get("envFrom", []):
            name = source.get("configMapRef", {}).get("name")
            require(
                isinstance(name, str) and name,
                "selected service envFrom is not a ConfigMap reference",
            )
            config_map = self.json(self.service_namespace, "get", "configmap", name)
            data = config_map.get("data", {})
            require(isinstance(data, dict), "selected service ConfigMap data is malformed")
            settings.update({key: value for key, value in data.items() if isinstance(value, str)})
        for variable in containers[0].get("env", []):
            if isinstance(variable.get("value"), str):
                settings[variable["name"]] = variable["value"]
        expected = "true" if mode == "enabled" else "false"
        require(
            settings.get("OTEL_ENABLED") == expected,
            f"selected service OTEL_ENABLED must be {expected}",
        )
        require(
            settings.get("SERVICE_NAME") == self.assets.contract.service_name,
            "selected service identity differs",
        )
        if mode == "enabled":
            require(
                settings.get("DEPLOYMENT_ENVIRONMENT") == "qa",
                "enabled service environment must be qa",
            )
            endpoint = settings.get("OTEL_EXPORTER_OTLP_ENDPOINT", "")
            require(
                endpoint.startswith("http://qa-alloy.qa-monitoring.svc"),
                "enabled service OTLP endpoint is not internal Alloy",
            )
        selector_labels = deployment.get("spec", {}).get("selector", {}).get("matchLabels", {})
        require(
            isinstance(selector_labels, dict)
            and selector_labels
            and all(
                isinstance(key, str) and isinstance(value, str)
                for key, value in selector_labels.items()
            ),
            "selected service Deployment selector is malformed",
        )
        selector = ",".join(f"{key}={value}" for key, value in sorted(selector_labels.items()))
        pods = self.json(
            self.service_namespace,
            "get",
            "pods",
            "-l",
            selector,
        ).get("items", [])
        require(
            len(pods) == desired, "selected service Pod inventory differs from desired replicas"
        )
        identities = []
        for pod in pods:
            statuses = pod.get("status", {}).get("containerStatuses", [])
            require(
                len(statuses) == 1
                and statuses[0].get("ready") is True
                and statuses[0].get("imageID"),
                "selected service Pod is not ready or lacks immutable image identity",
            )
            identities.append(
                {
                    "pod_uid": pod.get("metadata", {}).get("uid"),
                    "image_id": statuses[0]["imageID"],
                }
            )
        return {"settings": settings, "pods": identities}

    def monitoring_assets(self) -> dict[str, Any]:
        rule_name = prometheus_rule_resource(self.assets)["metadata"]["name"]
        rule = self.json(self.monitoring_namespace, "get", "prometheusrule", rule_name)
        alerts = {
            item.get("alert")
            for group in rule.get("spec", {}).get("groups", [])
            for item in group.get("rules", [])
            if item.get("alert")
        }
        expected_alerts = set(self.assets.contract.alerts.durations)
        require(
            alerts == expected_alerts,
            "live selected-service alert inventory differs from reviewed assets",
        )
        dashboard_name = grafana_dashboard_resource(self.assets)["metadata"]["name"]
        dashboard = self.json(self.monitoring_namespace, "get", "configmap", dashboard_name)
        documents = dashboard.get("data", {})
        require(
            isinstance(documents, dict) and len(documents) == 1,
            "live dashboard ConfigMap is malformed",
        )
        try:
            rendered = json.loads(next(iter(documents.values())))
        except (TypeError, json.JSONDecodeError) as error:
            raise VerificationError("live dashboard ConfigMap contains malformed JSON") from error
        require(
            rendered.get("uid") == self.assets.contract.dashboard.uid
            and rendered.get("editable") is False,
            "live selected-service dashboard identity or immutability differs",
        )
        return {"prometheus_rule": rule_name, "dashboard_config_map": dashboard_name}


@contextmanager
def forwards(
    target: KubernetesTarget, services: Sequence[tuple[str, str, str, int]]
) -> Iterator[dict[str, int]]:
    children: list[subprocess.Popen[str]] = []
    ports: dict[str, int] = {}
    try:
        for label, namespace, service, remote_port in services:
            with socket.socket() as reservation:
                reservation.bind(("127.0.0.1", 0))
                local_port = reservation.getsockname()[1]
            child = subprocess.Popen(
                [
                    "kubectl",
                    "--context",
                    target.context,
                    "--namespace",
                    namespace,
                    "port-forward",
                    "--address",
                    "127.0.0.1",
                    f"service/{service}",
                    f"{local_port}:{remote_port}",
                ],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                text=True,
                start_new_session=True,
            )
            children.append(child)
            deadline = time.monotonic() + FORWARD_TIMEOUT
            while time.monotonic() < deadline:
                require(child.poll() is None, f"port-forward for {label} exited")
                try:
                    with socket.create_connection(("127.0.0.1", local_port), timeout=0.2):
                        ports[label] = local_port
                        break
                except OSError:
                    time.sleep(0.1)
            require(label in ports, f"port-forward for {label} timed out")
        yield ports
    finally:
        primary = sys.exc_info()[1]
        errors: list[Exception] = []
        with monitoring_runtime.teardown_activity(45, resume_work=primary is None):
            for child in reversed(children):
                try:
                    remaining = monitoring_runtime.teardown_timeout(10)
                    monitoring_runtime.terminate_process_group(
                        child,
                        graceful_seconds=min(5, remaining / 2),
                        teardown_seconds=remaining,
                    )
                except Exception as error:
                    errors.append(error)
        if errors:
            if primary is not None:
                primary.add_note(f"port-forward cleanup also failed: {errors}")
            else:
                raise VerificationError(f"port-forward cleanup failed: {errors}")


def product_reads(ports: Mapping[str, int], trace_id: str) -> int:
    require(
        request_text(f"http://127.0.0.1:{ports['health']}/livez").strip() == "live",
        "Product liveness failed",
    )
    require(
        request_text(f"http://127.0.0.1:{ports['health']}/readyz").strip() == "ready",
        "Product readiness failed",
    )
    body = request_text(
        f"http://127.0.0.1:{ports['product']}/products",
        {"traceparent": f"00-{trace_id}-0123456789abcdef-01"},
    )
    try:
        products = json.loads(body)
    except json.JSONDecodeError as error:
        raise VerificationError("Product list response is malformed") from error
    require(isinstance(products, list) and products, "Product list response is empty")
    return len(products)


def product_services(target: KubernetesTarget) -> list[tuple[str, str, str, int]]:
    require(target.config.management_service is not None, "Product management service is required")
    return [
        ("product", target.service_namespace, target.config.business_service, 8081),
        ("health", target.service_namespace, target.config.management_service, 8080),
    ]


def query_results(value: Mapping[str, Any]) -> list[dict[str, Any]]:
    results = value.get("data", {}).get("result", [])
    require(isinstance(results, list), "backend query result is malformed")
    return results


def wait_for(description: str, probe: Any, timeout: float = QUERY_TIMEOUT) -> Any:
    deadline = time.monotonic() + timeout
    last: Any = None
    while time.monotonic() < deadline:
        try:
            last = probe()
            if last:
                return last
        except VerificationError:
            pass
        time.sleep(1)
    raise VerificationError(f"timed out waiting for {description}")


def verify_product_enabled_signals(
    assets: MonitoringAssets, ports: Mapping[str, int], trace_id: str, pod_uid: str
) -> dict[str, Any]:
    expected = assets.contract.verification.expected_signals
    require(
        len(expected.metrics) == 1
        and len(expected.logs) == 1
        and expected.traces == (assets.contract.service_name,),
        "Product expected signal inventory differs from its registered probe",
    )
    metric_query = (
        f'{expected.metrics[0]}{{service_name="{assets.contract.service_name}",'
        'deployment_environment_name="qa",service_implementation="go",'
        f'service_instance_id="{pod_uid}",insurance_hub_product_transport="http",'
        'insurance_hub_product_operation="list"}'
    )
    metric_url = f"http://127.0.0.1:{ports['prometheus']}/api/v1/query?" + urllib.parse.urlencode(
        {"query": metric_query}
    )
    metric = wait_for(
        "Product request metric",
        lambda: query_results(request_json(metric_url)),
    )
    now = time.time_ns()
    loki_query = (
        f'{{service_name="{assets.contract.service_name}",deployment_environment_name="qa",'
        f'service_implementation="go"}} | json | {expected.logs[0]}=' + json.dumps(trace_id)
    )
    loki_url = (
        f"http://127.0.0.1:{ports['loki']}/loki/api/v1/query_range?"
        + urllib.parse.urlencode(
            {
                "query": loki_query,
                "start": now - 300_000_000_000,
                "end": now + 30_000_000_000,
                "limit": 20,
            }
        )
    )
    logs = wait_for(
        "correlated Product completion log",
        lambda: query_results(request_json(loki_url)),
    )
    trace_url = f"http://127.0.0.1:{ports['tempo']}/api/traces/{trace_id}"
    trace = wait_for(
        "correlated Product trace",
        lambda: request_json(trace_url).get("batches"),
    )
    alert_query = (
        f'count(ALERTS{{service="{assets.contract.service_name}",alertstate="firing"}}) '
        "or vector(0)"
    )
    alert_url = f"http://127.0.0.1:{ports['prometheus']}/api/v1/query?" + urllib.parse.urlencode(
        {"query": alert_query}
    )
    alerts = query_results(request_json(alert_url))
    require(
        len(alerts) == 1 and float(alerts[0]["value"][1]) == 0,
        "a selected-service pilot alert is firing",
    )
    return {
        "metric_series": len(metric),
        "log_streams": len(logs),
        "trace_batches": len(trace),
        "firing_alerts": 0,
    }


@dataclass(frozen=True)
class ServiceProbe:
    """Service-owned workload and signal checks behind generic target selection."""

    service: str
    services: Callable[[KubernetesTarget], list[tuple[str, str, str, int]]]
    workload: Callable[[Mapping[str, int], str], int]
    enabled_signals: Callable[[MonitoringAssets, Mapping[str, int], str, str], dict[str, Any]]
    result_key: str


SERVICE_PROBES = {
    "product": ServiceProbe(
        service="product",
        services=product_services,
        workload=product_reads,
        enabled_signals=verify_product_enabled_signals,
        result_key="product_rows",
    )
}


def select_probe(assets: MonitoringAssets) -> ServiceProbe:
    key = assets.contract.verification.probe
    require(key == assets.service, "service probe selection differs from the selected service")
    probe = SERVICE_PROBES.get(key)
    require(probe is not None, f"service probe {key!r} is not registered")
    require(probe.service == assets.service, "registered probe belongs to another service")
    return probe


def execute(args: argparse.Namespace) -> dict[str, Any]:
    assets = load_assets(args.assets_root, args.environment, args.service)
    probe = select_probe(assets)
    target = KubernetesTarget(assets, args.source_revision)
    target.coordinates()
    revision = target.flux_revision()
    deployment = target.deployment(args.mode)
    monitoring: dict[str, Any] = {}
    if args.mode == "enabled":
        monitoring = target.monitoring_assets()
    services = probe.services(target)
    if args.mode == "enabled":
        services.extend(
            [
                (
                    "prometheus",
                    target.monitoring_namespace,
                    target.config.prometheus_service,
                    9090,
                ),
                ("loki", target.monitoring_namespace, target.config.loki_service, 3100),
                ("tempo", target.monitoring_namespace, target.config.tempo_service, 3200),
            ]
        )
    trace_id = hashlib.sha256(f"{args.source_revision}:{time.time_ns()}".encode()).hexdigest()[:32]
    with forwards(target, services) as ports:
        workload_result = probe.workload(ports, trace_id)
        signals: dict[str, Any] = {}
        if args.mode == "enabled":
            pod_uid = deployment["pods"][0]["pod_uid"]
            require(isinstance(pod_uid, str) and pod_uid, "selected service Pod UID is unavailable")
            signals = probe.enabled_signals(assets, ports, trace_id, pod_uid)
    evidence = {
        "mode": args.mode,
        "source_revision": args.source_revision,
        "flux_revision": revision,
        "trace_id": trace_id,
        "pods": deployment["pods"],
        "monitoring": monitoring,
        "signals": signals,
    }
    evidence[probe.result_key] = workload_result
    return evidence


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--assets-root", type=Path, required=True)
    parser.add_argument("--environment", required=True)
    parser.add_argument("--service", required=True)
    parser.add_argument("--mode", choices=("disabled", "enabled"), required=True)
    parser.add_argument("--source-revision", required=True)
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        with (
            monitoring_runtime.controlled_signals(),
            monitoring_runtime.bounded_activity(f"{args.service} observability verification", 600),
        ):
            evidence = execute(args)
    except monitoring_runtime.Interrupted as error:
        print(f"Service observability verification {error}", file=sys.stderr)
        for note in getattr(error, "__notes__", ()):
            print(note, file=sys.stderr)
        return error.status
    except (
        AssetError,
        monitoring_runtime.DeadlineExceeded,
        monitoring_runtime.TeardownExceeded,
        VerificationError,
        KeyError,
        TypeError,
        ValueError,
    ) as error:
        print(f"Service observability verification failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps(evidence, indent=2, sort_keys=True))
    print(f"{args.service} observability verification passed", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
