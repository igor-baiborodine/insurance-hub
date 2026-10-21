"""Likely legacy Docker build cases."""

from __future__ import annotations

import os
import sys
import time
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


def test_timeout_kills_descendant_after_docker_leader_exits(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    folder = tmp_path / "legacy/auth-service"
    folder.mkdir(parents=True)
    (folder / "Dockerfile").touch()
    descendant_pid = tmp_path / "descendant.pid"
    descendant = (
        "import os, pathlib, signal, time; "
        "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
        f"pathlib.Path({str(descendant_pid)!r}).write_text(str(os.getpid())); "
        "time.sleep(60)"
    )
    fake_docker = tmp_path / "docker"
    fake_docker.write_text(
        f"#!{sys.executable}\n"
        "import pathlib, subprocess, sys, time\n"
        f"ready = pathlib.Path({str(descendant_pid)!r})\n"
        f"subprocess.Popen([sys.executable, '-c', {descendant!r}])\n"
        "deadline = time.monotonic() + 5\n"
        "while not ready.is_file() and time.monotonic() < deadline:\n"
        "    time.sleep(0.01)\n"
        "time.sleep(60)\n",
        encoding="utf-8",
    )
    fake_docker.chmod(0o700)
    monkeypatch.setenv("PATH", f"{tmp_path}{os.pathsep}{os.environ.get('PATH', '')}")

    # when / then
    with pytest.raises(RuntimeError, match="timed out"):
        build.build(
            "legacy/auth-service",
            "review:temporary",
            root=tmp_path,
            timeout_seconds=0.2,
            teardown_seconds=1,
        )
    pid = int(descendant_pid.read_text(encoding="utf-8"))
    deadline = time.monotonic() + 1
    while time.monotonic() < deadline:
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            break
        time.sleep(0.02)
    else:
        raise AssertionError("owned Docker descendant was not reaped")
