#!/usr/bin/env python3
"""Exercise Product observability target guards and cleanup without live access."""

from __future__ import annotations

import importlib.util
import dataclasses
import json
import sys
import unittest
from contextlib import nullcontext
from pathlib import Path
from unittest import mock


SCRIPT = Path(__file__).with_name("verify_product_observability.py")
SPEC = importlib.util.spec_from_file_location("verify_product_observability", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot import {SCRIPT}")
verifier = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = verifier
SPEC.loader.exec_module(verifier)
ASSETS_ROOT = Path(__file__).parents[2] / "observability"


class ProductObservabilityVerificationTest(unittest.TestCase):
    def setUp(self) -> None:
        self.assets = verifier.load_assets(ASSETS_ROOT, "qa", "product")

    def test_configuration_resolves_exact_read_only_target(self) -> None:
        selected = verifier.verification_config(self.assets)

        self.assertEqual("qa-svc", selected["service_namespace"])
        self.assertEqual("qa-product-api", selected["product_deployment"])
        self.assertEqual(
            "qa-prometheus-kube-prometh-prometheus",
            selected["prometheus_service"],
        )

    def test_target_rejects_non_qa_or_malformed_revision(self) -> None:
        with self.assertRaisesRegex(verifier.VerificationError, "source revision"):
            verifier.KubernetesTarget(self.assets, "short")
        changed = dataclasses.replace(self.assets, environment_name="local-dev")
        with self.assertRaisesRegex(verifier.VerificationError, "QA-only"):
            verifier.KubernetesTarget(changed, "a" * 40)

    def test_coordinates_reject_wrong_active_context_before_resource_reads(self) -> None:
        target = verifier.KubernetesTarget(self.assets, "a" * 40)
        with mock.patch.object(verifier, "run", return_value="kind-local-dev-insurance-hub\n"), \
                mock.patch.object(target, "json") as kubernetes:
            with self.assertRaisesRegex(verifier.VerificationError, "active Kubernetes context"):
                target.coordinates()
            kubernetes.assert_not_called()

    def test_monitoring_assets_accept_reviewed_alert_inventory(self) -> None:
        target = verifier.KubernetesTarget(self.assets, "a" * 40)
        expected_alerts = set(self.assets.contract["alerts"]["for"])
        rule = {
            "spec": {
                "groups": [
                    {"rules": [{"alert": name} for name in sorted(expected_alerts)]},
                ]
            }
        }
        dashboard = {
            "data": {
                "product-dashboard.json": json.dumps(
                    {
                        "uid": self.assets.contract["dashboard"]["uid"],
                        "editable": False,
                    }
                )
            }
        }

        with mock.patch.object(target, "json", side_effect=[rule, dashboard]):
            observed = target.monitoring_assets()

        self.assertEqual(
            {
                "prometheus_rule": "qa-product-service",
                "dashboard_config_map": "qa-product-dashboard",
            },
            observed,
        )

    def test_monitoring_assets_reject_alert_inventory_mismatch(self) -> None:
        target = verifier.KubernetesTarget(self.assets, "a" * 40)
        expected_alerts = sorted(self.assets.contract["alerts"]["for"])
        rule = {
            "spec": {
                "groups": [
                    {"rules": [{"alert": name} for name in expected_alerts[1:]]},
                ]
            }
        }

        with mock.patch.object(target, "json", return_value=rule):
            with self.assertRaisesRegex(
                verifier.VerificationError,
                "live Product alert inventory differs from reviewed assets",
            ):
                target.monitoring_assets()

    def test_forwards_close_every_started_child_after_partial_failure(self) -> None:
        target = verifier.KubernetesTarget(self.assets, "a" * 40)

        class Child:
            def __init__(self, failed: bool) -> None:
                self.failed = failed
                self.terminated = False
                self.waited = False

            def poll(self) -> int | None:
                return 1 if self.failed else (0 if self.terminated else None)

            def terminate(self) -> None:
                self.terminated = True

            def wait(self, timeout: int) -> int:
                self.waited = True
                return 0

        first, second = Child(False), Child(True)
        services = [
            ("product", "qa-svc", "qa-product-api", 8081),
            ("health", "qa-svc", "qa-product-api-management", 8080),
        ]
        with mock.patch.object(
            verifier.subprocess, "Popen", side_effect=[first, second]
        ), mock.patch.object(
            verifier.socket, "create_connection", return_value=nullcontext()
        ):
            with self.assertRaisesRegex(verifier.VerificationError, "health exited"):
                with verifier.forwards(target, services):
                    pass
        self.assertTrue(first.terminated)
        self.assertTrue(first.waited and second.waited)

    def test_product_reads_use_safe_get_and_deterministic_trace_header(self) -> None:
        responses = ["live\n", "ready\n", '[{"code":"CAR"}]']
        with mock.patch.object(verifier, "request_text", side_effect=responses) as request:
            rows = verifier.product_reads({"health": 1001, "product": 1002}, "a" * 32)

        self.assertEqual(1, rows)
        self.assertEqual(
            {"traceparent": "00-" + "a" * 32 + "-0123456789abcdef-01"},
            request.call_args_list[-1].args[1],
        )


if __name__ == "__main__":
    unittest.main()
