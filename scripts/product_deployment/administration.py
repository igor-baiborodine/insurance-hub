#!/usr/bin/env python3
"""Provision Product administration identities and workload Secrets outside the API."""

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
from contextlib import ExitStack, contextmanager
from pathlib import Path
from urllib.parse import quote, unquote, urlsplit

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
if str(REPOSITORY_ROOT) not in sys.path:
    sys.path.insert(0, str(REPOSITORY_ROOT))

from scripts.product_deployment import reader  # noqa: E402

ROLES = ("go_product_reader", "go_product_migrator", "go_product_seeder")
ADMIN_ROLES = ROLES[1:]
CA_MOUNTS = {
    "go_product_reader": reader.CA_MOUNT,
    "go_product_migrator": "/etc/product-admin/migrator/ca.crt",
    "go_product_seeder": "/etc/product-admin/seeder/ca.crt",
}
SECRET_SUFFIX = {
    "go_product_reader": "reader-creds",
    "go_product_migrator": "migrator-creds",
    "go_product_seeder": "seeder-creds",
}


def database_url(role: str, password: str, hostname: str) -> str:
    reader.require(role in ROLES, "unsupported Product database role")
    return (
        f"postgresql://{role}:{quote(password, safe='')}@{hostname}:5432/product"
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
            and parsed.path == "/product"
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
        "database": "product",
        "host": manager.hostname,
        "seedAllowed": manager.environment in {"local-dev", "qa"},
        "disposable": False,
    }


class AdministrationKubernetes(reader.Kubernetes):
    """Exact Product role target plus named Secret operations."""

    def __init__(self, environment: str):
        super().__init__(environment)
        cluster = json.loads(
            self.kubectl(
                "-n", self.data_ns, "get", "cluster.postgresql.cnpg.io", self.cluster, "-o", "json"
            )
        )
        self.cluster_uid = cluster["metadata"].get("uid", "")
        reader.require(
            bool(re.fullmatch(r"[0-9a-f-]{36}", self.cluster_uid)),
            "database cluster UID is unavailable",
        )
        self.target_secret = f"{environment}-postgres-product-admin-target"

    def role_secret(self, role: str) -> str:
        return f"{self.environment}-postgres-product-{SECRET_SUFFIX[role]}"

    def put_named_secret(self, name: str, values: dict[str, bytes]) -> None:
        payload = {
            "apiVersion": "v1",
            "kind": "Secret",
            "type": "Opaque",
            "metadata": {"name": name, "namespace": self.svc_ns},
            "data": {key: base64.b64encode(value).decode() for key, value in values.items()},
        }
        self.kubectl("-n", self.svc_ns, "apply", "-f", "-", input_text=json.dumps(payload))

    def delete_named_secret(self, name: str) -> None:
        self.kubectl("-n", self.svc_ns, "delete", "secret", name, "--ignore-not-found")

    @contextmanager
    def role_connection(self, role: str, password: str, ca: bytes):
        reader.require(role in ROLES, "unsupported Product database role")
        with tempfile.TemporaryDirectory(prefix="product-administration-") as directory:
            root = Path(directory)
            cert = root / "ca.crt"
            passfile = root / "pgpass"
            cert.write_bytes(ca)
            passfile.write_text(
                f"{self.hostname}:*:product:{role}:"
                f"{password.replace('\\', '\\\\').replace(':', '\\:')}\n"
            )
            cert.chmod(0o600)
            passfile.chmod(0o600)

            def execute(sql: str) -> str:
                with socket.socket() as listener:
                    listener.bind(("127.0.0.1", 0))
                    port = listener.getsockname()[1]
                process = subprocess.Popen(
                    [
                        "kubectl",
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
                        f"dbname=product user={role} sslmode=verify-full sslrootcert={cert}"
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
            "('go_product_reader','go_product_migrator','go_product_seeder')) r;"
        )
    )
    memberships = json.loads(
        manager.admin(
            "SELECT coalesce(json_agg(json_build_object('role',r.rolname,'member',mbr.rolname,"
            "'inherit',m.inherit_option,'set',m.set_option,'admin',m.admin_option) "
            "ORDER BY r.rolname,mbr.rolname),'[]') FROM pg_auth_members m "
            "JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles mbr ON mbr.oid=m.member "
            "WHERE mbr.rolname IN ('go_product_reader','go_product_migrator','go_product_seeder');"
        )
    )
    marker_exists = manager.admin(
        "SELECT to_regclass('product_admin.target_identity') IS NOT NULL;"
    )
    marker = None
    if marker_exists == "t":
        value = manager.admin(
            "SELECT row_to_json(t) FROM (SELECT environment,instance_id,cluster_uid,seed_allowed "
            "FROM product_admin.target_identity WHERE singleton) t;"
        )
        marker = json.loads(value) if value else None
    return {"roles": roles, "memberships": memberships, "target": marker}


