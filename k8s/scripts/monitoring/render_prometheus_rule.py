#!/usr/bin/env python3
"""Render a service alert file as a PrometheusRule Kubernetes resource."""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Optional, Sequence

from check_monitoring import ValidationError, validate_alerts
from monitoring_assets import AssetError, load_assets, prometheus_rule_resource


def render(assets_root: Path, environment: str, service: str) -> str:
    assets = load_assets(assets_root, environment, service)
    validate_alerts(assets)
    resource = prometheus_rule_resource(assets)
    metadata = resource["metadata"]
    labels = metadata["labels"]
    output = [
        f"apiVersion: {resource['apiVersion']}",
        f"kind: {resource['kind']}",
        "metadata:",
        f"  name: {json.dumps(metadata['name'])}",
        f"  namespace: {json.dumps(metadata['namespace'])}",
        "  labels:",
    ]
    output.extend(
        f"    {key}: {json.dumps(value)}" for key, value in sorted(labels.items())
    )
    output.append("spec:")
    output.extend(f"  {line}" if line else "" for line in assets.alerts_path.read_text(
        encoding="utf-8"
    ).splitlines())
    return "\n".join(output) + "\n"


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--assets-root", type=Path, required=True)
    parser.add_argument("--environment", required=True)
    parser.add_argument("--service", required=True)
    parser.add_argument("--output", type=Path)
    return parser.parse_args(argv)


def main(argv: Optional[Sequence[str]] = None) -> int:
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
