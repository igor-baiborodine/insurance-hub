#!/usr/bin/env python3
"""Emit shell-safe environment values for generic monitoring Make targets."""

from __future__ import annotations

import argparse
import shlex
import sys
from pathlib import Path
from typing import Optional, Sequence

from monitoring_assets import (
    AssetError,
    grafana_dashboard_resource,
    load_assets,
    prometheus_rule_resource,
)


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--assets-root", type=Path, required=True)
    parser.add_argument("--environment", required=True)
    parser.add_argument("--service", required=True)
    return parser.parse_args(argv)


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = parse_args(argv)
    try:
        assets = load_assets(args.assets_root, args.environment, args.service)
        rule = prometheus_rule_resource(assets)
        dashboard = grafana_dashboard_resource(assets)
        values = {
            "MONITORING_KUBE_CONTEXT": assets.environment["kube_context"],
            "MONITORING_NAMESPACE": assets.environment["monitoring_namespace"],
            "MONITORING_PROMETHEUS_RULE": rule["metadata"]["name"],
            "MONITORING_GRAFANA_DASHBOARD_CONFIG_MAP": dashboard["metadata"]["name"],
            "MONITORING_PROMETHEUS_VALUES_PATH": str(
                assets.assets_root.parent / assets.environment["prometheus_values_path"]
            ),
            "MONITORING_SERVICE_DIR": str(assets.service_dir),
            "MONITORING_ALERTS_PATH": str(assets.alerts_path),
            "MONITORING_ALERT_TESTS_PATH": str(assets.alert_tests_path),
        }
    except AssetError as error:
        print(f"Monitoring asset resolution failed: {error}", file=sys.stderr)
        return 1
    for key, value in values.items():
        print(f"{key}={shlex.quote(value)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
