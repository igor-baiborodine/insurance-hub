#!/usr/bin/env python3
"""Own bounded monitoring validation, application, and QA deployment workflows."""

from __future__ import annotations

import argparse
import os
import shlex
import signal
import sys
import tempfile
import time
from collections.abc import Sequence
from pathlib import Path
from typing import Any

import yaml
from render_grafana_dashboard import render as render_dashboard
from render_prometheus_rule import render as render_rule

from check_monitoring import ValidationError
from monitoring_assets import AssetError, load_assets

ROOT = Path(__file__).resolve().parents[3]
K8S = ROOT / "k8s"
sys.path.insert(0, str(ROOT / "scripts" / "python"))
from process_runner import CommandError, run_command  # noqa: E402

CRDS = (
    "alertmanagerconfigs",
    "alertmanagers",
    "podmonitors",
    "probes",
    "prometheusagents",
    "prometheuses",
    "prometheusrules",
    "scrapeconfigs",
    "servicemonitors",
    "thanosrulers",
)
READABLE_CRDS = (
    "alertmanagers",
    "podmonitors",
    "probes",
    "prometheuses",
    "prometheusrules",
    "scrapeconfigs",
    "servicemonitors",
)


class OperationError(RuntimeError):
    """A monitoring operation failed before its postcondition."""


class Interrupted(RuntimeError):
    def __init__(self, signum: int) -> None:
        super().__init__(f"interrupted by {signal.Signals(signum).name}")
        self.status = 130 if signum == signal.SIGINT else 143


class Runner:
    """Capture bounded, owned commands using a shared work deadline."""

    def __init__(self, seconds: float = 1800) -> None:
        self.deadline = time.monotonic() + seconds
        self.completed: list[str] = []
        self.mutation_started = False

    def run(
        self,
        args: Sequence[str],
        *,
        label: str,
        timeout: float = 120,
        input_text: str | None = None,
    ) -> str:
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise OperationError(f"{label}: overall work deadline exceeded")
        started = time.monotonic()
        print(f"{label}: started", file=sys.stderr)
        try:
            result = run_command(
                args,
                cwd=K8S,
                env=os.environ,
                timeout_seconds=min(timeout, remaining),
                teardown_seconds=15,
                activity=label,
                input_text=input_text,
            )
        except CommandError as error:
            raise OperationError(str(error)) from error
        finally:
            print(f"{label}: ended after {time.monotonic() - started:.1f}s", file=sys.stderr)
        return result.stdout


def context(runner: Runner, expected: str) -> None:
    actual = runner.run(
        ["kubectl", "config", "current-context"], label="current context", timeout=20
    ).strip()
    if actual != expected:
        raise OperationError(f"Current context is {actual!r} (expected {expected!r})")


def values_path(assets: Any) -> Path:
    return assets.assets_root.parent / assets.environment["prometheus_values_path"]


def stack_template(
    runner: Runner,
    release: str,
    namespace: str,
    values: Path,
    version: str,
    *,
    only: str | None = None,
) -> str:
    command = [
        "helm",
        "template",
        release,
        "prometheus-community/kube-prometheus-stack",
        "--version",
        version,
        "--namespace",
        namespace,
        "--values",
        str(values),
    ]
    if only:
        command += ["--show-only", only]
    return runner.run(command, label="Prometheus stack template", timeout=240)


def receiver_check(args: argparse.Namespace, runner: Runner) -> None:
    rendered = stack_template(
        runner,
        "qa-prometheus",
        "qa-monitoring",
        args.prometheus_values,
        args.stack_version,
        only="templates/prometheus/prometheus.yaml",
    )
    runner.run(
        [
            sys.executable,
            str(K8S / "scripts/alloy/check_backend_config.py"),
            "--values",
            str(args.prometheus_values),
            "--makefile",
            str(K8S / "Makefile"),
            "--makefile",
            str(K8S / "tests/validation.mk"),
            "--rendered-prometheus",
            "-",
        ],
        label="Prometheus receiver validation",
        input_text=rendered,
    )
    stack_template(
        runner, "qa-prometheus", "qa-monitoring", args.prometheus_values, args.stack_version
    )
    print(f"✅ Prometheus chart {args.stack_version} receiver configuration passed.")


