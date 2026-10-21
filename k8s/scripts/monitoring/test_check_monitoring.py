#!/usr/bin/env python3
"""Pytest coverage for generic service monitoring tooling."""

from __future__ import annotations

import copy
import json
import tempfile
from dataclasses import replace
from pathlib import Path
from unittest import mock

import pytest
import render_grafana_dashboard as dashboard_renderer
import render_prometheus_rule as renderer
import resolve_monitoring
import yaml

import check_monitoring as check
import monitoring_assets
import monitoring_integration as harness

SCRIPT_DIR = Path(__file__).parent
ASSETS_ROOT = SCRIPT_DIR.parents[1] / "observability"


class TestMonitoringTooling:
    def setup_method(self) -> None:
        self.assets = monitoring_assets.load_assets(ASSETS_ROOT, "qa", "product")

    def test_resolver_rejects_unsafe_service_name(self) -> None:
        with pytest.raises(monitoring_assets.AssetError, match="service must match"):
            monitoring_assets.load_assets(ASSETS_ROOT, "qa", "../product")

    def test_product_assets_pass_generic_contract(self) -> None:
        # when
        check.validate_dashboard(self.assets)
        check.validate_alerts(self.assets)
        check.validate_fixture(self.assets)

    def test_nested_dashboard_values_must_use_declared_types(self) -> None:
        # given
        dashboard = copy.deepcopy(self.assets.dashboard)
        dashboard["panels"] = ["not-an-object"]

        # when / then
        with pytest.raises(check.ValidationError, match="dashboard panel must be an object"):
            check.validate_dashboard(replace(self.assets, dashboard=dashboard))

    @pytest.mark.parametrize(
        ("field", "value", "diagnostic"),
        (
            ("title", [], "dashboard panel 0 title must be a non-empty string"),
            ("expr", None, "dashboard Prometheus target 0 expression must be a non-empty string"),
        ),
    )
    def test_dashboard_renderer_rejects_malformed_scalar_fields(
        self, monkeypatch, capsys, field: str, value: object, diagnostic: str
    ) -> None:
        # given
        dashboard = copy.deepcopy(self.assets.dashboard)
        if field == "title":
            dashboard["panels"][0]["title"] = value
        else:
            dashboard["panels"][0]["targets"][0]["expr"] = value
        assets = replace(self.assets, dashboard=dashboard)
        monkeypatch.setattr(dashboard_renderer, "load_assets", lambda *_args: assets)

        # when
        status = dashboard_renderer.main(
            [
                "--assets-root",
                str(ASSETS_ROOT),
                "--environment",
                "qa",
                "--service",
                "product",
            ]
        )

        # then
        captured = capsys.readouterr()
        assert status == 1
        assert captured.out == ""
        assert diagnostic in captured.err

    def test_malformed_alert_yaml_has_contextual_failure(self, tmp_path: Path) -> None:
        # given
        path = tmp_path / "alerts.yaml"
        path.write_text("groups: [", encoding="utf-8")

        # when / then
        with pytest.raises(check.ValidationError, match="Prometheus alert rules is malformed YAML"):
            check.validate_alerts(replace(self.assets, alerts_path=path))

    def test_dashboard_requires_configured_filters(self) -> None:
        dashboard = copy.deepcopy(self.assets.dashboard)
        dashboard["panels"][0]["targets"][0]["expr"] = (
            'sum(insurance_hub_product_requests_total{service_name="product-service"})'
        )
        with pytest.raises(check.ValidationError, match="required filters"):
            check.validate_dashboard(replace(self.assets, dashboard=dashboard))

    def test_alert_inventory_and_labels_come_from_service_contract(self) -> None:
        rules = self.assets.alerts_path.read_text(encoding="utf-8")
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "alerts.yaml"
            path.write_text(rules.replace("        severity: warning\n", "", 1), encoding="utf-8")
            with pytest.raises(check.ValidationError, match="lacks label severity"):
                check.validate_alerts(replace(self.assets, alerts_path=path))

    def test_values_reject_embedded_service_assets(self) -> None:
        values = {
            "defaultRules": {"create": True},
            "grafana": {
                "enabled": True,
                "defaultDashboardsEnabled": True,
                "sidecar": {
                    "dashboards": {
                        "label": "grafana_dashboard",
                        "labelValue": "1",
                        "provider": {"disableDelete": True, "allowUiUpdates": False},
                    }
                },
                "persistence": {"enabled": True, "type": "pvc"},
            },
            "additionalPrometheusRulesMap": {},
        }
        with pytest.raises(check.ValidationError, match="still embed"):
            check.validate_values(yaml.safe_dump(values))

    def test_values_require_immutable_dashboard_provider(self) -> None:
        values = {
            "defaultRules": {"create": True},
            "grafana": {
                "enabled": True,
                "defaultDashboardsEnabled": True,
                "sidecar": {
                    "dashboards": {
                        "label": "grafana_dashboard",
                        "labelValue": "1",
                        "provider": {"allowUiUpdates": False},
                    }
                },
                "persistence": {"enabled": True, "type": "pvc"},
            },
        }
        with pytest.raises(check.ValidationError, match="disableDelete"):
            check.validate_values(yaml.safe_dump(values))

    def test_renderer_wraps_yaml_in_selected_prometheus_rule(self) -> None:
        # when
        rendered = renderer.render(ASSETS_ROOT, "qa", "product")
        resource = yaml.safe_load(rendered)

        # then
        assert "PrometheusRule" == resource["kind"]
        assert "qa-product-service" == resource["metadata"]["name"]
        assert "qa-prometheus" == resource["metadata"]["labels"]["release"]
        assert (
            yaml.safe_load(self.assets.alerts_path.read_text(encoding="utf-8")) == resource["spec"]
        )

    def test_dashboard_renderer_wraps_json_in_sidecar_config_map(self) -> None:
        # when
        rendered = dashboard_renderer.render(ASSETS_ROOT, "qa", "product")
        resource = yaml.safe_load(rendered)
        dashboard = json.loads(resource["data"]["product-dashboard.json"])

        # then
        assert resource["kind"] == "ConfigMap"
        assert resource["metadata"]["name"] == "qa-product-dashboard"
        assert resource["metadata"]["labels"]["grafana_dashboard"] == "1"
        assert dashboard == self.assets.dashboard

    def test_rendered_stack_accepts_equivalent_structured_yaml_layout(self) -> None:
        # given
        documents = [{"kind": "PrometheusRule"} for _ in range(10)]
        documents.extend(
            [
                {
                    "kind": "ConfigMap",
                    "data": {f"dashboard-{index}.json": "{}" for index in range(20)},
                },
                {
                    "kind": "ConfigMap",
                    "data": {
                        "dashboardsproviders.yaml": yaml.safe_dump(
                            {
                                "providers": [
                                    {
                                        "disableDeletion": True,
                                        "allowUiUpdates": False,
                                        "options": {"path": "/tmp/dashboards"},
                                    }
                                ]
                            }
                        )
                    },
                },
                {
                    "kind": "PersistentVolumeClaim",
                    "metadata": {"labels": {"app.kubernetes.io/name": "grafana"}},
                },
            ]
        )

        # when / then
        check.validate_rendered_stack(yaml.safe_dump_all(documents))

    def test_resolver_uses_json_and_explicit_field_output(self, capsys) -> None:
        # given
        arguments = [
            "--assets-root",
            str(ASSETS_ROOT),
            "--environment",
            "qa",
            "--service",
            "product",
        ]

        # when
        status = resolve_monitoring.main(arguments)
        output = capsys.readouterr().out

        # then
        assert status == 0
        assert json.loads(output)["namespace"] == "qa-monitoring"

        # when
        status = resolve_monitoring.main([*arguments, "--field", "prometheus-rule"])
        output = capsys.readouterr().out

        # then
        assert status == 0
        assert output == "qa-product-service\n"

    def test_fixture_and_dashboard_substitution_are_service_driven(self) -> None:
        fixture = harness.MetricFixture(self.assets.fixture)
        first = fixture.render().decode("utf-8")
        second = fixture.render().decode("utf-8")
        assert 'service_name="product-service"' in first
        assert "insurance_hub_product_requests_total" in first
        assert first != second
        expressions = harness.dashboard_expressions(self.assets)
        assert expressions
        assert all("$environment" not in value for value in expressions)
        assert all("$implementation" not in value for value in expressions)

    def test_cleanup_rejects_a_remaining_owned_container(self) -> None:
        def run(arguments, **kwargs):
            if arguments[1:3] == ["rm", "-f"]:
                return harness.subprocess.CompletedProcess(arguments, 0, stdout="", stderr="")
            if arguments[1:3] == ["container", "ls"]:
                return harness.subprocess.CompletedProcess(
                    arguments, 0, stdout="fixture\n", stderr=""
                )
            pytest.fail(f"unexpected Docker command: {arguments}")

        with mock.patch.object(harness.subprocess, "run", side_effect=run):
            with pytest.raises(harness.HarnessError, match="remains after cleanup"):
                harness.cleanup("fixture")
