"""Policy Search deployment guards; unit cases never contact a cluster."""

from __future__ import annotations

import os
import signal
from copy import deepcopy
from unittest.mock import Mock, patch

import policy_search_deployment_checks as checks
import pytest
from scripts.go.service import deployment as lifecycle

import deployment


def target_environment(name: str = "local-dev") -> dict[str, str]:
    if name == "local-dev":
        return {
            "DEPLOY_ENV": name,
            "DEPLOY_CONTEXT": "kind-local-dev-insurance-hub",
            "DEPLOY_SVC_NS": "local-dev-all",
            "DEPLOY_DATA_NS": "local-dev-all",
        }
    return {
        "DEPLOY_ENV": name,
        "DEPLOY_CONTEXT": "qa-insurance-hub",
        "DEPLOY_SVC_NS": "qa-svc",
        "DEPLOY_DATA_NS": "qa-svc",
    }


def rendered_items() -> list[dict]:
    labels = {"app.kubernetes.io/name": "policy-search-api"}
    return [
        {
            "apiVersion": "v1",
            "kind": "ConfigMap",
            "metadata": {"name": "local-dev-policy-search-config"},
        },
        {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": {"name": "local-dev-policy-search-api"},
            "spec": {
                "template": {
                    "metadata": {"labels": labels},
                    "spec": {
                        "containers": [
                            {
                                "name": "policy-search-service",
                                "image": "insurance-hub-policy-search-api:git-123456789abc",
                            }
                        ]
                    },
                }
            },
        },
        {
            "apiVersion": "v1",
            "kind": "Service",
            "metadata": {"name": "local-dev-policy-search-api"},
        },
        {
            "apiVersion": "networking.k8s.io/v1",
            "kind": "NetworkPolicy",
            "metadata": {"name": "local-dev-policy-search-private"},
        },
    ]


def test_target_rejects_wrong_coordinates_before_cluster_access() -> None:
    for name, value in (("DEPLOY_ENV", "unknown"), ("DEPLOY_CONTEXT", "qa-insurance-hub")):
        environment = target_environment()
        environment[name] = value
        with (
            patch.dict(os.environ, environment, clear=True),
            patch.object(checks, "run") as command,
        ):
            with pytest.raises(checks.DeploymentError):
                checks.Target()
            command.assert_not_called()


def test_qa_mutation_is_rejected_before_coordinates() -> None:
    with patch.dict(os.environ, target_environment("qa"), clear=True):
        fake = Mock()
        fake.configuration = {"mode": "flux"}
        with patch.object(checks, "Target", return_value=fake):
            for operation in ("image-load", "deploy", "stop"):
                with pytest.raises(lifecycle.DeploymentError, match="forbidden"):
                    lifecycle.execute(checks, [operation])
        fake.coordinates.assert_not_called()


def test_render_rejects_java_ownership_and_business_dependencies() -> None:
    items = rendered_items()
    checks.validate_render(
        "local-dev", "\n---\n".join(checks.yaml.safe_dump(item) for item in items)
    )
    java = deepcopy(items)
    java[0]["metadata"]["name"] = "local-dev-policy-search-api-legacy"
    with pytest.raises(checks.DeploymentError, match="Java"):
        checks.validate_render(
            "local-dev", "\n---\n".join(checks.yaml.safe_dump(item) for item in java)
        )
    dependency = deepcopy(items)
    dependency[0]["data"] = {"ELASTICSEARCH_URL": "http://synthetic"}
    with pytest.raises(checks.DeploymentError, match="dependency"):
        checks.validate_render(
            "local-dev", "\n---\n".join(checks.yaml.safe_dump(item) for item in dependency)
        )


def test_image_source_guard_rejects_mismatched_revision() -> None:
    target = Mock()
    with (
        patch.dict(os.environ, {"DOCKER": "docker"}, clear=True),
        patch.object(
            checks,
            "run",
            return_value='[{"Config":{"Labels":{"org.opencontainers.image.revision":"ffffffffffffffffffffffffffffffffffffffff"}}}]',
        ),
    ):
        with pytest.raises(checks.DeploymentError, match="source revision"):
            checks.secret_and_image(target, "insurance-hub-policy-search-api:git-123456789abc")


def test_cli_preserves_interrupt_status() -> None:
    for requested, expected in ((signal.SIGINT, 130), (signal.SIGTERM, 143)):

        def interrupted(_argv=None, selected=requested):
            os.kill(os.getpid(), selected)
            return 0

        with patch.object(deployment, "main", interrupted):
            assert deployment.cli([]) == expected
