#!/usr/bin/env python3
"""Guard the Dashboard total-sales Elasticsearch reader and workload Secrets."""

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

from scripts.go.service.configuration import service_record, target_record  # noqa: E402
from scripts.python.process_runner import CommandError, run_command  # noqa: E402

SERVICE = "dashboard"
INDEX = "policy_stats"
IDENTITY = "go_dashboard_total_sales_reader"
OWNER = "issue-287"
MAX_RESPONSE_BYTES = 64 * 1024
PASSWORD_BYTES = 48


class AdministrationError(RuntimeError):
    """The selected target or reader state differs from the accepted contract."""


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
            activity="Dashboard reader administration",
        )
    except CommandError as error:
        raise AdministrationError(
            f"command failed: {arguments[0]}; inspect the selected target safely"
        ) from error
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
        credential = base64.b64encode(f"{self.username}:{self.password}".encode()).decode("ascii")
        headers = {"Authorization": f"Basic {credential}"}
        if payload is not None:
            headers["Content-Type"] = "application/json"
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


class Target:
    """Resolve and guard the exact catalog target before mutation."""

    def __init__(self) -> None:
        record = service_record(ROOT, SERVICE)
        administration = record.get("readerAdministration", {})
        require(
            administration == {"owner": OWNER, "index": INDEX, "role": IDENTITY, "user": IDENTITY},
            "reader administration catalog contract differs",
        )
        self.environment = os.environ.get("DEPLOY_ENV", "")
        require(self.environment in record.get("environments", []), "DEPLOY_ENV is invalid")
        selected = target_record(ROOT, record, self.environment)
        expected = {
            "DEPLOY_CONTEXT": selected["context"],
            "DEPLOY_SVC_NS": selected["namespace"],
            "DEPLOY_DATA_NS": selected["dataNamespace"],
        }
        for name, value in expected.items():
            require(os.environ.get(name, "") == value, f"{name} differs from the catalog")
        reader = selected.get("readerAdministration", {})
        expected_reader = {
            "local-dev": {
                "service": "local-dev-elasticsearch-es-http",
                "adminSecret": "local-dev-elasticsearch-es-elastic-user",
                "workloadSecret": "local-dev-dashboard-elasticsearch",
                "publicCASecret": "",
                "workloadCASecret": "",
                "serverName": "",
            },
            "qa": {
                "service": "qa-elasticsearch-es-http",
                "adminSecret": "qa-elasticsearch-es-elastic-user",
                "workloadSecret": "qa-dashboard-elasticsearch",
                "publicCASecret": "qa-elasticsearch-es-http-certs-public",
                "workloadCASecret": "qa-dashboard-elasticsearch-ca",
                "serverName": "qa-elasticsearch-es-http.qa-data.svc",
            },
        }[self.environment]
        require(reader == expected_reader, "reader target catalog contract differs")
        self.context = selected["context"]
        self.namespace = selected["namespace"]
        self.data_namespace = selected["dataNamespace"]
        self.elasticsearch_service = reader["service"]
        self.admin_secret = reader["adminSecret"]
        self.workload_secret = reader["workloadSecret"]
        self.public_ca_secret = reader["publicCASecret"]
        self.workload_ca_secret = reader["workloadCASecret"]
        self.server_name = reader["serverName"]
        self.kubectl = os.environ.get("KUBECTL", "kubectl")

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
        current = command([self.kubectl, "config", "current-context"]).strip()
        require(current == self.context, "current Kubernetes context differs")
        namespace = self.json("get", "namespace", self.namespace)
        require(
            namespace.get("metadata", {}).get("name") == self.namespace,
            "service namespace differs",
        )
        data_namespace = self.json(
            "get", "namespace", self.data_namespace, namespace=self.data_namespace
        )
        require(
            data_namespace.get("metadata", {}).get("name") == self.data_namespace,
            "data namespace differs",
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
            and any(
                port.get("port") == 9200 and port.get("protocol", "TCP") == "TCP"
                for port in service.get("spec", {}).get("ports", [])
            ),
            "Elasticsearch Service identity differs",
        )

    def secret_value(self, namespace: str, name: str, key: str) -> bytes:
        value = self.json("get", "secret", name, namespace=namespace)
        encoded = value.get("data", {}).get(key)
        require(isinstance(encoded, str) and encoded, f"Secret/{name} is missing {key}")
        try:
            return base64.b64decode(encoded, validate=True)
        except ValueError as error:
            raise AdministrationError(f"Secret/{name} has malformed {key}") from error

    def optional_credentials(self) -> tuple[str, str] | None:
        value = self.json("get", "secret", self.workload_secret, "--ignore-not-found")
        if not value:
            return None
        metadata = value.get("metadata", {})
        data = value.get("data", {})
        require(
            value.get("kind") == "Secret"
            and metadata.get("name") == self.workload_secret
            and metadata.get("namespace") == self.namespace
            and metadata.get("labels", {}).get("insurance-hub.io/owner") == OWNER
            and metadata.get("labels", {}).get("app.kubernetes.io/component")
            == "elasticsearch-reader"
            and value.get("type", "Opaque") == "Opaque"
            and set(data) == {"username", "password"},
            "Dashboard reader Secret identity or keys differ",
        )
        try:
            username = base64.b64decode(data["username"], validate=True).decode("utf-8")
            password = base64.b64decode(data["password"], validate=True).decode("utf-8")
        except (TypeError, ValueError, UnicodeDecodeError) as error:
            raise AdministrationError("Dashboard reader Secret data is malformed") from error
        require(username == IDENTITY, "Dashboard reader Secret username differs")
        require_password(password)
        return username, password

    def apply_secret(self, name: str, data: dict[str, str], component: str) -> None:
        value = {
            "apiVersion": "v1",
            "kind": "Secret",
            "metadata": {
                "name": name,
                "namespace": self.namespace,
                "labels": {
                    "app.kubernetes.io/name": "dashboard-api",
                    "app.kubernetes.io/component": component,
                    "insurance-hub.io/owner": OWNER,
                },
            },
            "type": "Opaque",
            "data": {
                key: base64.b64encode(value.encode("utf-8")).decode("ascii")
                for key, value in data.items()
            },
        }
        self.run(
            "apply",
            "--server-side",
            "--field-manager=dashboard-reader-administration",
            "-f",
            "-",
            input_text=json.dumps(value),
        )

    def public_ca(self) -> bytes:
        if not self.public_ca_secret:
            return b""
        return self.secret_value(self.data_namespace, self.public_ca_secret, "ca.crt")

    def ensure_ca_secret(self, ca_certificate: bytes) -> None:
        if not self.workload_ca_secret:
            require(not ca_certificate, "local-dev must not receive a CA Secret")
            return
        try:
            value = ca_certificate.decode("utf-8")
        except UnicodeDecodeError as error:
            raise AdministrationError("QA public CA is malformed") from error
        require(bool(value), "QA public CA is empty")
        existing = self.json("get", "secret", self.workload_ca_secret, "--ignore-not-found")
        if existing:
            self.validate_ca_secret(existing, ca_certificate)
        self.apply_secret(self.workload_ca_secret, {"ca.crt": value}, "elasticsearch-trust")

    def verify_ca_secret(self, ca_certificate: bytes) -> None:
        if not self.workload_ca_secret:
            require(not ca_certificate, "local-dev must not receive a CA Secret")
            return
        value = self.json("get", "secret", self.workload_ca_secret)
        self.validate_ca_secret(value, ca_certificate)

    def validate_ca_secret(self, value: dict[str, Any], ca_certificate: bytes) -> None:
        metadata = value.get("metadata", {})
        data = value.get("data", {})
        require(
            value.get("kind") == "Secret"
            and metadata.get("name") == self.workload_ca_secret
            and metadata.get("namespace") == self.namespace
            and metadata.get("labels", {}).get("insurance-hub.io/owner") == OWNER
            and metadata.get("labels", {}).get("app.kubernetes.io/component")
            == "elasticsearch-trust"
            and value.get("type", "Opaque") == "Opaque"
            and set(data) == {"ca.crt"},
            "Dashboard Elasticsearch CA Secret identity or keys differ",
        )
        try:
            workload_ca = base64.b64decode(data["ca.crt"], validate=True)
        except (TypeError, ValueError) as error:
            raise AdministrationError("Dashboard Elasticsearch CA Secret is malformed") from error
        require(workload_ca == ca_certificate, "Dashboard Elasticsearch CA differs from ECK")


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


