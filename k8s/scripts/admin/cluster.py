"""Kubernetes-owned administrative workflows used by the public Make targets."""

from __future__ import annotations

import base64
import json
import os
import shlex
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
from collections.abc import Mapping, Sequence
from pathlib import Path

from runtime import (
    REPOSITORY_ROOT,
    AdminError,
    CommandError,
    Commands,
    Kubernetes,
    Target,
    require,
    safe_name,
)

OPERATOR_TIMEOUT = 600
ZIPKIN_SETUP_IMAGE = "curlimages/curl:8.16.0"

OPERATOR_SPECS = {
    "postgres": {
        "apply": [
            "kubectl",
            "apply",
            "--server-side",
            "-f",
            "https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v1.27.0/cnpg-1.27.0.yaml",
        ],
        "crds": ["clusters.postgresql.cnpg.io"],
        "resources": ["clusters.postgresql.cnpg.io"],
        "rollout": ("cnpg-system", "cnpg-controller-manager"),
    },
    "eck": {
        "apply": [
            "kubectl",
            "apply",
            "-f",
            "https://download.elastic.co/downloads/eck/3.2.0/crds.yaml",
            "-f",
            "https://download.elastic.co/downloads/eck/3.2.0/operator.yaml",
        ],
        "crds": ["elasticsearches.elasticsearch.k8s.elastic.co"],
        "resources": ["elasticsearches.elasticsearch.k8s.elastic.co"],
        "rollout": ("elastic-system", "elastic-operator"),
    },
    "minio": {
        "apply": ["kubectl", "apply", "--server-side", "-k", "{overlay}/infra/minio/operator"],
        "crds": ["tenants.minio.min.io"],
        "resources": ["tenants.minio.min.io"],
        "rollout": ("minio-operator", "minio-operator"),
    },
}


def _secret_payload(
    namespace: str, name: str, values: Mapping[str, str], kind: str = "Opaque"
) -> dict:
    require(values and all(values.values()), f"Secret {namespace}/{name} values must be non-empty")
    return {
        "apiVersion": "v1",
        "kind": "Secret",
        "type": kind,
        "metadata": {"name": name, "namespace": namespace},
        "data": {key: base64.b64encode(value.encode()).decode() for key, value in values.items()},
    }


def _single_line_secret(value: str, label: str) -> str:
    require(
        value and "\n" not in value and "\r" not in value,
        f"{label} must be non-empty and single-line",
    )
    return value


def _copy_secret_payload(source: dict, namespace: str) -> dict:
    require(source.get("apiVersion") == "v1" and source.get("kind") == "Secret", "invalid Secret")
    metadata = source.get("metadata", {})
    name = metadata.get("name", "")
    safe_name(name, "Secret name")
    return {
        "apiVersion": "v1",
        "kind": "Secret",
        "type": source.get("type", "Opaque"),
        "metadata": {"name": name, "namespace": namespace},
        "data": dict(source.get("data", {})),
    }


def guard(kube: Kubernetes, kind: str, service: str = "") -> None:
    if kind == "service-name":
        safe_name(service, "SVC_NAME")
        return
    kube.verify_context()
    namespaces = {
        "data": kube.target.data_namespace,
        "service": kube.target.service_namespace,
        "monitoring": kube.target.monitoring_namespace,
        "flux": kube.target.flux_namespace,
    }
    if kind == "environment":
        return
    require(kind in namespaces, f"unknown guard {kind}")
    namespace = namespaces[kind]
    require(
        namespace != "INVALID", f"{kind} namespace is unavailable for {kube.target.environment}"
    )
    safe_name(namespace, f"{kind} namespace")


def _operator_command(kube: Kubernetes, arguments: Sequence[str], activity: str) -> None:
    command = list(arguments)
    if command[0] == "kubectl":
        command[1:1] = ["--context", kube.target.context]
    kube.commands.run(command, timeout=OPERATOR_TIMEOUT, activity=activity)


