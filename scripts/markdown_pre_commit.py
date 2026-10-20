#!/usr/bin/env python3
"""Validate doctoc content from the Git index without touching the working tree."""

from __future__ import annotations

import os
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
from pathlib import Path


class HookError(RuntimeError):
    """The staged Markdown snapshot could not be validated."""


class HookTerminated(Exception):
    """The hook received a handled termination signal."""


def handle_signal(signum: int, _frame: object) -> None:
    raise HookTerminated(signum)


def run_git(
    root: Path, arguments: list[str], **kwargs: object
) -> subprocess.CompletedProcess[bytes]:
    return subprocess.run(
        ["git", *arguments],
        cwd=root,
        check=True,
        timeout=30,
        env={"PATH": os.environ.get("PATH", "")},
        **kwargs,
    )


def staged_markdown(root: Path) -> list[str]:
    result = run_git(
        root,
        [
            "diff",
            "--cached",
            "--name-only",
            "--diff-filter=ACMR",
            "--find-renames",
            "-z",
            "--",
            "*.md",
        ],
        stdout=subprocess.PIPE,
    )
    return [value.decode("utf-8") for value in result.stdout.split(b"\0") if value]


def validate_staged(root: Path, paths: list[str]) -> int:
    if not paths:
        return 0
    snapshot = Path(tempfile.mkdtemp(prefix=".markdown-toc-index.", dir=root))
    try:
        run_git(root, ["checkout-index", f"--prefix={snapshot}/", "--", *paths])
        if os.environ.get("MARKDOWN_HOOK_TEST_PAUSE_AFTER_SNAPSHOT") == "1":
            if not (root / ".markdown-hook-test-fixture").is_file():
                raise HookError("controlled pause requires a marked disposable fixture")
            while True:
                signal.pause()
        command = [str(root / "scripts" / "markdown-toc.sh"), "check"]
        command.extend(str(snapshot / path) for path in paths)
        result = subprocess.run(
            command,
            cwd=root,
            check=False,
            timeout=240,
            env={**os.environ, "PYTHON": sys.executable},
        )
        if result.returncode == 0:
            return 0
        print("markdown-toc-pre-commit: staged Markdown validation failed for:", file=sys.stderr)
        for path in paths:
            print(f"  {shlex.quote(path)}", file=sys.stderr)
        print(
            "markdown-toc-pre-commit: format the working-tree files with:\n"
            "  ./scripts/markdown-toc.sh format <file>...\n"
            "markdown-toc-pre-commit: then stage the formatted content and commit again.",
            file=sys.stderr,
        )
        return 1
    finally:
        shutil.rmtree(snapshot)


def main(argv: list[str] | None = None) -> int:
    if argv:
        raise HookError("the pre-commit hook does not accept arguments")
    root = Path(
        subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            check=True,
            text=True,
            capture_output=True,
            timeout=30,
        ).stdout.strip()
    ).resolve(strict=True)
    return validate_staged(root, staged_markdown(root))


if __name__ == "__main__":
    signal.signal(signal.SIGHUP, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130) from None
    except HookTerminated as error:
        raise SystemExit(128 + int(error.args[0])) from None
    except subprocess.TimeoutExpired:
        print("markdown-toc-pre-commit: validation deadline exceeded", file=sys.stderr)
        raise SystemExit(1) from None
    except (HookError, OSError, subprocess.SubprocessError) as error:
        print(f"markdown-toc-pre-commit: {error}", file=sys.stderr)
        raise SystemExit(1) from None
