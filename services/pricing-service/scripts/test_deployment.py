"""Pricing render, release, target, and disposable deployment checks."""

from __future__ import annotations

import json
import os
import shutil
import tempfile
import uuid
from pathlib import Path

import pytest
import yaml
from scripts.go.service import release
from scripts.go.service.deployment import DeploymentError, apply

import deployment as pricing

SMOKE_LABEL = "insurance-hub.io/pricing-disposable"


def test_wrong_target_is_rejected_before_access(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "qa")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "qa-svc")
    monkeypatch.setattr(pricing, "run", lambda *_args, **_kwargs: pytest.fail("unexpected access"))

    # when / then
    with pytest.raises(DeploymentError, match="DEPLOY_CONTEXT"):
        pricing.Target()


def test_disposable_workload_namespace_does_not_redirect_resource_inspection(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    monkeypatch.setenv("DEPLOY_DATA_NS", "local-dev-all")
    target = pricing.Target()

    # when
    target.namespace = "pricing-smoke-example"

    # then
    assert pricing.resource_environment(target)["PRICING_RESOURCE_SVC_NS"] == "local-dev-all"


@pytest.mark.parametrize(
    "defect",
    ["legacy selector", "public service", "broad Policy ingress", "exposed management"],
)
def test_render_rejects_access_or_selector_defects(defect: str) -> None:
    # given
    items = pricing.validate_render("qa", pricing.render("qa"))
    if defect == "legacy selector":
        items["Deployment"]["spec"]["selector"]["matchLabels"] = {
            "app.kubernetes.io/name": "pricing-api-legacy",
            "app.kubernetes.io/component": "api",
        }
    elif defect == "public service":
        items["Service"]["spec"]["type"] = "LoadBalancer"
    elif defect == "broad Policy ingress":
        items["NetworkPolicy"]["spec"]["ingress"] = [{}]
    else:
        items["Service"]["spec"]["ports"].append(
            {
                "name": "management",
                "port": 8081,
                "targetPort": "management",
                "protocol": "TCP",
            }
        )

    # when / then
    with pytest.raises(DeploymentError):
        pricing.validate_render("qa", yaml.safe_dump_all(items.values()))


def test_render_exposes_java_http_and_go_policy_grpc() -> None:
    # when
    items = pricing.validate_render("qa", pricing.render("qa"))

    # then
    assert items["ConfigMap"]["data"]["HTTP_ADDR"] == "0.0.0.0:8080"
    assert items["ConfigMap"]["data"]["GRPC_ADDR"] == "0.0.0.0:9090"
    assert items["Service"]["spec"] == {
        "type": "ClusterIP",
        "selector": pricing.SELECTOR,
        "ports": [
            {"name": "http", "port": 8080, "targetPort": "http", "protocol": "TCP"},
            {"name": "grpc", "port": 9090, "targetPort": "grpc", "protocol": "TCP"},
        ],
    }
    assert items["NetworkPolicy"]["spec"]["ingress"] == [
        {
            "from": [{"podSelector": {"matchLabels": pricing.LEGACY_POLICY_SELECTOR}}],
            "ports": [{"port": 8080, "protocol": "TCP"}],
        },
        {
            "from": [{"podSelector": {"matchLabels": pricing.GO_POLICY_SELECTOR}}],
            "ports": [{"port": 9090, "protocol": "TCP"}],
        },
    ]


def test_live_service_rejects_external_exposure() -> None:
    # given
    desired = pricing.validate_render("qa", pricing.render("qa"))["Service"]["spec"]
    exposed = {**desired, "clusterIP": "10.96.0.10", "externalIPs": ["192.0.2.10"]}
    private = {**desired, "clusterIP": "10.96.0.10"}

    # when
    exposed_matches = pricing.live_service_matches(exposed, desired)
    private_matches = pricing.live_service_matches(private, desired)

    # then
    assert not exposed_matches
    assert private_matches


@pytest.mark.parametrize(
    ("kind", "item"),
    [
        (
            "ingresses.networking.k8s.io",
            {
                "metadata": {"namespace": "qa-svc"},
                "spec": {
                    "rules": [
                        {"http": {"paths": [{"backend": {"service": {"name": "qa-pricing-api"}}}]}}
                    ]
                },
            },
        ),
        (
            "httproutes.gateway.networking.k8s.io",
            {
                "metadata": {"namespace": "qa-svc"},
                "spec": {"rules": [{"backendRefs": [{"name": "qa-pricing-api"}]}]},
            },
        ),
        (
            "grpcroutes.gateway.networking.k8s.io",
            {
                "metadata": {"namespace": "qa-svc"},
                "spec": {"rules": [{"backendRefs": [{"name": "qa-pricing-api"}]}]},
            },
        ),
    ],
)
def test_public_route_reference_is_exact(kind: str, item: dict) -> None:
    assert pricing.route_references_service(kind, item, "qa-pricing-api", "qa-svc")
    assert not pricing.route_references_service(kind, item, "qa-pricing-api-shadow", "qa-svc")
    assert not pricing.route_references_service(kind, item, "qa-pricing-api", "other-svc")


def test_http_route_reference_honors_explicit_backend_namespace() -> None:
    # given
    route = {
        "metadata": {"namespace": "gateway"},
        "spec": {"rules": [{"backendRefs": [{"name": "qa-pricing-api", "namespace": "qa-svc"}]}]},
    }

    # when
    referenced = pricing.route_references_service(
        "httproutes.gateway.networking.k8s.io", route, "qa-pricing-api", "qa-svc"
    )

    # then
    assert referenced


def test_effective_route_scan_rejects_pricing_backend(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    target = pricing.Target.__new__(pricing.Target)
    target.context = "qa-insurance-hub"
    target.namespace = "qa-svc"
    target.deployment = "qa-pricing-api"
    target.kubectl_binary = "kubectl"
    target.template_labels = pricing.SELECTOR
    target.json = lambda *_args, **_kwargs: {
        "items": [
            {
                "metadata": {"name": target.deployment},
                "spec": {"selector": pricing.SELECTOR},
            }
        ]
    }

    def run(argv: list[str], **_kwargs: object) -> str:
        if "api-resources" in argv:
            return "httproutes.gateway.networking.k8s.io\n"
        return json.dumps(
            {
                "items": [
                    {
                        "metadata": {"namespace": target.namespace},
                        "spec": {"rules": [{"backendRefs": [{"name": target.deployment}]}]},
                    }
                ]
            }
        )

    monkeypatch.setattr(pricing, "run", run)

    # when / then
    with pytest.raises(DeploymentError, match="public route references Pricing"):
        pricing.check_requested_route(target)


def test_local_egress_rejects_broad_dns() -> None:
    # given
    items = pricing.validate_render("local-dev", pricing.render("local-dev"))
    dns = items["NetworkPolicy"]["spec"]["egress"][0]
    del dns["to"][0]["podSelector"]

    # when / then
    with pytest.raises(DeploymentError, match="NetworkPolicy"):
        pricing.validate_render("local-dev", yaml.safe_dump_all(items.values()))


def test_qa_render_rejects_placeholder_release_identity() -> None:
    # given
    items = pricing.validate_render("qa", pricing.render("qa"))
    deployment = items["Deployment"]
    template = deployment["spec"]["template"]
    items["ConfigMap"]["data"]["SERVICE_VERSION"] = pricing.PLACEHOLDER
    for metadata in (deployment["metadata"], template["metadata"]):
        metadata["labels"][pricing.VERSION_LABEL] = pricing.PLACEHOLDER
    template["spec"]["containers"][0]["image"] = (
        "ghcr.io/igor-baiborodine/insurance-hub/pricing-api:0.0.0-unpublished"
    )

    # when / then
    with pytest.raises(DeploymentError, match="QA image and release version"):
        pricing.validate_render("qa", yaml.safe_dump_all(items.values()))


@pytest.mark.parametrize(
    ("resource", "error"),
    [
        ("./pricing/legacy", "nested Pricing resources"),
        ("./pricing", "shared QA ownership must not activate Pricing Go"),
    ],
    ids=["legacy", "go"],
)
def test_qa_handoff_keeps_pricing_out_of_shared_owner_and_suspends_old_initialization(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, resource: str, error: str
) -> None:
    # given
    for relative in (
        "k8s/overlays/qa/svc/kustomization.yaml",
        "k8s/flux/qa/kustomization.yaml",
        "k8s/flux/qa/releases/pricing/release.yaml",
    ):
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(pricing.ROOT / relative, target)
    monkeypatch.setattr(pricing, "ROOT", tmp_path)

    # when
    pricing.qa_release_ownership()

    # then
    shared = yaml.safe_load((tmp_path / "k8s/overlays/qa/svc/kustomization.yaml").read_text())
    shared["resources"].append(resource)
    (tmp_path / "k8s/overlays/qa/svc/kustomization.yaml").write_text(yaml.safe_dump(shared))
    with pytest.raises(DeploymentError, match=error):
        pricing.qa_release_ownership()


def _released_bundle(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> list[dict]:
    for relative in ("k8s/overlays/qa/svc/kustomization.yaml", "k8s/flux/qa/kustomization.yaml"):
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(pricing.ROOT / relative, target)
    monkeypatch.setattr(pricing, "ROOT", tmp_path)
    return [
        {"kind": "GitRepository", "metadata": {"name": "old-source"}},
        {
            "kind": "Kustomization",
            "metadata": {
                "name": "old-init",
                "annotations": {"insurance-hub.io/staged-predecessor": "true"},
            },
            "spec": {
                "suspend": True,
                "path": "./k8s/overlays/qa/svc/pricing/administration",
                "sourceRef": {"kind": "GitRepository", "name": "old-source"},
            },
        },
        {"kind": "GitRepository", "metadata": {"name": "new-source"}},
        {
            "kind": "Kustomization",
            "metadata": {"name": "new-init"},
            "spec": {
                "path": "./k8s/overlays/qa/svc/pricing/administration",
                "sourceRef": {"kind": "GitRepository", "name": "new-source"},
            },
        },
        {
            "kind": "Kustomization",
            "metadata": {"name": "qa-pricing-api"},
            "spec": {
                "path": "./k8s/overlays/qa/svc/pricing",
                "sourceRef": {"kind": "GitRepository", "name": "new-source"},
                "dependsOn": [{"name": "new-init"}],
            },
        },
    ]


def _staged_only(bundle: list[dict]) -> None:
    del bundle[2:4]
    bundle[-1]["spec"]["sourceRef"]["name"] = "old-source"
    del bundle[-1]["spec"]["dependsOn"]


@pytest.mark.parametrize(
    ("mutate", "error"),
    [
        (lambda bundle: None, None),
        (_staged_only, None),
        (lambda bundle: bundle[1]["spec"].pop("suspend"), "predecessor must not run"),
        (
            lambda bundle: bundle[-1]["spec"].update(dependsOn=[{"name": "old-init"}]),
            "own release initialization",
        ),
        (lambda bundle: bundle[-1]["spec"].pop("dependsOn"), "retain the old API"),
    ],
    ids=["released", "staged", "predecessor-active", "old-prerequisite", "no-prerequisite"],
)
def test_qa_release_bundle_requires_api_to_follow_its_own_initialization(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, mutate, error: str | None
) -> None:
    # given
    bundle = _released_bundle(tmp_path, monkeypatch)
    mutate(bundle)
    path = tmp_path / "k8s/flux/qa/releases/pricing/release.yaml"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(yaml.safe_dump_all(bundle))

    # when / then
    if error is None:
        pricing.qa_release_ownership()
    else:
        with pytest.raises(DeploymentError, match=error):
            pricing.qa_release_ownership()


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
    monkeypatch.setenv("DEPLOY_DATA_NS", "qa-data")
    monkeypatch.setenv("DEPLOY_IMAGE_SOURCE_REVISION", revision)
    monkeypatch.setenv("DEPLOY_IMAGE_DIGEST", digest)
    target = pricing.Target()
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

    monkeypatch.setattr(pricing, "run", inspect)

    # when / then
    if revision_matches:
        assert pricing.image_id(target, f"{registry}:1.0.0") == digest
    else:
        with pytest.raises(DeploymentError, match="OCI labels"):
            pricing.image_id(target, f"{registry}:1.0.0")


@pytest.mark.parametrize(
    ("mode", "image", "running", "accepted"),
    [
        (
            "flux",
            "registry/pricing-api@sha256:" + "d" * 64,
            "registry/pricing-api@sha256:" + "d" * 64,
            True,
        ),
        ("flux", "registry/pricing-api:1.1.0", "sha256:" + "d" * 64, True),
        (
            "flux",
            "registry/pricing-api@sha256:" + "d" * 64,
            "registry/pricing-api@sha256:" + "e" * 64,
            False,
        ),
        ("direct", "pricing-api:1.0.0-local.aaaaaaaaaaaa", "sha256:" + "d" * 64, True),
        ("direct", "pricing-api:1.0.0-local.aaaaaaaaaaaa", "sha256:" + "e" * 64, False),
    ],
    ids=["qa-digest-ref", "qa-bare-digest", "qa-other-digest", "local-id", "local-other-id"],
)
def test_running_image_matches_published_digest_or_local_id(
    mode: str, image: str, running: str, accepted: bool
) -> None:
    # given
    digest = "sha256:" + "d" * 64

    # when / then
    assert pricing.running_image_matches(mode, image, digest, running) is accepted


def test_resource_snapshot_rejects_secret_bearing_or_unknown_output(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    safe = {
        "roles": [],
        "memberships": [],
        "target": None,
        "databaseCompatible": False,
        "credentials": {
            "go_pricing_reader": "missing",
            "go_pricing_migrator": "missing",
            "go_pricing_seeder": "missing",
        },
        "markerMatchesEnrollment": False,
    }
    monkeypatch.setattr(pricing, "resource_action", lambda *_args: json.dumps(safe))

    # when
    assert pricing.resource_snapshot(object()) == safe

    # then
    monkeypatch.setattr(
        pricing,
        "resource_action",
        lambda *_args: json.dumps({**safe, "database-url": "synthetic-secret"}),
    )
    with pytest.raises(DeploymentError, match="unexpected state"):
        pricing.resource_snapshot(object())


def test_release_prepares_initialization_before_stable_api(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    source = pricing.ROOT
    for relative in ("k8s/apps", "k8s/overlays/qa", "k8s/flux/qa/releases/pricing"):
        shutil.copytree(source / relative, tmp_path / relative)
    for relative in (
        "scripts/go/service/catalog.json",
        "services/pricing-service/Dockerfile",
        "services/pricing-service/internal/administration/migrations/00001_pricing.sql",
        "services/pricing-service/internal/administration/seeds/base.json",
    ):
        target = tmp_path / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / relative, target)
    repository = "igor-baiborodine/insurance-hub"
    sha = "0123456789ab" + "c" * 28
    bundle = tmp_path / "k8s/flux/qa/releases/pricing/release.yaml"
    before = bundle.read_bytes()
    selection = release.select_service(
        ["services/pricing-service/Dockerfile"], tmp_path, repository
    )
    assert selection.service == "pricing"
    with pytest.raises(release.ReleaseSelectionError, match="digest"):
        release.prepare_release(tmp_path, "pricing", repository, "1.0.0", sha, "unpublished")
    assert bundle.read_bytes() == before

    # when
    paths = release.prepare_release(
        tmp_path, "pricing", repository, "1.0.0", sha, "sha256:" + "d" * 64
    )

    # then
    assert paths == [bundle]
    # Earlier released prerequisites are retained ahead of the new source, init and API owner.
    *retained, source_owner, initialize, api = list(yaml.safe_load_all(bundle.read_text()))
    staged = [
        item
        for item in retained
        if item["metadata"]["annotations"].get("insurance-hub.io/staged-predecessor") == "true"
    ]
    assert {item["kind"] for item in staged} == {"GitRepository", "Kustomization"}
    assert all(
        item["spec"]["suspend"] is True for item in staged if item["kind"] != "GitRepository"
    )
    assert source_owner["spec"]["ref"] == {"commit": sha}
    assert initialize["spec"]["path"].endswith("/pricing/administration")
    assert initialize["spec"]["postBuild"]["substitute"] == {"MIGRATION_TIMEOUT": "120s"}
    assert api["spec"]["dependsOn"] == [{"name": initialize["metadata"]["name"]}]
    assert api["spec"]["postBuild"]["substitute"] == {"PRICING_SERVICE_VERSION": "git-0123456789ab"}
    assert api["spec"]["images"][0]["digest"] == "sha256:" + "d" * 64
    monkeypatch.setattr(pricing, "ROOT", tmp_path)
    rendered = pricing.render("qa").replace("${PRICING_SERVICE_VERSION}", "git-0123456789ab")
    items = pricing.validate_render("qa", rendered)
    assert items["ConfigMap"]["data"]["SERVICE_VERSION"] == "git-0123456789ab"


def test_denial_cleanup_preserves_primary_error(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    monkeypatch.setenv("DEPLOY_DATA_NS", "local-dev-all")
    target = pricing.Target()
    reads = iter(({}, {"metadata": {"labels": {}}}))

    def create(*_args: str, **_kwargs: object) -> str:
        raise DeploymentError("probe creation failed")

    monkeypatch.setattr(target, "json", lambda *_args: next(reads))
    monkeypatch.setattr(target, "kubectl", create)

    # when / then
    with pytest.raises(DeploymentError, match="probe creation failed"):
        pricing.verify_denial(target, {"status": {"podIP": "192.0.2.10"}})
    assert "cleanup failed: Pricing denial probe ownership differs" in capsys.readouterr().err


def test_policy_probe_allows_only_service_http() -> None:
    # given
    target = pricing.Target.__new__(pricing.Target)
    target.namespace = "qa-svc"
    target.deployment = "qa-pricing-api"
    created: dict = {}
    deleted: list[str] = []

    def inspect(*_args: str, **_kwargs: object) -> dict:
        if not created:
            return {}
        return created

    def kubectl(*args: str, input_text: str | None = None, **_kwargs: object) -> str:
        if args[0] == "create":
            created.update(json.loads(input_text or "{}"))
            created["status"] = {"phase": "Succeeded"}
        elif args[0] == "logs":
            return "\n".join(
                (
                    "Pricing Policy HTTP access passed",
                    "Pricing Policy port 9090 denied",
                    "Pricing Policy port 8081 denied",
                )
            )
        elif args[0] == "delete":
            deleted.append(args[2])
        return ""

    target.json = inspect
    target.kubectl = kubectl

    # when
    pricing.verify_policy_access(target, {"status": {"podIP": "192.0.2.10"}})

    # then
    labels = created["metadata"]["labels"]
    assert pricing.LEGACY_POLICY_SELECTOR.items() <= labels.items()
    assert created["spec"]["containers"][0]["args"][0].startswith(
        f"sleep {pricing.NETWORK_POLICY_CONVERGENCE_SECONDS}; "
    )
    assert "Established connection" in created["spec"]["containers"][0]["args"][0]
    assert created["spec"]["containers"][0]["args"][-2:] == ["9090", "8081"]
    assert deleted == ["pricing-policy-access"]


def test_go_policy_probe_allows_only_service_grpc() -> None:
    # given
    target = pricing.Target.__new__(pricing.Target)
    target.namespace = "qa-svc"
    target.deployment = "qa-pricing-api"
    created: dict = {}
    deleted: list[str] = []

    def inspect(*_args: str, **_kwargs: object) -> dict:
        if not created:
            return {}
        return created

    def kubectl(*args: str, input_text: str | None = None, **_kwargs: object) -> str:
        if args[0] == "create":
            created.update(json.loads(input_text or "{}"))
            created["status"] = {"phase": "Succeeded"}
        elif args[0] == "logs":
            return "\n".join(
                (
                    "Pricing Go Policy gRPC access passed",
                    "Pricing Go Policy qa-pricing-api:8080 denied",
                    "Pricing Go Policy 192.0.2.10:8081 denied",
                )
            )
        elif args[0] == "delete":
            deleted.append(args[2])
        return ""

    target.json = inspect
    target.kubectl = kubectl

    # when
    pricing.verify_go_policy_access(target, {"status": {"podIP": "192.0.2.10"}})

    # then
    labels = created["metadata"]["labels"]
    assert pricing.GO_POLICY_SELECTOR.items() <= labels.items()
    assert "Established connection" in created["spec"]["containers"][0]["args"][0]
    assert created["spec"]["containers"][0]["args"][-2:] == [
        "qa-pricing-api",
        "192.0.2.10",
    ]
    assert deleted == ["pricing-go-policy-access"]


@pytest.mark.parametrize("error, status", [(KeyboardInterrupt, 130), (pricing.Terminated, 143)])
def test_cli_signal_exit_status(
    monkeypatch: pytest.MonkeyPatch, error: type[KeyboardInterrupt], status: int
) -> None:
    # given
    def interrupted() -> int:
        raise error

    monkeypatch.setattr(pricing, "main", interrupted)

    # when / then
    assert pricing.cli() == status


def test_stop_uses_remaining_poll_budget(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    monkeypatch.setenv("DEPLOY_DATA_NS", "local-dev-all")
    target = pricing.Target()
    time_values = iter((100.0, 185.0))
    monkeypatch.setattr(pricing.time, "monotonic", lambda: next(time_values))
    monkeypatch.setattr(target, "live", lambda *_args, **_kwargs: {})
    monkeypatch.setattr(pricing, "resource_snapshot", lambda *_args: {"safe": True})
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
    pricing.stop(target, {})

    # then
    assert scale_timeouts == [65]
    assert poll_timeouts == [5]


def test_business_http_smoke_uses_tri_and_representative_rejection(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    requests: list[tuple[int, str, bytes | None]] = []
    responses = iter(
        (
            (200, "application/json", b'{"totalPrice":78,"coversPrices":{"C1":26,"C2":52}}'),
            (
                400,
                "application/json; charset=utf-8",
                b'{"code":3,"message":"invalid request","details":[]}',
            ),
        )
    )

    def respond(port: int, path: str, payload: bytes | None = None) -> tuple[int, str, bytes]:
        requests.append((port, path, payload))
        return next(responses)

    monkeypatch.setattr(pricing, "response", respond)

    # when
    pricing.verify_business_http(18080)

    # then
    assert requests == [
        (18080, "/pricing/calculate", pricing.TRI_HTTP_REQUEST),
        (18080, "/pricing/calculate", pricing.INVALID_DATE_HTTP_REQUEST),
    ]


@pytest.mark.parametrize(
    ("status", "content_type", "body"),
    [
        (200, "application/json", b'{"message":"hello"}'),
        (200, "application/json", b'{"totalPrice":79,"coversPrices":{"C1":26,"C2":52}}'),
        (200, "application/json", b'{"totalPrice":"78","coversPrices":{"C1":26,"C2":52}}'),
        (200, "application/json", b'{"totalPrice":78.00,"coversPrices":{"C1":26.00,"C2":52.00}}'),
    ],
    ids=["stale-echo", "wrong-total", "quoted-total", "scaled-total"],
)
def test_business_http_smoke_rejects_incompatible_quote(
    monkeypatch: pytest.MonkeyPatch, status: int, content_type: str, body: bytes
) -> None:
    # given
    monkeypatch.setattr(
        pricing,
        "response",
        lambda *_args, **_kwargs: (status, content_type, body),
    )

    # when / then
    with pytest.raises(DeploymentError, match="Pricing HTTP TRI quote differs"):
        pricing.verify_business_http(18080)


def test_direct_rpc_smoke_requires_named_test_selection(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    commands: list[tuple[list[str], float]] = []

    def execute(argv: list[str], *, timeout: float) -> str:
        commands.append((argv, timeout))
        return "\n".join(
            (
                json.dumps({"Action": "run", "Test": pricing.DIRECT_RPC_SMOKE}),
                json.dumps({"Action": "pass", "Test": pricing.DIRECT_RPC_SMOKE}),
            )
        )

    monkeypatch.setattr(pricing, "run", execute)

    # when
    pricing.verify_direct_rpc("services/pricing-service", 19090)

    # then
    assert commands == [
        (
            [
                os.environ.get("MAKE_COMMAND", "make"),
                "--no-print-directory",
                "-C",
                str(pricing.ROOT / "services/pricing-service"),
                "_deploy-smoke-rpc",
                f"GO={os.environ.get('GO', 'go')}",
                "PRICING_SMOKE_GRPC_ADDR=127.0.0.1:19090",
            ],
            120,
        )
    ]


def test_direct_rpc_smoke_rejects_missing_named_test(monkeypatch: pytest.MonkeyPatch) -> None:
    # given
    monkeypatch.setattr(pricing, "run", lambda *_args, **_kwargs: "PASS\n")

    # when / then
    with pytest.raises(DeploymentError, match="did not execute and pass exactly once"):
        pricing.verify_direct_rpc("services/pricing-service", 19090)


@pytest.mark.parametrize(
    "output",
    [
        "",
        json.dumps({"Action": "pass", "Test": pricing.DIRECT_RPC_SMOKE}),
        "\n".join(
            (
                json.dumps({"Action": "run", "Test": pricing.DIRECT_RPC_SMOKE}),
                json.dumps({"Action": "fail", "Test": pricing.DIRECT_RPC_SMOKE}),
            )
        ),
        "\n".join(
            (
                json.dumps({"Action": "run", "Test": pricing.DIRECT_RPC_SMOKE}),
                json.dumps({"Action": "pass", "Test": pricing.DIRECT_RPC_SMOKE}),
                json.dumps({"Action": "pass", "Test": pricing.DIRECT_RPC_SMOKE}),
            )
        ),
        "\n".join(
            (
                json.dumps({"Action": "run", "Test": "TestRenamedDirectRPC"}),
                json.dumps({"Action": "pass", "Test": "TestRenamedDirectRPC"}),
            )
        ),
    ],
    ids=["empty", "missing-run", "failed", "duplicate-pass", "renamed"],
)
def test_direct_rpc_output_rejects_missing_renamed_failed_or_duplicate_results(
    output: str,
) -> None:
    with pytest.raises(DeploymentError, match="did not execute and pass exactly once"):
        pricing.validate_direct_rpc_output(output)


@pytest.mark.parametrize(
    ("output", "message"),
    [
        ("direct Pricing quote differs", "direct RPC TRI quote differs"),
        ("direct Pricing rejection differs", "direct RPC invalid-date rejection differs"),
        ("unclassified safe output", "direct RPC smoke failed"),
    ],
)
def test_direct_rpc_smoke_classifies_failure_without_exposing_output(
    monkeypatch: pytest.MonkeyPatch, output: str, message: str
) -> None:
    # given
    def fail(*_args, **_kwargs) -> str:
        raise pricing.CommandError("controlled failure", stdout=output)

    monkeypatch.setattr(pricing, "run", fail)

    # when / then
    with pytest.raises(DeploymentError, match=message) as caught:
        pricing.verify_direct_rpc("services/pricing-service", 19090)
    assert output not in str(caught.value)


@pytest.mark.integration
def test_pricing_in_owned_disposable_kind_namespace(monkeypatch: pytest.MonkeyPatch) -> None:
    # given

    monkeypatch.setenv("DEPLOY_ENV", "local-dev")
    monkeypatch.setenv("DEPLOY_CONTEXT", "kind-local-dev-insurance-hub")
    monkeypatch.setenv("DEPLOY_SVC_NS", "local-dev-all")
    monkeypatch.setenv("DEPLOY_DATA_NS", "local-dev-all")
    target = pricing.Target()
    require = pricing.require
    require(
        target.configuration["kindCluster"] == "local-dev-insurance-hub"
        and target.context == "kind-local-dev-insurance-hub",
        "unexpected disposable cluster",
    )
    image = os.environ.get("DEPLOY_IMAGE_REF", "")
    pricing.image_id(target, image)
    require(
        target.configuration["kindCluster"] in pricing.run(["kind", "get", "clusters"]).split(),
        "configured Kind cluster is missing",
    )
    _, items, selected = target.rendered()
    require(selected == image, "disposable image differs")
    source_secret = target.json("get", "secret", target.secret)
    secret_data = source_secret.get("data")
    require(
        source_secret.get("type") == "Opaque"
        and isinstance(secret_data, dict)
        and set(secret_data) == {"database-url", "ca.crt"},
        "disposable smoke requires the exact Pricing reader Secret",
    )
    token = uuid.uuid4().hex[:12]
    target.namespace = f"pricing-smoke-{token}"
    namespace = {
        "apiVersion": "v1",
        "kind": "Namespace",
        "metadata": {"name": target.namespace, "labels": {SMOKE_LABEL: token}},
    }
    reader_secret = {
        "apiVersion": "v1",
        "kind": "Secret",
        "metadata": {"name": target.secret, "namespace": target.namespace},
        "type": "Opaque",
        "data": secret_data,
    }
    for item in items.values():
        item["metadata"]["namespace"] = target.namespace

    with tempfile.TemporaryDirectory(prefix="pricing-smoke-kubeconfig-") as directory:
        kubeconfig = Path(directory) / "config"
        document = pricing.run(
            [
                target.kubectl_binary,
                "config",
                "view",
                "--raw",
                "--flatten",
                "--minify",
                "--context",
                target.context,
            ]
        )
        descriptor = os.open(kubeconfig, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            stream.write(document)
        monkeypatch.setenv("KUBECONFIG", str(kubeconfig))

        try:
            # when
            target.kubectl("create", "-f", "-", input_text=json.dumps(namespace))
            target.kubectl("create", "-f", "-", input_text=json.dumps(reader_secret))
            pricing.run(
                [
                    "kind",
                    "load",
                    "docker-image",
                    image,
                    "--name",
                    target.configuration["kindCluster"],
                ],
                timeout=120,
            )
            apply(target, yaml.safe_dump_all(items.values()), "insurance-hub-pricing-smoke")
            pricing.rollout(target)

            # then
            monkeypatch.setenv("DEPLOY_DENIAL_PROBE", "1")
            pricing.verify(target, items)
            pricing.stop(target, items)
            assert target.json("get", "deployment", target.deployment)["spec"]["replicas"] == 0
        finally:
            actual = target.json("get", "namespace", target.namespace, "--ignore-not-found")
            if actual:
                require(
                    actual["metadata"]["labels"].get(SMOKE_LABEL) == token,
                    "disposable namespace ownership differs",
                )
                target.kubectl(
                    "delete", "namespace", target.namespace, "--timeout=100s", timeout=110
                )
                assert not target.json("get", "namespace", target.namespace, "--ignore-not-found")
                print(f"Removed disposable namespace {target.namespace}")
