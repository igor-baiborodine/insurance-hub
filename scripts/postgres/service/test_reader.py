#!/usr/bin/env python3
"""Dispatch guards and the Product role contract in disposable PostgreSQL."""

import base64
from contextlib import contextmanager, redirect_stdout
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import tempfile
import time
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
MODULE = ROOT / "scripts/product-deployment/reader.py"
spec = importlib.util.spec_from_file_location("product_reader", MODULE)
reader = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reader)
IMAGE = "postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193"


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def run(argv, *, input_text=None, env=None, timeout=45):
    result = subprocess.run(argv, input=input_text, text=True, capture_output=True,
                            timeout=timeout, env=env)
    if result.returncode:
        raise AssertionError(f"test command failed: {argv[0]} (exit {result.returncode})")
    return result.stdout.strip()


def dispatch_guards():
    dispatch = ROOT / "scripts/postgres/service/dispatch.py"
    for service, profile, action in (("", "reader", "inventory"), ("product", "", "inventory"),
                                     ("../product", "reader", "inventory"),
                                     ("unknown", "reader", "inventory"),
                                     ("product", "admin", "inventory"),
                                     ("product", "reader", "deploy")):
        env = dict(os.environ, SVC_NAME=service, ROLE_PROFILE=profile)
        result = subprocess.run([sys.executable, str(dispatch), action], env=env,
                                capture_output=True, text=True, timeout=5)
        check(result.returncode != 0, f"dispatch accepted invalid {service}/{profile}/{action}")
        check("password" not in result.stderr.lower(), "dispatch diagnostic exposed credential text")
    selected = reader_path("product", "reader")
    check(selected == MODULE, "Product reader did not resolve to its service-owned script")
    spec = importlib.util.spec_from_file_location("dispatch", dispatch)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    check(module.resolve("snapshot", "product", "reader") == MODULE,
          "Product snapshot did not resolve to its service-owned script")


