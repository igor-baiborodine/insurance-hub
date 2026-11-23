"""Focused Policy resource administration guard and recovery tests with fake adapters."""

from __future__ import annotations

import base64
import json
from contextlib import contextmanager

import pytest
import resource_administration as resources
from scripts.python.process_runner import CommandError

CLUSTER_UID = "11111111-1111-4111-8111-111111111111"
CA = b"-----BEGIN CERTIFICATE-----\nsynthetic\n"


class FakeTarget:
    """A CNPG target whose Secrets and administrator SQL are recorded in memory."""

    environment = "local-dev"
    context = "kind-local-dev-insurance-hub"
    data_ns = "local-dev-all"
    svc_ns = "local-dev-all"
    cluster = "local-dev-postgres-policy"
    cluster_uid = CLUSTER_UID
    hostname = "local-dev-postgres-policy-rw.local-dev-all.svc.cluster.local"
    target_secret = "local-dev-postgres-policy-admin-target"

    def __init__(self, roles: str = ""):
        self.secrets: dict[str, dict[str, bytes]] = {}
        self.mutations: list[str] = []
        self.role_statements: list[str] = []
        self.roles = roles
        self.fail_roles = False

    def role_secret(self, role):
        return f"local-dev-postgres-policy-{resources.SECRET_SUFFIX[role]}"

    def get_secret(self, name, namespace):
        assert namespace == self.svc_ns
        return self.secrets.get(name)

    def put_secret(self, name, _component, values):
        assert name not in self.secrets
        self.secrets[name] = values
        self.mutations.append(name)

    def ca(self):
        return CA

    def admin(self, sql, *, sensitive=()):
        if sql == resources.BOUNDARY_SQL:
            return "t"
        if sql == resources.ROLES_SQL:
            return self.roles
        if sql == resources.MARKER_PRESENT_SQL:
            return "f"
        assert "CREATE ROLE" in sql or "ALTER ROLE" in sql
        assert set(sensitive) == {
            resources.password_from_url(
                role, self.secrets[self.role_secret(role)]["database-url"].decode(), self.hostname
            )
            for role in resources.ROLES
        }
        self.mutations.append("postgres")
        self.role_statements.append(sql)
        if self.fail_roles:
            raise resources.ResourceError("psql failed; inspect the target safely")
        return ""


@pytest.mark.parametrize("environment, confirmation", [("production", ""), ("qa", "wrong")])
def test_ensure_refuses_unconfirmed_target_before_access(monkeypatch, environment, confirmation):
    # given
    monkeypatch.setenv("POLICY_RESOURCE_ENV", environment)
    monkeypatch.setenv("POLICY_RESOURCE_CONFIRM", confirmation)
    monkeypatch.setattr(resources, "Kubernetes", lambda *_: pytest.fail("target accessed"))

    # when / then
    with pytest.raises(resources.ResourceError):
        resources.main(["ensure"])


def test_wrong_coordinates_fail_before_cluster_access(monkeypatch):
    # given
    for key, value in {
        "POLICY_RESOURCE_CONTEXT": "kind-local-dev-insurance-hub",
        "POLICY_RESOURCE_DATA_NS": "local-dev-all",
        "POLICY_RESOURCE_SVC_NS": "local-dev-all",
        "POLICY_RESOURCE_CLUSTER": "local-dev-postgres-pricing",
        "POLICY_RESOURCE_DB_HOST": FakeTarget.hostname,
    }.items():
        monkeypatch.setenv(key, value)
    monkeypatch.setattr(resources, "command", lambda *_, **__: pytest.fail("cluster accessed"))

    # when / then
    with pytest.raises(resources.ResourceError, match="POLICY_RESOURCE_CLUSTER"):
        resources.Kubernetes("local-dev")


