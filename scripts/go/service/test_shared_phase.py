"""Tests for shared Servicekit candidate-phase orchestration."""

from __future__ import annotations

import os
from pathlib import Path

import pytest

from scripts.go.service import shared_phase
from scripts.go.service.tooling_config import load_tooling_config

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
CONFIG_PATH = REPOSITORY_ROOT / "services/policy-search-service/go-service-tooling.json"


def test_candidate_files_follow_configured_policy_search_boundaries() -> None:
    # given
    config = load_tooling_config(CONFIG_PATH)

    # when
    dockerfile = shared_phase.candidate_dockerfile(config)
    dockerignore = shared_phase.candidate_dockerignore(config)

    # then
    assert "COPY servicekit /servicekit" in dockerfile
    assert 'LABEL insurance-hub.servicekit.candidate="${SERVICEKIT_CANDIDATE_SHA}"' in dockerfile
    assert "!cmd/**" in dockerignore
    assert "!gen/**" in dockerignore
    assert "!internal/**" in dockerignore
    assert "internal/testing/" in dockerignore


def test_candidate_source_identity_rejects_missing_value() -> None:
    # when / then
    with pytest.raises(ValueError, match="SOURCE_CREATED"):
        shared_phase.source_build_arguments({})


def test_run_phase_uses_configured_target_and_returns_owner_receipt(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    config = load_tooling_config(CONFIG_PATH)
    requests: list[tuple[str, str]] = []
    commands: list[list[str]] = []
    options: list[dict[str, object]] = []

    def fake_context_request(operation: str, phase: str) -> dict[str, object]:
        requests.append((operation, phase))
        return {"environment": {}}

    def fake_run(arguments: list[str], **kwargs: object) -> str:
        commands.append(arguments)
        options.append(kwargs)
        return ""

    monkeypatch.setattr(shared_phase, "context_request", fake_context_request)
    monkeypatch.setattr(shared_phase, "run", fake_run)

    # when
    shared_phase.run_phase(config, "generated")

    # then
    assert requests == [("begin", "generated"), ("end", "generated")]
    assert commands == [
        ["make", "--no-print-directory", "check-candidate-servicekit"],
        ["make", "--no-print-directory", "check-proto-drift"],
    ]
    assert options[1]["reveal_failure_output"] is True


def test_candidate_image_uses_shared_build_identity_and_owned_check(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # given
    config = load_tooling_config(CONFIG_PATH)
    candidate = REPOSITORY_ROOT / "libs/servicekit"
    alternate = tmp_path / "candidate.mod"
    alternate.write_text(
        "module fixture\n\nreplace github.com/igor-baiborodine/insurance-hub/libs/servicekit"
        f" => {candidate}\n",
        encoding="utf-8",
    )
    alternate.with_suffix(".sum").write_text("", encoding="utf-8")
    head = "b" * 40
    environment = {
        "DOCKER": "docker",
        "SOURCE_CREATED": "2026-01-02T03:04:05+00:00",
        "SOURCE_REPOSITORY": "https://example.invalid/insurance-hub",
        "SOURCE_REVISION": "a" * 40,
        "SOURCE_VERSION": "git-aaaaaaaaaaaa",
    }
    commands: list[list[str]] = []

    def fake_run(arguments: list[str], **_kwargs: object) -> str:
        commands.append(arguments)
        if arguments[:4] == ["docker", "image", "inspect", "--format"]:
            return head + "\n"
        return ""

    monkeypatch.setattr(shared_phase, "run", fake_run)

    # when
    shared_phase.candidate_image(
        config,
        {"head": head, "alternate": str(alternate)},
        environment,
    )

    # then
    build = next(command for command in commands if command[:3] == ["docker", "buildx", "build"])
    assert f"SERVICEKIT_CANDIDATE_SHA={head}" in build
    assert f"SOURCE_REVISION={environment['SOURCE_REVISION']}" in build
    assert any(
        command[:3] == ["make", "--no-print-directory", "_image-check-existing"]
        for command in commands
    )
    assert commands[-1][:4] == ["docker", "image", "rm", "--force"]
    assert str(os.getpid()) in commands[-1][-1]


def test_candidate_image_removes_owned_image_after_validation_failure(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # given
    config = load_tooling_config(CONFIG_PATH)
    candidate = REPOSITORY_ROOT / "libs/servicekit"
    alternate = tmp_path / "candidate.mod"
    alternate.write_text(
        "module fixture\n\nreplace github.com/igor-baiborodine/insurance-hub/libs/servicekit"
        f" => {candidate}\n",
        encoding="utf-8",
    )
    alternate.with_suffix(".sum").write_text("", encoding="utf-8")
    head = "b" * 40
    commands: list[list[str]] = []

    def fake_run(arguments: list[str], **_kwargs: object) -> str:
        commands.append(arguments)
        if arguments[:4] == ["docker", "image", "inspect", "--format"]:
            return head + "\n"
        if arguments[:3] == ["make", "--no-print-directory", "_image-check-existing"]:
            raise RuntimeError("fixture validation failure")
        return ""

    monkeypatch.setattr(shared_phase, "run", fake_run)

    # when / then
    with pytest.raises(RuntimeError, match="fixture validation failure"):
        shared_phase.candidate_image(
            config,
            {"head": head, "alternate": str(alternate)},
            {
                "DOCKER": "docker",
                "SOURCE_CREATED": "2026-01-02T03:04:05+00:00",
                "SOURCE_REPOSITORY": "https://example.invalid/insurance-hub",
                "SOURCE_REVISION": "a" * 40,
                "SOURCE_VERSION": "git-aaaaaaaaaaaa",
            },
        )
    assert commands[-1][:4] == ["docker", "image", "rm", "--force"]
