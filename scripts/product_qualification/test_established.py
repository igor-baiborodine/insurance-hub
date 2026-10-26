"""Established QA route checks against independently shaped live-state fakes."""

from __future__ import annotations

import json
from copy import deepcopy
from datetime import UTC, datetime, timedelta

import pytest

from scripts.product_qualification import ops
from scripts.product_qualification.core import QualificationError

REVISION = "main@sha1:" + "a" * 40


def route_state() -> tuple[dict, dict]:
    config_name = "qa-gateway-product-route-config-h5t75t5b9d"
    config_text = (
        "micronaut:\n  http:\n    services:\n      product-service:\n"
        f"        urls:\n          - {ops.JAVA_URL}\n"
    )
    spec = {
        "containers": [
            {
                "image": "gateway:1.3.0",
                "env": [
                    {
                        "name": "MICRONAUT_CONFIG_FILES",
                        "value": "/etc/product-route/application.yml",
                    }
                ],
                "volumeMounts": [
                    {
                        "name": "product-route-config",
                        "mountPath": "/etc/product-route",
                        "readOnly": True,
                    }
                ],
            }
        ],
        "volumes": [{"name": "product-route-config", "configMap": {"name": config_name}}],
    }
    gateway = {
        "metadata": {"name": "qa-gateway-1", "uid": "gateway-uid"},
        "spec": deepcopy(spec),
        "status": {
            "phase": "Running",
            "podIP": "10.0.0.1",
            "containerStatuses": [{"ready": True, "imageID": "gateway@sha256:abc"}],
        },
    }
    product = {
        "metadata": {"name": "qa-product-java-1", "uid": "product-uid"},
        "status": {
            "phase": "Running",
            "podIP": "10.0.0.2",
            "containerStatuses": [{"ready": True, "imageID": "java@sha256:def"}],
        },
    }
    ready = {"status": {"conditions": [{"type": "Ready", "status": "True"}]}}
    state = {
        ("flux-system", "gitrepository", "insurance-hub"): {
            **ready,
            "status": {**ready["status"], "artifact": {"revision": REVISION}},
        },
        ("flux-system", "kustomization", "qa-svc"): {
            **ready,
            "status": {**ready["status"], "lastAppliedRevision": REVISION},
        },
        ("qa-svc", "deployment", "qa-agent-portal-gateway-legacy"): {
            "metadata": {"generation": 3},
            "spec": {"replicas": 1, "template": {"spec": spec}},
            "status": {"observedGeneration": 3, "readyReplicas": 1, "updatedReplicas": 1},
        },
        ("qa-svc", "configmap", config_name): {"data": {"application.yml": config_text}},
        ("qa-svc", "pods", "app.kubernetes.io/name=agent-portal-gateway-legacy"): {
            "items": [gateway]
        },
        ("qa-svc", "pods", "app.kubernetes.io/name=product-api-legacy"): {"items": [product]},
    }
    endpoints = {
        "qa-agent-portal-gateway-legacy": {"10.0.0.1"},
        "qa-product-api-legacy": {"10.0.0.2"},
        "qa-product-api": {"10.0.0.3"},
    }
    return state, endpoints


def install(monkeypatch, state: dict, endpoints: dict) -> None:
    def kubectl(namespace: str, action: str, kind: str, name: str, *_args) -> dict:
        assert action == "get"
        if kind == "pods":
            assert name == "-l" and len(_args) == 1
            name = _args[0]
        return state[namespace, kind, name]

    monkeypatch.setattr(ops, "kubectl", kubectl)
    monkeypatch.setattr(ops, "endpoint_addresses", lambda service: endpoints[service])
    monkeypatch.setattr(
        ops,
        "command",
        lambda *_args, **_kwargs: next(
            item["data"]["application.yml"]
            for (namespace, kind, _name), item in state.items()
            if namespace == "qa-svc" and kind == "configmap"
        ),
    )


def test_established_status_requires_matching_serving_pods_mount_and_phase(monkeypatch) -> None:
    # given
    state, endpoints = route_state()
    install(monkeypatch, state, endpoints)

    # when / then
    result = ops.established_status(REVISION, "java")
    assert result["product_url"] == ops.JAVA_URL
    assert result["backend_pods"][0]["image_id"] == "java@sha256:def"
    with pytest.raises(QualificationError, match="selection"):
        ops.established_status(REVISION, "go")
    state["qa-svc", "pods", "app.kubernetes.io/name=agent-portal-gateway-legacy"]["items"][0][
        "spec"
    ]["volumes"] = []
    with pytest.raises(QualificationError, match="stale"):
        ops.established_status(REVISION, "java")


def test_established_status_rejects_stale_revision_and_wrong_backend(monkeypatch) -> None:
    # given
    state, endpoints = route_state()
    install(monkeypatch, state, endpoints)

    # when / then
    state["flux-system", "kustomization", "qa-svc"]["status"]["lastAppliedRevision"] = (
        "main@sha1:" + "b" * 40
    )
    with pytest.raises(QualificationError, match="revision"):
        ops.established_status(REVISION, "java")
    state["flux-system", "kustomization", "qa-svc"]["status"]["lastAppliedRevision"] = REVISION
    endpoints["qa-product-api-legacy"] = set()
    with pytest.raises(QualificationError, match="no Ready endpoints"):
        ops.established_status(REVISION, "java")


