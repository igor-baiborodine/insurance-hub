"""Inspect, ensure and verify the Policy PostgreSQL administration boundary.

Run through `make -C services/policy-service resource-{inspect,ensure,verify}`. Every target
coordinate is explicit and must equal the selected environment's contract:

    POLICY_RESOURCE_ENV        local-dev or qa
    POLICY_RESOURCE_CONTEXT    Kubernetes context
    POLICY_RESOURCE_DATA_NS    CloudNativePG namespace
    POLICY_RESOURCE_SVC_NS     Policy workload namespace
    POLICY_RESOURCE_CLUSTER    CloudNativePG cluster
    POLICY_RESOURCE_DB_HOST    read-write service host
    POLICY_RESOURCE_CONFIRM    ensure only: ensure-<environment>-policy-resources

`inspect` prints one JSON document and never mutates. `ensure` publishes recoverable Secrets
before it creates or updates roles, so an interrupted run is retried with the same credentials,
then runs `verify`. `verify` reads catalog state and probes each login's denials inside
rolled-back transactions. Each child command has a 30-second deadline; the CNPG primary is
reached through `kubectl exec` (administrator) or an owned loopback port-forward (logins).
Exit status: 0 success, 1 operational failure, 2 usage error, 130/143 interruption.
"""

from __future__ import annotations

import base64
import binascii
import json
import os
import re
import secrets
import sys
import tempfile
import uuid
from collections.abc import Callable, Iterator, Sequence
from contextlib import contextmanager
from pathlib import Path
from urllib.parse import quote, unquote, urlencode, urlsplit

from scripts.go.service.tooling_contracts import (
    AdministrationError,
    OwnedPortForward,
    PortForwardSpec,
    loopback_port,
)
from scripts.go.service.tooling_process import run_cli
from scripts.python.process_runner import CommandError, child_environment, run_command

MIGRATOR = "go_policy_migrator"
WRITER = "go_policy_writer"
ROLES = (MIGRATOR, WRITER)
CA_MOUNTS = {
    MIGRATOR: "/etc/policy-admin/migrator/ca.crt",
    WRITER: "/etc/policy-service/postgres/ca.crt",
}
SECRET_SUFFIX = {MIGRATOR: "migrator-creds", WRITER: "writer-creds"}
COMPONENTS = {MIGRATOR: "postgres-migrator", WRITER: "postgres-writer"}
ENVIRONMENTS = {
    "local-dev": ("kind-local-dev-insurance-hub", "local-dev-all", "local-dev-all"),
    "qa": ("qa-insurance-hub", "qa-data", "qa-svc"),
}
COMMAND_SECONDS = 30
PROBE_STATE = re.compile(r"policy-probe-state:([0-9A-Z]{5})")
ADMINISTRATION = Path(__file__).resolve().parents[1] / "internal/administration"
PROVISION_SQL = ADMINISTRATION / "provisioning/provision-access.sql"
INSPECT_ACCESS_SQL = ADMINISTRATION / "provisioning/inspect-access.sql"
WRITER_GRANTS_SQL = ADMINISTRATION / "queries/apply-writer-grants.sql"
MODULE_ROOT = Path(__file__).resolve().parents[1]
MIGRATION_SECONDS = 180
MIGRATION_OPERATIONS = {"migrate", "status"}
MANAGED_TABLES = (
    "offer",
    "offer_answers",
    "offer_cover",
    "policy",
    "policy_version",
    "cover",
    "policy_outbox",
)

