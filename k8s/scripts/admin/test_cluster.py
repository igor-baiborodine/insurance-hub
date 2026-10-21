"""Behavior tests for Kubernetes administrative workflows."""

from __future__ import annotations

import base64
import io
import json
import os
import signal
import subprocess
import sys
import time
import zipfile
from pathlib import Path

import cluster
import pytest
import runtime
from runtime import (
    REPOSITORY_ROOT,
    TARGETS,
    AdminError,
    CommandError,
    Commands,
    Kubernetes,
    secret_input,
)


class FakeCommands:
    def __init__(self, *, outputs: dict[tuple[str, ...], str] | None = None):
        self.calls: list[tuple[list[str], str | None]] = []
        self.outputs = outputs or {}
        self.environment = {"PATH": "/usr/bin"}

    def run(self, arguments, *, input_text=None, **_kwargs):
        self.calls.append((list(arguments), input_text))
        return self.outputs.get(tuple(arguments), "")

    def succeeds(self, arguments, **_kwargs):
        self.calls.append((list(arguments), None))
        return False


class FakeKubernetes:
    def __init__(self, target=TARGETS["qa"]):
        self.target = target
        self.commands = FakeCommands()
        self.applied = []
        self.kubectl_calls = []
        self.secrets = {}
        self.namespaces = []
        self.required_namespaces = []
        self.ensured_namespaces = []
        self.verified = False

    def verify_context(self):
        self.verified = True

    def require_namespace(self, namespace):
        self.namespaces.append(namespace)
        self.required_namespaces.append(namespace)

    def ensure_namespace(self, namespace):
        self.namespaces.append(namespace)
        self.ensured_namespaces.append(namespace)

    def apply(self, payload, **_kwargs):
        self.applied.append(payload)

    def secret(self, namespace, name):
        return self.secrets[(namespace, name)]

    def kubectl(self, *arguments, input_text=None, **_kwargs):
        self.kubectl_calls.append((list(arguments), input_text))
        if arguments[-2:] == ("-o", "json") and "pods" in arguments:
            return json.dumps({"items": [{"metadata": {"name": "minio-0"}}]})
        return ""


def encoded_secret(namespace: str, name: str, values: dict[str, str]) -> dict:
    return {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {"name": name, "namespace": namespace},
        "data": {key: base64.b64encode(value.encode()).decode() for key, value in values.items()},
    }


def complete_infra_values() -> dict[str, str]:
    return {
        "PG_SVC_USER_PWD": "postgres-secret",
        "MINIO_CONSOLE_ACCESS_KEY": "console-user",
        "MINIO_CONSOLE_SECRET_KEY": "console-secret",
        "MINIO_ROOT_USER": "root-user",
        "MINIO_ROOT_USER_PWD": "root-secret",
    }


@pytest.mark.parametrize(
    ("operator", "crd", "workload_kind", "namespace", "workload"),
    [
        (
            "postgres",
            "clusters.postgresql.cnpg.io",
            "deployment",
            "cnpg-system",
            "cnpg-controller-manager",
        ),
        (
            "eck",
            "elasticsearches.elasticsearch.k8s.elastic.co",
            "statefulset",
            "elastic-system",
            "elastic-operator",
        ),
        ("minio", "tenants.minio.min.io", "deployment", "minio-operator", "minio-operator"),
    ],
)
def test_manifest_operator_waits_for_authoritative_crd_and_workload(
    operator, crd, workload_kind, namespace, workload
):
    # given
    kube = FakeKubernetes()

    # when
    cluster.deploy_operator(kube, operator)

    # then
    assert any(f"crd/{crd}" in call for call, _ in kube.kubectl_calls)
    assert any(
        call[:2] == ["-n", namespace] and f"{workload_kind}/{workload}" in call
        for call, _ in kube.kubectl_calls
    )
    assert any(crd in call and "--all-namespaces" in call for call, _ in kube.kubectl_calls)


