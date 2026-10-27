"""Product administration role tooling tests; shared targets are never selected."""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import tempfile
import time
from contextlib import contextmanager
from pathlib import Path
from unittest.mock import patch

import pytest

from scripts.postgres.service.test_reader import IMAGE, run
from scripts.product_deployment import administration

PASSWORD = "administration-disposable-only"


def test_administration_urls_are_role_specific_and_canonical() -> None:
    # given
    host = "qa-postgres-product-rw.qa-data.svc.cluster.local"
    password = "a" * 64

    # when / then
    values = {
        role: administration.database_url(role, password, host) for role in administration.ROLES
    }
    assert len(set(values.values())) == 3
    for role, value in values.items():
        assert administration.password_from_url(role, value, host) == password
    with pytest.raises(administration.reader.RoleError):
        administration.password_from_url("go_product_migrator", values["go_product_seeder"], host)


def test_target_document_binds_cluster_uid_and_separate_namespaces() -> None:
    # given
    manager = object.__new__(administration.AdministrationKubernetes)
    manager.environment = "qa"
    manager.context = "qa-insurance-hub"
    manager.data_ns = "qa-data"
    manager.svc_ns = "qa-svc"
    manager.cluster = "qa-postgres-product"
    manager.cluster_uid = "00000000-0000-4000-8000-000000000190"
    manager.hostname = "qa-postgres-product-rw.qa-data.svc.cluster.local"

    # when
    target = administration.target_document(manager, "00000000-0000-4000-8000-000000000191")

    # then
    assert target == {
        "environment": "qa",
        "context": "qa-insurance-hub",
        "dataNamespace": "qa-data",
        "serviceNamespace": "qa-svc",
        "cluster": "qa-postgres-product",
        "clusterUID": "00000000-0000-4000-8000-000000000190",
        "instanceID": "00000000-0000-4000-8000-000000000191",
        "database": "product",
        "host": "qa-postgres-product-rw.qa-data.svc.cluster.local",
        "seedAllowed": True,
        "disposable": False,
    }


def test_role_catalog_dispatches_combined_administration_profile() -> None:
    # when
    from scripts.postgres.service import dispatch

    path = dispatch.resolve("verify", "product", "administration")

    # then
    assert path == Path(administration.__file__).resolve()


def test_administration_script_runs_from_outside_repository(tmp_path: Path) -> None:
    # when
    result = subprocess.run(
        [sys.executable, str(Path(administration.__file__).resolve()), "unsupported"],
        cwd=tmp_path,
        text=True,
        capture_output=True,
        timeout=10,
    )

    # then
    assert result.returncode == 2
    assert result.stdout == ""
    assert result.stderr == "product-administration: unsupported administration action\n"


class DisposableAdministration:
    environment = "qa"
    context = "qa-insurance-hub"
    data_ns = "qa-data"
    svc_ns = "qa-svc"
    cluster = "qa-postgres-product"
    cluster_uid = "00000000-0000-4000-8000-000000000190"
    hostname = "127.0.0.1"
    target_secret = "qa-postgres-product-admin-target"

    def __init__(self, container: str, port: int):
        self.container = container
        self.port = port
        self.secrets: dict[str, dict[str, bytes]] = {}
        self.fail_secret: str | None = None

    def role_secret(self, role: str) -> str:
        return f"qa-postgres-product-{administration.SECRET_SUFFIX[role]}"

    def admin(self, sql: str) -> str:
        return run(
            [
                "docker",
                "exec",
                "-i",
                self.container,
                "psql",
                "-X",
                "-qAt",
                "-v",
                "ON_ERROR_STOP=1",
                "-U",
                "postgres",
                "-d",
                "product",
            ],
            input_text=sql,
        )

    def get_secret(self, name: str, namespace: str):
        assert namespace == self.svc_ns
        return self.secrets.get(name)

    def put_named_secret(self, name: str, values: dict[str, bytes]) -> None:
        if name == self.fail_secret:
            raise administration.reader.RoleError("injected disposable Secret write failure")
        self.secrets[name] = values

    def delete_named_secret(self, name: str) -> None:
        self.secrets.pop(name, None)

    def ca(self) -> bytes:
        return b"-----BEGIN CERTIFICATE-----\ntest-only\n-----END CERTIFICATE-----\n"

    def kubectl(self, *arguments: str) -> str:
        assert arguments == (
            "-n",
            self.svc_ns,
            "get",
            "jobs",
            "-l",
            "app.kubernetes.io/name=product-init",
            "-o",
            "name",
        )
        return ""

    @contextmanager
    def role_connection(self, role: str, password: str, _ca: bytes):
        with tempfile.TemporaryDirectory(prefix="product-admin-role-") as directory:
            passfile = Path(directory) / "pgpass"
            passfile.write_text(f"127.0.0.1:{self.port}:product:{role}:{password}\n")
            passfile.chmod(0o600)
            environment = {
                key: value for key, value in os.environ.items() if not key.startswith("PG")
            }
            environment.update(PGPASSFILE=str(passfile), PGCONNECT_TIMEOUT="5")
            options = f"host=127.0.0.1 port={self.port} dbname=product user={role} sslmode=disable"

            def execute(sql: str) -> str:
                result = subprocess.run(
                    ["psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-d", options],
                    input=sql,
                    text=True,
                    capture_output=True,
                    timeout=15,
                    env=environment,
                )
                if result.returncode:
                    raise administration.reader.RoleError("disposable administration SQL rejected")
                state = re.search(r"reader-probe-state:([0-9A-Z]{5})", result.stderr)
                return result.stdout.strip() + (
                    "\nreader-probe-state:" + state.group(1) if state else ""
                )

            yield execute


