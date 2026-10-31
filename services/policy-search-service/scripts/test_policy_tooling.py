"""Fast controlled-failure tests for Policy Search tooling adapters."""

from __future__ import annotations

import os
from pathlib import Path

import integration_test
import pytest

from scripts.go.service import image as service_image
from scripts.go.service import shared_phase
from scripts.go.service.tooling_config import load_tooling_config

MODULE_ROOT = Path(__file__).resolve().parent.parent
TOOLING_CONFIG = MODULE_ROOT / "go-service-tooling.json"


def test_policy_search_config_owns_expected_image_and_candidate_targets() -> None:
    # when
    config = load_tooling_config(TOOLING_CONFIG)

    # then
    assert config.module_root == MODULE_ROOT
    assert config.image.name == "insurance-hub-policy-search-api"
    assert config.image.entrypoint == ("/policy-search-service",)
    assert config.candidate.phase_targets["integration"] == ("test-integration",)


def test_integration_rejects_ambient_elasticsearch_before_start(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    monkeypatch.setenv("DOCKER", "docker")
    monkeypatch.setenv("GO", "go")
    monkeypatch.setenv("ELASTICSEARCH_IMAGE", "example.invalid/elasticsearch@sha256:fixture")
    monkeypatch.setenv("INTEGRATION_SUITE", "foundation")
    monkeypatch.setenv("INTEGRATION_COVERAGE_FILE", "integration-coverage.txt")
    monkeypatch.setenv("POLICY_SEARCH_TEST_ELASTICSEARCH_URL", "http://shared.invalid:9200")
    commands: list[list[str]] = []

    def fake_run(arguments: list[str], **_kwargs: object) -> str:
        commands.append(arguments)
        return ""

    monkeypatch.setattr(integration_test, "run", fake_run)

    # when
    status = integration_test.main()

    # then
    assert status == 1
    assert "runner owns Elasticsearch" in capsys.readouterr().err
    assert commands == [["docker", "rm", "--force", f"policy-search-integration-{os.getpid()}"]]


def test_shared_candidate_integration_uses_owned_target(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    requests: list[tuple[str, str]] = []
    commands: list[list[str]] = []

    def fake_context_request(operation: str, phase: str) -> dict[str, object]:
        requests.append((operation, phase))
        return {"environment": {}}

    def fake_run(arguments: list[str], **_kwargs: object) -> str:
        commands.append(arguments)
        return ""

    monkeypatch.setattr(shared_phase, "context_request", fake_context_request)
    monkeypatch.setattr(shared_phase, "run", fake_run)

    # when
    status = shared_phase.main(["--config", str(TOOLING_CONFIG), "integration"])

    # then
    assert status == 0
    assert requests == [("begin", "integration"), ("end", "integration")]
    assert commands == [
        ["make", "--no-print-directory", "check-candidate-servicekit"],
        ["make", "--no-print-directory", "test-integration"],
    ]


def test_image_build_rejects_missing_identity_before_command(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    for name in service_image.REQUIRED_BUILD:
        monkeypatch.delenv(name, raising=False)
    monkeypatch.setattr(
        service_image,
        "run",
        lambda *_args, **_kwargs: pytest.fail("missing identity attempted an image build"),
    )

    # when
    status = service_image.main(["--config", str(TOOLING_CONFIG), "build"])

    # then
    assert status == 1
    assert "missing required environment" in capsys.readouterr().err