def require_status(status: int, accepted: set[int], action: str) -> None:
    require(status in accepted, f"{action} returned unexpected status {status}")


def require_password(password: str) -> None:
    require(
        bool(re.fullmatch(r"[A-Za-z0-9_-]{64}", password)),
        "Dashboard reader password does not match the accepted encoding",
    )


def new_password() -> str:
    password = secrets.token_urlsafe(PASSWORD_BYTES)
    require_password(password)
    return password


def accepted_role() -> dict[str, Any]:
    return {
        "cluster": [],
        "indices": [
            {
                "names": [INDEX],
                "privileges": ["read", "view_index_metadata"],
                "allow_restricted_indices": False,
            }
        ],
        "applications": [],
        "run_as": [],
        "metadata": {"owner": OWNER},
        "remote_indices": [],
        "remote_cluster": [],
    }


def accepted_user(password: str) -> dict[str, Any]:
    return {
        "password": password,
        "roles": [IDENTITY],
        "full_name": "Dashboard issue-287 total-sales reader",
        "metadata": {"owner": OWNER},
        "enabled": True,
    }


def inspect_index(client: ElasticsearchClient) -> None:
    status, value = client.request("GET", f"/{INDEX}/_mapping")
    require_status(status, {200}, "inspect policy_stats mapping")
    require(isinstance(value, dict) and set(value) == {INDEX}, "policy_stats is not one index")
    properties = value[INDEX].get("mappings", {}).get("properties", {})
    product = properties.get("productCode", {})
    require(
        product.get("type") == "text"
        and product.get("fields", {}).get("keyword", {}).get("type") == "keyword"
        and properties.get("from", {}).get("type") == "date"
        and properties.get("totalPremium", {}).get("type") in {"float", "double"},
        "policy_stats mapping differs from the accepted total-sales contract",
    )
    status, _ = client.request(
        "POST",
        f"/{INDEX}/_search",
        {
            "size": 0,
            "query": {
                "bool": {"filter": [{"range": {"from": {"gte": "2000-01-01", "lt": "2000-01-02"}}}]}
            },
            "aggs": {
                "per_product": {"terms": {"field": "productCode.keyword", "size": 100}},
                "total_premium": {"sum": {"field": "totalPremium"}},
            },
        },
    )
    require_status(status, {200}, "run policy_stats compatibility query")