@contextmanager
def disposable_database():
    assert "PRODUCT_DATABASE_URL" not in os.environ
    run(["docker", "image", "inspect", IMAGE])
    name = "issue190-admin-" + os.urandom(5).hex()
    port = 55432 + int.from_bytes(os.urandom(2), "big") % 500
    try:
        run(
            [
                "docker",
                "run",
                "-d",
                "--rm",
                "--name",
                name,
                "-p",
                f"127.0.0.1:{port}:5432",
                "-e",
                "POSTGRES_PASSWORD",
                "-e",
                "POSTGRES_DB=product",
                IMAGE,
            ],
            env=dict(os.environ, POSTGRES_PASSWORD=PASSWORD),
        )
        manager = DisposableAdministration(name, port)
        for _ in range(60):
            try:
                if manager.admin("SELECT 1") == "1":
                    break
            except AssertionError:
                time.sleep(0.25)
        else:
            pytest.fail("owned PostgreSQL did not become ready")
        manager.admin(
            f"CREATE ROLE product LOGIN NOINHERIT PASSWORD '{PASSWORD}'; "
            "ALTER DATABASE product OWNER TO product; ALTER SCHEMA public OWNER TO product;"
        )
        yield manager
    finally:
        if name in run(["docker", "ps", "-a", "--format", "{{.Names}}"]).splitlines():
            run(["docker", "rm", "-f", name])


def test_compensation_sql_is_scoped_to_created_roles_and_exact_marker() -> None:
    # given
    target = {
        "environment": "qa",
        "instanceID": "00000000-0000-4000-8000-000000000191",
        "clusterUID": "00000000-0000-4000-8000-000000000190",
        "seedAllowed": True,
    }

    # when
    sql = administration.compensation_sql(
        ("go_product_migrator", "go_product_seeder"),
        target,
        remove_target=True,
        remove_migrator_membership=True,
    )

    # then
    assert "DROP OWNED BY go_product_migrator,go_product_seeder" in sql
    assert "DROP ROLE IF EXISTS go_product_reader" not in sql
    assert "REVOKE product FROM go_product_migrator" in sql
    assert "instance_id='00000000-0000-4000-8000-000000000191'::uuid" in sql


@pytest.mark.integration
def test_initial_provision_secret_failure_compensates_and_retries() -> None:
    # given
    with disposable_database() as manager:
        manager.fail_secret = manager.role_secret("go_product_seeder")

        # when
        with pytest.raises(
            administration.reader.RoleError,
            match="administration provisioning failed; partial state compensated",
        ):
            administration.provision(manager)

        # then
        assert manager.secrets == {}
        assert administration.role_inventory(manager) == {
            "roles": [],
            "memberships": [],
            "target": None,
        }
        assert (
            manager.admin("SELECT to_regclass('product_admin.target_identity') IS NOT NULL;") == "t"
        )
        assert manager.admin("SELECT count(*) FROM product_admin.target_identity;") == "0"

        manager.fail_secret = None
        administration.provision(manager)
        administration.verify(manager)


