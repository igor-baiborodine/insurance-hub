"""Q04 decision probes; synthetic Docker databases only, never a deployment command."""

from __future__ import annotations

import json
import os
import subprocess
import time
from contextlib import contextmanager

import pytest

from scripts.postgres.service.test_reader import IMAGE, DisposableProduct, run
from scripts.product_deployment.reader import safe_probe_sql

PASSWORD = "q04-disposable-only"


@contextmanager
def database():
    """Require the provisioned image and own precisely one disposable container."""
    assert "PRODUCT_DATABASE_URL" not in os.environ
    run(["docker", "image", "inspect", IMAGE])
    name = "issue190-q04-" + os.urandom(6).hex()
    manager = DisposableProduct(name, 0)
    try:
        run(
            [
                "docker",
                "run",
                "-d",
                "--rm",
                "--name",
                name,
                "-e",
                "POSTGRES_PASSWORD",
                "-e",
                "POSTGRES_DB=product",
                IMAGE,
            ],
            env=dict(os.environ, POSTGRES_PASSWORD=PASSWORD),
        )
        for _ in range(60):
            try:
                if manager.admin("SELECT 1") == "1":
                    break
            except AssertionError:
                time.sleep(0.25)
        else:
            pytest.fail("owned PostgreSQL did not become ready")
        yield manager
    finally:
        if name in run(["docker", "ps", "-a", "--format", "{{.Names}}"]).splitlines():
            run(["docker", "rm", "-f", name])
        assert name not in run(["docker", "ps", "-a", "--format", "{{.Names}}"])


@pytest.mark.integration
def test_partial_start_is_cleaned(monkeypatch):
    # given
    original = run
    created = []

    def lose_acknowledgement(argv, **kwargs):
        output = original(argv, **kwargs)
        if argv[:2] == ["docker", "run"]:
            created.append(argv[argv.index("--name") + 1])
            raise AssertionError("injected start acknowledgement loss")
        return output

    monkeypatch.setitem(database.__wrapped__.__globals__, "run", lose_acknowledgement)

    # when / then
    with pytest.raises(AssertionError, match="injected start acknowledgement loss"):
        with database():
            pytest.fail("setup unexpectedly succeeded")

    # then
    assert len(created) == 1
    assert created[0] not in original(["docker", "ps", "-a", "--format", "{{.Names}}"])


def login(manager, role, sql):
    return subprocess.run(
        [
            "docker",
            "exec",
            "-i",
            "-e",
            "PGPASSWORD",
            manager.container,
            "psql",
            "-X",
            "-qAt",
            "-v",
            "ON_ERROR_STOP=1",
            "-v",
            "VERBOSITY=verbose",
            "-h",
            "127.0.0.1",
            "-U",
            role,
            "-d",
            "product",
        ],
        input=sql,
        text=True,
        capture_output=True,
        timeout=15,
        env=dict(os.environ, PGPASSWORD=PASSWORD),
    )


def allowed(manager, role, sql):
    result = login(manager, role, sql)
    assert result.returncode == 0, f"{role} permitted probe failed"
    return result.stdout.strip()


def denied(manager, role, sql):
    result = login(manager, role, safe_probe_sql(sql))
    assert result.returncode == 0
    assert "reader-probe-state:42501" in result.stderr, f"{role} privilege denial missing"


def fingerprint(manager):
    return manager.admin("""
SELECT json_build_array(
 (SELECT md5(string_agg(code||':'||definition::text,',' ORDER BY code)) FROM public.product),
 (SELECT row(oid,relfilenode,relowner)::text FROM pg_class WHERE oid='public.product'::regclass),
 (SELECT md5(string_agg(row(id,version_id,is_applied,tstamp)::text,',' ORDER BY id))
  FROM public.goose_db_version));
""")


