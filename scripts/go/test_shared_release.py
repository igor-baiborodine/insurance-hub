"""E15/E17 fake-provider evidence; these tests never publish remotely."""

from __future__ import annotations

import copy
import io
import json
import subprocess
import time
from pathlib import Path

import pytest
import yaml

import shared_release as release
from shared_support import MODULE, REPOSITORY, WORKFLOW, SharedError, write_json

SHA = "a" * 40
CONTROL = "b" * 40
SUM = "h1:" + "A" * 43 + "="


def record() -> dict:
    result = {"status": "passed", "identity": "d" * 64}
    return {
        "schema": 1,
        "module": MODULE,
        "version": "v0.1.0",
        "tag": "libs/servicekit/v0.1.0",
        "source_sha": SHA,
        "source_manifest": "c" * 64,
        "checksums": {"module": SUM, "go_mod": SUM},
        "producer": {
            "repository": REPOSITORY,
            "workflow": WORKFLOW,
            "workflow_sha": CONTROL,
            "run_id": 42,
            "run_attempt": 1,
        },
        "validation": {k: copy.deepcopy(result) for k in ("library", "consumers")},
        "public_resolution": {
            k: copy.deepcopy(result) for k in ("proxy", "checksums", "standalone", "container")
        },
    }


class Provider:
    def __init__(self):
        self.sha = None
        self.creates = 0
        self.commands = []
        self.immutable = True
        self.attested = True
        self.success = True

    def protections(self):
        if not self.immutable:
            raise SharedError("protection missing")

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
        if path.startswith("releases/"):
            return {"immutable": self.immutable, "draft": False}
        if "/jobs?" in path:
            return {
                "total_count": 2,
                "jobs": [
                    {"name": name, "conclusion": "success" if self.success else "failure"}
                    for name in ("Validate Go library release", "Verify published Go library")
                ],
            }
        if path.startswith("actions/runs/"):
            return {
                "path": WORKFLOW,
                "head_sha": CONTROL,
                "event": "workflow_dispatch",
                "status": "completed",
                "conclusion": "success" if self.success else "failure",
            }
        raise AssertionError(path)

    def command(self, args):
        self.commands.append(args)
        if not self.attested:
            raise SharedError("invalid attestation")
        return ""


def disposable_history(root: Path) -> tuple[str, str]:
    def git(*args: str) -> str:
        result = subprocess.run(
            ["git", *args], cwd=root, check=True, capture_output=True, text=True, timeout=5
        )
        return result.stdout.strip()

    git("init", "-q", "-b", "main")
    git("config", "user.name", "Release Test")
    git("config", "user.email", "release-test@example.invalid")
    (root / "source.txt").write_text("base\n")
    git("add", "source.txt")
    git("commit", "-qm", "base")
    base = git("rev-parse", "HEAD")
    (root / "source.txt").write_text("reviewed\n")
    git("commit", "-qam", "reviewed source")
    main = git("rev-parse", "HEAD")
    git("checkout", "-qb", "unmerged", base)
    (root / "source.txt").write_text("unmerged\n")
    git("commit", "-qam", "unmerged source")
    side = git("rev-parse", "HEAD")
    git("checkout", "-q", "main")
    assert git("rev-parse", "HEAD") == main
    return main, side


class ReviewAPI(release.GitHub):
    def __init__(self, root: Path, sha: str):
        super().__init__(root)
        self.sha = sha
        self.reviews = [{"state": "APPROVED", "commit_id": sha, "user": {"login": "reviewer"}}]
        self.pulls = [
            {
                "merged_at": "2026-01-01T00:00:00Z",
                "merge_commit_sha": sha,
                "base": {"ref": "main", "repo": {"full_name": REPOSITORY}},
                "head": {"sha": sha},
                "user": {"login": "author"},
                "number": 7,
            }
        ]
        self.calls = []

    def api(self, suffix, method="GET", body=None, absent=False):
        self.calls.append((method, suffix))
        if suffix == "git/ref/heads/main":
            return {"object": {"sha": self.sha}}
        if suffix.startswith("commits/") and suffix.endswith("/pulls?per_page=100"):
            return self.pulls
        if suffix == "pulls/7/reviews?per_page=100":
            return self.reviews
        raise AssertionError(suffix)