BOUNDARY_SQL = """
SELECT current_database() = 'policy'
  AND EXISTS (SELECT FROM pg_database WHERE datname = current_database()
      AND datdba = 'policy'::regrole)
  AND EXISTS (SELECT FROM pg_namespace WHERE nspname = 'public'
      AND nspowner IN ('policy'::regrole, 'pg_database_owner'::regrole))
  AND NOT EXISTS (SELECT FROM pg_namespace WHERE nspname = 'policy_admin'
      AND nspowner <> 'postgres'::regrole)
  AND NOT EXISTS (
      SELECT FROM pg_roles WHERE rolcanlogin AND rolname !~ '^pg_'
      AND rolname NOT IN ('policy', 'postgres', 'streaming_replica',
          'go_policy_migrator', 'go_policy_writer')
      AND has_database_privilege(oid, current_database(), 'CONNECT'))
  AND NOT EXISTS (
      SELECT FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
      WHERE n.nspname = 'public' AND c.relowner <> 'policy'::regrole);
"""

MARKER_PRESENT_SQL = "SELECT to_regclass('policy_admin.target_identity') IS NOT NULL;"

# Run only after MARKER_PRESENT_SQL: PostgreSQL resolves the marker relation while parsing.
MARKER_SQL = """
SELECT CASE WHEN NOT (SELECT c.relkind = 'r' AND NOT c.relrowsecurity
        AND c.relowner = 'postgres'::regrole AND n.nspowner = 'postgres'::regrole
        AND (SELECT count(*) = 4 FROM pg_attribute WHERE attrelid = c.oid AND attnum > 0
            AND NOT attisdropped)
        FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.oid = to_regclass('policy_admin.target_identity')) THEN 'invalid'
    ELSE coalesce((SELECT row_to_json(t)::text FROM (
        SELECT environment, instance_id, cluster_uid FROM policy_admin.target_identity
        WHERE singleton) t), 'empty')
END;
"""

ROLES_SQL = (
    "SELECT coalesce(string_agg(rolname, ',' ORDER BY rolname), '') FROM pg_roles "
    "WHERE rolname IN ('go_policy_migrator', 'go_policy_writer');"
)
MIGRATED_SQL = "SELECT to_regclass('public.policy_outbox') IS NOT NULL;"


class ResourceError(Exception):
    pass


def require(condition: object, message: str) -> None:
    if not condition:
        raise ResourceError(message)


def sql_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def kubernetes_environment(overrides: dict[str, str] | None = None) -> dict[str, str]:
    """Allowlisted child environment plus the caller's selected kubeconfig, if any."""
    selected = {key: os.environ[key] for key in ("KUBECONFIG",) if os.environ.get(key)}
    return child_environment({**selected, **(overrides or {})})


def command(
    argv: Sequence[str],
    *,
    input_text: str | None = None,
    sensitive: Sequence[str] = (),
    environment: dict[str, str] | None = None,
    cwd: Path | None = None,
    timeout: float = COMMAND_SECONDS,
) -> tuple[str, str]:
    """Run one bounded child. Diagnostics name the tool only, never its output or input."""
    try:
        result = run_command(
            argv,
            cwd=cwd or Path.cwd(),
            env=kubernetes_environment(environment),
            timeout_seconds=timeout,
            activity=f"policy-resources {Path(argv[0]).name}",
            input_text=input_text,
            sensitive_values=tuple(value for value in sensitive if value),
        )
    except CommandError as error:
        raise ResourceError(f"{Path(argv[0]).name} failed; inspect the target safely") from error
    except OSError as error:
        raise ResourceError(f"{Path(argv[0]).name} is unavailable") from error
    return result.stdout.strip(), result.stderr


def probe_sql(statement: str) -> str:
    """Attempt one statement in a rolled-back transaction and report only its SQLSTATE."""
    return f"""
BEGIN;
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '2s';
DO $probe$
DECLARE observed text := '00000';
BEGIN
  BEGIN
    EXECUTE {sql_literal(statement)};
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS observed = RETURNED_SQLSTATE;
  END;
  RAISE NOTICE 'policy-probe-state:%', observed;
END $probe$;
ROLLBACK;
"""


