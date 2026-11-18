"""Focused unit and disposable integration checks for the Chat deployment smoke."""

from __future__ import annotations

import json
import os
import signal
from unittest.mock import Mock

import chat_deployment_checks as checks
import disposable_deployment
import pytest


@pytest.mark.parametrize(
    ("variable", "foreign_value", "message"),
    [
        ("QUALIFICATION_CONTEXT", "qa-insurance-hub", "context differs"),
        ("QUALIFICATION_KIND_CLUSTER", "foreign-cluster", "cluster differs"),
    ],
)
def test_qualification_target_rejects_foreign_coordinates_before_command(
    monkeypatch: pytest.MonkeyPatch, variable: str, foreign_value: str, message: str
) -> None:
    # given
    monkeypatch.setenv("QUALIFICATION_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("QUALIFICATION_KIND_CLUSTER", "local-dev-insurance-hub")
    monkeypatch.setenv(variable, foreign_value)
    command = Mock()
    monkeypatch.setattr(disposable_deployment.checks, "run", command)

    # when / then
    with pytest.raises(disposable_deployment.DisposableError, match=message):
        disposable_deployment.qualification_target()
    command.assert_not_called()


def test_inspect_image_rejects_mismatched_oci_identity(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    image = "insurance-hub-chat-api:1.0.0-local.aaaaaaaaaaaa"
    details = [
        {
            "Id": "sha256:" + "c" * 64,
            "Config": {
                "Labels": {
                    "org.opencontainers.image.revision": "b" * 40,
                    "org.opencontainers.image.version": "git-aaaaaaaaaaaa",
                }
            },
        }
    ]
    monkeypatch.setattr(disposable_deployment.checks, "run", lambda _arguments: json.dumps(details))

    # when / then
    with pytest.raises(disposable_deployment.DisposableError, match="OCI identity"):
        disposable_deployment.inspect_image(image, "a" * 40)


def test_prepared_resources_rewrite_owned_identity_and_default_deny() -> None:
    # given
    source_revision = "a" * 40
    image = "insurance-hub-chat-api:1.0.0-local.aaaaaaaaaaaa"

    # when
    items = disposable_deployment.prepared_resources(
        checks.render("local-dev"),
        "chat-smoke-12345678",
        image,
        source_revision,
    )

    # then
    assert {item["metadata"]["namespace"] for item in items} == {"chat-smoke-12345678"}
    deployment = checks.resource(items, "Deployment", "local-dev-chat-api")
    assert deployment["spec"]["template"]["spec"]["containers"][0]["image"] == image
    assert deployment["metadata"]["labels"]["app.kubernetes.io/version"] == "git-aaaaaaaaaaaa"
    config = checks.resource(items, "ConfigMap", "local-dev-chat-config")
    assert config["data"]["SERVICE_VERSION"] == "git-aaaaaaaaaaaa"
    policy = checks.resource(items, "NetworkPolicy", "local-dev-chat-api")
    assert policy["spec"]["ingress"] == []
    assert policy["spec"]["egress"] == []


def test_execute_cleans_namespace_after_partial_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setattr(disposable_deployment, "verify_target", lambda _context: None)
    monkeypatch.setattr(disposable_deployment, "create_namespace", Mock())
    load = Mock(side_effect=checks.DeploymentError("controlled load failure"))
    monkeypatch.setattr(disposable_deployment.checks, "run", load)
    cleanup = Mock()
    monkeypatch.setattr(disposable_deployment, "delete_namespace", cleanup)

    # when / then
    with pytest.raises(checks.DeploymentError, match="controlled load failure"):
        disposable_deployment.execute(
            "kind-local-dev-insurance-hub",
            "local-dev-insurance-hub",
            "chat-smoke-12345678",
            "insurance-hub-chat-api:1.0.0-local.aaaaaaaaaaaa",
            "a" * 40,
            [],
        )
    cleanup.assert_called_once_with("kind-local-dev-insurance-hub", "chat-smoke-12345678")


def test_execute_cleans_namespace_after_uncertain_creation_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    monkeypatch.setattr(disposable_deployment, "verify_target", lambda _context: None)
    monkeypatch.setattr(
        disposable_deployment,
        "create_namespace",
        Mock(side_effect=checks.DeploymentError("namespace create response was lost")),
    )
    cleanup = Mock()
    monkeypatch.setattr(disposable_deployment, "delete_namespace", cleanup)

    # when / then
    with pytest.raises(checks.DeploymentError, match="create response was lost"):
        disposable_deployment.execute(
            "kind-local-dev-insurance-hub",
            "local-dev-insurance-hub",
            "chat-smoke-12345678",
            "insurance-hub-chat-api:1.0.0-local.aaaaaaaaaaaa",
            "a" * 40,
            [],
        )
    cleanup.assert_called_once_with("kind-local-dev-insurance-hub", "chat-smoke-12345678")


def test_delete_namespace_rejects_foreign_resource(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    command = Mock(return_value=json.dumps({"metadata": {"labels": {}}}))
    monkeypatch.setattr(disposable_deployment, "kubectl", command)

    # when / then
    with pytest.raises(disposable_deployment.DisposableError, match="foreign namespace"):
        disposable_deployment.delete_namespace(
            "kind-local-dev-insurance-hub", "chat-smoke-12345678"
        )
    assert command.call_count == 1


def test_deployed_response_contract_accepts_chat_boundaries() -> None:
    checks.validate_deployed_responses(
        (200, b"live\n"),
        (200, b"ready\n"),
        (400, b"bad request\n"),
        (404, b"404 page not found\n"),
    )


@pytest.mark.parametrize(
    ("liveness", "readiness", "non_upgrade", "wrong_path", "message"),
    [
        (
            (503, b"not live\n"),
            (200, b"ready\n"),
            (400, b"bad request\n"),
            (404, b"404 page not found\n"),
            "liveness",
        ),
        (
            (200, b"live\n"),
            (503, b"not ready\n"),
            (400, b"bad request\n"),
            (404, b"404 page not found\n"),
            "readiness",
        ),
        (
            (200, b"live\n"),
            (200, b"ready\n"),
            (200, b"unexpected\n"),
            (404, b"404 page not found\n"),
            "non-upgrade",
        ),
        (
            (200, b"live\n"),
            (200, b"ready\n"),
            (400, b"bad request\n"),
            (200, b"unexpected\n"),
            "wrong-path",
        ),
    ],
)
def test_deployed_response_contract_rejects_changed_response(
    liveness: tuple[int, bytes],
    readiness: tuple[int, bytes],
    non_upgrade: tuple[int, bytes],
    wrong_path: tuple[int, bytes],
    message: str,
) -> None:
    # given
    responses = (liveness, readiness, non_upgrade, wrong_path)

    # when / then
    with pytest.raises(checks.DeploymentError, match=message):
        checks.validate_deployed_responses(*responses)


def test_cli_preserves_interrupt_status(monkeypatch: pytest.MonkeyPatch) -> None:
    for requested, expected in ((signal.SIGINT, 130), (signal.SIGTERM, 143)):
        # given
        def interrupted(selected=requested):
            os.kill(os.getpid(), selected)
            return 0

        monkeypatch.setattr(disposable_deployment, "main", interrupted)

        # when / then
        assert disposable_deployment.cli() == expected


@pytest.mark.integration
def test_chat_disposable_deployment() -> None:
    # given
    assert os.environ.get("IMAGE_REF")
    assert os.environ.get("QUALIFICATION_SOURCE_REVISION")

    # when / then
    assert disposable_deployment.main() == 0
