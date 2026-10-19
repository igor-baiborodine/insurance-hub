#!/usr/bin/env python3

"""Validate the active Product Go Kubernetes renders without cluster access."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any


class ValidationError(Exception):
    """A rendered deployment contract was violated."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValidationError(message)


def load_documents(path: Path) -> list[dict[str, Any]]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ValidationError(f"cannot read rendered JSON {path}: {error}") from error
    require(isinstance(value, list), f"rendered JSON {path} must be an array")
    require(all(isinstance(item, dict) for item in value), f"rendered JSON {path} has a non-object item")
    return value


def identity(document: dict[str, Any]) -> tuple[str, str, str]:
    metadata = document.get("metadata", {})
    return document.get("kind", ""), metadata.get("namespace", ""), metadata.get("name", "")


def require_unique(documents: list[dict[str, Any]], description: str) -> None:
    identities = [identity(document) for document in documents]
    require(len(identities) == len(set(identities)), f"{description} contains duplicate object identities")


def one(
    documents: list[dict[str, Any]],
    kind: str,
    name: str | re.Pattern[str],
) -> dict[str, Any]:
    matches = []
    for document in documents:
        object_kind, _, object_name = identity(document)
        name_matches = object_name == name if isinstance(name, str) else name.fullmatch(object_name)
        if object_kind == kind and name_matches:
            matches.append(document)
    require(len(matches) == 1, f"expected exactly one {kind} named {getattr(name, 'pattern', name)}")
    return matches[0]


def selected(selector: dict[str, str], labels: dict[str, str]) -> bool:
    return all(labels.get(key) == value for key, value in selector.items())


EXPECTED_CONFIG = {
    "SERVICE_NAME": "product-service",
    "HTTP_ADDR": "0.0.0.0:8081",
    "GRPC_ADDR": "0.0.0.0:9090",
    "HEALTH_ADDR": "0.0.0.0:8080",
    "DB_MAX_CONNS": "8",
    "DB_CONNECT_TIMEOUT": "3s",
    "DB_ACQUIRE_TIMEOUT": "2s",
    "DB_QUERY_TIMEOUT": "5s",
    "STARTUP_TIMEOUT": "10s",
    "REQUEST_TIMEOUT": "8s",
    "PROBE_TIMEOUT": "2s",
    "HTTP_READ_HEADER_TIMEOUT": "5s",
    "HTTP_READ_TIMEOUT": "10s",
    "HTTP_WRITE_TIMEOUT": "10s",
    "HTTP_IDLE_TIMEOUT": "30s",
    "GRPC_MAX_RECV_BYTES": "1048576",
    "GRPC_MAX_SEND_BYTES": "8388608",
    "SHUTDOWN_TIMEOUT": "10s",
    "LOG_LEVEL": "info",
    "OTEL_ENABLED": "false",
}

QA_SERVICE_VERSION = "sha-be510cc"
QA_CONFIG = {
    **EXPECTED_CONFIG,
    "OTEL_ENABLED": "true",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "http://qa-alloy.qa-monitoring.svc.cluster.local:4317",
    "OTEL_EXPORTER_OTLP_TIMEOUT": "2s",
    "DEPLOYMENT_ENVIRONMENT": "qa",
    "SERVICE_VERSION": QA_SERVICE_VERSION,
}


def validate_service(
    service: dict[str, Any],
    expected_ports: list[dict[str, Any]],
    deployment_selector: dict[str, str],
) -> None:
    spec = service.get("spec", {})
    name = service.get("metadata", {}).get("name", "")
    require(spec.get("type") == "ClusterIP", f"Service {name} must remain ClusterIP")
    for forbidden in ("externalIPs", "externalName", "loadBalancerClass", "loadBalancerIP"):
        require(forbidden not in spec, f"Service {name} must not set {forbidden}")
    require(spec.get("selector") == deployment_selector, f"Service {name} selector must equal Deployment selector")
    require(spec.get("ports") == expected_ports, f"Service {name} ports do not match the Product contract")
    require(all("nodePort" not in port for port in spec.get("ports", [])), f"Service {name} must not use nodePort")