def assets_check(args: argparse.Namespace, runner: Runner) -> None:
    assets = load_assets(args.assets_root, args.environment, args.service)
    try:
        runner.run(
            ["docker", "image", "inspect", args.prometheus_image],
            label="pinned Prometheus image",
            timeout=30,
        )
    except OperationError as error:
        raise OperationError(
            f"Missing pinned image {args.prometheus_image}. Pull it explicitly before validation."
        ) from error
    rendered = stack_template(
        runner,
        f"{args.environment}-prometheus",
        assets.environment["monitoring_namespace"],
        values_path(assets),
        args.stack_version,
    )
    with tempfile.TemporaryDirectory(prefix="monitoring-check-") as directory:
        root = Path(directory)
        root.chmod(0o755)
        chart = root / "rendered.yaml"
        chart.write_text(rendered, encoding="utf-8")
        runner.run(
            [
                sys.executable,
                str(K8S / "scripts/monitoring/check_monitoring.py"),
                "--assets-root",
                str(args.assets_root),
                "--environment",
                args.environment,
                "--service",
                args.service,
                "--values",
                str(values_path(assets)),
                "--makefile",
                str(K8S / "Makefile"),
                "--makefile",
                str(K8S / "tests/validation.mk"),
                "--rendered-stack",
                str(chart),
            ],
            label="monitoring asset validation",
        )
        for action, mount, asset in (
            ("check", "/assets", assets.alerts_path),
            ("test", "/fixture", assets.alert_tests_path),
        ):
            runner.run(
                [
                    "docker",
                    "run",
                    "--rm",
                    "--mount",
                    f"type=bind,src={assets.service_dir},dst={mount},readonly",
                    "--entrypoint",
                    "/bin/promtool",
                    args.prometheus_image,
                    action,
                    "rules",
                    f"{mount}/{asset.name}",
                ],
                label=f"promtool {action} rules",
                timeout=180,
            )
    print(
        f"✅ {args.service} monitoring assets for {args.environment} passed chart {args.stack_version} and Prometheus v3.15.0 checks."
    )


def apply_asset(args: argparse.Namespace, runner: Runner) -> None:
    assets_check(args, runner)
    assets = load_assets(args.assets_root, args.environment, args.service)
    expected = assets.environment["kube_context"]
    context(runner, expected)
    if args.operation == "apply-rules":
        rendered = render_rule(args.assets_root, args.environment, args.service)
        kind = "prometheusrule"
        name = assets.contract["prometheus_rule_name"]
    else:
        rendered = render_dashboard(args.assets_root, args.environment, args.service)
        resource = yaml.safe_load(rendered)
        kind = "configmap"
        name = resource["metadata"]["name"]
    with tempfile.TemporaryDirectory(prefix="monitoring-apply-") as directory:
        path = Path(directory) / "resource.yaml"
        path.write_text(rendered, encoding="utf-8")
        prefix = [
            "kubectl",
            "--context",
            expected,
            "--namespace",
            assets.environment["monitoring_namespace"],
        ]
        runner.run(
            [*prefix, "apply", "--dry-run=server", "-f", str(path)],
            label="monitoring server dry-run",
            timeout=90,
        )
        runner.run([*prefix, "apply", "-f", str(path)], label="monitoring apply", timeout=90)
        runner.run([*prefix, "get", kind, name], label="monitoring postcondition", timeout=45)
    subject = "alert rules" if kind == "prometheusrule" else "immutable dashboard"
    print(f"✅ Applied {args.service} {subject} to {args.environment}.")


def alloy_check(args: argparse.Namespace, runner: Runner) -> None:
    try:
        runner.run(
            ["docker", "image", "inspect", args.alloy_image], label="pinned Alloy image", timeout=30
        )
    except OperationError as error:
        raise OperationError(
            f"Missing pinned image {args.alloy_image}. Pull it explicitly before validation."
        ) from error
    command = [
        "helm",
        "template",
        "qa-alloy",
        "grafana/alloy",
        "--version",
        args.alloy_version,
        "--namespace",
        "qa-monitoring",
        "--values",
        str(args.alloy_values),
        "--set-file",
        f"alloy.configMap.content={args.alloy_config}",
    ]
    rbac = runner.run(
        [*command, "--show-only", "templates/rbac.yaml"], label="Alloy RBAC template", timeout=240
    )
    runner.run(
        [
            sys.executable,
            str(K8S / "scripts/alloy/check_config.py"),
            "--values",
            str(args.alloy_values),
            "--config",
            str(args.alloy_config),
            "--makefile",
            str(K8S / "Makefile"),
            "--makefile",
            str(K8S / "tests/validation.mk"),
            "--operations",
            str(K8S / "scripts/monitoring/operations.py"),
            "--rendered-rbac",
            "-",
        ],
        label="Alloy source and RBAC validation",
        input_text=rbac,
    )
    rendered = runner.run(
        [*command, "--show-only", "templates/configmap.yaml"],
        label="Alloy ConfigMap template",
        timeout=240,
    )
    documents = [value for value in yaml.safe_load_all(rendered) if value is not None]
    if len(documents) != 1 or documents[0].get("data", {}).get("config.alloy", "").rstrip(
        "\n"
    ) != args.alloy_config.read_text(encoding="utf-8").rstrip("\n"):
        raise OperationError(f"Rendered Alloy ConfigMap differs from {args.alloy_config}")
    runner.run(
        ["docker", "run", "--rm", "-i", args.alloy_image, "validate", "/dev/stdin"],
        label="Alloy runtime validation",
        input_text=args.alloy_config.read_text(encoding="utf-8"),
        timeout=180,
    )
    runner.run(command, label="Alloy chart template", timeout=240)
    print(f"✅ Alloy chart {args.alloy_version} and {args.alloy_image} configuration passed.")