def deploy_operator(kube: Kubernetes, operator: str) -> None:
    kube.verify_context()
    if operator in OPERATOR_SPECS:
        spec = OPERATOR_SPECS[operator]
        overlay = str(REPOSITORY_ROOT / "k8s" / "overlays" / kube.target.environment)
        command = [value.format(overlay=overlay) for value in spec["apply"]]
        _operator_command(kube, command, f"deploy {operator} operator")
        for crd in spec["crds"]:
            kube.kubectl(
                "wait",
                "--for=condition=Established",
                f"crd/{crd}",
                "--timeout=120s",
                timeout=150,
                activity=f"wait for {crd}",
            )
        namespace, deployment = spec["rollout"]
        kube.kubectl(
            "-n",
            namespace,
            "rollout",
            "status",
            f"deployment/{deployment}",
            "--timeout=5m",
            timeout=330,
            activity=f"wait for {operator} operator",
        )
        for resource in spec["resources"]:
            kube.kubectl(
                "get",
                resource,
                "--all-namespaces",
                timeout=30,
                activity=f"verify {resource}",
            )
        return
    if operator == "kafka":
        kube.ensure_namespace("kafka-system")
        kube.commands.run(
            ["helm", "repo", "add", "strimzi", "https://strimzi.io/charts/", "--force-update"],
            timeout=120,
        )
        kube.commands.run(["helm", "repo", "update", "strimzi"], timeout=180)
        kube.commands.run(
            [
                "helm",
                "upgrade",
                "--install",
                "strimzi-operator",
                "strimzi/strimzi-kafka-operator",
                "--version",
                "0.48.0",
                "--kube-context",
                kube.target.context,
                "--namespace",
                "kafka-system",
                "--set",
                f"watchNamespaces={{{kube.target.data_namespace}}}",
                "--wait",
                "--timeout",
                "10m",
            ],
            timeout=OPERATOR_TIMEOUT,
            activity="deploy Strimzi operator",
        )
        required = ["kafkas.kafka.strimzi.io"]
        optional = "kafkanodepools.kafka.strimzi.io"
        for crd in required:
            kube.kubectl("wait", "--for=condition=Established", f"crd/{crd}", "--timeout=120s")
            kube.kubectl("get", crd, "--all-namespaces")
        if kube.commands.succeeds(
            ["kubectl", "--context", kube.target.context, "get", "crd", optional]
        ):
            kube.kubectl("wait", "--for=condition=Established", f"crd/{optional}", "--timeout=120s")
            kube.kubectl("get", optional, "--all-namespaces")
        return
    if operator == "mongodb":
        kube.require_namespace(kube.target.data_namespace)
        kube.commands.run(
            [
                "helm",
                "repo",
                "add",
                "mongodb",
                "https://mongodb.github.io/helm-charts",
                "--force-update",
            ],
            timeout=120,
        )
        kube.commands.run(["helm", "repo", "update", "mongodb"], timeout=180)
        kube.commands.run(
            [
                "helm",
                "upgrade",
                "--install",
                "mongodb-operator",
                "mongodb/community-operator",
                "--version",
                "0.13.0",
                "--kube-context",
                kube.target.context,
                "--namespace",
                kube.target.data_namespace,
                "--wait",
                "--timeout",
                "10m",
            ],
            timeout=OPERATOR_TIMEOUT,
            activity="deploy MongoDB operator",
        )
        crd = "mongodbcommunity.mongodbcommunity.mongodb.com"
        kube.kubectl("wait", "--for=condition=Established", f"crd/{crd}", "--timeout=120s")
        kube.kubectl("get", "mongodbcommunity", "--all-namespaces")
        return
    raise AdminError(f"unknown operator {operator}")


