#!/usr/bin/env python3
"""Render a service dashboard as a Grafana sidecar ConfigMap."""

from __future__ import annotations

import argparse
import json
import sys
from collections.abc import Sequence
from pathlib import Path

from check_monitoring import ValidationError, validate_dashboard
from monitoring_assets import AssetError, grafana_dashboard_resource, load_assets


def render(assets_root: Path, environment: str, service: str) -> str:
    assets = load_assets(assets_root, environment, service)
    validate_dashboard(assets)
    resource = grafana_dashboard_resource(assets)
    metadata = resource["metadata"]
    labels = metadata["labels"]
    dashboard_key = f"{assets.service}-dashboard.json"
    dashboard_json = json.dumps(assets.dashboard, indent=2, sort_keys=True)
    output = [
        f"apiVersion: {resource['apiVersion']}",
        f"kind: {resource['kind']}",
        "metadata:",
        f"  name: {json.dumps(metadata['name'])}",
        f"  namespace: {json.dumps(metadata['namespace'])}",
        "  labels:",
    ]
    output.extend(f"    {key}: {json.dumps(value)}" for key, value in sorted(labels.items()))
    output.extend(("data:", f"  {dashboard_key}: |-"))
    output.extend(f"    {line}" for line in dashboard_json.splitlines())
    return "\n".join(output) + "\n"


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
        print(f"Grafana dashboard ConfigMap render failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