@pytest.mark.parametrize(
    ("operator", "version", "namespace", "crd"),
    [
        ("kafka", "0.48.0", "kafka-system", "kafkas.kafka.strimzi.io"),
        (
            "mongodb",
            "0.13.0",
            "qa-data",
            "mongodbcommunity.mongodbcommunity.mongodb.com",
        ),
    ],
)
def test_helm_operator_preserves_version_namespace_and_crd(operator, version, namespace, crd):
    # given
    kube = FakeKubernetes()

    # when
    cluster.deploy_operator(kube, operator)

    # then
    helm = next(call for call, _ in kube.commands.calls if call[:2] == ["helm", "upgrade"])
    assert helm[helm.index("--version") + 1] == version
    assert helm[helm.index("--namespace") + 1] == namespace
    assert helm[helm.index("--kube-context") + 1] == "qa-insurance-hub"
    assert any(f"crd/{crd}" in call for call, _ in kube.kubectl_calls)
    if operator == "mongodb":
        assert kube.ensured_namespaces == [namespace]


def test_postgres_secret_uses_structured_input_and_copies_only_to_qa():
    # given
    kube = FakeKubernetes()
    sentinel = "SENTINEL-postgres-password"

    # when
    cluster.create_secret(
        kube,
        "postgres",
        {"SVC_NAME": "product", "PG_SVC_USER_PWD": sentinel},
    )

    # then
    assert kube.verified
    assert [item["metadata"]["namespace"] for item in kube.applied] == ["qa-data", "qa-svc"]
    assert kube.applied[0]["type"] == "kubernetes.io/basic-auth"
    assert base64.b64decode(kube.applied[0]["data"]["password"]).decode() == sentinel
    assert sentinel not in repr(kube.commands.calls)


def test_secret_value_crosses_kubectl_only_as_structured_stdin():
    # given
    sentinel = "SENTINEL-kubectl-secret"

    class ContextCommands(FakeCommands):
        def run(self, arguments, **kwargs):
            self.calls.append((list(arguments), kwargs.get("input_text")))
            if arguments == ["kubectl", "config", "current-context"]:
                return "qa-insurance-hub\n"
            return ""

    commands = ContextCommands()
    kube = Kubernetes(commands, TARGETS["qa"])

    # when
    cluster.create_secret(
        kube,
        "postgres",
        {"SVC_NAME": "product", "PG_SVC_USER_PWD": sentinel},
    )

    # then
    assert sentinel not in repr([arguments for arguments, _ in commands.calls])
    payloads = [json.loads(input_text) for _, input_text in commands.calls if input_text]
    secret_payload = next(item for item in payloads if item.get("kind") == "Secret")
    assert base64.b64decode(secret_payload["data"]["password"]).decode() == sentinel


def test_copy_secret_removes_controller_metadata():
    # given
    kube = FakeKubernetes()
    name = "qa-elasticsearch-es-elastic-user"
    source = encoded_secret("qa-data", name, {"elastic": "secret"})
    source["metadata"].update({"resourceVersion": "123", "ownerReferences": [{"name": "eck"}]})
    kube.secrets[("qa-data", name)] = source

    # when
    cluster.create_secret(kube, "elasticsearch-copy", {})

    # then
    assert kube.applied == [
        {
            "apiVersion": "v1",
            "kind": "Secret",
            "type": "Opaque",
            "metadata": {"name": name, "namespace": "qa-svc"},
            "data": source["data"],
        }
    ]


