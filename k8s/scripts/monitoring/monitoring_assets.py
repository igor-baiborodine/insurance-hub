#!/usr/bin/env python3
"""Resolve versioned service monitoring assets and environment configuration safely."""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any

IDENTIFIER = re.compile(r"^[a-z][a-z0-9-]*$")
SIGNAL_IDENTIFIER = re.compile(r"^[A-Za-z][A-Za-z0-9_.:-]*$")
DEPENDENCY_CAPABILITIES = frozenset({"elasticsearch", "postgresql"})
WORKER_CAPABILITIES = frozenset({"events"})
COMMON_OUTCOMES = frozenset(
    {
        "requests",
        "runtime",
        "pod_resources",
        "availability",
        "telemetry_delivery",
        "investigation",
    }
)
VARIABLE_LABELS = {
    "environment": "deployment_environment_name",
    "implementation": "service_implementation",
}


class AssetError(Exception):
    """A monitoring asset configuration or path is invalid."""


@dataclass(frozen=True)
class Capabilities:
    """Optional operational capabilities declared by a service."""

    dependencies: tuple[str, ...]
    workers: tuple[str, ...]


@dataclass(frozen=True)
class OutcomeInventory:
    """Service-owned panel or alert names grouped by applicable capability."""

    common: dict[str, tuple[str, ...]] | tuple[str, ...]
    dependencies: dict[str, tuple[str, ...]]
    workers: dict[str, tuple[str, ...]]

    def values(self) -> tuple[str, ...]:
        common = (
            tuple(item for values in self.common.values() for item in values)
            if isinstance(self.common, dict)
            else self.common
        )
        capabilities = tuple(
            item
            for group in (self.dependencies, self.workers)
            for values in group.values()
            for item in values
        )
        return (*common, *capabilities)


@dataclass(frozen=True)
class DashboardContract:
    """Validated dashboard contract for one service."""

    path: str
    uid: str
    editable: bool
    refresh: str
    minimum_prometheus_targets: int
    variables: dict[str, str]
    application_selector: str
    required_query_filters: tuple[str, ...]
    required_markers: tuple[str, ...]
    outcomes: OutcomeInventory


@dataclass(frozen=True)
class AlertsContract:
    """Validated Prometheus rule contract for one service."""

    path: str
    test_path: str
    prometheus_rule_name: str
    group_name: str
    interval: str
    common_labels: dict[str, str]
    required_annotations: tuple[str, ...]
    inventory: OutcomeInventory
    durations: dict[str, str | None]
    expression_markers: dict[str, tuple[str, ...]]


@dataclass(frozen=True)
class FixtureContract:
    """Collected Prometheus fixture requirements."""

    path: str
    minimum_scrapes_per_series: int


@dataclass(frozen=True)
class ExpectedSignals:
    """Bounded service signals required by live verification."""

    metrics: tuple[str, ...]
    logs: tuple[str, ...]
    traces: tuple[str, ...]


@dataclass(frozen=True)
class VerificationContract:
    """Service-owned live verification selection."""

    probe: str
    expected_signals: ExpectedSignals


@dataclass(frozen=True)
class ServiceMonitoring:
    """Version 2 monitoring contract for one service."""

    schema_version: int
    service: str
    service_name: str
    supported_environments: tuple[str, ...]
    capabilities: Capabilities
    dashboard: DashboardContract
    alerts: AlertsContract
    fixture: FixtureContract
    acceptance_path: str
    verification: VerificationContract


@dataclass(frozen=True)
class ServiceVerification:
    """Environment-owned live coordinates for one service."""

    flux_kustomization: str
    flux_namespace: str
    flux_source: str
    service_namespace: str
    deployment: str
    business_service: str
    management_service: str | None
    prometheus_service: str
    loki_service: str
    tempo_service: str


@dataclass(frozen=True)
class EnvironmentMonitoring:
    """Version 2 monitoring environment and its service coordinates."""

    schema_version: int
    environment: str
    kube_context: str
    monitoring_namespace: str
    prometheus_release: str
    prometheus_values_path: str
    service_verification: dict[str, ServiceVerification]


