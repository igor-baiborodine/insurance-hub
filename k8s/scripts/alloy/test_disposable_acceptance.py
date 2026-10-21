#!/usr/bin/env python3
"""Pytest coverage for Product acceptance policy and controlled verifier defects."""

from __future__ import annotations

import copy
import json
import tempfile
import time
from pathlib import Path
from unittest import mock

import pytest

import disposable_acceptance as acceptance

PROFILE_PATH = Path(__file__).parents[2] / "observability/services/product/acceptance.json"


class TestAcceptanceVerifier:
    def setup_method(self) -> None:
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
            with pytest.raises(acceptance.AcceptanceError):
                acceptance.load_profile(path)

    def test_load_rejects_missing_path(self) -> None:
        changed = copy.deepcopy(self.load)
        del changed["paths"]["grpc_get"]
        with pytest.raises(acceptance.AcceptanceError):
            acceptance.verify_load(changed, 2, self.profile)

    def test_load_rejects_duplicate_or_failed_count(self) -> None:
        changed = copy.deepcopy(self.load)
        changed["paths"]["http_list"]["requests"] = 3
        with pytest.raises(acceptance.AcceptanceError):
            acceptance.verify_load(changed, 2, self.profile)

    def test_load_rejects_stale_latency(self) -> None:
        changed = copy.deepcopy(self.load)
        changed["paths"]["grpc_list"]["p95Millis"] = 251
        with pytest.raises(acceptance.AcceptanceError):
            acceptance.verify_load(changed, 2, self.profile)

    def test_resources_reject_unsafe_nonfinite_or_over_budget_values(self) -> None:
        samples = [
            acceptance.ResourceSample(1, 10, 20, 0),
            acceptance.ResourceSample(2, 201, 20, 0),
        ]
        with pytest.raises(acceptance.AcceptanceError):
            acceptance.verify_resources(samples, self.profile["product_resources"], "Product")
        samples[1] = acceptance.ResourceSample(2, float("nan"), 20, 0)
        with pytest.raises(acceptance.AcceptanceError):
            acceptance.verify_resources(samples, self.profile["product_resources"], "Product")

    def test_overhead_rejects_incomplete_pairing(self) -> None:
        with pytest.raises(acceptance.AcceptanceError):
            acceptance.verify_overhead([self.load], [self.load], self.profile)

    def test_docker_memory_parser_prefers_complete_unit_suffix(self) -> None:
        assert 72.75 == acceptance.parse_memory_mib("72.75MiB")
        assert 1024 == acceptance.parse_memory_mib("1GiB")

    def test_resource_summary_preserves_observed_budget_values(self) -> None:
        samples = [
            acceptance.ResourceSample(float(index), float(index), float(index + 10), index / 100)
            for index in range(1, 62)
        ]
        summary = acceptance.resource_summary(samples)
        assert 61 == summary["samples"]
        assert 60 == summary["duration_seconds"]
        assert 58 == summary["cpu_p95_millicores"]
        assert 71 == summary["memory_max_mib"]
        assert 0.61 == summary["cpu_throttling_ratio_max"]
        assert summary["cpu_one_minute_max_millicores"] is not None

    def test_alloy_resource_delta_reports_observed_failure_values(self) -> None:
        baseline = [
            acceptance.ResourceSample(1, 20, 80, 0),
            acceptance.ResourceSample(2, 20, 80, 0),
        ]
        loaded = [
            acceptance.ResourceSample(3, 121, 90, 0),
            acceptance.ResourceSample(4, 121, 90, 0),
        ]

        with pytest.raises(
            acceptance.AcceptanceError,
            match=r"Alloy CPU p95 delta 101\.000m exceeds 100\.000m "
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

        assert 80 == evidence["alloy_cpu_p95_delta_millicores"]
        assert 20 == evidence["alloy_memory_max_delta_mib"]

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

        assert 145 == value
        query.assert_called_once_with(
            9090,
            'sum(otelcol_receiver_accepted_metric_points_total{transport="grpc"})',
        )

    def test_accepted_metric_points_is_zero_before_first_export(self) -> None:
        with mock.patch.object(
            acceptance.backend, "prom_query", return_value={"data": {"result": []}}
        ):
            value = acceptance.accepted_metric_points(9090)

        assert 0 == value

    def test_delivery_counts_reject_missing_and_duplicate_units(self) -> None:
        with pytest.raises(acceptance.AcceptanceError):
            acceptance.evaluate_delivery_counts(["a", "b"], {"a": 4}, 4, 3, 0, "spans")
        with pytest.raises(acceptance.AcceptanceError):
            acceptance.evaluate_delivery_counts(
                ["a", "b"],
                {"a": 4, "b": 5},
                4,
                0,
                0,
                "spans",
            )

    def test_loki_trace_counts_batch_more_than_series_limit(self) -> None:
        trace_ids = acceptance.deterministic_trace_ids("batch", 501)
        requests = []

        def request(url: str, *, timeout: float):
            requests.append((url, timeout))
            offset = (len(requests) - 1) * acceptance.LOKI_TRACE_QUERY_BATCH_SIZE
            batch = trace_ids[offset : offset + acceptance.LOKI_TRACE_QUERY_BATCH_SIZE]
            results = [
                {"stream": {"trace_id": trace_id}, "values": [["0", "{}"]]} for trace_id in batch
            ]
            return 200, json.dumps({"data": {"result": results}}).encode("utf-8")

        with mock.patch.object(acceptance.backend, "request", side_effect=request):
            counts = acceptance.query_loki_trace_counts(
                3100,
                "product-instance",
                trace_ids,
                acceptance.time.monotonic() + 30,
            )

        assert {trace_id: 1 for trace_id in trace_ids} == counts
        assert 6 == len(requests)
        for url, timeout in requests:
            query = acceptance.urllib.parse.parse_qs(acceptance.urllib.parse.urlparse(url).query)[
                "query"
            ][0]
            assert '| service_instance_id="product-instance"' in query
            assert "| trace_id=~" in query
            assert "/loki/api/v1/query_range?" in url
            parameters = acceptance.urllib.parse.parse_qs(
                acceptance.urllib.parse.urlparse(url).query
            )
            assert [str(acceptance.LOKI_TRACE_QUERY_RESULT_LIMIT)] == parameters["limit"]
            assert "start" in parameters
            assert "end" in parameters
            assert timeout > 0
            assert timeout <= acceptance.LOKI_TRACE_QUERY_TIMEOUT

    def test_batched_loki_counts_preserve_missing_and_duplicate_detection(self) -> None:
        trace_ids = acceptance.deterministic_trace_ids("counts", 501)
        responses = []
        for offset in range(0, len(trace_ids), acceptance.LOKI_TRACE_QUERY_BATCH_SIZE):
            batch = trace_ids[offset : offset + acceptance.LOKI_TRACE_QUERY_BATCH_SIZE]
            results = [
                {
                    "stream": {"trace_id": trace_id},
                    "values": (
                        [["0", "{}"], ["1", "{}"]] if trace_id == trace_ids[0] else [["0", "{}"]]
                    ),
                }
                for trace_id in batch
                if trace_id != trace_ids[-1]
            ]
            responses.append((200, json.dumps({"data": {"result": results}}).encode("utf-8")))

        with mock.patch.object(acceptance.backend, "request", side_effect=responses):
            counts = acceptance.query_loki_trace_counts(
                3100,
                "product-instance",
                trace_ids,
                acceptance.time.monotonic() + 30,
            )

        with pytest.raises(acceptance.AcceptanceError, match="lost 1"):
            acceptance.evaluate_delivery_counts(
                trace_ids,
                counts,
                1,
                0,
                1,
                "Loki completion logs",
            )
        with pytest.raises(acceptance.AcceptanceError, match="duplicated 1"):
            acceptance.evaluate_delivery_counts(
                trace_ids,
                counts,
                1,
                1,
                0,
                "Loki completion logs",
            )

    def test_loki_trace_count_failure_includes_bounded_sanitized_detail(self) -> None:
        detail = b"maximum series reached\n\x00" + b"x" * 600
        with mock.patch.object(acceptance.backend, "request", return_value=(400, detail)):
            with pytest.raises(acceptance.AcceptanceError) as raised:
                acceptance.query_loki_trace_counts(
                    3100,
                    "product-instance",
                    ["a"],
                    acceptance.time.monotonic() + 30,
                )

        message = str(raised.value)
        assert "Loki trace-log query returned HTTP 400: maximum series reached" in message
        assert "\n" not in message
        assert "\x00" not in message
        assert len(message.split(": ", 1)[1]) <= acceptance.LOKI_ERROR_DETAIL_MAX_CHARS

    def test_loki_trace_query_rejects_truncated_batch(self) -> None:
        results = [
            {
                "stream": {"trace_id": "a"},
                "values": [
                    [str(index), "{}"] for index in range(acceptance.LOKI_TRACE_QUERY_RESULT_LIMIT)
                ],
            }
        ]
        response = (200, json.dumps({"data": {"result": results}}).encode("utf-8"))
        with mock.patch.object(acceptance.backend, "request", return_value=response):
            with pytest.raises(acceptance.AcceptanceError, match="result limit"):
                acceptance.query_loki_trace_counts(
                    3100,
                    "product-instance",
                    ["a"],
                    acceptance.time.monotonic() + 30,
                )

    def test_log_delivery_failure_identifies_workload_phase(self) -> None:
        trace_id = acceptance.deterministic_trace_ids("healthy", 1)[0]
        with mock.patch.object(
            acceptance,
            "query_loki_trace_counts",
            return_value={trace_id: 2},
        ):
            with pytest.raises(
                acceptance.AcceptanceError, match="Loki healthy completion logs duplicated 1"
            ):
                acceptance.verify_log_delivery(
                    3100,
                    "product-instance",
                    "healthy",
                    1,
                    0,
                    0,
                    1,
                )

    def test_trace_delivery_stops_scheduling_at_its_deadline(self) -> None:
        # given
        started = time.monotonic()

        # when / then
        with (
            mock.patch.object(acceptance, "tempo_span_count", return_value=None),
            pytest.raises(acceptance.AcceptanceError, match="Tempo spans lost"),
        ):
            acceptance.verify_trace_delivery(3200, "healthy", 64, 0, 0, 0.02)
        assert time.monotonic() - started < 0.15

    def test_tempo_lookup_uses_time_bounds_and_preserves_exact_span_count(self) -> None:
        # given
        trace_id = acceptance.deterministic_trace_ids("healthy", 1)[0]
        response = {
            "batches": [
                {"scopeSpans": [{"spans": [{}, {}]}]},
                {"scopeSpans": [{"spans": [{}, {}]}]},
            ]
        }

        # when
        with mock.patch.object(
            acceptance.backend, "request", return_value=(200, json.dumps(response).encode())
        ) as request:
            count = acceptance.tempo_span_count(3200, trace_id, 100, 200, 1)

        # then
        assert count == 4
        assert "start=100" in request.call_args.args[0]
        assert "end=200" in request.call_args.args[0]

    def test_sentinel_verifier_rejects_unsafe_evidence(self) -> None:
        with pytest.raises(acceptance.AcceptanceError):
            acceptance.verify_sentinels_absent(
                [{"authorization": "acceptance-secret"}],
                ["acceptance-secret"],
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

        with pytest.raises(acceptance.AcceptanceError, match="changed Product metric cardinality"):
            acceptance.verify_metric_series_stability(baseline, varied, 800)

    def test_alloy_healthy_delivery_rejects_exporter_failure(self) -> None:
        exposition = """
otelcol_exporter_send_failed_spans_total{component_id="otelcol.exporter.otlp.tempo"} 1
otelcol_exporter_sent_spans_total{component_id="otelcol.exporter.otlp.tempo"} 4
otelcol_exporter_sent_spans_total{component_id="otelcol.exporter.otlp.zipkin_bridge"} 4
"""
        with mock.patch.object(acceptance.backend, "read_alloy_metrics", return_value=exposition):
            with pytest.raises(acceptance.AcceptanceError, match="healthy Alloy delivery reported"):
                acceptance.verify_alloy_healthy_delivery(12345, 4)

    def test_start_product_reaps_log_follower_when_readiness_fails(self) -> None:
        harness = mock.Mock()
        harness.start.return_value = "product-test"
        harness.host_port.return_value = 18080
        log_process = mock.Mock()
        log_process.poll.return_value = None
        log_process.wait.return_value = 0

        with (
            tempfile.TemporaryDirectory() as temporary,
            mock.patch.object(acceptance.subprocess, "Popen", return_value=log_process) as popen,
            mock.patch.object(
                acceptance,
                "wait_product",
                side_effect=acceptance.AcceptanceError("Product did not become ready"),
            ),
            mock.patch.object(acceptance.alloy_runtime, "terminate_process_group") as terminate,
        ):
            with pytest.raises(acceptance.AcceptanceError, match="Product did not become ready"):
                acceptance.start_product(
                    harness,
                    Path(temporary),
                    "product-image",
                    "product-test",
                    True,
                    "instance-test",
                )

        log_handle = popen.call_args.kwargs["stdout"]
        docker_arguments = harness.start.call_args.kwargs["docker_arguments"]
        terminate.assert_called_once_with(log_process, graceful_seconds=5, teardown_seconds=10)
        assert log_handle.closed
        assert "--env-file" in docker_arguments
        assert acceptance.READER_PASSWORD not in docker_arguments

    def test_start_product_closes_log_handle_when_log_follower_fails(self) -> None:
        harness = mock.Mock()
        harness.start.return_value = "product-test"
        failure = OSError("docker logs failed")

        with (
            tempfile.TemporaryDirectory() as temporary,
            mock.patch.object(acceptance.subprocess, "Popen", side_effect=failure) as popen,
        ):
            with pytest.raises(OSError, match="docker logs failed") as raised:
                acceptance.start_product(
                    harness,
                    Path(temporary),
                    "product-image",
                    "product-test",
                    True,
                    "instance-test",
                )

        assert failure is raised.value
        assert popen.call_args.kwargs["stdout"].closed

    def test_stop_product_cleans_logging_and_container_after_stop_failure(self) -> None:
        harness = mock.Mock()
        harness.containers = ["product-test"]
        harness.command.side_effect = acceptance.backend.HarnessError("stop failed")
        log_process = mock.Mock()
        log_process.poll.return_value = None
        log_process.wait.return_value = 0
        log_handle = mock.Mock()
        log_handle.closed = False
        product = acceptance.ProductRun(
            container="product-test",
            http_port=18081,
            grpc_port=19090,
            health_port=18080,
            instance="instance-test",
            log_process=log_process,
            log_handle=log_handle,
        )

        with (
            mock.patch.object(acceptance.alloy_runtime, "terminate_process_group") as terminate,
            pytest.raises(acceptance.backend.HarnessError, match="stop failed"),
        ):
            acceptance.stop_product(harness, product, 10)

        terminate.assert_called_once_with(log_process, graceful_seconds=5, teardown_seconds=10)
        log_handle.close.assert_called_once_with()
        harness.remove.assert_called_once_with("product-test")

    def test_stop_product_reports_primary_and_cleanup_failures(self) -> None:
        harness = mock.Mock()
        harness.containers = ["product-test"]
        primary = acceptance.backend.HarnessError("stop failed")
        harness.command.side_effect = primary
        harness.remove.side_effect = acceptance.backend.HarnessError("remove failed")
        log_process = mock.Mock()
        log_process.poll.return_value = None
        log_process.wait.return_value = 0
        log_handle = mock.Mock()
        log_handle.closed = False
        log_handle.close.side_effect = OSError("close failed")
        product = acceptance.ProductRun(
            container="product-test",
            http_port=18081,
            grpc_port=19090,
            health_port=18080,
            instance="instance-test",
            log_process=log_process,
            log_handle=log_handle,
        )

        with (
            mock.patch.object(acceptance.alloy_runtime, "terminate_process_group") as terminate,
            pytest.raises(
                acceptance.AcceptanceError, match="stop failed.*close failed.*remove failed"
            ) as raised,
        ):
            acceptance.stop_product(harness, product, 10)

        assert primary is raised.value.__cause__
        terminate.assert_called_once_with(log_process, graceful_seconds=5, teardown_seconds=10)
        log_handle.close.assert_called_once_with()
        harness.remove.assert_called_once_with("product-test")

    def test_stop_product_retries_logging_after_container_was_removed(self) -> None:
        harness = mock.Mock()
        harness.containers = []
        log_process = mock.Mock()
        log_process.poll.return_value = 0
        log_process.wait.return_value = 0
        log_handle = mock.Mock()
        log_handle.closed = False
        product = acceptance.ProductRun(
            container="product-test",
            http_port=18081,
            grpc_port=19090,
            health_port=18080,
            instance="instance-test",
            log_process=log_process,
            log_handle=log_handle,
        )

        acceptance.stop_product(harness, product, 10)

        harness.command.assert_not_called()
        harness.remove.assert_not_called()
        log_process.terminate.assert_not_called()
        log_process.wait.assert_called_once_with(timeout=5)
        log_handle.close.assert_called_once_with()