def provision(manager, adopted):
    manager.admin(f"""
CREATE ROLE product LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD '{PASSWORD}';
ALTER DATABASE product OWNER TO product;
ALTER SCHEMA public OWNER TO product;
CREATE ROLE go_product_migrator LOGIN NOINHERIT PASSWORD '{PASSWORD}';
CREATE ROLE go_product_seeder LOGIN NOINHERIT PASSWORD '{PASSWORD}';
CREATE ROLE go_product_reader LOGIN NOINHERIT PASSWORD '{PASSWORD}';
GRANT product TO go_product_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE;
REVOKE ALL ON DATABASE product FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE product TO go_product_migrator,go_product_seeder,go_product_reader;
GRANT USAGE ON SCHEMA public TO go_product_seeder,go_product_reader;
ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE product IN SCHEMA public REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE ALL ON SEQUENCES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
CREATE SCHEMA product_admin AUTHORIZATION postgres;
REVOKE ALL ON SCHEMA product_admin FROM PUBLIC;
CREATE TABLE product_admin.target_identity(
 singleton boolean PRIMARY KEY CHECK(singleton), environment text NOT NULL,
 instance_id text NOT NULL, seed_allowed boolean NOT NULL);
INSERT INTO product_admin.target_identity VALUES(true,'disposable','q04-owned',true);
GRANT USAGE ON SCHEMA product_admin TO product,go_product_migrator,go_product_seeder;
GRANT SELECT ON product_admin.target_identity TO product,go_product_migrator,go_product_seeder;
""")
    if adopted:
        allowed(
            manager,
            "product",
            """
CREATE TABLE public.product(code varchar(255) PRIMARY KEY, definition jsonb NOT NULL);
INSERT INTO public.product VALUES ('CAR','{"existing":true,"amount":1.2300}');
""",
        )
    # Q03 already proves Goose history/adoption mechanics. This probe isolates Q04 grants.
    allowed(
        manager,
        "go_product_migrator",
        """
SET ROLE product;
BEGIN;
CREATE TABLE IF NOT EXISTS public.product(code varchar(255) PRIMARY KEY, definition jsonb NOT NULL);
CREATE TABLE public.goose_db_version(id integer GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
 version_id bigint NOT NULL,is_applied boolean NOT NULL,tstamp timestamp NOT NULL DEFAULT now());
INSERT INTO public.goose_db_version(version_id,is_applied) VALUES(0,true),(1,true);
GRANT SELECT ON public.product TO go_product_reader;
GRANT SELECT,INSERT ON public.product TO go_product_seeder;
GRANT SELECT ON public.goose_db_version TO go_product_seeder;
COMMIT;
""",
    )