@dataclass(frozen=True)
class MonitoringAssets:
    """Resolved monitoring assets for one service in one environment."""

    assets_root: Path
    service_dir: Path
    environment_name: str
    service: str
    environment: EnvironmentMonitoring
    contract: ServiceMonitoring
    dashboard: dict[str, Any]
    dashboard_path: Path
    alerts_path: Path
    alert_tests_path: Path
    fixture: dict[str, Any]
    fixture_path: Path
    acceptance: dict[str, Any]
    acceptance_path: Path


def require_identifier(value: str, label: str) -> None:
    if not IDENTIFIER.fullmatch(value):
        raise AssetError(f"{label} must match {IDENTIFIER.pattern!r}: {value!r}")


def load_json_object(path: Path, label: str) -> dict[str, Any]:
    try:
        parsed = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise AssetError(f"cannot load {label} from {path}: {error}") from error
    if not isinstance(parsed, dict):
        raise AssetError(f"{label} must be a JSON object: {path}")
    return parsed


def require_object(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise AssetError(f"{label} must be an object")
    return value


def require_fields(mapping: dict[str, Any], expected: set[str], label: str) -> None:
    actual = set(mapping)
    if actual != expected:
        missing = sorted(expected - actual)
        unknown = sorted(actual - expected)
        raise AssetError(f"{label} fields differ: missing={missing}, unknown={unknown}")


def require_text(mapping: dict[str, Any], key: str, label: str) -> str:
    value = mapping.get(key)
    if not isinstance(value, str) or not value:
        raise AssetError(f"{label}.{key} must be a non-empty string")
    return value


def require_optional_text(mapping: dict[str, Any], key: str, label: str) -> str | None:
    value = mapping.get(key)
    if value is not None and (not isinstance(value, str) or not value):
        raise AssetError(f"{label}.{key} must be null or a non-empty string")
    return value


def require_bool(mapping: dict[str, Any], key: str, label: str) -> bool:
    value = mapping.get(key)
    if type(value) is not bool:
        raise AssetError(f"{label}.{key} must be a boolean")
    return value


def require_positive_int(mapping: dict[str, Any], key: str, label: str) -> int:
    value = mapping.get(key)
    if type(value) is not int or value < 1:
        raise AssetError(f"{label}.{key} must be a positive integer")
    return value


def require_text_list(value: Any, label: str, *, allow_empty: bool = False) -> tuple[str, ...]:
    if not isinstance(value, list) or (not value and not allow_empty):
        suffix = "" if allow_empty else " and non-empty"
        raise AssetError(f"{label} must be an array{suffix}")
    if not all(isinstance(item, str) and item for item in value):
        raise AssetError(f"{label} entries must be non-empty strings")
    if len(value) != len(set(value)):
        raise AssetError(f"{label} entries must be unique")
    return tuple(value)


def require_text_map(value: Any, label: str) -> dict[str, str]:
    mapping = require_object(value, label)
    if not mapping or not all(isinstance(item, str) and item for item in mapping.values()):
        raise AssetError(f"{label} values must be non-empty strings")
    return dict(mapping)


def resolve_child(parent: Path, value: str, label: str, suffixes: set[str]) -> Path:
    relative = Path(value)
    if relative.is_absolute() or len(relative.parts) != 1 or relative.name in {"", ".", ".."}:
        raise AssetError(f"{label} must be a direct relative child path: {value!r}")
    if relative.suffix not in suffixes:
        raise AssetError(f"{label} must use one of {sorted(suffixes)}: {value!r}")
    candidate = (parent / relative).resolve()
    resolved_parent = parent.resolve()
    if candidate == resolved_parent or resolved_parent not in candidate.parents:
        raise AssetError(f"{label} escapes {resolved_parent}: {value!r}")
    if not candidate.is_file():
        raise AssetError(f"{label} does not exist: {candidate}")
    return candidate


def parse_capabilities(value: Any) -> Capabilities:
    label = "service contract.capabilities"
    mapping = require_object(value, label)
    require_fields(mapping, {"dependencies", "workers"}, label)
    dependencies = require_text_list(
        mapping["dependencies"], f"{label}.dependencies", allow_empty=True
    )
    workers = require_text_list(mapping["workers"], f"{label}.workers", allow_empty=True)
    unknown_dependencies = set(dependencies) - DEPENDENCY_CAPABILITIES
    unknown_workers = set(workers) - WORKER_CAPABILITIES
    if unknown_dependencies or unknown_workers:
        raise AssetError(
            "service contract has unknown capabilities: "
            f"dependencies={sorted(unknown_dependencies)}, workers={sorted(unknown_workers)}"
        )
    return Capabilities(dependencies=dependencies, workers=workers)


def parse_capability_inventory(
    value: Any,
    label: str,
    capabilities: Capabilities,
    *,
    common_dashboard: bool,
) -> OutcomeInventory:
    mapping = require_object(value, label)
    require_fields(mapping, {"common", "dependencies", "workers"}, label)
    if common_dashboard:
        common_value = require_object(mapping["common"], f"{label}.common")
        require_fields(common_value, set(COMMON_OUTCOMES), f"{label}.common")
        common: dict[str, tuple[str, ...]] | tuple[str, ...] = {
            key: require_text_list(common_value[key], f"{label}.common.{key}")
            for key in sorted(COMMON_OUTCOMES)
        }
    else:
        common = require_text_list(mapping["common"], f"{label}.common")

    dependencies_value = require_object(mapping["dependencies"], f"{label}.dependencies")
    workers_value = require_object(mapping["workers"], f"{label}.workers")
    if set(dependencies_value) != set(capabilities.dependencies):
        raise AssetError(f"{label}.dependencies must exactly match declared dependencies")
    if set(workers_value) != set(capabilities.workers):
        raise AssetError(f"{label}.workers must exactly match declared workers")
    dependencies = {
        key: require_text_list(values, f"{label}.dependencies.{key}")
        for key, values in dependencies_value.items()
    }
    workers = {
        key: require_text_list(values, f"{label}.workers.{key}")
        for key, values in workers_value.items()
    }
    inventory = OutcomeInventory(common=common, dependencies=dependencies, workers=workers)
    values = inventory.values()
    if len(values) != len(set(values)):
        raise AssetError(f"{label} contains a duplicate member across outcome groups")
    return inventory


def parse_dashboard(value: Any, service_name: str, capabilities: Capabilities) -> DashboardContract:
    label = "service contract.dashboard"
    mapping = require_object(value, label)
    require_fields(
        mapping,
        {
            "path",
            "uid",
            "editable",
            "refresh",
            "minimum_prometheus_targets",
            "variables",
            "application_selector",
            "required_query_filters",
            "required_markers",
            "outcomes",
        },
        label,
    )
    variables = require_text_map(mapping["variables"], f"{label}.variables")
    if variables != VARIABLE_LABELS:
        raise AssetError(
            "service contract.dashboard.variables must declare only environment and implementation"
        )
    selector = require_text(mapping, "application_selector", label)
    if selector != f'service_name="{service_name}"':
        raise AssetError(
            "service contract.dashboard.application_selector differs from service_name"
        )
    expected_filters = {
        'deployment_environment_name=~"$environment"',
        'service_implementation=~"$implementation"',
    }
    required_filters = require_text_list(
        mapping["required_query_filters"], f"{label}.required_query_filters"
    )
    if set(required_filters) != expected_filters:
        raise AssetError(
            "service contract.dashboard.required_query_filters are unsafe or incomplete"
        )
    return DashboardContract(
        path=require_text(mapping, "path", label),
        uid=require_text(mapping, "uid", label),
        editable=require_bool(mapping, "editable", label),
        refresh=require_text(mapping, "refresh", label),
        minimum_prometheus_targets=require_positive_int(
            mapping, "minimum_prometheus_targets", label
        ),
        variables=variables,
        application_selector=selector,
        required_query_filters=required_filters,
        required_markers=require_text_list(
            mapping["required_markers"], f"{label}.required_markers"
        ),
        outcomes=parse_capability_inventory(
            mapping["outcomes"], f"{label}.outcomes", capabilities, common_dashboard=True
        ),
    )


def parse_alerts(value: Any, service_name: str, capabilities: Capabilities) -> AlertsContract:
    label = "service contract.alerts"
    mapping = require_object(value, label)
    require_fields(
        mapping,
        {
            "path",
            "test_path",
            "prometheus_rule_name",
            "group_name",
            "interval",
            "common_labels",
            "required_annotations",
            "inventory",
            "for",
            "expression_markers",
        },
        label,
    )
    inventory = parse_capability_inventory(
        mapping["inventory"], f"{label}.inventory", capabilities, common_dashboard=False
    )
    names = set(inventory.values())
    durations_value = require_object(mapping["for"], f"{label}.for")
    if set(durations_value) != names or not all(
        value is None or isinstance(value, str) and value for value in durations_value.values()
    ):
        raise AssetError(f"{label}.for must exactly describe the alert inventory")
    marker_value = require_object(mapping["expression_markers"], f"{label}.expression_markers")
    if set(marker_value) != names:
        raise AssetError(f"{label}.expression_markers must exactly describe the alert inventory")
    markers = {
        name: require_text_list(values, f"{label}.expression_markers.{name}")
        for name, values in marker_value.items()
    }
    common_labels = require_text_map(mapping["common_labels"], f"{label}.common_labels")
    if set(common_labels) != {"service", "severity"}:
        raise AssetError(f"{label}.common_labels may contain only service and severity")
    if common_labels["service"] != service_name:
        raise AssetError(f"{label}.common_labels.service differs from service_name")
    return AlertsContract(
        path=require_text(mapping, "path", label),
        test_path=require_text(mapping, "test_path", label),
        prometheus_rule_name=require_text(mapping, "prometheus_rule_name", label),
        group_name=require_text(mapping, "group_name", label),
        interval=require_text(mapping, "interval", label),
        common_labels=common_labels,
        required_annotations=require_text_list(
            mapping["required_annotations"], f"{label}.required_annotations"
        ),
        inventory=inventory,
        durations=dict(durations_value),
        expression_markers=markers,
    )


def parse_expected_signals(value: Any) -> ExpectedSignals:
    label = "service contract.verification.expected_signals"
    mapping = require_object(value, label)
    require_fields(mapping, {"metrics", "logs", "traces"}, label)
    parsed = {
        key: require_text_list(mapping[key], f"{label}.{key}")
        for key in ("metrics", "logs", "traces")
    }
    for key, values in parsed.items():
        invalid = [value for value in values if not SIGNAL_IDENTIFIER.fullmatch(value)]
        if invalid:
            raise AssetError(f"{label}.{key} contains unsafe identifiers: {invalid}")
    return ExpectedSignals(**parsed)


def parse_service_contract(document: dict[str, Any], expected_service: str) -> ServiceMonitoring:
    label = "service contract"
    require_fields(
        document,
        {
            "schema_version",
            "service",
            "service_name",
            "supported_environments",
            "capabilities",
            "dashboard",
            "alerts",
            "fixture",
            "acceptance",
            "verification",
        },
        label,
    )
    if document["schema_version"] != 2 or type(document["schema_version"]) is not int:
        raise AssetError("service contract schema_version must be 2")
    service = require_text(document, "service", label)
    require_identifier(service, "service contract.service")
    if service != expected_service:
        raise AssetError("service contract identity does not match the requested service")
    service_name = require_text(document, "service_name", label)
    require_identifier(service_name, "service contract.service_name")
    supported = require_text_list(
        document["supported_environments"], "service contract.supported_environments"
    )
    for environment in supported:
        require_identifier(environment, "supported environment")
    capabilities = parse_capabilities(document["capabilities"])

    fixture_value = require_object(document["fixture"], "service contract.fixture")
    require_fields(
        fixture_value, {"path", "minimum_scrapes_per_series"}, "service contract.fixture"
    )
    acceptance_value = require_object(document["acceptance"], "service contract.acceptance")
    require_fields(acceptance_value, {"path"}, "service contract.acceptance")
    verification_value = require_object(document["verification"], "service contract.verification")
    require_fields(
        verification_value, {"probe", "expected_signals"}, "service contract.verification"
    )
    probe = require_text(verification_value, "probe", "service contract.verification")
    require_identifier(probe, "service contract.verification.probe")
    if probe != service:
        raise AssetError("service contract.verification.probe must match the service key")

    return ServiceMonitoring(
        schema_version=2,
        service=service,
        service_name=service_name,
        supported_environments=supported,
        capabilities=capabilities,
        dashboard=parse_dashboard(document["dashboard"], service_name, capabilities),
        alerts=parse_alerts(document["alerts"], service_name, capabilities),
        fixture=FixtureContract(
            path=require_text(fixture_value, "path", "service contract.fixture"),
            minimum_scrapes_per_series=require_positive_int(
                fixture_value, "minimum_scrapes_per_series", "service contract.fixture"
            ),
        ),
        acceptance_path=require_text(acceptance_value, "path", "service contract.acceptance"),
        verification=VerificationContract(
            probe=probe,
            expected_signals=parse_expected_signals(verification_value["expected_signals"]),
        ),
    )


def parse_service_verification(value: Any, label: str) -> ServiceVerification:
    mapping = require_object(value, label)
    fields = {
        "flux_kustomization",
        "flux_namespace",
        "flux_source",
        "service_namespace",
        "deployment",
        "business_service",
        "management_service",
        "prometheus_service",
        "loki_service",
        "tempo_service",
    }
    require_fields(mapping, fields, label)
    values = {key: require_text(mapping, key, label) for key in fields - {"management_service"}}
    return ServiceVerification(
        **values,
        management_service=require_optional_text(mapping, "management_service", label),
    )


def parse_environment(document: dict[str, Any], expected_environment: str) -> EnvironmentMonitoring:
    label = "environment configuration"
    require_fields(
        document,
        {
            "schema_version",
            "environment",
            "kube_context",
            "monitoring_namespace",
            "prometheus_release",
            "prometheus_values_path",
            "service_verification",
        },
        label,
    )
    if document["schema_version"] != 2 or type(document["schema_version"]) is not int:
        raise AssetError("environment configuration schema_version must be 2")
    environment = require_text(document, "environment", label)
    if environment != expected_environment:
        raise AssetError(
            "environment configuration identity does not match the requested environment"
        )
    verification_value = require_object(
        document["service_verification"], "environment configuration.service_verification"
    )
    if not verification_value:
        raise AssetError("environment configuration.service_verification must not be empty")
    verification: dict[str, ServiceVerification] = {}
    for service, value in verification_value.items():
        require_identifier(service, "environment service verification key")
        verification[service] = parse_service_verification(
            value, f"environment configuration.service_verification.{service}"
        )
    return EnvironmentMonitoring(
        schema_version=2,
        environment=environment,
        kube_context=require_text(document, "kube_context", label),
        monitoring_namespace=require_text(document, "monitoring_namespace", label),
        prometheus_release=require_text(document, "prometheus_release", label),
        prometheus_values_path=require_text(document, "prometheus_values_path", label),
        service_verification=verification,
    )


def load_contract(service_dir: Path, service: str) -> ServiceMonitoring:
    path = resolve_child(service_dir, "monitoring.json", "service contract", {".json"})
    return parse_service_contract(load_json_object(path, "service contract"), service)


def validate_registry(assets_root: Path, selected: ServiceMonitoring | None = None) -> None:
    services_root = assets_root / "services"
    resolved_services_root = services_root.resolve()
    contracts: list[ServiceMonitoring] = []
    for service_dir in sorted(path for path in services_root.iterdir() if path.is_dir()):
        service = service_dir.name
        require_identifier(service, "service directory")
        resolved_service_dir = service_dir.resolve()
        if resolved_services_root not in resolved_service_dir.parents:
            raise AssetError(f"service asset directory escapes registry: {service_dir}")
        if selected is not None and service == selected.service:
            contracts.append(selected)
        else:
            contracts.append(load_contract(resolved_service_dir, service))

    identities: dict[str, dict[str, str]] = {
        "service_name": {},
        "dashboard UID": {},
        "PrometheusRule name": {},
        "rule group": {},
        "alert": {},
    }
    for contract in contracts:
        values = {
            "service_name": (contract.service_name,),
            "dashboard UID": (contract.dashboard.uid,),
            "PrometheusRule name": (contract.alerts.prometheus_rule_name,),
            "rule group": (contract.alerts.group_name,),
            "alert": contract.alerts.inventory.values(),
        }
        for label, candidates in values.items():
            for value in candidates:
                previous = identities[label].get(value)
                if previous is not None:
                    raise AssetError(
                        f"duplicate {label} {value!r} for services {previous!r} and "
                        f"{contract.service!r}"
                    )
                identities[label][value] = contract.service


def load_assets(assets_root: Path, environment_name: str, service: str) -> MonitoringAssets:
    require_identifier(environment_name, "environment")
    require_identifier(service, "service")
    root = assets_root.resolve()
    environment_path = resolve_child(
        root / "environments",
        f"{environment_name}.json",
        "environment configuration",
        {".json"},
    )
    service_dir = (root / "services" / service).resolve()
    services_root = (root / "services").resolve()
    if services_root not in service_dir.parents or not service_dir.is_dir():
        raise AssetError(f"service asset directory does not exist: {service_dir}")

    environment = parse_environment(
        load_json_object(environment_path, "environment configuration"), environment_name
    )
    values_path = (root.parent / environment.prometheus_values_path).resolve()
    if root.parent.resolve() not in values_path.parents or not values_path.is_file():
        raise AssetError(
            f"Prometheus values file does not exist under {root.parent}: {values_path}"
        )

    contract = load_contract(service_dir, service)
    validate_registry(root, contract)
    if environment_name not in contract.supported_environments:
        raise AssetError(f"service {service!r} does not support environment {environment_name!r}")
    if service not in environment.service_verification:
        raise AssetError(
            f"environment {environment_name!r} has no verification coordinates for service "
            f"{service!r}"
        )

    paths = {
        "dashboard": resolve_child(
            service_dir, contract.dashboard.path, "dashboard asset", {".json"}
        ),
        "alerts": resolve_child(
            service_dir, contract.alerts.path, "alert rules asset", {".yaml", ".yml"}
        ),
        "alert tests": resolve_child(
            service_dir,
            contract.alerts.test_path,
            "alert rule tests",
            {".yaml", ".yml"},
        ),
        "fixture": resolve_child(service_dir, contract.fixture.path, "metric fixture", {".json"}),
        "acceptance": resolve_child(
            service_dir, contract.acceptance_path, "acceptance policy", {".json"}
        ),
    }
    if len(paths.values()) != len(set(paths.values())):
        raise AssetError("service contract asset paths must be distinct")

    return MonitoringAssets(
        assets_root=root,
        service_dir=service_dir,
        environment_name=environment_name,
        service=service,
        environment=environment,
        contract=contract,
        dashboard=load_json_object(paths["dashboard"], "dashboard"),
        dashboard_path=paths["dashboard"],
        alerts_path=paths["alerts"],
        alert_tests_path=paths["alert tests"],
        fixture=load_json_object(paths["fixture"], "metric fixture"),
        fixture_path=paths["fixture"],
        acceptance=load_json_object(paths["acceptance"], "acceptance policy"),
        acceptance_path=paths["acceptance"],
    )


def prometheus_rule_resource(assets: MonitoringAssets) -> dict[str, Any]:
    """Return metadata for a PrometheusRule; the YAML spec is appended by the renderer."""
    resource_name = f"{assets.environment_name}-{assets.contract.alerts.prometheus_rule_name}"
    require_identifier(resource_name, "PrometheusRule name")
    return {
        "apiVersion": "monitoring.coreos.com/v1",
        "kind": "PrometheusRule",
        "metadata": {
            "name": resource_name,
            "namespace": assets.environment.monitoring_namespace,
            "labels": {
                "app.kubernetes.io/managed-by": "insurance-hub-monitoring-assets",
                "app.kubernetes.io/part-of": "insurance-hub",
                "insurance-hub/service": assets.service,
                "release": assets.environment.prometheus_release,
            },
        },
    }


def grafana_dashboard_resource(assets: MonitoringAssets) -> dict[str, Any]:
    """Return metadata for a Grafana sidecar dashboard ConfigMap."""
    resource_name = f"{assets.environment_name}-{assets.service}-dashboard"
    require_identifier(resource_name, "Grafana dashboard ConfigMap name")
    return {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {
            "name": resource_name,
            "namespace": assets.environment.monitoring_namespace,
            "labels": {
                "app.kubernetes.io/managed-by": "insurance-hub-monitoring-assets",
                "app.kubernetes.io/part-of": "insurance-hub",
                "grafana_dashboard": "1",
                "insurance-hub/service": assets.service,
            },
        },
    }
