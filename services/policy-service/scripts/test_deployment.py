"""Policy render, release, target, and disposable deployment checks."""

from __future__ import annotations

import json
import os
import shutil
import uuid
from pathlib import Path

import pytest
import yaml
from scripts.go.service import release
from scripts.go.service.deployment import DeploymentError, apply

import deployment as policy

QA = Path("k8s/overlays/qa/svc")
SMOKE_LABEL = "insurance-hub.io/policy-disposable"


def test_wrong_target_is_rejected_before_access(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setattr(policy, "run", lambda *_args, **_kwargs: pytest.fail("unexpected access"))

    # when / then
    with pytest.raises(DeploymentError, match="coordinates"):
        policy.Target()


@pytest.mark.parametrize("defect", ["legacy selector", "public service", "allow Pod ingress"])
def test_render_rejects_access_or_selector_defects(defect: str) -> None:
    # given
    items = policy.validate_render("qa", policy.render("qa"))
    if defect == "legacy selector":
        items["Deployment"]["spec"]["selector"]["matchLabels"] = {
            "app.kubernetes.io/name": "policy-api-legacy",
            "app.kubernetes.io/component": "api",
        }
    elif defect == "public service":
        items["Service"] = {"kind": "Service", "spec": {"type": "LoadBalancer"}}
    else:
        items["NetworkPolicy"]["spec"]["ingress"] = [{}]

    # when / then
    with pytest.raises(DeploymentError):
        policy.validate_render("qa", yaml.safe_dump_all(items.values()))


def test_local_egress_rejects_broad_dns() -> None:
    # given
    items = policy.validate_render("local-dev", policy.render("local-dev"))
    dns = items["NetworkPolicy"]["spec"]["egress"][0]
    del dns["to"][0]["podSelector"]

    # when / then
    with pytest.raises(DeploymentError, match="NetworkPolicy"):
        policy.validate_render("local-dev", yaml.safe_dump_all(items.values()))


def test_qa_refuses_placeholder_activation_and_runtime(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    items = policy.validate_render("qa", policy.render("qa"))
    items["ConfigMap"]["data"]["SERVICE_VERSION"] = policy.PLACEHOLDER
    deployment = items["Deployment"]
    template = deployment["spec"]["template"]
    for metadata in (deployment["metadata"], template["metadata"]):
        metadata["labels"][policy.VERSION_LABEL] = policy.PLACEHOLDER
    template["spec"]["containers"][0]["image"] = (
        "ghcr.io/igor-baiborodine/insurance-hub/policy-api:0.0.0-unpublished"
    )
    monkeypatch.setattr(policy, "render", lambda _environment: yaml.safe_dump_all(items.values()))
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setenv("DEPLOY_IMAGE_SOURCE_REVISION", "a" * 40)
    monkeypatch.delenv("DEPLOY_IMAGE_REF", raising=False)

    # when / then
    with pytest.raises(DeploymentError, match="activation"):
        policy.qa_activation(["./policy/legacy", "./policy"], items)
    with pytest.raises(DeploymentError, match="inactive or differs"):
        policy.Target().rendered()


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
    target = policy.Target()
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

    monkeypatch.setattr(policy, "run", inspect)

    # when / then
    if revision_matches:
        assert policy.image_id(target, f"{registry}:1.0.0@{digest}") == digest
    else:
        with pytest.raises(DeploymentError, match="OCI labels"):
            policy.image_id(target, f"{registry}:1.0.0@{digest}")


def test_qa_rejects_desired_state_without_publication_digest(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setenv("DEPLOY_IMAGE_DIGEST", "sha256:" + "b" * 64)
    target = policy.Target()
    registry = target.record["registryImage"]
    items = policy.validate_render("qa", policy.render("qa"))
    monkeypatch.setattr(policy, "run", lambda *_args, **_kwargs: pytest.fail("unexpected access"))
    items["ConfigMap"]["data"]["SERVICE_VERSION"] = "git-0123456789ab"
    deployment = items["Deployment"]
    template = deployment["spec"]["template"]
    for metadata in (deployment["metadata"], template["metadata"]):
        metadata["labels"][policy.VERSION_LABEL] = "git-0123456789ab"
    template["spec"]["containers"][0]["image"] = f"{registry}:1.0.0"

    # when / then
    with pytest.raises(DeploymentError, match="digest-pinned release"):
        policy.validate_render("qa", yaml.safe_dump_all(items.values()))
    with pytest.raises(DeploymentError, match="not pinned to the publication digest"):
        policy.image_id(target, f"{registry}:1.0.0@sha256:" + "c" * 64)


def test_qa_requires_exact_flux_inventory(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    revision = "a" * 40
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setenv("DEPLOY_SOURCE_REVISION", revision)
    target = policy.Target()
    items = policy.validate_render("qa", policy.render("qa"))
    entries = [
        {"id": "qa-svc_qa-policy-api_apps_Deployment", "v": "v1"},
        {"id": "qa-svc_qa-policy-api_networking.k8s.io_NetworkPolicy", "v": "v1"},
        {"id": "qa-svc_qa-policy-config__ConfigMap", "v": "v1"},
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
    source = policy.ROOT
    for relative in ("k8s/apps", "k8s/overlays/qa"):
        shutil.copytree(source / relative, tmp_path / relative)
    for relative in ("scripts/go/service/catalog.json", "services/policy-service/Dockerfile"):
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / relative, target)
    repository = "igor-baiborodine/insurance-hub"
    sha = "0123456789ab" + "c" * 28
    parent = tmp_path / QA / "kustomization.yaml"
    aggregate = yaml.safe_load(parent.read_text())
    aggregate["resources"] = [
        resource for resource in aggregate["resources"] if resource != "./policy"
    ]
    parent.write_text(yaml.safe_dump(aggregate))
    previous = parent.read_bytes()

    def inventory() -> dict:
        rendered = policy.run(["kubectl", "kustomize", str(parent.parent)])
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
    selection = release.select_service(["services/policy-service/Dockerfile"], tmp_path, repository)
    assert selection.service == "policy"
    with pytest.raises(release.ReleaseSelectionError, match="digest"):
        release.prepare_release(tmp_path, "policy", repository, "1.0.0", sha, "unpublished")
    assert parent.read_bytes() == previous

    # when
    paths = release.prepare_release(
        tmp_path, "policy", repository, "1.0.0", sha, "sha256:" + "d" * 64
    )

    # then
    assert set(paths) == {parent, tmp_path / QA / "policy/kustomization.yaml"}
    monkeypatch.setattr(policy, "ROOT", tmp_path)
    items = policy.validate_render("qa", policy.render("qa"))
    assert items["ConfigMap"]["data"]["SERVICE_VERSION"] == "git-0123456789ab"
    assert items["Deployment"]["spec"]["template"]["spec"]["containers"][0]["image"] == (
        "ghcr.io/igor-baiborodine/insurance-hub/policy-api:1.0.0@sha256:" + "d" * 64
    )
    policy.qa_activation(yaml.safe_load(parent.read_text())["resources"], items)
    after = inventory()
    added = {key: item for key, item in after.items() if key not in before}
    assert {key: after[key] for key in before} == before
    assert {key[3] for key in added} == {
        "qa-policy-api",
        "qa-policy-config",
    }
    assert len(added) == 3
    assert {item["kind"]: item for item in added.values()} == items


def test_denial_cleanup_preserves_primary_error(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    target = policy.Target()
    reads = iter(({}, {"metadata": {"labels": {}}}))

    def create(*_args: str, **_kwargs: object) -> str:
        raise DeploymentError("probe creation failed")

    monkeypatch.setattr(target, "json", lambda *_args: next(reads))
    monkeypatch.setattr(target, "kubectl", create)

    # when / then
    with pytest.raises(DeploymentError, match="probe creation failed"):
        policy.verify_denial(target, {"status": {"podIP": "192.0.2.10"}})
    assert "cleanup failed: Policy denial probe ownership differs" in capsys.readouterr().err


def test_denial_probe_waits_for_network_policy_convergence(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    target = policy.Target()
    created: dict = {}
    deleted: list[str] = []

    def kubectl(*args: str, input_text: str | None = None, **_kwargs: object) -> str:
        if args[0] == "create":
            created.update(json.loads(input_text or "{}"))
        elif args[0] == "delete":
            deleted.append(args[2])
        return "\n".join(f"Policy port {port} denied" for port in policy.PORTS.values())

    def read(*args: str, **_kwargs: object) -> dict:
        if not created or "--ignore-not-found" not in args:
            return {"status": {"phase": "Succeeded"}} if created else {}
        return {"metadata": created["metadata"]}

    monkeypatch.setattr(target, "kubectl", kubectl)
    monkeypatch.setattr(target, "json", read)

    # when
    policy.verify_denial(target, {"status": {"podIP": "192.0.2.10"}})

    # then
    script = created["spec"]["containers"][0]["args"][0]
    assert script.startswith(f"sleep {policy.NETWORK_POLICY_CONVERGENCE_SECONDS}; ")
    assert "Established connection" in script
    assert created["spec"]["containers"][0]["args"][-3:] == ["8080", "9090", "8081"]
    assert deleted == ["policy-access-denied"]


@pytest.mark.parametrize("environment", ["local-dev", "qa"])
def test_gateway_route_rejects_go_policy_upstream(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, environment: str
) -> None:
    # given
    for template in (policy.GATEWAY_PROFILE, policy.GATEWAY_OVERRIDE):
        relative = template.format(environment=environment)
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(policy.ROOT / relative, target)
    monkeypatch.setattr(policy, "ROOT", tmp_path)
    policy.check_gateway_route(environment)
    override = tmp_path / policy.GATEWAY_OVERRIDE.format(environment=environment)
    document = yaml.safe_load(override.read_text())
    document["micronaut"]["http"]["services"]["policy-service"] = {
        "urls": [f"http://{environment}-policy-api:8080"]
    }
    override.write_text(yaml.safe_dump(document))

    # when / then
    with pytest.raises(DeploymentError, match="gateway Policy upstream"):
        policy.check_gateway_route(environment)


@pytest.mark.parametrize("go_endpoint", [False, True])
def test_java_route_selects_only_ready_java_pods(
    monkeypatch: pytest.MonkeyPatch, go_endpoint: bool
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "qa-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    target = policy.Target()
    endpoints = [{"conditions": {"ready": True}, "targetRef": {"kind": "Pod", "name": "java-0"}}]
    if go_endpoint:
        endpoints.append(
            {"conditions": {"ready": True}, "targetRef": {"kind": "Pod", "name": "go-0"}}
        )
    labels = {"java-0": policy.JAVA_SELECTOR, "go-0": policy.SELECTOR}

    def read(*args: str, **_kwargs: object) -> dict:
        if args[:2] == ("get", "service"):
            assert args[2] == "qa-policy-api-legacy"
            return {"spec": {"selector": {**policy.JAVA_SELECTOR, "environment": "qa"}}}
        if args[:2] == ("get", "endpointslices"):
            assert args[2:] == ("-l", "kubernetes.io/service-name=qa-policy-api-legacy")
            return {"items": [{"endpoints": endpoints}]}
        return {"metadata": {"labels": labels[args[2]]}}

    monkeypatch.setattr(target, "json", read)
    go_pod = {"metadata": {"name": "go-0"}}

    # when / then
    if go_endpoint:
        with pytest.raises(DeploymentError, match="only ready Java Policy Pods"):
            policy.verify_java_route(target, go_pod)
    else:
        policy.verify_java_route(target, go_pod)


@pytest.mark.parametrize(
    "output",
    [
        "PASS\n",
        json.dumps({"Action": "pass", "Test": policy.DIRECT_RPC_SMOKE}),
        "\n".join(
            json.dumps({"Action": action, "Test": "TestRenamedDirectRPC"})
            for action in ("run", "pass")
        ),
    ],
    ids=["no-json", "missing-run", "renamed"],
)
def test_direct_rpc_smoke_requires_named_test_to_pass(output: str) -> None:
    with pytest.raises(DeploymentError, match="did not execute and pass exactly once"):
        policy.validate_direct_rpc_output(output)


@pytest.mark.parametrize("error, status", [(KeyboardInterrupt, 130), (policy.Terminated, 143)])
def test_cli_signal_exit_status(
    monkeypatch: pytest.MonkeyPatch, error: type[KeyboardInterrupt], status: int
) -> None:
    # given
    def interrupted() -> int:
        raise error

    monkeypatch.setattr(policy, "main", interrupted)

    # when / then
    assert policy.cli() == status


def test_stop_uses_remaining_poll_budget(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    target = policy.Target()
    time_values = iter((100.0, 185.0))
    monkeypatch.setattr(policy.time, "monotonic", lambda: next(time_values))
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
    policy.stop(target, {})

    # then
    assert scale_timeouts == [65]
    assert poll_timeouts == [5]


@pytest.mark.integration
def test_policy_in_owned_disposable_kind_namespace(monkeypatch: pytest.MonkeyPatch) -> None:
    # given

    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    target = policy.Target()
    require = policy.require
    require(
        target.configuration["kindCluster"] == "local-dev-insurance-hub"
        and target.context == "kind-local-dev-insurance-hub",
        "unexpected disposable cluster",
    )
    image = os.environ.get("DEPLOY_IMAGE_REF", "")
    policy.image_id(target, image)
    require(
        target.configuration["kindCluster"] in policy.run(["kind", "get", "clusters"]).split(),
        "configured Kind cluster is missing",
    )
    _, items, selected = target.rendered()
    require(selected == image, "disposable image differs")
    token = uuid.uuid4().hex[:12]
    target.namespace = f"policy-smoke-{token}"
    namespace = {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {"name": target.namespace, "labels": {SMOKE_LABEL: token}},
    }
    for item in items.values():
        item["metadata"]["namespace"] = target.namespace

    try:
        # when
        target.kubectl("create", "-f", "-", input_text=json.dumps(namespace))
        policy.run(
            ["kind", "load", "docker-image", image, "--name", target.configuration["kindCluster"]],
            timeout=120,
        )
        apply(target, yaml.safe_dump_all(items.values()), "insurance-hub-policy-smoke")
        policy.rollout(target)

        # then
        monkeypatch.setenv("DEPLOY_DENIAL_PROBE", "1")
        policy.verify(target, items)
        policy.stop(target, items)
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