@pytest.mark.parametrize(
    "defect",
    [None, "unmerged", "no_pull", "unreviewed", "author_only", "stale", "revoked", "changes"],
)
def test_reviewed_source_requires_merged_ancestor_and_current_independent_approval(
    tmp_path: Path, defect: str | None
) -> None:
    # given: main and an unmerged branch are real disposable Git history.
    main, side = disposable_history(tmp_path)
    provider = ReviewAPI(tmp_path, main)
    source = side if defect == "unmerged" else main
    if defect == "unmerged":
        # The PR evidence otherwise qualifies, isolating the real Git ancestry guard.
        provider.pulls[0]["merge_commit_sha"] = side
        provider.pulls[0]["head"]["sha"] = side
        provider.reviews[0]["commit_id"] = side
    elif defect == "no_pull":
        provider.pulls = []
    elif defect == "unreviewed":
        provider.reviews = []
    elif defect == "author_only":
        provider.reviews[0]["user"]["login"] = "author"
    elif defect == "stale":
        provider.reviews[0]["commit_id"] = side
    elif defect == "revoked":
        provider.reviews.append(
            {"state": "DISMISSED", "commit_id": main, "user": {"login": "reviewer"}}
        )
    elif defect == "changes":
        provider.reviews.append(
            {"state": "CHANGES_REQUESTED", "commit_id": main, "user": {"login": "other"}}
        )

    # when / then: every denied path stops before any publication request.
    if defect == "unmerged":
        with pytest.raises(RuntimeError, match="inspect Git failed with status 1"):
            release.reviewed_source(provider, tmp_path, source)
    elif defect:
        with pytest.raises(RuntimeError):
            release.reviewed_source(provider, tmp_path, source)
    else:
        release.reviewed_source(provider, tmp_path, source)
    assert all(method == "GET" for method, _ in provider.calls)


@pytest.mark.parametrize(
    "defect",
    [
        None,
        "missing_immutability",
        "disabled_immutability",
        "missing_environment",
        "denied_environment",
        "missing_reviewers",
        "self_review",
        "admin_bypass",
    ],
)
def test_real_github_protections_rejects_unprotected_publication(
    tmp_path: Path, defect: str | None
) -> None:
    # given: fake API responses feed the real protection guard before publish_tag can create a tag.
    class ProtectionsAPI(release.GitHub):
        def __init__(self):
            super().__init__(tmp_path)
            self.calls = []

        def api(self, suffix, method="GET", body=None, absent=False):
            self.calls.append((method, suffix))
            assert method == "GET"
            if suffix == "immutable-releases":
                if defect == "missing_immutability":
                    raise SharedError("GitHub GET request failed: HTTP 404")
                return {"enabled": defect != "disabled_immutability"}
            if suffix == "environments/servicekit-release":
                if defect in {"missing_environment", "denied_environment"}:
                    code = 403 if defect == "denied_environment" else 404
                    raise SharedError(f"GitHub GET request failed: HTTP {code}")
                return {
                    "protection_rules": [
                        {
                            "type": "required_reviewers",
                            "reviewers": [] if defect == "missing_reviewers" else [{"id": 1}],
                            "prevent_self_review": defect != "self_review",
                        }
                    ],
                    "can_admins_bypass": defect == "admin_bypass",
                }
            if suffix == "git/ref/tags/libs%2Fservicekit%2Fv0.1.0":
                return None
            if suffix == "git/matching-refs/tags/libs/servicekit/":
                return []
            raise AssertionError(suffix)

    provider = ProtectionsAPI()
    # when / then
    if defect:
        with pytest.raises(SharedError):
            release.publish_tag(provider, "v0.1.0", SHA)
        assert all(not path.startswith("git/") for _, path in provider.calls)
    else:
        provider.protections()
        assert provider.calls == [
            ("GET", "immutable-releases"),
            ("GET", "environments/servicekit-release"),
        ]


@pytest.mark.parametrize("defect", [None, "job_ids", "missing", "duplicate", "failed"])
def test_release_job_verification_matches_installed_workflow_names(defect: str | None) -> None:
    # given: GitHub returns the configured display names for the required jobs.
    root = Path(__file__).resolve().parents[2]
    workflow = yaml.safe_load((root / WORKFLOW).read_text())
    jobs = [
        {"name": workflow["jobs"][job_id]["name"], "conclusion": "success"}
        for job_id in ("validate", "verify")
    ]
    if defect == "job_ids":
        jobs = [{"name": name, "conclusion": "success"} for name in ("validate", "verify")]
    elif defect == "missing":
        jobs.pop()
    elif defect == "duplicate":
        jobs.append(copy.deepcopy(jobs[0]))
    elif defect == "failed":
        jobs[1]["conclusion"] = "failure"

    class JobsProvider:
        def api(self, path):
            assert path == "actions/runs/42/attempts/1/jobs?per_page=100"
            return {"total_count": len(jobs), "jobs": jobs}

    # when / then
    if defect:
        with pytest.raises(SharedError, match="required job"):
            release.verify_run_jobs(JobsProvider(), 42, 1)
    else:
        release.verify_run_jobs(JobsProvider(), 42, 1)


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


