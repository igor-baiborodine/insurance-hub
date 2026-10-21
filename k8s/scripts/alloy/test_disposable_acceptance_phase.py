#!/usr/bin/env python3
"""Pytest coverage for restartable Product acceptance phase evidence and aggregation policy."""

from __future__ import annotations

import contextlib
import io
import json
import tempfile
import time
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

import pytest

import disposable_acceptance_phase as phase_runner

PROFILE_PATH = Path(__file__).parents[2] / "observability/services/product/acceptance.json"


class TestAcceptancePhase:
    def setup_method(self) -> None:
        self.profile = phase_runner.acceptance.load_profile(PROFILE_PATH)
        self.candidate = {"candidate": "same"}

    def write_phase(self, root: Path, phase: str, candidate=None, evidence=None) -> Path:
        path = root / f"{phase}.json"
        path.write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "kind": "phase",
                    "phase": phase,
                    "phase_contract_version": phase_runner.PHASE_CONTRACT_VERSIONS[phase],
                    "candidate": candidate or self.candidate,
                    "evidence": evidence or {},
                }
            ),
            encoding="utf-8",
        )
        return path

    def test_load_phase_documents_accepts_complete_compatible_set(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            paths = [self.write_phase(root, phase) for phase in phase_runner.PHASES]

            documents = phase_runner.load_phase_documents(paths, self.candidate)

        assert set(phase_runner.PHASES) == set(documents)

    def test_load_phase_documents_rejects_stale_candidate(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            paths = [self.write_phase(root, phase) for phase in phase_runner.PHASES]
            paths[-1] = self.write_phase(root, phase_runner.PHASES[-1], {"candidate": "changed"})

            with pytest.raises(
                phase_runner.acceptance.AcceptanceError,
                match="stale candidate evidence for recovery",
            ):
                phase_runner.load_phase_documents(paths, self.candidate)

    def test_load_phase_documents_rejects_missing_phase(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            paths = [self.write_phase(root, phase) for phase in phase_runner.PHASES[:-1]]

            with pytest.raises(
                phase_runner.acceptance.AcceptanceError,
                match="missing acceptance phase evidence: recovery",
            ):
                phase_runner.load_phase_documents(paths, self.candidate)

    def test_resource_summary_aggregation_preserves_median_pairing(self) -> None:
        disabled = [
            {"cpu_p95_millicores": value, "memory_max_mib": value + 20} for value in (10, 20, 30)
        ]
        enabled = [
            {"cpu_p95_millicores": value, "memory_max_mib": value + 30} for value in (20, 30, 40)
        ]

        evidence = phase_runner.verify_resource_overhead_summaries(disabled, enabled, self.profile)

        assert 10 == evidence["cpu_p95_delta_millicores"]
        assert 20 == evidence["disabled_cpu_p95_millicores"]
        assert 30 == evidence["enabled_cpu_p95_millicores"]
        assert 20 == evidence["memory_max_delta_mib"]

    def test_aggregate_evidence_combines_all_phase_outputs(self) -> None:
        load = {"paths": {name: {"p95Millis": 10} for name in phase_runner.acceptance.PATH_NAMES}}
        resources = {"cpu_p95_millicores": 10, "memory_max_mib": 20}
        pair = {
            "disabled": {"load": load, "resources": resources},
            "enabled": {"load": load, "resources": resources},
        }
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            paths = [
                self.write_phase(
                    root,
                    phase,
                    evidence=(pair if phase.startswith("overhead-") else {"passed": True}),
                )
                for phase in phase_runner.PHASES
            ]
            args = SimpleNamespace(evidence_input=paths)
            with (
                mock.patch.object(phase_runner, "candidate_identity", return_value=self.candidate),
                mock.patch.object(phase_runner, "tooling_identity", return_value={"tool": "test"}),
            ):
                document = phase_runner.aggregate_evidence(args, self.profile)

        assert "aggregate" == document["kind"]
        assert set(phase_runner.PHASES) == set(document["phases"])
        assert 0 == document["overhead"]["resources"]["cpu_p95_delta_millicores"]

    def test_failed_invocation_invalidates_previous_output(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "aggregate.json"
            output.write_text("stale evidence\n", encoding="utf-8")

            errors = io.StringIO()
            with contextlib.redirect_stderr(errors):
                result = phase_runner.main(
                    [
                        "--phase",
                        "aggregate",
                        "--config",
                        "unused-config",
                        "--profile",
                        str(PROFILE_PATH),
                        "--service-root",
                        "unused-service",
                        "--catalog-fixture",
                        "unused-catalog",
                        "--product-image",
                        "unused-image",
                        "--evidence-output",
                        str(output),
                    ]
                )
            assert 1 == result
            assert not output.exists()
            assert "requires phase evidence inputs" in errors.getvalue()

    def test_phase_runtime_preserves_primary_and_cleanup_failures(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            config = root / "config.alloy"
            config.write_text("test config\n", encoding="utf-8")
            args = SimpleNamespace(
                phase="healthy",
                config=config,
                service_root=root,
                catalog_fixture=root / "catalog.json",
                product_image="product-image",
            )
            harness = mock.Mock()
            harness.start.return_value = "postgres"
            harness.cleanup.side_effect = phase_runner.acceptance.backend.HarnessError(
                "harness cleanup failed"
            )
            product = object()
            primary = phase_runner.acceptance.AcceptanceError("phase failed")

            with (
                mock.patch.object(
                    phase_runner.acceptance.backend, "DockerHarness", return_value=harness
                ),
                mock.patch.object(phase_runner.acceptance.backend, "write_configs"),
                mock.patch.object(
                    phase_runner.acceptance.backend.backend_check,
                    "DISPOSABLE_IMAGES",
                    {"postgres": "postgres-image"},
                ),
                mock.patch.object(phase_runner.acceptance, "command"),
                mock.patch.object(phase_runner.acceptance, "wait_postgres"),
                mock.patch.object(phase_runner.acceptance, "seed_postgres"),
                mock.patch.object(phase_runner.acceptance, "start_backends", return_value={}),
                mock.patch.object(
                    phase_runner.acceptance,
                    "stop_product",
                    side_effect=phase_runner.acceptance.AcceptanceError("product cleanup failed"),
                ),
            ):
                with pytest.raises(
                    phase_runner.acceptance.AcceptanceError,
                    match="phase failed.*product cleanup failed.*harness cleanup failed",
                ) as raised:
                    with phase_runner.phase_runtime(args, self.profile) as runtime:
                        runtime.products.append(product)
                        raise primary

        assert primary is raised.value.__cause__
        assert [] == runtime.products

    @pytest.mark.parametrize("expires_in_work", (False, True))
    def test_phase_cleanup_finishes_after_work_deadline(
        self, tmp_path: Path, expires_in_work: bool
    ) -> None:
        # given
        config = tmp_path / "config.alloy"
        config.write_text("test config\n", encoding="utf-8")
        args = SimpleNamespace(
            phase="healthy",
            config=config,
            service_root=tmp_path,
            catalog_fixture=tmp_path / "catalog.json",
            product_image="product-image",
        )
        harness = mock.Mock()

        def setup() -> None:
            time.sleep(0.25 if expires_in_work else 0.15)
            if not expires_in_work:
                raise RuntimeError("setup failed")

        harness.create.side_effect = setup
        cleanup_done = []

        def slow_cleanup() -> None:
            time.sleep(0.25)
            cleanup_done.append(True)

        harness.cleanup.side_effect = slow_cleanup
        # when / then
        with (
            mock.patch.object(
                phase_runner.acceptance.backend, "DockerHarness", return_value=harness
            ),
            mock.patch.object(phase_runner.acceptance.backend, "write_configs"),
            mock.patch.object(phase_runner.acceptance, "command"),
            pytest.raises(
                phase_runner.alloy_runtime.DeadlineExceeded if expires_in_work else RuntimeError,
                match="work budget" if expires_in_work else "setup failed",
            ),
        ):
            with phase_runner.alloy_runtime.bounded_activity("phase cleanup test", 0.2):
                with phase_runner.phase_runtime(args, self.profile):
                    pass
        assert cleanup_done == [True]

    def test_failed_phase_stop_retains_product_for_cleanup_retry(self) -> None:
        product = object()
        runtime = phase_runner.PhaseRuntime(
            root=Path("unused"),
            helper=Path("unused-helper"),
            harness=mock.Mock(),
            ports={},
            products=[product],
        )

        with mock.patch.object(
            phase_runner.acceptance,
            "stop_product",
            side_effect=phase_runner.acceptance.AcceptanceError("stop failed"),
        ):
            with pytest.raises(phase_runner.acceptance.AcceptanceError, match="stop failed"):
                phase_runner.stop_product(runtime, product, self.profile)

        assert [product] == runtime.products
