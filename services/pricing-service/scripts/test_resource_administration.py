"""Focused Pricing resource administration guard and recovery tests."""

from __future__ import annotations

import json
from contextlib import contextmanager

import pytest
import resource_administration as resources


@pytest.mark.parametrize("environment, confirmation", [("production", ""), ("qa", "wrong")])
def test_ensure_refuses_unconfirmed_target_before_access(monkeypatch, environment, confirmation):
    # given
    monkeypatch.setenv("PRICING_RESOURCE_ENV", environment)
    monkeypatch.setenv("PRICING_RESOURCE_CONFIRM", confirmation)
    monkeypatch.setattr(
        resources, "AdministrationKubernetes", lambda *_: pytest.fail("target accessed")
    )

    # when / then
    with pytest.raises(resources.reader.RoleError):
        resources.main(["ensure"])


def test_wrong_coordinates_fail_before_access(monkeypatch):
    # given
    monkeypatch.setenv("PRICING_RESOURCE_CONTEXT", "wrong")
    monkeypatch.setattr(resources.reader, "command", lambda *_: pytest.fail("cluster accessed"))

    # when / then
    with pytest.raises(resources.reader.RoleError, match="PRICING_RESOURCE_CONTEXT"):
        resources.AdministrationKubernetes("qa")


class FakeTarget:
    environment = "qa"
    context = "qa-insurance-hub"
    data_ns = "qa-data"
    svc_ns = "qa-svc"
    cluster = "qa-postgres-pricing"
    cluster_uid = "11111111-1111-4111-8111-111111111111"
    hostname = "qa-postgres-pricing-rw.qa-data.svc.cluster.local"
    target_secret = "qa-postgres-pricing-admin-target"

    def __init__(self):
        self.secrets = {}
        self.mutations = []
        self.fail_sql = True

    def role_secret(self, role):
        return role

    def get_secret(self, name, namespace):
        assert namespace == self.svc_ns
        return self.secrets.get(name)

    def put_named_secret(self, name, values):
        assert name not in self.secrets
        self.secrets[name] = values
        self.mutations.append(name)

    def ca(self):
        return b"test certificate"

    def admin(self, statement):
        if statement == resources.BOUNDARY_SQL:
            return "t"
        assert set(self.secrets) == {self.target_secret, *resources.ROLES}
        self.mutations.append("postgres")
        if self.fail_sql:
            raise resources.reader.RoleError("database transaction failed")
        return ""


def test_ensure_reuses_published_secrets_after_database_failure(monkeypatch):
    # given
    target = FakeTarget()
    monkeypatch.setattr(
        resources,
        "role_inventory",
        lambda _: {
            "roles": [],
            "memberships": [],
            "target": None,
        },
    )
    verified = []
    monkeypatch.setattr(resources, "verify", verified.append)

    # when / then
    with pytest.raises(resources.reader.RoleError, match="database transaction failed"):
        resources.ensure(target)
    assert target.mutations == [target.target_secret, *resources.ROLES, "postgres"]
    before = dict(target.secrets)

    # when
    target.fail_sql = False
    resources.ensure(target)

    # then
    assert target.secrets == before
    assert target.mutations.count("postgres") == 2
    assert verified == [target]
    assert (
        json.loads(before[target.target_secret]["target.json"])["clusterUID"] == target.cluster_uid
    )


def test_command_failure_redacts_subprocess_output(monkeypatch, capsys):
    # given
    sentinel = "synthetic-sensitive-value"

    class Failure:
        returncode = 1
        stdout = sentinel
        stderr = sentinel

    monkeypatch.setattr(resources.reader.subprocess, "run", lambda *args, **options: Failure())
    monkeypatch.setenv("PRICING_RESOURCE_ENV", "qa")
    monkeypatch.setattr(
        resources, "AdministrationKubernetes", lambda _: resources.reader.command(["kubectl"])
    )

    # when
    result = resources.cli(["inspect"])

    # then
    output = capsys.readouterr()
    assert result == 1
    assert "command failed" in output.err
    assert sentinel not in output.out + output.err


def test_verify_requires_workload_sqlstate(monkeypatch):
    # given
    target = FakeTarget()
    roles = {role: {"ca.crt": target.ca()} for role in resources.ROLES}
    monkeypatch.setattr(
        resources,
        "read_or_create_inputs",
        lambda _: (dict.fromkeys(resources.ROLES, "synthetic"), roles, {"seedAllowed": True}),
    )
    monkeypatch.setattr(resources, "role_inventory", lambda _: {})
    monkeypatch.setattr(resources, "check_role_contract", lambda *_: None)
    monkeypatch.setattr(target, "admin", lambda _: "t")

    @contextmanager
    def role_connection(role, *_):
        yield lambda sql: role if sql == "SELECT current_user;" else "reader-probe-state:00000"

    monkeypatch.setattr(target, "role_connection", role_connection, raising=False)

    # when / then
    with pytest.raises(resources.reader.RoleError, match="SQLSTATE 42501"):
        resources.verify(target)