def test_minio_policy_attachment_uses_protected_iam_archive_without_credentials_in_argv(tmp_path):
    # given
    kube = FakeKubernetes()
    namespace = "qa-minio-document"
    access_sentinel = "SENTINEL-minio-access-key"
    secret_sentinel = "SENTINEL-minio-secret-key"
    kube.secrets[(namespace, f"{namespace}-storage-config")] = encoded_secret(
        namespace,
        f"{namespace}-storage-config",
        {"config.env": 'export MINIO_ROOT_USER="root"\nexport MINIO_ROOT_PASSWORD="admin-secret"'},
    )
    kube.secrets[(namespace, f"{namespace}-svc-user-creds")] = encoded_secret(
        namespace,
        f"{namespace}-svc-user-creds",
        {
            "MINIO_SVC_ACCESS_KEY": access_sentinel,
            "MINIO_SVC_SECRET_KEY": secret_sentinel,
        },
    )
    policy = tmp_path / "document-policy.json"
    policy.write_text('{"Version":"2012-10-17","Statement":[]}')

    def kubectl(*arguments, input_text=None, **_kwargs):
        kube.kubectl_calls.append((list(arguments), input_text))
        if arguments[-2:] == ("-o", "json"):
            return json.dumps({"items": [{"metadata": {"name": "minio-0"}}]})
        if "import" in arguments:
            return json.dumps(
                {
                    "added": {
                        "policies": ["document-policy"],
                        "users": [access_sentinel],
                        "userPolicies": [{access_sentinel: ["document-policy"]}],
                    }
                }
            )
        return ""

    kube.kubectl = kubectl

    # when
    cluster.configure_minio(kube, "user-policy", "document", policy_path=policy)

    # then
    all_arguments = [argument for call, _ in kube.kubectl_calls for argument in call]
    assert access_sentinel not in all_arguments
    assert secret_sentinel not in all_arguments
    archive_input = next(
        input_text for call, input_text in kube.kubectl_calls if "base64 -d" in " ".join(call)
    )
    with zipfile.ZipFile(io.BytesIO(base64.b64decode(archive_input))) as bundle:
        users = json.loads(bundle.read("iam-assets/users.json"))
        mappings = json.loads(bundle.read("iam-assets/user_mappings.json"))
    assert users == {access_sentinel: {"secretKey": secret_sentinel, "status": "enabled"}}
    assert mappings[access_sentinel]["policy"] == "document-policy"
    assert kube.applied == []


def test_zipkin_setup_uses_secret_volumes_and_pinned_image():
    # given
    kube = FakeKubernetes()
    sentinel = "SENTINEL-zipkin-password"
    kube.secrets[("qa-monitoring", "qa-zipkin-es-user-creds")] = encoded_secret(
        "qa-monitoring",
        "qa-zipkin-es-user-creds",
        {"username": "zipkin_user", "password": sentinel},
    )

    # when
    cluster.create_zipkin_user(kube, "zipkin_role", "zipkin_user")

    # then
    pod = next(item for item in kube.applied if item["kind"] == "Pod")
    container = pod["spec"]["containers"][0]
    assert container["image"] == cluster.ZIPKIN_SETUP_IMAGE
    assert ":latest" not in container["image"]
    assert {
        volume["secret"]["secretName"] for volume in pod["spec"]["volumes"] if "secret" in volume
    } == {
        "qa-elasticsearch-es-elastic-user",
        "zipkin-es-setup-request",
    }
    request = next(
        item
        for item in kube.applied
        if item["kind"] == "Secret" and item["metadata"]["name"] == "zipkin-es-setup-request"
    )
    assert sentinel in base64.b64decode(request["data"]["user.json"]).decode()
    assert sentinel not in repr(container)


@pytest.mark.parametrize(
    ("service", "expected"),
    [
        ("product", "insurance-hub-product-api-legacy:latest"),
        ("web-vue", "insurance-hub-web-vue-legacy:latest"),
    ],
)
def test_image_identity_preserves_legacy_mapping(service, expected):
    assert cluster.image_name(service) == expected


def test_wrong_service_name_is_rejected_before_mutation():
    # given
    kube = FakeKubernetes()

    # when / then
    with pytest.raises(AdminError, match="DNS-safe"):
        cluster.create_secret(
            kube,
            "postgres",
            {"SVC_NAME": "../../other", "PG_SVC_USER_PWD": "secret"},
        )
    assert kube.applied == []


def test_missing_secret_value_is_rejected_before_namespace_or_secret_mutation():
    # given
    kube = FakeKubernetes()

    # when / then
    with pytest.raises(AdminError, match="PG_SVC_USER_PWD must be non-empty"):
        cluster.create_secret(kube, "postgres", {"SVC_NAME": "product", "PG_SVC_USER_PWD": ""})
    assert not kube.verified
    assert kube.namespaces == []
    assert kube.applied == []


