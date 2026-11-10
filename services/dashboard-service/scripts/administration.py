#!/usr/bin/env python3
"""Shared guarded administration primitives for Dashboard resources."""

from __future__ import annotations

import base64
import http.client
import json
import os
import re
import secrets
import signal
import socket
import ssl
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

from scripts.python.process_runner import CommandError, run_command  # noqa: E402

MAX_RESPONSE_BYTES = 64 * 1024
PASSWORD_BYTES = 48


class AdministrationError(RuntimeError):
    """The selected target or resource state differs from the accepted contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise AdministrationError(message)


def safe_environment() -> dict[str, str]:
    allowed = ("HOME", "KUBECONFIG", "PATH", "SSL_CERT_DIR", "SSL_CERT_FILE", "TMPDIR")
    return {name: os.environ[name] for name in allowed if name in os.environ}


def command(arguments: list[str], *, input_text: str | None = None, timeout: int = 30) -> str:
    try:
        result = run_command(
            arguments,
            cwd=ROOT,
            env=safe_environment(),
            input_text=input_text,
            timeout_seconds=timeout,
            teardown_seconds=10,
            activity="Dashboard resource administration",
        )
    except CommandError as error:
        raise AdministrationError(
            f"command failed: {arguments[0]}; inspect the selected target safely"
        ) from error
    return result.stdout


class ForwardedHTTPSConnection(http.client.HTTPSConnection):
    def __init__(self, server_name: str, port: int, context: ssl.SSLContext) -> None:
        super().__init__(server_name, 443, context=context, timeout=10)
        self.forwarded_port = port

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
            require(bool(ca_certificate), "QA public CA is required")
            try:
                self.context = ssl.create_default_context(cadata=ca_certificate.decode("utf-8"))
            except (UnicodeDecodeError, ssl.SSLError) as error:
                raise AdministrationError("QA public CA is malformed") from error
            self.context.minimum_version = ssl.TLSVersion.TLSv1_2

    def request(
        self,
        method: str,
        path: str,
        body: dict[str, Any] | None = None,
    ) -> tuple[int, Any]:
        payload = None if body is None else json.dumps(body, separators=(",", ":")).encode()
        token = base64.b64encode(f"{self.username}:{self.password}".encode()).decode("ascii")
        headers = {"Authorization": f"Basic {token}"}
        if payload is not None:
            headers["Content-Type"] = "application/json"
        connection: http.client.HTTPConnection
        if self.context is None:
            connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=10)
        else:
            connection = ForwardedHTTPSConnection(self.server_name, self.port, self.context)
        try:
            connection.request(method, path, body=payload, headers=headers)
            response = connection.getresponse()
            data = response.read(MAX_RESPONSE_BYTES + 1)
            require(len(data) <= MAX_RESPONSE_BYTES, "Elasticsearch response exceeded limit")
            try:
                decoded: Any = json.loads(data) if data else None
            except json.JSONDecodeError:
                decoded = None
            return response.status, decoded
        except (OSError, http.client.HTTPException, ssl.SSLError) as error:
            raise AdministrationError("Elasticsearch request failed") from error
        finally:
            connection.close()


class Target:
    """Provide guarded Kubernetes operations for an exact catalog target."""

    def run(
        self,
        *arguments: str,
        namespace: str | None = None,
        input_text: str | None = None,
    ) -> str:
        return command(
            [
                self.kubectl,
                "--context",
                self.context,
                "--namespace",
                namespace or self.namespace,
                *arguments,
            ],
            input_text=input_text,
        )

    def json(self, *arguments: str, namespace: str | None = None) -> dict[str, Any]:
        try:
            value = json.loads(self.run(*arguments, "-o", "json", namespace=namespace) or "{}")
        except json.JSONDecodeError as error:
            raise AdministrationError("kubectl returned malformed JSON") from error
        require(isinstance(value, dict), "kubectl returned unexpected JSON")
        return value

    def verify_boundary(self) -> None:
        require(
            command([self.kubectl, "config", "current-context"]).strip() == self.context,
            "current Kubernetes context differs",
        )
        service = self.json(
            "get", "service", self.elasticsearch_service, namespace=self.data_namespace
        )
        require(
            service.get("metadata", {}).get("name") == self.elasticsearch_service
            and service.get("metadata", {}).get("namespace") == self.data_namespace
            and service.get("spec", {}).get("type") == "ClusterIP"
            and service.get("spec", {})
            .get("selector", {})
            .get("elasticsearch.k8s.elastic.co/cluster-name")
            == f"{self.environment}-elasticsearch"
            and any(item.get("port") == 9200 for item in service.get("spec", {}).get("ports", [])),
            "Elasticsearch target identity differs",
        )
        kafka = self.json("get", "kafka", self.kafka_cluster, namespace=self.data_namespace)
        listeners = kafka.get("spec", {}).get("kafka", {}).get("listeners", [])
        require(
            kafka.get("metadata", {}).get("name") == self.kafka_cluster
            and kafka.get("metadata", {}).get("namespace") == self.data_namespace
            and any(
                item.get("name") == "tls" and item.get("port") == 9093 and item.get("tls") is True
                for item in listeners
            ),
            "Kafka TLS target identity differs",
        )
        bootstrap = self.json(
            "get", "service", f"{self.kafka_cluster}-kafka-bootstrap", namespace=self.data_namespace
        )
        require(
            bootstrap.get("spec", {}).get("selector", {}).get("strimzi.io/cluster")
            == self.kafka_cluster
            and any(
                item.get("port") == 9093 for item in bootstrap.get("spec", {}).get("ports", [])
            ),
            "Kafka bootstrap Service differs",
        )

    def secret_value(self, namespace: str, name: str, key: str) -> bytes:
        encoded = self.json("get", "secret", name, namespace=namespace).get("data", {}).get(key)
        require(isinstance(encoded, str) and encoded, f"Secret/{name} is missing {key}")
        try:
            return base64.b64decode(encoded, validate=True)
        except ValueError as error:
            raise AdministrationError(f"Secret/{name} has malformed {key}") from error


class PortForward:
    def __init__(self, target: Target) -> None:
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
        try:
            deadline = time.monotonic() + 10
            stable_since = time.monotonic()
            while time.monotonic() < deadline:
                if self.process.poll() is not None:
                    raise AdministrationError("Elasticsearch port-forward stopped early")
                if time.monotonic() - stable_since >= 1:
                    return self.port
                time.sleep(0.1)
            raise AdministrationError("Elasticsearch port-forward did not become ready")
        except BaseException:
            self._stop()
            raise

    def __exit__(self, *_: object) -> None:
        self._stop()

    def _stop(self) -> None:
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


def require_password(password: str) -> None:
    require(bool(re.fullmatch(r"[A-Za-z0-9_-]{64}", password)), "password format differs")


def new_password() -> str:
    value = secrets.token_urlsafe(PASSWORD_BYTES)
    require_password(value)
    return value


def normalize_role(value: dict[str, Any]) -> dict[str, Any]:
    normalized = dict(value)
    normalized.pop("transient_metadata", None)
    normalized.setdefault("remote_indices", [])
    normalized.setdefault("remote_cluster", [])
    return normalized


def client_for(
    target: Target, username: str, password: str, port: int, certificate: bytes
) -> ElasticsearchClient:
    return ElasticsearchClient(
        port,
        username,
        password,
        server_name=target.server_name,
        ca_certificate=certificate,
    )