def validate_probe(container: dict[str, Any], name: str, path: str, period: int, failures: int) -> None:
    probe = container.get(name, {})
    require(
        probe.get("httpGet") == {"path": path, "port": "management", "scheme": "HTTP"},
        f"{name} must use {path} on the named management port",
    )
    require(probe.get("periodSeconds") == period, f"{name} periodSeconds is incorrect")
    require(probe.get("timeoutSeconds") == 3, f"{name} timeoutSeconds must exceed PROBE_TIMEOUT=2s")
    require(probe.get("failureThreshold") == failures, f"{name} failureThreshold is incorrect")


def validate_go_environment(
    documents: list[dict[str, Any]], environment: str
) -> dict[tuple[str, str, str], dict[str, Any]]:
    settings = {
        "local-dev": {
            "prefix": "local-dev-",
            "namespace": "local-dev-all",
            "secret": "local-dev-postgres-product-reader-creds",
            "image": re.compile(r"insurance-hub-product-api:git-[0-9a-f]{12}"),
            "pull": "Never",
        },
        "qa": {
            "prefix": "qa-",
            "namespace": "qa-svc",
            "secret": "qa-postgres-product-reader-creds",
            "image": re.compile(
                r"ghcr\.io/igor-baiborodine/insurance-hub/product-api:"
                r"1\.[0-9]+\.[0-9]+"
            ),
            "pull": "Always",
        },
    }[environment]
    prefix = settings["prefix"]
    namespace = settings["namespace"]
    secret_name = settings["secret"]

    require_unique(documents, f"{environment} Go render")
    require(len(documents) == 4, f"{environment} Go render must contain exactly four objects")
    require(
        all(identity(item)[1] == namespace for item in documents),
        f"{environment} namespace transformation is incorrect",
    )
    require(
        not any(item.get("kind") == "Secret" for item in documents),
        f"{environment} render must reference, not generate, the reader Secret",
    )
    forbidden_kinds = {"Ingress", "Gateway", "HTTPRoute", "GRPCRoute"}
    require(
        not any(item.get("kind") in forbidden_kinds for item in documents),
        f"{environment} Go render contains an external route",
    )
    require(
        not any("legacy" in identity(item)[2] for item in documents),
        f"{environment} Go render includes a legacy identity",
    )

    deployment = one(documents, "Deployment", f"{prefix}product-api")
    business = one(documents, "Service", f"{prefix}product-api")
    management = one(documents, "Service", f"{prefix}product-api-management")
    config = one(
        documents,
        "ConfigMap",
        re.compile(rf"{re.escape(prefix)}product-config-[a-z0-9]{{10}}"),
    )
    expected_config = QA_CONFIG if environment == "qa" else EXPECTED_CONFIG
    require(
        config.get("data") == expected_config,
        f"{environment} Product ConfigMap is incomplete or unexpected",
    )

    deployment_spec = deployment.get("spec", {})
    selector = deployment_spec.get("selector", {}).get("matchLabels", {})
    pod = deployment_spec.get("template", {})
    pod_metadata = pod.get("metadata", {})
    pod_spec = pod.get("spec", {})
    pod_labels = pod_metadata.get("labels", {})
    required_labels = {
        "app.kubernetes.io/name": "product-api",
        "app.kubernetes.io/component": "api",
        "environment": environment,
    }
    require(
        all(selector.get(key) == value for key, value in required_labels.items()),
        f"{environment} Deployment selector is incorrect",
    )
    require(
        selected(selector, pod_labels),
        f"{environment} Deployment selector does not select its pod template",
    )
    if environment == "qa":
        require(
            deployment.get("metadata", {}).get("labels", {}).get("app.kubernetes.io/version")
            == QA_SERVICE_VERSION,
            "qa Deployment version label must match the immutable Product source revision",
        )
        require(
            pod_labels.get("app.kubernetes.io/version") == QA_SERVICE_VERSION,
            "qa Pod version label must match the immutable Product source revision",
        )
    require(deployment_spec.get("replicas") == 1, f"{environment} replicas must be 1")
    require(deployment_spec.get("minReadySeconds") == 5, f"{environment} minReadySeconds must be 5")
    require(deployment_spec.get("progressDeadlineSeconds") == 180, f"{environment} progress deadline must be 180s")
    require(
        deployment_spec.get("strategy")
        == {"type": "RollingUpdate", "rollingUpdate": {"maxSurge": 1, "maxUnavailable": 0}},
        f"{environment} rollout strategy is incorrect",
    )

    validate_service(
        business,
        [
            {"name": "http", "port": 8081, "protocol": "TCP", "targetPort": "http"},
            {"name": "grpc", "port": 9090, "protocol": "TCP", "targetPort": "grpc"},
        ],
        selector,
    )
    validate_service(
        management,
        [{"name": "management", "port": 8080, "protocol": "TCP", "targetPort": "management"}],
        selector,
    )

    require(
        pod_spec.get("automountServiceAccountToken") is False,
        f"{environment} must disable API token mounting",
    )
    require(
        pod_spec.get("enableServiceLinks") is False,
        f"{environment} must disable Service environment links",
    )
    require(
        pod_spec.get("terminationGracePeriodSeconds") == 30,
        f"{environment} termination grace must be 30s",
    )
    for forbidden in ("hostNetwork", "hostPID", "hostIPC"):
        require(not pod_spec.get(forbidden, False), f"{environment} must not enable {forbidden}")
    require(
        not pod_spec.get("initContainers"),
        f"{environment} must not add an administrative init container",
    )
    require(
        pod_spec.get("securityContext")
        == {
            "runAsNonRoot": True,
            "runAsUser": 65532,
            "runAsGroup": 65532,
            "fsGroup": 65532,
            "seccompProfile": {"type": "RuntimeDefault"},
        },
        f"{environment} pod securityContext is incorrect",
    )
    containers = pod_spec.get("containers", [])
    require(
        len(containers) == 1 and containers[0].get("name") == "product-api",
        f"{environment} must contain only the Product runtime container",
    )
    container = containers[0]
    require(
        settings["image"].fullmatch(container.get("image", "")) is not None,
        f"{environment} image reference is invalid",
    )
    require("legacy" not in container.get("image", ""), f"{environment} image must be suffix-free")
    require(
        container.get("imagePullPolicy") == settings["pull"],
        f"{environment} imagePullPolicy is incorrect",
    )
    require(
        container.get("ports")
        == [
            {"containerPort": 8081, "name": "http", "protocol": "TCP"},
            {"containerPort": 9090, "name": "grpc", "protocol": "TCP"},
            {"containerPort": 8080, "name": "management", "protocol": "TCP"},
        ],
        f"{environment} named container ports are incorrect",
    )
    require(
        all("hostPort" not in port for port in container.get("ports", [])),
        f"{environment} must not use host ports",
    )
    require(
        container.get("securityContext")
        == {
            "allowPrivilegeEscalation": False,
            "readOnlyRootFilesystem": True,
            "capabilities": {"drop": ["ALL"]},
        },
        f"{environment} container securityContext is incorrect",
    )
    require(
        container.get("resources")
        == {
            "requests": {"cpu": "100m", "memory": "64Mi"},
            "limits": {"cpu": "300m", "memory": "256Mi"},
        },
        f"{environment} resource budget is incorrect",
    )
    require(
        container.get("envFrom") == [{"configMapRef": {"name": config["metadata"]["name"]}}],
        f"{environment} Deployment must reference the transformed Product ConfigMap",
    )
    expected_environment = [
        {
            "name": "PRODUCT_DATABASE_URL",
            "valueFrom": {"secretKeyRef": {"name": secret_name, "key": "database-url"}},
        }
    ]
    if environment == "qa":
        expected_environment.append(
            {
                "name": "SERVICE_INSTANCE_ID",
                "valueFrom": {"fieldRef": {"fieldPath": "metadata.uid"}},
            }
        )
    require(
        container.get("env") == expected_environment,
        f"{environment} PRODUCT_DATABASE_URL reader Secret reference is incorrect",
    )
    require(
        container.get("volumeMounts")
        == [{"name": "postgres-ca", "mountPath": "/etc/product-service/postgres", "readOnly": True}],
        f"{environment} CA mount is incorrect",
    )
    require(
        pod_spec.get("volumes")
        == [
            {
                "name": "postgres-ca",
                "secret": {"secretName": secret_name, "items": [{"key": "ca.crt", "path": "ca.crt"}]},
            }
        ],
        f"{environment} reader Secret CA reference is incorrect",
    )
    expected_credential_revision = "rotation-20261013T1450Z" if environment == "local-dev" else "1"
    require(
        pod_metadata.get("annotations", {}).get("insurance-hub.io/product-reader-credential-revision")
        == expected_credential_revision,
        f"{environment} credential rollout revision is missing",
    )
    validate_probe(container, "startupProbe", "/livez", 2, 30)
    validate_probe(container, "livenessProbe", "/livez", 10, 3)
    validate_probe(container, "readinessProbe", "/readyz", 5, 2)

    return {identity(document): document for document in documents}


