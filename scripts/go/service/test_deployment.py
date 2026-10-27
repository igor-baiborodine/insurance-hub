"""Shared lifecycle contracts with independent service and target fixtures."""

from __future__ import annotations

import json
import sys
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
import yaml

sys.path.insert(0, str(Path(__file__).resolve().parents[3]))
from scripts.go.service import deployment  # noqa: E402
from scripts.go.service.configuration import service_record, target_record  # noqa: E402
from test_release import create_service  # noqa: E402


@pytest.mark.parametrize(("service", "environment"), [("product", "qa"), ("claims", "staging")])
def test_flux_targets_reject_direct_mutation_before_contact(tmp_path, service, environment):
    # given
    create_service(tmp_path, service, environment=environment)
    record = service_record(tmp_path, service)
    target = SimpleNamespace(
        configuration=target_record(tmp_path, record, environment), coordinates=Mock()
    )
    hooks = SimpleNamespace(Target=lambda: target)

    # when / then
    for operation in ("deploy", "image-load", "stop"):
        with pytest.raises(deployment.DeploymentError, match="forbidden"):
            deployment.execute(hooks, [operation])
    target.coordinates.assert_not_called()


@pytest.mark.parametrize("with_initialization", [False, True])
def test_deploy_waits_for_initialization_before_api_apply(
    tmp_path, monkeypatch, with_initialization
):
    # given
    create_service(tmp_path, "claims", environment="sandbox", initialization=with_initialization)
    record = service_record(tmp_path, "claims")
    config = record["deploymentTargets"]["sandbox"]
    config["mode"] = "direct"
    events = []
    target = SimpleNamespace(
        service="claims",
        configuration=config,
        coordinates=lambda: None,
        rendered=lambda: ("api-yaml", [], "image"),
        live=lambda *a, **k: None,
    )
    hooks = SimpleNamespace(
        Target=lambda: target,
        check_render=lambda: None,
        secret_and_image=lambda *a: None,
        initialization_check=lambda *a: events.append("verified"),
        administration_snapshot=lambda *a: {},
        record_snapshot=lambda *a: None,
        initialization_complete=lambda *a: events.append("initialized"),
        role_snapshot=lambda *a: {"protected": "unchanged"},
        rollout=lambda *a: events.append("rollout"),
        run=Mock(),
    )
    monkeypatch.setattr(
        deployment, "local_administration_job", lambda *a: ("claims-init", "job-yaml")
    )
    monkeypatch.setattr(deployment, "apply", lambda target, text, manager: events.append(text))
    monkeypatch.setattr(deployment, "wait_for_job", lambda *a: events.append("complete"))

    # when
    assert deployment.execute(hooks, ["deploy"]) == 0

    # then
    assert events == (
        ["verified", "job-yaml", "complete", "initialized"] if with_initialization else []
    ) + ["api-yaml", "rollout"]


def test_failed_job_never_applies_api_and_is_retained(monkeypatch):
    # given
    target = SimpleNamespace(
        configuration={"initialization": {"waitSeconds": 420}},
        json=lambda *a: {"status": {"conditions": [{"type": "Failed", "status": "True"}]}},
    )
    sleeper = Mock()
    monkeypatch.setattr(deployment.time, "sleep", sleeper)

    # when / then
    with pytest.raises(deployment.DeploymentError, match="failed and was retained"):
        deployment.wait_for_job(target, "claims-init")
    sleeper.assert_not_called()


def test_local_job_uses_selected_assets_timeout_and_attempt(tmp_path, monkeypatch):
    # given
    create_service(tmp_path, "claims", environment="sandbox", initialization=True)
    catalog_path = tmp_path / "scripts/go/service/catalog.json"
    catalog = json.loads(catalog_path.read_text())
    config = catalog["services"]["claims"]["deploymentTargets"]["sandbox"]
    config.update(mode="direct", kindCluster="sandbox-cluster")
    config["initialization"].update(migrationTimeout="2m", attempt=3)
    catalog_path.write_text(json.dumps(catalog))
    record = service_record(tmp_path, "claims")
    config = target_record(tmp_path, record, "sandbox")
    image = "claims:local"
    job = {
        "kind": "Job",
        "metadata": {"name": "sandbox-claims-init"},
        "spec": {
            "backoffLimit": 0,
            "activeDeadlineSeconds": 300,
            "template": {
                "spec": {
                    "containers": [{"name": "migrate", "image": image, "args": ["--timeout=120s"]}]
                }
            },
        },
    }
    target = SimpleNamespace(
        service="claims",
        environment="sandbox",
        record=record,
        configuration=config,
        kubectl=lambda *a: yaml.safe_dump(job),
    )
    inspect = json.dumps(
        [
            {
                "Id": "sha256:" + "b" * 64,
                "Config": {"Labels": {"org.opencontainers.image.revision": "a" * 40}},
            }
        ]
    )
    monkeypatch.setattr(deployment, "ROOT", tmp_path)

    # when
    name, rendered = deployment.local_administration_job(target, image, lambda *a: inspect)

    # then
    assert name.startswith("sandbox-claims-init-") and name.endswith("-a3")
    value = yaml.safe_load(rendered)
    assert value["spec"]["template"]["spec"]["containers"][0]["args"] == ["--timeout=120s"]
    assert value["metadata"]["annotations"]["insurance-hub.io/source-revision"] == "a" * 40


def test_release_image_uses_digest_and_rejects_wrong_source(tmp_path, monkeypatch):
    # given
    from scripts.go.service.release import prepare_release

    create_service(tmp_path, "claims", environment="staging", initialization=True)
    prepare_release(
        tmp_path,
        "claims",
        "igor-baiborodine/insurance-hub",
        "1.2.3",
        "a" * 40,
        "sha256:" + "b" * 64,
    )
    record = service_record(tmp_path, "claims")
    target = SimpleNamespace(
        record=record, configuration=target_record(tmp_path, record, "staging")
    )
    monkeypatch.setattr(deployment, "ROOT", tmp_path)
    monkeypatch.setenv("DEPLOY_SOURCE_REVISION", "a" * 40)

    # when / then
    assert deployment.release_image(target) == record["registryImage"] + "@sha256:" + "b" * 64
    monkeypatch.setenv("DEPLOY_SOURCE_REVISION", "c" * 40)
    with pytest.raises(deployment.DeploymentError, match="source differs"):
        deployment.release_image(target)


def test_failed_initialization_prevents_api_apply(monkeypatch):
    # given
    target = SimpleNamespace(
        service="claims",
        configuration={"mode": "direct", "initialization": {}},
        coordinates=lambda: None,
        rendered=lambda: ("api", [], "image"),
        live=lambda *a, **k: None,
    )
    hooks = SimpleNamespace(
        Target=lambda: target,
        check_render=lambda: None,
        secret_and_image=lambda *a: None,
        initialization_check=lambda *a: None,
        record_snapshot=lambda *a: None,
        administration_snapshot=lambda *a: {},
        run=Mock(),
        role_snapshot=Mock(),
    )
    applied = []
    monkeypatch.setattr(deployment, "local_administration_job", lambda *a: ("claims-init", "job"))
    monkeypatch.setattr(deployment, "apply", lambda target, text, manager: applied.append(text))
    monkeypatch.setattr(
        deployment, "wait_for_job", Mock(side_effect=deployment.DeploymentError("Job failed"))
    )

    # when / then
    with pytest.raises(deployment.DeploymentError, match="Job failed"):
        deployment.execute(hooks, ["deploy"])
    assert applied == ["job"]
    hooks.role_snapshot.assert_not_called()
