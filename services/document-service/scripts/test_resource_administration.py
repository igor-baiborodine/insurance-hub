"""Focused Document resource guards; no target contact at import or collection."""

from __future__ import annotations

import importlib
import json
import sys
from pathlib import Path

import pytest

# Normal script path import: each owning target selects this service's script directory.
sys.path.insert(0, str(Path(__file__).resolve().parent))
import resource_administration as resources  # noqa: E402


def test_confirmation_rejected_before_target_contact(monkeypatch, capsys):
    # given
    monkeypatch.setenv("DOCUMENT_RESOURCE_ENV", "qa")
    monkeypatch.delenv("DOCUMENT_RESOURCE_CONFIRM", raising=False)

    def forbidden(*_args):
        raise AssertionError("target contacted without confirmation")

    monkeypatch.setattr(resources, "Kubernetes", forbidden)
    # when
    code = resources.main(["ensure"])
    # then
    assert code == 1
    assert "failed" in capsys.readouterr().err


def test_role_sql_preserves_owner_and_existing_credentials():
    # given
    target = {
        "environment": "disposable",
        "instanceID": "00000000-0000-4000-8000-000000000362",
        "clusterUID": "test",
    }
    # when
    sql = resources.role_sql(
        {role: "a" * 64 for role in resources.ROLES}, set(resources.ROLES), target
    )
    # then
    assert "CREATE ROLE" not in sql and "PASSWORD" not in sql
    assert "GRANT document TO go_document_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE" in sql
    assert "nextval" not in sql and "setval" not in sql
    assert "go_document_owner" not in sql


def test_protected_file_rejects_public_secret(tmp_path):
    # given
    path = tmp_path / "config.json"
    path.write_text("synthetic-secret")
    path.chmod(0o644)
    # when / then
    with pytest.raises(resources.ResourceError):
        resources.protected_file(path)


def test_local_migration_url_rebases_only_canonical_ca_mount(tmp_path):
    # given
    source = tmp_path / "database-url"
    ca = tmp_path / "ca.crt"
    source.write_text(
        "postgresql://go_document_migrator:synthetic@document/document"
        "?sslmode=verify-full&sslrootcert=/etc/document-admin/migrator/ca.crt"
    )
    ca.write_text("synthetic-ca")
    source.chmod(0o600)
    ca.chmod(0o600)
    # when
    with resources.local_migration_url(source, ca, 15432) as rebased:
        value = rebased.read_text()
        mode = rebased.stat().st_mode & 0o777
        temporary = rebased
    # then
    assert value.startswith("postgresql://go_document_migrator:synthetic@document/document?")
    assert "sslmode=verify-full" in value
    assert f"sslrootcert={str(ca).replace('/', '%2F')}" in value
    assert "hostaddr=127.0.0.1" in value
    assert "port=15432" in value
    assert mode == 0o600
    assert not temporary.exists()


def test_local_migration_url_rejects_noncanonical_secret_ca_mount(tmp_path):
    # given
    source = tmp_path / "database-url"
    ca = tmp_path / "ca.crt"
    source.write_text(
        "postgresql://go_document_migrator:synthetic@document/document"
        "?sslmode=verify-full&sslrootcert=/unexpected/ca.crt"
    )
    source.chmod(0o600)
    ca.write_text("synthetic-ca")
    ca.chmod(0o600)
    # when / then
    with pytest.raises(resources.ResourceError, match="canonical Secret mount"):
        with resources.local_migration_url(source, ca, 15432):
            raise AssertionError("noncanonical credential was accepted")


def test_minio_foreign_policy_rejected_before_writes(tmp_path):
    # given
    tmp_path.chmod(0o700)
    path = tmp_path / "config.json"
    path.write_text(json.dumps({"aliases": {"document": {"url": "http://localhost:9000"}}}))
    path.chmod(0o600)

    class Fake:
        def run(self, args, text=None, **_kwargs):
            if "stat" in args:
                return "{}"
            if "user" in args:
                return ""
            return json.dumps({"policy": {"Version": "2012-10-17", "Statement": []}})

    minio = resources.Minio(Fake(), path, "http://localhost:9000")
    # when / then
    with pytest.raises(resources.ResourceError, match="policy"):
        minio.inspect()


def test_import_has_no_command_effects(monkeypatch):
    # given
    def forbidden(*_args, **_kwargs):
        raise AssertionError("import contacted a target")

    monkeypatch.setattr(resources.subprocess, "Popen", forbidden)
    # when
    module = importlib.reload(resources)
    # then
    assert module.ROLES == (
        "go_document_reader",
        "go_document_migrator",
        "go_document_writer",
    )


def test_writer_definitions_are_exact_and_template_drift_blocks():
    # given
    expected_actions = {
        "s3:PutObject",
        "s3:AbortMultipartUpload",
        "s3:ListMultipartUploadParts",
    }
    jsreport = resources.JSReport("http://127.0.0.1:5488")
    jsreport.request = lambda *_args: {
        "value": [
            {
                "name": "POLICY",
                "content": resources.POLICY_TEMPLATE.decode() + "drift",
                "engine": "handlebars",
                "recipe": "chrome-pdf",
            }
        ]
    }
    # when / then
    statement = resources.WRITER_POLICY["Statement"]
    assert len(statement) == 1
    assert set(statement[0]["Action"]) == expected_actions
    assert statement[0]["Resource"] == ["arn:aws:s3:::policies/*"]
    assert len(resources.POLICY_TEMPLATE) == 183
    assert resources.hashlib.sha256(resources.POLICY_TEMPLATE).hexdigest() == (
        resources.POLICY_TEMPLATE_SHA256
    )
    with pytest.raises(resources.ResourceError, match="differs"):
        jsreport.inspect()