def test_invalid_copy_destination_is_rejected_before_source_secret_mutation():
    # given
    kube = FakeKubernetes()

    # when / then
    with pytest.raises(AdminError, match="COPY_SECRET_NS must be a DNS-safe"):
        cluster.create_secret(
            kube,
            "minio-service-user",
            {
                "SVC_NAME": "document",
                "MINIO_SVC_ACCESS_KEY": "access-key",
                "MINIO_SVC_SECRET_KEY": "secret-key",
                "COPY_SECRET_NS": "../../other",
            },
        )
    assert not kube.verified
    assert kube.namespaces == []
    assert kube.applied == []


def test_service_aggregate_validates_then_runs_the_complete_deployment(monkeypatch):
    # given
    kube = FakeKubernetes(target=TARGETS["local-dev"])
    calls = []
    make_calls = []
    monkeypatch.setattr(
        cluster,
        "_make",
        lambda _commands, target, **kwargs: make_calls.append((target, kwargs.get("variables"))),
    )
    monkeypatch.setattr(cluster, "create_secret", lambda *_args, **_kwargs: calls.append("secret"))

    # when
    cluster.deploy_aggregate(
        kube,
        "services",
        {
            "MINIO_SVC_ACCESS_KEY": "access-key",
            "MINIO_SVC_SECRET_KEY": "secret-key",
            "COPY_SECRET_NS": "local-dev-all",
        },
    )

    # then
    assert kube.verified
    assert calls.count("secret") == 2
    assert len(make_calls) == 15
    assert all(target == "svc-load-delete-deploy" for target, _ in make_calls[-11:])


def test_infra_aggregate_preserves_postgres_and_minio_phase_order(monkeypatch):
    # given
    kube = FakeKubernetes(target=TARGETS["local-dev"])
    events = []
    monkeypatch.setattr(
        cluster,
        "_make",
        lambda _commands, target, variables=None: events.append(
            ("make", target, (variables or {}).get("SVC_NAME"))
        ),
    )
    monkeypatch.setattr(
        cluster,
        "create_secret",
        lambda _kube, profile, values: events.append(("secret", profile, values.get("SVC_NAME"))),
    )

    # when
    cluster.deploy_aggregate(kube, "infra", complete_infra_values())

    # then
    postgres = [
        event[2]
        for event in events
        if event[:2] in (("secret", "postgres"), ("make", "postgres-svc-deploy"))
    ]
    assert postgres == [
        "auth",
        "auth",
        "document",
        "document",
        "payment",
        "payment",
        "policy",
        "policy",
        "pricing",
        "pricing",
        "product",
        "product",
    ]
    minio_secret_indexes = [
        index
        for index, event in enumerate(events)
        if event[0] == "secret" and event[1].startswith("minio-")
    ]
    minio_deploy_indexes = [
        index for index, event in enumerate(events) if event[:2] == ("make", "minio-tenant-deploy")
    ]
    assert len(minio_secret_indexes) == 4
    assert len(minio_deploy_indexes) == 2
    assert max(minio_secret_indexes) < min(minio_deploy_indexes)
    assert not any("mongodb" in str(event) for event in events)
    wait_calls = [call for call, _ in kube.kubectl_calls if "wait" in call]
    assert any("cluster.postgresql.cnpg.io" in call and "--all" in call for call in wait_calls)
    assert any(
        "kafka.kafka.strimzi.io" in call and "local-dev-kafka" in call for call in wait_calls
    )


def test_minio_tenant_waits_for_operator_to_create_pod(monkeypatch):
    # given
    kube = FakeKubernetes(target=TARGETS["local-dev"])
    inventories = iter(([], [], [{"metadata": {"name": "minio-pool-0-0"}}]))
    now = [0.0]

    def kubectl(*arguments, input_text=None, **_kwargs):
        kube.kubectl_calls.append((list(arguments), input_text))
        if arguments[-2:] == ("-o", "json"):
            return json.dumps({"items": next(inventories)})
        return ""

    kube.kubectl = kubectl
    monkeypatch.setattr(cluster.time, "monotonic", lambda: now[0])
    monkeypatch.setattr(cluster.time, "sleep", lambda seconds: now.__setitem__(0, now[0] + seconds))

    # when
    namespace, pod = cluster._wait_for_minio_tenant(kube, "document")

    # then
    assert namespace == "local-dev-minio-document"
    assert pod == "minio-pool-0-0"
    inventory_calls = [call for call, _ in kube.kubectl_calls if call[-2:] == ["-o", "json"]]
    assert len(inventory_calls) == 3
    assert kube.kubectl_calls[-1][0][2:4] == ["wait", "--for=condition=Ready"]


