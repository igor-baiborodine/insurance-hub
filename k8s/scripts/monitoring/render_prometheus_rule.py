#!/usr/bin/env python3
"""Render a service alert file as a PrometheusRule Kubernetes resource."""

from __future__ import annotations

import argparse
import sys
from collections.abc import Sequence
from pathlib import Path

import yaml

from check_monitoring import ValidationError, load_alert_document, validate_alerts
from monitoring_assets import AssetError, load_assets, prometheus_rule_resource


def render(assets_root: Path, environment: str, service: str) -> str:
    assets = load_assets(assets_root, environment, service)
    validate_alerts(assets)
    resource = prometheus_rule_resource(assets)
    resource["spec"] = dict(load_alert_document(assets))
    return yaml.safe_dump(resource, sort_keys=False, allow_unicode=True)


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--assets-root", type=Path, required=True)
    parser.add_argument("--environment", required=True)
    parser.add_argument("--service", required=True)
    parser.add_argument("--output", type=Path)
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        rendered = render(args.assets_root, args.environment, args.service)
        if args.output:
            args.output.write_text(rendered, encoding="utf-8")
        else:
            sys.stdout.write(rendered)
    except (AssetError, OSError, ValidationError) as error:
        print(f"PrometheusRule render failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
