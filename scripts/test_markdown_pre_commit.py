"""Disposable Git-index tests for the Markdown pre-commit hook."""

from __future__ import annotations

import os
import shutil
import signal
import subprocess
import sys
import time
from pathlib import Path

import pytest

REPOSITORY_ROOT = Path(__file__).resolve().parent.parent


def run(
    arguments: list[str | Path], *, cwd: Path, env: dict[str, str] | None = None
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [str(value) for value in arguments],
        cwd=cwd,
        check=False,
        text=True,
        capture_output=True,
        timeout=240,
        env=env,
    )


def git(repository: Path, *arguments: str) -> str:
    result = run(["git", *arguments], cwd=repository)
    assert result.returncode == 0, result.stderr
    return result.stdout.strip()


def hook(repository: Path, *, path: str | None = None) -> subprocess.CompletedProcess[str]:
    environment = {**os.environ, "PYTHON": sys.executable}
    if path is not None:
        environment["PATH"] = path
    return run([repository / ".githooks" / "pre-commit"], cwd=repository, env=environment)


def assert_no_hook_temporary_files(repository: Path) -> None:
    names = [item.name for item in repository.iterdir()]
    assert not any(name.startswith(".markdown-toc-index.") for name in names)
    assert not any(name.startswith(".markdown-toc-staged-paths.") for name in names)