@pytest.mark.integration
@pytest.mark.parametrize("adopted", [False, True], ids=["fresh", "adopted"])
def test_administration_role_matrix(adopted):
    # given
    with database() as manager:
        provision(manager, adopted)
        before = fingerprint(manager)

        # when
        reader_count = allowed(manager, "go_product_reader", "SELECT count(*) FROM public.product")

        # then
        assert reader_count == str(int(adopted))
        for sql in (
            "INSERT INTO public.product VALUES ('DENIED','{}')",
            "UPDATE public.product SET definition='{}'",
            "DELETE FROM public.product",
            "TRUNCATE public.product",
            "ALTER TABLE public.product ADD COLUMN denied text",
            "DROP TABLE public.product",
            "CREATE TABLE public.denied(id int)",
            "CREATE SCHEMA denied",
            "CREATE TEMP TABLE denied(id int)",
            "SET ROLE product",
            "SELECT * FROM public.goose_db_version",
            "INSERT INTO public.goose_db_version(version_id,is_applied) VALUES(9,true)",
            "UPDATE public.goose_db_version SET version_id=9",
            "DELETE FROM public.goose_db_version",
            "TRUNCATE public.goose_db_version",
            "DROP TABLE public.goose_db_version",
            "ALTER TABLE public.goose_db_version ADD COLUMN denied int",
            "SELECT nextval('public.goose_db_version_id_seq')",
            "UPDATE product_admin.target_identity SET seed_allowed=true",
        ):
            denied(manager, "go_product_reader", sql)
        for sql in (
            "UPDATE public.product SET definition='{}'",
            "DELETE FROM public.product",
            "TRUNCATE public.product",
            "ALTER TABLE public.product ADD COLUMN denied int",
            "DROP TABLE public.product",
            "CREATE TABLE public.denied(id int)",
            "SET ROLE product",
            "UPDATE public.goose_db_version SET version_id=9",
            "SELECT nextval('public.goose_db_version_id_seq')",
            "UPDATE product_admin.target_identity SET environment='qa'",
        ):
            denied(manager, "go_product_seeder", sql)
        assert fingerprint(manager) == before
        membership = json.loads(
            manager.admin("""
SELECT coalesce(json_agg(json_build_array(r.rolname,m.inherit_option,m.set_option,m.admin_option)), '[]')
FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.member
WHERE m.roleid='product'::regrole;
""")
        )
        assert membership == [["go_product_migrator", False, True, False]]
        assert (
            manager.admin("""
SELECT count(*) FROM pg_roles WHERE rolname IN ('go_product_reader','go_product_seeder','go_product_migrator')
AND (rolsuper OR rolcreatedb OR rolcreaterole OR rolinherit OR rolreplication OR rolbypassrls);
""")
            == "0"
        )
        # given
        manager.admin("GRANT INSERT ON public.product TO go_product_reader")

        # when / then
        with pytest.raises(AssertionError, match="privilege denial missing"):
            denied(
                manager, "go_product_reader", "INSERT INTO public.product VALUES ('MUTANT','{}')"
            )
        manager.admin("REVOKE INSERT ON public.product FROM go_product_reader")
        assert fingerprint(manager) == before
        # when
        allowed(
            manager, "go_product_migrator", "SET ROLE product; CREATE TABLE public.future(id int)"
        )

        # then
        denied(manager, "go_product_reader", "SELECT * FROM public.future")
        denied(manager, "go_product_seeder", "SELECT * FROM public.future")
        assert (
            manager.admin("""
SELECT count(*) FROM pg_default_acl d, LATERAL aclexplode(d.defaclacl) a
WHERE d.defaclrole='product'::regrole AND a.grantee<>d.defaclrole;
""")
            == "0"
        )
        # given
        seed = """
BEGIN;
INSERT INTO public.product VALUES ('CAR','{"seed":true}'),('TRI','{}') ON CONFLICT(code) DO NOTHING;
COMMIT;
"""
        # when
        allowed(manager, "go_product_seeder", seed)
        seeded = fingerprint(manager)
        allowed(manager, "go_product_seeder", seed)

        # then
        assert fingerprint(manager) == seeded
        if adopted:
            assert (
                allowed(
                    manager,
                    "go_product_reader",
                    "SELECT definition->>'existing' FROM public.product WHERE code='CAR'",
                )
                == "true"
            )


@pytest.mark.integration
def test_target_marker_cannot_be_relabelled_by_seed_caller():
    # given
    with database() as manager:
        provision(manager, True)
        before = fingerprint(manager)

        # when
        for actual, requested, enabled, accepted in (
            ("disposable", "disposable", True, True),
            ("qa", "qa", True, True),
            ("local-dev", "local-dev", True, True),
            ("production", "qa", True, False),
            ("production", "production", True, False),
            ("qa", "local-dev", True, False),
            ("qa", "qa", False, False),
            ("qa", "", True, False),
        ):
            manager.admin(
                f"UPDATE product_admin.target_identity SET environment='{actual}',seed_allowed={str(enabled).lower()}"
            )
            observed = allowed(
                manager,
                "go_product_seeder",
                f"""
SELECT count(*) FROM product_admin.target_identity WHERE singleton AND seed_allowed
AND environment='{requested}' AND environment IN ('local-dev','qa','disposable')
AND instance_id='q04-owned';
""",
            )
            assert (observed == "1") is accepted

        # then
        denied(
            manager,
            "go_product_seeder",
            "UPDATE product_admin.target_identity SET environment='qa'",
        )
        assert fingerprint(manager) == before
