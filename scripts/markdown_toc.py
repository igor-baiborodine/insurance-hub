#!/usr/bin/env python3
"""Format or verify doctoc tables of contents for repository Markdown files."""

from __future__ import annotations

import argparse
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

DOCTOC_VERSION = "2.5.0"
DOCTOC_TIMEOUT_SECONDS = 180
DOCTOC_TEARDOWN_SECONDS = 10
MANAGED_PROCESS_GROUP_ENV = "MARKDOWN_TOC_MANAGED_PROCESS_GROUP"


class MarkdownTocError(RuntimeError):
    """The Markdown TOC request cannot be completed safely."""


class MarkdownTocTerminated(Exception):
    """The CLI received a handled termination signal."""


def handle_signal(signum: int, _frame: object) -> None:
    raise MarkdownTocTerminated(signum)


def _process_group_exists(process: subprocess.Popen[bytes]) -> bool:
    try:
        os.killpg(process.pid, 0)
    except ProcessLookupError:
        return False
    except OSError:
        return True
    return True


def _wait_for_process_group(process: subprocess.Popen[bytes], deadline: float) -> bool:
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return False
        group_exists = _process_group_exists(process)
        try:
            process.wait(timeout=min(remaining, 0.05))
        except subprocess.TimeoutExpired:
            continue
        if not group_exists or not _process_group_exists(process):
            return True
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return False
        time.sleep(min(remaining, 0.01))


def _terminate_doctoc(process: subprocess.Popen[bytes], *, managed_process_group: bool) -> bool:
    deadline = time.monotonic() + DOCTOC_TEARDOWN_SECONDS
    term_deadline = time.monotonic() + DOCTOC_TEARDOWN_SECONDS / 2
    try:
        if managed_process_group:
            process.terminate()
        else:
            os.killpg(process.pid, signal.SIGTERM)
    except OSError:
        pass
    if managed_process_group:
        try:
            process.wait(timeout=max(0, term_deadline - time.monotonic()))
            return True
        except subprocess.TimeoutExpired:
            pass
    elif _wait_for_process_group(process, term_deadline):
        return True
    try:
        if managed_process_group:
            process.kill()
        else:
            os.killpg(process.pid, signal.SIGKILL)
    except OSError:
        pass
    if managed_process_group:
        try:
            process.wait(timeout=max(0, deadline - time.monotonic()))
            return True
        except subprocess.TimeoutExpired:
            return False
    return _wait_for_process_group(process, deadline)


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
    provisioned = os.environ.get("AI_DOCTOC_DIR")
    if provisioned:
        package = Path(provisioned).resolve() / "node_modules/doctoc"
        try:
            version = json.loads((package / "package.json").read_text())["version"]
        except (OSError, ValueError, KeyError, TypeError) as error:
            raise MarkdownTocError("AI_DOCTOC_DIR needs explicitly provisioned doctoc") from error
        if version != DOCTOC_VERSION or not (package / "doctoc.js").is_file():
            raise MarkdownTocError(f"AI_DOCTOC_DIR requires doctoc {DOCTOC_VERSION}")
        if shutil.which("node") is None:
            raise MarkdownTocError("node is required for AI_DOCTOC_DIR")
        command = ["node", str(package / "doctoc.js")]
    else:
        if shutil.which("npx") is None:
            raise MarkdownTocError("npx is required")
        command = ["npx", "--yes", "--package", f"doctoc@{DOCTOC_VERSION}", "doctoc"]
    managed_process_group = os.environ.get(MANAGED_PROCESS_GROUP_ENV) == "1"
    if managed_process_group and os.getpid() != os.getpgrp():
        raise MarkdownTocError("managed doctoc execution requires an owned process group")
    environment = {"PATH": os.environ.get("PATH", ""), "HOME": os.environ.get("HOME", "")}
    if "npm_config_offline" in os.environ:
        environment["npm_config_offline"] = os.environ["npm_config_offline"]
    process = subprocess.Popen(
        [
            *command,
            "--github",
            "--notitle",
            "--mintocitems",
            "1",
            str(path),
        ],
        stdout=subprocess.DEVNULL if quiet else None,
        env=environment,
        start_new_session=not managed_process_group,
    )
    try:
        return_code = process.wait(timeout=DOCTOC_TIMEOUT_SECONDS)
    except BaseException:
        if not _terminate_doctoc(process, managed_process_group=managed_process_group):
            print(
                "markdown-toc: doctoc cleanup exceeded its 10-second deadline",
                file=sys.stderr,
            )
        raise
    if not managed_process_group and _process_group_exists(process):
        if not _terminate_doctoc(process, managed_process_group=False):
            raise MarkdownTocError("doctoc cleanup exceeded its 10-second deadline")
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
