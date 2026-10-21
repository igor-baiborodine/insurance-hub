"""Behavior tests for Kubernetes administrative workflows."""

from __future__ import annotations

import base64
import json
import sys

import cluster
import pytest
from runtime import TARGETS, AdminError, CommandError, Commands, Kubernetes, secret_input


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
        self.verified = False

    def verify_context(self):
        self.verified = True

    def require_namespace(self, namespace):
        self.namespaces.append(namespace)

    def ensure_namespace(self, namespace):
        self.namespaces.append(namespace)

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


@pytest.mark.parametrize(
    ("operator", "crd", "namespace", "deployment"),
    [
        ("postgres", "clusters.postgresql.cnpg.io", "cnpg-system", "cnpg-controller-manager"),
        (
            "eck",
            "elasticsearches.elasticsearch.k8s.elastic.co",
            "elastic-system",
            "elastic-operator",
        ),
        ("minio", "tenants.minio.min.io", "minio-operator", "minio-operator"),
    ],
)
def test_manifest_operator_waits_for_exact_crd_and_deployment(operator, crd, namespace, deployment):
    # given
    kube = FakeKubernetes()

    # when
    cluster.deploy_operator(kube, operator)

    # then
    assert any(f"crd/{crd}" in call for call, _ in kube.kubectl_calls)
    assert any(
        call[:2] == ["-n", namespace] and f"deployment/{deployment}" in call
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


def test_minio_user_credentials_use_stdin_and_remote_config_is_removed(tmp_path, monkeypatch):
    # given
    kube = FakeKubernetes()
    namespace = "qa-minio-document"
    sentinel = "SENTINEL-minio-service-secret"
    kube.secrets[(namespace, f"{namespace}-storage-config")] = encoded_secret(
        namespace,
        f"{namespace}-storage-config",
        {"config.env": 'export MINIO_ROOT_USER="root"\nexport MINIO_ROOT_PASSWORD="admin-secret"'},
    )
    kube.secrets[(namespace, f"{namespace}-svc-user-creds")] = encoded_secret(
        namespace,
        f"{namespace}-svc-user-creds",
        {"MINIO_SVC_ACCESS_KEY": "document-user", "MINIO_SVC_SECRET_KEY": sentinel},
    )
    policy = tmp_path / "document-policy.json"
    policy.write_text('{"Version":"2012-10-17","Statement":[]}')
    monkeypatch.setattr(cluster.os, "getpid", lambda: 42)
    users = iter((set(), {"document-user"}))
    monkeypatch.setattr(cluster, "_minio_users", lambda *_args: next(users))
    monkeypatch.setattr(cluster, "_minio_policy_has_user", lambda *_args: True)

    # when
    cluster.configure_minio(kube, "user-policy", "document", policy_path=policy)

    # then
    all_arguments = [argument for call, _ in kube.kubectl_calls for argument in call]
    assert sentinel not in all_arguments
    inputs = [input_text for _, input_text in kube.kubectl_calls if input_text]
    assert any(sentinel in input_text for input_text in inputs)
    assert any(
        call[-4:] == ["rm", "-rf", "--", "/tmp/insurance-hub-42"] for call, _ in kube.kubectl_calls
    )


def test_minio_inventory_failure_does_not_attempt_user_mutation(tmp_path, monkeypatch):
    # given
    kube = FakeKubernetes()
    namespace = "qa-minio-document"
    kube.secrets[(namespace, f"{namespace}-storage-config")] = encoded_secret(
        namespace,
        f"{namespace}-storage-config",
        {"config.env": 'export MINIO_ROOT_USER="root"\nexport MINIO_ROOT_PASSWORD="secret"'},
    )
    kube.secrets[(namespace, f"{namespace}-svc-user-creds")] = encoded_secret(
        namespace,
        f"{namespace}-svc-user-creds",
        {"MINIO_SVC_ACCESS_KEY": "document-user", "MINIO_SVC_SECRET_KEY": "secret"},
    )
    policy = tmp_path / "document-policy.json"
    policy.write_text('{"Version":"2012-10-17","Statement":[]}')
    monkeypatch.setattr(
        cluster,
        "_minio_users",
        lambda *_args: (_ for _ in ()).throw(CommandError("inventory unavailable")),
    )

    # when / then
    with pytest.raises(CommandError, match="inventory unavailable"):
        cluster.configure_minio(kube, "user-policy", "document", policy_path=policy)
    flattened = [argument for call, _ in kube.kubectl_calls for argument in call]
    assert "add" not in flattened
    assert "attach" not in flattened
    assert "rm" in flattened


def test_minio_mutation_failure_reports_uncertain_outcome_and_cleans(tmp_path, monkeypatch):
    # given
    class FailingKubernetes(FakeKubernetes):
        def kubectl(self, *arguments, input_text=None, **kwargs):
            if "add" in arguments:
                raise CommandError("connection lost")
            return super().kubectl(*arguments, input_text=input_text, **kwargs)

    kube = FailingKubernetes()
    namespace = "qa-minio-document"
    kube.secrets[(namespace, f"{namespace}-storage-config")] = encoded_secret(
        namespace,
        f"{namespace}-storage-config",
        {"config.env": 'export MINIO_ROOT_USER="root"\nexport MINIO_ROOT_PASSWORD="secret"'},
    )
    kube.secrets[(namespace, f"{namespace}-svc-user-creds")] = encoded_secret(
        namespace,
        f"{namespace}-svc-user-creds",
        {"MINIO_SVC_ACCESS_KEY": "document-user", "MINIO_SVC_SECRET_KEY": "secret"},
    )
    policy = tmp_path / "document-policy.json"
    policy.write_text('{"Version":"2012-10-17","Statement":[]}')
    monkeypatch.setattr(cluster, "_minio_users", lambda *_args: set())

    # when / then
    with pytest.raises(AdminError, match="outcome is uncertain"):
        cluster.configure_minio(kube, "user-policy", "document", policy_path=policy)
    assert any("rm" in call for call, _ in kube.kubectl_calls)


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


def test_aggregate_preserves_service_order(monkeypatch):
    # given
    kube = FakeKubernetes(target=TARGETS["local-dev"])
    calls = []
    monkeypatch.setattr(
        cluster,
        "_make",
        lambda _commands, target, variables=None: calls.append((target, variables)),
    )
    monkeypatch.setattr(cluster, "create_secret", lambda *_args, **_kwargs: None)
    kube.kubectl = lambda *_args, **_kwargs: ""

    # when
    cluster.deploy_aggregate(kube, "services")

    # then
    service_calls = [
        variables["SVC_NAME"] for target, variables in calls if target == "svc-load-delete-deploy"
    ]
    assert service_calls == [
        "web-vue",
        "agent-portal-gateway",
        "auth",
        "product",
        "policy-search",
        "dashboard",
        "policy",
        "pricing",
        "document",
        "payment",
        "chat",
    ]


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
    cluster.deploy_aggregate(kube, "infra", {})

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
    assert {pid for pid, _ in killed} == {process.pid for process in processes[1:]}


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
        lambda _port: (_ for _ in ()).throw(KeyboardInterrupt()),
    )

    # when / then
    with pytest.raises(KeyboardInterrupt):
        cluster.port_forward(kube)
    assert len(processes) == len(cluster.PORT_FORWARDS)
    assert {pid for pid, _ in killed} == {process.pid for process in processes}