class Kubernetes:
    """One explicitly selected Policy CNPG target and its service-namespace Secrets."""

    def __init__(self, environment: str):
        require(environment in ENVIRONMENTS, "POLICY_RESOURCE_ENV must be local-dev or qa")
        self.environment = environment
        self.context, self.data_ns, self.svc_ns = ENVIRONMENTS[environment]
        self.kubectl_path = os.environ.get("KUBECTL", "kubectl")
        self.cluster = f"{environment}-postgres-policy"
        self.ca_name = f"{environment}-postgres-policy-ca"
        self.target_secret = f"{environment}-postgres-policy-admin-target"
        self.hostname = f"{self.cluster}-rw.{self.data_ns}.svc.cluster.local"
        expected = {
            "POLICY_RESOURCE_CONTEXT": self.context,
            "POLICY_RESOURCE_DATA_NS": self.data_ns,
            "POLICY_RESOURCE_SVC_NS": self.svc_ns,
            "POLICY_RESOURCE_CLUSTER": self.cluster,
            "POLICY_RESOURCE_DB_HOST": self.hostname,
        }
        for key, value in expected.items():
            require(os.environ.get(key) == value, f"{key} must explicitly equal {value}")
        for namespace in {self.data_ns, self.svc_ns}:
            self.kubectl("get", "namespace", namespace, "-o", "name")
        cluster = json.loads(
            self.kubectl(
                "-n", self.data_ns, "get", "cluster.postgresql.cnpg.io", self.cluster, "-o", "json"
            )
        )
        require(cluster["metadata"]["name"] == self.cluster, "database cluster identity mismatch")
        self.cluster_uid = cluster["metadata"].get("uid", "")
        require(
            re.fullmatch(r"[0-9a-f-]{36}", self.cluster_uid), "database cluster UID is unavailable"
        )
        self.primary = cluster.get("status", {}).get("currentPrimary", "")
        require(re.fullmatch(r"[a-z0-9-]+", self.primary), "database primary is not reported")
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

    def kubectl(self, *args: str, input_text: str | None = None, sensitive=()) -> str:
        argv = [self.kubectl_path, f"--context={self.context}", *args]
        return command(argv, input_text=input_text, sensitive=sensitive)[0]

    def admin(self, sql: str, *, sensitive: Sequence[str] = ()) -> str:
        """Run SQL as the CNPG superuser inside the primary; SQL travels on stdin only."""
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
            "policy",
            input_text=sql,
            sensitive=sensitive,
        )

    def role_secret(self, role: str) -> str:
        return f"{self.environment}-postgres-policy-{SECRET_SUFFIX[role]}"

    def get_secret(self, name: str, namespace: str) -> dict[str, bytes] | None:
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
        if name == self.ca_name:
            require(namespace == self.data_ns, "database CA Secret namespace differs")
        elif namespace == self.svc_ns:
            labels = record["metadata"].get("labels", {})
            require(
                labels.get("insurance-hub.io/owner") == "policy"
                and labels.get("app.kubernetes.io/name") == "policy-api",
                f"Secret {name} is not owned by Policy",
            )
        try:
            return {
                key: base64.b64decode(value, validate=True)
                for key, value in record.get("data", {}).items()
            }
        except (ValueError, TypeError, binascii.Error) as error:
            raise ResourceError("Secret data could not be decoded") from error

    def put_secret(self, name: str, component: str, values: dict[str, bytes]) -> None:
        payload = {
            "apiVersion": "v1",
            "kind": "Secret",
            "type": "Opaque",
            "metadata": {
                "name": name,
                "namespace": self.svc_ns,
                "labels": {
                    "app.kubernetes.io/name": "policy-api",
                    "app.kubernetes.io/component": component,
                    "insurance-hub.io/owner": "policy",
                },
            },
            "data": {key: base64.b64encode(value).decode() for key, value in values.items()},
        }
        sensitive = [value.decode() for value in values.values()]
        self.kubectl(
            "-n",
            self.svc_ns,
            "create",
            "-f",
            "-",
            input_text=json.dumps(payload),
            sensitive=sensitive,
        )

    def ca(self) -> bytes:
        source = self.get_secret(self.ca_name, self.data_ns)
        require(
            source is not None
            and source.get("ca.crt", b"").startswith(b"-----BEGIN CERTIFICATE-----"),
            "database CA Secret has no usable ca.crt",
        )
        return source["ca.crt"]

    @contextmanager
    def role_connection(
        self, role: str, password: str, ca: bytes
    ) -> Iterator[Callable[[str], tuple[str, str]]]:
        """Log in as one Policy role over verified TLS through an owned loopback forward."""
        require(role in ROLES, "unsupported Policy database role")
        with tempfile.TemporaryDirectory(prefix="policy-resources-") as directory:
            root = Path(directory)
            cert, passfile = root / "ca.crt", root / "pgpass"
            for path, content in (
                (cert, ca),
                (passfile, f"*:*:policy:{role}:{password}\n".encode()),
            ):
                descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                with os.fdopen(descriptor, "wb") as stream:
                    stream.write(content)
            environment = {"PGPASSFILE": str(passfile), "PGCONNECT_TIMEOUT": "5"}

            def execute(sql: str) -> tuple[str, str]:
                port = loopback_port()
                spec = PortForwardSpec(
                    self.context, self.data_ns, f"pod/{self.primary}", port, 5432
                )
                with OwnedPortForward(
                    spec,
                    cwd=Path.cwd(),
                    env=kubernetes_environment(),
                    startup_seconds=10,
                    kubectl=self.kubectl_path,
                ):
                    return command(
                        [
                            "psql",
                            "-X",
                            "-q",
                            "-A",
                            "-t",
                            "-v",
                            "ON_ERROR_STOP=1",
                            "-d",
                            f"host={self.hostname} hostaddr=127.0.0.1 port={port} "
                            f"dbname=policy user={role} sslmode=verify-full sslrootcert={cert}",
                        ],
                        input_text=sql,
                        sensitive=(password,),
                        environment=environment,
                    )

            yield execute