def test_established_restore_requires_two_full_rounds_before_deadline(
    tmp_path, monkeypatch
) -> None:
    # given
    token = tmp_path / "token"
    token.write_text("synthetic-secret", encoding="utf-8")
    token.chmod(0o600)
    output = tmp_path / "restore.json"
    calls = []
    monkeypatch.setattr(
        ops,
        "established_status",
        lambda *_args: (
            calls.append("route")
            or {
                "phase": "java",
                "product_url": ops.JAVA_URL,
                "gateway_pods": [{"name": "qa-gateway-1"}],
                "backend_pods": [{"name": "qa-product-java-1"}],
            }
        ),
    )
    monkeypatch.setattr(
        ops,
        "check_reads",
        lambda *_args: calls.append("reads") or dict.fromkeys(("list", *ops.CODES), "accepted"),
    )
    monkeypatch.setattr(ops.time, "sleep", lambda _seconds: None)
    published = datetime.now(UTC) - timedelta(seconds=10)
    attribution = tmp_path / "java-attribution.json"
    attribution.write_text(
        json.dumps(
            {
                "source": "Tempo",
                "revision": REVISION,
                "backend": "java",
                "operation": "list",
                "gateway_pod": "qa-gateway-1",
                "backend_pod": "qa-product-java-1",
                "trace_id": "a" * 32,
                "gateway_client_span_id": "b" * 16,
                "java_parent_span_id": "b" * 16,
                "observed_at_utc": datetime.now(UTC).isoformat(),
            }
        ),
        encoding="utf-8",
    )
    attribution.chmod(0o600)

    # when
    passed = ops.restore(
        REVISION,
        published.isoformat(),
        "",
        "http://127.0.0.1:18081",
        token,
        output,
        route="established",
        decision_at=(published - timedelta(seconds=2)).isoformat(),
        attribution_file=attribution,
    )

    # then
    assert passed
    assert calls.count("reads") == 2 and calls.count("route") == 3
    evidence = json.loads(output.read_text(encoding="utf-8"))
    assert evidence["status"] == "passed"
    assert len(evidence["observations"]) == 2
    assert evidence["java_attribution"]["trace_id"] == "a" * 32
    assert "synthetic-secret" not in output.read_text(encoding="utf-8")


def test_java_restoration_rejects_unattributed_or_go_route() -> None:
    with pytest.raises(QualificationError, match="attribution"):
        ops.require_restored_java({"phase": "java", "product_url": ops.JAVA_URL})
    with pytest.raises(QualificationError, match="attribution"):
        ops.require_restored_java(
            {
                "phase": "go",
                "product_url": ops.GO_URL,
                "gateway_pods": [{"name": "qa-gateway-1"}],
                "backend_pods": [{"name": "qa-product-1"}],
            }
        )


def test_java_attribution_requires_real_serving_pod_and_parent(tmp_path) -> None:
    # given
    published = datetime.now(UTC) - timedelta(seconds=5)
    state = {
        "gateway_pods": [{"name": "qa-gateway-1"}],
        "backend_pods": [{"name": "qa-product-java-1"}],
    }
    path = tmp_path / "attribution.json"
    value = {
        "source": "Tempo",
        "revision": REVISION,
        "backend": "java",
        "operation": "list",
        "gateway_pod": "qa-gateway-1",
        "backend_pod": "qa-product-java-1",
        "trace_id": "a" * 32,
        "gateway_client_span_id": "b" * 16,
        "java_parent_span_id": "c" * 16,
        "observed_at_utc": datetime.now(UTC).isoformat(),
    }
    path.write_text(json.dumps(value), encoding="utf-8")
    path.chmod(0o600)

    # when / then
    with pytest.raises(QualificationError, match="attributed"):
        ops.verify_java_attribution(path, REVISION, state, published)
    value["java_parent_span_id"] = "b" * 16
    value["backend_pod"] = "qa-other-product"
    path.write_text(json.dumps(value), encoding="utf-8")
    with pytest.raises(QualificationError, match="attributed"):
        ops.verify_java_attribution(path, REVISION, state, published)


def test_established_restore_never_accepts_after_publication_deadline(
    tmp_path, monkeypatch
) -> None:
    # given
    token = tmp_path / "token"
    token.write_text("synthetic-secret", encoding="utf-8")
    token.chmod(0o600)
    output = tmp_path / "late.json"
    monkeypatch.setattr(ops, "established_status", lambda *_args: pytest.fail("late route read"))
    published = datetime.now(UTC) - timedelta(seconds=901)

    # when
    passed = ops.restore(
        REVISION,
        published.isoformat(),
        "",
        "http://127.0.0.1:18081",
        token,
        output,
        route="established",
        decision_at=(published - timedelta(seconds=2)).isoformat(),
        attribution_file=tmp_path / "not-yet-produced.json",
    )

    # then
    assert not passed
    assert json.loads(output.read_text(encoding="utf-8"))["status"] == "recovery-timeout"