def install_crds(args: argparse.Namespace, runner: Runner) -> None:
    context(runner, args.context)
    runner.run(
        [
            "helm",
            "repo",
            "add",
            "prometheus-community",
            "https://prometheus-community.github.io/helm-charts",
            "--force-update",
        ],
        label="Prometheus Helm repository",
        timeout=90,
    )
    runner.run(
        ["helm", "repo", "update", "prometheus-community"],
        label="Prometheus Helm repository update",
        timeout=180,
    )
    print(
        f"Installing Prometheus Operator CRDs chart v{args.crds_version} into {args.namespace} namespace..."
    )
    runner.run(
        [
            "helm",
            "upgrade",
            "--install",
            "prometheus-operator-crds",
            "prometheus-community/prometheus-operator-crds",
            "--version",
            args.crds_version,
            "--kube-context",
            args.context,
            "--namespace",
            args.namespace,
            "--create-namespace",
            "--wait",
            "--timeout",
            "10m",
        ],
        label="Prometheus Operator CRD install",
        timeout=660,
    )
    print("Waiting for monitoring.coreos.com CRDs to be registered...")
    runner.run(
        [
            "kubectl",
            "--context",
            args.context,
            "wait",
            "--for=condition=Established",
            "--timeout=60s",
            *(f"crd/{name}.monitoring.coreos.com" for name in CRDS),
        ],
        label="Prometheus CRD readiness",
        timeout=75,
    )
    print("Confirming existing monitoring custom resources remain readable...")
    for name in READABLE_CRDS:
        runner.run(
            [
                "kubectl",
                "--context",
                args.context,
                "get",
                f"{name}.monitoring.coreos.com",
                "--all-namespaces",
            ],
            label=f"{name} readability",
            timeout=45,
        )


def make_target(runner: Runner, make: Sequence[str], target: str, *settings: str) -> None:
    print(f"Running {target} {' '.join(settings)}...", flush=True)
    runner.mutation_started = True
    output = runner.run(
        [*make, "--no-print-directory", "-C", str(K8S), target, *settings],
        label=f"make {target}",
        timeout=1200,
    )
    if output:
        print(output, end="" if output.endswith("\n") else "\n", flush=True)
    runner.completed.append(" ".join((target, *settings)))


def tenant_ready(runner: Runner, context_name: str, service: str) -> None:
    namespace = f"qa-minio-{service}"
    selector = f"v1.min.io/tenant={namespace}"
    runner.run(
        [
            "kubectl",
            "--context",
            context_name,
            "-n",
            namespace,
            "wait",
            "--for=condition=Ready",
            "pod",
            "-l",
            selector,
            "--timeout=180s",
        ],
        label=f"{service} tenant readiness",
        timeout=195,
    )
    pods = runner.run(
        [
            "kubectl",
            "--context",
            context_name,
            "-n",
            namespace,
            "get",
            "pods",
            "-l",
            selector,
            "-o",
            "name",
        ],
        label=f"{service} tenant postcondition",
        timeout=45,
    )
    if not pods.strip():
        raise OperationError(f"no MinIO tenant pod found in {namespace}")


