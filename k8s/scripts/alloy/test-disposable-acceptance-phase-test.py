#!/usr/bin/env python3
"""Exercise restartable Product acceptance phase evidence and aggregation policy."""

from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest import mock


SCRIPT = Path(__file__).with_name("test-disposable-acceptance-phase.py")
SPEC = importlib.util.spec_from_file_location("acceptance_phase", SCRIPT)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot import {SCRIPT}")
phase_runner = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = phase_runner
SPEC.loader.exec_module(phase_runner)
PROFILE_PATH = Path(__file__).parents[2] / "observability/services/product/acceptance.json"


class AcceptancePhaseTest(unittest.TestCase):
    def setUp(self) -> None:
        self.profile = phase_runner.acceptance.load_profile(PROFILE_PATH)
        self.candidate = {"candidate": "same"}

    def write_phase(
        self, root: Path, phase: str, candidate=None, evidence=None
    ) -> Path:
        path = root / f"{phase}.json"
        path.write_text(
            json.dumps(
                {
                    "schema_version": 1,
                    "kind": "phase",
                    "phase": phase,
                    "phase_contract_version": phase_runner.PHASE_CONTRACT_VERSIONS[
                        phase
                    ],
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

        self.assertEqual(set(phase_runner.PHASES), set(documents))

    def test_load_phase_documents_rejects_stale_candidate(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            paths = [self.write_phase(root, phase) for phase in phase_runner.PHASES]
            paths[-1] = self.write_phase(
                root, phase_runner.PHASES[-1], {"candidate": "changed"}
            )

            with self.assertRaisesRegex(
                phase_runner.acceptance.AcceptanceError,
                "stale candidate evidence for recovery",
            ):
                phase_runner.load_phase_documents(paths, self.candidate)

    def test_load_phase_documents_rejects_missing_phase(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            paths = [
                self.write_phase(root, phase) for phase in phase_runner.PHASES[:-1]
            ]

            with self.assertRaisesRegex(
                phase_runner.acceptance.AcceptanceError,
                "missing acceptance phase evidence: recovery",
            ):
                phase_runner.load_phase_documents(paths, self.candidate)

    def test_resource_summary_aggregation_preserves_median_pairing(self) -> None:
        disabled = [
            {"cpu_p95_millicores": value, "memory_max_mib": value + 20}
            for value in (10, 20, 30)
        ]
        enabled = [
            {"cpu_p95_millicores": value, "memory_max_mib": value + 30}
            for value in (20, 30, 40)
        ]

        evidence = phase_runner.verify_resource_overhead_summaries(
            disabled, enabled, self.profile
        )

        self.assertEqual(10, evidence["cpu_p95_delta_millicores"])
        self.assertEqual(20, evidence["disabled_cpu_p95_millicores"])
        self.assertEqual(30, evidence["enabled_cpu_p95_millicores"])
        self.assertEqual(20, evidence["memory_max_delta_mib"])

    def test_aggregate_evidence_combines_all_phase_outputs(self) -> None:
        load = {
            "paths": {
                name: {"p95Millis": 10}
                for name in phase_runner.acceptance.PATH_NAMES
            }
        }
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
                    evidence=(
                        pair if phase.startswith("overhead-") else {"passed": True}
                    ),
                )
                for phase in phase_runner.PHASES
            ]
            args = SimpleNamespace(evidence_input=paths)
            with mock.patch.object(
                phase_runner, "candidate_identity", return_value=self.candidate
            ), mock.patch.object(
                phase_runner, "tooling_identity", return_value={"tool": "test"}
            ):
                document = phase_runner.aggregate_evidence(args, self.profile)

        self.assertEqual("aggregate", document["kind"])
        self.assertEqual(set(phase_runner.PHASES), set(document["phases"]))
        self.assertEqual(
            0,
            document["overhead"]["resources"]["cpu_p95_delta_millicores"],
        )

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
            self.assertEqual(1, result)
            self.assertFalse(output.exists())
            self.assertIn("requires phase evidence inputs", errors.getvalue())


if __name__ == "__main__":
    unittest.main()