def create_secret(kube: Kubernetes, profile: str, values: Mapping[str, str]) -> None:
    kube.verify_context()
    service = values.get("SVC_NAME", "")
    if service:
        safe_name(service, "SVC_NAME")
    environment = kube.target.environment
    if profile == "postgres":
        kube.ensure_namespace(kube.target.data_namespace)
        if environment == "qa":
            kube.ensure_namespace(kube.target.service_namespace)
        payload = _secret_payload(
            kube.target.data_namespace,
            f"{environment}-postgres-{service}-user-creds",
            {"username": service, "password": values["PG_SVC_USER_PWD"]},
            "kubernetes.io/basic-auth",
        )
        kube.apply(payload)
        if environment == "qa":
            copy = dict(payload)
            copy["metadata"] = {
                "name": payload["metadata"]["name"],
                "namespace": kube.target.service_namespace,
            }
            kube.apply(copy)
        return
    if profile == "elasticsearch-copy":
        require(environment == "qa", "Elasticsearch Secret copy is only supported for qa")
        kube.ensure_namespace(kube.target.data_namespace)
        kube.ensure_namespace(kube.target.service_namespace)
        name = f"{environment}-elasticsearch-es-elastic-user"
        kube.apply(
            _copy_secret_payload(
                kube.secret(kube.target.data_namespace, name), kube.target.service_namespace
            )
        )
        return
    if profile.startswith("minio-"):
        namespace = f"{environment}-minio-{service}"
        kube.ensure_namespace(namespace)
        if profile == "minio-storage-user":
            payload = _secret_payload(
                namespace,
                f"{namespace}-storage-user-creds",
                {
                    "CONSOLE_ACCESS_KEY": values["MINIO_CONSOLE_ACCESS_KEY"],
                    "CONSOLE_SECRET_KEY": values["MINIO_CONSOLE_SECRET_KEY"],
                },
            )
        elif profile == "minio-storage-config":
            config = (
                f"export MINIO_ROOT_USER={shlex.quote(_single_line_secret(values['MINIO_ROOT_USER'], 'MINIO_ROOT_USER'))}\n"
                f"export MINIO_ROOT_PASSWORD={shlex.quote(_single_line_secret(values['MINIO_ROOT_USER_PWD'], 'MINIO_ROOT_USER_PWD'))}\n"
                'export MINIO_PROMETHEUS_AUTH_TYPE="public"'
            )
            payload = _secret_payload(
                namespace, f"{namespace}-storage-config", {"config.env": config}
            )
        elif profile == "minio-service-user":
            payload = _secret_payload(
                namespace,
                f"{namespace}-svc-user-creds",
                {
                    "MINIO_SVC_ACCESS_KEY": values["MINIO_SVC_ACCESS_KEY"],
                    "MINIO_SVC_SECRET_KEY": values["MINIO_SVC_SECRET_KEY"],
                },
            )
        else:
            raise AdminError(f"unknown Secret profile {profile}")
        kube.apply(payload)
        if profile == "minio-service-user":
            copy_namespace = safe_name(values["COPY_SECRET_NS"], "COPY_SECRET_NS")
            kube.require_namespace(copy_namespace)
            copy = dict(payload)
            copy["metadata"] = {"name": payload["metadata"]["name"], "namespace": copy_namespace}
            kube.apply(copy)
        return
    if profile == "mongodb-root":
        kube.ensure_namespace(kube.target.data_namespace)
        kube.apply(
            _secret_payload(
                kube.target.data_namespace,
                f"{environment}-mongodb-root-user-creds",
                {"username": "root", "password": values["MONGO_ROOT_USER_PWD"]},
            )
        )
        return
    if profile == "mongodb-product":
        kube.ensure_namespace(kube.target.data_namespace)
        if environment == "qa":
            kube.ensure_namespace(kube.target.service_namespace)
        payload = _secret_payload(
            kube.target.data_namespace,
            f"{environment}-mongodb-product-user-creds",
            {
                "username": "product",
                "password": values["MONGO_PRODUCT_USER_PWD"],
                "database": "products-demo",
            },
        )
        kube.apply(payload)
        if environment == "qa":
            copy = dict(payload)
            copy["metadata"] = {
                "name": payload["metadata"]["name"],
                "namespace": kube.target.service_namespace,
            }
            kube.apply(copy)
        return
    if profile == "zipkin":
        require(environment == "qa", "Zipkin administration is only supported for qa")
        kube.ensure_namespace(kube.target.monitoring_namespace)
        kube.apply(
            _secret_payload(
                kube.target.monitoring_namespace,
                f"{environment}-zipkin-es-user-creds",
                {"username": values["ZIPKIN_ES_USER"], "password": values["ZIPKIN_ES_USER_PWD"]},
            )
        )
        return
    raise AdminError(f"unknown Secret profile {profile}")


def _decode_secret_value(secret: dict, key: str) -> str:
    encoded = secret.get("data", {}).get(key)
    require(isinstance(encoded, str), f"Secret key {key} is missing")
    try:
        return base64.b64decode(encoded, validate=True).decode()
    except (ValueError, UnicodeError) as error:
        raise AdminError(f"Secret key {key} is invalid") from error


def _minio_runtime(kube: Kubernetes, service: str) -> tuple[str, str, str, dict]:
    namespace = f"{kube.target.environment}-minio-{safe_name(service, 'SVC_NAME')}"
    kube.require_namespace(namespace)
    selector = f"v1.min.io/tenant={namespace}"
    kube.kubectl(
        "-n",
        namespace,
        "wait",
        "--for=condition=Ready",
        "pod",
        "-l",
        selector,
        "--timeout=120s",
        timeout=150,
        activity=f"wait for MinIO tenant {namespace}",
    )
    pod_json = kube.kubectl("-n", namespace, "get", "pods", "-l", selector, "-o", "json")
    try:
        inventory = json.loads(pod_json)
    except json.JSONDecodeError as error:
        raise AdminError(f"MinIO pod inventory in {namespace} is malformed") from error
    require(isinstance(inventory, dict), f"MinIO pod inventory in {namespace} is invalid")
    pods = inventory.get("items", [])
    require(isinstance(pods, list), f"MinIO pod inventory in {namespace} is invalid")
    names = sorted(item.get("metadata", {}).get("name", "") for item in pods)
    require(names and all(names), f"no MinIO tenant pod found in {namespace}")
    pod = names[0]
    root = kube.secret(namespace, f"{namespace}-storage-config")
    config_text = _decode_secret_value(root, "config.env")
    entries = {}
    for line in config_text.splitlines():
        if line.startswith("export ") and "=" in line:
            try:
                assignment = shlex.split(line[7:])
            except ValueError as error:
                raise AdminError("MinIO storage config contains invalid shell quoting") from error
            require(len(assignment) == 1 and "=" in assignment[0], "invalid MinIO storage config")
            key, value = assignment[0].split("=", 1)
            entries[key] = value
    require(
        entries.get("MINIO_ROOT_USER") and entries.get("MINIO_ROOT_PASSWORD"),
        "root credentials missing",
    )
    scheme = "https" if kube.target.environment == "qa" else "http"
    config = {
        "version": "10",
        "aliases": {
            namespace: {
                "url": f"{scheme}://{namespace}-hl:9000",
                "accessKey": entries["MINIO_ROOT_USER"],
                "secretKey": entries["MINIO_ROOT_PASSWORD"],
                "api": "S3v4",
                "path": "auto",
            }
        },
    }
    return namespace, pod, f"/tmp/insurance-hub-{os.getpid()}", config