def deploy_qa(args: argparse.Namespace, runner: Runner) -> None:
    if args.environment != "qa" or args.context != "qa-insurance-hub":
        raise OperationError("QA monitoring deployment requires qa-insurance-hub context")
    context(runner, args.context)
    make = shlex.split(args.make_command)
    if not make:
        raise OperationError("empty Make command")
    print("🚀 Deploying monitoring stack into 'qa' cluster...")
    for target in ("es-deploy", "prometheus-stack-install", "minio-operator-deploy"):
        make_target(runner, make, target)
    for service in ("loki", "tempo"):
        make_target(runner, make, "minio-storage-user-secret-create", f"SVC_NAME={service}")
        make_target(runner, make, "minio-storage-config-secret-create", f"SVC_NAME={service}")
    # Each tenant is an owned, bounded Make child. Sequential execution ensures a failed
    # deployment cannot be hidden by another child's success or an unqualified wait.
    for service in ("loki", "tempo"):
        make_target(runner, make, "minio-tenant-deploy", f"SVC_NAME={service}")
        tenant_ready(runner, args.context, service)
    for service, bucket, policy in (
        ("loki", "loki-logs", "apps/infra/loki/minio/s3-policy-loki-logs.json"),
        ("tempo", "tempo-traces", "apps/infra/tempo/minio/s3-policy-tempo-traces.json"),
    ):
        make_target(
            runner, make, "minio-svc-bucket-create", f"SVC_NAME={service}", f"BUCKET_NAME={bucket}"
        )
        make_target(
            runner,
            make,
            "minio-svc-user-secret-create",
            f"SVC_NAME={service}",
            f"COPY_SECRET_NS={args.namespace}",
        )
        make_target(
            runner,
            make,
            "minio-svc-user-with-policy-create",
            f"SVC_NAME={service}",
            f"POLICY_FILE={policy}",
        )
    for target in (
        "zipkin-es-user-secret-create",
        "zipkin-es-user-create",
        "zipkin-install",
        "loki-install",
        "tempo-install",
        "alloy-install",
    ):
        make_target(runner, make, target)
    print("✅ QA cluster monitoring stack deployed!")


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "operation",
        choices=(
            "crds-install",
            "apply-rules",
            "apply-dashboard",
            "receiver-check",
            "assets-check",
            "assets-check-all",
            "alloy-check",
            "qa-deploy",
        ),
    )
    parser.add_argument("--assets-root", type=Path, default=K8S / "observability")
    parser.add_argument("--environment", default="qa")
    parser.add_argument("--service")
    parser.add_argument("--services", nargs="*")
    parser.add_argument("--context", default="qa-insurance-hub")
    parser.add_argument("--namespace", default="qa-monitoring")
    parser.add_argument("--make-command", default="make")
    parser.add_argument("--crds-version", default="32.0.1")
    parser.add_argument("--stack-version", default="92.6.0")
    parser.add_argument(
        "--prometheus-image", default="quay.io/prometheus/prometheus:v3.15.0-distroless"
    )
    parser.add_argument(
        "--prometheus-values", type=Path, default=K8S / "overlays/qa/infra/prometheus/values.yaml"
    )
    parser.add_argument("--alloy-version", default="1.13.1")
    parser.add_argument("--alloy-image", default="grafana/alloy:v1.20.1")
    parser.add_argument(
        "--alloy-values", type=Path, default=K8S / "overlays/qa/infra/alloy/values.yaml"
    )
    parser.add_argument(
        "--alloy-config", type=Path, default=K8S / "overlays/qa/infra/alloy/config.alloy"
    )
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    previous = {name: signal.getsignal(name) for name in (signal.SIGINT, signal.SIGTERM)}

    def interrupt(signum: int, _frame: object) -> None:
        raise Interrupted(signum)

    for name in previous:
        signal.signal(name, interrupt)
    try:
        runner = Runner(7200 if args.operation == "qa-deploy" else 1800)
        if args.operation == "crds-install":
            install_crds(args, runner)
        elif args.operation == "receiver-check":
            receiver_check(args, runner)
        elif args.operation == "assets-check":
            if not args.service:
                raise OperationError("--service is required")
            assets_check(args, runner)
        elif args.operation == "assets-check-all":
            if not args.services:
                raise OperationError(
                    f"No service monitoring assets were found under {args.assets_root}/services"
                )
            for service in args.services:
                args.service = service
                assets_check(args, runner)
        elif args.operation in ("apply-rules", "apply-dashboard"):
            if not args.service:
                raise OperationError("--service is required")
            apply_asset(args, runner)
        elif args.operation == "alloy-check":
            alloy_check(args, runner)
        else:
            deploy_qa(args, runner)
    except (
        AssetError,
        ValidationError,
        OperationError,
        OSError,
        ValueError,
        yaml.YAMLError,
    ) as error:
        print(f"monitoring {args.operation} failed: {error}", file=sys.stderr)
        if args.operation == "qa-deploy" and runner.mutation_started:
            print(
                "QA monitoring deployment may be partial; completed: "
                + (", ".join(runner.completed) or "none confirmed"),
                file=sys.stderr,
            )
        return 1
    except Interrupted as error:
        print(f"monitoring {args.operation} {error}", file=sys.stderr)
        if args.operation == "qa-deploy" and runner.mutation_started:
            print(
                "QA monitoring deployment may be partial; completed: "
                + (", ".join(runner.completed) or "none confirmed"),
                file=sys.stderr,
            )
        return error.status
    finally:
        for name, handler in previous.items():
            signal.signal(name, handler)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
