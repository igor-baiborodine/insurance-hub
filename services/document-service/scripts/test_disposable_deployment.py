"""Focused checks for the owned Document disposable deployment."""

from __future__ import annotations

import os
from unittest.mock import Mock

import disposable_deployment
import document_deployment_checks as checks
import pytest


@pytest.mark.parametrize(
    ("variable", "foreign_value", "message"),
    [
        ("QUALIFICATION_CONTEXT", "qa-insurance-hub", "context differs"),
        ("QUALIFICATION_KIND_CLUSTER", "foreign-cluster", "cluster differs"),
    ],
)
def test_main_rejects_foreign_target_before_command(
    monkeypatch: pytest.MonkeyPatch, variable: str, foreign_value: str, message: str
) -> None:
    # given
    monkeypatch.setenv("QUALIFICATION_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("QUALIFICATION_KIND_CLUSTER", "local-dev-insurance-hub")
    monkeypatch.setenv(variable, foreign_value)
    kubectl = Mock()
    command = Mock()
    monkeypatch.setattr(disposable_deployment, "kubectl", kubectl)
    monkeypatch.setattr(disposable_deployment.checks, "run", command)

    # when / then
    with pytest.raises(disposable_deployment.DisposableError, match=message):
        disposable_deployment.main()
    kubectl.assert_not_called()
    command.assert_not_called()


def test_prepared_resources_rejects_mismatched_source() -> None:
    with pytest.raises(disposable_deployment.DisposableError, match="source identity"):
        disposable_deployment.prepared_resources(
            "",
            "document-smoke-12345678",
            "insurance-hub-document-api:1.0.0-local.aaaaaaaaaaaa",
            "b" * 40,
        )


def test_prepared_resources_rewrites_only_owned_identity() -> None:
    rendered = checks.render("local-dev")
    source = "a" * 40
    items = disposable_deployment.prepared_resources(
        rendered,
        "document-smoke-12345678",
        "insurance-hub-document-api:1.0.0-local.aaaaaaaaaaaa",
        source,
    )
    assert {item["metadata"]["namespace"] for item in items} == {"document-smoke-12345678"}
    deployment = checks.resource(items, "Deployment", "local-dev-document-api")
    assert deployment["metadata"]["labels"]["app.kubernetes.io/version"] == "git-aaaaaaaaaaaa"


@pytest.mark.integration
def test_document_disposable_deployment() -> None:
    assert os.environ.get("IMAGE_REF")
    assert os.environ.get("QUALIFICATION_SOURCE_REVISION")
    assert disposable_deployment.main() == 0
