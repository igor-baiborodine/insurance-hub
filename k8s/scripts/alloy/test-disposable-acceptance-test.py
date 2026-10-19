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
from unittest import mock


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

    def test_alloy_resource_delta_reports_observed_failure_values(self) -> None:
        baseline = [
            acceptance.ResourceSample(1, 20, 80, 0),
            acceptance.ResourceSample(2, 20, 80, 0),
        ]
        loaded = [
            acceptance.ResourceSample(3, 121, 90, 0),
            acceptance.ResourceSample(4, 121, 90, 0),
        ]

        with self.assertRaisesRegex(
            acceptance.AcceptanceError,
            r"Alloy CPU p95 delta 101\.000m exceeds 100\.000m "
            r"\(baseline 20\.000m, loaded 121\.000m\)",
        ):
            acceptance.verify_alloy_resource_delta(
                baseline, loaded, self.profile["alloy_resources"]
            )

    def test_alloy_resource_delta_returns_paired_evidence(self) -> None:
        baseline = [
            acceptance.ResourceSample(1, 20, 80, 0),
            acceptance.ResourceSample(2, 20, 80, 0),
        ]
        loaded = [
            acceptance.ResourceSample(3, 100, 100, 0),
            acceptance.ResourceSample(4, 100, 100, 0),
        ]

        evidence = acceptance.verify_alloy_resource_delta(
            baseline, loaded, self.profile["alloy_resources"]
        )

        self.assertEqual(80, evidence["alloy_cpu_p95_delta_millicores"])
        self.assertEqual(20, evidence["alloy_memory_max_delta_mib"])

    def test_accepted_metric_points_uses_prometheus_scrape(self) -> None:
        response = {
            "data": {
                "result": [
                    {"value": [1234, "145"]},
                ],
            },
        }

        with mock.patch.object(acceptance.backend, "prom_query", return_value=response) as query:
            value = acceptance.accepted_metric_points(9090)

        self.assertEqual(145, value)
        query.assert_called_once_with(
            9090,
            'sum(otelcol_receiver_accepted_metric_points_total{transport="grpc"})',
        )

    def test_accepted_metric_points_is_zero_before_first_export(self) -> None:
        with mock.patch.object(
            acceptance.backend, "prom_query", return_value={"data": {"result": []}}
        ):
            value = acceptance.accepted_metric_points(9090)

        self.assertEqual(0, value)

    def test_delivery_counts_reject_missing_and_duplicate_units(self) -> None:
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.evaluate_delivery_counts(["a", "b"], {"a": 4}, 4, 3, 0, "spans")
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.evaluate_delivery_counts(
                ["a", "b"], {"a": 4, "b": 5}, 4, 0, 0, "spans",
            )

    def test_loki_trace_counts_batch_more_than_series_limit(self) -> None:
        trace_ids = acceptance.deterministic_trace_ids("batch", 501)
        requests = []

        def request(url: str, *, timeout: float):
            requests.append((url, timeout))
            offset = (len(requests) - 1) * acceptance.LOKI_TRACE_QUERY_BATCH_SIZE
            batch = trace_ids[offset:offset + acceptance.LOKI_TRACE_QUERY_BATCH_SIZE]
            results = [
                {"stream": {"trace_id": trace_id}, "values": [["0", "{}"]]}
                for trace_id in batch
            ]
            return 200, json.dumps({"data": {"result": results}}).encode("utf-8")

        with mock.patch.object(acceptance.backend, "request", side_effect=request):
            counts = acceptance.query_loki_trace_counts(
                3100, "product-instance", trace_ids, acceptance.time.monotonic() + 30,
            )

        self.assertEqual({trace_id: 1 for trace_id in trace_ids}, counts)
        self.assertEqual(6, len(requests))
        for url, timeout in requests:
            query = acceptance.urllib.parse.parse_qs(
                acceptance.urllib.parse.urlparse(url).query
            )["query"][0]
            self.assertIn('| service_instance_id="product-instance"', query)
            self.assertIn("| trace_id=~", query)
            self.assertIn("/loki/api/v1/query_range?", url)
            parameters = acceptance.urllib.parse.parse_qs(
                acceptance.urllib.parse.urlparse(url).query
            )
            self.assertEqual(
                [str(acceptance.LOKI_TRACE_QUERY_RESULT_LIMIT)], parameters["limit"]
            )
            self.assertIn("start", parameters)
            self.assertIn("end", parameters)
            self.assertGreater(timeout, 0)
            self.assertLessEqual(timeout, acceptance.LOKI_TRACE_QUERY_TIMEOUT)

    def test_batched_loki_counts_preserve_missing_and_duplicate_detection(self) -> None:
        trace_ids = acceptance.deterministic_trace_ids("counts", 501)
        responses = []
        for offset in range(0, len(trace_ids), acceptance.LOKI_TRACE_QUERY_BATCH_SIZE):
            batch = trace_ids[offset:offset + acceptance.LOKI_TRACE_QUERY_BATCH_SIZE]
            results = [
                {
                    "stream": {"trace_id": trace_id},
                    "values": (
                        [["0", "{}"], ["1", "{}"]]
                        if trace_id == trace_ids[0] else [["0", "{}"]]
                    ),
                }
                for trace_id in batch if trace_id != trace_ids[-1]
            ]
            responses.append(
                (200, json.dumps({"data": {"result": results}}).encode("utf-8"))
            )

        with mock.patch.object(acceptance.backend, "request", side_effect=responses):
            counts = acceptance.query_loki_trace_counts(
                3100, "product-instance", trace_ids, acceptance.time.monotonic() + 30,
            )

        with self.assertRaisesRegex(acceptance.AcceptanceError, "lost 1"):
            acceptance.evaluate_delivery_counts(
                trace_ids, counts, 1, 0, 1, "Loki completion logs",
            )
        with self.assertRaisesRegex(acceptance.AcceptanceError, "duplicated 1"):
            acceptance.evaluate_delivery_counts(
                trace_ids, counts, 1, 1, 0, "Loki completion logs",
            )

    def test_loki_trace_count_failure_includes_bounded_sanitized_detail(self) -> None:
        detail = b"maximum series reached\n\x00" + b"x" * 600
        with mock.patch.object(acceptance.backend, "request", return_value=(400, detail)):
            with self.assertRaises(acceptance.AcceptanceError) as raised:
                acceptance.query_loki_trace_counts(
                    3100, "product-instance", ["a"], acceptance.time.monotonic() + 30,
                )

        message = str(raised.exception)
        self.assertIn("Loki trace-log query returned HTTP 400: maximum series reached", message)
        self.assertNotIn("\n", message)
        self.assertNotIn("\x00", message)
        self.assertLessEqual(
            len(message.split(": ", 1)[1]), acceptance.LOKI_ERROR_DETAIL_MAX_CHARS,
        )

    def test_loki_trace_query_rejects_truncated_batch(self) -> None:
        results = [{
            "stream": {"trace_id": "a"},
            "values": [
                [str(index), "{}"]
                for index in range(acceptance.LOKI_TRACE_QUERY_RESULT_LIMIT)
            ],
        }]
        response = (200, json.dumps({"data": {"result": results}}).encode("utf-8"))
        with mock.patch.object(acceptance.backend, "request", return_value=response):
            with self.assertRaisesRegex(acceptance.AcceptanceError, "result limit"):
                acceptance.query_loki_trace_counts(
                    3100, "product-instance", ["a"], acceptance.time.monotonic() + 30,
                )

    def test_log_delivery_failure_identifies_workload_phase(self) -> None:
        trace_id = acceptance.deterministic_trace_ids("healthy", 1)[0]
        with mock.patch.object(
            acceptance, "query_loki_trace_counts", return_value={trace_id: 2},
        ):
            with self.assertRaisesRegex(
                acceptance.AcceptanceError, "Loki healthy completion logs duplicated 1",
            ):
                acceptance.verify_log_delivery(
                    3100, "product-instance", "healthy", 1, 0, 0, 1,
                )

    def test_sentinel_verifier_rejects_unsafe_evidence(self) -> None:
        with self.assertRaises(acceptance.AcceptanceError):
            acceptance.verify_sentinels_absent(
                [{"authorization": "acceptance-secret"}], ["acceptance-secret"],
            )

    def test_metric_series_identity_rejects_varied_input_cardinality(self) -> None:
        baseline = acceptance.metric_series_identity(
            [{"__name__": "requests_total", "outcome": "success"}]
        )
        varied = acceptance.metric_series_identity(
            [
                {"__name__": "requests_total", "outcome": "success"},
                {
                    "__name__": "requests_total",
                    "outcome": "success",
                    "product_code": "unsafe-variable-label",
                },
            ]
        )

        with self.assertRaisesRegex(
            acceptance.AcceptanceError, "changed Product metric cardinality"
        ):
            acceptance.verify_metric_series_stability(baseline, varied, 800)

    def test_alloy_healthy_delivery_rejects_exporter_failure(self) -> None:
        exposition = """
otelcol_exporter_send_failed_spans_total{component_id="otelcol.exporter.otlp.tempo"} 1
otelcol_exporter_sent_spans_total{component_id="otelcol.exporter.otlp.tempo"} 4
otelcol_exporter_sent_spans_total{component_id="otelcol.exporter.otlp.zipkin_bridge"} 4
"""
        with mock.patch.object(
            acceptance.backend, "read_alloy_metrics", return_value=exposition
        ):
            with self.assertRaisesRegex(
                acceptance.AcceptanceError, "healthy Alloy delivery reported"
            ):
                acceptance.verify_alloy_healthy_delivery(12345, 4)


if __name__ == "__main__":
    unittest.main()
