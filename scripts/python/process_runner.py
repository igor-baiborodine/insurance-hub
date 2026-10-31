"""Bounded subprocess execution for repository tooling."""

from __future__ import annotations

import os
import signal
import subprocess
import sys
import tempfile
import threading
import time
from collections.abc import Callable, Mapping, Sequence
from pathlib import Path


class CommandError(RuntimeError):
    """A bounded child command failed or exceeded its deadline."""


class CommandSignal(BaseException):
    """A handled POSIX signal requested controlled tooling teardown."""

    def __init__(self, signum: int) -> None:
        self.signum = signum
        super().__init__(signal.Signals(signum).name)


CAPTURE_LIMIT = 4 * 1024 * 1024


def run_cli(main: Callable[[], int], *, name: str) -> int:
    """Run a CLI with SIGINT/SIGTERM converted into protected teardown unwinding."""
    handled = (signal.SIGINT, signal.SIGTERM)
    previous = {signum: signal.getsignal(signum) for signum in handled}

    def interrupt(signum: int, _frame: object) -> None:
        for protected in handled:
            signal.signal(protected, signal.SIG_IGN)
        raise CommandSignal(signum)

    try:
        for signum in handled:
            signal.signal(signum, interrupt)
        try:
            return main()
        except CommandSignal as interruption:
            print(
                f"{name}: interrupted by {signal.Signals(interruption.signum).name}",
                file=sys.stderr,
            )
            return 128 + interruption.signum
    finally:
        for signum, handler in previous.items():
            signal.signal(signum, handler)


def _process_group_exists(process: subprocess.Popen[str]) -> bool:
    try:
        os.killpg(process.pid, 0)
    except ProcessLookupError:
        return False
    except OSError:
        return True
    return True


def _wait_for_process_group(process: subprocess.Popen[str], deadline: float) -> bool:
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return False
        group_exists = _process_group_exists(process)
        try:
            process.communicate(timeout=min(remaining, 0.05))
        except subprocess.TimeoutExpired:
            continue
        except OSError, ValueError:
            return False
        if not group_exists or not _process_group_exists(process):
            return True
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            return False
        time.sleep(min(remaining, 0.01))


def _terminate(process: subprocess.Popen[str], teardown_seconds: float) -> bool:
    deadline = time.monotonic() + teardown_seconds
    term_deadline = time.monotonic() + teardown_seconds / 2
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except OSError:
        pass
    if _wait_for_process_group(process, term_deadline):
        return True
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except OSError:
        pass
    return _wait_for_process_group(process, deadline)


def run_command(
    arguments: Sequence[str],
    *,
    cwd: str | os.PathLike[str],
    env: Mapping[str, str],
    timeout_seconds: float,
    activity: str,
    input_text: str | None = None,
    heartbeat_seconds: float = 30,
    teardown_seconds: float = 5,
) -> subprocess.CompletedProcess[str]:
    """Capture a bounded child while reporting progress and owning its process group."""
    if not arguments or timeout_seconds <= 0 or teardown_seconds <= 0:
        raise ValueError("command and positive work/teardown deadlines are required")
    started = time.monotonic()
    deadline = started + timeout_seconds
    heartbeat = started + heartbeat_seconds
    process = subprocess.Popen(
        list(arguments),
        cwd=cwd,
        env=dict(env),
        text=True,
        stdin=subprocess.PIPE if input_text is not None else None,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        start_new_session=True,
    )
    try:
        pending_input = input_text
        while True:
            now = time.monotonic()
            remaining = deadline - now
            if remaining <= 0:
                raise CommandError(f"{activity} exceeded its {timeout_seconds:g}-second deadline")
            try:
                stdout, stderr = process.communicate(
                    input=pending_input, timeout=min(remaining, max(0.1, heartbeat - now))
                )
                break
            except subprocess.TimeoutExpired:
                pending_input = None
                if time.monotonic() >= heartbeat:
                    elapsed = time.monotonic() - started
                    print(f"{activity}: still running after {elapsed:.0f}s", file=sys.stderr)
                    heartbeat = time.monotonic() + heartbeat_seconds
    except BaseException:
        _terminate(process, teardown_seconds)
        raise
    if _process_group_exists(process) and not _terminate(process, teardown_seconds):
        raise CommandError(f"{activity} cleanup exceeded its {teardown_seconds:g}-second deadline")
    result = subprocess.CompletedProcess(list(arguments), process.returncode, stdout, stderr)
    if result.returncode != 0:
        raise CommandError(f"{activity} failed with status {result.returncode}")
    return result


