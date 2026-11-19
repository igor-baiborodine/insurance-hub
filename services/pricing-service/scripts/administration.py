"""Service-local Pricing PostgreSQL administration mechanics."""

from __future__ import annotations

import base64
import binascii
import json
import os
import re
import subprocess

ROLE = "go_pricing_reader"
DATABASE = "pricing"
CA_MOUNT = "/etc/pricing-service/postgres/ca.crt"
KUBECTL = os.environ.get("KUBECTL", "kubectl")
ENVIRONMENTS = {
    "local-dev": ("kind-local-dev-insurance-hub", "local-dev-all", "local-dev-all"),
    "qa": ("qa-insurance-hub", "qa-data", "qa-svc"),
}


class RoleError(Exception):
    pass


def require(condition: bool, message: str) -> None:
    if not condition:
        raise RoleError(message)


def command(argv: list[str], *, input_text: str | None = None, env: dict | None = None) -> str:
    """Never include secret material in argv or surfaced subprocess diagnostics."""
    try:
        result = subprocess.run(
            argv, input=input_text, text=True, capture_output=True, check=False, timeout=30, env=env
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise RoleError(f"command unavailable or timed out: {argv[0]}") from exc
    if result.returncode:
        raise RoleError(
            f"command failed: {argv[0]} (exit {result.returncode}); inspect target safely"
        )
    output = result.stdout.strip()
    probe = re.search(r"reader-probe-state:([0-9A-Z]{5})", result.stderr)
    if probe:
        output += "\nreader-probe-state:" + probe.group(1)
    return output


def sql_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def safe_probe_sql(statement: str) -> str:
    escaped = sql_literal(statement)
    return f"""
BEGIN;
SET LOCAL lock_timeout='1s';
SET LOCAL statement_timeout='2s';
SET LOCAL idle_in_transaction_session_timeout='2s';
DO $probe$
DECLARE observed_state text := '00000';
BEGIN
  BEGIN
    EXECUTE {escaped};
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS observed_state = RETURNED_SQLSTATE;
  END;
  RAISE NOTICE 'reader-probe-state:%', observed_state;
END $probe$;
ROLLBACK;
"""


class Kubernetes:
    def __init__(self, environment: str):
        require(environment in ENVIRONMENTS, "PRICING_RESOURCE_ENV must be local-dev or qa")
        self.environment = environment
        self.context, self.data_ns, self.svc_ns = ENVIRONMENTS[environment]
        self.cluster = f"{environment}-postgres-pricing"
        self.secret_name = f"{environment}-postgres-pricing-reader-creds"
        self.ca_name = f"{environment}-postgres-pricing-ca"
        self.hostname = f"{self.cluster}-rw.{self.data_ns}.svc.cluster.local"
        expected = {
            "PRICING_RESOURCE_CONTEXT": self.context,
            "PRICING_RESOURCE_DATA_NS": self.data_ns,
            "PRICING_RESOURCE_SVC_NS": self.svc_ns,
            "PRICING_RESOURCE_CLUSTER": self.cluster,
            "PRICING_RESOURCE_READER_SECRET": self.secret_name,
            "PRICING_RESOURCE_DB_HOST": self.hostname,
        }
        for key, value in expected.items():
            require(os.environ.get(key) == value, f"{key} must explicitly equal {value}")
        require(
            command([KUBECTL, "config", "current-context"]) == self.context,
            "current Kubernetes context differs from explicit target",
        )
        for namespace in (self.data_ns, self.svc_ns):
            self.kubectl("get", "namespace", namespace, "-o", "name")
        cluster = json.loads(
            self.kubectl(
                "-n", self.data_ns, "get", "cluster.postgresql.cnpg.io", self.cluster, "-o", "json"
            )
        )
        require(cluster["metadata"]["name"] == self.cluster, "database cluster identity mismatch")
        self.cluster_uid = cluster["metadata"].get("uid", "")
        require(
            bool(re.fullmatch(r"[0-9a-f-]{36}", self.cluster_uid)),
            "database cluster UID is unavailable",
        )
        self.primary = cluster.get("status", {}).get("currentPrimary")
        require(
            bool(self.primary) and re.fullmatch(r"[a-z0-9-]+", self.primary),
            "database primary is not reported",
        )
        pod = json.loads(self.kubectl("-n", self.data_ns, "get", "pod", self.primary, "-o", "json"))
        require(
            pod["metadata"]["labels"].get("cnpg.io/cluster") == self.cluster
            and pod["status"].get("phase") == "Running"
            and any(
                item.get("type") == "Ready" and item.get("status") == "True"
                for item in pod["status"].get("conditions", [])
            ),
            "database primary pod identity/readiness mismatch",
        )

    def kubectl(self, *args: str, input_text: str | None = None) -> str:
        return command([KUBECTL, f"--context={self.context}", *args], input_text=input_text)

    def admin(self, sql: str) -> str:
        return self.kubectl(
            "-n",
            self.data_ns,
            "exec",
            "-i",
            self.primary,
            "--",
            "psql",
            "-X",
            "-q",
            "-A",
            "-t",
            "-v",
            "ON_ERROR_STOP=1",
            "-U",
            "postgres",
            "-d",
            DATABASE,
            input_text=sql,
        )

    def get_secret(
        self, name: str, namespace: str, *, owner: str | None = None
    ) -> dict[str, bytes] | None:
        raw = self.kubectl(
            "-n", namespace, "get", "secret", name, "--ignore-not-found", "-o", "json"
        )
        if not raw:
            return None
        record = json.loads(raw)
        require(
            record["metadata"]["name"] == name and record["metadata"]["namespace"] == namespace,
            "Secret identity mismatch",
        )
        if owner is not None:
            labels = record["metadata"].get("labels", {})
            require(
                labels.get("insurance-hub.io/owner") == owner
                and labels.get("app.kubernetes.io/name") == "pricing-api",
                "managed Secret ownership differs",
            )
        try:
            return {
                key: base64.b64decode(value, validate=True) for key, value in record["data"].items()
            }
        except (ValueError, TypeError, binascii.Error) as exc:
            raise RoleError("Secret data could not be decoded") from exc

    def ca(self) -> bytes:
        source = self.get_secret(self.ca_name, self.data_ns)
        require(
            source is not None
            and source.get("ca.crt", b"").startswith(b"-----BEGIN CERTIFICATE-----"),
            "database CA Secret has no usable ca.crt",
        )
        return source["ca.crt"]
