"""Pytest scenarios for the Product deployment render checker."""

from __future__ import annotations

import copy
import json
import os
import shutil
from collections.abc import Callable
from pathlib import Path
from typing import Any

import deployment_check
import pytest

import check_render

KEYS = tuple(deployment_check.OVERLAYS)
QA_JAVA_URL = "http://qa-product-api-legacy.qa-svc.svc.cluster.local:80"
QA_GO_URL = "http://qa-product-api.qa-svc.svc.cluster.local:80"
Documents = dict[str, list[dict[str, Any]]]
Mutation = Callable[[Documents], None]


def find(documents: list[dict[str, Any]], kind: str, name: str) -> dict[str, Any]:
    for document in documents:
        if document.get("kind") == kind and document.get("metadata", {}).get("name") == name:
            return document
    raise AssertionError(f"fixture does not contain {kind}/{name}")


def find_prefix(documents: list[dict[str, Any]], kind: str, prefix: str) -> dict[str, Any]:
    matches = [
        document
        for document in documents
        if document.get("kind") == kind
        and document.get("metadata", {}).get("name", "").startswith(prefix)
    ]
    if len(matches) != 1:
        raise AssertionError(f"fixture does not contain exactly one {kind} with prefix {prefix}")
    return matches[0]


def write_documents(
    root: Path, values: Documents, prefix: str, *, release_active: bool = True
) -> list[str]:
    arguments: list[str] = []
    for key in KEYS:
        path = root / f"{prefix}-{key}.json"
        path.write_text(json.dumps(values[key]), encoding="utf-8")
        arguments.extend((f"--{key}", str(path)))
    if release_active:
        arguments.append("--qa-release-active")
    return arguments


def active_documents(baseline: Documents) -> Documents:
    return copy.deepcopy(baseline)


@pytest.fixture(scope="module")
def rendered_documents() -> Documents:
    module_root = Path(__file__).resolve().parent.parent
    kubectl = os.environ.get("KUBECTL") or shutil.which("kubectl")
    if kubectl is None:
        bundled = module_root / ".tools/bin/kubectl"
        assert bundled.is_file(), f"missing Product kubectl prerequisite: {bundled}"
        kubectl = str(bundled)
    return {
        name: deployment_check.render(kubectl, deployment_check.REPOSITORY_ROOT / source)
        for name, source in deployment_check.OVERLAYS.items()
    }


def bad_selector(values: Documents) -> None:
    service = find(values["local-go"], "Service", "local-dev-product-api")
    service["spec"]["selector"]["app.kubernetes.io/name"] = "product-api-legacy"


def wrong_port(values: Documents) -> None:
    deployment = find(values["local-go"], "Deployment", "local-dev-product-api")
    deployment["spec"]["template"]["spec"]["containers"][0]["ports"][0]["containerPort"] = 8082


def wrong_business_service_port(values: Documents) -> None:
    service = find(values["local-go"], "Service", "local-dev-product-api")
    service["spec"]["ports"][0]["port"] = 8081


def wrong_management_service_port(values: Documents) -> None:
    service = find(values["qa-go"], "Service", "qa-product-api-management")
    service["spec"]["ports"][0]["port"] = 8080


def missing_secret(values: Documents) -> None:
    deployment = find(values["local-go"], "Deployment", "local-dev-product-api")
    deployment["spec"]["template"]["spec"]["containers"][0]["env"] = []


def external_exposure(values: Documents) -> None:
    service = find(values["local-go"], "Service", "local-dev-product-api")
    service["spec"]["type"] = "LoadBalancer"


def duplicate_qa_ownership(values: Documents) -> None:
    values["qa-aggregate"].extend(copy.deepcopy(values["qa-go"]))


def stale_java_product(values: Documents) -> None:
    values["qa-aggregate"].extend(copy.deepcopy(values["qa-legacy"]))


def unreleased_qa_tag(values: Documents) -> None:
    deployment = find(values["qa-go"], "Deployment", "qa-product-api")
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = (
        "ghcr.io/igor-baiborodine/insurance-hub/product-api:sha-unpublished"
    )


def invalid_qa_tag(values: Documents) -> None:
    deployment = find(values["qa-go"], "Deployment", "qa-product-api")
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = (
        "ghcr.io/igor-baiborodine/insurance-hub/product-api:latest"
    )


def unversioned_local_tag(values: Documents) -> None:
    deployment = find(values["local-go"], "Deployment", "local-dev-product-api")
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = (
        "insurance-hub-product-api:git-c977bbcd879d"
    )


