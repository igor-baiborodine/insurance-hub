"""Fake-command scenarios for bounded monitoring ownership and Make selection."""

from __future__ import annotations

import os
import signal
import subprocess
import sys
import time
from pathlib import Path
from types import SimpleNamespace

import operations
import pytest

ASSETS = Path(__file__).parents[2] / "observability"


class FakeRunner:
    def __init__(self, *, context: str = "qa-insurance-hub", fail: str = "") -> None:
        self.context = context
        self.fail = fail
        self.calls: list[list[str]] = []
        self.completed: list[str] = []
        self.mutation_started = False

    def run(self, args, *, label: str, **_kwargs) -> str:
        self.calls.append(list(args))
        if self.fail and self.fail in " ".join(args):
            raise operations.OperationError(f"{label} failed")
        if list(args) == ["kubectl", "config", "current-context"]:
            return self.context + "\n"
        if "-o" in args and "name" in args:
            return "pod/tenant-0\n"
        return "ok\n"


def test_crd_install_waits_for_every_crd_and_checks_readability() -> None:
    # given
    runner = FakeRunner()
    args = SimpleNamespace(
        context="qa-insurance-hub", namespace="qa-monitoring", crds_version="32.0.1"
    )

    # when
    operations.install_crds(args, runner)

    # then
    joined = [" ".join(call) for call in runner.calls]
    assert "--version 32.0.1" in joined[3]
    assert all(f"crd/{name}.monitoring.coreos.com" in joined[4] for name in operations.CRDS)
    assert len(operations.READABLE_CRDS) == len(runner.calls[5:])


def test_apply_wrong_context_stops_before_kubernetes_mutation(monkeypatch) -> None:
    # given
    runner = FakeRunner(context="kind-local-dev-insurance-hub")
    args = SimpleNamespace(
        assets_root=ASSETS,
        environment="qa",
        service="product",
        operation="apply-rules",
        prometheus_image="image",
        stack_version="92.6.0",
    )
    monkeypatch.setattr(operations, "assets_check", lambda *_args: None)

    # when / then
    with pytest.raises(operations.OperationError, match="Current context"):
        operations.apply_asset(args, runner)
    assert not any("apply" in call for call in runner.calls)


def test_apply_dry_run_precedes_mutation_and_postcondition(monkeypatch) -> None:
    # given
    runner = FakeRunner()
    args = SimpleNamespace(
        assets_root=ASSETS,
        environment="qa",
        service="product",
        operation="apply-dashboard",
        prometheus_image="image",
        stack_version="92.6.0",
    )
    monkeypatch.setattr(operations, "assets_check", lambda *_args: None)

    # when
    operations.apply_asset(args, runner)

    # then
    kubernetes = [call for call in runner.calls if call[0] == "kubectl" and "--namespace" in call]
    assert ["apply", "apply", "get"] == [
        next(value for value in call if value in {"apply", "get"}) for call in kubernetes
    ]
    assert "--dry-run=server" in kubernetes[0]
    assert "--dry-run=server" not in kubernetes[1]
    assert "configmap" in kubernetes[2]


def test_qa_deploy_halts_on_failed_tenant_before_bucket_creation() -> None:
    # given
    runner = FakeRunner(fail="minio-tenant-deploy SVC_NAME=loki")
    args = SimpleNamespace(
        environment="qa", context="qa-insurance-hub", namespace="qa-monitoring", make_command="make"
    )

    # when / then
    with pytest.raises(operations.OperationError, match="minio-tenant-deploy"):
        operations.deploy_qa(args, runner)
    joined = [" ".join(call) for call in runner.calls]
    assert any("minio-tenant-deploy SVC_NAME=loki" in call for call in joined)
    assert not any("minio-svc-bucket-create" in call for call in joined)
    assert runner.completed[-1] == "minio-storage-config-secret-create SVC_NAME=tempo"


def test_tenant_readiness_waits_for_operator_to_create_pod(monkeypatch) -> None:
    # given
    runner = FakeRunner()
    inventories = iter(("", "", "pod/tenant-0\n"))
    now = [0.0]

    def run(args, *, label: str, **_kwargs):
        runner.calls.append(list(args))
        if "get" in args:
            return next(inventories)
        return ""

    runner.run = run
    monkeypatch.setattr(operations.time, "monotonic", lambda: now[0])
    monkeypatch.setattr(
        operations.time, "sleep", lambda seconds: now.__setitem__(0, now[0] + seconds)
    )

    # when
    operations.tenant_ready(runner, "qa-insurance-hub", "loki")

    # then
    discovery_calls = [call for call in runner.calls if "get" in call]
    assert len(discovery_calls) == 3
    assert "wait" in runner.calls[-1]


def test_qa_deploy_requires_exact_context_before_child_mutation() -> None:
    # given
    runner = FakeRunner()
    args = SimpleNamespace(
        environment="qa",
        context="kind-local-dev-insurance-hub",
        namespace="qa-monitoring",
        make_command="make",
    )

    # when / then
    with pytest.raises(operations.OperationError, match="qa-insurance-hub"):
        operations.deploy_qa(args, runner)
    assert runner.calls == []


def test_qa_deploy_handles_parent_termination(monkeypatch) -> None:
    # given
    monkeypatch.setattr(operations, "deploy_qa", lambda *_args: signal.raise_signal(signal.SIGTERM))

    # when
    status = operations.main(["qa-deploy"])

    # then
    assert status == 143


def test_owned_command_unwinds_after_parent_only_termination(tmp_path: Path) -> None:
    # given
    started = tmp_path / "started"
    cleaned = tmp_path / "cleaned"
    child = tmp_path / "child.py"
    child.write_text(
        "import signal,sys,time\n"
        "from pathlib import Path\n"
        "signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))\n"
        "Path(sys.argv[1]).write_text('started')\n"
        "try: time.sleep(60)\n"
        "finally: Path(sys.argv[2]).write_text('cleaned')\n",
        encoding="utf-8",
    )
    parent = tmp_path / "parent.py"
    parent.write_text(
        "import signal,sys\n"
        "from pathlib import Path\n"
        "import operations\n"
        "signal.signal(signal.SIGTERM, lambda signum,_frame: (_ for _ in ()).throw(operations.Interrupted(signum)))\n"
        "root=Path(sys.argv[1])\n"
        "try: operations.Runner(30).run([sys.executable,str(root/'child.py'),str(root/'started'),str(root/'cleaned')],label='owned fake child')\n"
        "except operations.Interrupted as error: sys.exit(error.status)\n",
        encoding="utf-8",
    )
    process = subprocess.Popen(
        [sys.executable, str(parent), str(tmp_path)],
        env={**os.environ, "PYTHONPATH": str(Path(operations.__file__).parent)},
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        start_new_session=True,
    )
    try:
        deadline = time.monotonic() + 5
        while not started.exists() and time.monotonic() < deadline:
            time.sleep(0.02)
        assert started.exists(), process.communicate(timeout=2)

        # when
        os.kill(process.pid, signal.SIGTERM)
        _stdout, stderr = process.communicate(timeout=10)

        # then
        assert process.returncode == 143, stderr
        assert cleaned.read_text(encoding="utf-8") == "cleaned"
    finally:
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGKILL)
            process.communicate(timeout=2)