def inspect_identity(
    client: ElasticsearchClient,
) -> tuple[dict[str, Any] | None, dict[str, Any] | None]:
    status, value = client.request("GET", f"/_security/role/{IDENTITY}")
    require_status(status, {200, 404}, "inspect Dashboard reader role")
    role = value.get(IDENTITY) if status == 200 and isinstance(value, dict) else None
    status, value = client.request("GET", f"/_security/user/{IDENTITY}")
    require_status(status, {200, 404}, "inspect Dashboard reader user")
    user = value.get(IDENTITY) if status == 200 and isinstance(value, dict) else None
    for item, label in ((role, "role"), (user, "user")):
        if item is not None:
            require(
                isinstance(item, dict) and item.get("metadata") == {"owner": OWNER},
                f"existing Dashboard reader {label} is not owned by {OWNER}",
            )
    return role, user


def validate_role(role: dict[str, Any]) -> None:
    expected = accepted_role()
    normalized = dict(role)
    transient = normalized.pop("transient_metadata", {})
    normalized.setdefault("remote_indices", [])
    normalized.setdefault("remote_cluster", [])
    require(
        normalized == expected and transient in ({}, {"enabled": True}),
        "Dashboard reader role differs",
    )


def validate_user(user: dict[str, Any]) -> None:
    require(
        user.get("metadata") == {"owner": OWNER}
        and user.get("roles") == [IDENTITY]
        and user.get("full_name") == "Dashboard issue-287 total-sales reader"
        and user.get("enabled") is True,
        "Dashboard reader user differs",
    )


def verify_stored_identity(client: ElasticsearchClient) -> None:
    role, user = inspect_identity(client)
    require(role is not None, "Dashboard reader role is absent")
    require(user is not None, "Dashboard reader user is absent")
    validate_role(role)
    validate_user(user)


def put_identity(client: ElasticsearchClient, password: str) -> None:
    status, _ = client.request("PUT", f"/_security/role/{IDENTITY}", accepted_role())
    require_status(status, {200}, "apply Dashboard reader role")
    status, _ = client.request("PUT", f"/_security/user/{IDENTITY}", accepted_user(password))
    require_status(status, {200}, "apply Dashboard reader user")
    verify_stored_identity(client)


