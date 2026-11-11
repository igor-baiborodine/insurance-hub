"""Typed generic contracts for Go-service repository tooling boundaries."""

from __future__ import annotations

import json
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path, PurePosixPath

from scripts.go.service.tooling_config import (
    NAME,
    PINNED_IMAGE,
    DependencyConfig,
    ToolingConfigError,
)
from scripts.go.service.tooling_process import run
from scripts.python.process_runner import child_environment

RESOURCE_NAME = re.compile(r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?")
RESOURCE_REFERENCE = re.compile(r"(?:deployment|pod|service)/[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?")
OWNER_TOKEN = re.compile(r"[a-z0-9][a-z0-9-]{0,62}")


def module_file(module_root: Path, relative: str) -> Path:
    """Resolve an existing module-owned file without following an escaping symlink."""
    path = PurePosixPath(relative)
    if path.is_absolute() or ".." in path.parts:
        raise ToolingConfigError("module file path must be relative")
    try:
        root = module_root.resolve(strict=True)
        resolved = (root / path).resolve(strict=True)
    except OSError as error:
        raise ToolingConfigError("module file is missing") from error
    if not resolved.is_relative_to(root) or not resolved.is_file():
        raise ToolingConfigError("module file escapes its owner")
    return resolved


def load_json_object(module_root: Path, relative: str) -> dict[str, object]:
    """Load a gateway document as an object from a validated module-owned path."""
    try:
        value = json.loads(module_file(module_root, relative).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ToolingConfigError("gateway document is unreadable") from error
    if not isinstance(value, dict):
        raise ToolingConfigError("gateway document must contain a JSON object")
    return value


def dependency_container_arguments(
    dependency: DependencyConfig,
    *,
    owner: str,
    docker: str = "docker",
) -> tuple[str, ...]:
    """Build an owned disposable-container command from validated typed data."""
    if not OWNER_TOKEN.fullmatch(owner):
        raise ToolingConfigError("dependency owner token is invalid")
    if not NAME.fullmatch(dependency.name) or not NAME.fullmatch(dependency.kind):
        raise ToolingConfigError("dependency identity is invalid")
    if not PINNED_IMAGE.fullmatch(dependency.image):
        raise ToolingConfigError("dependency image is not digest-pinned")
    if not dependency.ports or any(
        not isinstance(port, int) or not 1 <= port <= 65535 for port in dependency.ports
    ):
        raise ToolingConfigError("dependency ports are invalid")
    if len(dependency.ports) != len(set(dependency.ports)):
        raise ToolingConfigError("dependency ports contain duplicates")
    container_name = f"insurance-hub-{owner}-{dependency.name}"
    arguments = [
        docker,
        "run",
        "--detach",
        "--name",
        container_name,
        "--label",
        f"insurance-hub.tooling-owner={owner}",
    ]
    for port in dependency.ports:
        arguments.extend(("--publish", f"127.0.0.1::{port}"))
    arguments.append(dependency.image)
    return tuple(arguments)


@dataclass(frozen=True)
class PortForwardSpec:
    context: str
    namespace: str
    resource: str
    local_port: int
    remote_port: int

    def arguments(self, *, kubectl: str = "kubectl") -> tuple[str, ...]:
        """Build a namespace- and context-bound kubectl port-forward command."""
        for value, field in ((self.context, "context"), (self.namespace, "namespace")):
            if not RESOURCE_NAME.fullmatch(value):
                raise ToolingConfigError(f"port-forward {field} is invalid")
        if not RESOURCE_REFERENCE.fullmatch(self.resource):
            raise ToolingConfigError("port-forward resource is invalid")
        for value, field in ((self.local_port, "local port"), (self.remote_port, "remote port")):
            if not isinstance(value, int) or not 1 <= value <= 65535:
                raise ToolingConfigError(f"port-forward {field} is invalid")
        return (
            kubectl,
            "--context",
            self.context,
            "--namespace",
            self.namespace,
            "port-forward",
            self.resource,
            f"{self.local_port}:{self.remote_port}",
        )


def required_secret(name: str, source: Mapping[str, str]) -> str:
    """Read a named secret without placing its value in configuration."""
    if not re.fullmatch(r"[A-Z][A-Z0-9_]*", name):
        raise ToolingConfigError("secret environment name is invalid")
    value = source.get(name, "")
    if not value:
        raise ToolingConfigError(f"required secret environment is missing: {name}")
    return value


def run_secret_input(
    arguments: Sequence[str],
    *,
    cwd: Path,
    secret: str,
    timeout_seconds: float,
    activity: str,
    environment: Mapping[str, str] | None = None,
) -> None:
    """Run an administrative transport with the secret confined to standard input."""
    if not secret:
        raise ToolingConfigError("administrative secret is empty")
    run(
        arguments,
        cwd=cwd,
        env=child_environment(environment, preferred_executables=arguments[:1]),
        timeout_seconds=timeout_seconds,
        activity=activity,
        input_text=f"{secret}\n",
        sensitive_values=(secret,),
    )
