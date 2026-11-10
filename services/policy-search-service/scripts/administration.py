"""Shared guarded administration primitives for Policy Search resources."""

from __future__ import annotations

import base64
import http.client
import json
import os
import signal
import socket
import ssl
import subprocess
import time
from pathlib import Path
from typing import Any, Protocol

from scripts.python.process_runner import run_command

MAX_RESPONSE_BYTES = 64 * 1024
ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()


class AdministrationError(RuntimeError):
    """The selected target or Elasticsearch state differs from the service contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise AdministrationError(message)


def safe_environment() -> dict[str, str]:
    allowed = ("HOME", "KUBECONFIG", "PATH", "SSL_CERT_DIR", "SSL_CERT_FILE", "TMPDIR")
    return {name: os.environ[name] for name in allowed if name in os.environ}


def command(arguments: list[str], *, input_text: str | None = None, timeout: int = 30) -> str:
    result = run_command(
        arguments,
        cwd=ROOT,
        env=safe_environment(),
        input_text=input_text,
        timeout_seconds=timeout,
        teardown_seconds=10,
        activity="Policy Search administration",
    )
    return result.stdout


class ForwardedHTTPSConnection(http.client.HTTPSConnection):
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


class ElasticsearchClient:
    def __init__(
        self,
        port: int,
        username: str,
        password: str,
        *,
        server_name: str = "",
        ca_certificate: bytes = b"",
    ) -> None:
        self.port = port
        self.username = username
        self.password = password
        self.server_name = server_name
        self.context: ssl.SSLContext | None = None
        if server_name:
            require(bool(ca_certificate), "public CA is required")
            self.context = ssl.create_default_context(cadata=ca_certificate.decode("utf-8"))
            self.context.minimum_version = ssl.TLSVersion.TLSv1_2

    def request(
        self,
        method: str,
        path: str,
        body: dict[str, Any] | bytes | None = None,
    ) -> tuple[int, Any]:
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
            require(len(data) <= MAX_RESPONSE_BYTES, "Elasticsearch response exceeded limit")
            if not data:
                decoded: Any = None
            else:
                try:
                    decoded = json.loads(data)
                except json.JSONDecodeError:
                    decoded = None
            return response.status, decoded
        except (OSError, http.client.HTTPException, ssl.SSLError) as error:
            raise AdministrationError("Elasticsearch request failed") from error
        finally:
            connection.close()


class ForwardTarget(Protocol):
    kubectl: str
    context: str
    data_namespace: str
    elasticsearch_service: str


class PortForward:
    def __init__(self, target: ForwardTarget) -> None:
        self.target = target
        self.port = free_port()
        self.process: subprocess.Popen[bytes] | None = None

    def __enter__(self) -> int:
        self.process = subprocess.Popen(
            [
                self.target.kubectl,
                "--context",
                self.target.context,
                "--namespace",
                self.target.data_namespace,
                "port-forward",
                f"service/{self.target.elasticsearch_service}",
                f"{self.port}:9200",
            ],
            env=safe_environment(),
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
        deadline = time.monotonic() + 5
        stable_since = time.monotonic()
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise AdministrationError("Elasticsearch port-forward stopped early")
            if time.monotonic() - stable_since >= 1:
                return self.port
            time.sleep(0.1)
        raise AdministrationError("Elasticsearch port-forward did not become ready")

    def __exit__(self, *_: object) -> None:
        if self.process is None or self.process.poll() is not None:
            return
        os.killpg(self.process.pid, signal.SIGTERM)
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(self.process.pid, signal.SIGKILL)
            self.process.wait(timeout=5)


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def client_for(target: Any, username: str, password: str, port: int) -> ElasticsearchClient:
    ca_certificate = (
        target.secret_value(target.data_namespace, target.public_ca_secret, "ca.crt")
        if target.public_ca_secret
        else b""
    )
    return ElasticsearchClient(
        port,
        username,
        password,
        server_name=target.server_name,
        ca_certificate=ca_certificate,
    )


def require_status(status: int, accepted: set[int], activity: str) -> None:
    require(status in accepted, f"{activity} returned HTTP {status}")
