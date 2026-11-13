#!/usr/bin/env python3
"""Guarded Document PostgreSQL/MinIO administration; never imported by the API."""

from __future__ import annotations

import argparse
import base64
import json
import os
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import uuid
from contextlib import contextmanager
from pathlib import Path
from urllib.parse import parse_qsl, quote, urlencode, urlsplit, urlunsplit

ROOT = Path(__file__).resolve().parents[3]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))
from scripts.go.service.tooling_process import run_cli  # noqa: E402
from scripts.python.process_runner import (  # noqa: E402
    CommandError,
    child_environment,
    run_command,
    terminate_process_group,
)

SERVICE = Path(__file__).resolve().parents[1]
ENVIRONMENTS = {
    "local-dev": ("kind-local-dev-insurance-hub", "local-dev-all", "local-dev-all"),
    "qa": ("qa-insurance-hub", "qa-data", "qa-svc"),
}
ROLES = ("go_document_reader", "go_document_migrator")
ATTRIBUTES = {
    "rolcanlogin": True,
    "rolsuper": False,
    "rolcreatedb": False,
    "rolcreaterole": False,
    "rolinherit": False,
    "rolreplication": False,
    "rolbypassrls": False,
}
MEMBERSHIP = {
    "role": "document",
    "member": "go_document_migrator",
    "inherit": False,
    "set": True,
    "admin": False,
}
POLICY = {
    "Version": "2012-10-17",
    "Statement": [
        {"Effect": "Allow", "Action": ["s3:GetObject"], "Resource": ["arn:aws:s3:::policies/*"]},
        {
            "Effect": "Allow",
            "Action": ["s3:GetBucketLocation", "s3:ListBucket"],
            "Resource": ["arn:aws:s3:::policies"],
        },
    ],
}
POLICY_NAME = "document-reader"
USER = "document-reader"
MIGRATOR_CA_MOUNT = "/etc/document-admin/migrator/ca.crt"


class ResourceError(Exception):
    """Safe operational failure; never include upstream output or credentials."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise ResourceError(message)


def protected_file(path: Path) -> bytes:
    require(
        path.is_file() and not path.is_symlink() and path.stat().st_mode & 0o077 == 0,
        "protected input must be a regular private file",
    )
    require(path.stat().st_size <= 65536, "protected input is too large")
    return path.read_bytes()


@contextmanager
def local_migration_url(source: Path, ca: Path, port: int):
    """Rebase only the protected operator copy's CA and loopback tunnel."""
    try:
        value = protected_file(source).decode().strip()
        parsed = urlsplit(value)
        query = parse_qsl(parsed.query, keep_blank_values=True, strict_parsing=True)
    except (UnicodeDecodeError, ValueError) as error:
        raise ResourceError("invalid protected migrator credential") from error
    require(
        query == [("sslmode", "verify-full"), ("sslrootcert", MIGRATOR_CA_MOUNT)],
        "migrator credential trust path differs from the canonical Secret mount",
    )
    rebased = urlunsplit(
        parsed._replace(
            query=urlencode(
                [
                    ("sslmode", "verify-full"),
                    ("sslrootcert", str(ca)),
                    ("hostaddr", "127.0.0.1"),
                    ("port", str(port)),
                ],
            )
        )
    )
    with tempfile.TemporaryDirectory(prefix="document-migration-") as directory:
        path = Path(directory) / "database-url"
        path.write_text(rebased)
        path.chmod(0o600)
        yield path