def database_url(role: str, password: str, hostname: str) -> str:
    require(role in ROLES, "unsupported Policy database role")
    return (
        f"postgresql://{role}:{quote(password, safe='')}@{hostname}:5432/policy"
        f"?sslmode=verify-full&sslrootcert={quote(CA_MOUNTS[role], safe='/')}"
    )


def password_from_url(role: str, value: str, hostname: str) -> str:
    try:
        password = unquote(urlsplit(value).password or "")
    except ValueError as error:
        raise ResourceError(f"{role} Secret URL is malformed") from error
    require(
        re.fullmatch(r"[A-Za-z0-9_-]{64}", password)
        and database_url(role, password, hostname) == value,
        f"{role} Secret URL differs from the canonical role/TLS contract",
    )
    return password


def target_document(manager: Kubernetes, instance_id: str) -> dict[str, object]:
    return {
        "environment": manager.environment,
        "context": manager.context,
        "dataNamespace": manager.data_ns,
        "serviceNamespace": manager.svc_ns,
        "cluster": manager.cluster,
        "clusterUID": manager.cluster_uid,
        "instanceID": instance_id,
        "database": "policy",
        "host": manager.hostname,
        "disposable": False,
    }


def marker_document(target: dict[str, object]) -> dict[str, object]:
    return {
        "environment": target["environment"],
        "instance_id": target["instanceID"],
        "cluster_uid": target["clusterUID"],
    }


def read_marker(manager: Kubernetes) -> dict[str, object] | None:
    if manager.admin(MARKER_PRESENT_SQL) != "t":
        return None
    value = manager.admin(MARKER_SQL)
    require(value != "invalid", "database enrollment marker has unexpected ownership or shape")
    return None if value == "empty" else json.loads(value)


def existing_roles(manager: Kubernetes) -> set[str]:
    value = manager.admin(ROLES_SQL)
    return set(value.split(",")) if value else set()