def _minio_exec(
    kube: Kubernetes,
    namespace: str,
    pod: str,
    arguments: Sequence[str],
    *,
    input_text: str | None = None,
) -> str:
    return kube.kubectl(
        "-n",
        namespace,
        "exec",
        "-i",
        pod,
        "--",
        *arguments,
        input_text=input_text,
        timeout=120,
        activity="configure MinIO",
    )


def _minio_mutation(
    kube: Kubernetes,
    namespace: str,
    pod: str,
    arguments: Sequence[str],
    outcome: str,
    *,
    input_text: str | None = None,
) -> str:
    try:
        return _minio_exec(kube, namespace, pod, arguments, input_text=input_text)
    except CommandError as error:
        raise AdminError(
            f"{outcome} outcome is uncertain; inspect MinIO state before retrying"
        ) from error


def _json_lines(output: str, label: str) -> list[dict]:
    values = []
    for line in output.splitlines():
        if not line.strip():
            continue
        try:
            value = json.loads(line)
        except json.JSONDecodeError as error:
            raise AdminError(f"MinIO returned malformed {label} JSON") from error
        require(isinstance(value, dict), f"MinIO returned invalid {label} data")
        values.append(value)
    return values


def _minio_buckets(
    kube: Kubernetes, namespace: str, pod: str, mc: list[str], alias: str
) -> set[str]:
    rows = _json_lines(_minio_exec(kube, namespace, pod, [*mc, "ls", alias]), "bucket list")
    return {
        str(row.get("key", "")).rstrip("/")
        for row in rows
        if row.get("status", "success") == "success"
    }


def _minio_users(kube: Kubernetes, namespace: str, pod: str, mc: list[str], alias: str) -> set[str]:
    rows = _json_lines(
        _minio_exec(kube, namespace, pod, [*mc, "admin", "user", "list", alias]),
        "user list",
    )
    return {
        str(row.get("accessKey", row.get("user", row.get("userName", ""))))
        for row in rows
        if row.get("status", "success") == "success"
    }


def _contains_scalar(value: object, expected: str) -> bool:
    if isinstance(value, dict):
        return any(_contains_scalar(item, expected) for item in value.values())
    if isinstance(value, list):
        return any(_contains_scalar(item, expected) for item in value)
    return value == expected


def _minio_policy_has_user(
    kube: Kubernetes,
    namespace: str,
    pod: str,
    mc: list[str],
    alias: str,
    policy: str,
    user: str,
) -> bool:
    rows = _json_lines(
        _minio_exec(
            kube,
            namespace,
            pod,
            [*mc, "admin", "policy", "entities", alias, "--policy", policy],
        ),
        "policy association",
    )
    return any(_contains_scalar(row, user) for row in rows)


def configure_minio(
    kube: Kubernetes,
    action: str,
    service: str,
    *,
    bucket: str = "",
    policy_path: Path | None = None,
) -> None:
    kube.verify_context()
    namespace, pod, directory, config = _minio_runtime(kube, service)
    mc = ["mc", "--json", "--config-dir", directory, "--insecure"]
    alias = namespace
    primary_error: BaseException | None = None
    try:
        _minio_exec(
            kube,
            namespace,
            pod,
            ["sh", "-ceu", f"umask 077; mkdir -p {directory}; cat > {directory}/config.json"],
            input_text=json.dumps(config),
        )
        if action == "bucket":
            safe_name(bucket, "BUCKET_NAME")
            if bucket not in _minio_buckets(kube, namespace, pod, mc, alias):
                _minio_mutation(
                    kube,
                    namespace,
                    pod,
                    [*mc, "mb", f"{alias}/{bucket}"],
                    "MinIO bucket creation",
                )
            _minio_exec(kube, namespace, pod, [*mc, "stat", f"{alias}/{bucket}"])
            return
        require(action == "user-policy" and policy_path is not None, "invalid MinIO action")
        policy = json.loads(policy_path.read_text())
        require(isinstance(policy.get("Statement"), list), "policy must contain a Statement list")
        credentials = kube.secret(namespace, f"{namespace}-svc-user-creds")
        access_key = _single_line_secret(
            _decode_secret_value(credentials, "MINIO_SVC_ACCESS_KEY"), "MINIO_SVC_ACCESS_KEY"
        )
        secret_key = _single_line_secret(
            _decode_secret_value(credentials, "MINIO_SVC_SECRET_KEY"), "MINIO_SVC_SECRET_KEY"
        )
        safe_name(policy_path.stem, "policy name")
        credential_input = f"{access_key}\n{secret_key}\n"
        # MinIO's supported stdin form keeps both user credentials out of process arguments.
        if access_key not in _minio_users(kube, namespace, pod, mc, alias):
            _minio_mutation(
                kube,
                namespace,
                pod,
                [*mc, "admin", "user", "add", alias],
                "MinIO user creation",
                input_text=credential_input,
            )
        _minio_mutation(
            kube,
            namespace,
            pod,
            [*mc, "admin", "policy", "create", alias, policy_path.stem, "/dev/stdin"],
            "MinIO policy creation",
            input_text=json.dumps(policy),
        )
        _minio_mutation(
            kube,
            namespace,
            pod,
            [*mc, "admin", "policy", "attach", alias, policy_path.stem, "--user", access_key],
            "MinIO policy attachment",
        )
        require(
            access_key in _minio_users(kube, namespace, pod, mc, alias),
            "MinIO user postcondition failed",
        )
        require(
            _minio_policy_has_user(kube, namespace, pod, mc, alias, policy_path.stem, access_key),
            "MinIO policy association postcondition failed",
        )
    except BaseException as error:
        primary_error = error
        raise
    finally:
        try:
            _minio_exec(kube, namespace, pod, ["rm", "-rf", "--", directory])
        except Exception as error:
            if primary_error is None:
                raise AdminError(
                    "MinIO operation finished but protected remote cleanup failed"
                ) from error
            primary_error.add_note("protected MinIO remote cleanup also failed")