def test_existing_policy_template_is_preserved_without_write():
    # given
    jsreport = resources.JSReport("http://127.0.0.1:5488")
    jsreport.request = lambda *_args: (_ for _ in ()).throw(
        AssertionError("accepted existing template must not be rewritten")
    )
    # when / then
    jsreport.ensure({"template": True})


def test_secret_replay_never_overwrites_credentials():
    # given
    manager = object.__new__(resources.Kubernetes)
    manager.environment = "qa"
    manager.svc_ns = "qa-svc"
    manager.target_secret = "qa-postgres-document-admin-target"
    manager.kafka_ca_secret = "qa-document-kafka-ca"
    manager.secret = lambda _name: {"password": b"original"}

    def forbidden(*_args, **_kwargs):
        raise AssertionError("existing Secret must not be rewritten")

    manager.kube = forbidden
    # when
    manager.put_secret(manager.secret_name("reader"), {"password": b"original"})
    # then
    with pytest.raises(resources.ResourceError, match="no credential rotation"):
        manager.put_secret(manager.secret_name("reader"), {"password": b"replacement"})


def test_kafka_ca_uses_exact_strimzi_source_identity():
    # given
    ca = b"-----BEGIN CERTIFICATE-----\nsynthetic\n-----END CERTIFICATE-----\n"
    manager = object.__new__(resources.Kubernetes)
    manager.data_ns = "qa-data"
    manager.kafka_cluster = "qa-kafka"
    manager.kafka_source_secret = "qa-kafka-cluster-ca-cert"
    record = {
        "metadata": {
            "name": manager.kafka_source_secret,
            "namespace": manager.data_ns,
            "labels": {"strimzi.io/cluster": manager.kafka_cluster},
        },
        "data": {"ca.crt": resources.base64.b64encode(ca).decode()},
    }
    manager.kube = lambda *_args: json.dumps(record)

    # when
    observed = manager.kafka_ca()

    # then
    assert observed == ca
    record["metadata"]["labels"]["strimzi.io/cluster"] = "foreign-kafka"
    with pytest.raises(resources.ResourceError, match="source identity"):
        manager.kafka_ca()


def test_kafka_trust_rejects_drift_and_creates_only_owned_secret():
    # given
    ca = b"-----BEGIN CERTIFICATE-----\nsynthetic\n-----END CERTIFICATE-----\n"
    manager = object.__new__(resources.Kubernetes)
    manager.environment = "qa"
    manager.svc_ns = "qa-svc"
    manager.target_secret = "qa-postgres-document-admin-target"
    manager.kafka_ca_secret = "qa-document-kafka-ca"
    manager.kafka_ca = lambda: ca
    manager.secret = lambda _name: None
    created = []
    manager.kube = lambda *args, text=None: created.append((args, text)) or ""

    # when
    observed, present = resources.kafka_trust(manager)
    manager.put_secret(manager.kafka_ca_secret, {"ca.crt": observed})

    # then
    assert not present
    payload = json.loads(created[0][1])
    assert payload["metadata"] == {
        "name": "qa-document-kafka-ca",
        "namespace": "qa-svc",
        "labels": {"insurance-hub.io/owner": "document"},
    }
    assert resources.base64.b64decode(payload["data"]["ca.crt"]) == ca
    manager.secret = lambda _name: {"ca.crt": b"different-ca"}
    with pytest.raises(resources.ResourceError, match="trust drift"):
        resources.kafka_trust(manager)


def test_wrong_explicit_target_rejected_before_contact(monkeypatch):
    # given
    monkeypatch.setenv("DOCUMENT_RESOURCE_CONTEXT", "kind-local-dev-insurance-hub")

    class NoContact:
        def run(self, *_args, **_kwargs):
            raise AssertionError("wrong target contacted")

    # when / then
    with pytest.raises(resources.ResourceError, match="target mismatch"):
        resources.Kubernetes("qa", NoContact())


def test_failed_child_cannot_leak_input(monkeypatch, capsys):
    # given
    sentinel = "synthetic-secret-sentinel-362"

    def failure(*_args, **_kwargs):
        raise resources.CommandError("failed", stdout=sentinel, stderr=sentinel)

    monkeypatch.setattr(resources, "run_command", failure)
    # when / then
    with pytest.raises(resources.ResourceError) as observed:
        resources.Commands().run(["synthetic-command"], text=sentinel)
    assert sentinel not in str(observed.value)
    assert sentinel not in capsys.readouterr().out


def test_unavailable_dependency_is_not_missing_resource(monkeypatch):
    # given
    def failure(*_args, **_kwargs):
        raise resources.CommandError(
            "unavailable", stdout='{"error":{"cause":{"message":"connection refused"}}}'
        )

    monkeypatch.setattr(resources, "run_command", failure)
    # when / then
    with pytest.raises(resources.ResourceError):
        resources.Commands().run(["synthetic-mc"], missing=("NoSuchBucket",))
