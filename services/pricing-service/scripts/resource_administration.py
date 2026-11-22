"""Inspect, ensure and verify the Pricing PostgreSQL administration boundary."""

from __future__ import annotations

import base64
import json
import os
import re
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import uuid
from collections.abc import Sequence
from contextlib import contextmanager
from pathlib import Path
from urllib.parse import quote, unquote, urlsplit

import administration as reader
from scripts.go.service.tooling_process import run_cli

ROLES = ("go_pricing_reader", "go_pricing_migrator", "go_pricing_seeder")
CA_MOUNTS = {
    "go_pricing_reader": reader.CA_MOUNT,
    "go_pricing_migrator": "/etc/pricing-admin/migrator/ca.crt",
    "go_pricing_seeder": "/etc/pricing-admin/seeder/ca.crt",
}
SECRET_SUFFIX = {
    "go_pricing_reader": "reader-creds",
    "go_pricing_migrator": "migrator-creds",
    "go_pricing_seeder": "seeder-creds",
}


def database_url(role: str, password: str, hostname: str) -> str:
    reader.require(role in ROLES, "unsupported Pricing database role")
    return (
        f"postgresql://{role}:{quote(password, safe='')}@{hostname}:5432/pricing"
        f"?sslmode=verify-full&sslrootcert={quote(CA_MOUNTS[role], safe='/')}"
    )


def password_from_url(role: str, value: str, hostname: str) -> str:
    try:
        parsed = urlsplit(value)
        password = unquote(parsed.password or "")
        valid = (
            parsed.scheme == "postgresql"
            and parsed.username == role
            and parsed.hostname == hostname
            and parsed.port == 5432
            and parsed.path == "/pricing"
            and parsed.query
            == f"sslmode=verify-full&sslrootcert={quote(CA_MOUNTS[role], safe='/')}"
        )
    except ValueError as error:
        raise reader.RoleError("administration Secret URL is malformed") from error
    reader.require(valid, "administration Secret URL differs from the role/TLS contract")
    reader.require(
        bool(re.fullmatch(r"[A-Za-z0-9_-]{64}", password))
        and database_url(role, password, hostname) == value,
        "administration Secret URL is not canonical",
    )
    return password


def target_document(manager: AdministrationKubernetes, instance_id: str) -> dict[str, object]:
    return {
        "environment": manager.environment,
        "context": manager.context,
        "dataNamespace": manager.data_ns,
        "serviceNamespace": manager.svc_ns,
        "cluster": manager.cluster,
        "clusterUID": manager.cluster_uid,
        "instanceID": instance_id,
        "database": "pricing",
        "host": manager.hostname,
        "seedAllowed": manager.environment in {"local-dev", "qa"},
        "disposable": False,
    }


class AdministrationKubernetes(reader.Kubernetes):
    """Exact Pricing role target plus named Secret operations."""

    def __init__(self, environment: str):
        super().__init__(environment)
        self.target_secret = f"{environment}-postgres-pricing-admin-target"

    def get_secret(self, name: str, namespace: str, *, owner: str | None = None):
        if namespace == self.svc_ns and (
            name == self.target_secret or any(name == self.role_secret(role) for role in ROLES)
        ):
            owner = "pricing"
        return super().get_secret(name, namespace, owner=owner)

    def role_secret(self, role: str) -> str:
        return f"{self.environment}-postgres-pricing-{SECRET_SUFFIX[role]}"

    def put_named_secret(self, name: str, values: dict[str, bytes]) -> None:
        components = {
            self.role_secret("go_pricing_reader"): "postgres-reader",
            self.role_secret("go_pricing_migrator"): "postgres-migrator",
            self.role_secret("go_pricing_seeder"): "postgres-seeder",
            self.target_secret: "postgres-administration",
        }
        reader.require(name in components, "unsupported Pricing resource Secret")
        payload = {
            "apiVersion": "v1",
            "kind": "Secret",
            "type": "Opaque",
            "metadata": {
                "name": name,
                "namespace": self.svc_ns,
                "labels": {
                    "app.kubernetes.io/name": "pricing-api",
                    "app.kubernetes.io/component": components[name],
                    "insurance-hub.io/owner": "pricing",
                },
            },
            "data": {key: base64.b64encode(value).decode() for key, value in values.items()},
        }
        self.kubectl("-n", self.svc_ns, "create", "-f", "-", input_text=json.dumps(payload))

    @contextmanager
    def role_connection(self, role: str, password: str, ca: bytes):
        reader.require(role in ROLES, "unsupported Pricing database role")
        with tempfile.TemporaryDirectory(prefix="pricing-administration-") as directory:
            root = Path(directory)
            cert = root / "ca.crt"
            passfile = root / "pgpass"
            descriptor = os.open(cert, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, "wb") as stream:
                stream.write(ca)
            descriptor = os.open(passfile, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, "w") as stream:
                stream.write(f"{self.hostname}:*:pricing:{role}:{password}\n")

            def execute(sql: str) -> str:
                with socket.socket() as listener:
                    listener.bind(("127.0.0.1", 0))
                    port = listener.getsockname()[1]
                process = subprocess.Popen(
                    [
                        reader.KUBECTL,
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
                )
                try:
                    for _ in range(40):
                        if process.poll() is not None:
                            raise reader.RoleError("administration port-forward ended early")
                        with socket.socket() as check:
                            check.settimeout(0.1)
                            if check.connect_ex(("127.0.0.1", port)) == 0:
                                break
                        time.sleep(0.1)
                    else:
                        raise reader.RoleError("administration port-forward did not become ready")
                    options = (
                        f"host={self.hostname} hostaddr=127.0.0.1 port={port} "
                        f"dbname=pricing user={role} sslmode=verify-full sslrootcert={cert}"
                    )
                    environment = {
                        key: value for key, value in os.environ.items() if not key.startswith("PG")
                    }
                    environment.update(PGPASSFILE=str(passfile), PGCONNECT_TIMEOUT="5")
                    return reader.command(
                        ["psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-d", options],
                        input_text=sql,
                        env=environment,
                    )
                finally:
                    process.terminate()
                    try:
                        process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=5)

            yield execute