def create_zipkin_user(kube: Kubernetes, role: str, user: str) -> None:
    kube.verify_context()
    require(kube.target.environment == "qa", "Zipkin administration is only supported for qa")
    safe_name(role.replace("_", "-"), "Zipkin role")
    safe_name(user.replace("_", "-"), "Zipkin user")
    pod_name = "zipkin-es-setup"
    request_secret_name = "zipkin-es-setup-request"
    credentials = kube.secret(
        kube.target.monitoring_namespace, f"{kube.target.environment}-zipkin-es-user-creds"
    )
    secret_user = _decode_secret_value(credentials, "username")
    require(secret_user == user, "Zipkin Secret username differs from the requested user")
    user_body = json.dumps(
        {
            "password": _decode_secret_value(credentials, "password"),
            "roles": [role],
            "full_name": "Zipkin Service Account",
            "email": "zipkin@insurance-hub.com",
        }
    )
    role_body = json.dumps(
        {
            "cluster": ["monitor", "manage_index_templates"],
            "indices": [
                {
                    "names": ["zipkin*"],
                    "privileges": ["create_index", "write", "read", "manage"],
                }
            ],
        }
    )
    script = """set -eu
umask 077
trap 'rm -f /tmp/netrc' EXIT
printf 'machine %s login elastic password ' "$ES_HOST" > /tmp/netrc
cat /elastic/elastic >> /tmp/netrc
printf '\n' >> /tmp/netrc
curl --fail --silent --show-error --insecure --netrc-file /tmp/netrc -H 'Content-Type: application/json' -X PUT --data-binary @/role.json "https://${ES_HOST}:9200/_security/role/${ZIPKIN_ROLE}"
curl --fail --silent --show-error --insecure --netrc-file /tmp/netrc -H 'Content-Type: application/json' -X PUT --data-binary @/request/user.json "https://${ES_HOST}:9200/_security/user/${ZIPKIN_USER}"
curl --fail --silent --show-error --insecure --netrc-file /tmp/netrc "https://${ES_HOST}:9200/_security/role/${ZIPKIN_ROLE}"
curl --fail --silent --show-error --insecure --netrc-file /tmp/netrc "https://${ES_HOST}:9200/_security/user/${ZIPKIN_USER}"
"""
    pod = {
        "apiVersion": "v1",
        "kind": "Pod",
        "metadata": {"name": pod_name, "namespace": kube.target.data_namespace},
        "spec": {
            "restartPolicy": "Never",
            "containers": [
                {
                    "name": "setup",
                    "image": ZIPKIN_SETUP_IMAGE,
                    "command": ["sh", "-ceu", script],
                    "env": [
                        {
                            "name": "ES_HOST",
                            "value": f"qa-elasticsearch-es-http.{kube.target.data_namespace}.svc.cluster.local",
                        },
                        {"name": "ZIPKIN_ROLE", "value": role},
                        {"name": "ZIPKIN_USER", "value": user},
                    ],
                    "volumeMounts": [
                        {"name": "elastic", "mountPath": "/elastic", "readOnly": True},
                        {"name": "request", "mountPath": "/request", "readOnly": True},
                        {
                            "name": "role",
                            "mountPath": "/role.json",
                            "subPath": "role.json",
                            "readOnly": True,
                        },
                    ],
                }
            ],
            "volumes": [
                {
                    "name": "elastic",
                    "secret": {"secretName": "qa-elasticsearch-es-elastic-user"},
                },
                {"name": "request", "secret": {"secretName": request_secret_name}},
                {
                    "name": "role",
                    "configMap": {"name": "zipkin-es-setup-role"},
                },
            ],
        },
    }
    role_config = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": "zipkin-es-setup-role", "namespace": kube.target.data_namespace},
        "data": {"role.json": role_body},
    }
    request_secret = _secret_payload(
        kube.target.data_namespace, request_secret_name, {"user.json": user_body}
    )
    primary_error: BaseException | None = None
    try:
        kube.apply(role_config)
        kube.apply(request_secret)
        kube.kubectl(
            "-n", kube.target.data_namespace, "delete", "pod", pod_name, "--ignore-not-found"
        )
        kube.apply(pod)
        kube.kubectl(
            "-n",
            kube.target.data_namespace,
            "wait",
            "--for=jsonpath={.status.phase}=Succeeded",
            f"pod/{pod_name}",
            "--timeout=180s",
            timeout=200,
        )
    except BaseException as error:
        primary_error = error
        raise
    finally:
        cleanup_errors = []
        for kind, name in (
            ("pod", pod_name),
            ("configmap", "zipkin-es-setup-role"),
            ("secret", request_secret_name),
        ):
            try:
                kube.kubectl(
                    "-n",
                    kube.target.data_namespace,
                    "delete",
                    kind,
                    name,
                    "--ignore-not-found",
                )
            except Exception as error:
                cleanup_errors.append(f"{kind}/{name}: {error}")
        if cleanup_errors:
            detail = "; ".join(cleanup_errors)
            if primary_error is None:
                raise AdminError(f"Zipkin setup cleanup failed: {detail}")
            primary_error.add_note(f"Zipkin setup cleanup also failed: {detail}")