def validate_disjoint(
    go_documents: list[dict[str, Any]], legacy_documents: list[dict[str, Any]], environment: str
) -> None:
    prefix = f"{environment}-"
    go_deployment = one(go_documents, "Deployment", f"{prefix}product-api")
    go_service = one(go_documents, "Service", f"{prefix}product-api")
    legacy_deployment = one(legacy_documents, "Deployment", f"{prefix}product-api-legacy")
    legacy_service = one(legacy_documents, "Service", f"{prefix}product-api-legacy")
    go_labels = go_deployment["spec"]["template"]["metadata"]["labels"]
    legacy_labels = legacy_deployment["spec"]["template"]["metadata"]["labels"]
    go_selector = go_service["spec"]["selector"]
    legacy_selector = legacy_service["spec"]["selector"]
    require(not selected(go_selector, legacy_labels), f"{environment} Go Service selector overlaps the Java pod")
    require(not selected(legacy_selector, go_labels), f"{environment} Java Service selector overlaps the Go pod")


def contains_qa_go(documents: list[dict[str, Any]]) -> bool:
    names = {identity(document) for document in documents}
    return ("Deployment", "qa-svc", "qa-product-api") in names


def validate_qa_java(deployment: dict[str, Any], container_name: str, version: str) -> None:
    metadata = deployment.get("metadata", {})
    pod = deployment.get("spec", {}).get("template", {})
    require(
        metadata.get("labels", {}).get("app.kubernetes.io/version") == version,
        f"{container_name} Deployment version label must match its QA image",
    )
    require(
        pod.get("metadata", {}).get("labels", {}).get("app.kubernetes.io/version") == version,
        f"{container_name} Pod version label must match its QA image",
    )
    containers = pod.get("spec", {}).get("containers", [])
    matches = [container for container in containers if container.get("name") == container_name]
    require(len(matches) == 1, f"expected exactly one {container_name} container")
    container = matches[0]
    require(
        container.get("image")
        == f"ghcr.io/igor-baiborodine/insurance-hub/{container_name}:{version}",
        f"{container_name} image and version label must match",
    )
    environment = container.get("env", [])
    require(
        len({item.get("name") for item in environment}) == len(environment),
        f"{container_name} environment must not contain duplicate names",
    )
    values = {item.get("name"): item.get("value") for item in environment}
    require(
        values.get("TRACING_ZIPKIN_SAMPLER_PROBABILITY") == "1.0",
        f"{container_name} must bind the QA Zipkin sampler to 1.0",
    )


