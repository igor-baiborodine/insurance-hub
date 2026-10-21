#!/usr/bin/env python3
"""Resolve service monitoring assets and environment configuration safely."""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any

IDENTIFIER = re.compile(r"^[a-z][a-z0-9-]*$")


class AssetError(Exception):
    """A monitoring asset configuration or path is invalid."""


@dataclass(frozen=True)
class MonitoringAssets:
    """Resolved monitoring assets for one service in one environment."""

    assets_root: Path
    service_dir: Path
    environment_name: str
    service: str
    environment: dict[str, Any]
    contract: dict[str, Any]
    dashboard: dict[str, Any]
    alerts_path: Path
    alert_tests_path: Path
    fixture: dict[str, Any]


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


def require_text(mapping: dict[str, Any], key: str, label: str) -> str:
    value = mapping.get(key)
    if not isinstance(value, str) or not value:
        raise AssetError(f"{label}.{key} must be a non-empty string")
    return value


def resolve_child(parent: Path, value: str, label: str) -> Path:
    candidate = (parent / value).resolve()
    resolved_parent = parent.resolve()
    if candidate == resolved_parent or resolved_parent not in candidate.parents:
        raise AssetError(f"{label} escapes {resolved_parent}: {value!r}")
    if not candidate.is_file():
        raise AssetError(f"{label} does not exist: {candidate}")
    return candidate


def load_assets(assets_root: Path, environment_name: str, service: str) -> MonitoringAssets:
    require_identifier(environment_name, "environment")
    require_identifier(service, "service")
    root = assets_root.resolve()
    environment_path = resolve_child(
        root / "environments", f"{environment_name}.json", "environment configuration"
    )
    service_dir = (root / "services" / service).resolve()
    services_root = (root / "services").resolve()
    if services_root not in service_dir.parents or not service_dir.is_dir():
        raise AssetError(f"service asset directory does not exist: {service_dir}")

    environment = load_json_object(environment_path, "environment configuration")
    contract_path = resolve_child(service_dir, "monitoring.json", "service contract")
    contract = load_json_object(contract_path, "service contract")

    if environment.get("schema_version") != 1:
        raise AssetError("environment configuration schema_version must be 1")
    if environment.get("environment") != environment_name:
        raise AssetError(
            "environment configuration identity does not match the requested environment"
        )
    for key in (
        "kube_context",
        "monitoring_namespace",
        "prometheus_release",
        "prometheus_values_path",
    ):
        require_text(environment, key, "environment configuration")
    resolve_child(
        root.parent,
        environment["prometheus_values_path"],
        "Prometheus values file",
    )

    if contract.get("schema_version") != 1:
        raise AssetError("service contract schema_version must be 1")
    if contract.get("service") != service:
        raise AssetError("service contract identity does not match the requested service")
    supported = contract.get("supported_environments")
    if not isinstance(supported, list) or environment_name not in supported:
        raise AssetError(f"service {service!r} does not support environment {environment_name!r}")
    require_text(contract, "service_name", "service contract")
    require_text(contract, "prometheus_rule_name", "service contract")

    dashboard_config = contract.get("dashboard")
    alerts_config = contract.get("alerts")
    fixture_config = contract.get("fixture")
    if not isinstance(dashboard_config, dict):
        raise AssetError("service contract.dashboard must be an object")
    if not isinstance(alerts_config, dict):
        raise AssetError("service contract.alerts must be an object")
    if not isinstance(fixture_config, dict):
        raise AssetError("service contract.fixture must be an object")

    dashboard_path = resolve_child(
        service_dir,
        require_text(dashboard_config, "path", "service contract.dashboard"),
        "dashboard asset",
    )
    alerts_path = resolve_child(
        service_dir,
        require_text(alerts_config, "path", "service contract.alerts"),
        "alert rules asset",
    )
    alert_tests_path = resolve_child(
        service_dir,
        require_text(alerts_config, "test_path", "service contract.alerts"),
        "alert rule tests",
    )
    fixture_path = resolve_child(
        service_dir,
        require_text(fixture_config, "path", "service contract.fixture"),
        "metric fixture",
    )

    return MonitoringAssets(
        assets_root=root,
        service_dir=service_dir,
        environment_name=environment_name,
        service=service,
        environment=environment,
        contract=contract,
        dashboard=load_json_object(dashboard_path, "dashboard"),
        alerts_path=alerts_path,
        alert_tests_path=alert_tests_path,
        fixture=load_json_object(fixture_path, "metric fixture"),
    )


def prometheus_rule_resource(assets: MonitoringAssets) -> dict[str, Any]:
    """Return metadata for a PrometheusRule; the YAML spec is appended by the renderer."""
    resource_name = f"{assets.environment_name}-{assets.contract['prometheus_rule_name']}"
    require_identifier(resource_name, "PrometheusRule name")
    return {
        "apiVersion": "monitoring.coreos.com/v1",
        "kind": "PrometheusRule",
        "metadata": {
            "name": resource_name,
            "namespace": assets.environment["monitoring_namespace"],
            "labels": {
                "app.kubernetes.io/managed-by": "insurance-hub-monitoring-assets",
                "app.kubernetes.io/part-of": "insurance-hub",
                "insurance-hub/service": assets.service,
                "release": assets.environment["prometheus_release"],
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
            "namespace": assets.environment["monitoring_namespace"],
            "labels": {
                "app.kubernetes.io/managed-by": "insurance-hub-monitoring-assets",
                "app.kubernetes.io/part-of": "insurance-hub",
                "grafana_dashboard": "1",
                "insurance-hub/service": assets.service,
            },
        },
    }