def image_name(service: str) -> str:
    safe_name(service, "SVC_NAME")
    suffix = "legacy" if service in {"agent-portal-gateway", "web-vue"} else "api-legacy"
    return f"insurance-hub-{service}-{suffix}:latest"


def manage_image(kube: Kubernetes, action: str, service: str) -> None:
    kube.verify_context()
    image = image_name(service)
    if action == "load":
        action = f"{kube.target.environment}-load"
    if action == "local-dev-load":
        require(kube.target.environment == "local-dev", "local image load requires local-dev")
        kube.commands.run(
            ["kind", "load", "docker-image", "--name", "local-dev-insurance-hub", image],
            timeout=300,
            activity=f"load {image}",
        )
        return
    if action == "local-dev-unload":
        require(kube.target.environment == "local-dev", "local image unload requires local-dev")
        output = kube.commands.run(
            [
                "docker",
                "ps",
                "--filter",
                "label=io.x-k8s.kind.cluster=local-dev-insurance-hub",
                "--format",
                "{{.Names}}",
            ]
        )
        nodes = [line for line in output.splitlines() if line]
        require(nodes, "no local-dev Kind nodes found")
        for node in nodes:
            kube.commands.run(
                [
                    "docker",
                    "exec",
                    node,
                    "ctr",
                    "-n",
                    "k8s.io",
                    "images",
                    "rm",
                    f"docker.io/library/{image}",
                ],
                timeout=60,
                activity=f"remove image from {node}",
            )
        return
    require(action == "qa-load" and kube.target.environment == "qa", "QA image load requires qa")
    nodes_json = kube.commands.run(["lxc", "list", "--format", "json"], timeout=30)
    nodes = sorted(
        item["name"]
        for item in json.loads(nodes_json)
        if item.get("name") in {"qa-master", "qa-worker1", "qa-worker2"}
    )
    require(nodes, "no QA LXD nodes found")
    with tempfile.TemporaryDirectory(prefix="insurance-hub-image-") as directory:
        archive = Path(directory) / f"{service}.tar"
        kube.commands.run(["docker", "save", image, "-o", str(archive)], timeout=300)
        loaded: list[str] = []
        try:
            for node in nodes:
                remote = f"/tmp/insurance-hub-{service}-{os.getpid()}.tar"
                push_attempted = False
                primary_error: BaseException | None = None
                try:
                    push_attempted = True
                    kube.commands.run(
                        ["lxc", "file", "push", str(archive), f"{node}{remote}"], timeout=180
                    )
                    kube.commands.run(
                        [
                            "lxc",
                            "exec",
                            node,
                            "--",
                            "/usr/local/bin/k3s",
                            "ctr",
                            "images",
                            "import",
                            remote,
                        ],
                        timeout=300,
                    )
                    loaded.append(node)
                except BaseException as error:
                    primary_error = error
                    raise
                finally:
                    if push_attempted:
                        try:
                            kube.commands.run(
                                ["lxc", "exec", node, "--", "rm", "-f", remote], timeout=30
                            )
                        except Exception as error:
                            if primary_error is None:
                                raise AdminError(
                                    f"image loaded but remote cleanup failed on {node}"
                                ) from error
                            primary_error.add_note(f"remote image cleanup also failed on {node}")
        except Exception as error:
            completed = ", ".join(loaded) or "none"
            raise AdminError(f"QA image load failed; completed nodes: {completed}") from error