def read_inputs(
    manager: Kubernetes,
) -> tuple[dict[str, str], dict[str, dict[str, bytes] | None], dict[str, object], bool]:
    """Return passwords, existing role Secrets, the target and whether it was already stored."""
    stored = {role: manager.get_secret(manager.role_secret(role), manager.svc_ns) for role in ROLES}
    passwords: dict[str, str] = {}
    for role, value in stored.items():
        if value is None:
            passwords[role] = secrets.token_urlsafe(48)
            continue
        require(set(value) == {"database-url", "ca.crt"}, f"{role} Secret has unexpected keys")
        passwords[role] = password_from_url(role, value["database-url"].decode(), manager.hostname)
    raw_target = manager.get_secret(manager.target_secret, manager.svc_ns)
    if raw_target is None:
        return passwords, stored, target_document(manager, str(uuid.uuid4())), False
    require(set(raw_target) == {"target.json"}, "target Secret has unexpected keys")
    target = json.loads(raw_target["target.json"])
    require(
        isinstance(target, dict)
        and target == target_document(manager, str(target.get("instanceID", ""))),
        "target Secret differs from the current cluster identity",
    )
    return passwords, stored, target, True


def role_sql(passwords: dict[str, str], existing: set[str], target: dict[str, object]) -> str:
    """Create or normalize both logins and apply the shared provisioning in one transaction."""
    attributes = "LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS"
    statements = ["BEGIN", "SET LOCAL lock_timeout = '2s'", "SET LOCAL statement_timeout = '10s'"]
    for role in ROLES:
        if role in existing:
            statements.append(f"ALTER ROLE {role} {attributes}")
        else:
            statements.append(
                f"CREATE ROLE {role} {attributes} PASSWORD {sql_literal(passwords[role])}"
            )
    statements += [
        f"SET LOCAL policy.environment = {sql_literal(str(target['environment']))}",
        f"SET LOCAL policy.instance_id = {sql_literal(str(target['instanceID']))}",
        f"SET LOCAL policy.cluster_uid = {sql_literal(str(target['clusterUID']))}",
    ]
    grants = WRITER_GRANTS_SQL.read_text()
    return (
        ";\n".join(statements)
        + ";\n"
        + PROVISION_SQL.read_text()
        + "\nDO $existing$\nBEGIN\n    IF to_regclass('public.policy_outbox') IS NOT NULL THEN\n"
        + f"        EXECUTE $sql${grants}$sql$;\n    END IF;\nEND $existing$;\nCOMMIT;\n"
    )


def secret_values(role: str, password: str, hostname: str, ca: bytes) -> dict[str, bytes]:
    return {"database-url": database_url(role, password, hostname).encode(), "ca.crt": ca}


def ensure(manager: Kubernetes) -> None:
    require(manager.admin(BOUNDARY_SQL) == "t", "Policy database ownership or consumers differ")
    roles = existing_roles(manager)
    passwords, stored, target, target_stored = read_inputs(manager)
    ca = manager.ca()
    for role, value in stored.items():
        require(value is None or value["ca.crt"] == ca, f"{role} Secret CA differs from CNPG CA")
        require(role not in roles or value is not None, f"{role} exists without its Secret")
    marker = read_marker(manager)
    require(
        marker is None or marker == marker_document(target),
        "database enrollment marker differs from the protected target",
    )

    # Publish recoverable inputs before enabling any new role; a retry reuses them.
    if not target_stored:
        manager.put_secret(
            manager.target_secret,
            "postgres-administration",
            {"target.json": json.dumps(target, sort_keys=True).encode()},
        )
    for role, value in stored.items():
        if value is None:
            manager.put_secret(
                manager.role_secret(role),
                COMPONENTS[role],
                secret_values(role, passwords[role], manager.hostname, ca),
            )
    manager.admin(role_sql(passwords, roles, target), sensitive=tuple(passwords.values()))
    verify(manager)
    print(f"policy-resources: ensured {manager.environment} roles, grants and Secrets")


