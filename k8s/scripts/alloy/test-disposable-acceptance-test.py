#!/usr/bin/env python3
"""Exercise Product disposable-acceptance policy and controlled verifier defects."""

from __future__ import annotations

import copy
import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("test-disposable-acceptance.py")
SPEC = importlib.util.spec_from_file_location("acceptance", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot import {SCRIPT}")
acceptance = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = acceptance
SPEC.loader.exec_module(acceptance)
PROFILE_PATH = Path(__file__).parents[2] / "observability/services/product/acceptance.json"


class AcceptanceVerifierTest(unittest.TestCase):
    def setUp(self) -> None:
        self.profile = acceptance.load_profile(PROFILE_PATH)
        self.load = {
            "maxInFlight": 8,
            "paths": {
                name: {
                    "requests": 2,
                    "successes": 2,
                    "failures": 0,
                    "p95Millis": 10,
                    "p99Millis": 20,
                }
                for name in acceptance.PATH_NAMES
            },
        }

    def test_profile_rejects_changed_predeclared_budget(self) -> None:
        changed = copy.deepcopy(self.profile)
        changed["fault"]["seconds"] = 59
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "profile.json"
            path.write_text(json.dumps(changed), encoding="utf-8")
            with self.assertRaises(acceptance.AcceptanceError):
                acceptance.load_profile(path)

    def test_load_rejects_missing_path(self) -> None:
        changed = copy.deepcopy(self.load)
        del changed["paths"]["grpc_get"]
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.verify_load(changed, 2, self.profile)

    def test_load_rejects_duplicate_or_failed_count(self) -> None:
        changed = copy.deepcopy(self.load)
        changed["paths"]["http_list"]["requests"] = 3
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.verify_load(changed, 2, self.profile)

    def test_load_rejects_stale_latency(self) -> None:
        changed = copy.deepcopy(self.load)
        changed["paths"]["grpc_list"]["p95Millis"] = 251
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.verify_load(changed, 2, self.profile)

    def test_resources_reject_unsafe_nonfinite_or_over_budget_values(self) -> None:
        samples = [
            acceptance.ResourceSample(1, 10, 20, 0),
            acceptance.ResourceSample(2, 201, 20, 0),
        ]
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.verify_resources(samples, self.profile["product_resources"], "Product")
        samples[1] = acceptance.ResourceSample(2, float("nan"), 20, 0)
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.verify_resources(samples, self.profile["product_resources"], "Product")

    def test_overhead_rejects_incomplete_pairing(self) -> None:
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.verify_overhead([self.load], [self.load], self.profile)

    def test_docker_memory_parser_prefers_complete_unit_suffix(self) -> None:
        self.assertEqual(72.75, acceptance.parse_memory_mib("72.75MiB"))
        self.assertEqual(1024, acceptance.parse_memory_mib("1GiB"))

    def test_resource_summary_preserves_observed_budget_values(self) -> None:
        samples = [
            acceptance.ResourceSample(float(index), float(index), float(index + 10), index / 100)
            for index in range(1, 62)
        ]
        summary = acceptance.resource_summary(samples)
        self.assertEqual(61, summary["samples"])
        self.assertEqual(60, summary["duration_seconds"])
        self.assertEqual(58, summary["cpu_p95_millicores"])
        self.assertEqual(71, summary["memory_max_mib"])
        self.assertEqual(0.61, summary["cpu_throttling_ratio_max"])
        self.assertIsNotNone(summary["cpu_one_minute_max_millicores"])

    def test_delivery_counts_reject_missing_and_duplicate_units(self) -> None:
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.evaluate_delivery_counts(["a", "b"], {"a": 4}, 4, 3, 0, "spans")
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.evaluate_delivery_counts(
                ["a", "b"], {"a": 4, "b": 5}, 4, 0, 0, "spans",
            )

    def test_sentinel_verifier_rejects_unsafe_evidence(self) -> None:
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.verify_sentinels_absent(
                [{"authorization": "acceptance-secret"}], ["acceptance-secret"],
            )


if __name__ == "__main__":
    unittest.main()