def test_minio_tenant_discovery_has_a_bounded_failure(monkeypatch):
    # given
    kube = FakeKubernetes(target=TARGETS["local-dev"])
    now = [0.0]
    kube.kubectl = lambda *_args, **_kwargs: json.dumps({"items": []})
    monkeypatch.setattr(cluster.time, "monotonic", lambda: now[0])
    monkeypatch.setattr(cluster.time, "sleep", lambda seconds: now.__setitem__(0, now[0] + seconds))

    # when / then
    with pytest.raises(AdminError, match="no MinIO tenant pod appeared.*within 5s"):
        cluster._wait_for_minio_tenant(
            kube,
            "document",
            discovery_timeout=5,
            poll_interval=2,
        )


def test_infra_aggregate_rejects_missing_credentials_before_operator_or_namespace_mutation(
    monkeypatch,
):
    # given
    kube = FakeKubernetes(target=TARGETS["local-dev"])
    calls = []
    monkeypatch.setattr(cluster, "_make", lambda *_args, **_kwargs: calls.append("make"))

    # when / then
    with pytest.raises(AdminError, match="PG_SVC_USER_PWD must be non-empty"):
        cluster.deploy_aggregate(kube, "infra", {})
    assert not kube.verified
    assert calls == []
    assert kube.namespaces == []
    assert kube.applied == []


def test_local_cluster_failure_reports_retained_partial_state(tmp_path):
    # given
    config = tmp_path / "kind.yaml"
    config.write_text("kind: Cluster")

    class FailingCommands(FakeCommands):
        def run(self, arguments, **kwargs):
            if arguments[:3] == ["curl", "--fail", "--silent"]:
                raise AdminError("download failed")
            return super().run(arguments, **kwargs)

    # when / then
    with pytest.raises(AdminError, match="retained cluster"):
        cluster.create_local_cluster(
            FailingCommands(), TARGETS["local-dev"], tmp_path / "local-dev-storage", config
        )


def test_qa_image_failure_reports_completed_nodes_and_cleans_remote(monkeypatch):
    # given
    kube = FakeKubernetes()

    class PartialCommands(FakeCommands):
        def run(self, arguments, **kwargs):
            self.calls.append((list(arguments), kwargs.get("input_text")))
            if arguments[:3] == ["lxc", "list", "--format"]:
                return json.dumps([{"name": "qa-master"}, {"name": "qa-worker1"}])
            if "qa-worker1" in arguments and "import" in arguments:
                raise CommandError("import failed")
            return ""

    kube.commands = PartialCommands()
    monkeypatch.setattr(cluster.os, "getpid", lambda: 42)

    # when / then
    with pytest.raises(AdminError, match="completed nodes: qa-master"):
        cluster.manage_image(kube, "qa-load", "product")
    cleanup_nodes = [
        call[2]
        for call, _ in kube.commands.calls
        if call[:2] == ["lxc", "exec"]
        and call[-3:] == ["rm", "-f", "/tmp/insurance-hub-product-42.tar"]
    ]
    assert cleanup_nodes == ["qa-master", "qa-worker1"]


def test_guard_does_not_change_context_or_create_namespace():
    # given
    kube = FakeKubernetes()

    # when
    cluster.guard(kube, "data")

    # then
    assert kube.namespaces == []
    assert kube.applied == []


def test_service_name_guard_never_contacts_kubernetes():
    # given
    kube = FakeKubernetes()

    # when
    cluster.guard(kube, "service-name", "product")

    # then
    assert not kube.verified
    assert kube.kubectl_calls == []