def deny(connection: Callable[[str], tuple[str, str]], statement: str) -> None:
    state = PROBE_STATE.search(connection(probe_sql(statement))[1])
    require(
        state is not None and state.group(1) == "42501",
        "Policy role privilege denial did not report SQLSTATE 42501",
    )


def verify(manager: Kubernetes) -> None:
    require(manager.admin(BOUNDARY_SQL) == "t", "Policy database ownership or consumers differ")
    passwords, stored, target, target_stored = read_inputs(manager)
    require(
        target_stored and all(value is not None for value in stored.values()),
        "Policy administration Secrets are incomplete",
    )
    require(
        read_marker(manager) == marker_document(target),
        "database enrollment marker differs from the protected target",
    )
    require(manager.admin(INSPECT_ACCESS_SQL.read_text()) == "t", "Policy grants differ")
    ca = manager.ca()
    migrated = manager.admin(MIGRATED_SQL) == "t"
    for role, value in stored.items():
        require(value["ca.crt"] == ca, f"{role} Secret CA differs from CNPG CA")
        with manager.role_connection(role, passwords[role], ca) as connection:
            require(connection("SELECT current_user;")[0] == role, "effective role differs")
            if role == MIGRATOR:
                require(
                    connection("SET ROLE policy; SELECT current_user;")[0] == "policy",
                    "migrator cannot assume the Policy owner role",
                )
                continue
            deny(connection, "CREATE TABLE public.denied_policy (value integer)")
            deny(connection, "SET ROLE policy")
            deny(connection, "SELECT count(*) FROM policy_admin.target_identity")
            if migrated:
                connection(
                    "".join(f"SELECT count(*) FROM public.{name};" for name in MANAGED_TABLES)
                )
                deny(connection, "DELETE FROM public.policy WHERE false")
                deny(connection, "ALTER TABLE public.policy ADD COLUMN denied integer")
                deny(connection, "SELECT count(*) FROM public.goose_db_version")
    print(f"policy-resources: verified {manager.environment} roles, grants and SQL denials")


def inspect(manager: Kubernetes) -> dict[str, object]:
    _, stored, target, target_stored = read_inputs(manager)
    ca = manager.ca()
    marker = read_marker(manager)
    roles = existing_roles(manager)
    return {
        "databaseCompatible": manager.admin(BOUNDARY_SQL) == "t",
        "roles": sorted(roles),
        "targetSecret": "present" if target_stored else "missing",
        "credentials": {
            role: "missing"
            if value is None
            else "present"
            if value["ca.crt"] == ca
            else "CA differs"
            for role, value in stored.items()
        },
        "marker": "missing"
        if marker is None
        else "matches"
        if target_stored and marker == marker_document(target)
        else "differs",
        "migrated": manager.admin(MIGRATED_SQL) == "t",
        "accessMatches": len(roles) == len(ROLES)
        and marker is not None
        and manager.admin(INSPECT_ACCESS_SQL.read_text()) == "t",
    }


def tunnel_url(value: str, hostname: str, port: int, ca_path: Path) -> str:
    """Point the canonical migrator URL at a loopback tunnel; TLS still verifies the host name."""
    password = password_from_url(MIGRATOR, value, hostname)
    return (
        f"postgresql://{MIGRATOR}:{quote(password, safe='')}@{hostname}:5432/policy?"
        + urlencode(
            {
                "sslmode": "verify-full",
                "sslrootcert": str(ca_path),
                "hostaddr": "127.0.0.1",
                "port": str(port),
            }
        )
    )


