#!/usr/bin/env python3
"""Validate the pinned QA Prometheus receiver and disposable backend contract."""

from __future__ import annotations

import argparse
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

import yaml

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


def _yaml_documents(text: str, label: str) -> list[Mapping[str, Any]]:
    try:
        parsed = list(yaml.safe_load_all(text))
    except yaml.YAMLError as error:
        raise ValidationError(f"{label} is malformed YAML: {error}") from error
    documents = [document for document in parsed if document is not None]
    if not documents or not all(isinstance(document, Mapping) for document in documents):
        raise ValidationError(f"{label} must contain YAML mapping documents")
    return documents


def _mapping(value: Any, label: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise ValidationError(f"{label} must be a mapping")
    return value


def validate_values(values: str) -> None:
    document = _yaml_documents(values, "Prometheus values")[0]
    prometheus = _mapping(document.get("prometheus"), "prometheus")
    specification = _mapping(prometheus.get("prometheusSpec"), "prometheus.prometheusSpec")
    if specification.get("enableRemoteWriteReceiver") is not True:
        raise ValidationError("Prometheus remote-write receiver must be enabled")
    if specification.get("enableOTLPReceiver") is not False:
        raise ValidationError("Prometheus native OTLP receiver must remain disabled")
    if specification.get("retention") != RETENTION:
        raise ValidationError(f"Prometheus retention must remain {RETENTION}")


def validate_rendered_prometheus(rendered: str) -> None:
    resources = [
        document
        for document in _yaml_documents(rendered, "rendered Prometheus")
        if document.get("kind") == "Prometheus"
    ]
    if len(resources) != 1:
        raise ValidationError(
            f"rendered output must contain one Prometheus resource, found {len(resources)}"
        )
    resource = resources[0]
    metadata = _mapping(resource.get("metadata"), "Prometheus metadata")
    labels = _mapping(metadata.get("labels"), "Prometheus metadata.labels")
    specification = _mapping(resource.get("spec"), "Prometheus spec")
    expected = {
        "image": PROMETHEUS_IMAGE,
        "enableRemoteWriteReceiver": True,
        "enableOTLPReceiver": False,
        "retention": RETENTION,
    }
    for field, value in expected.items():
        if specification.get(field) != value:
            raise ValidationError(f"rendered Prometheus spec.{field} must be {value!r}")
    if labels.get("app.kubernetes.io/version") != KUBE_PROMETHEUS_STACK_VERSION:
        raise ValidationError("rendered Prometheus chart version label differs from the pin")
    if "web.enable-otlp-receiver" in yaml.safe_dump(resource, sort_keys=True):
        raise ValidationError("rendered Prometheus unexpectedly enables native OTLP")


def validate_makefile(makefile: str) -> None:
    required = (
        f"KUBE_PROMETHEUS_STACK_VERSION := {KUBE_PROMETHEUS_STACK_VERSION}",
        f"PROMETHEUS_IMAGE := {PROMETHEUS_IMAGE}",
        "prometheus-receiver-config-check:",
        "test-observability-backends:",
        "OBSERVABILITY_SERVICE_DOMAIN ?= product",
        "OBSERVABILITY_SERVICE_ROOT ?= "
        "$(K8S_VALIDATION_REPOSITORY_ROOT)/services/"
        "$(OBSERVABILITY_SERVICE_DOMAIN)-service",
        "OBSERVABILITY_SERVICE_PROBE ?= "
        "$(K8S_VALIDATION_ROOT)/scripts/alloy/"
        "probe_$(OBSERVABILITY_SERVICE_DOMAIN).py",
        '--values "$(PROMETHEUS_VALUES_PATH)"',
    )
    for fragment in required:
        if fragment not in makefile:
            raise ValidationError(f"k8s Makefile is missing: {fragment}")


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--values", type=Path, required=True)
    parser.add_argument("--makefile", type=Path, required=True, action="append")
    parser.add_argument("--rendered-prometheus", required=True)
    return parser.parse_args(argv)


def read_input(path: str) -> str:
    return sys.stdin.read() if path == "-" else Path(path).read_text(encoding="utf-8")


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        validate_values(args.values.read_text(encoding="utf-8"))
        makefiles = "\n".join(path.read_text(encoding="utf-8") for path in args.makefile)
        validate_makefile(makefiles)
        validate_rendered_prometheus(read_input(args.rendered_prometheus))
    except (OSError, TypeError, ValidationError) as error:
        print(f"backend config check failed: {error}", file=sys.stderr)
        return 1
    print("backend config check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