@pytest.mark.parametrize(
    ("override", "expected_error"),
    [
        ("CUR_CTX=other-context", "context must explicitly equal qa-insurance-hub"),
        ("DATA_NS=other-data", "data namespace must explicitly equal qa-data"),
    ],
)
@pytest.mark.parametrize(
    "make_prefix",
    [
        pytest.param(["make"], id="root"),
        pytest.param(["make", "-C", str(REPOSITORY_ROOT / "k8s")], id="k8s"),
    ],
)
def test_retained_make_deploy_rejects_coordinate_override_before_kubectl(
    tmp_path, override, expected_error, make_prefix
):
    # given
    recorder = tmp_path / "kubectl"
    calls = tmp_path / "kubectl-calls"
    recorder.write_text(f'#!/bin/sh\nprintf "%s\\n" "$*" >> "{calls}"\n')
    recorder.chmod(0o755)
    environment = os.environ.copy()
    environment["PATH"] = f"{tmp_path}:{environment['PATH']}"
    make_arguments = [
        *make_prefix,
        "postgres-svc-deploy",
        "ENV_NAME=qa",
        "CUR_CTX=qa-insurance-hub",
        "DATA_NS=qa-data",
        "SVC_NS=qa-svc",
        "MONITORING_NS=qa-monitoring",
        "FLUX_SYSTEM_NS=flux-system",
        "SVC_NAME=product",
        f"PYTHON={sys.executable}",
        override,
    ]

    # when
    result = subprocess.run(
        make_arguments,
        cwd=REPOSITORY_ROOT,
        env=environment,
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )

    # then
    assert result.returncode != 0
    assert expected_error in result.stderr
    assert calls.read_text().splitlines() == ["config current-context"]


def test_retained_make_secret_rejects_invalid_file_before_namespace_mutation(tmp_path):
    # given
    secret = tmp_path / "postgres-password"
    secret.write_text("SENTINEL-invalid-permissions")
    secret.chmod(0o640)
    calls = tmp_path / "kubectl-calls"
    recorder = tmp_path / "kubectl"
    recorder.write_text(
        "#!/bin/sh\n"
        f'printf "%s\\n" "$*" >> "{calls}"\n'
        'if [ "$*" = "config current-context" ]; then printf "qa-insurance-hub\\n"; fi\n'
    )
    recorder.chmod(0o755)
    environment = os.environ.copy()
    environment["PATH"] = f"{tmp_path}:{environment['PATH']}"

    # when
    result = subprocess.run(
        [
            "make",
            "-C",
            str(REPOSITORY_ROOT / "k8s"),
            "postgres-svc-secret-create",
            "ENV_NAME=qa",
            "CUR_CTX=qa-insurance-hub",
            "DATA_NS=qa-data",
            "SVC_NS=qa-svc",
            "MONITORING_NS=qa-monitoring",
            "FLUX_SYSTEM_NS=flux-system",
            "SVC_NAME=product",
            f"PG_SVC_USER_PWD_FILE={secret}",
            f"PYTHON={sys.executable}",
        ],
        cwd=REPOSITORY_ROOT,
        env=environment,
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )

    # then
    assert result.returncode != 0
    assert "must not be accessible by group or others" in result.stderr
    assert set(calls.read_text().splitlines()) == {"config current-context"}


def test_secret_file_requires_exclusive_protected_input(tmp_path):
    # given
    secret = tmp_path / "database-password"
    secret.write_text("SENTINEL-protected\n")
    secret.chmod(0o600)

    # when
    value = secret_input("PG_SVC_USER_PWD", {"PG_SVC_USER_PWD_FILE": str(secret)})

    # then
    assert value == "SENTINEL-protected"
    with pytest.raises(AdminError, match="cannot both be set") as conflict:
        secret_input(
            "PG_SVC_USER_PWD",
            {
                "PG_SVC_USER_PWD": "SENTINEL-raw",
                "PG_SVC_USER_PWD_FILE": str(secret),
            },
        )
    assert "SENTINEL" not in str(conflict.value)