def _make(commands: Commands, target: str, *, variables: Mapping[str, str] | None = None) -> None:
    make_command = shlex.split(os.environ.get("MAKE", "make"))
    require(make_command, "MAKE command is empty")
    arguments = [*make_command, "-C", str(REPOSITORY_ROOT / "k8s"), target]
    arguments.extend(f"{key}={value}" for key, value in (variables or {}).items())
    arguments.append(f"PYTHON={sys.executable}")
    commands.run(arguments, timeout=OPERATOR_TIMEOUT, activity=f"make {target}")


def deploy_aggregate(kube: Kubernetes, kind: str, values: Mapping[str, str] | None = None) -> None:
    kube.verify_context()
    environment = kube.target.environment
    values = dict(values or {})
    if kind == "infra":
        if environment == "local-dev":
            _make(kube.commands, "minio-operator-deploy")
        for target in (
            "postgres-operator-deploy",
            "mongodb-operator-install",
            "kafka-strimzi-operator-install",
        ):
            _make(kube.commands, target)
        if environment == "local-dev":
            _make(kube.commands, "es-deploy")
        for service in ("auth", "document", "payment", "policy", "pricing", "product"):
            create_secret(kube, "postgres", {**values, "SVC_NAME": service})
            _make(kube.commands, "postgres-svc-deploy", variables={"SVC_NAME": service})
        create_secret(kube, "mongodb-root", values)
        create_secret(kube, "mongodb-product", values)
        for target in ("mongodb-deploy", "kafka-deploy", "jsreport-deploy"):
            _make(kube.commands, target)
        minio_services = ("document", "payment")
        for service in minio_services:
            service_values = {**values, "SVC_NAME": service}
            create_secret(kube, "minio-storage-user", service_values)
            create_secret(kube, "minio-storage-config", service_values)
        for service in minio_services:
            _make(kube.commands, "minio-tenant-deploy", variables={"SVC_NAME": service})
            namespace = f"{environment}-minio-{service}"
            kube.kubectl(
                "-n",
                namespace,
                "wait",
                "--for=condition=Ready",
                "pod",
                "--all",
                "--timeout=5m",
                timeout=330,
                activity=f"wait for {service} MinIO",
            )
        return
    require(kind == "services", f"unknown aggregate {kind}")
    if environment == "qa":
        _make(kube.commands, "es-elastic-user-secret-copy")
    minio = (
        ("document", "policies", "apps/svc/document/minio/s3-policy-policies.json"),
        ("payment", "payments-import", "apps/svc/payment/minio/s3-policy-payments-import.json"),
    )
    for service, bucket, policy in minio:
        _make(
            kube.commands,
            "minio-svc-bucket-create",
            variables={"SVC_NAME": service, "BUCKET_NAME": bucket},
        )
        create_secret(kube, "minio-service-user", {**values, "SVC_NAME": service})
        _make(
            kube.commands,
            "minio-svc-user-with-policy-create",
            variables={"SVC_NAME": service, "POLICY_FILE": policy},
        )
    services = (
        "web-vue",
        "agent-portal-gateway",
        "auth",
        "product",
        "policy-search",
        "dashboard",
        "policy",
        "pricing",
        "document",
        "payment",
        "chat",
    )
    action = "svc-load-delete-deploy" if environment == "local-dev" else "svc-deploy"
    for service in services:
        _make(kube.commands, action, variables={"SVC_NAME": service})
    kube.kubectl(
        "-n",
        kube.target.service_namespace,
        "wait",
        "--for=condition=Available",
        "deployment",
        "--all",
        "--timeout=10m",
        timeout=630,
        activity="wait for service deployments",
    )


