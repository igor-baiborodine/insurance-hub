"""Shared runtime boundaries for Kubernetes administrative commands."""

from __future__ import annotations

import json
import os
import re
import stat
import sys
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
SHARED_PYTHON = REPOSITORY_ROOT / "scripts" / "python"
if str(SHARED_PYTHON) not in sys.path:
    sys.path.insert(0, str(SHARED_PYTHON))

from process_runner import CommandError, run_command  # noqa: E402


class AdminError(RuntimeError):
    """An administrative contract or operation failed."""


@dataclass(frozen=True)
class Target:
    environment: str
    context: str
    data_namespace: str
    service_namespace: str
    monitoring_namespace: str
    flux_namespace: str = "flux-system"


TARGETS = {
    "local-dev": Target(
        "local-dev",
        "kind-local-dev-insurance-hub",
        "local-dev-all",
        "local-dev-all",
        "INVALID",
        "INVALID",
    ),
    "qa": Target("qa", "qa-insurance-hub", "qa-data", "qa-svc", "qa-monitoring"),
}
SAFE_NAME = re.compile(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?")
SAFE_CHILD_ENV = {
    "DOCKER_HOST",
    "HOME",
    "KUBECONFIG",
    "LANG",
    "LC_ALL",
    "LOGNAME",
    "PATH",
    "SSL_CERT_DIR",
    "SSL_CERT_FILE",
    "TERM",
    "TMPDIR",
    "USER",
    "XDG_CONFIG_HOME",
    "XDG_RUNTIME_DIR",
}


def require(condition: bool, message: str) -> None:
    if not condition:
        raise AdminError(message)


def safe_name(value: str, label: str) -> str:
    require(bool(SAFE_NAME.fullmatch(value)), f"{label} must be a DNS-safe lowercase name")
    return value


def selected_target(
    environment: str,
    context: str,
    *,
    data_namespace: str | None = None,
    service_namespace: str | None = None,
    monitoring_namespace: str | None = None,
    flux_namespace: str | None = None,
) -> Target:
    require(environment in TARGETS, "environment must be local-dev or qa")
    expected = TARGETS[environment]
    selected = Target(
        environment,
        context,
        expected.data_namespace if data_namespace is None else data_namespace,
        expected.service_namespace if service_namespace is None else service_namespace,
        expected.monitoring_namespace if monitoring_namespace is None else monitoring_namespace,
        expected.flux_namespace if flux_namespace is None else flux_namespace,
    )
    require(
        selected.context == expected.context, f"context must explicitly equal {expected.context}"
    )
    require(
        selected.data_namespace == expected.data_namespace,
        f"data namespace must explicitly equal {expected.data_namespace}",
    )
    require(
        selected.service_namespace == expected.service_namespace,
        f"service namespace must explicitly equal {expected.service_namespace}",
    )
    require(
        selected.monitoring_namespace == expected.monitoring_namespace,
        f"monitoring namespace must explicitly equal {expected.monitoring_namespace}",
    )
    require(
        selected.flux_namespace == expected.flux_namespace,
        f"Flux namespace must explicitly equal {expected.flux_namespace}",
    )
    return selected


def secret_input(name: str, environment: Mapping[str, str] | None = None) -> str:
    """Read a secret from one protected source without disclosing its value."""
    source = os.environ if environment is None else environment
    raw = source.get(name, "")
    file_value = source.get(f"{name}_FILE", "")
    require(not (raw and file_value), f"{name} and {name}_FILE cannot both be set")
    if not file_value:
        return raw
    path = Path(file_value).expanduser()
    require(path.is_absolute(), f"{name}_FILE must be an absolute path")
    descriptor = -1
    try:
        descriptor = os.open(path, os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW)
        details = os.fstat(descriptor)
    except OSError as error:
        raise AdminError(f"{name}_FILE cannot be inspected") from error
    try:
        require(stat.S_ISREG(details.st_mode), f"{name}_FILE must be a regular file")
        require(details.st_uid == os.getuid(), f"{name}_FILE must be owned by the invoking user")
        require(
            details.st_mode & 0o077 == 0,
            f"{name}_FILE must not be accessible by group or others",
        )
        try:
            with os.fdopen(descriptor) as input_file:
                descriptor = -1
                value = input_file.read()
        except OSError as error:
            raise AdminError(f"{name}_FILE cannot be read") from error
    finally:
        if descriptor >= 0:
            os.close(descriptor)
    if value.endswith("\n"):
        value = value[:-1]
    require(
        value and "\n" not in value and "\r" not in value,
        f"{name}_FILE must contain one non-empty line",
    )
    return value


class Commands:
    """Bounded command adapter with a deliberately small test seam."""

    def __init__(
        self, *, cwd: Path = REPOSITORY_ROOT, environment: Mapping[str, str] | None = None
    ):
        self.cwd = cwd
        source = os.environ if environment is None else environment
        self.environment = {key: value for key, value in source.items() if key in SAFE_CHILD_ENV}

    def run(
        self,
        arguments: Sequence[str],
        *,
        input_text: str | None = None,
        timeout: float = 120,
        activity: str | None = None,
    ) -> str:
        result = run_command(
            arguments,
            cwd=self.cwd,
            env=self.environment,
            timeout_seconds=timeout,
            activity=activity or arguments[0],
            input_text=input_text,
            teardown_seconds=10,
        )
        return result.stdout

    def succeeds(self, arguments: Sequence[str], *, timeout: float = 30) -> bool:
        try:
            self.run(arguments, timeout=timeout)
        except CommandError:
            return False
        return True


class Kubernetes:
    def __init__(self, commands: Commands, target: Target):
        self.commands = commands
        self.target = target

    def kubectl(
        self,
        *arguments: str,
        input_text: str | None = None,
        timeout: float = 120,
        activity: str | None = None,
    ) -> str:
        return self.commands.run(
            ["kubectl", "--context", self.target.context, *arguments],
            input_text=input_text,
            timeout=timeout,
            activity=activity,
        )

    def verify_context(self) -> None:
        observed = self.commands.run(
            ["kubectl", "config", "current-context"], timeout=15, activity="inspect context"
        ).strip()
        require(
            observed == self.target.context,
            f"current context is {observed!r}, expected {self.target.context!r}",
        )

    def require_namespace(self, namespace: str) -> None:
        safe_name(namespace, "namespace")
        self.kubectl("get", "namespace", namespace, "-o", "name", timeout=30)

    def ensure_namespace(self, namespace: str) -> None:
        safe_name(namespace, "namespace")
        payload = {
            "apiVersion": "v1",
            "kind": "Namespace",
            "metadata": {"name": namespace},
        }
        self.apply(payload)

    def apply(self, payload: Mapping[str, object], *, namespace: str | None = None) -> None:
        arguments = [] if namespace is None else ["-n", namespace]
        self.kubectl(*arguments, "apply", "-f", "-", input_text=json.dumps(payload), timeout=60)

    def secret(self, namespace: str, name: str) -> dict[str, object]:
        output = self.kubectl("-n", namespace, "get", "secret", name, "-o", "json", timeout=30)
        try:
            value = json.loads(output)
        except json.JSONDecodeError as error:
            raise AdminError(f"Secret {namespace}/{name} returned malformed JSON") from error
        require(value.get("metadata", {}).get("name") == name, "Secret identity mismatch")
        return value


def cli_error(name: str, function) -> int:
    try:
        return function()
    except KeyboardInterrupt:
        print(f"{name}: interrupted", file=sys.stderr)
        return 130
    except (AdminError, CommandError, OSError, ValueError, KeyError) as error:
        print(f"{name}: {error}", file=sys.stderr)
        return 1