def check_role_contract(state: dict[str, object], target: dict[str, object]) -> None:
    roles = {item["rolname"]: item for item in state["roles"]}
    reader.require(set(roles) == set(ROLES), "Product administration roles are incomplete")
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
            f"{role} attributes differ from the Product contract",
        )
    reader.require(
        state["memberships"]
        == [
            {
                "role": "product",
                "member": "go_product_migrator",
                "inherit": False,
                "set": True,
                "admin": False,
            }
        ],
        "Product administration memberships differ from the contract",
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
            "REVOKE ALL ON DATABASE product FROM PUBLIC",
            "REVOKE ALL ON SCHEMA public FROM PUBLIC",
            "GRANT CONNECT ON DATABASE product TO go_product_reader,go_product_migrator,go_product_seeder",
            "GRANT USAGE ON SCHEMA public TO go_product_reader,go_product_seeder",
            "GRANT product TO go_product_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE",
            "CREATE SCHEMA IF NOT EXISTS product_admin AUTHORIZATION postgres",
            "REVOKE ALL ON SCHEMA product_admin FROM PUBLIC",
            "CREATE TABLE IF NOT EXISTS product_admin.target_identity("
            "singleton boolean PRIMARY KEY CHECK(singleton), environment text NOT NULL, "
            "instance_id uuid NOT NULL, cluster_uid text NOT NULL, seed_allowed boolean NOT NULL)",
            "REVOKE ALL ON product_admin.target_identity FROM PUBLIC",
            "GRANT USAGE ON SCHEMA product_admin TO product,go_product_migrator,go_product_seeder",
            "GRANT SELECT ON product_admin.target_identity TO product,go_product_migrator,go_product_seeder",
            "INSERT INTO product_admin.target_identity("
            "singleton,environment,instance_id,cluster_uid,seed_allowed) "
            f"VALUES(true,{reader.sql_literal(str(target['environment']))},"
            f"{reader.sql_literal(str(target['instanceID']))}::uuid,"
            f"{reader.sql_literal(str(target['clusterUID']))},"
            f"{str(bool(target['seedAllowed'])).lower()}) ON CONFLICT(singleton) DO NOTHING",
            "COMMIT",
        ]
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


def compensation_sql(
    created_roles: Sequence[str],
    target: dict[str, object],
    *,
    remove_target: bool,
    remove_migrator_membership: bool,
) -> str:
    roles = tuple(created_roles)
    reader.require(
        len(set(roles)) == len(roles) and all(role in ROLES for role in roles),
        "invalid administration compensation role",
    )
    statements = ["BEGIN", "SET LOCAL lock_timeout='2s'", "SET LOCAL statement_timeout='10s'"]
    if remove_target:
        statements.append(
            "DELETE FROM product_admin.target_identity WHERE singleton "
            f"AND environment={reader.sql_literal(str(target['environment']))} "
            f"AND instance_id={reader.sql_literal(str(target['instanceID']))}::uuid "
            f"AND cluster_uid={reader.sql_literal(str(target['clusterUID']))} "
            f"AND seed_allowed={str(bool(target['seedAllowed'])).lower()}"
        )
    if remove_migrator_membership:
        statements.append("REVOKE product FROM go_product_migrator")
    if roles:
        statements.append("DROP OWNED BY " + ",".join(roles))
        statements.extend(f"DROP ROLE IF EXISTS {role}" for role in reversed(roles))
    statements.append("COMMIT")
    return ";\n".join(statements) + ";\n"