def test_interrupted_ensure_retries_with_published_credentials(monkeypatch):
    # given
    target = FakeTarget()
    target.fail_roles = True
    verified = []
    monkeypatch.setattr(resources, "verify", verified.append)

    # when / then
    with pytest.raises(resources.ResourceError, match="psql failed"):
        resources.ensure(target)
    published = dict(target.secrets)
    assert target.mutations == [
        target.target_secret,
        *(target.role_secret(role) for role in resources.ROLES),
        "postgres",
    ]

    # when
    target.fail_roles = False
    resources.ensure(target)

    # then
    assert target.secrets == published
    assert target.role_statements[0] == target.role_statements[1]
    assert verified == [target]
    stored = json.loads(published[target.target_secret]["target.json"])
    assert stored["clusterUID"] == CLUSTER_UID
    assert f"SET LOCAL policy.instance_id = '{stored['instanceID']}'" in target.role_statements[1]


def test_existing_role_without_recoverable_secret_is_refused():
    # given
    target = FakeTarget(roles="go_policy_runtime")

    # when / then
    with pytest.raises(resources.ResourceError, match="go_policy_runtime exists without"):
        resources.ensure(target)
    assert target.mutations == []


def test_existing_roles_keep_their_passwords():
    # given
    passwords = {role: "synthetic-sensitive-password" for role in resources.ROLES}
    target = {"environment": "local-dev", "instanceID": "x", "clusterUID": CLUSTER_UID}

    # when
    sql = resources.role_sql(passwords, set(resources.ROLES), target)

    # then
    assert "synthetic-sensitive-password" not in sql
    assert sql.count("ALTER ROLE") == 2 and "CREATE ROLE" not in sql
    assert "GRANT policy TO go_policy_migrator WITH INHERIT FALSE, SET TRUE" in sql
    assert "TO go_policy_runtime" in sql and sql.rstrip().endswith("COMMIT;")


def test_secret_owned_by_another_service_is_refused(monkeypatch):
    # given
    manager = object.__new__(resources.Kubernetes)
    manager.svc_ns = "local-dev-all"
    record = {
        "metadata": {
            "name": "local-dev-postgres-policy-runtime-creds",
            "namespace": "local-dev-all",
            "labels": {
                "insurance-hub.io/owner": "pricing",
                "app.kubernetes.io/name": "pricing-api",
            },
        },
        "data": {"ca.crt": base64.b64encode(CA).decode()},
    }
    monkeypatch.setattr(manager, "kubectl", lambda *_, **__: json.dumps(record), raising=False)

    # when / then
    with pytest.raises(resources.ResourceError, match="not owned by Policy"):
        manager.get_secret("local-dev-postgres-policy-runtime-creds", "local-dev-all")


def test_command_failure_redacts_subprocess_output(monkeypatch, capsys):
    # given
    sentinel = "synthetic-sensitive-value"

    def failure(*_args, **_options):
        raise CommandError("kubectl failed", stdout=sentinel, stderr=sentinel)

    monkeypatch.setattr(resources, "run_command", failure)
    monkeypatch.setenv("POLICY_RESOURCE_ENV", "local-dev")
    monkeypatch.setattr(resources, "Kubernetes", lambda _: resources.command(["kubectl"]))

    # when
    result = resources.cli(["inspect"])

    # then
    output = capsys.readouterr()
    assert result == 1
    assert "kubectl failed; inspect the target safely" in output.err
    assert sentinel not in output.out + output.err


def test_verify_requires_permission_denied_sqlstate(monkeypatch):
    # given
    target = FakeTarget()
    stored = {role: {"ca.crt": CA} for role in resources.ROLES}
    document = {"environment": "local-dev", "instanceID": "x", "clusterUID": CLUSTER_UID}
    monkeypatch.setattr(
        resources,
        "read_inputs",
        lambda _: (dict.fromkeys(resources.ROLES, "synthetic"), stored, document, True),
    )
    monkeypatch.setattr(resources, "read_marker", lambda _: resources.marker_document(document))
    monkeypatch.setattr(
        target, "admin", lambda sql, **_: "f" if sql == resources.MIGRATED_SQL else "t"
    )

    @contextmanager
    def role_connection(role, *_):
        def connection(sql):
            if sql.startswith("SET ROLE"):
                return "policy", ""
            if "current_user" in sql:
                return role, ""
            # The probe reports success instead of the required privilege denial.
            return "", "policy-probe-state:00000"

        yield connection

    monkeypatch.setattr(target, "role_connection", role_connection, raising=False)

    # when / then
    with pytest.raises(resources.ResourceError, match="SQLSTATE 42501"):
        resources.verify(target)