@pytest.fixture
def fixture_repository(tmp_path: Path) -> tuple[Path, Path]:
    # given
    repository = tmp_path / "repository"
    restricted = tmp_path / "restricted-bin"
    (repository / ".githooks").mkdir(parents=True)
    (repository / "scripts").mkdir()
    restricted.mkdir()
    for source, destination in (
        (REPOSITORY_ROOT / ".githooks" / "pre-commit", repository / ".githooks" / "pre-commit"),
        (REPOSITORY_ROOT / ".githooks" / "pre-push", repository / ".githooks" / "pre-push"),
        (
            REPOSITORY_ROOT / "scripts" / "markdown-toc.sh",
            repository / "scripts" / "markdown-toc.sh",
        ),
        (
            REPOSITORY_ROOT / "scripts" / "markdown_toc.py",
            repository / "scripts" / "markdown_toc.py",
        ),
        (
            REPOSITORY_ROOT / "scripts" / "markdown_pre_commit.py",
            repository / "scripts" / "markdown_pre_commit.py",
        ),
    ):
        shutil.copy2(source, destination)
    git(repository, "init", "--quiet", "--initial-branch=feat/issue-174-hook-test")
    git(repository, "config", "user.name", "Markdown Hook Test")
    git(repository, "config", "user.email", "markdown-hook@example.invalid")
    git(repository, "config", "core.hooksPath", ".githooks")
    (repository / "valid.md").write_text(
        "# Valid document\n\n## Existing section\n", encoding="utf-8"
    )
    result = run(
        [repository / "scripts" / "markdown-toc.sh", "format", "valid.md"],
        cwd=repository,
        env={**os.environ, "PYTHON": sys.executable},
    )
    assert result.returncode == 0, result.stderr
    shutil.copy2(repository / "valid.md", repository / "partial.md")
    (repository / "delete-me.md").write_text(
        "# Removed document\n\n## Missing TOC\n", encoding="utf-8"
    )
    (repository / "notes.txt").write_text("initial text\n", encoding="utf-8")
    (repository / ".markdown-hook-test-fixture").write_text(
        "controlled fixture\n", encoding="utf-8"
    )
    git(repository, "add", ".")
    git(repository, "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "-m", "test fixture")
    for name in ("env", "git", "dirname"):
        executable = shutil.which(name)
        assert executable is not None
        (restricted / name).symlink_to(executable)
    return repository, restricted


def test_non_markdown_selection_does_not_require_npx(
    fixture_repository: tuple[Path, Path],
) -> None:
    # given
    repository, restricted = fixture_repository
    (repository / "notes.txt").write_text("changed text\n", encoding="utf-8")
    git(repository, "add", "notes.txt")

    # when
    result = hook(repository, path=str(restricted))

    # then
    assert result.returncode == 0, result.stderr
    assert_no_hook_temporary_files(repository)


def test_markdown_deletion_does_not_require_npx(
    fixture_repository: tuple[Path, Path],
) -> None:
    # given
    repository, restricted = fixture_repository
    (repository / "delete-me.md").unlink()
    git(repository, "add", "delete-me.md")

    # when
    result = hook(repository, path=str(restricted))

    # then
    assert result.returncode == 0, result.stderr
    assert_no_hook_temporary_files(repository)


def test_valid_new_renamed_nested_and_spaced_paths_pass(
    fixture_repository: tuple[Path, Path],
) -> None:
    # given
    repository, _ = fixture_repository
    git(repository, "mv", "valid.md", "renamed document.md")
    nested = repository / "docs" / "nested path"
    nested.mkdir(parents=True)
    shutil.copy2(repository / "partial.md", nested / "copied document.md")
    shutil.copy2(repository / "partial.md", repository / "new-document.md")
    git(repository, "add", ".")

    # when
    result = hook(repository)

    # then
    assert result.returncode == 0, result.stderr
    assert_no_hook_temporary_files(repository)


def test_missing_and_stale_tocs_fail_without_mutating_content(
    fixture_repository: tuple[Path, Path],
) -> None:
    # given
    repository, _ = fixture_repository
    missing = repository / "missing.md"
    stale = repository / "stale.md"
    missing.write_text("# Missing TOC\n\n## Section\n", encoding="utf-8")
    shutil.copy2(repository / "valid.md", stale)
    with stale.open("a", encoding="utf-8") as stream:
        stream.write("\n## Unlisted section\n")
    git(repository, "add", "missing.md", "stale.md")
    before = {
        name: (git(repository, "rev-parse", f":{name}"), git(repository, "hash-object", name))
        for name in ("missing.md", "stale.md")
    }

    # when
    result = hook(repository)

    # then
    assert result.returncode == 1
    assert "stale or missing table of contents" in result.stderr
    assert "missing.md" in result.stderr and "stale.md" in result.stderr
    assert "then stage the formatted content" in result.stderr
    after = {
        name: (git(repository, "rev-parse", f":{name}"), git(repository, "hash-object", name))
        for name in before
    }
    assert after == before
    assert_no_hook_temporary_files(repository)


def test_valid_staged_content_ignores_stale_worktree_content(
    fixture_repository: tuple[Path, Path],
) -> None:
    # given
    repository, _ = fixture_repository
    partial = repository / "partial.md"
    with partial.open("a", encoding="utf-8") as stream:
        stream.write("\n## Staged section\n")
    formatted = run(
        [repository / "scripts" / "markdown-toc.sh", "format", "partial.md"],
        cwd=repository,
        env={**os.environ, "PYTHON": sys.executable},
    )
    assert formatted.returncode == 0, formatted.stderr
    git(repository, "add", "partial.md")
    with partial.open("a", encoding="utf-8") as stream:
        stream.write("\n## Worktree-only section\n")
    before = (
        git(repository, "rev-parse", ":partial.md"),
        git(repository, "hash-object", "partial.md"),
    )

    # when
    result = hook(repository)

    # then
    assert result.returncode == 0, result.stderr
    after = (
        git(repository, "rev-parse", ":partial.md"),
        git(repository, "hash-object", "partial.md"),
    )
    assert after == before
    assert_no_hook_temporary_files(repository)


def test_stale_staged_content_fails_when_worktree_is_valid(
    fixture_repository: tuple[Path, Path],
) -> None:
    # given
    repository, _ = fixture_repository
    partial = repository / "partial.md"
    with partial.open("a", encoding="utf-8") as stream:
        stream.write("\n## Staged-only section\n")
    git(repository, "add", "partial.md")
    formatted = run(
        [repository / "scripts" / "markdown-toc.sh", "format", "partial.md"],
        cwd=repository,
        env={**os.environ, "PYTHON": sys.executable},
    )
    assert formatted.returncode == 0, formatted.stderr
    before = (
        git(repository, "rev-parse", ":partial.md"),
        git(repository, "hash-object", "partial.md"),
    )

    # when
    result = hook(repository)

    # then
    assert result.returncode == 1
    assert "stale or missing table of contents" in result.stderr
    after = (
        git(repository, "rev-parse", ":partial.md"),
        git(repository, "hash-object", "partial.md"),
    )
    assert after == before
    assert_no_hook_temporary_files(repository)


def test_staged_markdown_reports_missing_npx(fixture_repository: tuple[Path, Path]) -> None:
    # given
    repository, restricted = fixture_repository
    (repository / "requires-checker.md").write_text(
        "# Requires checker\n\n## Section\n", encoding="utf-8"
    )
    git(repository, "add", "requires-checker.md")

    # when
    result = hook(repository, path=str(restricted))

    # then
    assert result.returncode == 1
    assert "npx is required" in result.stderr
    assert_no_hook_temporary_files(repository)


def test_pre_push_hook_coexists(fixture_repository: tuple[Path, Path]) -> None:
    # given
    repository, _ = fixture_repository

    # when
    result = run([repository / ".githooks" / "pre-push"], cwd=repository)

    # then
    assert result.returncode == 0, result.stderr
    assert git(repository, "config", "--get", "core.hooksPath") == ".githooks"


def test_sigterm_cleans_staged_snapshot(fixture_repository: tuple[Path, Path]) -> None:
    # given
    repository, _ = fixture_repository
    (repository / "interrupt.md").write_text("# Interrupt\n\n## Section\n", encoding="utf-8")
    git(repository, "add", "interrupt.md")
    environment = {
        **os.environ,
        "PYTHON": sys.executable,
        "MARKDOWN_HOOK_TEST_PAUSE_AFTER_SNAPSHOT": "1",
    }
    process = subprocess.Popen(
        [str(repository / ".githooks" / "pre-commit")],
        cwd=repository,
        env=environment,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    deadline = time.monotonic() + 5
    while not list(repository.glob(".markdown-toc-index.*")) and time.monotonic() < deadline:
        time.sleep(0.02)
    assert list(repository.glob(".markdown-toc-index.*"))

    # when
    process.send_signal(signal.SIGTERM)
    _, stderr = process.communicate(timeout=10)

    # then
    assert process.returncode == 143, stderr
    assert_no_hook_temporary_files(repository)
