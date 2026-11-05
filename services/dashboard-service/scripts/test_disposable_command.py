"""Fast process-lifecycle tests for disposable Dashboard commands."""

from __future__ import annotations

import os
import sys
import time
from copy import deepcopy
from pathlib import Path

import disposable_deployment
import pytest


def rendered_items() -> list[dict[str, object]]:
    metadata = {"namespace": "local-dev-all", "labels": {}}
    return [
        {
            "apiVersion": "v1",
            "kind": "ConfigMap",
            "metadata": {**deepcopy(metadata), "name": "local-dev-dashboard-config"},
            "data": {"SERVICE_VERSION": "git-aaaaaaaaaaaa"},
        },
        {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": {**deepcopy(metadata), "name": "local-dev-dashboard-api"},
            "spec": {
                "template": {
                    "metadata": {"labels": {}},
                    "spec": {"containers": [{"image": "old", "env": []}]},
                }
            },
        },
        {
            "apiVersion": "networking.k8s.io/v1",
            "kind": "NetworkPolicy",
            "metadata": {**deepcopy(metadata), "name": "local-dev-dashboard-api"},
            "spec": {"egress": []},
        },
    ]


def test_render_adds_owned_dependency_reader_secret_and_exact_identity(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    items = rendered_items()
    monkeypatch.setattr(disposable_deployment.checks, "render", lambda _environment: "rendered")
    monkeypatch.setattr(
        disposable_deployment.checks,
        "validate_render",
        lambda _environment, _text: deepcopy(items),
    )
    cluster = disposable_deployment.Cluster("dashboard-disposable-test")

    _rendered, actual = disposable_deployment.render(
        cluster,
        "insurance-hub-dashboard-api:1.0.0-local.0123456789ab",
        "0123456789ab",
        "elasticsearch@example@sha256:" + "a" * 64,
    )

    deployment = disposable_deployment.checks.resource(
        actual, "Deployment", "local-dev-dashboard-api"
    )
    assert deployment["spec"]["template"]["spec"]["containers"][0]["image"].endswith("0123456789ab")
    config = disposable_deployment.checks.resource(
        actual, "ConfigMap", "local-dev-dashboard-config"
    )["data"]
    assert config["SERVICE_VERSION"] == "git-0123456789ab"
    assert config["ELASTICSEARCH_USERNAME"] == disposable_deployment.READER_USERNAME
    secret = disposable_deployment.checks.resource(
        actual, "Secret", disposable_deployment.ELASTICSEARCH_NAME
    )
    assert secret["stringData"]["reader-password"] == disposable_deployment.READER_PASSWORD
    policy = disposable_deployment.checks.resource(
        actual, "NetworkPolicy", "local-dev-dashboard-api"
    )
    assert policy["spec"]["egress"][1]["ports"] == [{"port": 9200, "protocol": "TCP"}]


def test_run_rejects_missing_digest_qualified_dependency_before_commands(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("IMAGE_REF", "insurance-hub-dashboard-api:1.0.0-local.0123456789ab")
    monkeypatch.delenv("ELASTICSEARCH_IMAGE", raising=False)
    monkeypatch.setattr(
        disposable_deployment,
        "command",
        lambda *_args, **_kwargs: pytest.fail("command must not run"),
    )

    with pytest.raises(disposable_deployment.CheckFailed, match="digest-qualified"):
        disposable_deployment.run()


def test_selected_deployed_rpc_must_execute_and_pass() -> None:
    passed = '{"Action":"pass","Test":"TestDeployedDirectRPC"}'
    disposable_deployment.require_go_test_passed(passed, "TestDeployedDirectRPC")

    for output in (
        '{"Action":"pass","Package":"example/internal/service"}',
        '{"Action":"pass","Test":"TestRenamedDeployedDirectRPC"}',
    ):
        with pytest.raises(disposable_deployment.CheckFailed, match="did not execute"):
            disposable_deployment.require_go_test_passed(output, "TestDeployedDirectRPC")


def test_reader_permission_denial_rejects_wrong_outcomes() -> None:
    denied = disposable_deployment.HTTPProbeResponse(
        403,
        '{"error":{"type":"security_exception"},"status":403}',
    )
    disposable_deployment.require_elasticsearch_permission_denied("reader-write", denied)

    for response in (
        disposable_deployment.HTTPProbeResponse(0, "connection refused"),
        disposable_deployment.HTTPProbeResponse(401, denied.body),
        disposable_deployment.HTTPProbeResponse(404, denied.body),
        disposable_deployment.HTTPProbeResponse(403, '{"error":{"type":"other"},"status":403}'),
    ):
        with pytest.raises(disposable_deployment.CheckFailed, match="permission denial|not JSON"):
            disposable_deployment.require_elasticsearch_permission_denied("reader-write", response)


def test_dependency_failure_requires_exact_safe_response() -> None:
    safe = '{"code":14,"message":"service unavailable","details":[]}'
    expected = disposable_deployment.HTTPProbeResponse(503, safe)
    disposable_deployment.require_http_response("dependency-total-sales", expected, 503, safe)

    for response in (
        disposable_deployment.HTTPProbeResponse(500, safe),
        disposable_deployment.HTTPProbeResponse(503, '{"message":"private dependency failure"}'),
    ):
        with pytest.raises(disposable_deployment.CheckFailed, match="response differs"):
            disposable_deployment.require_http_response(
                "dependency-total-sales", response, 503, safe
            )


def assert_reaped(pid_file: Path) -> None:
    deadline = time.monotonic() + 2
    pid = int(pid_file.read_text(encoding="utf-8"))
    while time.monotonic() < deadline:
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            return
        time.sleep(0.02)
    raise AssertionError("owned disposable descendant was not reaped")


def descendant_program(pid_file: Path, *, interrupt_parent: bool) -> str:
    action = "os.kill(os.getppid(), signal.SIGINT); " if interrupt_parent else ""
    return (
        "import os, pathlib, signal, subprocess, sys, time; "
        "child=subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)']); "
        f"pathlib.Path({str(pid_file)!r}).write_text(str(child.pid)); "
        f"{action}"
        "time.sleep(60)"
    )


def test_timeout_reports_progress_and_reaps_descendant(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    pid_file = tmp_path / "timeout-child.pid"

    with pytest.raises(disposable_deployment.CheckFailed, match="deadline"):
        disposable_deployment.command(
            [sys.executable, "-c", descendant_program(pid_file, interrupt_parent=False)],
            timeout=0.3,
            heartbeat=0.1,
            teardown=2,
            activity="disposable timeout fixture",
        )

    assert "still running" in capsys.readouterr().err
    assert_reaped(pid_file)


def test_interruption_reaps_descendant(tmp_path: Path) -> None:
    pid_file = tmp_path / "interrupt-child.pid"

    with pytest.raises(KeyboardInterrupt):
        disposable_deployment.command(
            [sys.executable, "-c", descendant_program(pid_file, interrupt_parent=True)],
            timeout=5,
            heartbeat=1,
            teardown=2,
            activity="disposable interrupt fixture",
        )

    assert_reaped(pid_file)