def disabled_qa_telemetry(values: Documents) -> None:
    config = find_prefix(values["qa-go"], "ConfigMap", "qa-product-config-")
    config["data"]["OTEL_ENABLED"] = "false"


def missing_qa_instance(values: Documents) -> None:
    deployment = find(values["qa-go"], "Deployment", "qa-product-api")
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    container["env"] = [
        item for item in container["env"] if item.get("name") != "SERVICE_INSTANCE_ID"
    ]


def qa_gateway_uses_management_port(values: Documents) -> None:
    config = find_prefix(values["qa-gateway"], "ConfigMap", "qa-gateway-product-route-config-")
    config["data"]["application.yml"] = config["data"]["application.yml"].replace(
        ".svc.cluster.local:80",
        ".svc.cluster.local:8081",
    )


def select_qa_gateway_backend(values: Documents, selected_url: str) -> None:
    for key in ("qa-gateway", "qa-aggregate"):
        config = find_prefix(values[key], "ConfigMap", "qa-gateway-product-route-config-")
        current = config["data"]["application.yml"]
        assert QA_JAVA_URL in current or QA_GO_URL in current
        config["data"]["application.yml"] = current.replace(
            QA_JAVA_URL if QA_JAVA_URL in current else QA_GO_URL, selected_url
        )


def local_gateway_has_second_url(values: Documents) -> None:
    config = find_prefix(
        values["local-gateway"], "ConfigMap", "local-dev-gateway-product-route-config-"
    )
    config["data"]["application.yml"] += (
        "          - http://local-dev-product-api-legacy.local-dev-all.svc.cluster.local:80\n"
    )


def gateway_mount_uses_subpath(values: Documents) -> None:
    deployment = find(values["qa-gateway"], "Deployment", "qa-agent-portal-gateway-legacy")
    deployment["spec"]["template"]["spec"]["containers"][0]["volumeMounts"][0]["subPath"] = (
        "application.yml"
    )


def gateway_references_stale_config(values: Documents) -> None:
    deployment = find(
        values["local-gateway"], "Deployment", "local-dev-agent-portal-gateway-legacy"
    )
    deployment["spec"]["template"]["spec"]["volumes"][0]["configMap"]["name"] = (
        "local-dev-gateway-product-route-config-stale0000"
    )


def qa_aggregate_changes_gateway_route(values: Documents) -> None:
    config = find_prefix(values["qa-aggregate"], "ConfigMap", "qa-gateway-product-route-config-")
    current = config["data"]["application.yml"]
    assert QA_JAVA_URL in current or QA_GO_URL in current
    config["data"]["application.yml"] = current.replace(
        QA_JAVA_URL if QA_JAVA_URL in current else QA_GO_URL,
        QA_GO_URL if QA_JAVA_URL in current else QA_JAVA_URL,
    )


CASES: list[tuple[str, Mutation, str]] = [
    ("bad selector", bad_selector, "selector"),
    ("wrong port", wrong_port, "named container ports"),
    ("wrong business Service port", wrong_business_service_port, "Product contract"),
    ("wrong management Service port", wrong_management_service_port, "Product contract"),
    ("missing Secret reference", missing_secret, "PRODUCT_DATABASE_URL"),
    ("external exposure", external_exposure, "must remain ClusterIP"),
    (
        "duplicate QA Product ownership",
        duplicate_qa_ownership,
        "still contains Go Product after release activation",
    ),
    ("stale Java Product desired state", stale_java_product, "stale Java Product objects"),
    ("unreleased QA image tag", unreleased_qa_tag, "qa image reference is invalid"),
    ("invalid QA image tag", invalid_qa_tag, "qa image reference is invalid"),
    ("unversioned local image tag", unversioned_local_tag, "local-dev image reference is invalid"),
    ("disabled QA telemetry", disabled_qa_telemetry, "qa Product ConfigMap"),
    ("missing QA Pod identity", missing_qa_instance, "PRODUCT_DATABASE_URL"),
    ("QA gateway uses management port", qa_gateway_uses_management_port, "expected Product URL"),
    ("multiple local-dev gateway URLs", local_gateway_has_second_url, "expected Product URL"),
    ("gateway subPath mount", gateway_mount_uses_subpath, "without subPath"),
    (
        "stale gateway ConfigMap mount",
        gateway_references_stale_config,
        "generated Product ConfigMap",
    ),
    (
        "QA aggregate changes gateway route",
        qa_aggregate_changes_gateway_route,
        "QA aggregate changed gateway object",
    ),
]


