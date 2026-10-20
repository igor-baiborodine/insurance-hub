"""Tests for exact Git branch/upstream synchronization checks."""

from __future__ import annotations

import subprocess
from pathlib import Path

import pytest

import check_upstream_sync


def git(repository: Path, *arguments: str) -> str:
    return subprocess.run(
        ["git", "-C", str(repository), *arguments],
        check=True,
        text=True,
        capture_output=True,
        timeout=30,
    ).stdout.strip()


@pytest.fixture
def repositories(tmp_path: Path) -> tuple[Path, Path]:
    # given
    remote = tmp_path / "remote.git"
    local = tmp_path / "local"
    git(tmp_path, "init", "--quiet", "--bare", str(remote))
    git(tmp_path, "init", "--quiet", "--initial-branch=main", str(local))
    git(local, "config", "user.name", "Branch Sync Test")
    git(local, "config", "user.email", "branch-sync@example.invalid")
    (local / "tracked.txt").write_text("initial\n", encoding="utf-8")
    git(local, "add", "tracked.txt")
    git(local, "commit", "--quiet", "-m", "initial")
    git(local, "remote", "add", "origin", str(remote))
    git(local, "push", "--quiet", "--set-upstream", "origin", "main")
    git(remote, "symbolic-ref", "HEAD", "refs/heads/main")
    return local, remote


def test_synchronized_branch_returns_one_line(repositories: tuple[Path, Path]) -> None:
    # given
    local, _ = repositories

    # when
    result = check_upstream_sync.check_sync(local)

    # then
    assert result.startswith("branch-sync: branch='main' upstream='origin/main' commit='")
    assert result.endswith("ahead=0 behind=0")
    assert "\n" not in result


def test_detached_head_is_rejected(repositories: tuple[Path, Path]) -> None:
    # given
    local, _ = repositories
    git(local, "checkout", "--quiet", "--detach")

    # when / then
    with pytest.raises(check_upstream_sync.BranchSyncError, match="detached HEAD"):
        check_upstream_sync.check_sync(local)


def test_branch_without_upstream_is_rejected(repositories: tuple[Path, Path]) -> None:
    # given
    local, _ = repositories
    git(local, "checkout", "--quiet", "-b", "local-only")

    # when / then
    with pytest.raises(check_upstream_sync.BranchSyncError, match="no remote upstream"):
        check_upstream_sync.check_sync(local)


def test_fetch_failure_is_rejected(repositories: tuple[Path, Path]) -> None:
    # given
    local, remote = repositories
    remote.rename(remote.with_name("unavailable.git"))

    # when / then
    with pytest.raises(check_upstream_sync.BranchSyncError, match="could not fetch upstream"):
        check_upstream_sync.check_sync(local)


def test_diverged_branch_reports_ahead_and_behind(repositories: tuple[Path, Path]) -> None:
    # given
    local, remote = repositories
    other = local.parent / "other"
    git(local.parent, "clone", "--quiet", str(remote), str(other))
    git(other, "config", "user.name", "Branch Sync Test")
    git(other, "config", "user.email", "branch-sync@example.invalid")
    (other / "remote.txt").write_text("remote\n", encoding="utf-8")
    git(other, "add", "remote.txt")
    git(other, "commit", "--quiet", "-m", "remote")
    git(other, "push", "--quiet", "origin", "main")
    (local / "local.txt").write_text("local\n", encoding="utf-8")
    git(local, "add", "local.txt")
    git(local, "commit", "--quiet", "-m", "local")

    # when / then
    with pytest.raises(check_upstream_sync.BranchSyncError, match=r"ahead=1 behind=1"):
        check_upstream_sync.check_sync(local)