@contextmanager
def migration_database_forward(commands: Commands, target_path: Path):
    target = json.loads(protected_file(target_path))
    require(
        target.get("environment") in ENVIRONMENTS
        and target.get("context") == ENVIRONMENTS[target["environment"]][0]
        and target.get("dataNamespace") == ENVIRONMENTS[target["environment"]][1]
        and target.get("cluster") == f"{target['environment']}-postgres-document",
        "migration target differs from the enrolled platform coordinates",
    )
    cluster = json.loads(
        commands.run(
            [
                os.environ.get("KUBECTL", "kubectl"),
                f"--context={target['context']}",
                "--request-timeout=15s",
                "-n",
                target["dataNamespace"],
                "get",
                "cluster.postgresql.cnpg.io",
                target["cluster"],
                "-o",
                "json",
            ]
        )
    )
    primary = cluster.get("status", {}).get("currentPrimary", "")
    require(
        cluster.get("metadata", {}).get("uid") == target.get("clusterUID")
        and primary
        and cluster.get("status", {}).get("readyInstances", 0) > 0,
        "migration database identity/readiness mismatch",
    )
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    process = subprocess.Popen(
        [
            os.environ.get("KUBECTL", "kubectl"),
            f"--context={target['context']}",
            "-n",
            target["dataNamespace"],
            "port-forward",
            f"pod/{primary}",
            f"{port}:5432",
            "--address=127.0.0.1",
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )
    try:
        for _ in range(40):
            require(process.poll() is None, "migration database forward ended early")
            with socket.socket() as check:
                check.settimeout(0.1)
                if check.connect_ex(("127.0.0.1", port)) == 0:
                    break
            time.sleep(0.1)
        else:
            raise ResourceError("migration database forward did not become ready")
        yield port
    finally:
        require(terminate_process_group(process, 5), "migration database forward cleanup failed")


class Commands:
    """One 180s operation budget, bounded children and signal-safe process cleanup."""

    def __init__(self, timeout: float = 180):
        self.deadline = time.monotonic() + timeout

    def run(
        self,
        args: list[str],
        text: str | None = None,
        env: dict | None = None,
        *,
        missing: tuple[str, ...] = (),
        timeout_cap: float = 30,
    ) -> str:
        remaining = self.deadline - time.monotonic()
        require(remaining > 0, "administration deadline exceeded; inspect before retry")
        try:
            result = run_command(
                args,
                cwd=SERVICE,
                env=env or child_environment(),
                timeout_seconds=min(timeout_cap, remaining),
                activity="document administration",
                input_text=text,
            )
        except CommandError as exc:
            if missing:
                try:
                    records = [json.loads(line) for line in exc.stdout.splitlines() if line.strip()]
                    for record in records:
                        cause = record.get("error", {}).get("cause", {})
                        detail = cause.get("error", {})
                        code = detail.get("Code") or record.get("error", {}).get("code")
                        if code in missing:
                            return ""
                        if (
                            "NoSuchBucket" in missing
                            and detail.get("Bucket") == "policies"
                            and cause.get("message") == "Bucket `policies` does not exist."
                        ):
                            return ""
                except ValueError, TypeError, AttributeError:
                    pass
            raise ResourceError(
                "administration command failed; inspect target safely before retry"
            ) from exc
        require(len(result.stdout) <= 262144, "administration output exceeds safe bound")
        return result.stdout.strip()


def database_url(role: str, password: str, host: str) -> str:
    require(role in ROLES, "unsupported Document role")
    mount = (
        "/etc/document-service/postgres/ca.crt"
        if role == ROLES[0]
        else "/etc/document-admin/migrator/ca.crt"
    )
    return f"postgresql://{role}:{quote(password, safe='')}@{host}:5432/document?sslmode=verify-full&sslrootcert={mount}"


def role_sql(passwords: dict[str, str], existing: set[str], target: dict) -> str:
    # Identifiers are constants; values are quoted independently of shell escaping.
    def literal(value):
        return "'" + str(value).replace("'", "''") + "'"

    statements = [
        "BEGIN",
        "SET LOCAL lock_timeout='2s'",
        "SET LOCAL statement_timeout='10s'",
        "SELECT pg_advisory_xact_lock(190002)",
    ]
    for role in ROLES:
        if role not in existing:
            require(
                len(passwords[role]) == 64 and passwords[role].isalnum(),
                "invalid generated credential",
            )
            statements.append(
                f"CREATE ROLE {role} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD {literal(passwords[role])}"
            )
    statements += [
        "REVOKE ALL ON DATABASE document FROM PUBLIC",
        "REVOKE CONNECT ON DATABASE postgres,template1 FROM PUBLIC",
        "REVOKE ALL ON SCHEMA public FROM PUBLIC",
        "GRANT CONNECT ON DATABASE document TO go_document_reader,go_document_migrator",
        "GRANT USAGE ON SCHEMA public TO go_document_reader",
        "GRANT document TO go_document_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE",
        "CREATE SCHEMA IF NOT EXISTS document_admin AUTHORIZATION postgres",
        "REVOKE ALL ON SCHEMA document_admin FROM PUBLIC",
        "CREATE TABLE IF NOT EXISTS document_admin.target_identity(singleton boolean PRIMARY KEY CHECK(singleton),environment text NOT NULL,instance_id uuid NOT NULL,cluster_uid text NOT NULL)",
        "REVOKE ALL ON document_admin.target_identity FROM PUBLIC",
        "GRANT USAGE ON SCHEMA document_admin TO document,go_document_migrator",
        "GRANT SELECT ON document_admin.target_identity TO document,go_document_migrator",
        "INSERT INTO document_admin.target_identity VALUES(true,"
        + ",".join(literal(target[k]) for k in ("environment", "instanceID", "clusterUID"))
        + ") ON CONFLICT(singleton) DO NOTHING",
        "DO $$ BEGIN IF to_regclass('public.policy_document') IS NOT NULL THEN GRANT SELECT ON public.policy_document TO go_document_reader; END IF; END $$",
        "COMMIT",
    ]
    return ";\n".join(statements) + ";"


class Kubernetes:
    def __init__(self, environment: str, commands: Commands):
        require(environment in ENVIRONMENTS, "DOCUMENT_RESOURCE_ENV must be local-dev or qa")
        self.commands = commands
        self.environment = environment
        self.context, self.data_ns, self.svc_ns = ENVIRONMENTS[environment]
        self.cluster = f"{environment}-postgres-document"
        self.host = f"{self.cluster}-rw.{self.data_ns}.svc.cluster.local"
        self.minio_ns = f"{environment}-minio-document"
        self.endpoint = f"{'https' if environment == 'qa' else 'http'}://minio.{self.minio_ns}.svc.cluster.local:{443 if environment == 'qa' else 80}"
        required = {
            "CONTEXT": self.context,
            "DATA_NS": self.data_ns,
            "SVC_NS": self.svc_ns,
            "CLUSTER": self.cluster,
            "DB_HOST": self.host,
            "MINIO_NS": self.minio_ns,
            "MINIO_ENDPOINT": self.endpoint,
        }
        for key, value in required.items():
            require(
                os.environ.get(f"DOCUMENT_RESOURCE_{key}") == value,
                f"explicit DOCUMENT_RESOURCE_{key} target mismatch",
            )
        for ns in {self.data_ns, self.svc_ns, self.minio_ns}:
            self.kube("get", "namespace", ns, "-o", "name")
        cluster = json.loads(
            self.kube(
                "-n", self.data_ns, "get", "cluster.postgresql.cnpg.io", self.cluster, "-o", "json"
            )
        )
        self.uid = cluster["metadata"]["uid"]
        self.primary = cluster["status"]["currentPrimary"]
        pod = json.loads(self.kube("-n", self.data_ns, "get", "pod", self.primary, "-o", "json"))
        require(
            pod["metadata"]["labels"].get("cnpg.io/cluster") == self.cluster
            and any(
                c.get("type") == "Ready" and c.get("status") == "True"
                for c in pod["status"].get("conditions", [])
            ),
            "database primary identity/readiness mismatch",
        )
        tenant = json.loads(
            self.kube(
                "-n", self.minio_ns, "get", "tenant", f"{environment}-minio-document", "-o", "json"
            )
        )
        expected_image = "cgr.dev/chainguard/minio@sha256:7ad2a5efd83f0ca1b8e00af9f7ec7fc3ff977c38ed29b942b0c47460ad2f8f3e"
        require(
            tenant["spec"].get("image") == expected_image,
            "MinIO selected image differs from accepted security disposition",
        )
        pods = json.loads(
            self.kube(
                "-n",
                self.minio_ns,
                "get",
                "pods",
                "-l",
                f"v1.min.io/tenant={environment}-minio-document",
                "-o",
                "json",
            )
        )
        require(
            pods.get("items")
            and all(
                any(
                    container.get("name") == "minio"
                    and container.get("ready")
                    and container.get("imageID") == expected_image
                    for container in pod.get("status", {}).get("containerStatuses", [])
                )
                for pod in pods["items"]
            ),
            "MinIO running image/readiness differs from accepted target",
        )
        self.target_secret = f"{environment}-postgres-document-admin-target"

    def kube(self, *args: str, text: str | None = None) -> str:
        return self.commands.run(
            [
                os.environ.get("KUBECTL", "kubectl"),
                f"--context={self.context}",
                "--request-timeout=15s",
                *args,
            ],
            text,
        )

    def admin(self, sql: str) -> str:
        return self.kube(
            "-n",
            self.data_ns,
            "exec",
            "-i",
            self.primary,
            "-c",
            "postgres",
            "--",
            "psql",
            "-X",
            "-qAt",
            "-v",
            "ON_ERROR_STOP=1",
            "-U",
            "postgres",
            "-d",
            "document",
            text=sql,
        )

    def secret_name(self, component: str) -> str:
        return f"{self.environment}-{'minio' if component == 'minio-reader' else 'postgres'}-document-{component.removeprefix('minio-')}-creds"

    def secret(self, name: str) -> dict | None:
        raw = self.kube(
            "-n", self.svc_ns, "get", "secret", name, "--ignore-not-found", "-o", "json"
        )
        if not raw:
            return None
        record = json.loads(raw)
        require(
            record["metadata"]["name"] == name
            and record["metadata"].get("labels", {}).get("insurance-hub.io/owner") == "document",
            "foreign Document Secret",
        )
        return {k: base64.b64decode(v, validate=True) for k, v in record.get("data", {}).items()}

    def put_secret(self, name: str, values: dict[str, bytes]) -> None:
        require(
            name
            in {
                self.target_secret,
                *(self.secret_name(c) for c in ("reader", "migrator", "minio-reader")),
            },
            "unsupported Secret target",
        )
        data = {
            "apiVersion": "v1",
            "kind": "Secret",
            "type": "Opaque",
            "metadata": {
                "name": name,
                "namespace": self.svc_ns,
                "labels": {"insurance-hub.io/owner": "document"},
            },
            "data": {k: base64.b64encode(v).decode() for k, v in values.items()},
        }
        existing = self.secret(name)
        if existing is not None:
            require(existing == values, "existing Secret differs; no credential rotation performed")
            return
        # Create is atomic. A concurrent creator must be inspected, never overwritten.
        self.kube("-n", self.svc_ns, "create", "-f", "-", text=json.dumps(data))

    def ca(self) -> bytes:
        raw = json.loads(
            self.kube("-n", self.data_ns, "get", "secret", f"{self.cluster}-ca", "-o", "json")
        )
        ca = base64.b64decode(raw["data"]["ca.crt"], validate=True)
        require(ca.startswith(b"-----BEGIN CERTIFICATE-----"), "CNPG CA is missing")
        return ca

    @contextmanager
    def connection(self, role: str, password: str, ca: bytes):
        require(role in ROLES, "unsupported connection role")
        with tempfile.TemporaryDirectory(prefix="document-db-") as directory:
            root = Path(directory)
            cert, pgpass = root / "ca.crt", root / "pgpass"
            cert.write_bytes(ca)
            pgpass.write_text(f"{self.host}:*:document:{role}:{password}\n")
            cert.chmod(0o600)
            pgpass.chmod(0o600)
            env = child_environment({"PGPASSFILE": str(pgpass), "PGCONNECT_TIMEOUT": "5"})

            def execute(sql: str) -> str:
                with socket.socket() as sock:
                    sock.bind(("127.0.0.1", 0))
                    port = sock.getsockname()[1]
                proc = subprocess.Popen(
                    [
                        os.environ.get("KUBECTL", "kubectl"),
                        f"--context={self.context}",
                        "-n",
                        self.data_ns,
                        "port-forward",
                        f"pod/{self.primary}",
                        f"{port}:5432",
                        "--address=127.0.0.1",
                    ],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    start_new_session=True,
                )
                try:
                    for _ in range(40):
                        require(proc.poll() is None, "database forward ended early")
                        with socket.socket() as sock:
                            sock.settimeout(0.1)
                            if sock.connect_ex(("127.0.0.1", port)) == 0:
                                break
                        time.sleep(0.1)
                    else:
                        raise ResourceError("database forward did not become ready")
                    dsn = f"host={self.host} hostaddr=127.0.0.1 port={port} dbname=document user={role} sslmode=verify-full sslrootcert={cert}"
                    return self.commands.run(
                        ["psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-d", dsn], sql, env
                    )
                finally:
                    require(terminate_process_group(proc, 5), "database forward cleanup failed")

            yield execute


def canonical_policy(value: dict) -> tuple:
    require(
        isinstance(value, dict)
        and value.get("Version") == "2012-10-17"
        and set(value) <= {"Version", "Statement", "Id"},
        "unsupported MinIO policy shape",
    )
    statements = []
    for statement in value.get("Statement", []):
        require(
            set(statement) <= {"Effect", "Action", "Resource", "Sid", "Condition"}
            and not statement.get("Condition"),
            "unexpected MinIO policy conditions",
        )
        action, resource = statement["Action"], statement["Resource"]
        actions = [action] if isinstance(action, str) else action
        items = [resource] if isinstance(resource, str) else resource
        require(
            isinstance(actions, list) and isinstance(items, list),
            "invalid policy actions/resources",
        )
        statements.append((statement["Effect"], tuple(sorted(actions)), tuple(sorted(items))))
    return tuple(sorted(statements))


def authorization_denied(output: str) -> bool:
    # mc maps S3 AccessDenied into a path-permission error for cp/rm/stat.
    return any(
        message in output
        for message in (
            "AccessDenied",
            "Access Denied",
            "Insufficient permissions to access this path",
        )
    )


class Minio:
    def __init__(self, commands: Commands, config: Path, endpoint: str):
        data = json.loads(protected_file(config))
        require(
            config.name == "config.json" and config.parent.stat().st_mode & 0o077 == 0,
            "mc config needs a private directory and config.json filename",
        )
        require(
            data.get("aliases", {}).get("document", {}).get("url") == endpoint,
            "MinIO alias endpoint differs from explicit target",
        )
        require(not os.environ.get("MC_INSECURE"), "insecure MinIO transport rejected")
        self.commands, self.config, self.endpoint = commands, config, endpoint
        if endpoint.startswith("https:"):
            require(bool(self.trust()), "QA MinIO requires an explicitly provisioned CA bundle")

    def trust(self) -> bytes:
        directory = self.config.parent / "certs" / "CAs"
        if not directory.is_dir():
            return b""
        files = sorted(path for path in directory.iterdir() if path.is_file())
        values = [path.read_bytes() for path in files]
        require(
            all(value.startswith(b"-----BEGIN CERTIFICATE-----") for value in values),
            "invalid MinIO trust input",
        )
        return b"\n".join(values)

    def mc(self, *args: str, text: str | None = None, missing: tuple[str, ...] = ()) -> str:
        return self.commands.run(
            [os.environ.get("MC", "mc"), "--json", "--config-dir", str(self.config.parent), *args],
            text,
            missing=missing,
        )

    def inspect(self) -> dict:
        bucket = self.mc("stat", "document/policies", missing=("NoSuchBucket",))
        user = self.mc(
            "admin", "user", "info", "document", USER, missing=("XMinioAdminNoSuchUser",)
        )
        policy = self.mc(
            "admin", "policy", "info", "document", POLICY_NAME, missing=("XMinioAdminNoSuchPolicy",)
        )
        bound = False
        if user:
            record = json.loads(user)
            require(
                record.get("policyName", "") in ("", POLICY_NAME)
                and record.get("userStatus") == "enabled"
                and not record.get("memberOf", []),
                "foreign/incompatible MinIO user or groups",
            )
            bound = record.get("policyName") == POLICY_NAME
        if policy:
            record = json.loads(policy)
            value = record.get("policyInfo", record.get("policy"))
            if isinstance(value, str):
                value = json.loads(value)
            if isinstance(value, dict) and "Policy" in value:
                require(value.get("PolicyName") == POLICY_NAME, "foreign policy identity")
                value = value["Policy"]
            require(
                canonical_policy(value) == canonical_policy(POLICY),
                "foreign/incompatible MinIO reader policy",
            )
        if bucket:
            anonymous = json.loads(self.mc("anonymous", "get-json", "document/policies"))
            require(
                not anonymous.get("policy") and anonymous.get("access", "none") == "none",
                "policies bucket permits anonymous access",
            )
        return {"bucket": bool(bucket), "user": bool(user), "policy": bool(policy), "bound": bound}

    def ensure(self, before: dict, credential: str) -> None:
        with tempfile.TemporaryDirectory(prefix="document-policy-") as directory:
            policy = Path(directory) / "policy.json"
            policy.write_text(json.dumps(POLICY))
            if not before["bucket"]:
                self.mc("mb", "document/policies")
            if not before["policy"]:
                self.mc("admin", "policy", "create", "document", POLICY_NAME, str(policy))
            if not before["user"]:
                self.mc("admin", "user", "add", "document", text=f"{USER}\n{credential}\n")
            if not before["bound"]:
                self.mc("admin", "policy", "attach", "document", POLICY_NAME, "--user", USER)

    @contextmanager
    def reader(self, credential: str):
        with tempfile.TemporaryDirectory(prefix="document-minio-reader-") as directory:
            path = Path(directory) / "config.json"
            value = {
                "version": "10",
                "aliases": {
                    "document": {
                        "url": self.endpoint,
                        "accessKey": USER,
                        "secretKey": credential,
                        "api": "S3v4",
                        "path": "auto",
                    }
                },
            }
            path.write_text(json.dumps(value))
            path.chmod(0o600)
            source = self.config.parent / "certs" / "CAs"
            if source.is_dir():
                (path.parent / "certs").mkdir(mode=0o700)
                shutil.copytree(source, path.parent / "certs" / "CAs")
            yield Minio(self.commands, path, self.endpoint)

    def verify(self, credential: str, *, object_probe: bool) -> None:
        with self.reader(credential) as reader:
            reader.mc("stat", "document/policies")
            if object_probe:
                require(
                    os.environ.get("DOCUMENT_RESOURCE_PROBE_KEY") == "2026/02/SYN-362.pdf",
                    "verify requires the separately provisioned synthetic object fixture",
                )
                with tempfile.TemporaryDirectory(prefix="document-object-probe-") as directory:
                    output = Path(directory) / "probe.pdf"
                    stat = json.loads(reader.mc("stat", "document/policies/2026/02/SYN-362.pdf"))
                    require(stat.get("size") == 8, "synthetic object fixture size mismatch")
                    reader.mc("cp", "document/policies/2026/02/SYN-362.pdf", str(output))
                    require(output.read_bytes() == b"%PDF-362", "synthetic object fixture mismatch")
                for args in (
                    ("stat", "document/unrelated-document-probe"),
                    ("admin", "user", "list", "document"),
                ):
                    try:
                        reader.mc(*args)
                    except ResourceError as error:
                        detail = error.__cause__.stdout
                        require(
                            authorization_denied(detail),
                            "MinIO denied-read/admin probe was not an authorization denial",
                        )
                    else:
                        raise ResourceError("MinIO unrelated/admin read unexpectedly allowed")


def inventory(manager: Kubernetes) -> dict:
    # Catalog only; no business rows or PDF bytes enter administrative output.
    require(
        manager.admin(
            "SELECT NOT EXISTS(SELECT FROM pg_database WHERE datname NOT IN ('document','postgres','template0','template1')) AND NOT EXISTS(SELECT FROM pg_database WHERE datname IN ('postgres','template0','template1') AND datdba<>'postgres'::regrole);"
        )
        == "t",
        "foreign databases or maintenance ownership in Document cluster",
    )
    owned = manager.admin(
        "SELECT pg_get_userbyid(datdba)='document' FROM pg_database WHERE datname=current_database();"
    )
    require(owned == "t", "foreign Document database owner")
    query = (SERVICE / "internal/administration/queries/inspect-schema-objects.sql").read_text()
    flags = manager.admin(query).split("|")
    require(
        len(flags) == 4 and flags[3] == "t" and flags[0] == flags[1],
        "foreign/partial public schema",
    )
    if flags[0] == "t":
        require(
            manager.admin(
                (
                    SERVICE / "internal/administration/queries/inspect-document-schema.sql"
                ).read_text()
            )
            == "t",
            "incompatible Document table",
        )
    history_state = "uninitialized"
    if flags[2] == "t":
        require(
            manager.admin(
                (SERVICE / "internal/administration/queries/inspect-history-schema.sql").read_text()
            )
            == "t",
            "foreign history shape",
        )
        history_state = manager.admin(
            (SERVICE / "internal/administration/queries/inspect-history-rows.sql")
            .read_text()
            .replace("$1", "true" if flags[0] == "t" else "false")
        )
        require(history_state in {"pending", "version=1"}, "inconsistent migration history")
    raw = manager.admin(
        "SELECT coalesce(json_agg(row_to_json(r)),'[]') FROM (SELECT rolname,rolcanlogin,rolsuper,rolcreatedb,rolcreaterole,rolinherit,rolreplication,rolbypassrls FROM pg_roles WHERE rolname IN ('go_document_reader','go_document_migrator')) r;"
    )
    roles = json.loads(raw)
    for role in roles:
        require({k: role[k] for k in ATTRIBUTES} == ATTRIBUTES, "foreign/elevated Document role")
    memberships = json.loads(
        manager.admin(
            "SELECT coalesce(json_agg(json_build_object('role',r.rolname,'member',m.rolname,'inherit',a.inherit_option,'set',a.set_option,'admin',a.admin_option)),'[]') FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member WHERE m.rolname IN ('go_document_reader','go_document_migrator') OR r.rolname IN ('go_document_reader','go_document_migrator');"
        )
    )
    require(memberships in ([], [MEMBERSHIP]), "foreign Document role memberships")
    others = manager.admin(
        "SELECT count(*) FROM pg_roles WHERE rolcanlogin AND rolname NOT IN ('postgres','document','go_document_reader','go_document_migrator','streaming_replica') AND rolname !~ '^pg_';"
    )
    require(others == "0", "unknown database consumers; PUBLIC grants require inspection")
    schemas = manager.admin(
        "SELECT count(*) FROM pg_namespace WHERE nspname NOT IN ('public','document_admin','information_schema') AND nspname !~ '^pg_';"
    )
    require(schemas == "0", "foreign application schema")
    owner_members = manager.admin(
        "SELECT count(*) FROM pg_auth_members WHERE roleid='document'::regrole AND member NOT IN (SELECT oid FROM pg_roles WHERE rolname='go_document_migrator');"
    )
    require(owner_members == "0", "foreign owner role membership")
    require(
        manager.admin(
            "SELECT coalesce((SELECT nspowner='postgres'::regrole FROM pg_namespace WHERE nspname='document_admin'),true) AND NOT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='document_admin' AND c.relname NOT IN ('target_identity','target_identity_pkey')) AND NOT EXISTS(SELECT FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='document_admin');"
        )
        == "t",
        "foreign enrollment schema or objects",
    )
    marker = manager.admin("SELECT to_regclass('document_admin.target_identity') IS NOT NULL;")
    if marker == "t":
        require(
            manager.admin(
                "SELECT c.relowner='postgres'::regrole AND n.nspowner='postgres'::regrole AND c.relkind='r' AND NOT c.relrowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='document_admin.target_identity'::regclass;"
            )
            == "t",
            "foreign enrollment owner",
        )
    if any(role["rolname"] == ROLES[0] for role in roles):
        require(
            manager.admin(
                "SELECT NOT has_database_privilege('go_document_reader','postgres','CONNECT') AND NOT has_database_privilege('go_document_reader','template1','CONNECT');"
            )
            == "t",
            "reader can connect to unrelated maintenance databases",
        )
    if any(role["rolname"] == ROLES[0] for role in roles):
        require(
            manager.admin(
                "SELECT NOT has_database_privilege('go_document_reader','document','CREATE,TEMPORARY') AND NOT has_schema_privilege('go_document_reader','public','CREATE') AND NOT EXISTS(SELECT FROM pg_class WHERE relowner='go_document_reader'::regrole);"
            )
            == "t",
            "reader owns resources or has administrative privileges",
        )
        if flags[0] == "t":
            require(
                manager.admin(
                    "SELECT NOT has_table_privilege('go_document_reader','public.policy_document','INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') AND NOT has_any_column_privilege('go_document_reader','public.policy_document','INSERT,UPDATE,REFERENCES') AND NOT has_sequence_privilege('go_document_reader','public.hibernate_sequence','USAGE,SELECT,UPDATE');"
                )
                == "t",
                "reader has forbidden table or sequence access",
            )
    require(
        manager.admin(
            "SELECT NOT EXISTS(SELECT FROM pg_default_acl d,LATERAL aclexplode(d.defaclacl) a WHERE d.defaclnamespace IN (0,'public'::regnamespace) AND d.defaclobjtype IN ('r','S') AND a.grantee IN (0,(SELECT oid FROM pg_roles WHERE rolname='go_document_reader')));"
        )
        == "t",
        "unexpected PUBLIC/reader default grants",
    )
    if any(role["rolname"] == ROLES[0] for role in roles):
        require(
            manager.admin(
                "SELECT NOT EXISTS(SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname<>'policy_document' AND c.relkind IN ('r','p','v','m','S') AND CASE WHEN c.relkind='S' THEN has_sequence_privilege('go_document_reader',c.oid,'USAGE,SELECT,UPDATE') ELSE has_table_privilege('go_document_reader',c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') END);"
            )
            == "t",
            "reader can access unrelated objects",
        )
    return {
        "table": flags[0] == "t",
        "history": flags[2] == "t",
        "historyState": history_state,
        "roles": [r["rolname"] for r in roles],
        "memberships": memberships,
    }


def target_document(manager: Kubernetes, instance: str) -> dict:
    return {
        "environment": manager.environment,
        "context": manager.context,
        "dataNamespace": manager.data_ns,
        "serviceNamespace": manager.svc_ns,
        "cluster": manager.cluster,
        "clusterUID": manager.uid,
        "instanceID": instance,
        "database": "document",
        "host": manager.host,
        "disposable": manager.environment == "disposable",
    }


def inputs(manager: Kubernetes, state: dict, *, create: bool) -> tuple[dict, dict, bytes]:
    stored = manager.secret(manager.target_secret)
    target = (
        json.loads(stored["target.json"]) if stored else target_document(manager, str(uuid.uuid4()))
    )
    require(
        target == target_document(manager, str(target.get("instanceID", ""))),
        "target enrollment drift",
    )
    uuid.UUID(target["instanceID"])
    marker = manager.admin("SELECT to_regclass('document_admin.target_identity') IS NOT NULL;")
    if marker == "t":
        observed = json.loads(
            manager.admin(
                "SELECT row_to_json(t) FROM (SELECT environment,instance_id,cluster_uid FROM document_admin.target_identity WHERE singleton) t;"
            )
        )
        require(
            stored
            and observed
            == {
                "environment": target["environment"],
                "instance_id": target["instanceID"],
                "cluster_uid": target["clusterUID"],
            },
            "foreign platform enrollment",
        )
    require(create or stored, "target enrollment is missing")
    ca = manager.ca()
    credentials = {}
    for role, component in zip(ROLES, ("reader", "migrator"), strict=True):
        secret = manager.secret(manager.secret_name(component))
        require(
            role not in state["roles"] or secret is not None,
            "role exists without recoverable Secret; no rotation performed",
        )
        if secret:
            require(
                set(secret) == {"database-url", "ca.crt", "password"} and secret["ca.crt"] == ca,
                "credential Secret shape/trust drift",
            )
            password = secret["password"].decode()
            require(
                secret["database-url"].decode() == database_url(role, password, manager.host),
                "credential URL drift",
            )
        else:
            require(create, "credential Secret missing")
            password = secrets.token_hex(32)
        require(len(password) == 64 and password.isalnum(), "credential is not canonical")
        if role in state["roles"]:
            with manager.connection(role, password, ca) as execute:
                require(
                    execute("SELECT current_user;") == role,
                    "existing credential is unusable; no rotation performed",
                )
        credentials[role] = password
    return target, credentials, ca


def verify_reader(execute) -> None:
    execute("SELECT id,policy_number,bytes FROM public.policy_document WHERE false;")
    # Always rolls back even if a forbidden statement unexpectedly succeeds. No sequence calls.
    for statement in (
        "INSERT INTO public.policy_document(id) VALUES(-9223372036854775808)",
        "UPDATE public.policy_document SET bytes=bytes WHERE false",
        "DELETE FROM public.policy_document WHERE false",
        "CREATE TABLE public.document_denied_probe(id integer)",
        "CREATE TEMP TABLE document_denied_probe(id integer)",
        "SELECT * FROM public.hibernate_sequence",
        "SELECT * FROM document_admin.target_identity",
    ):
        literal = "'" + statement.replace("'", "''") + "'"
        result = execute(
            "BEGIN; SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='2s'; DO $$ DECLARE s text := '00000'; BEGIN BEGIN EXECUTE "
            + literal
            + "; EXCEPTION WHEN OTHERS THEN GET STACKED DIAGNOSTICS s=RETURNED_SQLSTATE; END; IF s<>'42501' THEN RAISE EXCEPTION 'privilege denial must be 42501'; END IF; END $$; ROLLBACK;"
        )
        require(not result, "unexpected reader probe output")


def execute_action(action: str, manager: Kubernetes, minio: Minio) -> None:
    state = inventory(manager)
    mstate = minio.inspect()
    if action == "inspect":
        print(
            json.dumps(
                {"environment": manager.environment, "postgres": state, "minio": mstate},
                sort_keys=True,
            )
        )
        return
    if action == "verify":
        require(
            state["table"] and state["historyState"] == "version=1",
            "explicit schema initialization/adoption must precede verification",
        )
    target, credentials, ca = inputs(manager, state, create=action == "ensure")
    msecret_name = manager.secret_name("minio-reader")
    msecret = manager.secret(msecret_name)
    require(
        not mstate["user"] or msecret is not None,
        "MinIO user has no recoverable Secret; no rotation performed",
    )
    if msecret:
        require(
            set(msecret) == {"access-key", "secret-key", "endpoint", "ca.crt"}
            and msecret["access-key"] == USER.encode()
            and msecret["endpoint"] == manager.endpoint.encode()
            and msecret["ca.crt"] == minio.trust(),
            "MinIO credential target drift",
        )
    require(action == "ensure" or msecret, "MinIO credential Secret missing")
    if mstate["user"] and mstate["bound"]:
        minio.verify(msecret["secret-key"].decode(), object_probe=False)
    if action == "ensure":
        # Retain pending credentials first: interrupted setup can resume without password rotation.
        manager.put_secret(
            manager.target_secret, {"target.json": json.dumps(target, sort_keys=True).encode()}
        )
        for role, component in zip(ROLES, ("reader", "migrator"), strict=True):
            manager.put_secret(
                manager.secret_name(component),
                {
                    "password": credentials[role].encode(),
                    "database-url": database_url(role, credentials[role], manager.host).encode(),
                    "ca.crt": ca,
                },
            )
        msecret = msecret or {
            "access-key": USER.encode(),
            "secret-key": secrets.token_hex(32).encode(),
            "endpoint": manager.endpoint.encode(),
            "ca.crt": minio.trust(),
        }
        manager.put_secret(msecret_name, msecret)
        manager.admin(role_sql(credentials, set(state["roles"]), target))
        require(inventory(manager)["memberships"] == [MEMBERSHIP], "role setup verification failed")
        minio.ensure(mstate, msecret["secret-key"].decode())
        require(
            minio.inspect() == {"bucket": True, "user": True, "policy": True, "bound": True},
            "MinIO setup incomplete; rerun with retained credentials",
        )
    else:
        require(
            mstate == {"bucket": True, "user": True, "policy": True, "bound": True},
            "MinIO setup incomplete",
        )
        for role in ROLES:
            with manager.connection(role, credentials[role], ca) as connection:
                require(
                    connection("SELECT current_user;") == role,
                    "effective PostgreSQL login mismatch",
                )
                if role == ROLES[1]:
                    require(
                        connection("SET ROLE document; SELECT current_user;") == "document",
                        "migrator owner membership failed",
                    )
                elif state["table"]:
                    verify_reader(connection)
        minio.verify(msecret["secret-key"].decode(), object_probe=True)
    print(f"document-resources: {action} complete for {manager.environment}")


def migration(commands: Commands) -> None:
    binary = Path(os.environ.get("DOCUMENT_MIGRATOR", ""))
    operation = os.environ.get("DOCUMENT_MIGRATION_OPERATION", "")
    require(
        binary.is_absolute() and binary.is_file() and operation in {"migrate", "status", "version"},
        "explicit migrator binary/operation required",
    )
    env = os.environ.get("DOCUMENT_RESOURCE_ENV", "")
    if operation == "migrate":
        require(
            os.environ.get("DOCUMENT_RESOURCE_CONFIRM") == f"migrate-{env}-document-schema",
            "schema mutation confirmation missing",
        )
    paths = {
        flag: Path(os.environ.get(setting, ""))
        for flag, setting in (
            ("target-file", "DOCUMENT_MIGRATION_TARGET_FILE"),
            ("database-url-file", "DOCUMENT_MIGRATION_URL_FILE"),
            ("ca-file", "DOCUMENT_MIGRATION_CA_FILE"),
        )
    }
    for path in paths.values():
        protected_file(path)
    with (
        migration_database_forward(commands, paths["target-file"]) as port,
        local_migration_url(paths["database-url-file"], paths["ca-file"], port) as url,
    ):
        args = [
            str(binary),
            "--operation",
            operation,
            "--environment",
            env,
            "--context",
            os.environ.get("DOCUMENT_RESOURCE_CONTEXT", ""),
        ]
        effective_paths = {**paths, "database-url-file": url}
        for flag, path in effective_paths.items():
            args += [f"--{flag}", str(path)]
        print(commands.run(args, timeout_cap=180))


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("inspect", "ensure", "verify", "migration"))
    args = parser.parse_args(argv)
    commands = Commands()
    try:
        if args.action == "migration":
            migration(commands)
        else:
            env = os.environ.get("DOCUMENT_RESOURCE_ENV", "")
            if args.action == "ensure":
                require(
                    env in ENVIRONMENTS
                    and os.environ.get("DOCUMENT_RESOURCE_CONFIRM")
                    == f"ensure-{env}-document-resources",
                    "resource mutation confirmation missing",
                )
            manager = Kubernetes(env, commands)
            minio = Minio(
                commands, Path(os.environ.get("DOCUMENT_MINIO_ADMIN_CONFIG", "")), manager.endpoint
            )
            execute_action(args.action, manager, minio)
        return 0
    except ResourceError, OSError, ValueError, KeyError, TypeError:
        print(
            "document-resources: operation failed; inspect protected target/partial setup before retry",
            file=sys.stderr,
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="document-resources"))