def verify_effective_privileges(client: ElasticsearchClient) -> None:
    status, value = client.request("GET", "/_security/user/_privileges")
    require_status(status, {200}, "inspect Dashboard reader effective privileges")
    require(isinstance(value, dict), "Dashboard reader effective privileges are malformed")
    normalized = dict(value)
    normalized.setdefault("global", [])
    normalized.setdefault("applications", [])
    normalized.setdefault("run_as", [])
    normalized.setdefault("remote_indices", [])
    normalized.setdefault("remote_cluster", [])
    expected = accepted_role()
    expected.pop("metadata")
    expected["global"] = []
    require(normalized == expected, "Dashboard reader effective privileges differ")


def client_for(
    target: Target,
    username: str,
    password: str,
    port: int,
    ca_certificate: bytes,
) -> ElasticsearchClient:
    return ElasticsearchClient(
        port,
        username,
        password,
        server_name=target.server_name,
        ca_certificate=ca_certificate,
    )


def verify_reader(
    target: Target,
    port: int,
    ca_certificate: bytes,
    credentials: tuple[str, str],
) -> None:
    username, password = credentials
    reader = client_for(target, username, password, port, ca_certificate)
    verify_effective_privileges(reader)
    inspect_index(reader)
    target.verify_ca_secret(ca_certificate)


def ensure_reader(
    target: Target,
    admin: ElasticsearchClient,
    port: int,
    ca_certificate: bytes,
) -> None:
    role, user = inspect_identity(admin)
    credentials = target.optional_credentials()
    if credentials is None:
        require(
            role is None and user is None,
            "reader identity exists without its owned Secret; use explicit rotation recovery",
        )
        password = new_password()
    else:
        password = credentials[1]
    target.ensure_ca_secret(ca_certificate)
    put_identity(admin, password)
    target.apply_secret(
        target.workload_secret,
        {"username": IDENTITY, "password": password},
        "elasticsearch-reader",
    )
    verify_reader(target, port, ca_certificate, (IDENTITY, password))


def rotate_reader(
    target: Target,
    admin: ElasticsearchClient,
    port: int,
    ca_certificate: bytes,
) -> None:
    inspect_identity(admin)
    previous = target.optional_credentials()
    password = new_password()
    target.ensure_ca_secret(ca_certificate)
    put_identity(admin, password)
    try:
        target.apply_secret(
            target.workload_secret,
            {"username": IDENTITY, "password": password},
            "elasticsearch-reader",
        )
        verify_reader(target, port, ca_certificate, (IDENTITY, password))
    except BaseException:
        if previous is not None:
            try:
                put_identity(admin, previous[1])
                target.apply_secret(
                    target.workload_secret,
                    {"username": IDENTITY, "password": previous[1]},
                    "elasticsearch-reader",
                )
            except BaseException as rollback_error:
                raise AdministrationError(
                    "reader rotation failed and previous credential rollback failed"
                ) from rollback_error
        raise


def execute(operation: str) -> None:
    require(operation in {"ensure", "verify", "rotate"}, "unsupported reader operation")
    if operation != "verify":
        environment = os.environ.get("DEPLOY_ENV", "")
        require(environment in {"local-dev", "qa"}, "DEPLOY_ENV is invalid")
        expected = f"{operation}-{environment}-dashboard-reader"
        require(
            os.environ.get("DASHBOARD_READER_CONFIRM") == expected,
            f"DASHBOARD_READER_CONFIRM must equal {expected}",
        )
    target = Target()
    target.verify_boundary()
    ca_certificate = target.public_ca()
    admin_password = target.secret_value(
        target.data_namespace, target.admin_secret, "elastic"
    ).decode("utf-8")
    with PortForward(target) as port:
        admin = client_for(target, "elastic", admin_password, port, ca_certificate)
        inspect_index(admin)
        if operation == "ensure":
            ensure_reader(target, admin, port, ca_certificate)
        elif operation == "rotate":
            rotate_reader(target, admin, port, ca_certificate)
        else:
            verify_stored_identity(admin)
            credentials = target.optional_credentials()
            require(credentials is not None, "Dashboard reader Secret is absent")
            verify_reader(target, port, ca_certificate, credentials)
    print(
        json.dumps(
            {
                "environment": target.environment,
                "index": INDEX,
                "operation": operation,
                "reader": IDENTITY,
                "status": "passed",
            },
            sort_keys=True,
        )
    )


def cli(argv: list[str] | None = None) -> int:
    arguments = sys.argv[1:] if argv is None else argv
    try:
        require(len(arguments) == 1, "one reader operation is required")
        execute(arguments[0])
    except KeyboardInterrupt:
        return 130
    except (AdministrationError, CommandError, OSError, UnicodeDecodeError, ValueError) as error:
        print(f"dashboard-reader: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(cli())