def test_secret_file_rejects_group_access_without_reading_value(tmp_path):
    # given
    secret = tmp_path / "database-password"
    secret.write_text("SENTINEL-must-not-leak")
    secret.chmod(0o640)

    # when / then
    with pytest.raises(AdminError, match="group or others") as failure:
        secret_input("PG_SVC_USER_PWD", {"PG_SVC_USER_PWD_FILE": str(secret)})
    assert "SENTINEL" not in str(failure.value)


def test_secret_file_rejects_symbolic_links(tmp_path):
    # given
    secret = tmp_path / "database-password"
    secret.write_text("SENTINEL-must-not-leak")
    secret.chmod(0o600)
    link = tmp_path / "database-password-link"
    link.symlink_to(secret)

    # when / then
    with pytest.raises(AdminError, match="cannot be inspected") as failure:
        secret_input("PG_SVC_USER_PWD", {"PG_SVC_USER_PWD_FILE": str(link)})
    assert "SENTINEL" not in str(failure.value)


def test_child_environment_excludes_unrelated_credentials():
    # given
    source = {
        "PATH": "/usr/bin",
        "HOME": "/tmp/home",
        "UNRELATED_API_TOKEN": "SENTINEL-token",
        "PG_SVC_USER_PWD": "SENTINEL-database",
    }

    # when
    commands = Commands(environment=source)

    # then
    assert commands.environment == {"PATH": "/usr/bin", "HOME": "/tmp/home"}


def test_recursive_make_receives_the_verified_interpreter(monkeypatch):
    # given
    commands = FakeCommands()
    monkeypatch.setenv("MAKE", "make --no-print-directory")

    # when
    cluster._make(commands, "svc-deploy", variables={"SVC_NAME": "product"})

    # then
    arguments, _ = commands.calls[0]
    assert arguments[:2] == ["make", "--no-print-directory"]
    assert "SVC_NAME=product" in arguments
    assert f"PYTHON={sys.executable}" in arguments


def test_recursive_make_inherits_and_caps_to_parent_workflow_deadline(monkeypatch):
    # given
    now = [100.0]
    calls = []
    monkeypatch.setattr(runtime.time, "monotonic", lambda: now[0])
    monkeypatch.setenv("MAKE", "make")

    def complete(arguments, **kwargs):
        calls.append((list(arguments), kwargs))
        return subprocess.CompletedProcess(arguments, 0, "", "")

    monkeypatch.setattr(runtime, "run_command", complete)
    commands = Commands(
        environment={"PATH": "/usr/bin"},
        deadline=runtime.WorkflowDeadline("deploy infra", 150.0),
    )

    # when
    cluster._make(commands, "postgres-operator-deploy")

    # then
    assert calls[0][1]["timeout_seconds"] == 50
    child_environment = calls[0][1]["env"]
    child = runtime.WorkflowDeadline.start("operator postgres", 1200, child_environment)
    assert child == runtime.WorkflowDeadline("deploy infra", 150.0)


def test_port_forward_failure_terminates_every_owned_child(monkeypatch):
    # given
    kube = FakeKubernetes(target=TARGETS["local-dev"])
    processes = []
    killed = []

    class Process:
        def __init__(self, *_args, **_kwargs):
            self.pid = 1000 + len(processes)
            self.failed = not processes
            self.stopped = False
            processes.append(self)

        def poll(self):
            if self.failed:
                return 1
            return 0 if self.stopped else None

    def killpg(pid, signal):
        killed.append((pid, signal))
        processes[pid - 1000].stopped = True

    monkeypatch.setattr(cluster.subprocess, "Popen", Process)
    monkeypatch.setattr(cluster.os, "killpg", killpg)

    # when / then
    with pytest.raises(AdminError, match="before readiness"):
        cluster.port_forward(kube)
    assert len(processes) == len(cluster.PORT_FORWARDS)
    assert {pid for pid, _ in killed} == {process.pid for process in processes}


