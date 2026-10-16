#!/usr/bin/env python3
"""Unit tests for the Prometheus receiver and disposable-backend policy."""

from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path


CHECK_PATH = Path(__file__).with_name("check-backend-config.py")
SPEC = importlib.util.spec_from_file_location("backend_check", CHECK_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot import {CHECK_PATH}")
backend_check = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = backend_check
SPEC.loader.exec_module(backend_check)

HARNESS_PATH = Path(__file__).with_name("test-backend-ingestion.py")
HARNESS_SPEC = importlib.util.spec_from_file_location("backend_harness", HARNESS_PATH)
if HARNESS_SPEC is None or HARNESS_SPEC.loader is None:
    raise RuntimeError(f"cannot import {HARNESS_PATH}")
backend_harness = importlib.util.module_from_spec(HARNESS_SPEC)
sys.modules[HARNESS_SPEC.name] = backend_harness
HARNESS_SPEC.loader.exec_module(backend_harness)


class BackendConfigTest(unittest.TestCase):
    def test_values_require_remote_write_and_reject_native_otlp(self) -> None:
        valid = """prometheusSpec:
  retention: 10d
  enableRemoteWriteReceiver: true
  enableOTLPReceiver: false
"""
        backend_check.validate_values(valid)
        for old, new in (
            ("enableRemoteWriteReceiver: true", "enableRemoteWriteReceiver: false"),
            ("enableOTLPReceiver: false", "enableOTLPReceiver: true"),
            ("retention: 10d", "retention: 1d"),
        ):
            with self.subTest(change=new), self.assertRaises(backend_check.ValidationError):
                backend_check.validate_values(valid.replace(old, new))

    def test_rendered_prometheus_pins_effective_receiver_settings(self) -> None:
        rendered = f"""kind: Prometheus
metadata:
  labels:
    app.kubernetes.io/version: \"{backend_check.KUBE_PROMETHEUS_STACK_VERSION}\"
spec:
  image: \"{backend_check.PROMETHEUS_IMAGE}\"
  enableRemoteWriteReceiver: true
  enableOTLPReceiver: false
  retention: \"10d\"
"""
        backend_check.validate_rendered_prometheus(rendered)
        with self.assertRaises(backend_check.ValidationError):
            backend_check.validate_rendered_prometheus(
                rendered.replace("enableOTLPReceiver: false", "enableOTLPReceiver: true")
            )

    def test_disposable_images_are_service_neutral_and_pinned(self) -> None:
        self.assertEqual(
            {
                "alloy",
                "prometheus",
                "loki",
                "tempo",
                "zipkin_bridge",
                "zipkin",
                "postgres",
            },
            set(backend_check.DISPOSABLE_IMAGES),
        )
        for name, image in backend_check.DISPOSABLE_IMAGES.items():
            with self.subTest(name=name):
                self.assertRegex(image, r"(@sha256:[0-9a-f]{64}|:[v0-9][A-Za-z0-9.-]*)$")

    def test_disposable_config_reuses_policy_for_selected_service(self) -> None:
        source = (Path(__file__).parents[2] / "overlays/qa/infra/alloy/config.alloy").read_text(
            encoding="utf-8"
        )
        config = backend_harness.build_alloy_config(
            source, "product", "/fixture/service.log"
        )
        self.assertNotIn('discovery.kubernetes "qa_service_pods"', config)
        self.assertNotIn('otelcol.processor.k8sattributes "qa_services"', config)
        self.assertIn('otelcol.processor.transform "qa_services"', config)
        self.assertIn('loki.source.file "logs"', config)
        self.assertIn("local.file_match.fixture_service_logs.targets", config)
        self.assertIn('service_json_logs "product"', config)
        self.assertIn('endpoint = "tempo:4317"', config)
        self.assertIn('url = "http://prometheus:9090/api/v1/write"', config)

    def test_synthetic_fixture_follows_service_conventions(self) -> None:
        payload = backend_harness.synthetic_metric_payload(7, 2_000_000_000)
        resource = payload["resourceMetrics"][0]["resource"]["attributes"]
        values = {entry["key"]: entry["value"]["stringValue"] for entry in resource}
        self.assertEqual("synthetic-service", values["service.name"])
        metrics = payload["resourceMetrics"][0]["scopeMetrics"][0]["metrics"]
        self.assertEqual(
            [
                "insurance_hub.synthetic.requests",
                "insurance_hub.synthetic.request.duration",
            ],
            [metric["name"] for metric in metrics],
        )
        histogram = metrics[1]["histogram"]["dataPoints"][0]
        self.assertEqual(len(histogram["explicitBounds"]) + 1, len(histogram["bucketCounts"]))


if __name__ == "__main__":
    unittest.main()
