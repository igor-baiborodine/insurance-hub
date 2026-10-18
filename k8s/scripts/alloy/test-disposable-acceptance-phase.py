#!/usr/bin/env python3
"""Run restartable phases of the Product telemetry acceptance profile."""

from __future__ import annotations

import argparse
import concurrent.futures
import contextlib
import hashlib
import importlib.util
import json
import math
import os
import statistics
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Dict, Iterator, List, Mapping, Optional, Sequence


ACCEPTANCE_PATH = Path(__file__).with_name("test-disposable-acceptance.py")
ACCEPTANCE_SPEC = importlib.util.spec_from_file_location(
    "disposable_acceptance", ACCEPTANCE_PATH
)
if ACCEPTANCE_SPEC is None or ACCEPTANCE_SPEC.loader is None:
    raise RuntimeError(f"cannot import {ACCEPTANCE_PATH}")
acceptance = importlib.util.module_from_spec(ACCEPTANCE_SPEC)
sys.modules[ACCEPTANCE_SPEC.name] = acceptance
ACCEPTANCE_SPEC.loader.exec_module(acceptance)


PHASES = ("overhead-1", "overhead-2", "overhead-3", "healthy", "recovery")
PHASE_CONTRACT_VERSIONS = {
    "overhead-1": 1,
    "overhead-2": 1,
    "overhead-3": 1,
    "healthy": 1,
    "recovery": 1,
}
RUNNER_PATH = Path(__file__).resolve()
REPOSITORY_ROOT = RUNNER_PATH.parents[3]


@dataclass
class PhaseRuntime:
    root: Path
    helper: Path
    harness: Any
    ports: Dict[str, Any]
    products: List[Any]


def file_sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def image_id(reference: str) -> str:
    return json.loads(acceptance.command(["docker", "image", "inspect", reference]))[0]["Id"]


def candidate_identity(args: argparse.Namespace) -> Mapping[str, Any]:
    backend_images = {
        reference: image_id(reference)
        for reference in sorted(set(acceptance.backend.backend_check.DISPOSABLE_IMAGES.values()))
    }
    return {
        "time_scale": args.time_scale,
        "product_image": image_id(args.product_image),
        "backend_images": backend_images,
        "config_sha256": file_sha256(args.config),
        "profile_sha256": file_sha256(args.profile),
        "catalog_fixture_sha256": file_sha256(args.catalog_fixture),
    }


def tooling_identity() -> Mapping[str, str]:
    return {
        "source_revision": acceptance.command(
            ["git", "-C", str(REPOSITORY_ROOT), "rev-parse", "HEAD"]
        ),
        "phase_runner_sha256": file_sha256(RUNNER_PATH),
        "acceptance_script_sha256": file_sha256(ACCEPTANCE_PATH),
    }