def role_inventory(manager: AdministrationKubernetes) -> dict[str, object]:
    roles = json.loads(
        manager.admin(
            "SELECT coalesce(json_agg(row_to_json(r) ORDER BY rolname),'[]') FROM ("
            "SELECT rolname,rolcanlogin,rolsuper,rolcreatedb,rolcreaterole,rolinherit,"
            "rolreplication,rolbypassrls FROM pg_roles WHERE rolname IN "
            "('go_pricing_reader','go_pricing_migrator','go_pricing_seeder')) r;"
        )
    )
    memberships = json.loads(
        manager.admin(
            "SELECT coalesce(json_agg(json_build_object('role',r.rolname,'member',mbr.rolname,"
            "'inherit',m.inherit_option,'set',m.set_option,'admin',m.admin_option) "
            "ORDER BY r.rolname,mbr.rolname),'[]') FROM pg_auth_members m "
            "JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles mbr ON mbr.oid=m.member "
            "WHERE mbr.rolname IN ('go_pricing_reader','go_pricing_migrator','go_pricing_seeder');"
        )
    )
    marker_exists = manager.admin(
        "SELECT to_regclass('pricing_admin.target_identity') IS NOT NULL;"
    )
    marker = None
    if marker_exists == "t":
        reader.require(
            manager.admin(MARKER_SHAPE_SQL) == "t",
            "database target marker has unexpected ownership or schema",
        )
        value = manager.admin(
            "SELECT row_to_json(t) FROM (SELECT environment,instance_id,cluster_uid,seed_allowed "
            "FROM pricing_admin.target_identity WHERE singleton) t;"
        )
        marker = json.loads(value) if value else None
    return {"roles": roles, "memberships": memberships, "target": marker}


def check_role_contract(state: dict[str, object], target: dict[str, object]) -> None:
    roles = {item["rolname"]: item for item in state["roles"]}
    reader.require(set(roles) == set(ROLES), "Pricing administration roles are incomplete")
    expected_attributes = {
        "rolcanlogin": True,
        "rolsuper": False,
        "rolcreatedb": False,
        "rolcreaterole": False,
        "rolinherit": False,
        "rolreplication": False,
        "rolbypassrls": False,
    }
    for role, attributes in roles.items():
        reader.require(
            {key: attributes[key] for key in expected_attributes} == expected_attributes,
            f"{role} attributes differ from the Pricing contract",
        )
    reader.require(
        state["memberships"]
        == [
            {
                "role": "pricing",
                "member": "go_pricing_migrator",
                "inherit": False,
                "set": True,
                "admin": False,
            }
        ],
        "Pricing administration memberships differ from the contract",
    )
    reader.require(
        state["target"]
        == {
            "environment": target["environment"],
            "instance_id": target["instanceID"],
            "cluster_uid": target["clusterUID"],
            "seed_allowed": target["seedAllowed"],
        },
        "database target marker differs from protected enrollment",
    )


