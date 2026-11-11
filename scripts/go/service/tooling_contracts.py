"""Typed generic contracts for Go-service repository tooling boundaries."""

from __future__ import annotations

import base64
import http.client
import json
import re
import socket
import ssl
import time
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
from scripts.go.service.tooling_resources import OwnedProcess
from scripts.python.process_runner import child_environment

RESOURCE_NAME = re.compile(r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?")
RESOURCE_REFERENCE = re.compile(r"(?:deployment|pod|service)/[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?")
OWNER_TOKEN = re.compile(r"[a-z0-9][a-z0-9-]{0,62}")
MAX_RESPONSE_BYTES = 64 * 1024


class AdministrationError(RuntimeError):
    """A guarded administration transport violated its runtime contract."""


def require_administration(condition: object, message: str) -> None:
    if not condition:
        raise AdministrationError(message)


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


class ForwardedHTTPSConnection(http.client.HTTPSConnection):
    """Keep TLS server-name verification while connecting through loopback."""

    def __init__(
        self,
        server_name: str,
        forwarded_port: int,
        context: ssl.SSLContext,
        timeout: float,
    ) -> None:
        super().__init__(server_name, 443, context=context, timeout=timeout)
        self.forwarded_port = forwarded_port

    def connect(self) -> None:
        plain = socket.create_connection(("127.0.0.1", self.forwarded_port), self.timeout)
        try:
            self.sock = self._context.wrap_socket(plain, server_hostname=self.host)
        except BaseException:
            plain.close()
            raise


class JSONAdministrationClient:
    """Bounded loopback JSON client with optional verified TLS and basic authentication."""

    def __init__(
        self,
        port: int,
        username: str,
        password: str,
        *,
        server_name: str = "",
        ca_certificate: bytes = b"",
    ) -> None:
        if not isinstance(port, int) or not 1 <= port <= 65535:
            raise AdministrationError("administration port is invalid")
        self.port = port
        self.username = username
        self.password = password
        self.server_name = server_name
        self.context: ssl.SSLContext | None = None
        if server_name:
            require_administration(bool(ca_certificate), "public CA is required")
            self.context = ssl.create_default_context(cadata=ca_certificate.decode("utf-8"))
            self.context.minimum_version = ssl.TLSVersion.TLSv1_2

    def request(
        self,
        method: str,
        path: str,
        body: dict[str, object] | bytes | None = None,
    ) -> tuple[int, object]:
        payload: bytes | None
        content_type = "application/json"
        if isinstance(body, dict):
            payload = json.dumps(body, separators=(",", ":")).encode("utf-8")
        else:
            payload = body
            if isinstance(body, bytes):
                content_type = "application/x-ndjson"
        headers: dict[str, str] = {}
        if self.username or self.password:
            credential = base64.b64encode(f"{self.username}:{self.password}".encode()).decode(
                "ascii"
            )
            headers["Authorization"] = f"Basic {credential}"
        if payload is not None:
            headers["Content-Type"] = content_type
        connection: http.client.HTTPConnection
        if self.context is None:
            connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=10)
        else:
            connection = ForwardedHTTPSConnection(
                self.server_name, self.port, self.context, timeout=10
            )
        try:
            connection.request(method, path, body=payload, headers=headers)
            response = connection.getresponse()
            data = response.read(MAX_RESPONSE_BYTES + 1)
            require_administration(
                len(data) <= MAX_RESPONSE_BYTES,
                "administration response exceeded limit",
            )
            if not data:
                decoded: object = None
            else:
                try:
                    decoded = json.loads(data)
                except json.JSONDecodeError:
                    decoded = None
            return response.status, decoded
        except (OSError, http.client.HTTPException, ssl.SSLError) as error:
            raise AdministrationError("administration request failed") from error
        finally:
            connection.close()


class OwnedPortForward:
    """Start and clean one context-bound port forward through the shared process owner."""

    def __init__(
        self,
        spec: PortForwardSpec,
        *,
        cwd: Path,
        env: Mapping[str, str],
        startup_seconds: float,
        teardown_seconds: float = 5,
        kubectl: str = "kubectl",
    ) -> None:
        if startup_seconds <= 0:
            raise ValueError("a positive port-forward startup deadline is required")
        self.spec = spec
        self.cwd = cwd
        self.env = env
        self.startup_seconds = startup_seconds
        self.teardown_seconds = teardown_seconds
        self.kubectl = kubectl
        self.owned: OwnedProcess | None = None

    def __enter__(self) -> int:
        owned = OwnedProcess.start(
            self.spec.arguments(kubectl=self.kubectl),
            cwd=self.cwd,
            env=self.env,
            teardown_seconds=self.teardown_seconds,
        )
        self.owned = owned
        deadline = time.monotonic() + self.startup_seconds
        stable_since = time.monotonic()
        try:
            while time.monotonic() < deadline:
                if owned.process.poll() is not None:
                    raise AdministrationError(
                        "port-forward stopped early: " + owned.diagnostics().strip()
                    )
                if time.monotonic() - stable_since >= 1:
                    return self.spec.local_port
                time.sleep(0.1)
            raise AdministrationError("port-forward did not become ready")
        except BaseException:
            owned.close()
            raise

    def __exit__(self, *_: object) -> None:
        if self.owned is not None:
            self.owned.close()


def loopback_port() -> int:
    """Reserve and return one currently available loopback TCP port."""
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def require_status(status: int, accepted: set[int], activity: str) -> None:
    require_administration(status in accepted, f"{activity} returned HTTP {status}")


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
