"""Focused checks for the owned Document disposable deployment."""

from __future__ import annotations

import os

import disposable_deployment
import document_deployment_checks as checks
import pytest


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