def provision(manager: AdministrationKubernetes) -> None:
    before = role_inventory(manager)
    existing_roles = {item["rolname"] for item in before["roles"]}
    expected_membership = {
        "role": "product",
        "member": "go_product_migrator",
        "inherit": False,
        "set": True,
        "admin": False,
    }
    reader.require(
        before["memberships"] in ([], [expected_membership]),
        "unexpected administration role membership requires inspection",
    )
    passwords, existing_secrets, target = read_or_create_inputs(manager)
    ca = manager.ca()
    for role, value in existing_secrets.items():
        if value is not None:
            reader.require(value["ca.crt"] == ca, f"{role} Secret CA differs from CNPG CA")
        reader.require(
            (role in existing_roles) == (value is not None),
            f"{role} role/Secret partial state requires recovery",
        )
    manager.admin(role_sql(passwords, existing_roles, target))
    written: list[str] = []
    try:
        for role in ROLES:
            if existing_secrets[role] is None:
                name = manager.role_secret(role)
                manager.put_named_secret(
                    name, secret_values(role, passwords[role], manager.hostname, ca)
                )
                written.append(name)
        if manager.get_secret(manager.target_secret, manager.svc_ns) is None:
            manager.put_named_secret(
                manager.target_secret,
                {"target.json": json.dumps(target, sort_keys=True).encode()},
            )
            written.append(manager.target_secret)
        check_role_contract(role_inventory(manager), target)
    except Exception as error:
        for name in reversed(written):
            try:
                manager.delete_named_secret(name)
            except reader.RoleError:
                pass
        created_roles = [role for role in ROLES if role not in existing_roles]
        remove_target = before["target"] is None
        if created_roles or remove_target or not before["memberships"]:
            try:
                manager.admin(
                    compensation_sql(
                        created_roles,
                        target,
                        remove_target=remove_target,
                        remove_migrator_membership=not before["memberships"],
                    )
                )
                compensated = role_inventory(manager)
                reader.require(
                    not ({item["rolname"] for item in compensated["roles"]} & set(created_roles))
                    and compensated["memberships"] == before["memberships"]
                    and compensated["target"] == before["target"],
                    "administration compensation state differs from its predecessor",
                )
                reader.require(
                    all(manager.get_secret(name, manager.svc_ns) is None for name in written),
                    "administration compensation left a newly written Secret",
                )
            except reader.RoleError as compensation_error:
                raise reader.RoleError(
                    "administration provisioning failed and compensation was incomplete"
                ) from compensation_error
        raise reader.RoleError(
            "administration provisioning failed; partial state compensated"
        ) from error
    print(f"product-administration: provisioned {manager.environment} roles and workload Secrets")


def verify(manager: AdministrationKubernetes) -> None:
    passwords, existing_secrets, target = read_or_create_inputs(manager)
    reader.require(
        all(value is not None for value in existing_secrets.values()),
        "Product administration Secrets are incomplete",
    )
    check_role_contract(role_inventory(manager), target)
    ca = manager.ca()
    product_exists = manager.admin("SELECT to_regclass('public.product') IS NOT NULL;") == "t"
    history_exists = (
        manager.admin("SELECT to_regclass('public.goose_db_version') IS NOT NULL;") == "t"
    )
    reader.require(
        product_exists or not history_exists, "Goose history exists without Product table"
    )
    with ExitStack() as stack:
        connections = {
            role: stack.enter_context(manager.role_connection(role, passwords[role], ca))
            for role in ROLES
        }
        reader_connection = connections["go_product_reader"]
        if product_exists:
            reader.verify_reader(reader_connection)
        else:
            reader.require(
                reader_connection("SELECT current_user;") == "go_product_reader",
                "reader effective identity differs before initialization",
            )
            reader.require(
                "reader-probe-state:42501"
                in reader_connection(
                    reader.safe_probe_sql("CREATE TABLE public.denied_reader(value integer)")
                ),
                "reader pre-initialization DDL denial did not report SQLSTATE 42501",
            )
        reader.require(
            connections["go_product_migrator"]("SET ROLE product; SELECT current_user; RESET ROLE;")
            == "product",
            "migrator cannot assume the Product owner role",
        )
        seeder = connections["go_product_seeder"]
        statements = [
            "CREATE TABLE public.denied_seeder(value integer)",
            "UPDATE product_admin.target_identity SET seed_allowed=false",
        ]
        if product_exists and history_exists:
            seeder(
                "SELECT count(*) FROM public.product; SELECT count(*) FROM public.goose_db_version;"
            )
        else:
            reader.require(
                seeder("SELECT current_user;") == "go_product_seeder",
                "seeder effective identity differs before initialization",
            )
            if product_exists:
                statements.append("SELECT count(*) FROM public.product")
        if product_exists:
            statements.extend(
                (
                    "UPDATE public.product SET definition=definition WHERE false",
                    "DELETE FROM public.product WHERE false",
                    "ALTER TABLE public.product ADD COLUMN denied integer",
                )
            )
        for statement in statements:
            reader.require(
                "reader-probe-state:42501" in seeder(reader.safe_probe_sql(statement)),
                "seeder privilege denial did not report SQLSTATE 42501",
            )
    print(f"product-administration: verified {manager.environment} roles, Secrets and SQL denials")


