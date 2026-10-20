#!/usr/bin/env python3
"""Format or verify doctoc tables of contents for repository Markdown files."""

from __future__ import annotations

import argparse
import os
import shutil
import signal
import subprocess
import sys
import tempfile
from pathlib import Path

DOCTOC_VERSION = "2.5.0"
DOCTOC_TIMEOUT_SECONDS = 180


class MarkdownTocError(RuntimeError):
    """The Markdown TOC request cannot be completed safely."""


class MarkdownTocTerminated(Exception):
    """The CLI received a handled termination signal."""


def handle_signal(signum: int, _frame: object) -> None:
    raise MarkdownTocTerminated(signum)


def repository_root(cwd: Path) -> Path:
    try:
        result = subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            cwd=cwd,
            check=True,
            text=True,
            capture_output=True,
            timeout=30,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise MarkdownTocError("run this command from an Insurance Hub checkout") from error
    return Path(result.stdout.strip()).resolve(strict=True)


def select_files(root: Path, cwd: Path, values: list[str]) -> list[Path]:
    selected: list[Path] = []
    for value in values:
        requested = Path(value)
        candidate = requested if requested.is_absolute() else cwd / requested
        if not candidate.is_file():
            raise MarkdownTocError(f"file does not exist: {value}")
        if candidate.suffix != ".md":
            raise MarkdownTocError(f"expected a .md file: {value}")
        canonical = candidate.resolve(strict=True)
        if not canonical.is_relative_to(root):
            raise MarkdownTocError(f"file is outside the repository: {value}")
        selected.append(canonical)
    return selected


def run_doctoc(path: Path, *, quiet: bool = False) -> None:
    if shutil.which("npx") is None:
        raise MarkdownTocError("npx is required")
    process = subprocess.Popen(
        [
            "npx",
            "--yes",
            "--package",
            f"doctoc@{DOCTOC_VERSION}",
            "doctoc",
            "--github",
            "--notitle",
            "--mintocitems",
            "1",
            str(path),
        ],
        stdout=subprocess.DEVNULL if quiet else None,
        env={"PATH": os.environ.get("PATH", ""), "HOME": os.environ.get("HOME", "")},
        start_new_session=True,
    )
    try:
        return_code = process.wait(timeout=DOCTOC_TIMEOUT_SECONDS)
    except BaseException:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait(timeout=5)
        raise
    if return_code != 0:
        raise MarkdownTocError(f"doctoc failed with status {return_code}")


def format_files(files: list[Path]) -> int:
    for path in files:
        run_doctoc(path)
    return 0


def check_files(root: Path, files: list[Path]) -> int:
    stale: list[Path] = []
    with tempfile.TemporaryDirectory(prefix="markdown-toc-check.") as directory:
        temporary = Path(directory)
        for index, path in enumerate(files, start=1):
            candidate = temporary / f"{index}.md"
            shutil.copyfile(path, candidate)
            run_doctoc(candidate, quiet=True)
            if path.read_bytes() != candidate.read_bytes():
                stale.append(path)
    for path in stale:
        print(
            f"markdown-toc: stale or missing table of contents: {path.relative_to(root)}",
            file=sys.stderr,
        )
    if stale:
        print("markdown-toc: run the format mode for the reported files", file=sys.stderr)
        return 1
    return 0


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(prog="markdown-toc.sh")
    result.add_argument("mode", choices=("format", "check"))
    result.add_argument("markdown_files", nargs="+")
    return result


def main(argv: list[str] | None = None) -> int:
    arguments = parser().parse_args(argv)
    cwd = Path.cwd().resolve(strict=True)
    root = repository_root(cwd)
    files = select_files(root, cwd, arguments.markdown_files)
    return format_files(files) if arguments.mode == "format" else check_files(root, files)


if __name__ == "__main__":
    signal.signal(signal.SIGHUP, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130) from None
    except MarkdownTocTerminated as error:
        raise SystemExit(128 + int(error.args[0])) from None
    except subprocess.TimeoutExpired:
        print("markdown-toc: doctoc exceeded its 180-second deadline", file=sys.stderr)
        raise SystemExit(1) from None
    except (MarkdownTocError, OSError, subprocess.SubprocessError) as error:
        print(f"markdown-toc: {error}", file=sys.stderr)
        raise SystemExit(1) from None
