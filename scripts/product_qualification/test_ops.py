"""Guarded preview operations and restoration evidence."""

from __future__ import annotations

import json
import os
import signal
from datetime import UTC, datetime, timedelta

import pytest
import yaml

from scripts.product_qualification import ops
from scripts.product_qualification.core import QualificationError

REVISION = "main@sha1:" + "a" * 40


def test_render_has_isolated_java_default() -> None:
    result = ops.render()
    assert result["product_url"] == ops.JAVA_URL
    assert len(result["resources"]) == 6
    assert result["config_map"].startswith("qa-gateway-product-preview-config-")


def test_wrong_revision_and_target_rejected_before_kubectl(monkeypatch) -> None:
    monkeypatch.setattr(ops, "kubectl", lambda *_args: pytest.fail("unexpected QA call"))
    with pytest.raises(QualificationError, match="revision"):
        ops.status("main", "java")
    with pytest.raises(QualificationError, match="UTC"):
        ops.utc_time("2026-10-21T10:00:00-04:00")
    assert ops.main(["forward", "--service", "preview", "--local-port", "80"]) == 1


def test_ready_java_route_and_selector_isolation(monkeypatch) -> None:
    # given
    resources = list(
        yaml.safe_load_all(
            ops.command("kubectl", "kustomize", "k8s/overlays/qa/svc/product-preview")
        )
    )
    by_resource = {(item["kind"].lower(), item["metadata"]["name"]): item for item in resources}
    for item in resources:
        if item["kind"] == "Deployment":
            item["status"] = {"readyReplicas": 1, "updatedReplicas": 1}
    source = {
        "status": {
            "artifact": {"revision": REVISION},
            "conditions": [{"type": "Ready", "status": "True"}],
        }
    }
    applied = {
        "status": {
            "lastAppliedRevision": REVISION,
            "conditions": [{"type": "Ready", "status": "True"}],
        }
    }

    def pods(name: str, address: str) -> dict:
        deployment = (
            "qa-agent-portal-gateway-product-preview"
            if "gateway" in name
            else "qa-web-vue-product-preview"
        )
        return {
            "items": [
                {
                    "metadata": {"name": name, "uid": name},
                    "spec": by_resource["deployment", deployment]["spec"]["template"]["spec"],
                    "status": {
                        "phase": "Running",
                        "podIP": address,
                        "containerStatuses": [{"ready": True, "imageID": "image@sha256:abc"}],
                    },
                }
            ]
        }

    def kubectl(_namespace: str, action: str, kind: str, name: str, *_args) -> dict:
        assert action == "get"
        if kind == "gitrepository":
            return source
        if kind == "kustomization":
            return applied
        if kind == "pods":
            return (
                pods("qa-preview-gateway-1", "10.0.0.1")
                if "gateway" in _args[0]
                else pods("qa-preview-vue-1", "10.0.0.2")
            )
        return by_resource[kind.lower(), name]

    addresses = {
        "qa-agent-portal-gateway-product-preview": {"10.0.0.1"},
        "qa-agent-portal-gateway-legacy": {"10.0.0.3"},
        "qa-web-vue-product-preview": {"10.0.0.2"},
        "qa-web-vue-legacy": {"10.0.0.4"},
    }
    monkeypatch.setattr(ops, "kubectl", kubectl)
    monkeypatch.setattr(ops, "endpoint_addresses", lambda service: addresses[service])

    # when / then
    assert ops.status(REVISION, "java")["frontend_pod"] == "qa-preview-vue-1"
    addresses["qa-agent-portal-gateway-legacy"] = {"10.0.0.1"}
    with pytest.raises(QualificationError, match="isolation"):
        ops.status(REVISION, "java")


def test_restoration_requires_two_rounds_and_records_bound(tmp_path, monkeypatch) -> None:
    # given
    token_file = tmp_path / "token"
    token_file.write_text("synthetic-secret", encoding="utf-8")
    token_file.chmod(0o600)
    fixture = json.loads(
        ops.Path(
            "legacy/product-service/src/test/resources/product-read-baseline/http/qa.json"
        ).read_text(encoding="utf-8")
    )
    list_body = next(
        item["response"]["rawBody"].encode()
        for item in fixture["observations"]
        if item["scenarioId"] == "HTTP-GATEWAY-LIST-001"
    )
    status_calls = []
    rounds = []
    monkeypatch.setattr(
        ops,
        "status",
        lambda revision, phase: status_calls.append((revision, phase)) or {"phase": phase},
    )
    monkeypatch.setattr(ops, "check_reads", lambda *_args: rounds.append("accepted"))
    monkeypatch.setattr(ops, "http_request", lambda *_args: (200, list_body))
    monkeypatch.setattr(ops.time, "sleep", lambda _seconds: None)
    published = datetime.now(UTC) - timedelta(minutes=16)
    output = tmp_path / "restore.json"

    # when
    passed = ops.restore(
        REVISION,
        published.isoformat(),
        "http://127.0.0.1:18080",
        "http://127.0.0.1:18081",
        token_file,
        output,
    )

    # then
    evidence = json.loads(output.read_text(encoding="utf-8"))
    assert not passed and evidence["status"] == "bound-exceeded"
    assert len(rounds) == 2 and len(status_calls) == 2
    assert len(evidence["observations"]) == 2
    assert "synthetic-secret" not in output.read_text(encoding="utf-8")
    assert os.stat(output).st_mode & 0o777 == 0o600


def test_restoration_interruption_retains_partial_evidence(tmp_path, monkeypatch) -> None:
    token_file = tmp_path / "token"
    token_file.write_text("synthetic-secret", encoding="utf-8")
    token_file.chmod(0o600)
    output = tmp_path / "interrupted.json"
    monkeypatch.setattr(ops, "status", lambda *_args: (_ for _ in ()).throw(ops.Interrupted(130)))
    before = signal.getsignal(signal.SIGINT)
    with pytest.raises(ops.Interrupted) as raised:
        ops.restore(
            REVISION,
            datetime.now(UTC).isoformat(),
            "http://127.0.0.1:18080",
            "http://127.0.0.1:18081",
            token_file,
            output,
        )
    assert raised.value.code == 130
    assert signal.getsignal(signal.SIGINT) == before
    evidence = json.loads(output.read_text(encoding="utf-8"))
    assert evidence["status"] == "interrupted"
    assert "synthetic-secret" not in output.read_text(encoding="utf-8")
