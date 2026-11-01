"""Tests for shared Go-service image build and identity checks."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from scripts.go.service import image
from scripts.go.service.tooling_config import load_tooling_config

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
CONFIG_PATH = REPOSITORY_ROOT / "services/policy-search-service/go-service-tooling.json"


def source_environment() -> dict[str, str]:
    revision = "a" * 40
    return {
        "DOCKER": "docker",
        "IMAGE_NAME": "insurance-hub-policy-search-api",
        "IMAGE_TAG": "1.0.0-local.aaaaaaaaaaaa",
        "IMAGE_REF": "insurance-hub-policy-search-api:1.0.0-local.aaaaaaaaaaaa",
        "SOURCE_CREATED": "2026-01-02T03:04:05+00:00",
        "SOURCE_REPOSITORY": "https://example.invalid/insurance-hub",
        "SOURCE_REVISION": revision,
        "SOURCE_VERSION": "git-aaaaaaaaaaaa",
        "TARGET_PLATFORM": "linux/amd64",
    }


def test_build_image_uses_validated_identity(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    config = load_tooling_config(CONFIG_PATH)
    environment = source_environment()
    commands: list[list[str]] = []

    def fake_run(arguments: list[str], **_kwargs: object) -> str:
        commands.append(arguments)
        if arguments[:3] == ["git", "rev-parse", "--verify"]:
            return environment["SOURCE_REVISION"] + "\n"
        if arguments[:3] == ["git", "show", "-s"]:
            return environment["SOURCE_CREATED"] + "\n"
        if arguments[:4] == ["docker", "image", "inspect", "--format"]:
            return "sha256:fixture\n"
        return ""

    monkeypatch.setattr(image, "run", fake_run)

    # when
    image.build_image(config, environment)

    # then
    build = next(command for command in commands if command[:3] == ["docker", "buildx", "build"])
    assert f"SOURCE_REVISION={environment['SOURCE_REVISION']}" in build
    assert build[-1] == str(config.module_root)


def test_build_image_rejects_wrong_local_tag_before_docker(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    config = load_tooling_config(CONFIG_PATH)
    environment = source_environment()
    environment["IMAGE_TAG"] = "wrong"
    environment["IMAGE_REF"] = f"{environment['IMAGE_NAME']}:wrong"
    commands: list[list[str]] = []

    def fake_run(arguments: list[str], **_kwargs: object) -> str:
        commands.append(arguments)
        if arguments[:3] == ["git", "rev-parse", "--verify"]:
            return environment["SOURCE_REVISION"] + "\n"
        if arguments[:3] == ["git", "show", "-s"]:
            return environment["SOURCE_CREATED"] + "\n"
        return ""

    monkeypatch.setattr(image, "run", fake_run)

    # when / then
    with pytest.raises(image.ImageError, match="IMAGE_TAG"):
        image.build_image(config, environment)
    assert not any(command[:3] == ["docker", "buildx", "build"] for command in commands)


def test_inspect_image_contract_rejects_wrong_entrypoint(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    config = load_tooling_config(CONFIG_PATH)
    environment = source_environment()
    record = {
        "Os": "linux",
        "Architecture": "amd64",
        "Config": {
            "User": "65532:65532",
            "Entrypoint": ["/wrong"],
            "Cmd": None,
            "Labels": {
                "org.opencontainers.image.created": environment["SOURCE_CREATED"],
                "org.opencontainers.image.source": environment["SOURCE_REPOSITORY"],
                "org.opencontainers.image.revision": environment["SOURCE_REVISION"],
                "org.opencontainers.image.title": config.image.title,
                "org.opencontainers.image.version": environment["SOURCE_VERSION"],
            },
        },
    }
    monkeypatch.setattr(image, "run", lambda *_args, **_kwargs: json.dumps([record]))

    # when / then
    with pytest.raises(image.ImageError, match="entrypoint"):
        image.inspect_image_contract(config, environment)