def create_local_cluster(commands: Commands, target: Target, storage: Path, config: Path) -> None:
    require(target.environment == "local-dev", "local cluster create requires local-dev")
    require(target.context.startswith("kind-"), "local cluster context must be a Kind context")
    require(config.is_file(), f"Kind config does not exist: {config}")
    cluster_name = target.context.removeprefix("kind-")
    storage.mkdir(mode=0o700, parents=True, exist_ok=True)
    creation_attempted = False
    created = False
    try:
        creation_attempted = True
        commands.run(
            [
                "kind",
                "create",
                "cluster",
                "--name",
                cluster_name,
                "--config",
                str(config),
            ],
            timeout=600,
            activity="create local Kind cluster",
        )
        created = True
        manifest = commands.run(
            [
                "curl",
                "--fail",
                "--silent",
                "--show-error",
                "--location",
                "https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml",
            ],
            timeout=120,
            activity="download Metrics Server manifest",
        )
        kube = Kubernetes(commands, target)
        kube.verify_context()
        kube.kubectl("apply", "-f", "-", input_text=manifest, timeout=120)
        for value in (
            "--kubelet-insecure-tls",
            "--kubelet-preferred-address-types=InternalIP,Hostname",
        ):
            patch = json.dumps(
                [{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": value}]
            )
            kube.kubectl(
                "-n",
                "kube-system",
                "patch",
                "deployment",
                "metrics-server",
                "--type=json",
                "-p",
                patch,
            )
        kube.kubectl(
            "-n",
            "kube-system",
            "rollout",
            "status",
            "deployment/metrics-server",
            "--timeout=180s",
            timeout=210,
        )
    except Exception as error:
        if created:
            state = "created; rerun after inspecting the retained cluster"
        elif creation_attempted:
            state = "creation outcome uncertain; inspect for a retained cluster before retrying"
        else:
            state = "not created"
        raise AdminError(f"local cluster setup failed ({state})") from error


PORT_FORWARDS = (
    ("local-dev-all", "svc/local-dev-kafka-broker-controller-0", 9094, 9094),
    ("local-dev-all", "svc/local-dev-postgres-auth-rw", 5442, 5432),
    ("local-dev-all", "svc/local-dev-postgres-document-rw", 5452, 5432),
    ("local-dev-all", "svc/local-dev-postgres-payment-rw", 5462, 5432),
    ("local-dev-all", "svc/local-dev-postgres-policy-rw", 5472, 5432),
    ("local-dev-all", "svc/local-dev-postgres-pricing-rw", 5482, 5432),
    ("local-dev-all", "svc/local-dev-postgres-product-rw", 5492, 5432),
    ("local-dev-minio-document", "svc/local-dev-minio-document-hl", 9001, 9000),
    ("local-dev-minio-payment", "svc/local-dev-minio-payment-hl", 9002, 9000),
    ("local-dev-all", "svc/local-dev-jsreport", 5488, 5488),
    ("local-dev-all", "svc/local-dev-web-vue-legacy", 8080, 80),
)


def _port_ready(port: int) -> bool:
    with socket.socket() as connection:
        connection.settimeout(0.1)
        return connection.connect_ex(("127.0.0.1", port)) == 0


def port_forward(kube: Kubernetes) -> None:
    kube.verify_context()
    require(kube.target.environment == "local-dev", "port forwards require local-dev")
    processes: list[subprocess.Popen] = []
    diagnostics = []

    def failure_detail(process: subprocess.Popen) -> str:
        index = processes.index(process)
        output = diagnostics[index]
        output.seek(0)
        detail = output.read()[-1000:].strip()
        namespace, resource, local_port, _ = PORT_FORWARDS[index]
        prefix = f"{namespace}/{resource} on 127.0.0.1:{local_port}"
        return f"{prefix}: {detail}" if detail else prefix

    try:
        for namespace, resource, local_port, remote_port in PORT_FORWARDS:
            diagnostic = tempfile.TemporaryFile(mode="w+t")
            diagnostics.append(diagnostic)
            process = subprocess.Popen(
                [
                    "kubectl",
                    "--context",
                    kube.target.context,
                    "-n",
                    namespace,
                    "port-forward",
                    resource,
                    f"{local_port}:{remote_port}",
                    "--address=127.0.0.1",
                ],
                stdin=subprocess.DEVNULL,
                stdout=subprocess.DEVNULL,
                stderr=diagnostic,
                env=kube.commands.environment,
                text=True,
                start_new_session=True,
            )
            processes.append(process)
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            failed = [process for process in processes if process.poll() is not None]
            if failed:
                raise AdminError(
                    f"port-forward exited before readiness: {failure_detail(failed[0])}"
                )
            if all(_port_ready(item[2]) for item in PORT_FORWARDS):
                break
            time.sleep(0.2)
        else:
            raise AdminError("port-forwards did not become ready within 30 seconds")
        print("All local-dev port-forwards are ready; press Ctrl+C to stop.")
        while all(process.poll() is None for process in processes):
            time.sleep(1)
        failed = next(process for process in processes if process.poll() is not None)
        raise AdminError(f"port-forward exited unexpectedly: {failure_detail(failed)}")
    finally:
        for sig in (signal.SIGTERM, signal.SIGKILL):
            for process in processes:
                if process.poll() is None:
                    try:
                        os.killpg(process.pid, sig)
                    except ProcessLookupError:
                        pass
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline and any(p.poll() is None for p in processes):
                time.sleep(0.05)
            if all(p.poll() is not None for p in processes):
                break
        for diagnostic in diagnostics:
            diagnostic.close()


def clean_local_storage(path: Path) -> None:
    require(
        path.is_absolute() and path.name == "local-dev-storage", "refusing broad storage deletion"
    )
    if path.exists():
        shutil.rmtree(path)
