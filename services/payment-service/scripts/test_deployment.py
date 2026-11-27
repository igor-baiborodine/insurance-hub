"""Payment render, release, target, and disposable deployment checks."""

from __future__ import annotations

import json
import os
import shutil
import uuid
from pathlib import Path

import pytest
import yaml

# isort: split
from scripts.go.service import release
from scripts.go.service.deployment import DeploymentError, apply

# isort: split
import deployment as payment

QA = Path("k8s/overlays/qa/svc")
SMOKE_LABEL = "insurance-hub.io/payment-disposable"


def test_wrong_target_is_rejected_before_access(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setattr(payment, "run", lambda *_args, **_kwargs: pytest.fail("unexpected access"))

    # when / then
    with pytest.raises(DeploymentError, match="coordinates"):
        payment.Target()


@pytest.mark.parametrize(
    "defect",
    [
        "wrong selector",
        "Java Service selector",
        "Java workload selector",
        "extra service port",
        "overbroad ingress",
        "missing caller label",
        "management ingress port",
    ],
)
def test_render_rejects_access_or_selector_defects(defect: str) -> None:
    # given
    items = payment.validate_render("qa", payment.render("qa"))
    caller = items["NetworkPolicy"]["spec"]["ingress"][0]
    if defect == "wrong selector":
        items["Service"]["spec"]["selector"] = {"app.kubernetes.io/name": "other-api"}
    elif defect == "Java Service selector":
        items["Service"]["spec"]["selector"] = dict(payment.JAVA_SELECTOR)
    elif defect == "Java workload selector":
        items["NetworkPolicy"]["spec"]["podSelector"]["matchLabels"] = dict(payment.JAVA_SELECTOR)
    elif defect == "extra service port":
        items["Service"]["spec"]["ports"].append(
            {"name": "grpc", "port": 9090, "targetPort": "grpc", "protocol": "TCP"}
        )
    elif defect == "overbroad ingress":
        del caller["from"][0]["podSelector"]
    elif defect == "missing caller label":
        del caller["from"][0]["podSelector"]["matchLabels"][payment.BUSINESS_CLIENT_LABEL]
    else:
        caller["ports"].append({"port": payment.PORTS["management"], "protocol": "TCP"})

    # when / then
    with pytest.raises(DeploymentError):
        payment.validate_render("qa", yaml.safe_dump_all(items.values()))


def test_local_egress_rejects_broad_dns() -> None:
    # given
    items = payment.validate_render("local-dev", payment.render("local-dev"))
    dns = items["NetworkPolicy"]["spec"]["egress"][0]
    del dns["to"][0]["podSelector"]

    # when / then
    with pytest.raises(DeploymentError, match="NetworkPolicy"):
        payment.validate_render("local-dev", yaml.safe_dump_all(items.values()))


@pytest.mark.parametrize("defect", ["owner credential", "extra mount", "missing database egress"])
def test_local_render_mounts_only_the_writer_database_path(defect: str) -> None:
    # given
    items = payment.validate_render("local-dev", payment.render("local-dev"))
    pod = items["Deployment"]["spec"]["template"]["spec"]
    if defect == "owner credential":
        pod["volumes"][0]["secret"]["secretName"] = "local-dev-postgres-payment-user-creds"
    elif defect == "extra mount":
        pod["containers"][0]["volumeMounts"].append({"name": "admin", "mountPath": "/admin"})
    else:
        items["NetworkPolicy"]["spec"]["egress"] = [
            rule
            for rule in items["NetworkPolicy"]["spec"]["egress"]
            if rule["ports"] != [{"port": payment.DATABASE_PORT, "protocol": "TCP"}]
        ]

    # when / then
    with pytest.raises(DeploymentError):
        payment.validate_render("local-dev", yaml.safe_dump_all(items.values()))


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
@pytest.mark.parametrize(
    "defect", ["wrong topic", "wrong CA Secret", "namespace-wide Kafka egress"]
)
def test_render_scopes_the_payment_kafka_client(environment: str, defect: str) -> None:
    # given
    items = payment.validate_render(environment, payment.render(environment))
    pod = items["Deployment"]["spec"]["template"]["spec"]
    kafka_egress = next(
        rule
        for rule in items["NetworkPolicy"]["spec"]["egress"]
        if rule["to"][0]
        .get("podSelector", {})
        .get("matchLabels", {})
        .get("strimzi.io/component-type")
        == "kafka"
    )
    if defect == "wrong topic":
        items["ConfigMap"]["data"]["KAFKA_REGISTERED_TOPIC"] = "shared-business-events"
    elif defect == "wrong CA Secret":
        next(volume for volume in pod["volumes"] if volume["name"] == "kafka-ca")["secret"][
            "secretName"
        ] = "unrelated-kafka-ca"
    else:
        del kafka_egress["to"][0]["podSelector"]

    # when / then
    with pytest.raises(DeploymentError):
        payment.validate_render(environment, yaml.safe_dump_all(items.values()))


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
@pytest.mark.parametrize(
    "defect", ["legacy credential", "other tenant egress", "wrong bucket", "QA trust toggled"]
)
def test_render_scopes_the_payment_minio_reader(environment: str, defect: str) -> None:
    # given
    items = payment.validate_render(environment, payment.render(environment))
    container = items["Deployment"]["spec"]["template"]["spec"]["containers"][0]
    data = items["ConfigMap"]["data"]
    minio_egress = next(
        rule
        for rule in items["NetworkPolicy"]["spec"]["egress"]
        if rule["ports"] == [{"port": payment.MINIO_PORT, "protocol": "TCP"}]
    )
    if defect == "legacy credential":
        next(item for item in container["env"] if item["name"] == "MINIO_ACCESS_KEY")["valueFrom"][
            "secretKeyRef"
        ]["name"] = f"{environment}-minio-payment-svc-user-creds"
    elif defect == "other tenant egress":
        minio_egress["to"][0]["namespaceSelector"]["matchLabels"]["kubernetes.io/metadata.name"] = (
            f"{environment}-minio-document"
        )
    elif defect == "wrong bucket":
        data["MINIO_BUCKET"] = "policies"
    elif "MINIO_CA_FILE" in data:
        del data["MINIO_CA_FILE"]
    else:
        data["MINIO_CA_FILE"] = f"{payment.MINIO_CA_DIRECTORY}/ca.crt"

    # when / then
    with pytest.raises(DeploymentError):
        payment.validate_render(environment, yaml.safe_dump_all(items.values()))


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
def test_service_specific_namespace_is_rejected(
    monkeypatch: pytest.MonkeyPatch, environment: str
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", environment)
    monkeypatch.setenv(
        "DEPLOY_CONTEXT", f"{'kind-' if environment != 'qa' else ''}{environment}-insurance-hub"
    )
    monkeypatch.setenv("DEPLOY_SVC_NS", "payment-specific-namespace")

    # when / then
    with pytest.raises(DeploymentError, match="coordinates"):
        payment.Target()


def test_qa_refuses_placeholder_activation_and_runtime(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    items = payment.validate_render("qa", payment.render("qa"))
    assert items["ConfigMap"]["data"]["SERVICE_VERSION"] == payment.PLACEHOLDER
    monkeypatch.setattr(payment, "render", lambda _environment: yaml.safe_dump_all(items.values()))
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setenv("DEPLOY_IMAGE_SOURCE_REVISION", "a" * 40)
    monkeypatch.delenv("DEPLOY_IMAGE_REF", raising=False)

    # when / then
    with pytest.raises(DeploymentError, match="activation"):
        payment.qa_activation(["./payment", "./payment/legacy"], items)
    with pytest.raises(DeploymentError, match="legacy Payment"):
        payment.qa_activation(["./payment/legacy", "./payment/other"], items)
    with pytest.raises(DeploymentError, match="inactive or differs"):
        payment.Target().rendered()


@pytest.mark.parametrize("revision_matches", [True, False])
def test_qa_image_requires_published_digest_and_matching_oci_labels(
    monkeypatch: pytest.MonkeyPatch, revision_matches: bool
) -> None:
    # given
    revision = "a" * 40
    digest = "sha256:" + "b" * 64
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setenv("DEPLOY_IMAGE_SOURCE_REVISION", revision)
    monkeypatch.setenv("DEPLOY_IMAGE_DIGEST", digest)
    target = payment.Target()
    registry = target.record["registryImage"]

    def inspect(arguments: list[str]) -> str:
        assert arguments[1:] == ["image", "inspect", f"{registry}@{digest}"]
        return json.dumps(
            [
                {
                    "RepoDigests": [f"{registry}@{digest}"],
                    "Config": {
                        "Labels": {
                            "org.opencontainers.image.revision": revision
                            if revision_matches
                            else "c" * 40,
                            "org.opencontainers.image.version": "git-" + revision[:12],
                        }
                    },
                }
            ]
        )

    monkeypatch.setattr(payment, "run", inspect)

    # when / then
    if revision_matches:
        assert payment.image_id(target, f"{registry}:1.0.0@{digest}") == digest
    else:
        with pytest.raises(DeploymentError, match="OCI labels"):
            payment.image_id(target, f"{registry}:1.0.0@{digest}")


def test_qa_rejects_desired_state_without_publication_digest(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setenv("DEPLOY_IMAGE_DIGEST", "sha256:" + "b" * 64)
    target = payment.Target()
    registry = target.record["registryImage"]
    items = payment.validate_render("qa", payment.render("qa"))
    monkeypatch.setattr(payment, "run", lambda *_args, **_kwargs: pytest.fail("unexpected access"))
    items["ConfigMap"]["data"]["SERVICE_VERSION"] = "git-0123456789ab"
    deployment = items["Deployment"]
    template = deployment["spec"]["template"]
    for metadata in (deployment["metadata"], template["metadata"]):
        metadata["labels"][payment.VERSION_LABEL] = "git-0123456789ab"
    template["spec"]["containers"][0]["image"] = f"{registry}:1.0.0"

    # when / then
    with pytest.raises(DeploymentError, match="digest-pinned release"):
        payment.validate_render("qa", yaml.safe_dump_all(items.values()))
    with pytest.raises(DeploymentError, match="not pinned to the publication digest"):
        payment.image_id(target, f"{registry}:1.0.0@sha256:" + "c" * 64)


def test_qa_requires_exact_flux_inventory(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    revision = "a" * 40
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setenv("DEPLOY_SOURCE_REVISION", revision)
    target = payment.Target()
    items = payment.validate_render("qa", payment.render("qa"))
    entries = [
        {"id": "qa-svc_qa-payment-api_apps_Deployment", "v": "v1"},
        {"id": "qa-svc_qa-payment-api__Service", "v": "v1"},
        {"id": "qa-svc_qa-payment-api_networking.k8s.io_NetworkPolicy", "v": "v1"},
        {"id": "qa-svc_qa-payment-config__ConfigMap", "v": "v1"},
    ]

    def owner(*args: str) -> dict:
        assert args == ("get", "kustomization", "qa-svc", "-n", "flux-system")
        return {
            "status": {
                "conditions": [{"type": "Ready", "status": "True"}],
                "lastAppliedRevision": f"main@sha1:{revision}",
                "inventory": {"entries": entries},
            }
        }

    monkeypatch.setattr(target, "json", owner)

    # when / then
    target.verify_flux_inventory(items)
    entries.pop()
    with pytest.raises(DeploymentError, match="does not own"):
        target.verify_flux_inventory(items)


def test_release_prepares_published_version_and_activation(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    source = payment.ROOT
    for relative in ("k8s/apps", "k8s/overlays/qa"):
        shutil.copytree(source / relative, tmp_path / relative)
    for relative in ("scripts/go/service/catalog.json", "services/payment-service/Dockerfile"):
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / relative, target)
    repository = "igor-baiborodine/insurance-hub"
    sha = "0123456789ab" + "c" * 28
    parent = tmp_path / QA / "kustomization.yaml"
    assert "./payment" not in yaml.safe_load(parent.read_text())["resources"]
    previous = parent.read_bytes()

    def inventory() -> dict:
        rendered = payment.run(["kubectl", "kustomize", str(parent.parent)])
        objects = list(yaml.safe_load_all(rendered))
        result = {
            (
                item["apiVersion"],
                item["kind"],
                item["metadata"].get("namespace", ""),
                item["metadata"]["name"],
            ): item
            for item in objects
        }
        assert len(result) == len(objects)
        return result

    before = inventory()
    selection = release.select_service(
        ["services/payment-service/Dockerfile"], tmp_path, repository
    )
    assert selection.service == "payment"
    with pytest.raises(release.ReleaseSelectionError, match="digest"):
        release.prepare_release(tmp_path, "payment", repository, "1.0.0", sha, "unpublished")
    assert parent.read_bytes() == previous

    # when
    paths = release.prepare_release(
        tmp_path, "payment", repository, "1.0.0", sha, "sha256:" + "d" * 64
    )

    # then
    assert set(paths) == {parent, tmp_path / QA / "payment/kustomization.yaml"}
    monkeypatch.setattr(payment, "ROOT", tmp_path)
    items = payment.validate_render("qa", payment.render("qa"))
    assert items["ConfigMap"]["data"]["SERVICE_VERSION"] == "git-0123456789ab"
    assert items["Deployment"]["spec"]["template"]["spec"]["containers"][0]["image"] == (
        "ghcr.io/igor-baiborodine/insurance-hub/payment-api:1.0.0@sha256:" + "d" * 64
    )
    payment.qa_activation(yaml.safe_load(parent.read_text())["resources"], items)
    after = inventory()
    added = {key: item for key, item in after.items() if key not in before}
    assert {key: after[key] for key in before} == before
    assert {key[3] for key in added} == {
        "qa-payment-api",
        "qa-payment-config",
    }
    assert len(added) == 4
    assert {item["kind"]: item for item in added.values()} == items


def test_denial_cleanup_preserves_primary_error(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    target = payment.Target()
    reads = iter(({}, {"metadata": {"labels": {}}}))

    def create(*_args: str, **_kwargs: object) -> str:
        raise DeploymentError("probe creation failed")

    monkeypatch.setattr(target, "json", lambda *_args: next(reads))
    monkeypatch.setattr(target, "kubectl", create)

    # when / then
    with pytest.raises(DeploymentError, match="probe creation failed"):
        payment.verify_denial(target, {"status": {"podIP": "192.0.2.10"}})
    assert "cleanup failed: Payment denial probe ownership differs" in capsys.readouterr().err


@pytest.mark.parametrize("denied_output", [True, False])
def test_payment_http_requires_gateway_identity_and_ordinary_denial(
    monkeypatch: pytest.MonkeyPatch, denied_output: bool
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    target = payment.Target()
    address = "local-dev-payment-api:80"
    probes: list[tuple[str, list[str], dict | None]] = []

    def probe(_target: object, name: str, _description: str, args: list[str], labels=None):
        probes.append((name, args[1:], labels))
        verb = "allowed" if labels else ("denied" if denied_output else "reached")
        return [f"{verb} {address}"]

    monkeypatch.setattr(payment, "run_probe", probe)

    # when / then
    if denied_output:
        payment.verify_business_access(target)
    else:
        with pytest.raises(DeploymentError, match="unrelated workload reaches Payment HTTP"):
            payment.verify_business_access(target)
    assert probes[0] == (
        "payment-gateway-allowed",
        [address],
        payment.gateway_selector("local-dev"),
    )
    assert probes[1] == ("payment-http-denied", [address], None)


def test_requested_route_supports_shared_runner_hook(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    target = payment.Target()
    monkeypatch.setattr(
        target,
        "json",
        lambda *_args, **_kwargs: {
            "items": [
                {
                    "metadata": {"name": target.deployment},
                    "spec": {"selector": dict(payment.SELECTOR)},
                }
            ]
        },
    )
    target.template_labels = dict(payment.SELECTOR)

    # when / then
    payment.check_requested_route(target)


def test_denial_probe_waits_for_network_policy_convergence(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    target = payment.Target()
    created: dict = {}
    deleted: list[str] = []

    def kubectl(*args: str, input_text: str | None = None, **_kwargs: object) -> str:
        if args[0] == "create":
            created.update(json.loads(input_text or "{}"))
        elif args[0] == "delete":
            deleted.append(args[2])
        return "\n".join(f"Payment port {port} denied" for port in payment.PORTS.values())

    def read(*args: str, **_kwargs: object) -> dict:
        if not created or "--ignore-not-found" not in args:
            return {"status": {"phase": "Succeeded"}} if created else {}
        return {"metadata": created["metadata"]}

    monkeypatch.setattr(target, "kubectl", kubectl)
    monkeypatch.setattr(target, "json", read)

    # when
    payment.verify_denial(target, {"status": {"podIP": "192.0.2.10"}})

    # then
    script = created["spec"]["containers"][0]["args"][0]
    assert script.startswith(f"sleep {payment.NETWORK_POLICY_CONVERGENCE_SECONDS}; ")
    assert "Established connection" in script
    assert created["spec"]["containers"][0]["args"][-3:] == ["8080", "9090", "8081"]
    assert deleted == ["payment-access-denied"]


@pytest.mark.parametrize("extra_endpoint", [False, True])
def test_go_service_selects_only_the_ready_go_pod(
    monkeypatch: pytest.MonkeyPatch, extra_endpoint: bool
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    target = payment.Target()
    endpoints = [{"conditions": {"ready": True}, "targetRef": {"kind": "Pod", "name": "go-0"}}]
    if extra_endpoint:
        endpoints.append(
            {"conditions": {"ready": True}, "targetRef": {"kind": "Pod", "name": "java-0"}}
        )

    def read(*args: str, **_kwargs: object) -> dict:
        if args[:2] == ("get", "endpointslices"):
            assert args[2:] == ("-l", "kubernetes.io/service-name=qa-payment-api")
            return {"items": [{"endpoints": endpoints}]}
        assert args == ("get", "pod", "go-0")
        return {"metadata": {"labels": payment.SELECTOR}}

    monkeypatch.setattr(target, "json", read)
    go_pod = {"metadata": {"name": "go-0"}}

    # when / then
    if extra_endpoint:
        with pytest.raises(DeploymentError, match="ready endpoints differ"):
            payment.verify_go_service(target, go_pod)
    else:
        payment.verify_go_service(target, go_pod)


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
def test_gateway_route_rejects_go_payment_upstream(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, environment: str
) -> None:
    # given
    for template in (payment.GATEWAY_PROFILE, payment.GATEWAY_OVERRIDE):
        relative = template.format(environment=environment)
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(payment.ROOT / relative, target)
    monkeypatch.setattr(payment, "ROOT", tmp_path)
    payment.check_gateway_route(environment)
    override = tmp_path / payment.GATEWAY_OVERRIDE.format(environment=environment)
    document = yaml.safe_load(override.read_text())
    document["micronaut"]["http"]["services"]["payment-service"] = {
        "urls": [f"http://{environment}-payment-api:80"]
    }
    override.write_text(yaml.safe_dump(document))

    # when / then
    with pytest.raises(DeploymentError, match="gateway Payment upstream"):
        payment.check_gateway_route(environment)


@pytest.mark.parametrize("go_endpoint", [False, True])
def test_java_route_selects_only_ready_java_pods(
    monkeypatch: pytest.MonkeyPatch, go_endpoint: bool
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    target = payment.Target()
    endpoints = [{"conditions": {"ready": True}, "targetRef": {"kind": "Pod", "name": "java-0"}}]
    if go_endpoint:
        endpoints.append(
            {"conditions": {"ready": True}, "targetRef": {"kind": "Pod", "name": "go-0"}}
        )
    labels = {"java-0": payment.JAVA_SELECTOR, "go-0": payment.SELECTOR}

    def read(*args: str, **_kwargs: object) -> dict:
        if args[:2] == ("get", "service"):
            assert args[2] == "qa-payment-api-legacy"
            return {"spec": {"selector": {**payment.JAVA_SELECTOR, "environment": "qa"}}}
        if args[:2] == ("get", "endpointslices"):
            assert args[2:] == ("-l", "kubernetes.io/service-name=qa-payment-api-legacy")
            return {"items": [{"endpoints": endpoints}]}
        return {"metadata": {"labels": labels[args[2]]}}

    monkeypatch.setattr(target, "json", read)
    go_pod = {"metadata": {"name": "go-0"}}

    # when / then
    if go_endpoint:
        with pytest.raises(DeploymentError, match="only ready Java Payment Pods"):
            payment.verify_java_route(target, go_pod)
    else:
        payment.verify_java_route(target, go_pod)


@pytest.mark.parametrize("logged", [True, False], ids=["database-ready", "database-unverified"])
def test_workload_verification_checks_database_readiness(
    monkeypatch: pytest.MonkeyPatch, logged: bool
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    monkeypatch.setenv("DEPLOY_DENIAL_PROBE", "0")
    target = payment.Target()
    target.namespace = "payment-smoke-0123456789ab"
    items = {"ConfigMap": {"data": {"SERVICE_VERSION": "git-0123456789ab"}}}
    pod = {"metadata": {"name": "go-0", "uid": "uid-0"}}
    started = {
        "msg": "service started",
        "service_name": "payment-service",
        "service_namespace": "insurance-hub",
        "service_version": "git-0123456789ab",
        "service_instance_id": "uid-0",
        "deployment_environment_name": "local-dev",
        "service_implementation": "go",
    }

    class Forward:
        def __init__(self, spec: object, **_kwargs: object) -> None:
            self.port = spec.remote_port

        def __enter__(self) -> int:
            return self.port

        def __exit__(self, *_args: object) -> None:
            return None

    def answer(port: int, path: str, payload: bytes | None = None) -> tuple[int, bytes]:
        if payload:
            return 200, payload
        return 200, b"live\n" if path == "/livez" else b"ready\n"

    monkeypatch.setattr(target, "live", lambda *_args, **_kwargs: pod)
    ready = {"msg": "payment database ready", "database_role": "go_payment_writer"}
    logs = "\n".join(json.dumps(record) for record in [started, *([ready] if logged else [])])
    monkeypatch.setattr(target, "kubectl", lambda *_args, **_kwargs: logs)
    monkeypatch.setattr(target, "json", lambda *_args, **_kwargs: pytest.fail("Java lookup"))
    monkeypatch.setattr(payment, "OwnedPortForward", Forward)
    monkeypatch.setattr(payment, "response", answer)
    monkeypatch.setattr(payment, "verify_direct_rpc", lambda *_args: None)
    java: list[dict] = []
    gateway: list[str] = []
    go: list[dict] = []
    monkeypatch.setattr(payment, "verify_java_route", lambda _target, value: java.append(value))
    monkeypatch.setattr(
        payment, "verify_gateway_route", lambda value: gateway.append(value.environment)
    )
    monkeypatch.setattr(payment, "verify_go_service", lambda _target, value: go.append(value))

    if not logged:
        # when / then
        with pytest.raises(DeploymentError, match="writer database connection"):
            payment.verify_workload(target, items)
        return

    # when
    workload = payment.verify_workload(target, items)
    payment.verify(target, items)

    # then
    assert workload == pod
    assert java == [pod]
    assert gateway == ["local-dev"]
    assert go == [pod]


@pytest.mark.parametrize("routing", ["java", "config-file", "environment", "profile"])
def test_gateway_route_observes_effective_destination_beside_healthy_java(
    monkeypatch: pytest.MonkeyPatch, routing: str
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    target = payment.Target()
    services = {"product-service": {"urls": ["http://qa-product-api.qa-svc.svc.cluster.local:80"]}}
    if routing == "config-file":
        services["payment-service"] = {"urls": ["http://qa-payment-api:80"]}
    env = [
        {"name": "MICRONAUT_ENVIRONMENTS", "value": "local-dev" if routing == "profile" else "qa"},
        {"name": "MICRONAUT_CONFIG_FILES", "value": "/etc/product-route/application.yml"},
    ]
    if routing == "environment":
        env.append({"name": "MICRONAUT_HTTP_SERVICES_PAYMENT_SERVICE_URLS", "value": "http://x"})
    gateway_pod = {
        "spec": {
            "containers": [
                {
                    "env": env,
                    "volumeMounts": [
                        {"name": "route", "mountPath": "/etc/product-route", "readOnly": True}
                    ],
                }
            ],
            "volumes": [{"name": "route", "configMap": {"name": "qa-gateway-route"}}],
        }
    }
    override = {"micronaut": {"http": {"services": services}}}

    def endpoints(name: str) -> dict:
        return {
            "items": [
                {
                    "endpoints": [
                        {"conditions": {"ready": True}, "targetRef": {"kind": "Pod", "name": name}}
                    ]
                }
            ]
        }

    def read(*args: str, **_kwargs: object) -> dict:
        if args[:2] == ("get", "endpointslices"):
            service = args[3].split("=", 1)[1]
            return endpoints("gateway-0" if "gateway" in service else "java-0")
        if args[:2] == ("get", "configmap"):
            assert args[2] == "qa-gateway-route"
            return {"data": {"application.yml": yaml.safe_dump(override)}}
        if args[:2] == ("get", "service"):
            return {"spec": {"selector": dict(payment.JAVA_SELECTOR)}}
        if args[2] == "gateway-0":
            return gateway_pod
        return {"metadata": {"labels": dict(payment.JAVA_SELECTOR)}}

    monkeypatch.setattr(target, "json", read)

    # when
    payment.verify_java_route(target, {"metadata": {"name": "go-0"}})

    # then
    if routing == "java":
        payment.verify_gateway_route(target)
    else:
        with pytest.raises(DeploymentError, match="Payment upstream"):
            payment.verify_gateway_route(target)


@pytest.mark.parametrize(
    "output",
    [
        "PASS\n",
        json.dumps({"Action": "pass", "Test": payment.DIRECT_RPC_SMOKE}),
        "\n".join(
            json.dumps({"Action": action, "Test": "TestRenamedDirectRPC"})
            for action in ("run", "pass")
        ),
    ],
    ids=["no-json", "missing-run", "renamed"],
)
def test_direct_rpc_smoke_requires_named_test_to_pass(output: str) -> None:
    with pytest.raises(DeploymentError, match="did not execute and pass exactly once"):
        payment.validate_direct_rpc_output(output)


@pytest.mark.parametrize("error, status", [(KeyboardInterrupt, 130), (payment.Terminated, 143)])
def test_cli_signal_exit_status(
    monkeypatch: pytest.MonkeyPatch, error: type[KeyboardInterrupt], status: int
) -> None:
    # given
    def interrupted() -> int:
        raise error

    monkeypatch.setattr(payment, "main", interrupted)

    # when / then
    assert payment.cli() == status


def test_stop_uses_remaining_poll_budget(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    target = payment.Target()
    time_values = iter((100.0, 185.0))
    monkeypatch.setattr(payment.time, "monotonic", lambda: next(time_values))
    monkeypatch.setattr(target, "live", lambda *_args, **_kwargs: {})
    scale_timeouts: list[float] = []
    poll_timeouts: list[float] = []

    def scale(*_args: str, timeout: float) -> None:
        scale_timeouts.append(timeout)

    def poll(*_args: str, timeout: float) -> dict:
        poll_timeouts.append(timeout)
        return {"metadata": {"generation": 1}, "status": {"observedGeneration": 1}}

    monkeypatch.setattr(target, "kubectl", scale)
    monkeypatch.setattr(target, "json", poll)

    # when
    payment.stop(target, {})

    # then
    assert scale_timeouts == [65]
    assert poll_timeouts == [5]


@pytest.mark.integration
def test_payment_in_owned_disposable_kind_namespace(monkeypatch: pytest.MonkeyPatch) -> None:
    # given

    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    target = payment.Target()
    require = payment.require
    require(
        target.configuration["kindCluster"] == "local-dev-insurance-hub"
        and target.context == "kind-local-dev-insurance-hub",
        "unexpected disposable cluster",
    )
    image = os.environ.get("DEPLOY_IMAGE_REF", "")
    payment.image_id(target, image)
    require(
        target.configuration["kindCluster"] in payment.run(["kind", "get", "clusters"]).split(),
        "configured Kind cluster is missing",
    )
    _, items, selected = target.rendered()
    require(selected == image, "disposable image differs")
    secrets = []
    for name, keys in (
        (f"{target.environment}-postgres-payment-writer-creds", {"database-url", "ca.crt"}),
        (
            f"{target.environment}-minio-payment-reader-creds",
            {"endpoint", "access-key", "secret-key", "ca.crt"},
        ),
        (f"{target.environment}-payment-kafka-ca", {"ca.crt"}),
    ):
        source_secret = target.json("get", "secret", name)
        secret_data = source_secret.get("data")
        require(
            source_secret.get("type") == "Opaque"
            and isinstance(secret_data, dict)
            and set(secret_data) == keys,
            f"disposable smoke requires the exact Payment Secret {name}",
        )
        secrets.append((name, secret_data))
    token = uuid.uuid4().hex[:12]
    target.namespace = f"payment-smoke-{token}"
    namespace = {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {"name": target.namespace, "labels": {SMOKE_LABEL: token}},
    }
    # The writer, reader and public Kafka CA Secrets are copied into the owned namespace and
    # removed with it. The workload only runs its read-only startup and readiness checks; Echo
    # writes nothing. The fully qualified broker name preserves the ordinary cluster while DNS
    # originates in the disposable namespace.
    for item in items.values():
        item["metadata"]["namespace"] = target.namespace
    ingress_peer = items["NetworkPolicy"]["spec"]["ingress"][0]["from"][0]
    ingress_peer["namespaceSelector"]["matchLabels"]["kubernetes.io/metadata.name"] = (
        target.namespace
    )
    items["ConfigMap"]["data"]["KAFKA_BROKERS"] = (
        "local-dev-kafka-kafka-bootstrap.local-dev-all.svc:9093"
    )

    try:
        # when
        target.kubectl("create", "-f", "-", input_text=json.dumps(namespace))
        for name, data in secrets:
            secret = {
                "apiVersion": "v1",
                "kind": "Secret",
                "metadata": {"name": name, "namespace": target.namespace},
                "type": "Opaque",
                "data": data,
            }
            target.kubectl("create", "-f", "-", input_text=json.dumps(secret))
        payment.run(
            ["kind", "load", "docker-image", image, "--name", target.configuration["kindCluster"]],
            timeout=120,
        )
        apply(target, yaml.safe_dump_all(items.values()), "insurance-hub-payment-smoke")
        payment.rollout(target)

        # then
        monkeypatch.setenv("DEPLOY_DENIAL_PROBE", "0")
        pod = payment.verify_workload(target, items)
        payment.verify_go_service(target, pod)
        payment.verify_denial(target, pod)
        payment.verify_business_access(target)
        payment.stop(target, items)
        assert target.json("get", "deployment", target.deployment)["spec"]["replicas"] == 0
    finally:
        actual = target.json("get", "namespace", target.namespace, "--ignore-not-found")
        if actual:
            require(
                actual["metadata"]["labels"].get(SMOKE_LABEL) == token,
                "disposable namespace ownership differs",
            )
            target.kubectl("delete", "namespace", target.namespace, "--timeout=100s", timeout=110)
            assert not target.json("get", "namespace", target.namespace, "--ignore-not-found")
            print(f"Removed disposable namespace {target.namespace}")