def role_sql(passwords: dict[str, str], existing: set[str], target: dict[str, object]) -> str:
    statements = [
        "BEGIN",
        "SET LOCAL lock_timeout='2s'",
        "SET LOCAL statement_timeout='10s'",
    ]
    for role in ROLES:
        if role in existing:
            statements.append(
                f"ALTER ROLE {role} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE "
                "NOINHERIT NOREPLICATION NOBYPASSRLS"
            )
        else:
            statements.append(
                f"CREATE ROLE {role} LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE "
                "NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD "
                f"{reader.sql_literal(passwords[role])}"
            )
    statements.extend(
        [
            "REVOKE ALL ON DATABASE pricing FROM PUBLIC",
            "REVOKE ALL ON SCHEMA public FROM PUBLIC",
            "GRANT CONNECT ON DATABASE pricing TO go_pricing_reader,go_pricing_migrator,go_pricing_seeder",
            "GRANT USAGE ON SCHEMA public TO go_pricing_reader,go_pricing_seeder",
            "GRANT pricing TO go_pricing_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE",
            "CREATE SCHEMA IF NOT EXISTS pricing_admin AUTHORIZATION postgres",
            "REVOKE ALL ON SCHEMA pricing_admin FROM PUBLIC",
            "CREATE TABLE IF NOT EXISTS pricing_admin.target_identity("
            "singleton boolean PRIMARY KEY CHECK(singleton), environment text NOT NULL, "
            "instance_id uuid NOT NULL, cluster_uid text NOT NULL, seed_allowed boolean NOT NULL)",
            "REVOKE ALL ON pricing_admin.target_identity FROM PUBLIC",
            "GRANT USAGE ON SCHEMA pricing_admin TO pricing,go_pricing_migrator,go_pricing_seeder",
            "GRANT SELECT ON pricing_admin.target_identity TO pricing,go_pricing_migrator,go_pricing_seeder",
            "INSERT INTO pricing_admin.target_identity("
            "singleton,environment,instance_id,cluster_uid,seed_allowed) "
            f"VALUES(true,{reader.sql_literal(str(target['environment']))},"
            f"{reader.sql_literal(str(target['instanceID']))}::uuid,"
            f"{reader.sql_literal(str(target['clusterUID']))},"
            f"{str(bool(target['seedAllowed'])).lower()}) ON CONFLICT(singleton) DO NOTHING",
            "COMMIT",
        ]
    )
    grants = GRANT_SQL.read_text()
    statements.insert(
        -1,
        """DO $existing$
BEGIN
    IF to_regclass('public.pricing_schema_version') IS NOT NULL
       AND to_regclass('public.tariff') IS NOT NULL
       AND to_regclass('public.base_price_rules') IS NOT NULL
       AND to_regclass('public.discount_markup_rule') IS NOT NULL THEN
        EXECUTE $sql$"""
        + grants
        + """$sql$;
    END IF;
END $existing$""",
    )
    return ";\n".join(statements) + ";\n"


def read_or_create_inputs(
    manager: AdministrationKubernetes,
) -> tuple[dict[str, str], dict[str, dict[str, bytes] | None], dict[str, object]]:
    existing_secrets = {
        role: manager.get_secret(manager.role_secret(role), manager.svc_ns) for role in ROLES
    }
    passwords: dict[str, str] = {}
    for role, value in existing_secrets.items():
        if value is None:
            passwords[role] = secrets.token_urlsafe(48)
            continue
        reader.require(
            set(value) == {"database-url", "ca.crt"},
            f"{role} Secret has unexpected keys",
        )
        passwords[role] = password_from_url(role, value["database-url"].decode(), manager.hostname)
    stored_target = manager.get_secret(manager.target_secret, manager.svc_ns)
    if stored_target is None:
        target = target_document(manager, str(uuid.uuid4()))
    else:
        reader.require(set(stored_target) == {"target.json"}, "target Secret has unexpected keys")
        target = json.loads(stored_target["target.json"])
        expected = target_document(manager, str(target.get("instanceID", "")))
        reader.require(target == expected, "target Secret differs from current cluster identity")
    return passwords, existing_secrets, target


def secret_values(role: str, password: str, hostname: str, ca: bytes) -> dict[str, bytes]:
    return {"database-url": database_url(role, password, hostname).encode(), "ca.crt": ca}


