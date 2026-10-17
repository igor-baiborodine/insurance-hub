#!/usr/bin/env python3
"""Unit tests for the Prometheus receiver and disposable-backend policy."""

from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path
from unittest import mock


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
    def test_cleanup_retains_and_reports_container_removal_failure(self) -> None:
        harness = backend_harness.DockerHarness("fixture", Path("/tmp/fixture"))
        container = "fixture-alloy"
        harness.containers.append(container)
        calls = []

        def run(arguments, **kwargs):
            calls.append((arguments, kwargs))
            if arguments[1:4] == ["rm", "-f", container]:
                raise backend_harness.subprocess.TimeoutExpired(
                    arguments, timeout=kwargs["timeout"]
                )
            if arguments[1:3] == ["container", "ls"]:
                return backend_harness.subprocess.CompletedProcess(
                    arguments, 0, stdout=f"{container}\n", stderr=""
                )
            self.fail(f"unexpected Docker command: {arguments}")

        with mock.patch.object(backend_harness.subprocess, "run", side_effect=run):
            with self.assertRaisesRegex(
                backend_harness.HarnessError,
                rf"could not remove container {container}.*container remains after cleanup",
            ):
                harness.cleanup()

        self.assertEqual([container], harness.containers)
        self.assertTrue(calls)
        self.assertTrue(
            all(call[1]["timeout"] == backend_harness.CLEANUP_TIMEOUT for call in calls)
        )

    def test_cleanup_retains_and_reports_network_removal_failure(self) -> None:
        harness = backend_harness.DockerHarness("fixture", Path("/tmp/fixture"))
        harness.network_owned = True
        calls = []

        def run(arguments, **kwargs):
            calls.append((arguments, kwargs))
            if arguments[1:3] == ["network", "ls"]:
                return backend_harness.subprocess.CompletedProcess(
                    arguments, 0, stdout=f"{harness.network}\n", stderr=""
                )
            if arguments[1:3] == ["network", "rm"]:
                return backend_harness.subprocess.CompletedProcess(
                    arguments, 1, stdout="", stderr="network is in use"
                )
            self.fail(f"unexpected Docker command: {arguments}")

        with mock.patch.object(backend_harness.subprocess, "run", side_effect=run):
            with self.assertRaisesRegex(
                backend_harness.HarnessError,
                rf"could not remove network {harness.network}.*network remains after cleanup",
            ):
                harness.cleanup()

        self.assertTrue(harness.network_owned)
        self.assertTrue(calls)
        self.assertTrue(
            all(call[1]["timeout"] == backend_harness.CLEANUP_TIMEOUT for call in calls)
        )

    def test_cleanup_clears_absent_partial_setup_ownership(self) -> None:
        harness = backend_harness.DockerHarness("fixture", Path("/tmp/fixture"))
        harness.containers.append("fixture-alloy")
        harness.network_owned = True
        calls = []

        def run(arguments, **kwargs):
            calls.append((arguments, kwargs))
            if arguments[1:3] in (["container", "ls"], ["network", "ls"]):
                return backend_harness.subprocess.CompletedProcess(
                    arguments, 0, stdout="", stderr=""
                )
            self.fail(f"unexpected Docker command: {arguments}")

        with mock.patch.object(backend_harness.subprocess, "run", side_effect=run):
            harness.cleanup()

        self.assertEqual([], harness.containers)
        self.assertFalse(harness.network_owned)
        self.assertTrue(calls)
        self.assertTrue(
            all(call[1]["timeout"] == backend_harness.CLEANUP_TIMEOUT for call in calls)
        )

    def test_cleanup_failure_preserves_primary_failure(self) -> None:
        with backend_harness.tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config = root / "config.alloy"
            service_root = root / "service"
            probe = root / "probe.py"
            config.write_text("", encoding="utf-8")
            service_root.mkdir()
            probe.touch()
            args = backend_harness.argparse.Namespace(
                config=config,
                service_root=service_root,
                service_probe=probe,
                service_domain="product",
                request_histogram_buckets=15,
            )
            primary = backend_harness.HarnessError("primary execution failure")
            harness = mock.Mock()
            harness.containers = []
            harness.create.side_effect = primary
            harness.cleanup.side_effect = backend_harness.HarnessError(
                "network remains after cleanup"
            )

            with mock.patch.object(backend_harness, "write_configs"), mock.patch.object(
                backend_harness, "DockerHarness", return_value=harness
            ):
                with self.assertRaisesRegex(
                    backend_harness.HarnessError,
                    "(?s)primary execution failure.*cleanup also failed:.*network remains",
                ) as raised:
                    backend_harness.execute(args)

        self.assertIs(primary, raised.exception.__cause__)

    def test_remote_write_samples_ignore_metadata_and_select_endpoint(self) -> None:
        exposition = '''
# HELP prometheus_remote_storage_samples_failed_total Total number of failed samples.
# TYPE prometheus_remote_storage_samples_failed_total counter
prometheus_remote_storage_samples_failed_total{remote_name="primary",url="http://prometheus:9090/api/v1/write"} 0
prometheus_remote_storage_samples_failed_total{remote_name="other",url="http://other:9090/api/v1/write"} 7
prometheus_remote_storage_samples_pending{remote_name="primary",url="http://prometheus:9090/api/v1/write"} 3
'''
        self.assertEqual(
            0,
            backend_harness.remote_write_sample_value(
                exposition,
                backend_harness.REMOTE_WRITE_FAILED_METRIC,
                backend_harness.DISPOSABLE_REMOTE_WRITE_ENDPOINT,
            ),
        )
        self.assertEqual(
            3,
            backend_harness.remote_write_sample_value(
                exposition,
                backend_harness.REMOTE_WRITE_PENDING_METRIC,
                backend_harness.DISPOSABLE_REMOTE_WRITE_ENDPOINT,
            ),
        )

    def test_remote_write_sample_requires_one_endpoint_series(self) -> None:
        sample = (
            'prometheus_remote_storage_samples_pending{remote_name="primary",'
            'url="http://prometheus:9090/api/v1/write"} 1\n'
        )
        with self.assertRaises(backend_harness.HarnessError):
            backend_harness.remote_write_sample_value(
                "# HELP prometheus_remote_storage_samples_pending Pending samples.\n",
                backend_harness.REMOTE_WRITE_PENDING_METRIC,
                backend_harness.DISPOSABLE_REMOTE_WRITE_ENDPOINT,
            )
        with self.assertRaises(backend_harness.HarnessError):
            backend_harness.remote_write_sample_value(
                sample + sample,
                backend_harness.REMOTE_WRITE_PENDING_METRIC,
                backend_harness.DISPOSABLE_REMOTE_WRITE_ENDPOINT,
            )

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