def write_evidence(path: Path, value: Mapping[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    temporary.replace(path)


def phase_document(
    args: argparse.Namespace, phase: str, evidence: Mapping[str, Any]
) -> Mapping[str, Any]:
    return {
        "schema_version": 1,
        "kind": "phase",
        "phase": phase,
        "phase_contract_version": PHASE_CONTRACT_VERSIONS[phase],
        "candidate": candidate_identity(args),
        "tooling": tooling_identity(),
        "evidence": evidence,
    }


@contextlib.contextmanager
def phase_runtime(
    args: argparse.Namespace, profile: Mapping[str, Any]
) -> Iterator[PhaseRuntime]:
    source = args.config.read_text(encoding="utf-8")
    prefix = f"ih-accept-{args.phase}-{os.getpid()}"
    with tempfile.TemporaryDirectory(prefix="insurance-hub-acceptance-") as temporary:
        root = Path(temporary)
        root.chmod(0o755)
        acceptance.backend.write_configs(root, source, "product")
        helper = root / "telemetry-acceptance"
        acceptance.command(
            [
                "make",
                "-C",
                str(args.service_root),
                "build-telemetry-acceptance",
                f"TELEMETRY_ACCEPTANCE_OUTPUT={helper}",
            ],
            timeout=300,
        )
        harness = acceptance.backend.DockerHarness(prefix, root)
        products: List[Any] = []
        try:
            harness.create()
            for image in set(acceptance.backend.backend_check.DISPOSABLE_IMAGES.values()):
                harness.pull(image)
            acceptance.command(["docker", "image", "inspect", args.product_image])
            postgres = harness.start(
                "postgres",
                acceptance.backend.backend_check.DISPOSABLE_IMAGES["postgres"],
                [],
                ports=[5432],
                docker_arguments=[
                    "-e",
                    f"POSTGRES_PASSWORD={acceptance.POSTGRES_PASSWORD}",
                    "-e",
                    "POSTGRES_DB=product",
                ],
            )
            acceptance.wait_postgres(harness, postgres)
            acceptance.seed_postgres(harness, postgres, args.catalog_fixture)
            runtime = PhaseRuntime(
                root=root,
                helper=helper,
                harness=harness,
                ports=acceptance.start_backends(harness, root),
                products=products,
            )
            yield runtime
        finally:
            for product in products:
                try:
                    acceptance.stop_product(
                        harness,
                        product,
                        profile["product_resources"]["shutdown_seconds"],
                    )
                except Exception:
                    pass
            harness.cleanup()


def stop_product(
    runtime: PhaseRuntime, product: Any, profile: Mapping[str, Any]
) -> float:
    elapsed = acceptance.stop_product(
        runtime.harness,
        product,
        profile["product_resources"]["shutdown_seconds"],
    )
    runtime.products.remove(product)
    return elapsed


def run_overhead_pair(
    args: argparse.Namespace, profile: Mapping[str, Any], run_index: int
) -> Mapping[str, Any]:
    with phase_runtime(args, profile) as runtime:
        overhead_seconds = max(
            2, profile["overhead"]["seconds_per_run"] * args.time_scale
        )
        overhead_requests = max(
            1,
            int(overhead_seconds * profile["healthy"]["rate_per_second"] / 4),
        )
        warmup_seconds = max(
            1, profile["healthy"]["warmup_seconds"] * args.time_scale
        )
        warmup_requests = max(
            1,
            int(warmup_seconds * profile["healthy"]["rate_per_second"] / 4),
        )
        order = (
            ("disabled", "enabled")
            if run_index % 2 == 0
            else ("enabled", "disabled")
        )
        evidence: Dict[str, Any] = {"run_index": run_index, "order": list(order)}
        for mode in order:
            enabled = mode == "enabled"
            product = acceptance.start_product(
                runtime.harness,
                runtime.root,
                args.product_image,
                f"product-{mode}-{run_index}",
                enabled,
                acceptance.backend.SERVICE_INSTANCE_ID,
            )
            runtime.products.append(product)
            acceptance.run_load(
                runtime.helper,
                product,
                warmup_requests,
                profile["healthy"]["rate_per_second"],
                profile["healthy"]["max_in_flight"],
                profile["healthy"]["request_deadline_seconds"],
                f"warmup-{mode}-{run_index}",
                math.ceil(warmup_seconds) + 120,
            )
            with acceptance.Sampler(product.container) as sampler:
                result = acceptance.run_load(
                    runtime.helper,
                    product,
                    overhead_requests,
                    profile["healthy"]["rate_per_second"],
                    profile["healthy"]["max_in_flight"],
                    profile["healthy"]["request_deadline_seconds"],
                    f"overhead-{mode}-{run_index}",
                    math.ceil(overhead_seconds) + 120,
                )
            acceptance.verify_load(result, overhead_requests, profile)
            acceptance.verify_resources(
                sampler.samples,
                profile["product_resources"],
                f"Product {mode} overhead run {run_index + 1}",
            )
            evidence[mode] = {
                "load": result,
                "resources": acceptance.resource_summary(sampler.samples),
            }
            stop_product(runtime, product, profile)
        return evidence


def sample_alloy_baseline(
    container: str, profile: Mapping[str, Any], time_scale: float
) -> List[Any]:
    samples: List[Any] = []
    seconds = max(2, profile["alloy_resources"]["baseline_seconds"] * time_scale)
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        samples.append(acceptance.container_stats(container))
        time.sleep(1)
    return samples


def run_healthy_phase(
    args: argparse.Namespace, profile: Mapping[str, Any]
) -> Mapping[str, Any]:
    with phase_runtime(args, profile) as runtime:
        baseline = sample_alloy_baseline(
            runtime.ports["alloy_container"], profile, args.time_scale
        )
        instance = acceptance.backend.SERVICE_INSTANCE_ID
        product = acceptance.start_product(
            runtime.harness,
            runtime.root,
            args.product_image,
            "product-healthy",
            True,
            instance,
        )
        runtime.products.append(product)
        healthy_requests = max(
            1,
            math.ceil(profile["healthy"]["requests_per_path"] * args.time_scale),
        )
        with acceptance.Sampler(product.container) as product_sampler, acceptance.Sampler(
            runtime.ports["alloy_container"]
        ) as alloy_sampler, acceptance.MetricIntervalSampler(
            runtime.ports["prometheus"]
        ) as metric_interval_sampler:
            healthy = acceptance.run_load(
                runtime.helper,
                product,
                healthy_requests,
                profile["healthy"]["rate_per_second"],
                profile["healthy"]["max_in_flight"],
                profile["healthy"]["request_deadline_seconds"],
                "healthy",
                math.ceil(
                    healthy_requests * 4 / profile["healthy"]["rate_per_second"]
                )
                + 180,
            )
        acceptance.verify_load(healthy, healthy_requests, profile)
        acceptance.verify_resources(
            product_sampler.samples, profile["product_resources"], "Product"
        )
        acceptance.verify_resources(
            alloy_sampler.samples, profile["alloy_resources"], "Alloy"
        )
        resources = acceptance.verify_alloy_resource_delta(
            baseline, alloy_sampler.samples, profile["alloy_resources"]
        )
        backend_evidence = acceptance.verify_backend_counts(
            runtime.ports,
            instance,
            profile,
            healthy["traceIds"],
            healthy_requests,
            args.time_scale,
            metric_interval_sampler.values,
        )
        alloy_delivery = acceptance.verify_alloy_healthy_delivery(
            runtime.ports["alloy_metrics"],
            healthy_requests * len(acceptance.PATH_NAMES) * 4,
        )
        container_state = {
            "product": acceptance.verify_container_state(product.container, "Product"),
            "alloy": acceptance.verify_container_state(
                runtime.ports["alloy_container"], "Alloy"
            ),
        }
        shutdown_seconds = stop_product(runtime, product, profile)
        return {
            "healthy": healthy,
            "backend": backend_evidence,
            "alloy_delivery": alloy_delivery,
            "container_state": container_state,
            "shutdown_seconds": shutdown_seconds,
            "resources": {
                "product": acceptance.resource_summary(product_sampler.samples),
                **resources,
            },
        }


def prime_recovery_phase(
    runtime: PhaseRuntime, product: Any, profile: Mapping[str, Any]
) -> Mapping[str, Any]:
    primer = acceptance.run_load(
        runtime.helper,
        product,
        1,
        profile["healthy"]["rate_per_second"],
        profile["healthy"]["max_in_flight"],
        profile["healthy"]["request_deadline_seconds"],
        "recovery-primer",
        120,
    )
    acceptance.verify_load(primer, 1, profile)
    acceptance.wait_metric_request_total(
        runtime.ports["prometheus"],
        product.instance,
        len(acceptance.PATH_NAMES),
        profile["delivery"]["metric_max_seconds"],
    )
    return primer


def run_recovery_phase(
    args: argparse.Namespace, profile: Mapping[str, Any]
) -> Mapping[str, Any]:
    with phase_runtime(args, profile) as runtime:
        instance = acceptance.backend.SERVICE_INSTANCE_ID
        product = acceptance.start_product(
            runtime.harness,
            runtime.root,
            args.product_image,
            "product-recovery",
            True,
            instance,
        )
        runtime.products.append(product)
        primer = prime_recovery_phase(runtime, product, profile)
        freshness = acceptance.verify_freshness(
            runtime.helper, product, runtime.ports, profile, args.time_scale
        )
        safety = acceptance.verify_varied_input_safety(
            product, runtime.ports, runtime.root, profile
        )

        pre_fault_counter = acceptance.metric_request_total(
            runtime.ports["prometheus"], instance
        )
        pre_fault_histogram = acceptance.metric_histogram_count(
            runtime.ports["prometheus"], instance
        )
        backend_names = [
            runtime.ports["prometheus_container"],
            f"{runtime.harness.prefix}-loki",
            f"{runtime.harness.prefix}-tempo",
            f"{runtime.harness.prefix}-zipkin",
            f"{runtime.harness.prefix}-zipkin-bridge",
        ]
        for name in backend_names:
            runtime.harness.command(["docker", "pause", name], timeout=30)
        fault_seconds = max(1, profile["fault"]["seconds"] * args.time_scale)
        fault_requests_per_path = max(
            1, math.ceil(profile["fault"]["requests"] * args.time_scale / 4)
        )
        fault_started = time.monotonic()
        fault = acceptance.run_load(
            runtime.helper,
            product,
            fault_requests_per_path,
            profile["healthy"]["rate_per_second"],
            profile["healthy"]["max_in_flight"],
            profile["healthy"]["request_deadline_seconds"],
            "backend-outage",
            math.ceil(fault_seconds) + 120,
        )
        acceptance.verify_load(fault, fault_requests_per_path, profile)
        remaining = fault_seconds - (time.monotonic() - fault_started)
        if remaining > 0:
            time.sleep(remaining)
        if time.monotonic() - fault_started < fault_seconds - 0.1:
            raise acceptance.AcceptanceError(
                "backend outage workload did not cover sixty seconds"
            )
        for name in reversed(backend_names):
            runtime.harness.command(["docker", "unpause", name], timeout=30)
        acceptance.backend.wait_http(
            f"http://127.0.0.1:{runtime.ports['prometheus']}/-/ready", timeout=60
        )
        acceptance.backend.wait_http(
            f"http://127.0.0.1:{runtime.ports['loki']}/ready", timeout=60
        )
        acceptance.backend.wait_http(
            f"http://127.0.0.1:{runtime.ports['tempo']}/ready", timeout=60
        )

        pending = acceptance.backend.wait_remote_write_value(
            runtime.ports["alloy_metrics"],
            acceptance.backend.REMOTE_WRITE_PENDING_METRIC,
            lambda value: value == 0,
            "remote-write queue recovery",
            timeout=profile["fault"]["queues_healthy_seconds"],
        )
        fault_requests = fault_requests_per_path * len(acceptance.PATH_NAMES)
        post_fault_counter = acceptance.wait_metric_request_total(
            runtime.ports["prometheus"],
            instance,
            pre_fault_counter + fault_requests,
            profile["fault"]["queues_healthy_seconds"],
        )
        post_fault_histogram = acceptance.wait_metric_histogram_count(
            runtime.ports["prometheus"],
            instance,
            pre_fault_histogram + fault_requests,
            profile["fault"]["queues_healthy_seconds"],
        )
        if (
            post_fault_counter - pre_fault_counter != fault_requests
            or post_fault_histogram - pre_fault_histogram != fault_requests
        ):
            raise acceptance.AcceptanceError(
                "backend outage did not preserve cumulative metric deltas"
            )
        loss_scale = args.time_scale if args.time_scale < 1 else 1
        backend_fault_spans = acceptance.verify_trace_delivery(
            runtime.ports["tempo"],
            "backend-outage",
            fault_requests,
            math.ceil(profile["fault"]["lost_spans_max"] * loss_scale),
            math.ceil(profile["fault"]["duplicate_spans_max"] * loss_scale),
            profile["fault"]["queues_healthy_seconds"],
        )
        backend_fault_logs = acceptance.verify_log_delivery(
            runtime.ports["loki"],
            instance,
            "backend-outage",
            fault_requests,
            math.ceil(profile["fault"]["lost_logs_max"] * loss_scale),
            math.ceil(profile["fault"]["duplicate_logs_max"] * loss_scale),
            profile["fault"]["queues_healthy_seconds"],
        )
        backend_recovery = acceptance.verify_recovery_marker(
            runtime.helper, product, runtime.ports, profile, "backend-recovery"
        )

        pre_replacement_counter = acceptance.metric_request_total(
            runtime.ports["prometheus"], instance
        )
        pre_replacement_histogram = acceptance.metric_histogram_count(
            runtime.ports["prometheus"], instance
        )
        runtime.harness.remove(runtime.ports["alloy_container"])
        replacement_started_epoch = time.time()
        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as executor:
            replacement_future = executor.submit(
                acceptance.run_load,
                runtime.helper,
                product,
                fault_requests_per_path,
                profile["healthy"]["rate_per_second"],
                profile["healthy"]["max_in_flight"],
                profile["healthy"]["request_deadline_seconds"],
                "collector-replacement",
                math.ceil(fault_seconds) + 120,
            )
            time.sleep(max(1, min(10, fault_seconds / 2)))
            replacement, _, replacement_metrics = acceptance.backend.start_alloy(
                runtime.harness
            )
            replacement_fault = replacement_future.result()
        runtime.ports["alloy_container"] = replacement
        runtime.ports["alloy_metrics"] = replacement_metrics
        acceptance.verify_load(replacement_fault, fault_requests_per_path, profile)
        replacement_pending = acceptance.backend.wait_remote_write_value(
            replacement_metrics,
            acceptance.backend.REMOTE_WRITE_PENDING_METRIC,
            lambda value: value == 0,
            "replacement remote-write queue recovery",
            timeout=profile["fault"]["queues_healthy_seconds"],
        )
        post_replacement_counter = acceptance.wait_metric_request_total(
            runtime.ports["prometheus"],
            instance,
            pre_replacement_counter + fault_requests,
            profile["fault"]["new_metric_seconds"],
        )
        post_replacement_histogram = acceptance.wait_metric_histogram_count(
            runtime.ports["prometheus"],
            instance,
            pre_replacement_histogram + fault_requests,
            profile["fault"]["new_metric_seconds"],
        )
        if (
            post_replacement_counter - pre_replacement_counter != fault_requests
            or post_replacement_histogram - pre_replacement_histogram
            != fault_requests
        ):
            raise acceptance.AcceptanceError(
                "Alloy replacement did not converge cumulative metric deltas"
            )
        replacement_spans = acceptance.verify_trace_delivery(
            runtime.ports["tempo"],
            "collector-replacement",
            fault_requests,
            math.ceil(profile["fault"]["lost_spans_max"] * loss_scale),
            math.ceil(profile["fault"]["duplicate_spans_max"] * loss_scale),
            profile["fault"]["queues_healthy_seconds"],
        )
        replacement_logs = acceptance.verify_log_delivery(
            runtime.ports["loki"],
            instance,
            "collector-replacement",
            fault_requests,
            math.ceil(profile["fault"]["lost_logs_max"] * loss_scale),
            math.ceil(profile["fault"]["duplicate_logs_max"] * loss_scale),
            profile["fault"]["queues_healthy_seconds"],
        )
        replacement_recovery = acceptance.verify_recovery_marker(
            runtime.helper, product, runtime.ports, profile, "collector-recovery"
        )
        platform_metric_seconds = acceptance.wait_platform_metric_after(
            runtime.ports["prometheus"],
            instance,
            replacement_started_epoch,
            profile["fault"]["new_metric_seconds"],
        )
        replacement_state = acceptance.verify_container_state(
            replacement, "replacement Alloy"
        )
        shutdown_seconds = stop_product(runtime, product, profile)
        return {
            "primer": primer,
            "freshness": freshness,
            "safety": safety,
            "fault": {
                "backend_outage": {
                    "requests": fault_requests,
                    "pending_after_recovery": pending,
                    "counter_delta": post_fault_counter - pre_fault_counter,
                    "histogram_count_delta": post_fault_histogram
                    - pre_fault_histogram,
                    "spans": backend_fault_spans,
                    "logs": backend_fault_logs,
                    "recovery": backend_recovery,
                },
                "collector_replacement": {
                    "requests": fault_requests,
                    "pending_after_recovery": replacement_pending,
                    "counter_delta": post_replacement_counter
                    - pre_replacement_counter,
                    "histogram_count_delta": post_replacement_histogram
                    - pre_replacement_histogram,
                    "spans": replacement_spans,
                    "logs": replacement_logs,
                    "recovery": replacement_recovery,
                    "platform_metric_seconds": platform_metric_seconds,
                    "container_state": replacement_state,
                },
            },
            "shutdown_seconds": shutdown_seconds,
        }


def verify_resource_overhead_summaries(
    disabled: Sequence[Mapping[str, Any]],
    enabled: Sequence[Mapping[str, Any]],
    profile: Mapping[str, Any],
) -> Mapping[str, Any]:
    expected = profile["overhead"]["runs_per_mode"]
    if len(disabled) != expected or len(enabled) != expected:
        raise acceptance.AcceptanceError("paired overhead resource evidence is incomplete")
    disabled_cpu = statistics.median(
        value["cpu_p95_millicores"] for value in disabled
    )
    enabled_cpu = statistics.median(value["cpu_p95_millicores"] for value in enabled)
    disabled_memory = statistics.median(value["memory_max_mib"] for value in disabled)
    enabled_memory = statistics.median(value["memory_max_mib"] for value in enabled)
    cpu_delta = enabled_cpu - disabled_cpu
    memory_delta = enabled_memory - disabled_memory
    if cpu_delta > profile["overhead"]["cpu_p95_delta_millicores"]:
        raise acceptance.AcceptanceError(
            "telemetry CPU p95 delta exceeds its budget"
        )
    if memory_delta > profile["overhead"]["memory_max_delta_mib"]:
        raise acceptance.AcceptanceError(
            "telemetry working-set maximum delta exceeds its budget"
        )
    return {
        "disabled_cpu_p95_millicores": disabled_cpu,
        "enabled_cpu_p95_millicores": enabled_cpu,
        "cpu_p95_delta_millicores": cpu_delta,
        "disabled_memory_max_mib": disabled_memory,
        "enabled_memory_max_mib": enabled_memory,
        "memory_max_delta_mib": memory_delta,
    }


def load_phase_documents(
    paths: Sequence[Path], expected_candidate: Mapping[str, Any]
) -> Mapping[str, Mapping[str, Any]]:
    documents: Dict[str, Mapping[str, Any]] = {}
    for path in paths:
        document = json.loads(path.read_text(encoding="utf-8"))
        phase = document.get("phase")
        if document.get("schema_version") != 1 or document.get("kind") != "phase":
            raise acceptance.AcceptanceError(f"invalid phase evidence: {path}")
        if phase not in PHASES or phase in documents:
            raise acceptance.AcceptanceError(f"unexpected or duplicate phase evidence: {path}")
        if document.get("candidate") != expected_candidate:
            raise acceptance.AcceptanceError(f"stale candidate evidence for {phase}")
        if document.get("phase_contract_version") != PHASE_CONTRACT_VERSIONS[phase]:
            raise acceptance.AcceptanceError(f"stale contract evidence for {phase}")
        documents[phase] = document
    missing = sorted(set(PHASES) - set(documents))
    if missing:
        raise acceptance.AcceptanceError(
            "missing acceptance phase evidence: " + ", ".join(missing)
        )
    return documents


def aggregate_evidence(
    args: argparse.Namespace, profile: Mapping[str, Any]
) -> Mapping[str, Any]:
    candidate = candidate_identity(args)
    documents = load_phase_documents(args.evidence_input, candidate)
    pairs = [documents[f"overhead-{index}"]["evidence"] for index in range(1, 4)]
    disabled_loads = [pair["disabled"]["load"] for pair in pairs]
    enabled_loads = [pair["enabled"]["load"] for pair in pairs]
    disabled_resources = [pair["disabled"]["resources"] for pair in pairs]
    enabled_resources = [pair["enabled"]["resources"] for pair in pairs]
    overhead = {
        "latency": acceptance.verify_overhead(
            disabled_loads, enabled_loads, profile
        ),
        "resources": verify_resource_overhead_summaries(
            disabled_resources, enabled_resources, profile
        ),
    }
    return {
        "schema_version": 1,
        "kind": "aggregate",
        "candidate": candidate,
        "tooling": tooling_identity(),
        "phase_contract_versions": PHASE_CONTRACT_VERSIONS,
        "overhead": overhead,
        "phases": {
            phase: documents[phase]["evidence"] for phase in PHASES
        },
    }


def run_phase(args: argparse.Namespace, profile: Mapping[str, Any]) -> Mapping[str, Any]:
    if args.phase.startswith("overhead-"):
        run_index = int(args.phase.rsplit("-", 1)[1]) - 1
        evidence = run_overhead_pair(args, profile, run_index)
    elif args.phase == "healthy":
        evidence = run_healthy_phase(args, profile)
    elif args.phase == "recovery":
        evidence = run_recovery_phase(args, profile)
    else:
        raise acceptance.AcceptanceError(f"unsupported phase: {args.phase}")
    return phase_document(args, args.phase, evidence)


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--phase", choices=(*PHASES, "aggregate"), required=True)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--profile", type=Path, required=True)
    parser.add_argument("--service-root", type=Path, required=True)
    parser.add_argument("--catalog-fixture", type=Path, required=True)
    parser.add_argument("--product-image", required=True)
    parser.add_argument("--time-scale", type=float, default=1.0)
    parser.add_argument("--evidence-output", type=Path, required=True)
    parser.add_argument("--evidence-input", type=Path, action="append", default=[])
    return parser.parse_args(argv)


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = parse_args(argv)
    try:
        profile = acceptance.load_profile(args.profile)
        if args.time_scale <= 0 or args.time_scale > 1:
            raise acceptance.AcceptanceError(
                "time-scale must be greater than zero and at most one"
            )
        args.evidence_output.unlink(missing_ok=True)
        if args.phase == "aggregate":
            if not args.evidence_input:
                raise acceptance.AcceptanceError(
                    "aggregate requires phase evidence inputs"
                )
            document = aggregate_evidence(args, profile)
        else:
            if args.evidence_input:
                raise acceptance.AcceptanceError(
                    "phase execution does not accept evidence inputs"
                )
            document = run_phase(args, profile)
        write_evidence(args.evidence_output, document)
        print(json.dumps(document, indent=2, sort_keys=True))
    except (
        acceptance.AcceptanceError,
        acceptance.backend.HarnessError,
        json.JSONDecodeError,
        OSError,
        subprocess.TimeoutExpired,
        ValueError,
    ) as error:
        print(f"disposable acceptance phase failed: {error}", file=sys.stderr)
        return 1
    print(f"disposable Product telemetry acceptance {args.phase} passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