def validate_qa_java_observability(
    qa_legacy: list[dict[str, Any]], aggregate: list[dict[str, Any]]
) -> None:
    product = one(qa_legacy, "Deployment", "qa-product-api-legacy")
    validate_qa_java(product, "product-api-legacy", "1.6.0")
    product_config = one(
        qa_legacy,
        "ConfigMap",
        re.compile(r"qa-product-config-legacy-[a-z0-9]{10}"),
    )
    require(
        product_config.get("data", {}).get("TRACING_ZIPKIN_ENABLED") == "true"
        and product_config.get("data", {}).get("ZIPKIN_HOST")
        == "qa-alloy.qa-monitoring.svc.cluster.local",
        "product-api-legacy must preserve the Alloy Zipkin destination",
    )

    gateway = one(aggregate, "Deployment", "qa-agent-portal-gateway-legacy")
    validate_qa_java(gateway, "agent-portal-gateway-legacy", "1.3.0")
    gateway_container = gateway["spec"]["template"]["spec"]["containers"][0]
    gateway_values = {
        item.get("name"): item.get("value") for item in gateway_container.get("env", [])
    }
    require(
        gateway_values.get("TRACING_ZIPKIN_HTTP_URL")
        == "http://qa-alloy.qa-monitoring.svc.cluster.local:9411",
        "agent-portal-gateway-legacy must preserve the Alloy Zipkin destination",
    )


