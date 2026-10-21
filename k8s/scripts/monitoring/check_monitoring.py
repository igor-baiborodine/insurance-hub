#!/usr/bin/env python3
"""Validate external dashboard and alert assets for any configured service."""

from __future__ import annotations

import argparse
import json
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

import yaml

from monitoring_assets import AssetError, MonitoringAssets, load_assets

KUBE_PROMETHEUS_STACK_VERSION = "92.6.0"
PROMETHEUS_IMAGE = "quay.io/prometheus/prometheus:v3.15.0-distroless"


class ValidationError(Exception):
    """A monitoring asset contract validation failed."""


def mapping(value: Any, label: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise ValidationError(f"{label} must be an object")
    return value


def sequence(value: Any, label: str) -> list[Any]:
    if not isinstance(value, list):
        raise ValidationError(f"{label} must be an array")
    return value


def yaml_documents(text: str, label: str) -> list[Mapping[str, Any]]:
    try:
        loaded = list(yaml.safe_load_all(text))
    except yaml.YAMLError as error:
        raise ValidationError(f"{label} is malformed YAML: {error}") from error
    documents = [document for document in loaded if document is not None]
    if not documents or not all(isinstance(document, Mapping) for document in documents):
        raise ValidationError(f"{label} must contain YAML mapping documents")
    return documents


def prometheus_targets(dashboard: Mapping[str, Any]) -> list[Mapping[str, Any]]:
    targets: list[Mapping[str, Any]] = []
    for index, panel_value in enumerate(sequence(dashboard.get("panels"), "dashboard panels")):
        panel = mapping(panel_value, f"dashboard panel {index}")
        datasource = panel.get("datasource", {})
        if isinstance(datasource, Mapping) and datasource.get("type") == "prometheus":
            for target_index, target in enumerate(
                sequence(panel.get("targets"), f"dashboard panel {index} targets")
            ):
                targets.append(mapping(target, f"dashboard panel {index} target {target_index}"))
    return targets


def validate_dashboard(assets: MonitoringAssets) -> None:
    dashboard = mapping(assets.dashboard, "dashboard")
    config = mapping(assets.contract.get("dashboard"), "monitoring dashboard contract")
    if dashboard.get("uid") != config.get("uid"):
        raise ValidationError(f"dashboard UID must be {config.get('uid')!r}")
    if dashboard.get("refresh") != config.get("refresh"):
        raise ValidationError(f"dashboard refresh must be {config.get('refresh')!r}")
    if config.get("editable") is not False or dashboard.get("editable") is not False:
        raise ValidationError("dashboard and monitoring.json must disable UI editing")

    expected_variables = config.get("variables")
    templating = mapping(dashboard.get("templating"), "dashboard templating")
    variable_list = sequence(templating.get("list"), "dashboard templating list")
    variables: dict[str, Mapping[str, Any]] = {}
    for index, variable_value in enumerate(variable_list):
        variable = mapping(variable_value, f"dashboard variable {index}")
        name = variable.get("name")
        if not isinstance(name, str) or not name:
            raise ValidationError(f"dashboard variable {index} has no name")
        if name in variables:
            raise ValidationError(f"dashboard variable {name!r} is duplicated")
        variables[name] = variable
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

    panels = sequence(dashboard.get("panels"), "dashboard panels")
    titles: set[str] = set()
    for index, panel_value in enumerate(panels):
        title = mapping(panel_value, "dashboard panel").get("title")
        if not isinstance(title, str) or not title:
            raise ValidationError(f"dashboard panel {index} title must be a non-empty string")
        titles.add(title)
    required_titles = sequence(config.get("required_panel_titles"), "required panel titles")
    if not all(isinstance(title, str) and title for title in required_titles):
        raise ValidationError("required panel titles must be non-empty strings")
    expected_titles = set(required_titles)
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
    for index, target in enumerate(targets):
        expression = target.get("expr")
        if not isinstance(expression, str) or not expression:
            raise ValidationError(
                f"dashboard Prometheus target {index} expression must be a non-empty string"
            )
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


def load_alert_document(assets: MonitoringAssets) -> Mapping[str, Any]:
    if assets.alerts_path.suffix not in {".yaml", ".yml"}:
        raise ValidationError("alert rules must use a .yaml or .yml file")
    documents = yaml_documents(
        assets.alerts_path.read_text(encoding="utf-8"), "Prometheus alert rules"
    )
    if len(documents) != 1:
        raise ValidationError("alert rules must contain exactly one YAML document")
    document = documents[0]
    if "kind" in document or set(document) != {"groups"}:
        raise ValidationError("alert rules must be a standalone Prometheus rule file")
    return document


def validate_alerts(assets: MonitoringAssets) -> None:
    document = load_alert_document(assets)
    config = mapping(assets.contract.get("alerts"), "monitoring alert contract")
    groups = sequence(document.get("groups"), "alert groups")
    if len(groups) != 1:
        raise ValidationError("alert rules must contain exactly one group")
    group = mapping(groups[0], "alert group")
    if group.get("name") != config.get("group"):
        raise ValidationError("alert rule group does not match monitoring.json")
    if group.get("interval") != config.get("interval"):
        raise ValidationError("alert rule interval does not match monitoring.json")

    expected_for = config.get("for")
    markers = config.get("expression_markers")
    expected_labels = config.get("labels")
    required_annotations = config.get("required_annotations")
    if not all(isinstance(value, Mapping) for value in (expected_for, markers, expected_labels)):
        raise ValidationError("alert inventory, markers, and labels must be objects")
    if not isinstance(required_annotations, list):
        raise ValidationError("required alert annotations must be a list")

    alert_rules: dict[str, Mapping[str, Any]] = {}
    for index, rule_value in enumerate(sequence(group.get("rules"), "alert rules")):
        rule = mapping(rule_value, f"alert rule {index}")
        name = rule.get("alert")
        if not isinstance(name, str) or not name:
            raise ValidationError(f"alert rule {index} has no alert name")
        if name in alert_rules:
            raise ValidationError(f"alert {name} is duplicated")
        alert_rules[name] = rule
    if set(alert_rules) != set(expected_for):
        raise ValidationError(f"alerts are {sorted(alert_rules)}, expected {sorted(expected_for)}")
    if set(markers) != set(expected_for):
        raise ValidationError("expression marker inventory does not match alert inventory")

    for name, duration in expected_for.items():
        rule = alert_rules[name]
        labels = mapping(rule.get("labels"), f"{name} labels")
        annotations = mapping(rule.get("annotations"), f"{name} annotations")
        for label, value in expected_labels.items():
            if labels.get(label) != value:
                raise ValidationError(f"{name} lacks label {label}={value}")
        for annotation in required_annotations:
            if not isinstance(annotations.get(annotation), str) or not annotations[annotation]:
                raise ValidationError(f"{name} lacks annotation {annotation}")
        actual_duration = rule.get("for")
        if actual_duration != duration:
            raise ValidationError(f"{name} for={actual_duration!r}, expected {duration!r}")
        for marker in markers[name]:
            expression = rule.get("expr")
            if not isinstance(expression, str) or marker not in expression:
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
    document = yaml_documents(values, "Prometheus values")[0]
    default_rules = mapping(document.get("defaultRules"), "defaultRules")
    grafana = mapping(document.get("grafana"), "grafana")
    sidecar = mapping(grafana.get("sidecar"), "grafana.sidecar")
    dashboards = mapping(sidecar.get("dashboards"), "grafana.sidecar.dashboards")
    provider = mapping(dashboards.get("provider"), "grafana.sidecar.dashboards.provider")
    persistence = mapping(grafana.get("persistence"), "grafana.persistence")
    expected = {
        "defaultRules.create": default_rules.get("create") is True,
        "grafana.enabled": grafana.get("enabled") is True,
        "grafana.defaultDashboardsEnabled": grafana.get("defaultDashboardsEnabled") is True,
        "grafana.sidecar.dashboards.label": dashboards.get("label") == "grafana_dashboard",
        "grafana.sidecar.dashboards.labelValue": dashboards.get("labelValue") == "1",
        "grafana.sidecar.dashboards.provider.disableDelete": provider.get("disableDelete") is True,
        "grafana.sidecar.dashboards.provider.allowUiUpdates": provider.get("allowUiUpdates")
        is False,
        "grafana.persistence.enabled": persistence.get("enabled") is True,
        "grafana.persistence.type": persistence.get("type") == "pvc",
    }
    for field, valid in expected.items():
        if not valid:
            raise ValidationError(f"Prometheus values violate {field}")
    if "additionalPrometheusRulesMap" in document or "dashboards" in grafana:
        raise ValidationError("Prometheus values still embed service assets")


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
    documents = yaml_documents(rendered, "rendered Prometheus stack")
    if len([document for document in documents if document.get("kind") == "PrometheusRule"]) < 10:
        raise ValidationError("base chart render removed the default Prometheus rules")
    dashboard_keys = [
        key
        for document in documents
        if document.get("kind") == "ConfigMap"
        for key in mapping(document.get("data", {}), "ConfigMap data")
        if isinstance(key, str) and key.endswith(".json")
    ]
    if len(dashboard_keys) < 20:
        raise ValidationError("base chart render removed the default Grafana dashboards")
    grafana_claims = []
    for document in documents:
        if document.get("kind") != "PersistentVolumeClaim":
            continue
        metadata = mapping(document.get("metadata"), "PVC metadata")
        labels = mapping(metadata.get("labels", {}), "PVC labels")
        if labels.get("app.kubernetes.io/name") == "grafana":
            grafana_claims.append(document)
    if len(grafana_claims) != 1:
        raise ValidationError(
            f"base chart must render one persistent Grafana claim, found {len(grafana_claims)}"
        )
    providers: list[Mapping[str, Any]] = []
    for document in documents:
        if document.get("kind") != "ConfigMap":
            continue
        for value in mapping(document.get("data", {}), "ConfigMap data").values():
            if not isinstance(value, str):
                continue
            try:
                embedded = yaml.safe_load(value)
            except yaml.YAMLError:
                continue
            if not isinstance(embedded, Mapping):
                continue
            if "providers" not in embedded:
                continue
            for provider in sequence(embedded["providers"], "Grafana dashboard providers"):
                providers.append(mapping(provider, "Grafana dashboard provider"))
    if not any(
        provider.get("disableDeletion") is True and provider.get("allowUiUpdates") is False
        for provider in providers
    ):
        raise ValidationError("Grafana dashboard provider must disable deletion and UI updates")


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--assets-root", type=Path, required=True)
    parser.add_argument("--environment", required=True)
    parser.add_argument("--service", required=True)
    parser.add_argument("--values", type=Path, required=True)
    parser.add_argument("--makefile", type=Path, required=True, action="append")
    parser.add_argument("--rendered-stack", type=Path, required=True)
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        assets = load_assets(args.assets_root, args.environment, args.service)
        validate_dashboard(assets)
        validate_alerts(assets)
        validate_fixture(assets)
        validate_values(args.values.read_text(encoding="utf-8"))
        makefiles = "\n".join(path.read_text(encoding="utf-8") for path in args.makefile)
        validate_makefile(makefiles)
        validate_rendered_stack(args.rendered_stack.read_text(encoding="utf-8"))
    except (AssetError, OSError, ValidationError) as error:
        print(f"Monitoring asset check failed: {error}", file=sys.stderr)
        return 1
    print(f"Monitoring assets passed contract checks for {args.service} in {args.environment}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
