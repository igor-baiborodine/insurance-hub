#!/usr/bin/env python3
"""Validate the pinned QA Prometheus receiver and disposable backend contract."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path
from typing import Optional, Sequence


KUBE_PROMETHEUS_STACK_VERSION = "92.6.0"
PROMETHEUS_IMAGE = "quay.io/prometheus/prometheus:v3.15.0-distroless"
RETENTION = "10d"

DISPOSABLE_IMAGES = {
    "alloy": "grafana/alloy:v1.20.1",
    "prometheus": PROMETHEUS_IMAGE,
    "loki": "grafana/loki:3.7.8",
    "tempo": "grafana/tempo:3.1.0",
    "zipkin_bridge": "otel/opentelemetry-collector-contrib:0.114.0",
    "zipkin": "openzipkin/zipkin:3.5.1",
    "postgres": (
        "postgres:17.10-alpine@sha256:"
        "742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193"
    ),
}


class ValidationError(Exception):
    """A deterministic desired-state or rendered-config failure."""


def _yaml_bool(text: str, field: str) -> bool:
    matches = re.findall(rf"(?m)^\s*{re.escape(field)}:\s*(true|false)\s*$", text)
    if len(matches) != 1:
        raise ValidationError(f"{field} must be set exactly once")
    return matches[0] == "true"


def _yaml_scalar(text: str, field: str) -> str:
    matches = re.findall(rf'(?m)^\s*{re.escape(field)}:\s*["\']?([^\s"\']+)["\']?\s*$', text)
    if len(matches) != 1:
        raise ValidationError(f"{field} must be set exactly once")
    return matches[0]


def validate_values(values: str) -> None:
    if not _yaml_bool(values, "enableRemoteWriteReceiver"):
        raise ValidationError("Prometheus remote-write receiver must be enabled")
    if _yaml_bool(values, "enableOTLPReceiver"):
        raise ValidationError("Prometheus native OTLP receiver must remain disabled")
    if _yaml_scalar(values, "retention") != RETENTION:
        raise ValidationError(f"Prometheus retention must remain {RETENTION}")


def validate_rendered_prometheus(rendered: str) -> None:
    required = (
        "kind: Prometheus",
        f'image: "{PROMETHEUS_IMAGE}"',
        f'app.kubernetes.io/version: "{KUBE_PROMETHEUS_STACK_VERSION}"',
        "enableRemoteWriteReceiver: true",
        "enableOTLPReceiver: false",
        f'retention: "{RETENTION}"',
    )
    for fragment in required:
        if fragment not in rendered:
            raise ValidationError(f"rendered Prometheus is missing: {fragment}")
    if "web.enable-otlp-receiver" in rendered:
        raise ValidationError("rendered Prometheus unexpectedly enables native OTLP")


def validate_makefile(makefile: str) -> None:
    required = (
        f"KUBE_PROMETHEUS_STACK_VERSION := {KUBE_PROMETHEUS_STACK_VERSION}",
        f"PROMETHEUS_IMAGE := {PROMETHEUS_IMAGE}",
        "prometheus-receiver-config-check:",
        "test-observability-backends:",
        "OBSERVABILITY_SERVICE_DOMAIN ?= product",
        "OBSERVABILITY_SERVICE_ROOT ?= ../services/$(OBSERVABILITY_SERVICE_DOMAIN)-service",
        "OBSERVABILITY_SERVICE_PROBE ?= scripts/alloy/probe-$(OBSERVABILITY_SERVICE_DOMAIN).py",
        '--values "$(PROMETHEUS_VALUES_PATH)"',
    )
    for fragment in required:
        if fragment not in makefile:
            raise ValidationError(f"k8s Makefile is missing: {fragment}")


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--values", type=Path, required=True)
    parser.add_argument("--makefile", type=Path, required=True)
    parser.add_argument("--rendered-prometheus", required=True)
    return parser.parse_args(argv)


def read_input(path: str) -> str:
    return sys.stdin.read() if path == "-" else Path(path).read_text(encoding="utf-8")


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = parse_args(argv)
    try:
        validate_values(args.values.read_text(encoding="utf-8"))
        validate_makefile(args.makefile.read_text(encoding="utf-8"))
        validate_rendered_prometheus(read_input(args.rendered_prometheus))
    except (OSError, ValidationError) as error:
        print(f"backend config check failed: {error}", file=sys.stderr)
        return 1
    print("backend config check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