def reader_path(service, profile):
    spec = importlib.util.spec_from_file_location("dispatch", ROOT / "scripts/postgres/service/dispatch.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.resolve("inventory", service, profile)


def secret_adapter_test():
    manager = object.__new__(reader.Kubernetes)
    manager.context = "kind-local-dev-insurance-hub"
    manager.data_ns = "local-dev-all"
    manager.svc_ns = "local-dev-all"
    manager.secret_name = "local-dev-postgres-product-reader-creds"
    manager.ca_name = "local-dev-postgres-product-ca"
    sent = {}

    def kubectl(*args, input_text=None):
        if "apply" in args:
            check(args == ("-n", "local-dev-all", "apply", "-f", "-"),
                  "Secret apply used unexpected arguments")
            sent.update(json.loads(input_text))
            return ""
        if "get" in args and "secret" in args:
            name = args[args.index("secret") + 1]
            if name == manager.ca_name:
                return json.dumps({"metadata": {"name": name}, "data": {
                    "ca.crt": base64.b64encode(b"-----BEGIN CERTIFICATE-----\ntest\n").decode(),
                    "ca.key": base64.b64encode(b"not-copied").decode()}})
            return json.dumps(sent)
        raise AssertionError("unexpected Secret adapter operation")

    manager.kubectl = kubectl
    password = "a" * 64
    values = reader.secret_values(password, "local-dev-postgres-product-rw.local-dev-all.svc.cluster.local",
                                  manager.ca())
    manager.put_secret(values)
    check(sent["metadata"] == {"name": manager.secret_name, "namespace": manager.svc_ns},
          "Secret identity/namespace mismatch")
    check(set(sent["data"]) == {"database-url", "ca.crt"} and "ca.key" not in sent["data"],
          "Secret contains an unexpected key")
    check(manager.get_secret(manager.secret_name, manager.svc_ns) == values,
          "Secret roundtrip changed protected data")


def deployment_ca_contract_test():
    deployment = (ROOT / "k8s/apps/svc/product/base/deployment.yaml").read_text()
    match = re.search(r"- name: postgres-ca\n\s+mountPath: (\S+)", deployment)
    check(match is not None, "Product Deployment has no postgres-ca mount")
    check(reader.CA_MOUNT == f"{match.group(1)}/ca.crt",
          "reader URL CA path differs from the Product Deployment mount")


def forward_cleanup_test():
    manager = object.__new__(reader.Kubernetes)
    manager.context = "kind-local-dev-insurance-hub"
    manager.data_ns = "local-dev-all"
    manager.primary = "local-dev-postgres-product-1"
    manager.hostname = "local-dev-postgres-product-rw.local-dev-all.svc.cluster.local"

    class FailedForward:
        terminated = False

        def poll(self):
            return 1

        def terminate(self):
            self.terminated = True

        def wait(self, timeout=None):
            return 1

    process = FailedForward()
    with patch.object(reader.subprocess, "Popen", return_value=process):
        try:
            with manager.connection("a" * 64, b"-----BEGIN CERTIFICATE-----\ntest\n") as runtime:
                runtime("SELECT 1")
                raise AssertionError("failed port-forward executed reader operation")
        except reader.RoleError:
            pass
    check(process.terminated, "failed port-forward was not cleaned up")


class DisposableProduct:
    environment = "disposable"
    hostname = "127.0.0.1"
    secret_name = "disposable-postgres-product-reader-creds"
    svc_ns = "disposable"

    def __init__(self, container, port):
        self.container = container
        self.port = port
        self.secret = None
        self.fail_put = False

    def admin(self, sql):
        return run(["docker", "exec", "-i", self.container, "psql", "-X", "-q", "-A", "-t",
                    "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", "product"], input_text=sql)

    def get_secret(self, name, namespace):
        check(name == self.secret_name and namespace == self.svc_ns, "wrong Secret target")
        return self.secret

    def put_secret(self, values):
        if self.fail_put:
            self.fail_put = False
            raise reader.RoleError("injected Secret write failure")
        self.secret = values

    def ca(self):
        return b"-----BEGIN CERTIFICATE-----\ntest-only\n-----END CERTIFICATE-----\n"

    def kubectl(self, *args):
        if "deployment" in args:
            return ""
        raise AssertionError("unexpected disposable kubectl operation")

    def delete_secret(self):
        self.secret = None

    @contextmanager
    def connection(self, password, ca):
        with tempfile.TemporaryDirectory(prefix="product-reader-disposable-") as directory:
            passfile = Path(directory) / "pgpass"
            passfile.write_text(f"127.0.0.1:{self.port}:product:go_product_reader:{password}\n")
            passfile.chmod(0o600)
            env = {key: value for key, value in os.environ.items() if not key.startswith("PG")}
            env.update(PGPASSFILE=str(passfile), PGCONNECT_TIMEOUT="5")
            options = f"host=127.0.0.1 port={self.port} dbname=product user=go_product_reader sslmode=disable"

            def execute(sql):
                result = subprocess.run(["psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1",
                                         "-d", options], input=sql, text=True, capture_output=True,
                                        timeout=15, env=env)
                if result.returncode:
                    raise reader.RoleError("disposable reader SQL rejected")
                probe = re.search(r"reader-probe-state:([0-9A-Z]{5})", result.stderr)
                return result.stdout.strip() + ("\nreader-probe-state:" + probe.group(1)
                                                if probe else "")

            yield execute


def disposable_test():
    print("product-reader-test: pulling pinned disposable PostgreSQL image", flush=True)
    run(["docker", "pull", IMAGE], timeout=180)
    run(["docker", "image", "inspect", IMAGE])
    print("product-reader-test: starting disposable PostgreSQL", flush=True)
    name = "issue134-reader-" + os.urandom(5).hex()
    run(["docker", "run", "-d", "--rm", "--name", name, "-e", "POSTGRES_PASSWORD",
         "-e", "POSTGRES_DB=product", "-p", "127.0.0.1::5432", IMAGE],
        env=dict(os.environ, POSTGRES_PASSWORD="disposable-admin"))
    try:
        for _ in range(60):
            ready = subprocess.run(["docker", "exec", name, "psql", "-X", "-q", "-A", "-t",
                                    "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", "product",
                                    "-c", "SELECT 1"], capture_output=True)
            if ready.returncode == 0:
                break
            time.sleep(0.25)
        else:
            raise AssertionError("disposable PostgreSQL did not become ready")
        port_line = run(["docker", "port", name, "5432/tcp"])
        port = int(port_line.rsplit(":", 1)[1])
        manager = DisposableProduct(name, port)
        manager.admin("""
CREATE ROLE product LOGIN PASSWORD 'disposable-owner';
ALTER DATABASE product OWNER TO product;
ALTER SCHEMA public OWNER TO product;
SET ROLE product;
CREATE TABLE public.product(code text PRIMARY KEY, definition jsonb NOT NULL);
INSERT INTO public.product VALUES ('CAR','{}'),('HOME','{}'),('LIFE','{}'),('TRI','{}');
RESET ROLE;
""")
        before = reader.snapshot(manager.admin)
        reader.provision(manager)
        first = dict(manager.secret)
        credential = reader.password_from_url(first["database-url"].decode(), manager.hostname)
        output = io.StringIO()
        with redirect_stdout(output):
            reader.provision(manager)
        check(credential not in output.getvalue(), "reapply output exposed credential")
        check(manager.secret == first, "ordinary reapply rotated the credential")
        reader.verify(manager)

        # Existing grants drift is rejected by verify and deliberately repaired by provision.
        manager.admin("GRANT INSERT ON public.product TO go_product_reader;")
        try:
            reader.verify(manager)
            raise AssertionError("unexpected reader INSERT grant was accepted")
        except reader.RoleError:
            pass
        reader.provision(manager)
        manager.admin("GRANT SELECT ON public.product TO PUBLIC;")
        try:
            reader.verify(manager)
            raise AssertionError("unexpected PUBLIC table grant was accepted")
        except reader.RoleError:
            pass
        reader.provision(manager)

        # Complete destructive matrix is restricted to this disposable database.
        full_matrix = (
            "INSERT INTO public.product(code,definition) VALUES ('DISPOSABLE','{}'::jsonb)",
            "UPDATE public.product SET definition='{}'::jsonb WHERE code='CAR'",
            "DELETE FROM public.product WHERE code='CAR'",
            "TRUNCATE TABLE public.product",
            "CREATE TABLE public.permission_probe(id integer)",
            "ALTER TABLE public.product ADD COLUMN permission_probe text",
            "DROP TABLE public.product",
        )
        with manager.connection(reader.password_from_url(manager.secret["database-url"].decode(),
                                                     manager.hostname), manager.ca()) as runtime:
            for statement in full_matrix:
                result = runtime(reader.safe_probe_sql(statement))
                check("reader-probe-state:42501" in result, f"mutation not denied: {statement.split()[0]}")
            try:
                reader.verify_reader(lambda sql: runtime("SET default_transaction_read_only=on;\n" + sql))
            except reader.RoleError:
                pass
            else:
                raise AssertionError("read-only transaction was accepted as a permission proof")

        # Unexpected privilege must fail the safe probe and still leave data unchanged.
        manager.admin("GRANT INSERT ON public.product TO go_product_reader;")
        with manager.connection(reader.password_from_url(manager.secret["database-url"].decode(),
                                                     manager.hostname), manager.ca()) as runtime:
            try:
                reader.verify_reader(runtime)
            except reader.RoleError:
                pass
            else:
                raise AssertionError("privileged safe probe was accepted")
        manager.admin("REVOKE INSERT ON public.product FROM go_product_reader;")
        check(reader.snapshot(manager.admin) == before, "catalog/schema changed during permission tests")

        # Secret update failure after DB password change restores the prior credential.
        old = reader.password_from_url(manager.secret["database-url"].decode(), manager.hostname)
        manager.fail_put = True
        with tempfile.TemporaryDirectory() as directory:
            os.environ["POSTGRES_ROLE_RECOVERY_FILE"] = str(Path(directory) / "recovery")
            try:
                reader.rotate(manager)
                raise AssertionError("injected Secret failure did not stop rotation")
            except reader.RoleError as error:
                check(old not in str(error), "rotation failure diagnostic exposed credential")
            reader.verify(manager)
            check(Path(os.environ["POSTGRES_ROLE_RECOVERY_FILE"]).stat().st_mode & 0o777 == 0o600,
                  "rotation recovery file is not private")
            os.environ["POSTGRES_ROLE_RECOVERY_FILE"] = str(Path(directory) / "recovery-next")
            # An established session finishes while the password changes; the old password
            # must fail on a new connection after rotation.
            passfile = Path(directory) / "old-session-pass"
            passfile.write_text(f"127.0.0.1:{port}:product:go_product_reader:{old}\n")
            passfile.chmod(0o600)
            old_session = subprocess.Popen(
                ["psql", "-X", "-q", "-A", "-t", "-d",
                 f"host=127.0.0.1 port={port} dbname=product user=go_product_reader sslmode=disable",
                 "-c", "SELECT pg_sleep(5), current_user"],
                env={**{key: value for key, value in os.environ.items() if not key.startswith("PG")},
                     "PGPASSFILE": str(passfile)}, stdout=subprocess.PIPE,
                stderr=subprocess.PIPE, text=True)
            try:
                for _ in range(30):
                    active = manager.admin("SELECT count(*) FROM pg_stat_activity WHERE usename='go_product_reader' "
                                           "AND query LIKE 'SELECT pg_sleep%';")
                    if int(active) > 0:
                        break
                    time.sleep(0.1)
                else:
                    raise AssertionError("old reader session never became active")
                reader.rotate(manager)
                output, _ = old_session.communicate(timeout=10)
                check(old_session.returncode == 0 and "go_product_reader" in output,
                      "pre-existing reader session did not finish after rotation")
            finally:
                if old_session.poll() is None:
                    old_session.terminate()
                    old_session.wait(timeout=5)
            new = reader.password_from_url(manager.secret["database-url"].decode(), manager.hostname)
            check(new != old, "explicit rotation preserved old credential")
            with manager.connection(old, manager.ca()) as runtime:
                try:
                    runtime("SELECT current_user;")
                    raise AssertionError("old credential opened a new connection")
                except reader.RoleError:
                    pass
            os.environ["POSTGRES_ROLE_RESTORE_FILE"] = str(Path(directory) / "recovery")
            os.environ["POSTGRES_ROLE_RECOVERY_FILE"] = str(Path(directory) / "recovery-of-new")
            reader.rotate(manager)
            restored = reader.password_from_url(manager.secret["database-url"].decode(), manager.hostname)
            check(restored == old, "protected rollback input did not restore old credential")
            with manager.connection(new, manager.ca()) as runtime:
                try:
                    runtime("SELECT current_user;")
                    raise AssertionError("rotated credential opened a connection after recovery")
                except reader.RoleError:
                    pass
            os.environ.pop("POSTGRES_ROLE_RESTORE_FILE")

        # Unknown effective consumer blocks PUBLIC revocation before database changes.
        manager.admin("CREATE ROLE other_consumer LOGIN PASSWORD 'disposable-other'; "
                      "GRANT CONNECT ON DATABASE product TO other_consumer;")
        try:
            reader.provision(manager)
            raise AssertionError("unknown consumer was accepted")
        except reader.RoleError:
            pass
        manager.admin("REVOKE CONNECT ON DATABASE product FROM other_consumer; DROP ROLE other_consumer;")
        check(reader.snapshot(manager.admin) == before, "catalog/schema changed during lifecycle")

        # A failed initial Secret write compensates the newly created role. A retained
        # Secret can then recover a one-sided role failure without changing its password.
        prior_secret = dict(manager.secret)
        manager.admin("REVOKE ALL ON DATABASE product FROM go_product_reader; "
                      "REVOKE ALL ON SCHEMA public FROM go_product_reader; "
                      "REVOKE ALL ON ALL TABLES IN SCHEMA public FROM go_product_reader; "
                      "DROP ROLE go_product_reader;")
        manager.secret = None
        manager.fail_put = True
        try:
            reader.provision(manager)
            raise AssertionError("initial Secret failure was accepted")
        except reader.RoleError:
            pass
        check(reader.inventory(manager.admin)["reader"] is None and manager.secret is None,
              "initial failure left an ambiguous role or Secret")
        manager.secret = prior_secret
        reader.provision(manager)
        check(manager.secret == prior_secret, "one-sided role recovery changed credential")
        manager.secret = None
        try:
            reader.provision(manager)
            raise AssertionError("role without Secret was silently recovered")
        except reader.RoleError:
            pass
        manager.secret = prior_secret
        reader.verify(manager)

        os.environ["POSTGRES_ROLE_CONFIRM_REVOKE"] = "product/reader/disposable"
        reader.revoke(manager)
        check(reader.inventory(manager.admin)["reader"] is None and manager.secret is None,
              "revocation left role or workload Secret")
        print("product-reader-test: disposable grants, denials, drift, idempotency, rotation and recovery passed")
    finally:
        subprocess.run(["docker", "rm", "-f", name], capture_output=True, check=False)


if __name__ == "__main__":
    try:
        print("product-reader-test: checking dispatch and Kubernetes adapters", flush=True)
        dispatch_guards()
        secret_adapter_test()
        deployment_ca_contract_test()
        forward_cleanup_test()
        disposable_test()
    except (AssertionError, OSError, subprocess.SubprocessError) as error:
        print(f"product-reader-test: {error}", file=sys.stderr, flush=True)
        raise SystemExit(1) from None