MARKER_SHAPE_SQL = """
SELECT c.relkind = 'r' AND NOT c.relrowsecurity
    AND c.relowner = 'postgres'::regrole AND n.nspowner = 'postgres'::regrole
    AND (SELECT count(*) = 5 AND bool_and(attnotnull AND (attname, atttypid) IN (
        ('singleton', 'boolean'::regtype), ('environment', 'text'::regtype),
        ('instance_id', 'uuid'::regtype), ('cluster_uid', 'text'::regtype),
        ('seed_allowed', 'boolean'::regtype)))
        FROM pg_attribute WHERE attrelid = c.oid AND attnum > 0 AND NOT attisdropped)
    AND NOT EXISTS (SELECT FROM pg_trigger WHERE tgrelid = c.oid AND NOT tgisinternal)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.oid = to_regclass('pricing_admin.target_identity');
"""

BOUNDARY_SQL = """
SELECT current_database() = 'pricing'
  AND EXISTS (SELECT FROM pg_database WHERE datname = current_database()
      AND datdba = 'pricing'::regrole)
  AND EXISTS (SELECT FROM pg_namespace WHERE nspname = 'public'
      AND nspowner IN ('pricing'::regrole, 'pg_database_owner'::regrole))
  AND NOT EXISTS (SELECT FROM pg_namespace WHERE nspname = 'pricing_admin'
      AND nspowner <> 'postgres'::regrole)
  AND NOT EXISTS (
      SELECT FROM pg_roles WHERE rolcanlogin AND rolname !~ '^pg_'
      AND rolname NOT IN ('pricing','postgres','streaming_replica',
          'go_pricing_reader','go_pricing_migrator','go_pricing_seeder')
      AND has_database_privilege(oid, current_database(), 'CONNECT'))
  AND NOT EXISTS (
      SELECT FROM pg_stat_activity WHERE datname = current_database()
      AND usename NOT IN ('pricing','postgres','streaming_replica',
          'go_pricing_reader','go_pricing_migrator','go_pricing_seeder'))
  AND NOT EXISTS (
      SELECT FROM pg_class WHERE oid IN (to_regclass('public.tariff'),
          to_regclass('public.base_price_rules'), to_regclass('public.discount_markup_rule'),
          to_regclass('public.tariff_id_seq'), to_regclass('public.discount_markup_rule_id_seq'),
          to_regclass('public.pricing_schema_version'))
      AND relowner <> 'pricing'::regrole
  );
"""

GRANT_SQL = (
    Path(__file__).resolve().parents[1] / "internal/administration/queries/grant-tariff-access.sql"
)
READER = "go_pricing_reader"
MIGRATOR = "go_pricing_migrator"
SEEDER = "go_pricing_seeder"


def marker_document(target: dict[str, object]) -> dict[str, object]:
    return {
        "environment": target["environment"],
        "instance_id": target["instanceID"],
        "cluster_uid": target["clusterUID"],
        "seed_allowed": target["seedAllowed"],
    }


def ensure(manager: AdministrationKubernetes) -> None:
    reader.require(
        manager.admin(BOUNDARY_SQL) == "t", "Pricing database ownership or consumers differ"
    )
    before = role_inventory(manager)
    existing_roles = {item["rolname"] for item in before["roles"]}
    passwords, existing_secrets, target = read_or_create_inputs(manager)
    ca = manager.ca()
    for role, value in existing_secrets.items():
        if value is not None:
            reader.require(value["ca.crt"] == ca, f"{role} Secret CA differs from CNPG CA")
        reader.require(
            role not in existing_roles or value is not None,
            f"{role} role exists without its recoverable Secret",
        )
    reader.require(
        before["target"] is None or before["target"] == marker_document(target),
        "database target marker differs from protected enrollment",
    )
    membership = {
        "role": "pricing",
        "member": MIGRATOR,
        "inherit": False,
        "set": True,
        "admin": False,
    }
    reader.require(
        before["memberships"] in ([], [membership]),
        "unexpected administration role membership requires inspection",
    )

    # Publish recoverable inputs before enabling any new role. A retry uses the same credentials.
    if manager.get_secret(manager.target_secret, manager.svc_ns) is None:
        manager.put_named_secret(
            manager.target_secret, {"target.json": json.dumps(target, sort_keys=True).encode()}
        )
    for role, value in existing_secrets.items():
        if value is None:
            manager.put_named_secret(
                manager.role_secret(role),
                secret_values(role, passwords[role], manager.hostname, ca),
            )
    manager.admin(role_sql(passwords, existing_roles, target))
    verify(manager)
    print(f"pricing-resources: ensured {manager.environment} roles and workload Secrets")