@pytest.mark.integration
def test_semantic_qa_release_tag_is_accepted(
    rendered_documents: Documents,
    tmp_path: Path,
) -> None:
    # given
    values = active_documents(rendered_documents)
    deployment = find(values["qa-go"], "Deployment", "qa-product-api")
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = (
        "ghcr.io/igor-baiborodine/insurance-hub/product-api:1.2.3"
    )

    # when
    status = check_render.main(write_documents(tmp_path, values, "semantic"))

    # then
    assert status == 0


@pytest.mark.integration
def test_staged_shared_qa_ownership_is_accepted(
    rendered_documents: Documents,
    tmp_path: Path,
) -> None:
    # given
    values = active_documents(rendered_documents)
    values["qa-aggregate"].extend(copy.deepcopy(values["qa-go"]))

    # when
    status = check_render.main(write_documents(tmp_path, values, "staged", release_active=False))

    # then
    assert status == 0


@pytest.mark.integration
def test_staged_shared_qa_owner_requires_product(
    rendered_documents: Documents,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    values = active_documents(rendered_documents)

    # when
    status = check_render.main(
        write_documents(tmp_path, values, "missing-staged", release_active=False)
    )

    # then
    assert status == 1
    assert "does not contain Go Product" in capsys.readouterr().err


@pytest.mark.integration
@pytest.mark.integration
def test_java_fallback_gateway_route_is_rejected(
    rendered_documents: Documents,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    values = active_documents(rendered_documents)
    select_qa_gateway_backend(values, QA_JAVA_URL)

    # when
    status = check_render.main(write_documents(tmp_path, values, "java-fallback"))

    # then
    assert status == 1
    assert "must route to Go Product" in capsys.readouterr().err


@pytest.mark.integration
def test_go_gateway_route_is_accepted(
    rendered_documents: Documents,
    tmp_path: Path,
) -> None:
    # given
    values = active_documents(rendered_documents)
    select_qa_gateway_backend(values, QA_GO_URL)

    # when
    status = check_render.main(write_documents(tmp_path, values, "go-route"))

    # then
    assert status == 0


@pytest.mark.integration
@pytest.mark.parametrize(
    ("description", "mutation", "expected_error"),
    CASES,
    ids=[case[0] for case in CASES],
)
def test_contract_defect_is_rejected(
    rendered_documents: Documents,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    description: str,
    mutation: Mutation,
    expected_error: str,
) -> None:
    # given
    values = active_documents(rendered_documents)
    mutation(values)

    # when
    status = check_render.main(write_documents(tmp_path, values, description.replace(" ", "-")))

    # then
    assert status == 1
    assert expected_error in capsys.readouterr().err


@pytest.mark.parametrize(
    ("document", "expected_error"),
    [
        (
            {"apiVersion": "v1", "kind": "Service", "metadata": None},
            "metadata must be an object",
        ),
        (
            {"apiVersion": "v1", "kind": "Service", "metadata": {}},
            "spec must be an object",
        ),
        (
            {
                "apiVersion": "apps/v1",
                "kind": "Deployment",
                "metadata": {},
                "spec": {"template": []},
            },
            "spec.template must be an object",
        ),
        (
            {
                "apiVersion": "apps/v1",
                "kind": "Deployment",
                "metadata": {},
                "spec": {"template": {"metadata": {}, "spec": {"containers": None}}},
            },
            "spec.template.spec.containers must be an array",
        ),
        (
            {
                "apiVersion": "v1",
                "kind": "Service",
                "metadata": {},
                "spec": {"ports": [None]},
            },
            "spec.ports has a non-object item",
        ),
        (
            {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {}, "data": []},
            "data must be an object",
        ),
    ],
    ids=(
        "null-metadata",
        "missing-spec",
        "list-template",
        "null-containers",
        "malformed-port",
        "list-data",
    ),
)
def test_malformed_nested_render_is_reported_through_cli(
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
    document: dict[str, Any],
    expected_error: str,
) -> None:
    # given
    values: Documents = {key: [] for key in KEYS}
    values["local-go"] = [document]

    # when
    status = check_render.main(write_documents(tmp_path, values, "malformed"))

    # then
    diagnostic = capsys.readouterr().err
    assert status == 1
    assert expected_error in diagnostic
    assert "Traceback" not in diagnostic