def run(
    arguments: Sequence[str],
    *,
    cwd: Path,
    env: Mapping[str, str],
    timeout_seconds: float,
    activity: str,
    input_text: str | None = None,
    capture: bool = False,
    log_path: Path | None = None,
    teardown_seconds: float = 10,
    expected_failure: bool = False,
) -> str:
    """Run one command with bounded output, progress, and owned process-group cleanup."""
    if not arguments or timeout_seconds <= 0 or teardown_seconds <= 0:
        raise ValueError("command and positive work/teardown deadlines are required")
    if capture and log_path is not None:
        raise ValueError("capture and log_path are mutually exclusive")
    captured_output = tempfile.TemporaryFile(mode="w+", encoding="utf-8") if capture else None
    captured_error = tempfile.TemporaryFile(mode="w+", encoding="utf-8") if capture else None
    command_input = None
    log = None
    if input_text is not None:
        command_input = tempfile.TemporaryFile(mode="w+", encoding="utf-8")
        command_input.write(input_text)
        command_input.seek(0)
    if log_path is not None:
        log_path.parent.mkdir(parents=True, exist_ok=True)
        log = log_path.open("w", encoding="utf-8")
    collect_output = capture or log is not None
    started = time.monotonic()
    deadline = started + timeout_seconds
    heartbeat = started + 30
    pump_errors: list[Exception] = []
    exceeded: set[str] = set()
    threads: list[threading.Thread] = []
    print(f"{activity}: started", file=sys.stderr)
    try:
        process = subprocess.Popen(
            list(arguments),
            cwd=cwd,
            env=dict(env),
            text=True,
            stdin=command_input,
            stdout=subprocess.PIPE if collect_output else None,
            stderr=(subprocess.PIPE if capture else subprocess.STDOUT) if collect_output else None,
            start_new_session=True,
        )

        def start_pump(
            stream: object | None, target: object | None, label: str, bounded: bool
        ) -> None:
            if stream is None or target is None:
                return

            def pump() -> None:
                try:
                    size = 0
                    while chunk := stream.read(64 * 1024):
                        size += len(chunk)
                        if bounded and size > CAPTURE_LIMIT:
                            exceeded.add(label)
                            continue
                        target.write(chunk)
                        target.flush()
                except Exception as error:
                    pump_errors.append(error)

            thread = threading.Thread(target=pump, daemon=True)
            thread.start()
            threads.append(thread)

        start_pump(process.stdout, captured_output if capture else log, "stdout", capture)
        start_pump(process.stderr, captured_error, "stderr", capture)
        while process.poll() is None:
            now = time.monotonic()
            if now >= deadline:
                raise CommandError(f"{activity} exceeded its {timeout_seconds:g}-second deadline")
            if now >= heartbeat:
                print(f"{activity}: still running after {now - started:.0f}s", file=sys.stderr)
                heartbeat = now + 30
            time.sleep(min(0.1, deadline - now))
        for thread in threads:
            thread.join(timeout=teardown_seconds)
        if any(thread.is_alive() for thread in threads):
            raise CommandError(f"{activity} output collection did not stop")
        if pump_errors:
            raise CommandError(f"{activity} output collection failed: {pump_errors[0]}")
        if exceeded:
            labels = ", ".join(sorted(exceeded))
            raise CommandError(
                f"{activity} {labels} exceeded the {CAPTURE_LIMIT}-character capture limit"
            )
        captured_stdout = ""
        captured_stderr = ""
        if captured_output is not None:
            captured_output.seek(0)
            captured_stdout = captured_output.read()
        if captured_error is not None:
            captured_error.seek(0)
            captured_stderr = captured_error.read()
    except BaseException:
        if "process" in locals() and not _terminate(process, teardown_seconds):
            print(f"{activity}: cleanup exceeded {teardown_seconds:g}s", file=sys.stderr)
        for thread in threads:
            thread.join(timeout=teardown_seconds)
        raise
    finally:
        if log is not None:
            log.close()
        if captured_output is not None:
            captured_output.close()
        if captured_error is not None:
            captured_error.close()
        if command_input is not None:
            command_input.close()
    if _process_group_exists(process) and not _terminate(process, teardown_seconds):
        raise CommandError(f"{activity} cleanup exceeded its {teardown_seconds:g}-second deadline")
    if expected_failure and process.returncode == 0:
        raise CommandError(f"{activity} unexpectedly succeeded")
    if not expected_failure and process.returncode != 0:
        detail = (captured_stderr or captured_stdout).strip() if capture else ""
        suffix = f": {detail}" if detail else ""
        raise CommandError(f"{activity} failed with status {process.returncode}{suffix}")
    print(f"{activity}: completed in {time.monotonic() - started:.1f}s", file=sys.stderr)
    return captured_stdout