def deny(connection, statement: str) -> None:
    reader.require(
        "reader-probe-state:42501" in connection(reader.safe_probe_sql(statement)),
        "Pricing role privilege denial did not report SQLSTATE 42501",
    )


def verify(manager: AdministrationKubernetes) -> None:
    reader.require(
        manager.admin(BOUNDARY_SQL) == "t", "Pricing database ownership or consumers differ"
    )
    passwords, existing_secrets, target = read_or_create_inputs(manager)
    reader.require(
        all(value is not None for value in existing_secrets.values()),
        "Pricing administration Secrets are incomplete",
    )
    check_role_contract(role_inventory(manager), target)
    ca = manager.ca()
    migrated = (
        manager.admin("SELECT to_regclass('public.pricing_schema_version') IS NOT NULL;") == "t"
    )
    for role, value in existing_secrets.items():
        reader.require(value["ca.crt"] == ca, f"{role} Secret CA differs from CNPG CA")
        with manager.role_connection(role, passwords[role], ca) as connection:
            reader.require(
                connection("SELECT current_user;") == role,
                "effective Pricing database role differs",
            )
            if role == MIGRATOR:
                reader.require(
                    connection("SET ROLE pricing; SELECT current_user; RESET ROLE;") == "pricing",
                    "migrator cannot assume the Pricing owner role",
                )
                continue
            deny(connection, "CREATE TABLE public.denied_pricing(value integer)")
            deny(
                connection,
                "UPDATE pricing_admin.target_identity SET seed_allowed=false WHERE false",
            )
            if migrated and (role == READER or target["seedAllowed"]):
                connection(
                    "SELECT count(*) FROM public.tariff;"
                    "SELECT count(*) FROM public.base_price_rules;"
                    "SELECT count(*) FROM public.discount_markup_rule;"
                )
                deny(connection, "UPDATE public.tariff SET code=code WHERE false")
                deny(connection, "DELETE FROM public.tariff WHERE false")
                if role == READER:
                    deny(connection, "INSERT INTO public.tariff (code) SELECT '' WHERE false")
                else:
                    connection("SELECT count(*) FROM public.pricing_schema_version;")
    print(f"pricing-resources: verified {manager.environment} roles, Secrets and SQL denials")


def main(argv: Sequence[str] | None = None) -> int:
    arguments = list(sys.argv[1:] if argv is None else argv)
    if len(arguments) != 1 or arguments[0] not in {"inspect", "ensure", "verify"}:
        print("pricing-resources: expected inspect, ensure or verify", file=sys.stderr)
        return 2
    action = arguments[0]
    environment = os.environ.get("PRICING_RESOURCE_ENV", "")
    reader.require(
        environment in reader.ENVIRONMENTS, "PRICING_RESOURCE_ENV must be local-dev or qa"
    )
    if action == "ensure":
        expected = f"ensure-{environment}-pricing-resources"
        reader.require(
            os.environ.get("PRICING_RESOURCE_CONFIRM") == expected,
            f"PRICING_RESOURCE_CONFIRM must equal {expected}",
        )
    manager = AdministrationKubernetes(environment)
    if action == "inspect":
        state = role_inventory(manager)
        state["databaseCompatible"] = manager.admin(BOUNDARY_SQL) == "t"
        _, credentials, target = read_or_create_inputs(manager)
        ca = manager.ca()
        state["credentials"] = {
            role: "missing"
            if value is None
            else ("present" if value["ca.crt"] == ca else "CA differs")
            for role, value in credentials.items()
        }
        state["markerMatchesEnrollment"] = state["target"] == marker_document(target)
        print(json.dumps(state, sort_keys=True))
    elif action == "ensure":
        ensure(manager)
    else:
        verify(manager)
    return 0


def cli(argv: Sequence[str] | None = None) -> int:
    def execute() -> int:
        try:
            return main(argv)
        except reader.RoleError as error:
            print(f"pricing-resources: {error}", file=sys.stderr)
        except OSError, ValueError, KeyError, TypeError, AttributeError:
            print(
                "pricing-resources: resource data or local process operation failed",
                file=sys.stderr,
            )
        return 1

    return run_cli(execute, name="pricing-resources")


if __name__ == "__main__":
    raise SystemExit(cli())