def rotate(manager: AdministrationKubernetes) -> None:
    passwords, existing_secrets, _ = read_or_create_inputs(manager)
    reader.require(
        all(value is not None for value in existing_secrets.values()),
        "rotation requires complete Product administration Secrets",
    )
    recovery_path = Path(os.environ.get("POSTGRES_ROLE_RECOVERY_FILE", ""))
    reader.require(
        recovery_path.is_absolute() and not recovery_path.exists(),
        "POSTGRES_ROLE_RECOVERY_FILE must be an absolute unused path",
    )
    try:
        descriptor = os.open(recovery_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except OSError as error:
        raise reader.RoleError("protected recovery file could not be created") from error
    with os.fdopen(descriptor, "w") as output:
        json.dump({role: passwords[role] for role in ADMIN_ROLES}, output, sort_keys=True)
        output.write("\n")
    replacements = {role: secrets.token_urlsafe(48) for role in ADMIN_ROLES}
    ca = manager.ca()
    try:
        manager.admin(
            "BEGIN; "
            + " ".join(
                f"ALTER ROLE {role} PASSWORD {reader.sql_literal(replacements[role])};"
                for role in ADMIN_ROLES
            )
            + " COMMIT;"
        )
        for role in ADMIN_ROLES:
            manager.put_named_secret(
                manager.role_secret(role),
                secret_values(role, replacements[role], manager.hostname, ca),
            )
        verify(manager)
    except Exception as error:
        try:
            manager.admin(
                "BEGIN; "
                + " ".join(
                    f"ALTER ROLE {role} PASSWORD {reader.sql_literal(passwords[role])};"
                    for role in ADMIN_ROLES
                )
                + " COMMIT;"
            )
            for role in ADMIN_ROLES:
                value = existing_secrets[role]
                reader.require(value is not None, "rotation recovery Secret is missing")
                manager.put_named_secret(manager.role_secret(role), value)
        except Exception as recovery_error:
            raise reader.RoleError(
                "administration rotation failed and automatic recovery was incomplete"
            ) from recovery_error
        raise reader.RoleError(
            "administration rotation failed; previous credentials restored"
        ) from error
    print(f"product-administration: rotated {manager.environment}; retain protected recovery input")


def snapshot(manager: AdministrationKubernetes) -> dict[str, object]:
    table = manager.admin("SELECT to_regclass('public.product') IS NOT NULL;") == "t"
    result = {"roles": role_inventory(manager), "productTable": table}
    if table:
        result["product"] = json.loads(manager.admin(reader.SNAPSHOT_SQL))
    return result


def revoke(manager: AdministrationKubernetes) -> None:
    reader.require(
        os.environ.get("POSTGRES_ROLE_CONFIRM_REVOKE")
        == f"product/administration/{manager.environment}",
        "explicit POSTGRES_ROLE_CONFIRM_REVOKE=product/administration/<environment> is required",
    )
    jobs = manager.kubectl(
        "-n",
        manager.svc_ns,
        "get",
        "jobs",
        "-l",
        "app.kubernetes.io/name=product-init",
        "-o",
        "name",
    )
    reader.require(not jobs, "Product initialization Jobs still exist")
    manager.admin(
        "BEGIN; "
        "REVOKE CONNECT ON DATABASE product FROM go_product_migrator,go_product_seeder; "
        "REVOKE ALL ON SCHEMA public FROM go_product_migrator,go_product_seeder; "
        "REVOKE ALL ON ALL TABLES IN SCHEMA public FROM go_product_migrator,go_product_seeder; "
        "REVOKE USAGE ON SCHEMA product_admin FROM go_product_migrator,go_product_seeder; "
        "REVOKE SELECT ON product_admin.target_identity FROM go_product_migrator,go_product_seeder; "
        "REVOKE product FROM go_product_migrator; "
        "DROP ROLE go_product_seeder; DROP ROLE go_product_migrator; COMMIT;"
    )
    for role in ADMIN_ROLES:
        manager.delete_named_secret(manager.role_secret(role))
    print(f"product-administration: revoked {manager.environment} elevated roles and Secrets")


def main(argv: Sequence[str] | None = None) -> int:
    arguments = list(sys.argv[1:] if argv is None else argv)
    reader.require(
        len(arguments) == 1
        and arguments[0] in {"inventory", "snapshot", "provision", "verify", "rotate", "revoke"},
        "unsupported administration action",
    )
    manager = AdministrationKubernetes(os.environ.get("POSTGRES_ROLE_ENV", ""))
    action = arguments[0]
    if action == "inventory":
        print(json.dumps(role_inventory(manager), sort_keys=True))
    elif action == "snapshot":
        print(json.dumps(snapshot(manager), sort_keys=True))
    elif action == "provision":
        provision(manager)
    elif action == "verify":
        verify(manager)
    elif action == "rotate":
        rotate(manager)
    else:
        revoke(manager)
    return 0


def cli(argv: Sequence[str] | None = None) -> int:
    def execute() -> int:
        try:
            return main(argv)
        except (
            reader.RoleError,
            ValueError,
            KeyError,
            UnicodeError,
            json.JSONDecodeError,
        ) as error:
            print(f"product-administration: {error}", file=sys.stderr)
            return 2
        except OSError:
            print("product-administration: local process operation failed", file=sys.stderr)
            return 2

    return reader.run_cli(execute, name="product-administration")


if __name__ == "__main__":
    raise SystemExit(cli())
