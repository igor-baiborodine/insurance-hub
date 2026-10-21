"""Likely legacy Docker build cases."""

from __future__ import annotations

from pathlib import Path

import pytest

import build


@pytest.mark.parametrize(
    ("service", "folder", "image"),
    [
        ("auth", "legacy/auth-service", "insurance-hub-auth-api-legacy:latest"),
        ("document", "legacy/documents-service", "insurance-hub-document-api-legacy:latest"),
        (
            "agent-portal-gateway",
            "legacy/agent-portal-gateway",
            "insurance-hub-agent-portal-gateway-legacy:latest",
        ),
    ],
)
def test_service_mapping(service: str, folder: str, image: str) -> None:
    # when
    result = build.image_for(service)
    # then
    assert result == (folder, image)


def test_all_builds_stop_at_first_failure_without_pruning(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    called: list[str] = []

    def fake_build(folder: str, image: str, *, timeout_seconds: float) -> None:
        assert timeout_seconds > 0
        called.append(folder)
        if folder == "legacy/auth-service":
            raise RuntimeError("build failed")

    monkeypatch.setattr(build, "build", fake_build)
    # when
    status = build.main(["all"])
    # then
    assert status == 1
    assert called == ["legacy/agent-portal-gateway", "legacy/auth-service"]


def test_missing_dockerfile_rejected_before_docker(tmp_path: Path) -> None:
    # when / then
    with pytest.raises(ValueError, match="Dockerfile is missing"):
        build.build("legacy/auth-service", "image:latest", root=tmp_path)
