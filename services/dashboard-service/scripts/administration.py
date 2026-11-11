#!/usr/bin/env python3
"""Dashboard policy adapters for shared guarded administration mechanics."""

from __future__ import annotations

import base64
import json
import os
import re
import secrets
import sys
from pathlib import Path
from typing import Any

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

from scripts.go.service.configuration import service_record  # noqa: E402
from scripts.go.service.tooling_config import (  # noqa: E402
    load_tooling_config,
    validate_catalog_identity,
)
from scripts.go.service.tooling_contracts import (  # noqa: E402
    AdministrationError,
    JSONAdministrationClient,
    OwnedPortForward,
    PortForwardSpec,
    loopback_port,
    require_administration,
)
from scripts.go.service.tooling_process import run  # noqa: E402
from scripts.python.process_runner import CommandError, child_environment  # noqa: E402

PASSWORD_BYTES = 48
ElasticsearchClient = JSONAdministrationClient


def require(condition: object, message: str) -> None:
    require_administration(condition, message)


def safe_environment() -> dict[str, str]:
    overrides = {"KUBECONFIG": os.environ["KUBECONFIG"]} if "KUBECONFIG" in os.environ else None
    return child_environment(
        overrides,
        preferred_executables=(os.environ.get("KUBECTL", "kubectl"),),
    )


def command(arguments: list[str], *, input_text: str | None = None, timeout: int = 30) -> str:
    try:
        return run(
            arguments,
            cwd=ROOT,
            env=safe_environment(),
            input_text=input_text,
            timeout_seconds=timeout,
            teardown_seconds=10,
            activity="Dashboard resource administration",
            capture=True,
        )
    except CommandError as error:
        raise AdministrationError(
            f"command failed: {arguments[0]}; inspect the selected target safely"
        ) from error


class Target:
    """Provide Dashboard-owned Kubernetes assertions over shared transports."""

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


class PortForward(OwnedPortForward):
    """Bind Dashboard target data to the generic owned port-forward lifecycle."""

    def __init__(self, target: Target) -> None:
        record = service_record(ROOT, "dashboard")
        tooling = load_tooling_config(ROOT / "services/dashboard-service/go-service-tooling.json")
        validate_catalog_identity(tooling, "dashboard", record)
        require(tooling.contracts is not None, "tooling contracts are missing")
        require(
            set(tooling.contracts.administration.transports) == {"https", "kubectl", "stdin"},
            "administration transport contract differs",
        )
        super().__init__(
            PortForwardSpec(
                context=target.context,
                namespace=target.data_namespace,
                resource=f"service/{target.elasticsearch_service}",
                local_port=loopback_port(),
                remote_port=9200,
            ),
            cwd=ROOT,
            env=safe_environment(),
            startup_seconds=tooling.contracts.deployment.port_forward_seconds,
            teardown_seconds=5,
            kubectl=target.kubectl,
        )


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
