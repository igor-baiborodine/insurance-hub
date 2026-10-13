#!/usr/bin/env python3
"""Product reader administration; never runs from the application container."""

from __future__ import annotations

import base64
import binascii
from contextlib import contextmanager, nullcontext
import json
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import sys
import tempfile
import time
from urllib.parse import quote, unquote, urlsplit

ROLE = "go_product_reader"
DATABASE = "product"
CA_MOUNT = "/var/run/secrets/insurance-hub/product-db-ca/ca.crt"
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
        result = subprocess.run(argv, input=input_text, text=True, capture_output=True,
                                check=False, timeout=30, env=env)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise RoleError(f"command unavailable or timed out: {argv[0]}") from exc
    if result.returncode:
        raise RoleError(f"command failed: {argv[0]} (exit {result.returncode}); inspect target safely")
    output = result.stdout.strip()
    probe = re.search(r"reader-probe-state:([0-9A-Z]{5})", result.stderr)
    if probe:
        output += "\nreader-probe-state:" + probe.group(1)
    return output


def sql_literal(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def reader_url(password: str, hostname: str) -> str:
    return (f"postgresql://{ROLE}:{quote(password, safe='')}@{hostname}:5432/{DATABASE}"
            f"?sslmode=verify-full&sslrootcert={quote(CA_MOUNT, safe='/')}")


def password_from_url(url: str, hostname: str) -> str:
    try:
        parsed = urlsplit(url)
        valid = (parsed.scheme == "postgresql" and parsed.username == ROLE and
                 parsed.hostname == hostname and parsed.port == 5432 and
                 parsed.path == f"/{DATABASE}" and parsed.query ==
                 f"sslmode=verify-full&sslrootcert={quote(CA_MOUNT, safe='/')}")
        password = unquote(parsed.password or "")
    except ValueError as exc:
        raise RoleError("reader Secret URL is malformed") from exc
    require(valid, "reader Secret URL does not match the declared role/host/TLS contract")
    require(bool(re.fullmatch(r"[A-Za-z0-9_-]{64}", password)) and
            reader_url(password, hostname) == url,
            "reader Secret URL is not canonical")
    return password


INVENTORY_SQL = r"""
SELECT json_build_object(
 'database', current_database(),
 'databaseOwner', (SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database()),
 'schemaOwner', (SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname='public'),
 'tableOwner', (SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='public.product'::regclass),
 'otherLogins', (SELECT coalesce(json_agg(rolname), '[]'::json) FROM pg_roles
   WHERE rolcanlogin AND rolname !~ '^pg_'
   AND rolname NOT IN ('postgres','product','go_product_reader','streaming_replica')
   AND has_database_privilege(rolname,current_database(),'CONNECT')),
 'otherSessions', (SELECT coalesce(json_agg(DISTINCT usename), '[]'::json) FROM pg_stat_activity
   WHERE datname=current_database() AND usename IS NOT NULL
   AND usename NOT IN ('postgres','product','go_product_reader','streaming_replica')),
 'reader', (SELECT row_to_json(r) FROM (
   SELECT rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolinherit, rolreplication,
          rolbypassrls FROM pg_roles WHERE rolname='go_product_reader') r),
 'memberOf', (SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.member
   WHERE r.rolname='go_product_reader'),
 'readerOwns', (SELECT count(*) FROM pg_database WHERE datdba=(SELECT oid FROM pg_roles WHERE rolname='go_product_reader'))
   + (SELECT count(*) FROM pg_namespace WHERE nspowner=(SELECT oid FROM pg_roles WHERE rolname='go_product_reader'))
   + (SELECT count(*) FROM pg_class WHERE relowner=(SELECT oid FROM pg_roles WHERE rolname='go_product_reader'))
   + (SELECT count(*) FROM pg_proc WHERE proowner=(SELECT oid FROM pg_roles WHERE rolname='go_product_reader'))
   + (SELECT count(*) FROM pg_type WHERE typowner=(SELECT oid FROM pg_roles WHERE rolname='go_product_reader')),
 'publicDatabase', (SELECT count(*) FROM pg_database d,
   LATERAL aclexplode(coalesce(d.datacl,acldefault('d',d.datdba))) a
   WHERE d.datname=current_database() AND a.grantee=0),
 'publicSchema', (SELECT count(*) FROM pg_namespace n,
   LATERAL aclexplode(coalesce(n.nspacl,acldefault('n',n.nspowner))) a
   WHERE n.nspname='public' AND a.grantee=0),
 'publicTable', (SELECT count(*) FROM pg_class c,
   LATERAL aclexplode(coalesce(c.relacl,acldefault('r',c.relowner))) a
   WHERE c.oid='public.product'::regclass AND a.grantee=0),
 'publicDefault', (SELECT count(*) FROM pg_default_acl d, LATERAL aclexplode(d.defaclacl) a
   WHERE d.defaclobjtype='r' AND d.defaclnamespace IN (0,'public'::regnamespace) AND a.grantee=0),
 'readerDefault', (SELECT count(*) FROM pg_default_acl d, LATERAL aclexplode(d.defaclacl) a
   WHERE d.defaclobjtype='r' AND d.defaclnamespace IN (0,'public'::regnamespace)
   AND a.grantee=(SELECT oid FROM pg_roles WHERE rolname='go_product_reader')),
 'readerOtherObjects', (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
   WHERE n.nspname='public' AND c.relname <> 'product' AND c.relkind IN ('r','p','v','m','S')
   AND (SELECT oid FROM pg_roles WHERE rolname='go_product_reader') IS NOT NULL
   AND (CASE WHEN c.relkind='S' THEN
          has_sequence_privilege('go_product_reader',c.oid,'USAGE,SELECT,UPDATE')
        ELSE has_table_privilege('go_product_reader',c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') END)),
 'privileges', CASE WHEN EXISTS(SELECT 1 FROM pg_roles WHERE rolname='go_product_reader') THEN
   json_build_object(
     'connect',has_database_privilege('go_product_reader',current_database(),'CONNECT'),
     'databaseCreate',has_database_privilege('go_product_reader',current_database(),'CREATE'),
     'temporary',has_database_privilege('go_product_reader',current_database(),'TEMPORARY'),
     'schemaUsage',has_schema_privilege('go_product_reader','public','USAGE'),
     'schemaCreate',has_schema_privilege('go_product_reader','public','CREATE'),
     'select',has_table_privilege('go_product_reader','public.product','SELECT'),
     'insert',has_table_privilege('go_product_reader','public.product','INSERT'),
     'update',has_table_privilege('go_product_reader','public.product','UPDATE'),
     'delete',has_table_privilege('go_product_reader','public.product','DELETE'),
     'truncate',has_table_privilege('go_product_reader','public.product','TRUNCATE'),
     'references',has_table_privilege('go_product_reader','public.product','REFERENCES'),
     'trigger',has_table_privilege('go_product_reader','public.product','TRIGGER')) END,
 'javaCanRead', has_table_privilege('product','public.product','SELECT'),
 'rowCount', (SELECT count(*) FROM public.product)
)::text;
"""

SNAPSHOT_SQL = r"""
SELECT json_build_object(
 'rows',(SELECT count(*) FROM public.product),
 'catalog',(SELECT md5(coalesce(string_agg(code || E'\n' || definition::text,E'\n' ORDER BY code),'')) FROM public.product),
 'columns',(SELECT md5(coalesce(string_agg(column_name || ':' || data_type || ':' || is_nullable,',' ORDER BY ordinal_position),''))
   FROM information_schema.columns WHERE table_schema='public' AND table_name='product'),
 'tableOwner',(SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='public.product'::regclass)
)::text;
"""


def inventory(admin) -> dict:
    try:
        data = json.loads(admin(INVENTORY_SQL))
    except (ValueError, TypeError) as exc:
        raise RoleError("database inventory could not be decoded") from exc
    require(data["database"] == DATABASE and data["databaseOwner"] == "product" and
            data["tableOwner"] == "product" and data["javaCanRead"],
            "Product database/Java owner identity or read access differs from contract")
    return data


def consumer_gate(data: dict) -> None:
    require(not data["otherLogins"] and not data["otherSessions"],
            "unknown database consumer; inventory and resolve PUBLIC grant impact before mutation")
    require(data["schemaOwner"] in ("product", "pg_database_owner"),
            "unexpected public schema owner")
    require(not data["readerOwns"] and not data["readerOtherObjects"],
            "reader owns or can access unexpected objects")


def check_boundary(data: dict) -> None:
    consumer_gate(data)
    role = data["reader"]
    require(role is not None and role == {
        "rolcanlogin": True, "rolsuper": False, "rolcreatedb": False,
        "rolcreaterole": False, "rolinherit": False, "rolreplication": False,
        "rolbypassrls": False,
    }, "reader role attributes differ from contract")
    require(data["memberOf"] == 0 and data["readerOwns"] == 0,
            "reader owns objects or inherits a role")
    require(all(data[key] == 0 for key in ("publicDatabase", "publicSchema", "publicTable",
                                             "publicDefault", "readerDefault", "readerOtherObjects")),
            "PUBLIC/default/other-object privileges remain")
    require(data["privileges"] == {
        "connect": True, "databaseCreate": False, "temporary": False,
        "schemaUsage": True, "schemaCreate": False, "select": True,
        "insert": False, "update": False, "delete": False, "truncate": False,
        "references": False, "trigger": False,
    }, "reader effective privileges differ from contract")


def grants_sql(password: str | None, exists: bool) -> str:
    role_stmt = ("ALTER ROLE go_product_reader LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE "
                 "NOINHERIT NOREPLICATION NOBYPASSRLS;" if exists else
                 "CREATE ROLE go_product_reader LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE "
                 f"NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD {sql_literal(password or '')};")
    return f"""
BEGIN;
SET LOCAL lock_timeout='2s';
SET LOCAL statement_timeout='10s';
REVOKE ALL ON DATABASE product FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE product IN SCHEMA public REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE ALL ON TABLES FROM PUBLIC;
{role_stmt}
REVOKE ALL ON DATABASE product FROM go_product_reader;
REVOKE ALL ON SCHEMA public FROM go_product_reader;
REVOKE ALL ON ALL TABLES IN SCHEMA public FROM go_product_reader;
GRANT CONNECT ON DATABASE product TO go_product_reader;
GRANT USAGE ON SCHEMA public TO go_product_reader;
GRANT SELECT ON TABLE public.product TO go_product_reader;
COMMIT;
"""


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
        require(environment in ENVIRONMENTS, "POSTGRES_ROLE_ENV must be local-dev or qa")
        self.environment = environment
        self.context, self.data_ns, self.svc_ns = ENVIRONMENTS[environment]
        self.cluster = f"{environment}-postgres-product"
        self.secret_name = f"{environment}-postgres-product-reader-creds"
        self.ca_name = f"{environment}-postgres-product-ca"
        self.hostname = f"{self.cluster}-rw.{self.data_ns}.svc.cluster.local"
        expected = {
            "POSTGRES_ROLE_CONTEXT": self.context,
            "POSTGRES_ROLE_DATA_NS": self.data_ns,
            "POSTGRES_ROLE_SVC_NS": self.svc_ns,
            "POSTGRES_ROLE_CLUSTER": self.cluster,
            "POSTGRES_ROLE_SECRET": self.secret_name,
            "POSTGRES_ROLE_DB_HOST": self.hostname,
        }
        for key, value in expected.items():
            require(os.environ.get(key) == value, f"{key} must explicitly equal {value}")
        require(command(["kubectl", "config", "current-context"]) == self.context,
                "current Kubernetes context differs from explicit target")
        for namespace in (self.data_ns, self.svc_ns):
            self.kubectl("get", "namespace", namespace, "-o", "name")
        cluster = json.loads(self.kubectl("-n", self.data_ns, "get", "cluster.postgresql.cnpg.io",
                                          self.cluster, "-o", "json"))
        require(cluster["metadata"]["name"] == self.cluster, "database cluster identity mismatch")
        self.primary = cluster.get("status", {}).get("currentPrimary")
        require(bool(self.primary) and re.fullmatch(r"[a-z0-9-]+", self.primary),
                "database primary is not reported")
        pod = json.loads(self.kubectl("-n", self.data_ns, "get", "pod", self.primary, "-o", "json"))
        require(pod["metadata"]["labels"].get("cnpg.io/cluster") == self.cluster and
                pod["status"].get("phase") == "Running" and
                any(item.get("type") == "Ready" and item.get("status") == "True"
                    for item in pod["status"].get("conditions", [])),
                "database primary pod identity/readiness mismatch")

    def kubectl(self, *args: str, input_text: str | None = None) -> str:
        return command(["kubectl", f"--context={self.context}", *args], input_text=input_text)

    def admin(self, sql: str) -> str:
        return self.kubectl("-n", self.data_ns, "exec", "-i", self.primary, "--", "psql",
                            "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres",
                            "-d", DATABASE, input_text=sql)

    def get_secret(self, name: str, namespace: str) -> dict[str, bytes] | None:
        raw = self.kubectl("-n", namespace, "get", "secret", name,
                           "--ignore-not-found", "-o", "json")
        if not raw:
            return None
        record = json.loads(raw)
        require(record["metadata"]["name"] == name, "Secret identity mismatch")
        try:
            return {key: base64.b64decode(value, validate=True) for key, value in record["data"].items()}
        except (ValueError, TypeError, binascii.Error) as exc:
            raise RoleError("Secret data could not be decoded") from exc

    def put_secret(self, values: dict[str, bytes]) -> None:
        require(set(values) == {"database-url", "ca.crt"}, "unexpected reader Secret keys")
        payload = {
            "apiVersion": "v1", "kind": "Secret", "type": "Opaque",
            "metadata": {"name": self.secret_name, "namespace": self.svc_ns},
            "data": {key: base64.b64encode(value).decode() for key, value in values.items()},
        }
        self.kubectl("-n", self.svc_ns, "apply", "-f", "-", input_text=json.dumps(payload))

    def delete_secret(self) -> None:
        self.kubectl("-n", self.svc_ns, "delete", "secret", self.secret_name)

    def ca(self) -> bytes:
        source = self.get_secret(self.ca_name, self.data_ns)
        require(source is not None and source.get("ca.crt", b"").startswith(b"-----BEGIN CERTIFICATE-----"),
                "database CA Secret has no usable ca.crt")
        return source["ca.crt"]

    @contextmanager
    def connection(self, password: str, ca: bytes):
        with tempfile.TemporaryDirectory(prefix="product-reader-") as directory:
            path = Path(directory)
            cert = path / "ca.crt"
            passfile = path / "pgpass"
            cert.write_bytes(ca)
            passfile.write_text(f"{self.hostname}:*:{DATABASE}:{ROLE}:{password.replace('\\', '\\\\').replace(':', '\\:')}\n")
            cert.chmod(0o600)
            passfile.chmod(0o600)
            with socket.socket() as sock:
                sock.bind(("127.0.0.1", 0))
                port = sock.getsockname()[1]
            proc = subprocess.Popen(["kubectl", f"--context={self.context}", "-n", self.data_ns,
                                     "port-forward", f"pod/{self.primary}", f"{port}:5432",
                                     "--address=127.0.0.1"], stdout=subprocess.DEVNULL,
                                    stderr=subprocess.DEVNULL)
            try:
                for _ in range(40):
                    if proc.poll() is not None:
                        raise RoleError("reader port-forward ended before connection")
                    with socket.socket() as check:
                        check.settimeout(0.1)
                        if check.connect_ex(("127.0.0.1", port)) == 0:
                            break
                    time.sleep(0.1)
                else:
                    raise RoleError("reader port-forward did not become ready")
                options = (f"host={self.hostname} hostaddr=127.0.0.1 port={port} dbname={DATABASE} "
                           f"user={ROLE} sslmode=verify-full sslrootcert={cert}")
                environment = {key: value for key, value in os.environ.items()
                               if not key.startswith("PG")}
                environment.update(PGPASSFILE=str(passfile), PGCONNECT_TIMEOUT="5")
                yield lambda sql: command(["psql", "-X", "-q", "-A", "-t", "-v",
                                           "ON_ERROR_STOP=1", "-d", options],
                                          input_text=sql, env=environment)
            finally:
                proc.terminate()
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait(timeout=5)


def verify_reader(runtime, *, safe: bool = True) -> None:
    observed = runtime("SELECT json_build_object('user',current_user,'rows',"
                       "(SELECT count(*) FROM public.product),'car',"
                       "(SELECT code FROM public.product WHERE code='CAR'))::text;")
    record = json.loads(observed)
    require(record["user"] == ROLE and record["rows"] > 0 and record["car"] == "CAR",
            "reader login or list/get probe failed")
    if safe:
        sentinel = "READER_PROBE_" + secrets.token_hex(8).upper()
        for operation in (
            f"INSERT INTO public.product(code,definition) VALUES ('{sentinel}','{{}}'::jsonb)",
            "UPDATE public.product SET definition=definition WHERE false",
            "DELETE FROM public.product WHERE false",
            f"CREATE TABLE public.reader_probe_{secrets.token_hex(8)} (id integer)",
        ):
            # The DO block records the actual SQLSTATE and always reaches ROLLBACK,
            # including when an unexpectedly privileged statement succeeds.
            result = runtime(safe_probe_sql(operation))
            require("reader-probe-state:42501" in result,
                    "safe mutation probe did not report SQLSTATE 42501")


def snapshot(admin) -> dict:
    return json.loads(admin(SNAPSHOT_SQL))


def secret_values(password: str, host: str, ca: bytes) -> dict[str, bytes]:
    return {"database-url": reader_url(password, host).encode(), "ca.crt": ca}


@contextmanager
def generated_credential():
    with tempfile.TemporaryDirectory(prefix="product-reader-credential-") as directory:
        path = Path(directory) / "password"
        path.write_text(secrets.token_urlsafe(48))
        path.chmod(0o600)
        yield path.read_text()


def verify(manager: Kubernetes, *, safe: bool = True) -> None:
    before = snapshot(manager.admin)
    state = inventory(manager.admin)
    check_boundary(state)
    secret = manager.get_secret(manager.secret_name, manager.svc_ns)
    require(secret is not None and set(secret) == {"database-url", "ca.crt"},
            "reader workload Secret is missing or has unexpected keys")
    ca = manager.ca()
    require(secret["ca.crt"] == ca, "reader Secret CA differs from cluster CA")
    password = password_from_url(secret["database-url"].decode(), manager.hostname)
    with manager.connection(password, ca) as runtime:
        verify_reader(runtime, safe=safe)
    owner_read = json.loads(manager.admin(
        "SET ROLE product; SELECT json_build_object('user',current_user,'rows',"
        "(SELECT count(*) FROM public.product))::text; RESET ROLE;"))
    require(owner_read == {"user": "product", "rows": before["rows"]},
            "Java owner role cannot read the unchanged catalog")
    require(snapshot(manager.admin) == before, "catalog/schema identity changed during reader proof")
    print(f"product-reader: verified {manager.environment} role, Secret login, reads, grants and safe denials")


def provision(manager: Kubernetes) -> None:
    before = snapshot(manager.admin)
    state = inventory(manager.admin)
    consumer_gate(state)
    existing = manager.get_secret(manager.secret_name, manager.svc_ns)
    ca = manager.ca()
    if existing is not None:
        require(set(existing) == {"database-url", "ca.crt"} and existing["ca.crt"] == ca,
                "existing reader Secret does not match declared CA and keys")
        credential = nullcontext(password_from_url(existing["database-url"].decode(), manager.hostname))
    else:
        require(state["reader"] is None, "reader role exists without Secret; use explicit rotation/recovery")
        credential = generated_credential()
    created = state["reader"] is None
    with credential as password:
        manager.admin(grants_sql(password, not created))
        try:
            if existing is None:
                manager.put_secret(secret_values(password, manager.hostname, ca))
            verify(manager)
        except Exception:
            if created and existing is None:
                try:
                    manager.admin("BEGIN; REVOKE ALL ON DATABASE product FROM go_product_reader; "
                                  "REVOKE ALL ON SCHEMA public FROM go_product_reader; "
                                  "REVOKE ALL ON ALL TABLES IN SCHEMA public FROM go_product_reader; "
                                  "DROP ROLE go_product_reader; COMMIT;")
                except RoleError:
                    raise RoleError("provision failed and role compensation failed; inspect role and Secret")
            raise
    require(snapshot(manager.admin) == before, "catalog/schema identity changed during provisioning")
    print(f"product-reader: provisioned {manager.environment}; ordinary reapply preserved credential")


def rotate(manager: Kubernetes) -> None:
    before = snapshot(manager.admin)
    state = inventory(manager.admin)
    check_boundary(state)
    existing = manager.get_secret(manager.secret_name, manager.svc_ns)
    require(existing is not None and set(existing) == {"database-url", "ca.crt"},
            "rotation requires an existing reader Secret")
    ca = manager.ca()
    old = password_from_url(existing["database-url"].decode(), manager.hostname)
    with manager.connection(old, ca) as runtime:
        verify_reader(runtime, safe=False)
    recovery_path = os.environ.get("POSTGRES_ROLE_RECOVERY_FILE", "")
    require(bool(recovery_path) and Path(recovery_path).is_absolute(),
            "POSTGRES_ROLE_RECOVERY_FILE must be an absolute, unused protected file path")
    try:
        fd = os.open(recovery_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except OSError as exc:
        raise RoleError("protected recovery file could not be created") from exc
    with os.fdopen(fd, "w") as output:
        output.write(old + "\n")
    restore_path = os.environ.get("POSTGRES_ROLE_RESTORE_FILE", "")
    if restore_path:
        try:
            source = Path(restore_path)
            require(source.is_absolute() and source.is_file() and
                    source.stat().st_mode & 0o777 == 0o600,
                    "restore input must be an absolute mode-0600 file")
            new = source.read_text().rstrip("\n")
        except OSError as exc:
            raise RoleError("protected restore input could not be read") from exc
        require(bool(re.fullmatch(r"[A-Za-z0-9_-]{64}", new)) and new != old,
                "restore input is not a distinct reader credential")
    else:
        new = secrets.token_urlsafe(48)
    try:
        manager.admin(f"ALTER ROLE go_product_reader PASSWORD {sql_literal(new)};")
        manager.put_secret(secret_values(new, manager.hostname, ca))
        verify(manager)
    except Exception as exc:
        try:
            manager.admin(f"ALTER ROLE go_product_reader PASSWORD {sql_literal(old)};")
            manager.put_secret(existing)
        except RoleError as recovery_exc:
            raise RoleError("rotation failed and automatic recovery failed; use protected recovery input") from recovery_exc
        raise RoleError("rotation failed; previous credential restored") from exc
    require(snapshot(manager.admin) == before, "catalog/schema identity changed during rotation")
    print(f"product-reader: rotated {manager.environment}; retain recovery input until rollout verification")


def revoke(manager: Kubernetes) -> None:
    before = snapshot(manager.admin)
    require(os.environ.get("POSTGRES_ROLE_CONFIRM_REVOKE") == f"product/reader/{manager.environment}",
            "explicit POSTGRES_ROLE_CONFIRM_REVOKE=product/reader/<environment> is required")
    deployed = manager.kubectl("-n", manager.svc_ns, "get", "deployment",
                               f"{manager.environment}-product-api", "--ignore-not-found", "-o", "name")
    require(not deployed, "Go deployment still exists; stop it before reader revocation")
    state = inventory(manager.admin)
    consumer_gate(state)
    if state["reader"] is not None:
        manager.admin("BEGIN; REVOKE ALL ON DATABASE product FROM go_product_reader; "
                      "REVOKE ALL ON SCHEMA public FROM go_product_reader; "
                      "REVOKE ALL ON ALL TABLES IN SCHEMA public FROM go_product_reader; "
                      "DROP ROLE go_product_reader; COMMIT;")
    if manager.get_secret(manager.secret_name, manager.svc_ns) is not None:
        manager.delete_secret()
    require(snapshot(manager.admin) == before, "catalog/schema identity changed during revocation")
    print(f"product-reader: revoked {manager.environment} reader role and workload Secret")


def main() -> None:
    require(len(sys.argv) == 2 and sys.argv[1] in
            {"inventory", "provision", "verify", "rotate", "revoke"}, "unsupported role action")
    manager = Kubernetes(os.environ.get("POSTGRES_ROLE_ENV", ""))
    action = sys.argv[1]
    if action == "inventory":
        state = inventory(manager.admin)
        # Names and privilege counts are safe; no Secret payload is read in this operation.
        print(json.dumps(state, sort_keys=True))
    elif action == "provision":
        provision(manager)
    elif action == "verify":
        verify(manager)
    elif action == "rotate":
        rotate(manager)
    else:
        revoke(manager)


if __name__ == "__main__":
    try:
        main()
    except (RoleError, ValueError, KeyError, UnicodeError) as error:
        print(f"product-reader: {error}", file=sys.stderr)
        raise SystemExit(2) from None
    except OSError:
        print("product-reader: protected local file or process operation failed", file=sys.stderr)
        raise SystemExit(2) from None
