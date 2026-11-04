#!/usr/bin/env python3
"""Pytest coverage for Product observability target guards and cleanup."""

from __future__ import annotations

import dataclasses
import json
from argparse import Namespace
from contextlib import nullcontext
from pathlib import Path
from unittest import mock

import pytest
import verify_product_observability as verifier

from monitoring_assets import VerificationContract

ASSETS_ROOT = Path(__file__).parents[2] / "observability"


class TestProductObservabilityVerification:
    def setup_method(self) -> None:
        self.assets = verifier.load_assets(ASSETS_ROOT, "qa", "product")

    def test_configuration_resolves_exact_read_only_target(self) -> None:
        # when
        selected = verifier.verification_config(self.assets)

        # then
        assert "qa-svc" == selected.service_namespace
        assert "qa-product-api" == selected.deployment
        assert "qa-prometheus-kube-prometh-prometheus" == selected.prometheus_service

    def test_probe_selection_rejects_cross_service_and_unregistered_defaults(self) -> None:
        # given
        cross_contract = dataclasses.replace(
            self.assets.contract,
            verification=VerificationContract(
                probe="policy-search",
                expected_signals=self.assets.contract.verification.expected_signals,
            ),
        )
        cross_service = dataclasses.replace(self.assets, contract=cross_contract)

        # when / then
        with pytest.raises(verifier.VerificationError, match="differs from the selected service"):
            verifier.select_probe(cross_service)

        unregistered_contract = dataclasses.replace(
            cross_contract,
            service="missing-service",
            verification=VerificationContract(
                probe="missing-service",
                expected_signals=self.assets.contract.verification.expected_signals,
            ),
        )
        unregistered = dataclasses.replace(
            self.assets,
            service="missing-service",
            contract=unregistered_contract,
        )
        with pytest.raises(verifier.VerificationError, match="is not registered"):
            verifier.select_probe(unregistered)

    def test_target_rejects_non_qa_or_malformed_revision(self) -> None:
        # when / then
        with pytest.raises(verifier.VerificationError, match="source revision"):
            verifier.KubernetesTarget(self.assets, "short")
        changed = dataclasses.replace(self.assets, environment_name="local-dev")
        with pytest.raises(verifier.VerificationError, match="QA-only"):
            verifier.KubernetesTarget(changed, "a" * 40)

    def test_coordinates_reject_wrong_active_context_before_resource_reads(self) -> None:
        # given
        target = verifier.KubernetesTarget(self.assets, "a" * 40)

        # when / then
        with (
            mock.patch.object(verifier, "run", return_value="kind-local-dev-insurance-hub\n"),
            mock.patch.object(target, "json") as kubernetes,
        ):
            with pytest.raises(verifier.VerificationError, match="active Kubernetes context"):
                target.coordinates()
            kubernetes.assert_not_called()

    def test_monitoring_assets_accept_reviewed_alert_inventory(self) -> None:
        # given
        target = verifier.KubernetesTarget(self.assets, "a" * 40)
        expected_alerts = set(self.assets.contract.alerts.durations)
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
                        "uid": self.assets.contract.dashboard.uid,
                        "editable": False,
                    }
                )
            }
        }

        # when
        with mock.patch.object(target, "json", side_effect=[rule, dashboard]):
            observed = target.monitoring_assets()

        # then
        assert {
            "prometheus_rule": "qa-product-service",
            "dashboard_config_map": "qa-product-dashboard",
        } == observed

    def test_monitoring_assets_reject_alert_inventory_mismatch(self) -> None:
        # given
        target = verifier.KubernetesTarget(self.assets, "a" * 40)
        expected_alerts = sorted(self.assets.contract.alerts.durations)
        rule = {
            "spec": {
                "groups": [
                    {"rules": [{"alert": name} for name in expected_alerts[1:]]},
                ]
            }
        }

        # when / then
        with mock.patch.object(target, "json", return_value=rule):
            with pytest.raises(
                verifier.VerificationError,
                match="live selected-service alert inventory differs from reviewed assets",
            ):
                target.monitoring_assets()

    def test_flux_revision_rejects_wrong_source_before_workload(self) -> None:
        # given
        target = verifier.KubernetesTarget(self.assets, "a" * 40)
        source = {
            "status": {
                "artifact": {"revision": "main@sha1:" + "b" * 40},
                "conditions": [{"type": "Ready", "status": "True"}],
            }
        }
        reconciler = {
            "status": {
                "lastAppliedRevision": "main@sha1:" + "b" * 40,
                "conditions": [{"type": "Ready", "status": "True"}],
            }
        }

        # when / then
        with mock.patch.object(target, "json", side_effect=[source, reconciler]):
            with pytest.raises(verifier.VerificationError, match="differs from the requested"):
                target.flux_revision()

    @pytest.mark.parametrize("mode", ["pre-provision", "enabled"])
    def test_deployment_uses_selected_identity_and_deployment_selector(self, mode: str) -> None:
        # given
        target = verifier.KubernetesTarget(self.assets, "a" * 40)
        deployment = {
            "spec": {
                "replicas": 1,
                "selector": {"matchLabels": {"app.kubernetes.io/name": "product-api"}},
                "template": {
                    "spec": {
                        "containers": [{"envFrom": [{"configMapRef": {"name": "product-config"}}]}]
                    }
                },
            },
            "status": {"readyReplicas": 1},
        }
        config_map = {
            "data": {
                "OTEL_ENABLED": "true",
                "SERVICE_NAME": "product-service",
                "DEPLOYMENT_ENVIRONMENT": "qa",
                "OTEL_EXPORTER_OTLP_ENDPOINT": "http://qa-alloy.qa-monitoring.svc:4318",
            }
        }
        pods = {
            "items": [
                {
                    "metadata": {"uid": "pod-uid"},
                    "status": {
                        "containerStatuses": [
                            {"ready": True, "imageID": "registry/product@sha256:abc"}
                        ]
                    },
                }
            ]
        }

        # when
        with mock.patch.object(target, "json", side_effect=[deployment, config_map, pods]) as query:
            observed = target.deployment(mode)

        # then
        assert observed["pods"] == [
            {"pod_uid": "pod-uid", "image_id": "registry/product@sha256:abc"}
        ]
        assert query.call_args_list[-1] == mock.call(
            "qa-svc", "get", "pods", "-l", "app.kubernetes.io/name=product-api"
        )

    def test_pre_provision_checks_enabled_workload_without_monitoring_assets(self) -> None:
        # given
        args = Namespace(
            assets_root=ASSETS_ROOT,
            environment="qa",
            service="product",
            mode="pre-provision",
            source_revision="a" * 40,
        )
        target = mock.Mock(spec=verifier.KubernetesTarget)
        target.flux_revision.return_value = "main@sha1:" + "a" * 40
        target.deployment.return_value = {
            "pods": [{"pod_uid": "pod-uid", "image_id": "registry/product@sha256:abc"}],
            "settings": {},
        }
        target.monitoring_namespace = "qa-monitoring"
        services = mock.Mock(return_value=[("product", "qa-svc", "qa-product-api", 80)])
        workload = mock.Mock(return_value=1)
        enabled_signals = mock.Mock()
        probe = verifier.ServiceProbe(
            service="product",
            services=services,
            workload=workload,
            enabled_signals=enabled_signals,
            result_key="product_rows",
        )

        # when
        with (
            mock.patch.object(verifier, "load_assets", return_value=self.assets),
            mock.patch.object(verifier, "select_probe", return_value=probe),
            mock.patch.object(verifier, "KubernetesTarget", return_value=target),
            mock.patch.object(
                verifier, "forwards", return_value=nullcontext({"product": 1001})
            ) as forwarded,
        ):
            observed = verifier.execute(args)

        # then
        target.coordinates.assert_called_once_with()
        target.flux_revision.assert_called_once_with()
        target.deployment.assert_called_once_with("pre-provision")
        target.monitoring_assets.assert_not_called()
        forwarded.assert_called_once_with(target, [("product", "qa-svc", "qa-product-api", 80)])
        workload.assert_called_once_with({"product": 1001}, observed["trace_id"], {})
        enabled_signals.assert_not_called()
        assert observed["mode"] == "pre-provision"
        assert observed["monitoring"] == {}
        assert observed["signals"] == {}
        assert observed["product_rows"] == 1

    def test_deployment_rejects_missing_service_identity(self) -> None:
        # given
        target = verifier.KubernetesTarget(self.assets, "a" * 40)
        deployment = {
            "spec": {
                "replicas": 1,
                "selector": {"matchLabels": {"app": "product"}},
                "template": {
                    "spec": {
                        "containers": [
                            {
                                "env": [{"name": "OTEL_ENABLED", "value": "false"}],
                                "envFrom": [],
                            }
                        ]
                    }
                },
            },
            "status": {"readyReplicas": 1},
        }

        # when / then
        with mock.patch.object(target, "json", return_value=deployment):
            with pytest.raises(verifier.VerificationError, match="service identity differs"):
                target.deployment("disabled")

    def test_backend_result_rejects_malformed_payload(self) -> None:
        with pytest.raises(verifier.VerificationError, match="backend query result is malformed"):
            verifier.query_results({"data": {"result": "not-an-array"}})

    def test_backend_wait_is_bounded(self) -> None:
        with mock.patch.object(verifier.time, "monotonic", side_effect=[0.0, 1.0]):
            with pytest.raises(verifier.VerificationError, match="timed out waiting"):
                verifier.wait_for("test signal", lambda: [], timeout=0.5)

    def test_forwards_close_every_started_child_after_partial_failure(self) -> None:
        # given
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
        # when / then
        with (
            mock.patch.object(verifier.subprocess, "Popen", side_effect=[first, second]),
            mock.patch.object(verifier.socket, "create_connection", return_value=nullcontext()),
            mock.patch.object(verifier.monitoring_runtime, "terminate_process_group") as terminate,
        ):
            with pytest.raises(verifier.VerificationError, match="health exited"):
                with verifier.forwards(target, services):
                    pass
        assert [
            mock.call(second, graceful_seconds=5, teardown_seconds=10),
            mock.call(first, graceful_seconds=5, teardown_seconds=10),
        ] == terminate.call_args_list

    def test_forwards_report_owned_cleanup_failure(self) -> None:
        # given
        target = verifier.KubernetesTarget(self.assets, "a" * 40)

        class Child:
            def poll(self) -> int | None:
                return None

        child = Child()
        services = [("product", "qa-svc", "qa-product-api", 8081)]

        # when / then
        with (
            mock.patch.object(verifier.subprocess, "Popen", return_value=child),
            mock.patch.object(verifier.socket, "create_connection", return_value=nullcontext()),
            mock.patch.object(
                verifier.monitoring_runtime,
                "terminate_process_group",
                side_effect=RuntimeError("owned forward survived"),
            ),
        ):
            with pytest.raises(verifier.VerificationError, match="cleanup failed"):
                with verifier.forwards(target, services):
                    pass

    def test_product_reads_use_safe_get_and_deterministic_trace_header(self) -> None:
        # given
        responses = ["live\n", "ready\n", '[{"code":"CAR"}]']

        # when
        with mock.patch.object(verifier, "request_text", side_effect=responses) as request:
            rows = verifier.product_reads({"health": 1001, "product": 1002}, "a" * 32, {})

        # then
        assert 1 == rows
        assert {"traceparent": "00-" + "a" * 32 + "-0123456789abcdef-01"} == request.call_args_list[
            -1
        ].args[1]

    def test_policy_search_probe_resolves_its_own_target_and_safe_read(self) -> None:
        # given
        assets = verifier.load_assets(ASSETS_ROOT, "qa", "policy-search")
        target = verifier.KubernetesTarget(assets, "a" * 40)
        responses = ["live\n", "ready\n", '{"policies":[{"number":"SYN-267"}]}']

        # when
        probe = verifier.select_probe(assets)
        services = probe.services(target)
        with mock.patch.object(verifier, "request_text", side_effect=responses) as request:
            rows = probe.workload(
                {"health": 1001, "policy-search": 1002},
                "b" * 32,
                {
                    "ELASTICSEARCH_INDEX": "policy-views-issue-229",
                    "EVENT_WORKERS_ENABLED": "true",
                },
            )

        # then
        assert probe.service == "policy-search"
        assert services == [
            ("policy-search", "qa-svc", "qa-policy-search-api", 80),
            ("health", "qa-svc", "qa-policy-search-api-management", 8081),
        ]
        assert rows == 1
        assert request.call_args_list[-1].args[0].endswith("/policies?q=%2A")
        assert request.call_args_list[-1].args[1] == {
            "traceparent": "00-" + "b" * 32 + "-0123456789abcdef-01"
        }

    def test_policy_search_probe_accepts_empty_canonical_query_phase_only(self) -> None:
        responses = ["live\n", "ready\n", "{}"]
        with mock.patch.object(verifier, "request_text", side_effect=responses):
            rows = verifier.policy_search_reads(
                {"health": 1001, "policy-search": 1002},
                "b" * 32,
                {
                    "ELASTICSEARCH_INDEX": "policy-views",
                    "EVENT_WORKERS_ENABLED": "false",
                },
            )
        assert rows == 0

        with (
            mock.patch.object(verifier, "request_text", side_effect=responses),
            pytest.raises(verifier.VerificationError, match="has no policies"),
        ):
            verifier.policy_search_reads(
                {"health": 1001, "policy-search": 1002},
                "b" * 32,
                {
                    "ELASTICSEARCH_INDEX": "policy-views-issue-229",
                    "EVENT_WORKERS_ENABLED": "true",
                },
            )

    @pytest.mark.parametrize(
        ("configured", "observed"),
        [("true", "1"), ("false", "0")],
    )
    def test_policy_search_enabled_signals_match_deployment_worker_mode(
        self, configured: str, observed: str
    ) -> None:
        # given
        assets = verifier.load_assets(ASSETS_ROOT, "qa", "policy-search")
        worker_results = [
            {
                "metric": {"insurance_hub_policy_search_event_type": event_type},
                "value": [0, observed],
            }
            for event_type in ("registered", "terminated")
        ]

        def observed_signal(description: str, _probe: object) -> object:
            if description == "Policy Search event_workers metric":
                return worker_results
            if "trace" in description:
                return [{"trace": "observed"}]
            return [{"value": [0, "1"]}]

        # when
        with (
            mock.patch.object(verifier, "wait_for", side_effect=observed_signal),
            mock.patch.object(
                verifier,
                "request_json",
                return_value={"data": {"result": [{"value": [0, "0"]}]}},
            ),
        ):
            result = verifier.verify_policy_search_enabled_signals(
                assets,
                {"prometheus": 1001, "loki": 1002, "tempo": 1003},
                "a" * 32,
                "pod-uid",
                {"EVENT_WORKERS_ENABLED": configured},
            )

        # then
        assert result["metric_series"]["event_workers"] == 2

    @pytest.mark.parametrize(
        ("configured", "observed"),
        [("true", "0"), ("false", "1")],
    )
    def test_policy_search_enabled_signals_reject_worker_mode_mismatch(
        self, configured: str, observed: str
    ) -> None:
        # given
        assets = verifier.load_assets(ASSETS_ROOT, "qa", "policy-search")
        worker_results = [
            {
                "metric": {"insurance_hub_policy_search_event_type": event_type},
                "value": [0, observed],
            }
            for event_type in ("registered", "terminated")
        ]

        def observed_signal(description: str, _probe: object) -> object:
            if description == "Policy Search event_workers metric":
                return worker_results
            return [{"value": [0, "1"]}]

        # when / then
        with (
            mock.patch.object(verifier, "wait_for", side_effect=observed_signal),
            pytest.raises(
                verifier.VerificationError,
                match="differs from Deployment EVENT_WORKERS_ENABLED",
            ),
        ):
            verifier.verify_policy_search_enabled_signals(
                assets,
                {"prometheus": 1001, "loki": 1002, "tempo": 1003},
                "a" * 32,
                "pod-uid",
                {"EVENT_WORKERS_ENABLED": configured},
            )
