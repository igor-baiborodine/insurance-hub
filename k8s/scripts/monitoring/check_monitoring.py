#!/usr/bin/env python3
"""Validate external dashboard and alert assets for any configured service."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any, Dict, List, Optional, Sequence

from monitoring_assets import AssetError, MonitoringAssets, load_assets


KUBE_PROMETHEUS_STACK_VERSION = "92.6.0"
PROMETHEUS_IMAGE = "quay.io/prometheus/prometheus:v3.15.0-distroless"


class ValidationError(Exception):
    """A monitoring asset contract validation failed."""


def prometheus_targets(dashboard: Dict[str, Any]) -> List[Dict[str, Any]]:
    targets: List[Dict[str, Any]] = []
    for panel in dashboard.get("panels", []):
        if not isinstance(panel, dict):
            continue
        datasource = panel.get("datasource", {})
        if isinstance(datasource, dict) and datasource.get("type") == "prometheus":
            targets.extend(
                target for target in panel.get("targets", []) if isinstance(target, dict)
            )
    return targets


def validate_dashboard(assets: MonitoringAssets) -> None:
    dashboard = assets.dashboard
    config = assets.contract["dashboard"]
    if dashboard.get("uid") != config.get("uid"):
        raise ValidationError(f"dashboard UID must be {config.get('uid')!r}")
    if dashboard.get("refresh") != config.get("refresh"):
        raise ValidationError(f"dashboard refresh must be {config.get('refresh')!r}")
    if config.get("editable") is not False or dashboard.get("editable") is not False:
        raise ValidationError("dashboard and monitoring.json must disable UI editing")

    expected_variables = config.get("variables")
    variables = {
        variable.get("name"): variable
        for variable in dashboard.get("templating", {}).get("list", [])
        if isinstance(variable, dict)
    }
    if not isinstance(expected_variables, dict) or set(variables) != set(expected_variables):
        raise ValidationError("dashboard variables do not match monitoring.json")
    selector = config.get("application_selector")
    if not isinstance(selector, str) or not selector:
        raise ValidationError("dashboard application_selector must be configured")
    for name, label in expected_variables.items():
        query_config = variables[name].get("query", {})
        query = query_config.get("query", "") if isinstance(query_config, dict) else query_config
        query = f"{variables[name].get('definition', '')} {query}"
        if label not in query or selector not in query:
            raise ValidationError(f"dashboard variable {name!r} lacks {label!r} or {selector!r}")

    panels = dashboard.get("panels", [])
    titles = {panel.get("title") for panel in panels if isinstance(panel, dict)}
    expected_titles = set(config.get("required_panel_titles", []))
    if titles != expected_titles:
        raise ValidationError(
            f"dashboard panels are {sorted(titles)}, expected {sorted(expected_titles)}"
        )

    targets = prometheus_targets(dashboard)
    minimum_targets = config.get("minimum_prometheus_targets")
    if not isinstance(minimum_targets, int) or len(targets) < minimum_targets:
        raise ValidationError(
            f"dashboard has {len(targets)} Prometheus targets, expected at least {minimum_targets}"
        )
    required_filters = config.get("required_query_filters", [])
    if not isinstance(required_filters, list) or not all(
        isinstance(value, str) and value for value in required_filters
    ):
        raise ValidationError("dashboard required_query_filters must be non-empty strings")
    for target in targets:
        expression = target.get("expr", "")
        if selector in expression:
            missing = [value for value in required_filters if value not in expression]
            if missing:
                raise ValidationError(
                    f"application query lacks required filters {missing}: {expression}"
                )

    rendered = json.dumps(dashboard, sort_keys=True)
    for marker in config.get("required_markers", []):
        if marker not in rendered:
            raise ValidationError(f"dashboard lacks required marker {marker!r}")


def alert_blocks(rules: str) -> Dict[str, str]:
    matches = list(re.finditer(r"(?m)^    - alert: ([A-Za-z0-9_]+)\s*$", rules))
    blocks: Dict[str, str] = {}
    for index, match in enumerate(matches):
        end = matches[index + 1].start() if index + 1 < len(matches) else len(rules)
        blocks[match.group(1)] = rules[match.start() : end]
    return blocks


def validate_alerts(assets: MonitoringAssets) -> None:
    rules = assets.alerts_path.read_text(encoding="utf-8")
    config = assets.contract["alerts"]
    if assets.alerts_path.suffix not in {".yaml", ".yml"}:
        raise ValidationError("alert rules must use a .yaml or .yml file")
    if not rules.startswith("groups:\n") or re.search(r"(?m)^kind:\s*PrometheusRule\s*$", rules):
        raise ValidationError("alert rules must be a standalone Prometheus rule file")
    if f"name: {config.get('group')}" not in rules:
        raise ValidationError("alert rule group does not match monitoring.json")
    if f"interval: {config.get('interval')}" not in rules:
        raise ValidationError("alert rule interval does not match monitoring.json")

    expected_for = config.get("for")
    markers = config.get("expression_markers")
    expected_labels = config.get("labels")
    required_annotations = config.get("required_annotations")
    if not all(isinstance(value, dict) for value in (expected_for, markers, expected_labels)):
        raise ValidationError("alert inventory, markers, and labels must be objects")
    if not isinstance(required_annotations, list):
        raise ValidationError("required alert annotations must be a list")

    blocks = alert_blocks(rules)
    if set(blocks) != set(expected_for):
        raise ValidationError(
            f"alerts are {sorted(blocks)}, expected {sorted(expected_for)}"
        )
    if set(markers) != set(expected_for):
        raise ValidationError("expression marker inventory does not match alert inventory")

    for name, duration in expected_for.items():
        block = blocks[name]
        for label, value in expected_labels.items():
            if not re.search(
                rf"(?m)^        {re.escape(label)}:\s*{re.escape(str(value))}\s*$", block
            ):
                raise ValidationError(f"{name} lacks label {label}={value}")
        for annotation in required_annotations:
            if not re.search(rf"(?m)^        {re.escape(annotation)}:\s*", block):
                raise ValidationError(f"{name} lacks annotation {annotation}")
        match = re.search(r"(?m)^      for:\s*(\S+)\s*$", block)
        actual_duration = match.group(1) if match else None
        if actual_duration != duration:
            raise ValidationError(
                f"{name} for={actual_duration!r}, expected {duration!r}"
            )
        for marker in markers[name]:
            if marker not in block:
                raise ValidationError(f"{name} expression lacks {marker!r}")


def validate_fixture(assets: MonitoringAssets) -> None:
    fixture = assets.fixture
    if fixture.get("schema_version") != 1:
        raise ValidationError("metric fixture schema_version must be 1")
    common_labels = fixture.get("common_labels")
    series = fixture.get("series")
    if not isinstance(common_labels, dict) or not isinstance(series, list) or not series:
        raise ValidationError("metric fixture requires common_labels and non-empty series")
    for index, item in enumerate(series):
        if not isinstance(item, dict) or not isinstance(item.get("name"), str):
            raise ValidationError(f"metric fixture series {index} has no metric name")
        if not isinstance(item.get("labels"), dict) or "value" not in item:
            raise ValidationError(f"metric fixture series {index} lacks labels or value")


def validate_values(values: str) -> None:
    for marker in (
        "defaultRules:\n  create: true",
        "grafana:\n  enabled: true",
        "defaultDashboardsEnabled: true",
        "label: grafana_dashboard",
        'labelValue: "1"',
        "disableDelete: true",
        "allowUiUpdates: false",
        "  persistence:\n    enabled: true\n    type: pvc",
    ):
        if marker not in values:
            raise ValidationError(f"Prometheus values lack {marker!r}")
    for forbidden in ("additionalPrometheusRulesMap:", "  dashboards:\n    default:"):
        if forbidden in values:
            raise ValidationError(f"Prometheus values still embed service assets: {forbidden!r}")


def validate_makefile(makefile: str) -> None:
    for marker in (
        f"KUBE_PROMETHEUS_STACK_VERSION := {KUBE_PROMETHEUS_STACK_VERSION}",
        f"PROMETHEUS_IMAGE := {PROMETHEUS_IMAGE}",
        "monitoring-assets-check:",
        "grafana-dashboard-render:",
        "grafana-dashboard-apply:",
        "prometheus-rules-apply:",
        "test-monitoring:",
    ):
        if marker not in makefile:
            raise ValidationError(f"k8s Makefile lacks {marker!r}")


def validate_rendered_stack(rendered: str) -> None:
    documents = [
        document for document in re.split(r"(?m)^---\s*$", rendered) if document.strip()
    ]
    if len([document for document in documents if "kind: PrometheusRule" in document]) < 10:
        raise ValidationError("base chart render removed the default Prometheus rules")
    dashboard_keys = re.findall(r"(?m)^  [a-z0-9-]+\.json:\s*(?:\|-)?\s*$", rendered)
    if len(dashboard_keys) < 20:
        raise ValidationError("base chart render removed the default Grafana dashboards")
    grafana_claims = [
        document
        for document in documents
        if "kind: PersistentVolumeClaim" in document
        and "app.kubernetes.io/name: grafana" in document
    ]
    if len(grafana_claims) != 1:
        raise ValidationError(
            f"base chart must render one persistent Grafana claim, found {len(grafana_claims)}"
        )
    if "disableDeletion: true\n        allowUiUpdates: false" not in rendered:
        raise ValidationError(
            "Grafana dashboard provider must disable deletion and UI updates"
        )


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--assets-root", type=Path, required=True)
    parser.add_argument("--environment", required=True)
    parser.add_argument("--service", required=True)
    parser.add_argument("--values", type=Path, required=True)
    parser.add_argument("--makefile", type=Path, required=True)
    parser.add_argument("--rendered-stack", type=Path, required=True)
    return parser.parse_args(argv)


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = parse_args(argv)
    try:
        assets = load_assets(args.assets_root, args.environment, args.service)
        validate_dashboard(assets)
        validate_alerts(assets)
        validate_fixture(assets)
        validate_values(args.values.read_text(encoding="utf-8"))
        validate_makefile(args.makefile.read_text(encoding="utf-8"))
        validate_rendered_stack(args.rendered_stack.read_text(encoding="utf-8"))
    except (AssetError, OSError, ValidationError) as error:
        print(f"Monitoring asset check failed: {error}", file=sys.stderr)
        return 1
    print(
        f"Monitoring assets passed contract checks for {args.service} in {args.environment}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
