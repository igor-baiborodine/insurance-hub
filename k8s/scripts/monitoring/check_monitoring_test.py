#!/usr/bin/env python3
"""Unit tests for generic service monitoring tooling."""

from __future__ import annotations

import copy
import importlib.util
import sys
import tempfile
import unittest
from dataclasses import replace
from pathlib import Path
from unittest import mock


SCRIPT_DIR = Path(__file__).parent
ASSETS_ROOT = SCRIPT_DIR.parents[1] / "observability"
sys.path.insert(0, str(SCRIPT_DIR))

import check_monitoring as check  # noqa: E402
import monitoring_assets  # noqa: E402
import test_monitoring as harness  # noqa: E402


def load_script(name: str, filename: str):
    path = SCRIPT_DIR / filename
    specification = importlib.util.spec_from_file_location(name, path)
    if specification is None or specification.loader is None:
        raise RuntimeError(f"cannot import {path}")
    module = importlib.util.module_from_spec(specification)
    sys.modules[name] = module
    specification.loader.exec_module(module)
    return module


renderer = load_script("monitoring_rule_renderer", "render_prometheus_rule.py")
dashboard_renderer = load_script(
    "grafana_dashboard_renderer", "render_grafana_dashboard.py"
)


class MonitoringToolingTest(unittest.TestCase):
    def setUp(self) -> None:
        self.assets = monitoring_assets.load_assets(ASSETS_ROOT, "qa", "product")

    def test_resolver_rejects_unsafe_service_name(self) -> None:
        with self.assertRaisesRegex(monitoring_assets.AssetError, "service must match"):
            monitoring_assets.load_assets(ASSETS_ROOT, "qa", "../product")

    def test_product_assets_pass_generic_contract(self) -> None:
        check.validate_dashboard(self.assets)
        check.validate_alerts(self.assets)
        check.validate_fixture(self.assets)

    def test_dashboard_requires_configured_filters(self) -> None:
        dashboard = copy.deepcopy(self.assets.dashboard)
        dashboard["panels"][0]["targets"][0]["expr"] = (
            'sum(insurance_hub_product_requests_total{service_name="product-service"})'
        )
        with self.assertRaisesRegex(check.ValidationError, "required filters"):
            check.validate_dashboard(replace(self.assets, dashboard=dashboard))

    def test_alert_inventory_and_labels_come_from_service_contract(self) -> None:
        rules = self.assets.alerts_path.read_text(encoding="utf-8")
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "alerts.yaml"
            path.write_text(rules.replace("        severity: warning\n", "", 1), encoding="utf-8")
            with self.assertRaisesRegex(check.ValidationError, "lacks label severity"):
                check.validate_alerts(replace(self.assets, alerts_path=path))

    def test_values_reject_embedded_service_assets(self) -> None:
        with self.assertRaisesRegex(check.ValidationError, "still embed"):
            check.validate_values(
                "defaultRules:\n  create: true\n"
                "grafana:\n  enabled: true\n  defaultDashboardsEnabled: true\n"
                "      label: grafana_dashboard\n      labelValue: \"1\"\n"
                "      disableDelete: true\n        allowUiUpdates: false\n"
                "  persistence:\n    enabled: true\n    type: pvc\n"
                "additionalPrometheusRulesMap:\n"
            )

    def test_values_require_immutable_dashboard_provider(self) -> None:
        with self.assertRaisesRegex(check.ValidationError, "disableDelete"):
            check.validate_values(
                "defaultRules:\n  create: true\n"
                "grafana:\n  enabled: true\n  defaultDashboardsEnabled: true\n"
                "      label: grafana_dashboard\n      labelValue: \"1\"\n"
                "  persistence:\n    enabled: true\n    type: pvc\n"
            )

    def test_renderer_wraps_yaml_in_selected_prometheus_rule(self) -> None:
        rendered = renderer.render(ASSETS_ROOT, "qa", "product")
        self.assertIn("kind: PrometheusRule", rendered)
        self.assertIn('name: "qa-product-service"', rendered)
        self.assertIn('release: "qa-prometheus"', rendered)
        self.assertIn("  groups:\n    - interval: 30s", rendered)

    def test_dashboard_renderer_wraps_json_in_sidecar_config_map(self) -> None:
        rendered = dashboard_renderer.render(ASSETS_ROOT, "qa", "product")
        self.assertIn("kind: ConfigMap", rendered)
        self.assertIn('name: "qa-product-dashboard"', rendered)
        self.assertIn('grafana_dashboard: "1"', rendered)
        self.assertIn("  product-dashboard.json: |-", rendered)
        self.assertIn('    "editable": false,', rendered)
        self.assertIn('    "uid": "insurance-hub-product-service"', rendered)

    def test_fixture_and_dashboard_substitution_are_service_driven(self) -> None:
        fixture = harness.MetricFixture(self.assets.fixture)
        first = fixture.render().decode("utf-8")
        second = fixture.render().decode("utf-8")
        self.assertIn('service_name="product-service"', first)
        self.assertIn("insurance_hub_product_requests_total", first)
        self.assertNotEqual(first, second)
        expressions = harness.dashboard_expressions(self.assets)
        self.assertTrue(expressions)
        self.assertTrue(all("$environment" not in value for value in expressions))
        self.assertTrue(all("$implementation" not in value for value in expressions))

    def test_cleanup_rejects_a_remaining_owned_container(self) -> None:
        def run(arguments, **kwargs):
            if arguments[1:3] == ["rm", "-f"]:
                return harness.subprocess.CompletedProcess(arguments, 0, stdout="", stderr="")
            if arguments[1:3] == ["container", "ls"]:
                return harness.subprocess.CompletedProcess(
                    arguments, 0, stdout="fixture\n", stderr=""
                )
            self.fail(f"unexpected Docker command: {arguments}")

        with mock.patch.object(harness.subprocess, "run", side_effect=run):
            with self.assertRaisesRegex(harness.HarnessError, "remains after cleanup"):
                harness.cleanup("fixture")


if __name__ == "__main__":
    unittest.main()
