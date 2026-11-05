#!/usr/bin/env python3
"""Fail a Go module pull request when changed executable lines lack coverage."""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from pathlib import Path, PurePosixPath

import yaml

SHA = re.compile(r"[0-9a-f]{40}")
PROFILE_ENTRY = re.compile(r"(.+?):(\d+)\.(\d+),(\d+)\.(\d+)\s+\d+\s+(\d+)$")
HUNK = re.compile(r"^@@ .* \+(\d+)(?:,(\d+))? @@")


class PatchCoverageError(RuntimeError):
    """The patch coverage input or result is invalid."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise PatchCoverageError(message)


def safe_relative(value: str, description: str) -> PurePosixPath:
    path = PurePosixPath(value)
    require(
        bool(value)
        and not path.is_absolute()
        and value == path.as_posix()
        and ".." not in path.parts,
        f"patch-coverage-check: invalid {description}: {value}",
    )
    return path


def git(root: Path, *arguments: str) -> str:
    result = subprocess.run(
        ["git", *arguments],
        cwd=root,
        env={"PATH": os.environ.get("PATH", ""), "GIT_CONFIG_NOSYSTEM": "1"},
        check=True,
        capture_output=True,
        text=True,
        timeout=60,
    )
    return result.stdout


def changed_lines(
    root: Path, base: str, head: str, module_dir: PurePosixPath
) -> set[tuple[str, int]]:
    for revision in (base, head):
        require(SHA.fullmatch(revision), "patch-coverage-check: revisions must be full Git SHAs")
        git(root, "cat-file", "-e", f"{revision}^{{commit}}")
    diff = git(
        root,
        "diff",
        "--unified=0",
        "--no-renames",
        "--diff-filter=ACMRT",
        base,
        head,
        "--",
        module_dir.as_posix(),
    )
    result: set[tuple[str, int]] = set()
    source: str | None = None
    line_number: int | None = None
    for line in diff.splitlines():
        if line.startswith("+++ b/"):
            source = line.removeprefix("+++ b/")
            line_number = None
            continue
        match = HUNK.match(line)
        if match:
            line_number = int(match.group(1))
            continue
        if source is None or line_number is None:
            continue
        if line.startswith("+") and not line.startswith("+++"):
            if source.endswith(".go"):
                result.add((source, line_number))
            line_number += 1
        elif line.startswith("-") and not line.startswith("---"):
            continue
        elif not line.startswith("\\"):
            line_number += 1
    return result


def codecov_exclusions(
    root: Path,
    base: str,
    config_path: PurePosixPath,
    module_dir: PurePosixPath,
) -> tuple[PurePosixPath, ...]:
    """Return simple module subtrees ignored by Codecov at the exact PR base."""
    require(SHA.fullmatch(base), "patch-coverage-check: base must be a full Git SHA")
    git(root, "cat-file", "-e", f"{base}^{{commit}}")
    source = git(root, "show", f"{base}:{config_path.as_posix()}")
    try:
        document = yaml.safe_load(source)
    except yaml.YAMLError as error:
        raise PatchCoverageError(
            f"patch-coverage-check: invalid base Codecov configuration: {error}"
        ) from error
    require(
        isinstance(document, dict),
        "patch-coverage-check: base Codecov configuration must be a mapping",
    )
    ignored = document.get("ignore", [])
    require(
        isinstance(ignored, list) and all(isinstance(item, str) for item in ignored),
        "patch-coverage-check: base Codecov ignore must be a string list",
    )
    prefix = f"{module_dir.as_posix()}/"
    exclusions: list[PurePosixPath] = []
    for pattern in ignored:
        if not pattern.startswith(prefix):
            continue
        relative = pattern.removeprefix(prefix)
        require(
            relative.endswith("/**") and "*" not in relative.removesuffix("/**"),
            f"patch-coverage-check: unsupported module Codecov ignore pattern: {pattern}",
        )
        exclusions.append(safe_relative(relative.removesuffix("/**"), "Codecov exclusion"))
    return tuple(exclusions)


def profile_lines(
    root: Path,
    module_dir: PurePosixPath,
    profile_paths: list[PurePosixPath],
    exclusions: tuple[PurePosixPath, ...],
) -> tuple[set[tuple[str, int]], set[tuple[str, int]]]:
    executable: set[tuple[str, int]] = set()
    covered: set[tuple[str, int]] = set()
    marker = f"/{module_dir.as_posix()}/"
    for profile_path in profile_paths:
        require(
            profile_path.is_relative_to(module_dir),
            f"patch-coverage-check: profile is outside selected module: {profile_path}",
        )
        profile = root / profile_path
        require(profile.is_file(), f"patch-coverage-check: profile not found: {profile_path}")
        lines = profile.read_text(encoding="utf-8").splitlines()
        require(
            lines and lines[0] in {"mode: atomic", "mode: count", "mode: set"},
            f"patch-coverage-check: invalid profile header: {profile_path}",
        )
        for line_number, line in enumerate(lines[1:], start=2):
            match = PROFILE_ENTRY.fullmatch(line)
            require(
                bool(match),
                f"patch-coverage-check: invalid profile entry at {profile_path}:{line_number}",
            )
            assert match is not None
            source, start, _start_column, end, end_column, count = match.groups()
            require(
                marker in source,
                f"patch-coverage-check: profile source is outside selected module: {source}",
            )
            repository_source = module_dir / source.split(marker, 1)[1]
            relative_source = repository_source.relative_to(module_dir)
            if any(
                relative_source == exclusion or relative_source.is_relative_to(exclusion)
                for exclusion in exclusions
            ):
                continue
            # Go profile end coordinates are exclusive. An end column of one
            # therefore belongs to the following line and must not inflate the
            # executable-line denominator.
            stop = int(end) + (1 if int(end_column) > 1 else 0)
            for source_line in range(int(start), stop):
                coordinate = (repository_source.as_posix(), source_line)
                executable.add(coordinate)
                if int(count) > 0:
                    covered.add(coordinate)
    return executable, covered


def evaluate(
    changed: set[tuple[str, int]],
    executable: set[tuple[str, int]],
    covered: set[tuple[str, int]],
    minimum: int,
) -> tuple[int, int]:
    included = changed & executable
    hits = included & covered
    require(
        not included or 100 * len(hits) >= minimum * len(included),
        f"patch-coverage-check: below minimum ({len(hits)}/{len(included)} lines; minimum {minimum}%)",
    )
    return len(hits), len(included)


def csv_paths(value: str, description: str) -> list[PurePosixPath]:
    values = value.split(",") if value else []
    require(values and all(values), f"patch-coverage-check: {description} must not be empty")
    return [safe_relative(item, description) for item in values]


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser()
    result.add_argument("--base", required=True)
    result.add_argument("--head", required=True)
    result.add_argument("--module-dir", required=True)
    result.add_argument("--profiles", required=True)
    result.add_argument("--codecov-config", default="codecov.yml")
    result.add_argument("--minimum", type=int, default=75)
    return result


def main(argv: list[str] | None = None) -> int:
    arguments = parser().parse_args(argv)
    require(0 <= arguments.minimum <= 100, "patch-coverage-check: minimum must be from 0 to 100")
    root = Path.cwd()
    module_dir = safe_relative(arguments.module_dir, "module directory")
    profiles = csv_paths(arguments.profiles, "profiles")
    exclusions = codecov_exclusions(
        root,
        arguments.base,
        safe_relative(arguments.codecov_config, "Codecov configuration"),
        module_dir,
    )
    changed = changed_lines(root, arguments.base, arguments.head, module_dir)
    executable, covered = profile_lines(root, module_dir, profiles, exclusions)
    hits, total = evaluate(changed, executable, covered, arguments.minimum)
    percentage = 100.0 if total == 0 else 100 * hits / total
    print(
        f"patch-coverage-check: {percentage:.2f}% ({hits}/{total} changed executable lines; "
        f"minimum {arguments.minimum}%; profiles {len(profiles)})"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, subprocess.SubprocessError, PatchCoverageError) as error:
        print(error, file=sys.stderr)
        raise SystemExit(1) from None