def validate_aggregates(
    qa_go_index: dict[tuple[str, str, str], dict[str, Any]],
    aggregate: list[dict[str, Any]],
) -> None:
    require_unique(aggregate, "QA aggregate")
    java = one(aggregate, "Deployment", "qa-product-api-legacy")
    state = java.get("metadata", {}).get("labels", {}).get(
        "insurance-hub.io/product-go-state", "active"
    )
    require(state in {"active", "stopped"}, "QA aggregate has an invalid Product Go state")
    aggregate_index = {identity(document): document for document in aggregate}
    if state == "stopped":
        require(
            not contains_qa_go(aggregate)
            and not any(object_identity in aggregate_index for object_identity in qa_go_index),
            "stopped QA aggregate still contains Go Product objects",
        )
        return

    require(contains_qa_go(aggregate), "active QA aggregate does not contain Go Product")
    for object_identity, expected in qa_go_index.items():
        require(
            aggregate_index.get(object_identity) == expected,
            f"active QA aggregate changed Go object {object_identity}",
        )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--local-go", required=True, type=Path)
    parser.add_argument("--local-legacy", required=True, type=Path)
    parser.add_argument("--qa-go", required=True, type=Path)
    parser.add_argument("--qa-legacy", required=True, type=Path)
    parser.add_argument("--qa-aggregate", required=True, type=Path)
    return parser.parse_args()


def main() -> int:
    arguments = parse_args()
    try:
        local_go = load_documents(arguments.local_go)
        local_legacy = load_documents(arguments.local_legacy)
        qa_go = load_documents(arguments.qa_go)
        qa_legacy = load_documents(arguments.qa_legacy)
        qa_aggregate = load_documents(arguments.qa_aggregate)
        validate_go_environment(local_go, "local-dev")
        qa_go_index = validate_go_environment(qa_go, "qa")
        validate_disjoint(local_go, local_legacy, "local-dev")
        validate_disjoint(qa_go, qa_legacy, "qa")
        validate_qa_java_observability(qa_legacy, qa_aggregate)
        validate_aggregates(qa_go_index, qa_aggregate)
    except ValidationError as error:
        print(f"product-deploy-check: {error}", file=sys.stderr)
        return 1
    print("product-deploy-check: rendered overlays, Java isolation, and QA lifecycle state passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
