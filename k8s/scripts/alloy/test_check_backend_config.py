#!/usr/bin/env python3
"""Pytest coverage for the Prometheus receiver and disposable-backend policy."""

from __future__ import annotations

import re
import signal
from pathlib import Path
from unittest import mock

import pytest

import backend_ingestion as backend_harness
import check_backend_config as backend_check


class TestBackendConfig:
    @pytest.mark.parametrize(("signum", "status"), ((signal.SIGINT, 130), (signal.SIGTERM, 143)))
    def test_main_preserves_parent_interruption_with_cleanup_failure(
        self, tmp_path: Path, capsys, signum: int, status: int
    ) -> None:
        # given
        config = tmp_path / "config.alloy"
        config.write_text("", encoding="utf-8")
        service = tmp_path / "service"
        service.mkdir()
        probe = tmp_path / "probe.py"
        probe.touch()
        harness = mock.Mock(containers=[])
        harness.create.side_effect = backend_harness.alloy_runtime.Interrupted(signum)
        harness.cleanup.side_effect = backend_harness.HarnessError("cleanup failed")

        # when
        with (
            mock.patch.object(backend_harness, "write_configs"),
            mock.patch.object(backend_harness, "DockerHarness", return_value=harness),
        ):
            result = backend_harness.main(
                [
                    "--config",
                    str(config),
                    "--service-root",
                    str(service),
                    "--service-probe",
                    str(probe),
                    "--request-histogram-buckets",
                    "15",
                ]
            )

        # then
        assert result == status
        assert "cleanup failed" in capsys.readouterr().err
        harness.cleanup.assert_called_once()

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
            pytest.fail(f"unexpected Docker command: {arguments}")

        with mock.patch.object(backend_harness.subprocess, "run", side_effect=run):
            with pytest.raises(
                backend_harness.HarnessError,
                match=rf"could not remove container {container}.*container remains after cleanup",
            ):
                harness.cleanup()

        assert [container] == harness.containers
        assert calls
        assert all(call[1]["timeout"] == backend_harness.CLEANUP_TIMEOUT for call in calls)

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
            pytest.fail(f"unexpected Docker command: {arguments}")

        with mock.patch.object(backend_harness.subprocess, "run", side_effect=run):
            with pytest.raises(
                backend_harness.HarnessError,
                match=rf"could not remove network {harness.network}.*network remains after cleanup",
            ):
                harness.cleanup()

        assert harness.network_owned
        assert calls
        assert all(call[1]["timeout"] == backend_harness.CLEANUP_TIMEOUT for call in calls)

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
            pytest.fail(f"unexpected Docker command: {arguments}")

        with mock.patch.object(backend_harness.subprocess, "run", side_effect=run):
            harness.cleanup()

        assert [] == harness.containers
        assert not harness.network_owned
        assert calls
        assert all(call[1]["timeout"] == backend_harness.CLEANUP_TIMEOUT for call in calls)

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

            with (
                mock.patch.object(backend_harness, "write_configs"),
                mock.patch.object(backend_harness, "DockerHarness", return_value=harness),
            ):
                with pytest.raises(
                    backend_harness.HarnessError,
                    match="(?s)primary execution failure.*cleanup also failed:.*network remains",
                ) as raised:
                    backend_harness.execute(args)

        assert primary is raised.value.__cause__

    def test_remote_write_samples_ignore_metadata_and_select_endpoint(self) -> None:
        exposition = """
# HELP prometheus_remote_storage_samples_failed_total Total number of failed samples.
# TYPE prometheus_remote_storage_samples_failed_total counter
prometheus_remote_storage_samples_failed_total{remote_name="primary",url="http://prometheus:9090/api/v1/write"} 0
prometheus_remote_storage_samples_failed_total{remote_name="other",url="http://other:9090/api/v1/write"} 7
prometheus_remote_storage_samples_pending{remote_name="primary",url="http://prometheus:9090/api/v1/write"} 3
"""
        assert 0 == backend_harness.remote_write_sample_value(
            exposition,
            backend_harness.REMOTE_WRITE_FAILED_METRIC,
            backend_harness.DISPOSABLE_REMOTE_WRITE_ENDPOINT,
        )
        assert 3 == backend_harness.remote_write_sample_value(
            exposition,
            backend_harness.REMOTE_WRITE_PENDING_METRIC,
            backend_harness.DISPOSABLE_REMOTE_WRITE_ENDPOINT,
        )

    def test_remote_write_sample_requires_one_endpoint_series(self) -> None:
        sample = (
            'prometheus_remote_storage_samples_pending{remote_name="primary",'
            'url="http://prometheus:9090/api/v1/write"} 1\n'
        )
        with pytest.raises(backend_harness.HarnessError):
            backend_harness.remote_write_sample_value(
                "# HELP prometheus_remote_storage_samples_pending Pending samples.\n",
                backend_harness.REMOTE_WRITE_PENDING_METRIC,
                backend_harness.DISPOSABLE_REMOTE_WRITE_ENDPOINT,
            )
        with pytest.raises(backend_harness.HarnessError):
            backend_harness.remote_write_sample_value(
                sample + sample,
                backend_harness.REMOTE_WRITE_PENDING_METRIC,
                backend_harness.DISPOSABLE_REMOTE_WRITE_ENDPOINT,
            )

    def test_values_require_remote_write_and_reject_native_otlp(self) -> None:
        valid = """prometheus:
  prometheusSpec:
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
            with pytest.raises(backend_check.ValidationError):
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
        with pytest.raises(backend_check.ValidationError):
            backend_check.validate_rendered_prometheus(
                rendered.replace("enableOTLPReceiver: false", "enableOTLPReceiver: true")
            )

    def test_disposable_images_are_service_neutral_and_pinned(self) -> None:
        assert {
            "alloy",
            "prometheus",
            "loki",
            "tempo",
            "zipkin_bridge",
            "zipkin",
            "postgres",
        } == set(backend_check.DISPOSABLE_IMAGES)
        for name, image in backend_check.DISPOSABLE_IMAGES.items():
            assert re.search(r"(@sha256:[0-9a-f]{64}|:[v0-9][A-Za-z0-9.-]*)$", image), name

    def test_disposable_config_reuses_policy_for_selected_service(self) -> None:
        source = (Path(__file__).parents[2] / "overlays/qa/infra/alloy/config.alloy").read_text(
            encoding="utf-8"
        )
        config = backend_harness.build_alloy_config(source, "product", "/fixture/service.log")
        assert 'discovery.kubernetes "qa_service_pods"' not in config
        assert 'otelcol.processor.k8sattributes "qa_services"' not in config
        assert 'otelcol.processor.transform "qa_services"' in config
        assert 'loki.source.file "logs"' in config
        assert "local.file_match.fixture_service_logs.targets" in config
        assert 'service_json_logs "product"' in config
        assert 'endpoint = "tempo:4317"' in config
        assert 'url = "http://prometheus:9090/api/v1/write"' in config

    def test_synthetic_fixture_follows_service_conventions(self) -> None:
        payload = backend_harness.synthetic_metric_payload(7, 2_000_000_000)
        resource = payload["resourceMetrics"][0]["resource"]["attributes"]
        values = {entry["key"]: entry["value"]["stringValue"] for entry in resource}
        assert "synthetic-service" == values["service.name"]
        metrics = payload["resourceMetrics"][0]["scopeMetrics"][0]["metrics"]
        assert ["insurance_hub.synthetic.requests", "insurance_hub.synthetic.request.duration"] == [
            metric["name"] for metric in metrics
        ]
        histogram = metrics[1]["histogram"]["dataPoints"][0]
        assert len(histogram["explicitBounds"]) + 1 == len(histogram["bucketCounts"])
