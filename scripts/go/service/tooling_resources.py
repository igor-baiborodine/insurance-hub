"""Explicit ownership and bounded cleanup for disposable tooling resources."""

from __future__ import annotations

import subprocess
import tempfile
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import BinaryIO, TypeVar

from scripts.python.process_runner import terminate_process_group

T = TypeVar("T")


class ResourceError(RuntimeError):
    """An owned disposable resource could not be started or cleaned up safely."""


@dataclass
class ResourceOwner:
    """Own cleanup callbacks and execute them once in reverse startup order."""

    _cleanups: list[tuple[str, Callable[[], None]]] = field(default_factory=list)

    def add(self, name: str, cleanup: Callable[[], None]) -> None:
        if not name:
            raise ValueError("an owned resource name is required")
        self._cleanups.append((name, cleanup))

    def start(self, name: str, factory: Callable[[], tuple[T, Callable[[], None]]]) -> T:
        """Start and register a resource, cleaning earlier resources on partial failure."""
        try:
            resource, cleanup = factory()
        except BaseException:
            self.close()
            raise
        self.add(name, cleanup)
        return resource

    def close(self) -> None:
        failures: list[str] = []
        while self._cleanups:
            name, cleanup = self._cleanups.pop()
            try:
                cleanup()
            except BaseException as error:
                failures.append(f"{name}: {error}")
        if failures:
            raise ResourceError("owned resource cleanup failed: " + "; ".join(failures))

    def __enter__(self) -> ResourceOwner:
        return self

    def __exit__(self, _type: object, _value: object, _traceback: object) -> None:
        self.close()


@dataclass
class OwnedProcess:
    """A process group with private diagnostics and an explicit teardown deadline."""

    process: subprocess.Popen[bytes]
    output: BinaryIO
    teardown_seconds: float
    sensitive_values: tuple[str, ...] = ()
    _closed: bool = False

    @classmethod
    def start(
        cls,
        arguments: Sequence[str],
        *,
        cwd: Path,
        env: Mapping[str, str],
        teardown_seconds: float,
        sensitive_values: Sequence[str] = (),
    ) -> OwnedProcess:
        if not arguments or teardown_seconds <= 0:
            raise ValueError("command and positive teardown deadline are required")
        if any(not value for value in sensitive_values):
            raise ValueError("sensitive values must be non-empty")
        if any(value in argument for value in sensitive_values for argument in arguments):
            raise ValueError("command arguments must not contain sensitive values")
        if any(value in item for value in sensitive_values for item in env.values()):
            raise ValueError("command environment must not contain sensitive values")
        output = tempfile.TemporaryFile()
        try:
            process = subprocess.Popen(
                list(arguments),
                cwd=cwd,
                env=dict(env),
                stdin=subprocess.DEVNULL,
                stdout=output,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        except BaseException:
            output.close()
            raise
        return cls(
            process=process,
            output=output,
            teardown_seconds=teardown_seconds,
            sensitive_values=tuple(sensitive_values),
        )

    def diagnostics(self) -> str:
        self.output.flush()
        self.output.seek(0)
        value = self.output.read().decode("utf-8", errors="replace")
        for sensitive in self.sensitive_values:
            value = value.replace(sensitive, "[REDACTED]")
        return value

    def close(self) -> None:
        if self._closed:
            return
        self._closed = True
        try:
            if not terminate_process_group(self.process, self.teardown_seconds):
                raise ResourceError(
                    f"owned process cleanup exceeded its {self.teardown_seconds:g}-second deadline"
                )
        finally:
            self.output.close()
