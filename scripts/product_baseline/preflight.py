#!/usr/bin/env python3
"""Run bounded read-only Product baseline target and database checks."""

from __future__ import annotations

import base64
import json
import os
import re
import shutil
import sys
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path

from scripts.python.process_runner import CommandError, run_command

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
SCRIPT_ROOT = REPOSITORY_ROOT / "scripts/product-baseline"


class PreflightError(RuntimeError):
    """The selected Product baseline target is unsafe or unavailable."""


@dataclass(frozen=True)
class Target:
    context: str
    service_namespace: str
    data_namespace: str
    product_deployment: str
    product_service: str
    gateway_deployment: str
    gateway_service: str
    database_secret: str
    database_cluster: str


TARGETS = {
    "local-dev": Target(
        "kind-local-dev-insurance-hub",
        "local-dev-all",
        "local-dev-all",
        "local-dev-product-api-legacy",
        "local-dev-product-api-legacy",
        "local-dev-agent-portal-gateway-legacy",
        "local-dev-agent-portal-gateway-legacy",
        "local-dev-postgres-product-user-creds",
        "local-dev-postgres-product",
    ),
    "qa": Target(
        "qa-insurance-hub",
        "qa-svc",
        "qa-data",
        "qa-product-api-legacy",
        "qa-product-api-legacy",
        "qa-agent-portal-gateway-legacy",
        "qa-agent-portal-gateway-legacy",
        "qa-postgres-product-user-creds",
        "qa-postgres-product",
    ),
}


def require(condition: bool, message: str) -> None:
    if not condition:
        raise PreflightError(message)


def optional_environment(name: str, default: str) -> str:
    """Apply shell-style ${NAME:-default} semantics to optional Make exports."""
    return os.environ.get(name) or default


def command(
    arguments: Sequence[str],
    *,
    timeout: float = 30,
    input_text: str | None = None,
    env: Mapping[str, str] | None = None,
) -> str:
    try:
        result = run_command(
            arguments,
            cwd=REPOSITORY_ROOT,
            env=dict(os.environ if env is None else env),
            input_text=input_text,
            timeout_seconds=timeout,
            teardown_seconds=5,
            activity=f"run {arguments[0]} for Product baseline preflight",
        )
    except (OSError, CommandError) as error:
        raise PreflightError(f"command unavailable or timed out: {arguments[0]}") from error
    return result.stdout.strip()


def kubectl(target: Target, *arguments: str, timeout: float = 30) -> str:
    return command(["kubectl", f"--context={target.context}", *arguments], timeout=timeout)


def database_query(
    target: Target,
    transport: str,
    host: str,
    port: str,
    username: str,
    password: str,
    pod: str,
    *,
    sql: str | None = None,
    sql_file: Path | None = None,
) -> str:
    require((sql is None) != (sql_file is None), "exactly one SQL input is required")
    input_text = sql_file.read_text(encoding="utf-8") if sql_file else None
    if transport == "kubectl-exec":
        arguments = [
            "kubectl",
            f"--context={target.context}",
            "-n",
            target.data_namespace,
            "exec",
            "-i" if sql_file else "",
            pod,
            "--",
            "psql",
            "-X",
            "-q",
            "-A",
            "-t",
            "-v",
            "ON_ERROR_STOP=1",
            "--username=postgres",
            "--dbname=product",
        ]
        arguments = [item for item in arguments if item]
        if sql is not None:
            arguments.append(f"--command={sql}")
        return command(arguments, timeout=60, input_text=input_text)
    environment = dict(os.environ, PGPASSWORD=password, PGCONNECT_TIMEOUT="5")
    arguments = [
        "psql",
        "-X",
        "-q",
        "-A",
        "-t",
        "-v",
        "ON_ERROR_STOP=1",
        f"--host={host}",
        f"--port={port}",
        f"--username={username}",
        "--dbname=product",
    ]
    if sql_file:
        arguments.append(f"--file={sql_file}")
        input_text = None
    else:
        arguments.append(f"--command={sql}")
    return command(arguments, timeout=60, input_text=input_text, env=environment)


