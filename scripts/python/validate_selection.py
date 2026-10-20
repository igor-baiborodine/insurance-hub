"""Validate repository-relative Python tool selections before mutation or execution."""

from __future__ import annotations

import argparse
import os
import re
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Literal

SelectionKind = Literal["files", "acceptance"]
SAFE_PATH = re.compile(r"[A-Za-z0-9_./-]+")


class SelectionError(ValueError):
    """An unsafe or invalid repository path selection."""


def validate_path(root: Path, value: str, kind: SelectionKind) -> None:
    if not SAFE_PATH.fullmatch(value):
        raise SelectionError(f"selection contains unsupported characters: {value}")
    relative = Path(value)
    if relative.is_absolute():
        raise SelectionError(f"selection must be repository-relative: {value}")
    if any(component in {"", ".", ".."} for component in value.split("/")):
        raise SelectionError(f"selection contains a prohibited path component: {value}")
    if kind == "files" and relative.suffix != ".py":
        raise SelectionError(f"selected Python path must end in .py: {value}")

    try:
        resolved = (root / relative).resolve(strict=True)
    except FileNotFoundError as exc:
        raise SelectionError(f"selection does not exist: {value}") from exc
    try:
        resolved.relative_to(root)
    except ValueError as exc:
        raise SelectionError(f"selection resolves outside repository: {value}") from exc
    if kind == "files" and not resolved.is_file():
        raise SelectionError(f"selected Python path is not a file: {value}")


def validate_selection(root: Path, raw_selection: str, kind: SelectionKind) -> list[str]:
    resolved_root = root.resolve(strict=True)
    selections = raw_selection.split()
    if not selections:
        variable = "PYTHON_FILES" if kind == "files" else "PYTHON_ACCEPTANCE_PATH"
        raise SelectionError(f"{variable} requires a repository-relative selection")
    if kind == "acceptance" and len(selections) != 1:
        raise SelectionError("PYTHON_ACCEPTANCE_PATH must contain exactly one path")
    for selection in selections:
        validate_path(resolved_root, selection, kind)
    return selections


def parse_args(argv: Sequence[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", required=True, type=Path)
    parser.add_argument("--kind", required=True, choices=("files", "acceptance"))
    parser.add_argument("--selection-variable", required=True)
    return parser.parse_args(argv)


def run(argv: Sequence[str] | None, environ: Mapping[str, str] | None = None) -> int:
    arguments = parse_args(argv)
    environment = os.environ if environ is None else environ
    try:
        validate_selection(
            arguments.root,
            environment.get(arguments.selection_variable, ""),
            arguments.kind,
        )
    except (OSError, SelectionError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2
    return 0


def main(argv: Sequence[str] | None = None) -> int:
    return run(argv)


if __name__ == "__main__":
    raise SystemExit(main())