def test_port_forward_interruption_terminates_every_owned_child(monkeypatch):
    # given
    kube = FakeKubernetes(target=TARGETS["local-dev"])
    processes = []
    killed = []

    class Process:
        def __init__(self, *_args, **_kwargs):
            self.pid = 2000 + len(processes)
            self.stopped = False
            processes.append(self)

        def poll(self):
            return 0 if self.stopped else None

    def killpg(pid, signal):
        killed.append((pid, signal))
        processes[pid - 2000].stopped = True

    monkeypatch.setattr(cluster.subprocess, "Popen", Process)
    monkeypatch.setattr(cluster.os, "killpg", killpg)
    monkeypatch.setattr(
        cluster,
        "_port_ready",
        lambda _port, _timeout=0.1: (_ for _ in ()).throw(KeyboardInterrupt()),
    )

    # when / then
    with pytest.raises(KeyboardInterrupt):
        cluster.port_forward(kube)
    assert len(processes) == len(cluster.PORT_FORWARDS)
    assert {pid for pid, _ in killed} == {process.pid for process in processes}


@pytest.mark.parametrize(
    ("requested_signal", "expected_status"),
    [(signal.SIGINT, 130), (signal.SIGTERM, 143)],
)
def test_cli_signal_terminates_real_owned_port_forward_groups(
    tmp_path, requested_signal, expected_status
):
    # given
    pid_log = tmp_path / "port-forward-pids"
    kubectl = tmp_path / "kubectl"
    kubectl.write_text(
        f"""#!{sys.executable}
import os
import signal
import subprocess
import sys
import time

if sys.argv[1:] == ["config", "current-context"]:
    print("kind-local-dev-insurance-hub")
    raise SystemExit(0)
if "port-forward" not in sys.argv[1:]:
    raise SystemExit(2)
child = subprocess.Popen(
    [
        sys.executable,
        "-c",
        "import signal, time; "
        "signal.signal(signal.SIGINT, signal.SIG_IGN); "
        "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
        "time.sleep(300)",
    ]
)
with open({str(pid_log)!r}, "a", encoding="utf-8") as output:
    output.write(f"{{os.getpid()}} {{child.pid}}\\n")
    output.flush()
while True:
    time.sleep(1)
"""
    )
    kubectl.chmod(0o755)
    environment = {
        **os.environ,
        "PATH": f"{tmp_path}:{os.environ['PATH']}",
        "ENV_NAME": "local-dev",
        "CUR_CTX": "kind-local-dev-insurance-hub",
        "DATA_NS": "local-dev-all",
        "SVC_NS": "local-dev-all",
        "MONITORING_NS": "INVALID",
        "FLUX_SYSTEM_NS": "INVALID",
    }
    dispatcher = subprocess.Popen(
        [
            sys.executable,
            str(REPOSITORY_ROOT / "k8s/scripts/admin/dispatch.py"),
            "local",
            "port-forward",
        ],
        env=environment,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    owned_pids: list[int] = []

    def process_is_running(pid: int) -> bool:
        try:
            state = (Path(f"/proc/{pid}/stat")).read_text().split()[2]
        except FileNotFoundError, ProcessLookupError:
            return False
        return state != "Z"

    try:
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if pid_log.exists():
                records = pid_log.read_text().splitlines()
                if len(records) == len(cluster.PORT_FORWARDS):
                    owned_pids = [int(pid) for record in records for pid in record.split()]
                    break
            if dispatcher.poll() is not None:
                stdout, stderr = dispatcher.communicate()
                pytest.fail(f"dispatcher exited before signal: {stdout!r} {stderr!r}")
            time.sleep(0.05)
        assert len(owned_pids) == 2 * len(cluster.PORT_FORWARDS)

        # when
        dispatcher.send_signal(requested_signal)
        stdout, stderr = dispatcher.communicate(timeout=15)

        # then
        assert dispatcher.returncode == expected_status
        assert stdout == ""
        assert signal.Signals(requested_signal).name in stderr
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline and any(process_is_running(pid) for pid in owned_pids):
            time.sleep(0.05)
        assert not [pid for pid in owned_pids if process_is_running(pid)]
    finally:
        if dispatcher.poll() is None:
            dispatcher.kill()
            dispatcher.communicate()
        for record in pid_log.read_text().splitlines() if pid_log.exists() else []:
            process_group = int(record.split()[0])
            try:
                os.killpg(process_group, signal.SIGKILL)
            except ProcessLookupError:
                pass