def migration(manager: Kubernetes, operation: str) -> str:
    """Run the Policy migrator from this checkout against the enrolled target's primary."""
    _, stored, target, target_stored = read_inputs(manager)
    credentials = stored[MIGRATOR]
    require(
        target_stored and credentials is not None,
        "Policy enrollment or migrator Secret is missing; run resource-ensure first",
    )
    require(credentials["ca.crt"] == manager.ca(), "migrator Secret CA differs from CNPG CA")
    port = loopback_port()
    spec = PortForwardSpec(manager.context, manager.data_ns, f"pod/{manager.primary}", port, 5432)
    with (
        tempfile.TemporaryDirectory(prefix="policy-migration-") as directory,
        OwnedPortForward(
            spec,
            cwd=MODULE_ROOT,
            env=kubernetes_environment(),
            startup_seconds=10,
            kubectl=manager.kubectl_path,
        ),
    ):
        root = Path(directory)
        paths = {name: root / name for name in ("target.json", "ca.crt", "database-url")}
        url = tunnel_url(
            credentials["database-url"].decode(), manager.hostname, port, paths["ca.crt"]
        )
        for name, content in (
            ("target.json", json.dumps(target).encode()),
            ("ca.crt", credentials["ca.crt"]),
            ("database-url", url.encode()),
        ):
            descriptor = os.open(paths[name], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, "wb") as stream:
                stream.write(content)
        argv = [
            os.environ.get("GO", "go"),
            "run",
            "-mod=readonly",
            "./cmd/migrate",
            "-operation",
            operation,
            "-environment",
            manager.environment,
            "-context",
            manager.context,
            "-target-file",
            str(paths["target.json"]),
            "-database-url-file",
            str(paths["database-url"]),
            "-ca-file",
            str(paths["ca.crt"]),
        ]
        try:
            result = run_command(
                argv,
                cwd=MODULE_ROOT,
                env=child_environment({"GOWORK": "off"}, preferred_executables=argv[:1]),
                timeout_seconds=MIGRATION_SECONDS,
                activity=f"policy-migrate {operation}",
                sensitive_values=(urlsplit(url).password or "",),
            )
        except CommandError as error:
            # The migrator reports only redacted, operator-safe diagnostics.
            raise ResourceError(
                f"policy-migrate {operation} refused: {error.stderr.strip()[-600:]}"
            ) from error
    return result.stdout.strip()


def main(argv: Sequence[str] | None = None) -> int:
    arguments = list(sys.argv[1:] if argv is None else argv)
    if len(arguments) != 1 or arguments[0] not in {"inspect", "ensure", "verify", "migration"}:
        print("policy-resources: expected inspect, ensure, verify or migration", file=sys.stderr)
        return 2
    action = arguments[0]
    environment = os.environ.get("POLICY_RESOURCE_ENV", "")
    require(environment in ENVIRONMENTS, "POLICY_RESOURCE_ENV must be local-dev or qa")
    if action == "ensure":
        expected = f"ensure-{environment}-policy-resources"
        require(
            os.environ.get("POLICY_RESOURCE_CONFIRM") == expected,
            f"POLICY_RESOURCE_CONFIRM must equal {expected}",
        )
    operation = os.environ.get("POLICY_MIGRATION_OPERATION", "")
    if action == "migration":
        require(
            operation in MIGRATION_OPERATIONS,
            "POLICY_MIGRATION_OPERATION must be migrate or status",
        )
        expected = f"migrate-{environment}-policy-schema"
        require(
            operation == "status" or os.environ.get("POLICY_MIGRATION_CONFIRM") == expected,
            f"POLICY_MIGRATION_CONFIRM must equal {expected}",
        )
    manager = Kubernetes(environment)
    if action == "migration":
        print(migration(manager, operation))
    elif action == "inspect":
        print(json.dumps(inspect(manager), sort_keys=True))
    elif action == "ensure":
        ensure(manager)
    else:
        verify(manager)
    return 0


def cli(argv: Sequence[str] | None = None) -> int:
    def execute() -> int:
        try:
            return main(argv)
        except (ResourceError, AdministrationError) as error:
            print(f"policy-resources: {error}", file=sys.stderr)
        except OSError, ValueError, KeyError, TypeError, AttributeError:
            print("policy-resources: resource data or local operation failed", file=sys.stderr)
        return 1

    return run_cli(execute, name="policy-resources")


if __name__ == "__main__":
    raise SystemExit(cli())
