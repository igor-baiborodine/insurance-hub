"""Real, owned PostgreSQL/MinIO smoke; never uses configured live targets."""

from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import sys
import time
import uuid
from contextlib import contextmanager
from pathlib import Path
from urllib.request import urlopen

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import resource_administration as resources  # noqa: E402

pytestmark = pytest.mark.integration
DOCKER = os.environ.get("DOCKER", "docker")
PG_IMAGE = os.environ.get(
    "POSTGRES_IMAGE",
    "postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193",
)
MINIO_IMAGE = os.environ.get(
    "MINIO_IMAGE",
    "cgr.dev/chainguard/minio@sha256:7ad2a5efd83f0ca1b8e00af9f7ec7fc3ff977c38ed29b942b0c47460ad2f8f3e",
)
MINIO_CLIENT_IMAGE = os.environ.get(
    "MINIO_CLIENT_IMAGE",
    "cgr.dev/chainguard/minio-client@sha256:2e433473835685d7daf6a266dbbf44fc9d5479b24cb351b7d170492f4fe971d4",
)


def run(args, text=None, *, failure=False, environment=None):
    result = subprocess.run(
        args, input=text, capture_output=True, text=True, timeout=30, env=environment
    )
    if failure:
        assert result.returncode != 0, "forbidden action succeeded"
    else:
        assert result.returncode == 0, f"fixture command failed ({args[0]}): {result.stderr}"
    return result.stdout.strip()


@contextmanager
def container(image, args, mounts=(), environment=None):
    name = "document-362-" + uuid.uuid4().hex[:12]
    try:
        run(
            [
                DOCKER,
                "run",
                "--pull=missing",
                "--rm",
                "-d",
                "--name",
                name,
                "--label",
                "insurance-hub.fixture=document-administration",
                *mounts,
                image,
                *args,
            ],
            environment=environment,
        )
        yield name
    finally:
        # Always address the exact uniquely registered resource, including partial creation.
        result = subprocess.run(
            [DOCKER, "rm", "-f", name], capture_output=True, text=True, timeout=10
        )
        assert result.returncode == 0 or "No such container" in result.stderr, (
            "fixture cleanup failed"
        )


def wait_for(probe):
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        try:
            if probe():
                return
        except subprocess.TimeoutExpired, OSError:
            pass
        time.sleep(0.2)
    raise AssertionError("disposable dependency did not become ready")


def private(path, value):
    path.write_text(value)
    path.chmod(0o600)
    return path


def loopback_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


class ContainerizedMinioCommands(resources.Commands):
    """Run the fixture client from a pinned image without a host mc dependency."""

    def run(self, args, text=None, env=None, *, missing=(), timeout_cap=30):
        if args[0] != os.environ.get("MC", "mc"):
            return super().run(args, text, env, missing=missing, timeout_cap=timeout_cap)
        mounts = set()
        for value in args[1:]:
            path = Path(value)
            if not path.is_absolute():
                continue
            source = path if path.is_dir() else path.parent
            mounts.add(source)
        mount_args = []
        for source in sorted(mounts):
            mount_args.extend(("--mount", f"type=bind,src={source},dst={source}"))
        return super().run(
            [
                DOCKER,
                "run",
                "--pull=missing",
                "--rm",
                "--interactive",
                "--network",
                "host",
                "--user",
                f"{os.getuid()}:{os.getgid()}",
                *mount_args,
                MINIO_CLIENT_IMAGE,
                *args[1:],
            ],
            text,
            env,
            missing=missing,
            timeout_cap=timeout_cap,
        )


def minio_ready(minio):
    try:
        minio.mc("ready", "document")
    except resources.ResourceError:
        return False
    return True


