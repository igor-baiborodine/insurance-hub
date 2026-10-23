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
import time
from pathlib import Path

from markdown_toc import DOCTOC_TIMEOUT_SECONDS, MANAGED_PROCESS_GROUP_ENV, is_framework_adapter

CHECKER_OVERHEAD_SECONDS = 30
CHECKER_TEARDOWN_SECONDS = 10
TEST_CHECKER_TIMEOUT_ENV = "MARKDOWN_HOOK_TEST_CHECKER_TIMEOUT_SECONDS"


class HookError(RuntimeError):
    """The staged Markdown snapshot could not be validated."""


class HookTerminated(Exception):
    """The hook received a handled termination signal."""


def handle_signal(signum: int, _frame: object) -> None:
    raise HookTerminated(signum)


def _process_group_exists(process: subprocess.Popen[bytes]) -> bool:
    try:
        os.killpg(process.pid, 0)
    except ProcessLookupError:
        return False
    except OSError:
        return True
    return True


def _wait_for_checker_group(process: subprocess.Popen[bytes], deadline: float) -> bool:
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


def _terminate_checker(process: subprocess.Popen[bytes], termination_signal: int) -> bool:
    deadline = time.monotonic() + CHECKER_TEARDOWN_SECONDS
    term_deadline = time.monotonic() + CHECKER_TEARDOWN_SECONDS / 2
    if process.poll() is None:
        try:
            process.send_signal(termination_signal)
        except OSError:
            pass
    else:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except OSError:
            pass
    if _wait_for_checker_group(process, term_deadline):
        return True
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except OSError:
        pass
    return _wait_for_checker_group(process, deadline)


def _checker_timeout(root: Path, path_count: int) -> float:
    test_timeout = os.environ.get(TEST_CHECKER_TIMEOUT_ENV)
    if test_timeout is None:
        return CHECKER_OVERHEAD_SECONDS + DOCTOC_TIMEOUT_SECONDS * path_count
    if not (root / ".markdown-hook-test-fixture").is_file():
        raise HookError("controlled checker timeout requires a marked disposable fixture")
    try:
        timeout = float(test_timeout)
    except ValueError as error:
        raise HookError("controlled checker timeout must be a number") from error
    if timeout <= 0:
        raise HookError("controlled checker timeout must be positive")
    return timeout


def _cancellation_signal(error: BaseException) -> int:
    if isinstance(error, HookTerminated):
        return int(error.args[0])
    if isinstance(error, KeyboardInterrupt):
        return signal.SIGINT
    return signal.SIGTERM


def _run_checker(
    root: Path, command: list[str], environment: dict[str, str], timeout: float
) -> int:
    process = subprocess.Popen(
        command,
        cwd=root,
        env=environment,
        start_new_session=True,
    )
    try:
        return_code = process.wait(timeout=timeout)
    except BaseException as error:
        if not _terminate_checker(process, _cancellation_signal(error)):
            print(
                "markdown-toc-pre-commit: checker cleanup exceeded its 10-second deadline",
                file=sys.stderr,
            )
        raise
    if _process_group_exists(process) and not _terminate_checker(process, signal.SIGTERM):
        raise HookError("checker cleanup exceeded its 10-second deadline")
    return return_code


def run_git(
    root: Path, arguments: list[str], **kwargs: object
) -> subprocess.CompletedProcess[bytes]:
    return subprocess.run(
        ["git", *arguments],
        cwd=root,
        check=True,
        timeout=30,
        env=dict(os.environ),
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
    paths = [path for path in paths if not is_framework_adapter(Path(path))]
    if not paths:
        return 0
    snapshot = Path(tempfile.mkdtemp(prefix=".markdown-toc-index.", dir=root))
    checker_temporary = Path(tempfile.mkdtemp(prefix=".markdown-toc-staged-paths.", dir=root))
    try:
        run_git(root, ["checkout-index", f"--prefix={snapshot}/", "--", *paths])
        if os.environ.get("MARKDOWN_HOOK_TEST_PAUSE_AFTER_SNAPSHOT") == "1":
            if not (root / ".markdown-hook-test-fixture").is_file():
                raise HookError("controlled pause requires a marked disposable fixture")
            while True:
                signal.pause()
        command = [str(root / "scripts" / "markdown-toc.sh"), "check"]
        command.extend(str(snapshot / path) for path in paths)
        environment = {
            **os.environ,
            "PYTHON": sys.executable,
            "TMPDIR": str(checker_temporary),
            MANAGED_PROCESS_GROUP_ENV: "1",
        }
        return_code = _run_checker(
            root,
            command,
            environment,
            _checker_timeout(root, len(paths)),
        )
        if return_code == 0:
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
        try:
            shutil.rmtree(checker_temporary)
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
