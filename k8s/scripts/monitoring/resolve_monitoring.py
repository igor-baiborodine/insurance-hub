#!/usr/bin/env python3
"""Resolve typed monitoring asset fields for generic Make targets."""

from __future__ import annotations

import argparse
import json
import sys
from collections.abc import Sequence
from pathlib import Path

from monitoring_assets import (
    AssetError,
    grafana_dashboard_resource,
    load_assets,
    prometheus_rule_resource,
)

FIELDS = {
    "kube-context": "kube_context",
    "namespace": "namespace",
    "prometheus-rule": "prometheus_rule",
    "grafana-dashboard-config-map": "grafana_dashboard_config_map",
    "prometheus-values-path": "prometheus_values_path",
    "service-dir": "service_dir",
    "alerts-path": "alerts_path",
    "alert-tests-path": "alert_tests_path",
}


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--assets-root", type=Path, required=True)
    parser.add_argument("--environment", required=True)
    parser.add_argument("--service", required=True)
    parser.add_argument("--field", choices=tuple(FIELDS))
    return parser.parse_args(argv)


def resolve(args: argparse.Namespace) -> dict[str, str]:
    assets = load_assets(args.assets_root, args.environment, args.service)
    rule = prometheus_rule_resource(assets)
    dashboard = grafana_dashboard_resource(assets)
    return {
        "kube_context": assets.environment["kube_context"],
        "namespace": assets.environment["monitoring_namespace"],
        "prometheus_rule": rule["metadata"]["name"],
        "grafana_dashboard_config_map": dashboard["metadata"]["name"],
        "prometheus_values_path": str(
            assets.assets_root.parent / assets.environment["prometheus_values_path"]
        ),
        "service_dir": str(assets.service_dir),
        "alerts_path": str(assets.alerts_path),
        "alert_tests_path": str(assets.alert_tests_path),
    }


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        values = resolve(args)
    except (AssetError, KeyError, TypeError) as error:
        print(f"Monitoring asset resolution failed: {error}", file=sys.stderr)
        return 1
    if args.field:
        print(values[FIELDS[args.field]])
    else:
        print(json.dumps(values, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
