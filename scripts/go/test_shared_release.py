"""E15 fake-provider evidence; these tests never publish remotely."""

from __future__ import annotations

import io
import json
import time
from pathlib import Path

import pytest
import yaml

import shared_release as release
from shared_support import MODULE, REPOSITORY, WORKFLOW, SharedError

SHA = "a" * 40
SUM = "h1:" + "A" * 43 + "="


class Provider:
    def __init__(self):
        self.sha = None
        self.creates = 0

    def tag_sha(self, _tag):
        return self.sha

    def api(self, path, method="GET", body=None, absent=False):
        if path == "git/matching-refs/tags/libs/servicekit/":
            return []
        if method == "POST":
            assert path == "git/refs"
            assert self.sha is None
            self.sha = body["sha"]
            self.creates += 1
            return None
        raise AssertionError(path)


def test_release_source_requires_main_push_at_exact_sha(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given: the canonical repository, main ref and triggering push SHA.
    monkeypatch.setenv("GITHUB_REPOSITORY", REPOSITORY)
    monkeypatch.setenv("GITHUB_REF", "refs/heads/main")
    monkeypatch.setenv("GITHUB_EVENT_NAME", "push")
    monkeypatch.setenv("GITHUB_SHA", SHA)
    monkeypatch.setenv("RELEASE_SHA", SHA)

    # when / then: a main push passes; other triggers and sources do not.
    assert release.release_source() == SHA
    monkeypatch.setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
    with pytest.raises(SharedError, match="main push"):
        release.release_source()
    monkeypatch.setenv("GITHUB_EVENT_NAME", "push")
    monkeypatch.setenv("GITHUB_REF", "refs/heads/release")
    with pytest.raises(SharedError, match="main push"):
        release.release_source()
    monkeypatch.setenv("GITHUB_REF", "refs/heads/main")
    monkeypatch.setenv("GITHUB_REPOSITORY", "example/other")
    with pytest.raises(SharedError, match="main push"):
        release.release_source()
    monkeypatch.setenv("GITHUB_REPOSITORY", REPOSITORY)
    monkeypatch.setenv("RELEASE_SHA", "b" * 40)
    with pytest.raises(SharedError, match="push revision"):
        release.release_source()


def test_tag_create_and_same_sha_resume_never_move_tag() -> None:
    # given
    provider = Provider()
    # when
    release.publish_tag(provider, "v0.1.0", SHA)
    release.publish_tag(provider, "v0.1.0", SHA)
    # then
    assert provider.creates == 1
    with pytest.raises(SharedError, match="collision"):
        release.publish_tag(provider, "v0.1.0", "c" * 40)
    assert provider.sha == SHA


@pytest.mark.parametrize(
    "version", ["v1.0.0", "v0.01.0", "v0.1.0-rc1", "0.1.0", "v0.0.0", "v0.1.0/evil"]
)
def test_release_version_rejects_unapproved_forms(version: str) -> None:
    with pytest.raises(SharedError):
        release.release_identity(version, SHA)


@pytest.mark.parametrize("defect", [None, "checksum", "source", "container", "timeout"])
def test_public_verification_bounds_failure_and_cleans_owned_image(
    tmp_path: Path, monkeypatch, defect: str | None
) -> None:
    # given
    commands = []
    requests = []

    class Commands:
        def __init__(self, seconds=900):
            self.deadline = time.monotonic() + (0 if defect == "timeout" else seconds)

        def run(self, arguments, *_args, **_kwargs):
            commands.append(arguments)
            if arguments[:3] == ["go", "mod", "download"]:
                if defect == "checksum":
                    raise SharedError("checksum mismatch")
                return json.dumps(
                    {
                        "Path": MODULE,
                        "Version": "v0.1.0",
                        "Dir": str(tmp_path),
                        "Sum": SUM,
                        "GoModSum": SUM,
                    }
                )
            if arguments[:2] == ["go", "list"]:
                return json.dumps({"Version": "v0.1.0"})
            if arguments[:2] == ["docker", "build"] and defect == "container":
                raise SharedError("container failure")
            return ""

    def open_proxy(url, timeout):
        requests.append((url, timeout))
        return io.BytesIO(b'{"Version":"v0.1.0"}')

    monkeypatch.setattr(release, "Budget", Commands)
    monkeypatch.setattr(release.urllib.request, "urlopen", open_proxy)
    monkeypatch.setattr(release, "manifest_digest", lambda _path: "d" * 64)
    # when / then
    if defect:
        with pytest.raises(SharedError):
            release.public_verify(
                tmp_path, "v0.1.0", SHA, ("e" if defect == "source" else "d") * 64
            )
    else:
        assert release.public_verify(tmp_path, "v0.1.0", SHA, "d" * 64) is None
        assert any(command[:3] == ["go", "mod", "download"] for command in commands)
        assert any(command[:2] == ["docker", "build"] for command in commands)
    assert len(requests) == (0 if defect == "timeout" else 1)
    assert all(timeout <= 30 for _, timeout in requests)
    if defect in {None, "container"}:
        assert commands[-1][:4] == ["docker", "image", "rm", "-f"]
    else:
        assert not any(command[0] == "docker" for command in commands)


def test_library_release_uses_service_release_action_after_public_verification() -> None:
    # given: the service release publishes with a non-draft, Latest action.
    root = Path(__file__).resolve().parents[2]
    service = yaml.safe_load((root / ".github/workflows/go-release-svc.yml").read_text())
    service_action = service["jobs"]["release"]["steps"][-1]
    library = yaml.safe_load((root / WORKFLOW).read_text())
    assert list(library["jobs"]) == ["release"]
    steps = library["jobs"]["release"]["steps"]

    # when: the library workflow publishes after checking public module resolution.
    action = steps[-1]

    # then: release creation uses the same action and settings as the service workflow.
    assert steps[-2]["run"] == "make go-library-release-verify"
    assert action["uses"] == service_action["uses"] == "ncipollo/release-action@v1"
    for setting in ("allowUpdates", "draft", "makeLatest", "name", "tag", "bodyFile"):
        assert action["with"][setting] == service_action["with"][setting]
    assert "artifacts" not in action["with"]
    assert not any(step.get("uses", "").startswith("actions/attest@") for step in steps)
    assert action["with"]["tag"] == action["with"]["name"]
