#!/usr/bin/env python3
"""Select the single Go module changed by a pull request."""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from pathlib import Path, PurePosixPath

SHA = re.compile(r"[0-9a-f]{40}")


class SelectionError(RuntimeError):
    """The pull-request diff cannot be mapped to one supported Go module."""


def classify_module(path_value: str) -> str | None:
    path = PurePosixPath(path_value)
    if len(path.parts) >= 2 and path.parts[:2] == ("templates", "go-service"):
        return "templates/go-service"
    if len(path.parts) >= 3 and path.parts[0] == "services" and path.parts[1].endswith("-service"):
        return f"services/{path.parts[1]}"
    return None


def selection_outputs(paths: list[str], merge_base: str) -> dict[str, str]:
    modules = sorted({module for path in paths if (module := classify_module(path))})
    if not modules:
        return {"module_dir": ""}
    if len(modules) > 1:
        raise SelectionError(
            f"Multiple Go modules changed; split the pull request: {' '.join(modules)}"
        )
    module_dir = modules[0]
    module_role = "service-scaffold" if module_dir == "templates/go-service" else "business-service"
    coverage_file = f"{module_dir}/coverage.txt"
    coverage_files = coverage_file
    if module_role == "business-service":
        coverage_files += f",{module_dir}/integration-coverage.txt"
    return {
        "module_dir": module_dir,
        "module_key": module_dir.replace("/", "-"),
        "module_role": module_role,
        "go_sum": f"{module_dir}/go.sum",
        "validation_target": "check-go-pr",
        "format_base": merge_base,
        "coverage_file": coverage_file,
        "coverage_files": coverage_files,
        "coverage_flag": module_dir.rsplit("/", 1)[-1],
    }


def git_output(root: Path, *arguments: str, binary: bool = False) -> str | bytes:
    result = subprocess.run(
        ["git", *arguments],
        cwd=root,
        check=True,
        capture_output=True,
        text=not binary,
        timeout=60,
        env={"PATH": os.environ.get("PATH", ""), "GIT_CONFIG_NOSYSTEM": "1"},
    )
    return result.stdout


def changed_paths(root: Path, base: str, head: str) -> tuple[str, list[str]]:
    if not SHA.fullmatch(base) or not SHA.fullmatch(head):
        raise SelectionError("base and head must be full Git SHAs")
    for revision in (base, head):
        git_output(root, "cat-file", "-e", f"{revision}^{{commit}}")
    merge_base = str(git_output(root, "merge-base", base, head)).strip()
    output = git_output(
        root,
        "diff",
        "--name-only",
        "--diff-filter=ACDMRT",
        "-z",
        merge_base,
        head,
        binary=True,
    )
    assert isinstance(output, bytes)
    return merge_base, [part.decode("utf-8") for part in output.split(b"\0") if part]


def write_outputs(path: Path, outputs: dict[str, str]) -> None:
    with path.open("a", encoding="utf-8") as stream:
        for name, value in outputs.items():
            stream.write(f"{name}={value}\n")


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser()
    result.add_argument("--base", required=True)
    result.add_argument("--head", required=True)
    result.add_argument("--output", required=True, type=Path)
    return result


def main(argv: list[str] | None = None) -> int:
    arguments = parser().parse_args(argv)
    merge_base, paths = changed_paths(Path.cwd(), arguments.base, arguments.head)
    outputs = selection_outputs(paths, merge_base)
    write_outputs(arguments.output, outputs)
    if not outputs["module_dir"]:
        print(
            "No supported Go module is present in the complete pull request diff; validation is a no-op."
        )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, subprocess.SubprocessError, SelectionError) as error:
        print(f"go-pr-module-select: {error}", file=sys.stderr)
        raise SystemExit(1) from None
