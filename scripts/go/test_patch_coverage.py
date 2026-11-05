"""Tests for repository-owned Go patch coverage enforcement."""

from __future__ import annotations

import subprocess
from pathlib import Path, PurePosixPath

import patch_coverage
import pytest


def git(root: Path, *arguments: str) -> str:
    return subprocess.run(
        ["git", *arguments],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    ).stdout.strip()


def repository(tmp_path: Path) -> tuple[str, str]:
    git(tmp_path, "init", "--quiet", "--initial-branch=main")
    git(tmp_path, "config", "user.name", "Patch Coverage Test")
    git(tmp_path, "config", "user.email", "patch-coverage@example.invalid")
    module = tmp_path / "services" / "example-service"
    module.mkdir(parents=True)
    source = module / "main.go"
    source.write_text("package example\n", encoding="utf-8")
    (tmp_path / "codecov.yml").write_text(
        'ignore:\n  - "services/example-service/gen/**"\n', encoding="utf-8"
    )
    git(tmp_path, "add", ".")
    git(tmp_path, "commit", "--quiet", "-m", "base")
    base = git(tmp_path, "rev-parse", "HEAD")
    source.write_text(
        "package example\n\nfunc added(value bool) int {\n"
        "\tif value {\n\t\treturn 1\n\t}\n\treturn 0\n}\n",
        encoding="utf-8",
    )
    git(tmp_path, "add", ".")
    git(tmp_path, "commit", "--quiet", "-m", "head")
    return base, git(tmp_path, "rev-parse", "HEAD")


def write_profile(root: Path, name: str, entries: str) -> PurePosixPath:
    path = root / "services" / "example-service" / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(f"mode: atomic\n{entries}", encoding="utf-8")
    return PurePosixPath("services/example-service") / name


def test_combined_profiles_raise_patch_above_minimum(tmp_path: Path) -> None:
    # given
    base, head = repository(tmp_path)
    module = PurePosixPath("services/example-service")
    first = write_profile(
        tmp_path,
        "coverage.txt",
        "example.invalid/services/example-service/main.go:3.1,5.10 2 1\n"
        "example.invalid/services/example-service/main.go:6.1,8.2 2 0\n",
    )
    second = write_profile(
        tmp_path,
        "integration-coverage.txt",
        "example.invalid/services/example-service/main.go:3.1,5.10 2 0\n"
        "example.invalid/services/example-service/main.go:6.1,8.2 2 1\n",
    )
    changed = patch_coverage.changed_lines(tmp_path, base, head, module)

    # when
    executable, unit_covered = patch_coverage.profile_lines(tmp_path, module, [first], ())
    _, combined_covered = patch_coverage.profile_lines(tmp_path, module, [first, second], ())

    # then
    with pytest.raises(patch_coverage.PatchCoverageError, match="below minimum"):
        patch_coverage.evaluate(changed, executable, unit_covered, 75)
    assert patch_coverage.evaluate(changed, executable, combined_covered, 75) == (6, 6)


def test_profile_exclusions_remove_generated_subtree(tmp_path: Path) -> None:
    # given
    module = tmp_path / "services" / "example-service"
    (module / "gen").mkdir(parents=True)
    profile = write_profile(
        tmp_path,
        "coverage.txt",
        "example.invalid/services/example-service/gen/output.go:1.1,2.2 1 0\n",
    )

    # when
    executable, covered = patch_coverage.profile_lines(
        tmp_path,
        PurePosixPath("services/example-service"),
        [profile],
        (PurePosixPath("gen"),),
    )

    # then
    assert executable == set()
    assert covered == set()


def test_profile_end_at_column_one_does_not_count_following_line(tmp_path: Path) -> None:
    # given
    profile = write_profile(
        tmp_path,
        "coverage.txt",
        "example.invalid/services/example-service/main.go:3.1,5.1 1 1\n",
    )

    # when
    executable, covered = patch_coverage.profile_lines(
        tmp_path,
        PurePosixPath("services/example-service"),
        [profile],
        (),
    )

    # then
    assert (
        executable
        == covered
        == {
            ("services/example-service/main.go", 3),
            ("services/example-service/main.go", 4),
        }
    )


def test_codecov_exclusions_come_from_exact_base_revision(tmp_path: Path) -> None:
    # given
    base, _head = repository(tmp_path)
    (tmp_path / "codecov.yml").write_text(
        'ignore:\n  - "services/example-service/internal/testing/**"\n', encoding="utf-8"
    )

    # when
    exclusions = patch_coverage.codecov_exclusions(
        tmp_path,
        base,
        PurePosixPath("codecov.yml"),
        PurePosixPath("services/example-service"),
    )

    # then
    assert exclusions == (PurePosixPath("gen"),)


def test_reported_codecov_regression_is_below_required_minimum() -> None:
    # given
    changed = {("source.go", line) for line in range(1, 1096)}
    executable = changed.copy()
    covered = {("source.go", line) for line in range(1, 755)}

    # when / then
    with pytest.raises(
        patch_coverage.PatchCoverageError,
        match=r"below minimum \(754/1095 lines; minimum 75%\)",
    ):
        patch_coverage.evaluate(changed, executable, covered, 75)


@pytest.mark.parametrize("value", ["", "/absolute", "../escape", "path/../escape"])
def test_safe_relative_rejects_unsafe_paths(value: str) -> None:
    with pytest.raises(patch_coverage.PatchCoverageError, match="invalid controlled path"):
        patch_coverage.safe_relative(value, "controlled path")