def main(_argv: Sequence[str] | None = None) -> int:
    environment = os.environ.get("BASELINE_ENV", "")
    try:
        require(bool(environment), "BASELINE_ENV is required; use local-dev or qa.")
        require(
            environment in TARGETS,
            f"Unsupported BASELINE_ENV '{environment}'; use local-dev or qa.",
        )
        target = TARGETS[environment]
        requested_context = optional_environment("BASELINE_KUBE_CONTEXT", target.context)
        require(
            requested_context == target.context,
            f"BASELINE_KUBE_CONTEXT '{requested_context}' does not match {environment} context '{target.context}'.",
        )
        host = optional_environment("BASELINE_PRODUCT_DB_HOST", "127.0.0.1")
        port = optional_environment("BASELINE_PRODUCT_DB_PORT", "5492")
        transport = optional_environment("BASELINE_PRODUCT_DB_TRANSPORT", "port-forward")
        require(port.isdigit(), "BASELINE_PRODUCT_DB_PORT must be numeric.")
        require(
            transport in {"port-forward", "kubectl-exec"},
            "BASELINE_PRODUCT_DB_TRANSPORT must be 'port-forward' or 'kubectl-exec'.",
        )
        require(
            not (environment == "local-dev" and port == "5482"),
            "Local port 5482 belongs to Pricing; Product PostgreSQL uses 5492 by default.",
        )
        for executable in ("docker", "java", "kubectl", "mvn", "pg_isready", "psql"):
            require(
                shutil.which(executable) is not None,
                f"Required command '{executable}' is not available.",
            )
        docker_version = command(
            [
                "docker",
                "version",
                "--format",
                "{{.Client.Version}} client, {{.Server.Version}} server",
            ]
        )
        maven_details = command(["mvn", "-version"]).splitlines()
        maven_version = maven_details[0]
        java_version = next(
            (line for line in maven_details if line.startswith("Java version:")), "Java 14"
        )
        psql_version = command(["psql", "--version"])
        command(["kubectl", "version", "--client", "--output=json"])
        current = command(["kubectl", "config", "current-context"])
        require(
            current == target.context,
            f"Current Kubernetes context '{current}' does not match '{target.context}'.",
        )
        for resource in ("deployments", "services", "endpointslices.discovery.k8s.io"):
            allowed = kubectl(
                target, "auth", "can-i", "get", resource, "-n", target.service_namespace
            )
            require(
                allowed == "yes",
                f"Cannot read {resource} in namespace '{target.service_namespace}'.",
            )
        allowed = kubectl(
            target,
            "auth",
            "can-i",
            "get",
            f"secret/{target.database_secret}",
            "-n",
            target.data_namespace,
        )
        require(
            allowed == "yes",
            f"Cannot read Product database credentials in namespace '{target.data_namespace}'.",
        )
        for workload in (target.product_deployment, target.gateway_deployment):
            ready = kubectl(
                target,
                "-n",
                target.service_namespace,
                "get",
                "deployment",
                workload,
                "-o",
                "jsonpath={.status.readyReplicas}",
            )
            require(
                bool(re.fullmatch(r"[1-9][0-9]*", ready)),
                f"Deployment '{workload}' has no ready replica.",
            )
        for service in (target.product_service, target.gateway_service):
            kubectl(target, "-n", target.service_namespace, "get", "service", service)
            address = kubectl(
                target,
                "-n",
                target.service_namespace,
                "get",
                "endpointslices.discovery.k8s.io",
                f"--selector=kubernetes.io/service-name={service}",
                "-o",
                "jsonpath={.items[0].endpoints[0].addresses[0]}",
            )
            require(bool(address), f"Service '{service}' has no ready endpoint.")
        username = password = pod = ""
        if transport == "port-forward":
            command(["pg_isready", f"--host={host}", f"--port={port}", "--timeout=5", "--quiet"])
            encoded_user = kubectl(
                target,
                "-n",
                target.data_namespace,
                "get",
                "secret",
                target.database_secret,
                "-o",
                "jsonpath={.data.username}",
            )
            encoded_password = kubectl(
                target,
                "-n",
                target.data_namespace,
                "get",
                "secret",
                target.database_secret,
                "-o",
                "jsonpath={.data.password}",
            )
            try:
                username = base64.b64decode(encoded_user, validate=True).decode()
                password = base64.b64decode(encoded_password, validate=True).decode()
            except (ValueError, UnicodeError) as error:
                raise PreflightError("Product database credentials are malformed.") from error
            require(bool(username and password), "Product database credentials are incomplete.")
        else:
            pod = kubectl(
                target,
                "-n",
                target.data_namespace,
                "get",
                "pods",
                f"-l=cnpg.io/cluster={target.database_cluster},cnpg.io/instanceRole=primary",
                "-o",
                "jsonpath={.items[0].metadata.name}",
            )
            require(bool(pod), "The Product PostgreSQL primary pod is unavailable.")
            kubectl(
                target,
                "-n",
                target.data_namespace,
                "exec",
                pod,
                "--",
                "pg_isready",
                "--host=127.0.0.1",
                "--port=5432",
                "--quiet",
            )
        inventory_output = os.environ.get("BASELINE_PREFLIGHT_INVENTORY_OUTPUT")
        catalog_output = os.environ.get("BASELINE_PREFLIGHT_CATALOG_OUTPUT")
        require(
            not (inventory_output and catalog_output),
            "Only one preflight database output may be requested.",
        )
        if inventory_output or catalog_output:
            output = Path(inventory_output or catalog_output or "")
            sql_file = SCRIPT_ROOT / ("inventory.sql" if inventory_output else "catalog.sql")
            raw = database_query(
                target, transport, host, port, username, password, pod, sql_file=sql_file
            )
            document = json.loads(raw)
            if inventory_output:
                require(
                    document.get("database") == "product"
                    and document.get("schema") == "public"
                    and document.get("table", {}).get("qualifiedName") == "public.product",
                    "Endpoint is not the expected product/public.product database.",
                )
                row_count = document.get("catalogProvenance", {}).get("rowCount")
            else:
                require(
                    document.get("database") == "product"
                    and document.get("schema") == "public"
                    and document.get("table") == "public.product"
                    and document.get("transactionReadOnly") is True,
                    "Endpoint is not the expected product/public.product database.",
                )
                row_count = document.get("dataIdentity", {}).get("rowCount")
            output.write_text(raw + "\n", encoding="utf-8")
        else:
            sql = "SET default_transaction_read_only=on; SELECT current_database() || '|' || current_schema() || '|' || COALESCE(to_regclass('public.product')::text, ''); SELECT count(*)::text FROM public.product;"
            rows = database_query(
                target, transport, host, port, username, password, pod, sql=sql
            ).splitlines()
            require(
                len(rows) >= 2 and rows[0] == "product|public|product" and rows[1].isdigit(),
                "Endpoint is not the expected product/public.product database.",
            )
            row_count = int(rows[1])
    except (PreflightError, OSError, json.JSONDecodeError, KeyError, TypeError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2
    endpoint = f"kubectl-exec:{pod}" if transport == "kubectl-exec" else f"{host}:{port}"
    print(f"Product baseline preflight passed for {environment}.")
    print(f"  Kubernetes context: {target.context}")
    print(f"  Service namespace: {target.service_namespace}")
    print(f"  Data namespace: {target.data_namespace}")
    print(f"  Product database endpoint: {endpoint}")
    print(f"  Product catalog rows visible: {row_count}")
    print(f"  Docker: {docker_version}")
    print(f"  Java: {java_version}")
    print(f"  Maven: {maven_version}")
    print(f"  PostgreSQL client: {psql_version}")
    print("  kubectl client metadata: available")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