@pytest.mark.integration
def test_initial_provision_compensation_preserves_existing_reader() -> None:
    # given
    with disposable_database() as manager:
        password = "r" * 64
        manager.admin(
            "CREATE ROLE go_product_reader LOGIN NOINHERIT PASSWORD "
            f"'{password}'; GRANT CONNECT ON DATABASE product TO go_product_reader; "
            "GRANT USAGE,CREATE ON SCHEMA public TO go_product_reader;"
        )
        reader_secret = administration.secret_values(
            "go_product_reader", password, manager.hostname, manager.ca()
        )
        manager.secrets[manager.role_secret("go_product_reader")] = reader_secret
        manager.fail_secret = manager.role_secret("go_product_seeder")

        # when
        with pytest.raises(
            administration.reader.RoleError,
            match="administration provisioning failed; partial state compensated",
        ):
            administration.provision(manager)

        # then
        state = administration.role_inventory(manager)
        assert [item["rolname"] for item in state["roles"]] == ["go_product_reader"]
        assert state["memberships"] == []
        assert state["target"] is None
        assert manager.secrets == {manager.role_secret("go_product_reader"): reader_secret}
        assert (
            manager.admin(
                "SELECT has_database_privilege('go_product_reader','product','CONNECT') "
                "AND has_schema_privilege('go_product_reader','public','USAGE') "
                "AND has_schema_privilege('go_product_reader','public','CREATE');"
            )
            == "t"
        )


@pytest.mark.integration
def test_provisioned_roles_and_secrets_enforce_real_permissions() -> None:
    # given
    with disposable_database() as manager:
        # when
        administration.provision(manager)
        administration.verify(manager)
        first_secrets = json.loads(
            json.dumps(
                {
                    key: {name: value.decode() for name, value in values.items()}
                    for key, values in manager.secrets.items()
                }
            )
        )
        manager.admin(
            "SET ROLE product; "
            "CREATE TABLE public.product(code varchar(255) PRIMARY KEY,definition jsonb NOT NULL); "
            "INSERT INTO public.product VALUES('CAR','{}'); "
            "GRANT SELECT ON public.product TO go_product_reader; RESET ROLE;"
        )
        administration.verify(manager)
        manager.admin(
            "SET ROLE product; "
            "CREATE TABLE public.goose_db_version(id integer GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,"
            "version_id bigint NOT NULL,is_applied boolean NOT NULL,tstamp timestamp NOT NULL DEFAULT now()); "
            "INSERT INTO public.goose_db_version(version_id,is_applied) VALUES(0,true),(1,true); "
            "GRANT SELECT,INSERT ON public.product TO go_product_seeder; "
            "GRANT SELECT ON public.goose_db_version TO go_product_seeder; RESET ROLE;"
        )
        administration.verify(manager)
        administration.reader.consumer_gate(administration.reader.inventory(manager.admin))
        administration.provision(manager)

        # then
        assert first_secrets == {
            key: {name: value.decode() for name, value in values.items()}
            for key, values in manager.secrets.items()
        }
        state = administration.role_inventory(manager)
        target = json.loads(manager.secrets[manager.target_secret]["target.json"])
        administration.check_role_contract(state, target)
        assert manager.admin("SELECT count(*) FROM public.product;") == "1"

        with tempfile.TemporaryDirectory() as directory:
            recovery = Path(directory) / "administration-recovery.json"
            with patch.dict(
                os.environ,
                {"POSTGRES_ROLE_RECOVERY_FILE": str(recovery)},
                clear=False,
            ):
                administration.rotate(manager)
            assert recovery.stat().st_mode & 0o777 == 0o600
            assert recovery.read_text() != ""
        administration.verify(manager)

        with patch.dict(
            os.environ,
            {"POSTGRES_ROLE_CONFIRM_REVOKE": "product/administration/qa"},
            clear=False,
        ):
            administration.revoke(manager)
        assert (
            manager.admin(
                "SELECT count(*) FROM pg_roles WHERE rolname IN "
                "('go_product_migrator','go_product_seeder');"
            )
            == "0"
        )
        assert (
            manager.admin("SELECT to_regclass('product_admin.target_identity') IS NOT NULL;") == "t"
        )