@pytest.mark.parametrize(
    "defect", ["schema", "module", "sha", "producer", "missing", "failed", "checksum"]
)
def test_qualification_rejects_untrusted_incomplete_records(defect: str) -> None:
    # given
    value = record()
    release.qualification(value, "v0.1.0", SHA)
    if defect == "schema":
        value["schema"] = 2
    elif defect == "module":
        value["module"] = "example.com/other"
    elif defect == "sha":
        value["source_sha"] = "e" * 40
    elif defect == "producer":
        value["producer"]["workflow"] = ".github/workflows/untrusted.yml"
    elif defect == "missing":
        del value["public_resolution"]["container"]
    elif defect == "failed":
        value["public_resolution"]["checksums"]["status"] = "failed"
    else:
        value["checksums"]["module"] = "unknown"
    # when / then
    with pytest.raises(SharedError):
        release.qualification(value, "v0.1.0", SHA)


@pytest.mark.parametrize(
    "defect", [None, "tag", "mutable", "asset", "run", "version", "override", "workflow"]
)
def test_admission_binds_selected_graph_release_and_producer(
    tmp_path: Path, defect: str | None
) -> None:
    # given
    asset = tmp_path / "qualification.json"
    write_json(asset, record())
    provider = Provider()
    provider.sha = SHA
    selected = {"Path": MODULE, "Version": "v0.1.0", "Sum": SUM, "GoModSum": SUM}
    trusted = CONTROL
    if defect == "tag":
        provider.sha = "e" * 40
    elif defect == "mutable":
        provider.immutable = False
    elif defect == "asset":
        provider.attested = False
    elif defect == "run":
        provider.success = False
    elif defect == "version":
        selected["Version"] = "v0.0.1"
    elif defect == "override":
        selected["Replace"] = {"Path": "../library"}
    elif defect == "workflow":
        trusted = "f" * 40
    # when / then
    if defect:
        with pytest.raises(SharedError):
            release.verify_admission(provider, asset, "v0.1.0", SHA, selected, trusted)
    else:
        release.verify_admission(provider, asset, "v0.1.0", SHA, selected, trusted)
        assert any(c[:2] == ["release", "verify-asset"] for c in provider.commands)
        assert any("--signer-digest" in c and CONTROL in c for c in provider.commands)


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
        result = release.public_verify(tmp_path, "v0.1.0", SHA, "d" * 64)
        assert set(result["results"]) == {"proxy", "checksums", "standalone", "container"}
    assert len(requests) == (0 if defect == "timeout" else 1)
    assert all(timeout <= 30 for _, timeout in requests)
    if defect in {None, "container"}:
        assert commands[-1][:4] == ["docker", "image", "rm", "-f"]
    else:
        assert not any(command[0] == "docker" for command in commands)


def test_same_source_qualification_retry_reuses_prior_immutable_asset(tmp_path: Path) -> None:
    # given
    previous = record()
    current = copy.deepcopy(previous)
    current["producer"]["run_id"] = 43
    asset = tmp_path / "qualification.json"
    write_json(asset, current)

    class Published(Provider):
        def api(self, path, method="GET", body=None, absent=False):
            if path.startswith("releases/"):
                return {
                    "immutable": True,
                    "draft": False,
                    "assets": [{"name": "qualification.json"}],
                }
            return super().api(path, method, body, absent)

        def command(self, args):
            if args[:2] == ["release", "download"]:
                write_json(Path(args[args.index("--dir") + 1]) / "qualification.json", previous)
            return super().command(args)

    provider = Published()
    provider.sha = SHA
    # when
    release.publish_qualification(provider, asset, "v0.1.0", SHA)
    # then
    assert not any(
        command[:2] in (["release", "upload"], ["release", "edit"]) for command in provider.commands
    )