@contextmanager
def lookup_server(environment, root):
    image = os.environ.get("SERVER_IMAGE", "")
    if not image:
        process = subprocess.Popen(
            [os.environ["SERVER_OUTPUT"]],
            env=environment,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
        try:
            yield None
        finally:
            assert resources.terminate_process_group(process, 10), "lookup service cleanup failed"
        return

    image_ca = root / "image-ca.crt"
    image_ca.write_text((root / "ca.crt").read_text())
    image_ca.chmod(0o444)
    container_ca = "/etc/document-service/postgres/ca.crt"
    container_environment = dict(environment)
    container_environment["DOCUMENT_DATABASE_URL"] = container_environment[
        "DOCUMENT_DATABASE_URL"
    ].replace(str(root / "ca.crt"), container_ca)
    container_environment.update(
        {
            "DEPLOYMENT_ENVIRONMENT": "local-dev",
            "SERVICE_VERSION": os.environ["SOURCE_VERSION"],
            "SERVICE_INSTANCE_ID": "document-image-check-362",
        }
    )
    mounts = [
        "--network",
        "host",
        "--mount",
        f"type=bind,src={image_ca},dst={container_ca},readonly",
    ]
    runtime_names = {
        "HTTP_ADDR",
        "GRPC_ADDR",
        "HEALTH_ADDR",
        "DOCUMENT_DATABASE_URL",
        "MINIO_ENDPOINT",
        "MINIO_ACCESS_KEY",
        "MINIO_SECRET_KEY",
        "MINIO_BUCKET",
        "OTEL_ENABLED",
        "DEPLOYMENT_ENVIRONMENT",
        "SERVICE_VERSION",
        "SERVICE_INSTANCE_ID",
    }
    sensitive_names = {
        "DOCUMENT_DATABASE_URL",
        "MINIO_ACCESS_KEY",
        "MINIO_SECRET_KEY",
    }
    for name in sorted(runtime_names):
        value = container_environment[name]
        mounts.extend(("--env", name if name in sensitive_names else f"{name}={value}"))
    with container(image, [], mounts, container_environment) as name:
        yield name


@pytest.fixture(autouse=True)
def cleanup_private_fixture(tmp_path):
    try:
        yield
    finally:
        shutil.rmtree(tmp_path, ignore_errors=True)


@pytest.fixture
def postgres(tmp_path):
    # Use the public TLS endpoint even for disposable migration; no no-TLS backdoor.
    run(
        [
            "openssl",
            "req",
            "-x509",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-days",
            "1",
            "-subj",
            "/CN=localhost",
            "-addext",
            "subjectAltName=DNS:localhost",
            "-keyout",
            str(tmp_path / "server.key"),
            "-out",
            str(tmp_path / "server.crt"),
        ]
    )
    # Docker root copies the private key into its owned container and fixes PostgreSQL ownership.
    mounts = [
        "--tmpfs",
        "/var/lib/postgresql/data",
        "-e",
        "POSTGRES_PASSWORD=synthetic-admin",
        "-p",
        "127.0.0.1::5432",
        "--mount",
        f"type=bind,src={tmp_path},dst=/tls,readonly",
    ]
    args = [
        "sh",
        "-c",
        "cp /tls/server.key /tmp/server.key; cp /tls/server.crt /tmp/server.crt; chown postgres /tmp/server.key; chmod 600 /tmp/server.key; exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key",
    ]
    with container(PG_IMAGE, args, mounts) as name:
        wait_for(
            lambda: (
                subprocess.run(
                    [DOCKER, "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres"],
                    capture_output=True,
                    timeout=5,
                ).returncode
                == 0
            )
        )

        def admin(sql, database="document"):
            return run(
                [
                    DOCKER,
                    "exec",
                    "-i",
                    name,
                    "psql",
                    "-X",
                    "-qAt",
                    "-v",
                    "ON_ERROR_STOP=1",
                    "-U",
                    "postgres",
                    "-d",
                    database,
                ],
                sql,
            )

        admin(
            "CREATE ROLE document LOGIN NOINHERIT; CREATE DATABASE document OWNER document;",
            "postgres",
        )
        target = {
            "environment": "disposable",
            "context": "owned-container",
            "dataNamespace": "disposable",
            "serviceNamespace": "disposable",
            "cluster": name,
            "clusterUID": "synthetic-cluster",
            "instanceID": "00000000-0000-4000-8000-000000000362",
            "database": "document",
            "host": "localhost",
            "disposable": True,
        }
        passwords = {r: "a" * 64 for r in resources.ROLES}
        admin(resources.role_sql(passwords, set(), target))
        port = json.loads(run([DOCKER, "inspect", name]))[0]["NetworkSettings"]["Ports"][
            "5432/tcp"
        ][0]["HostPort"]
        ca = private(tmp_path / "ca.crt", (tmp_path / "server.crt").read_text())
        url = private(
            tmp_path / "url",
            f"postgresql://go_document_migrator:{'a' * 64}@localhost:{port}/document?sslmode=verify-full&sslrootcert={ca}",
        )
        target_file = private(tmp_path / "target.json", json.dumps(target))
        binary = os.environ["MIGRATOR_OUTPUT"]
        assert Path(binary).is_file(), "build the owning migrator before integration"

        def migrate(
            op="migrate",
            *,
            failure=False,
            timeout="15s",
            enrollment=target_file,
            trust=ca,
            credential=url,
        ):
            return run(
                [
                    binary,
                    "--operation",
                    op,
                    "--environment",
                    "disposable",
                    "--context",
                    "owned-container",
                    "--target-file",
                    str(enrollment),
                    "--database-url-file",
                    str(credential),
                    "--ca-file",
                    str(trust),
                    "--timeout",
                    timeout,
                ],
                failure=failure,
            )

        yield admin, migrate, target, tmp_path, name


def test_fresh_adoption_repeat_and_privileges(postgres):
    # given
    admin, migrate, target, root, _name = postgres
    assert migrate("status") == "uninitialized"
    assert migrate("version") == "uninitialized"
    assert admin("SELECT to_regclass('public.goose_db_version') IS NULL;") == "t"
    # A mismatched certificate hostname fails at real TLS, with matching enrollment/URL inputs.
    bad_target = dict(target, host="127.0.0.1")
    bad_enrollment = private(root / "bad-host.json", json.dumps(bad_target))
    original = (root / "url").read_text()
    bad_url = private(root / "bad-host-url", original.replace("@localhost:", "@127.0.0.1:"))
    migrate(failure=True, enrollment=bad_enrollment, credential=bad_url)
    assert admin("SELECT to_regclass('public.goose_db_version') IS NULL;") == "t"
    # Wrong issuing CA also fails actual TLS, rather than merely failing URL validation.
    run(
        [
            "openssl",
            "req",
            "-x509",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-days",
            "1",
            "-subj",
            "/CN=wrong-ca",
            "-keyout",
            str(root / "wrong.key"),
            "-out",
            str(root / "wrong.crt"),
        ]
    )
    wrong_ca = private(root / "wrong-ca.crt", (root / "wrong.crt").read_text())
    wrong_url = private(
        root / "wrong-ca-url", original.replace(str(root / "ca.crt"), str(wrong_ca))
    )
    migrate(failure=True, trust=wrong_ca, credential=wrong_url)
    assert admin("SELECT to_regclass('public.goose_db_version') IS NULL;") == "t"
    # Goose may record only version 0 before a failed first migration. That pending state is
    # inspectable/read-only and retryable; it must not be confused with foreign partial schema.
    admin("DROP OWNED BY go_document_reader; DROP ROLE go_document_reader;")
    migrate(failure=True)
    assert admin("SELECT to_regclass('public.policy_document') IS NULL;") == "t"
    assert migrate("status") == "pending"
    assert migrate("version") == "0"
    admin(
        resources.role_sql(
            {role: "a" * 64 for role in resources.ROLES}, {"go_document_migrator"}, target
        )
    )
    # when
    assert migrate() == "version=1"
    # then: exact independent legacy schema, empty table is valid, no JVM required.
    assert (
        admin(
            "SELECT string_agg(column_name||':'||data_type||':'||is_nullable,',' ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='public' AND table_name='policy_document';"
        )
        == "id:bigint:NO,bytes:bytea:YES,policy_number:character varying:YES"
    )
    assert (
        admin(
            "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='public.policy_document'::regclass;"
        )
        == "document"
    )
    assert migrate() == "version=1"
    assert migrate("status") == "version=1"
    assert migrate("version") == "1"
    admin(
        "SET ROLE document; INSERT INTO public.policy_document VALUES(nextval('public.hibernate_sequence'),'\\x25504446','SYN-362'),(nextval('public.hibernate_sequence'),NULL,'SYN-362'); SELECT setval('public.hibernate_sequence',35,true);"
    )
    baseline = admin(
        "SELECT md5(string_agg(id||':'||coalesce(encode(bytes,'hex'),'null')||':'||policy_number,',' ORDER BY id)) FROM public.policy_document;"
    )
    # Turn initialized fixture into an independent Java-owned populated schema, then adopt it.
    admin("SET ROLE document; DROP TABLE public.goose_db_version;")
    assert migrate("status") == "unmanaged"
    assert migrate() == "version=1"
    assert migrate() == "version=1"
    assert (
        admin(
            "SELECT md5(string_agg(id||':'||coalesce(encode(bytes,'hex'),'null')||':'||policy_number,',' ORDER BY id)) FROM public.policy_document;"
        )
        == baseline
    )
    assert admin("SELECT last_value||':'||is_called FROM public.hibernate_sequence;") == "35:true"
    assert (
        admin(
            "SET ROLE document; INSERT INTO public.policy_document VALUES(nextval('public.hibernate_sequence'),'\\x','SYN-362') RETURNING id;"
        )
        == "36"
    )
    # Real reader login via TCP; SET ROLE does not stand in for authentication.
    ca = root / "ca.crt"
    port = json.loads(run([DOCKER, "inspect", _name]))[0]["NetworkSettings"]["Ports"]["5432/tcp"][
        0
    ]["HostPort"]
    pgpass = private(root / "pgpass", f"localhost:*:document:go_document_reader:{'a' * 64}\n")
    env = os.environ.copy()
    env.update(PGPASSFILE=str(pgpass), PGCONNECT_TIMEOUT="3")
    dsn = f"host=localhost port={port} dbname=document user=go_document_reader sslmode=verify-full sslrootcert={ca}"

    def reader(sql):
        result = subprocess.run(
            ["psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-d", dsn],
            input=sql,
            env=env,
            text=True,
            capture_output=True,
            timeout=10,
        )
        assert result.returncode == 0, result.stderr
        return result.stdout.strip()

    resources.verify_reader(reader)
    # Sensitivity: an accidental runtime INSERT grant must fail the same denial check.
    admin("SET ROLE document; GRANT INSERT ON public.policy_document TO go_document_reader;")
    with pytest.raises(AssertionError, match="42501"):
        resources.verify_reader(reader)
    assert admin("SELECT count(*) FROM public.policy_document;") == "3", (
        "failed probe was not rolled back"
    )
    admin("SET ROLE document; REVOKE INSERT ON public.policy_document FROM go_document_reader;")
    resources.verify_reader(reader)

    private(pgpass, pgpass.read_text() + f"localhost:*:postgres:go_document_reader:{'a' * 64}\n")
    other = subprocess.run(
        [
            "psql",
            "-X",
            "-v",
            "VERBOSITY=verbose",
            "-d",
            dsn.replace("dbname=document", "dbname=postgres"),
            "-c",
            "SELECT 1",
        ],
        env=env,
        text=True,
        capture_output=True,
        timeout=10,
    )
    assert other.returncode != 0 and 'permission denied for database "postgres"' in other.stderr, (
        "unrelated database admission was not denied"
    )

    assert (
        admin(
            "SELECT has_sequence_privilege('go_document_reader','public.hibernate_sequence','USAGE,SELECT,UPDATE');"
        )
        == "f"
    )
    assert admin("SELECT pg_has_role('go_document_reader','document','MEMBER');") == "f"
    # Failed history/target checks create no new schema; read-only status cannot bootstrap history.
    bad = dict(target, instanceID="00000000-0000-4000-8000-000000000999")
    mismatch = private(root / "wrong-target.json", json.dumps(bad))
    migrate(failure=True, enrollment=mismatch)
    assert admin("SELECT count(*) FROM public.goose_db_version;") == "2"
    admin("SET ROLE document; UPDATE public.goose_db_version SET version_id=99 WHERE version_id=1;")
    migrate(failure=True)


def test_rejects_unsafe_schema_sequence_and_bounded_contention(postgres):
    # given
    admin, migrate, _target, _root, name = postgres
    admin("ALTER DATABASE document OWNER TO postgres;")
    migrate(failure=True)
    assert admin("SELECT to_regclass('public.goose_db_version') IS NULL;") == "t"
    admin("ALTER DATABASE document OWNER TO document;")
    # Start with independently stated legacy DDL, not output from the new migration.
    admin(
        "SET ROLE document; CREATE TABLE public.policy_document(id bigint PRIMARY KEY,bytes bytea,policy_number varchar(255)); CREATE SEQUENCE public.hibernate_sequence AS bigint; INSERT INTO public.policy_document VALUES(5,'\\x','SYN-362');"
    )
    # when / then: sequence behind existing rows rejects without history creation or repair.
    migrate(failure=True)
    assert admin("SELECT to_regclass('public.goose_db_version') IS NULL;") == "t"
    assert admin("SELECT last_value||':'||is_called FROM public.hibernate_sequence;") == "1:false"
    admin(
        "SET ROLE document; SELECT setval('public.hibernate_sequence',35,true); ALTER TABLE public.policy_document ALTER COLUMN bytes SET DEFAULT '\\x';"
    )
    migrate(failure=True)
    assert admin("SELECT to_regclass('public.goose_db_version') IS NULL;") == "t"
    admin(
        "SET ROLE document; ALTER TABLE public.policy_document ALTER COLUMN bytes DROP DEFAULT; CREATE TABLE public.foreign_fixture(id integer);"
    )
    migrate(failure=True)
    assert admin("SELECT to_regclass('public.goose_db_version') IS NULL;") == "t"
    admin("SET ROLE document; DROP TABLE public.foreign_fixture;")
    # An independent connection holds the actual migration advisory lock.
    proc = subprocess.Popen(
        [DOCKER, "exec", "-i", name, "psql", "-X", "-qAt", "-U", "postgres", "-d", "document"],
        stdin=subprocess.PIPE,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        text=True,
        start_new_session=True,
    )
    try:
        proc.stdin.write("SELECT pg_advisory_lock(190002); SELECT 'locked'; SELECT pg_sleep(10);\n")
        proc.stdin.flush()
        wait_for(
            lambda: (
                admin(
                    "SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND granted AND objid=190002;"
                )
                == "1"
            )
        )
        started = time.monotonic()
        migrate(failure=True, timeout="1s")
        assert time.monotonic() - started < 5
        assert admin("SELECT to_regclass('public.goose_db_version') IS NULL;") == "t"
    finally:
        assert resources.terminate_process_group(proc, 5), "owned lock client cleanup failed"
    # The held backend exits when psql/docker is terminated; success must be possible after cleanup.
    wait_for(
        lambda: (
            admin(
                "SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND granted AND objid=190002;"
            )
            == "0"
        )
    )
    assert migrate() == "version=1"


def test_minio_reader_preserves_credentials_and_denies_writes(tmp_path, monkeypatch):
    # given: isolated real server, root config is private; secrets never enter CLI argv.
    password = "synthetic-minio-admin-362"
    mounts = [
        "--tmpfs",
        "/data:uid=65532,gid=65532,mode=0700",
        "-e",
        "MINIO_ROOT_USER=fixture-admin",
        "-e",
        f"MINIO_ROOT_PASSWORD={password}",
        "-p",
        "127.0.0.1::9000",
        "--entrypoint",
        "minio",
    ]
    with container(MINIO_IMAGE, ["server", "/data", "--address", ":9000"], mounts) as name:
        port = json.loads(run([DOCKER, "inspect", name]))[0]["NetworkSettings"]["Ports"][
            "9000/tcp"
        ][0]["HostPort"]
        endpoint = f"http://127.0.0.1:{port}"
        config_dir = tmp_path / "admin"
        config_dir.mkdir(mode=0o700)
        config = private(
            config_dir / "config.json",
            json.dumps(
                {
                    "version": "10",
                    "aliases": {
                        "document": {
                            "url": endpoint,
                            "accessKey": "fixture-admin",
                            "secretKey": password,
                            "api": "S3v4",
                            "path": "auto",
                        }
                    },
                }
            ),
        )
        minio = resources.Minio(resources.Commands(), config, endpoint)
        wait_for(
            lambda: (
                subprocess.run(
                    ["mc", "--config-dir", str(config_dir), "ready", "document"],
                    capture_output=True,
                    timeout=5,
                ).returncode
                == 0
            )
        )
        try:
            before = minio.inspect()
        except resources.ResourceError as error:
            raise AssertionError(
                f"synthetic fixture inspection: {error.__cause__.stdout}"
            ) from error
        # when
        minio.ensure(before, "a" * 64)
        # then
        try:
            observed = minio.inspect()
        except resources.ResourceError as error:
            raise AssertionError(
                minio.mc("admin", "policy", "info", "document", resources.POLICY_NAME)
            ) from error
        assert observed == {"bucket": True, "user": True, "policy": True, "bound": True}
        fixture = private(tmp_path / "content.pdf", "%PDF-362")
        minio.mc("cp", str(fixture), "document/policies/2026/02/SYN-362.pdf")
        minio.ensure(minio.inspect(), "a" * 64)
        # Simulate interruption after user creation but before its policy binding.
        minio.mc(
            "admin", "policy", "detach", "document", resources.POLICY_NAME, "--user", resources.USER
        )
        partial = minio.inspect()
        assert partial["user"] and not partial["bound"]
        minio.ensure(partial, "a" * 64)
        monkeypatch.setenv("DOCUMENT_RESOURCE_PROBE_KEY", "2026/02/SYN-362.pdf")
        minio.verify("a" * 64, object_probe=True)
        reader_dir = tmp_path / "reader"
        reader_dir.mkdir(mode=0o700)
        reader_config = private(
            reader_dir / "config.json",
            json.dumps(
                {
                    "version": "10",
                    "aliases": {
                        "document": {
                            "url": endpoint,
                            "accessKey": resources.USER,
                            "secretKey": "a" * 64,
                            "api": "S3v4",
                            "path": "auto",
                        }
                    },
                }
            ),
        )
        reader = resources.Minio(resources.Commands(), reader_config, endpoint)
        assert reader.mc("cat", "document/policies/2026/02/SYN-362.pdf") == "%PDF-362"
        # Actual server denials, not a mock or client-side error.
        for args in [
            ("cp", str(fixture), "document/policies/denied.pdf"),
            ("rm", "document/policies/2026/02/SYN-362.pdf"),
            ("admin", "user", "list", "document"),
            ("stat", "document/unrelated-362"),
        ]:
            result = subprocess.run(
                ["mc", "--json", "--config-dir", str(reader_dir), *args],
                text=True,
                capture_output=True,
                timeout=10,
            )
            assert result.returncode != 0
            assert resources.authorization_denied(result.stdout), result.stdout
        assert minio.mc("cat", "document/policies/2026/02/SYN-362.pdf") == "%PDF-362"


def test_lookup_traverses_real_rpc_postgres_and_minio(postgres, tmp_path):
    admin, migrate, _target, root, pg_name = postgres
    assert migrate() == "version=1"
    pg_port = json.loads(run([DOCKER, "inspect", pg_name]))[0]["NetworkSettings"]["Ports"][
        "5432/tcp"
    ][0]["HostPort"]
    admin(
        "SET ROLE document; INSERT INTO public.policy_document(id,bytes,policy_number) VALUES"
        "(1,decode('255044462d333632','hex'),'SYN-362'),"
        "(2,convert_to('2026/02/SYN-362.pdf','UTF8'),'SYN-362'),"
        "(3,convert_to('2026/02/missing.pdf','UTF8'),'SYN-362'),"
        "(4,''::bytea,'SYN-362'),"
        "(5,decode('255044462d333632','hex'),'SYN-362');"
    )

    password = "synthetic-minio-admin-lookup-362"
    mounts = [
        "--tmpfs",
        "/data:uid=65532,gid=65532,mode=0700",
        "-e",
        "MINIO_ROOT_USER=fixture-admin",
        "-e",
        f"MINIO_ROOT_PASSWORD={password}",
        "-p",
        "127.0.0.1::9000",
        "--entrypoint",
        "minio",
    ]
    with container(MINIO_IMAGE, ["server", "/data", "--address", ":9000"], mounts) as name:
        minio_port = json.loads(run([DOCKER, "inspect", name]))[0]["NetworkSettings"]["Ports"][
            "9000/tcp"
        ][0]["HostPort"]
        endpoint = f"http://127.0.0.1:{minio_port}"
        config_dir = tmp_path / "lookup-admin"
        config_dir.mkdir(mode=0o700)
        config = private(
            config_dir / "config.json",
            json.dumps(
                {
                    "version": "10",
                    "aliases": {
                        "document": {
                            "url": endpoint,
                            "accessKey": "fixture-admin",
                            "secretKey": password,
                            "api": "S3v4",
                            "path": "auto",
                        }
                    },
                }
            ),
        )
        minio = resources.Minio(ContainerizedMinioCommands(), config, endpoint)
        wait_for(lambda: minio_ready(minio))
        minio.ensure(minio.inspect(), "a" * 64)
        fixture = private(tmp_path / "lookup.pdf", "%PDF-OBJECT-362")
        minio.mc("cp", str(fixture), "document/policies/2026/02/SYN-362.pdf")

        http_port, grpc_port, health_port = (loopback_port() for _ in range(3))
        environment = os.environ.copy()
        environment.update(
            {
                "HTTP_ADDR": f"127.0.0.1:{http_port}",
                "GRPC_ADDR": f"127.0.0.1:{grpc_port}",
                "HEALTH_ADDR": f"127.0.0.1:{health_port}",
                "DOCUMENT_DATABASE_URL": (
                    f"postgresql://go_document_reader:{'a' * 64}@localhost:{pg_port}/document"
                    f"?sslmode=verify-full&sslrootcert={root / 'ca.crt'}"
                ),
                "MINIO_ENDPOINT": endpoint,
                "MINIO_ACCESS_KEY": resources.USER,
                "MINIO_SECRET_KEY": "a" * 64,
                "MINIO_BUCKET": "policies",
                "OTEL_ENABLED": "false",
            }
        )
        with lookup_server(environment, root) as server_container:
            wait_for(
                lambda: urlopen(f"http://127.0.0.1:{health_port}/readyz", timeout=1).status == 200
            )
            with urlopen(f"http://127.0.0.1:{http_port}/documents/SYN-362", timeout=5) as response:
                payload = json.load(response)
            contents = sorted(item["content"] for item in payload["documents"])
            assert contents == sorted(["JVBERi0zNjI=", "JVBERi0zNjI=", "JVBERi1PQkpFQ1QtMzYy"])
            with urlopen(f"http://127.0.0.1:{http_port}/documents/ABSENT", timeout=5) as response:
                assert json.load(response) == {"documents": []}
            if server_container is not None:
                log_result = subprocess.run(
                    [DOCKER, "logs", server_container],
                    capture_output=True,
                    text=True,
                    timeout=30,
                )
                assert log_result.returncode == 0, "could not read runtime image logs"
                logs = "\n".join((log_result.stdout, log_result.stderr))
                entries = [json.loads(line) for line in logs.splitlines() if line.startswith("{")]
                assert any(
                    entry.get("msg") == "service started"
                    and entry.get("service_version") == os.environ["SOURCE_VERSION"]
                    for entry in entries
                ), "runtime startup log has no exact source version"
                assert "synthetic-minio-admin-lookup-362" not in logs


def test_resource_orchestration_replays_pending_credentials(postgres, tmp_path, monkeypatch):
    # Kubernetes Secret storage is a fake boundary; databases, authentication and IAM are real.
    # This exercises the actual ensure engine without touching a cluster or customer storage.
    admin, _migrate, target, root, pg_name = postgres
    password = "synthetic-minio-admin-362"
    mounts = [
        "--tmpfs",
        "/data:uid=65532,gid=65532,mode=0700",
        "-e",
        "MINIO_ROOT_USER=fixture-admin",
        "-e",
        f"MINIO_ROOT_PASSWORD={password}",
        "-p",
        "127.0.0.1::9000",
        "--entrypoint",
        "minio",
    ]
    with container(MINIO_IMAGE, ["server", "/data", "--address", ":9000"], mounts) as name:
        port = json.loads(run([DOCKER, "inspect", name]))[0]["NetworkSettings"]["Ports"][
            "9000/tcp"
        ][0]["HostPort"]
        endpoint = f"http://127.0.0.1:{port}"
        directory = tmp_path / "mc-admin"
        directory.mkdir(mode=0o700)
        config = private(
            directory / "config.json",
            json.dumps(
                {
                    "version": "10",
                    "aliases": {
                        "document": {
                            "url": endpoint,
                            "accessKey": "fixture-admin",
                            "secretKey": password,
                            "api": "S3v4",
                            "path": "auto",
                        }
                    },
                }
            ),
        )
        minio = resources.Minio(resources.Commands(), config, endpoint)
        wait_for(
            lambda: (
                subprocess.run(
                    ["mc", "--config-dir", str(directory), "ready", "document"],
                    capture_output=True,
                    timeout=5,
                ).returncode
                == 0
            )
        )
        manager = object.__new__(resources.Kubernetes)
        manager.environment = target["environment"]
        manager.context = target["context"]
        manager.data_ns = target["dataNamespace"]
        manager.svc_ns = target["serviceNamespace"]
        manager.cluster = target["cluster"]
        manager.uid = target["clusterUID"]
        manager.host = "localhost"
        manager.endpoint = endpoint
        manager.target_secret = "disposable-postgres-document-admin-target"
        manager.admin = admin
        ca = (root / "ca.crt").read_bytes()
        manager.ca = lambda: ca
        stored = {
            manager.target_secret: {"target.json": json.dumps(target, sort_keys=True).encode()}
        }
        for role, component in zip(resources.ROLES, ("reader", "migrator"), strict=True):
            stored[manager.secret_name(component)] = {
                "password": b"a" * 64,
                "database-url": resources.database_url(role, "a" * 64, "localhost").encode(),
                "ca.crt": ca,
            }
        manager.secret = lambda name: stored.get(name)
        writes = []

        def kube(*_args, text=None):
            record = json.loads(text)
            key = record["metadata"]["name"]
            assert key not in stored, "attempted to overwrite an existing Secret"
            stored[key] = {k: resources.base64.b64decode(v) for k, v in record["data"].items()}
            writes.append(key)
            return ""

        manager.kube = kube
        pg_port = json.loads(run([DOCKER, "inspect", pg_name]))[0]["NetworkSettings"]["Ports"][
            "5432/tcp"
        ][0]["HostPort"]

        @contextmanager
        def connection(role, password, _ca):
            pgpass = private(root / f"{role}.pgpass", f"localhost:*:document:{role}:{password}\n")
            env = os.environ.copy()
            env.update(PGPASSFILE=str(pgpass), PGCONNECT_TIMEOUT="3")
            dsn = f"host=localhost port={pg_port} dbname=document user={role} sslmode=verify-full sslrootcert={root / 'ca.crt'}"

            def execute(sql):
                result = subprocess.run(
                    ["psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-d", dsn],
                    input=sql,
                    env=env,
                    text=True,
                    capture_output=True,
                    timeout=10,
                )
                assert result.returncode == 0, result.stderr
                return result.stdout.strip()

            yield execute

        manager.connection = connection
        # Simulate a pending credential whose external role has not survived setup.
        admin(
            "REVOKE document FROM go_document_migrator; DROP OWNED BY go_document_migrator; DROP ROLE go_document_migrator;"
        )
        resources.execute_action("ensure", manager, minio)
        snapshot = dict(stored)
        resources.execute_action("ensure", manager, minio)
        assert stored == snapshot
        assert len(writes) == 1  # only the absent MinIO credential was created
        with manager.connection("go_document_migrator", "a" * 64, ca) as execute:
            assert execute("SET ROLE document; SELECT current_user;") == "document"
        # Unrecoverable external credentials are rejected before any new writes.
        saved = stored.pop(manager.secret_name("reader"))
        with pytest.raises(resources.ResourceError, match="without recoverable Secret"):
            resources.execute_action("ensure", manager, minio)
        stored[manager.secret_name("reader")] = saved
        assert len(writes) == 1
