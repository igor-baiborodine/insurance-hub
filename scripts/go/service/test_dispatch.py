"""Behavior tests for shared Go service dispatch."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

import resolve


def service_record(name: str, capability: str = "deploy-preflight") -> dict[str, object]:
    return {
        "repositoryRoot": "services",
        "modulePath": f"services/{name}",
        "localImage": name,
        "registryImage": f"example.org/{name}",
        "kustomizeBase": f"k8s/{name}",
        "overlays": {"local-dev": f"k8s/local/{name}", "qa": f"k8s/qa/{name}"},
        "environments": ["local-dev", "qa"],
        "capabilities": [capability],
    }


@pytest.fixture
def catalog_fixture(tmp_path: Path) -> tuple[Path, Path]:
    # given
    root = tmp_path / "repository"
    for service in ("first", "second"):
        module = root / "services" / service
        module.mkdir(parents=True)
        (module / "Makefile").write_text("deploy-preflight:\n\t@true\n", encoding="utf-8")
    catalog = root / "catalog.json"
    catalog.write_text(
        json.dumps(
            {
                "schemaVersion": 1,
                "services": {
                    "first": service_record("first"),
                    "second": service_record("second"),
                },
            }
        ),
        encoding="utf-8",
    )
    return root, catalog


@pytest.mark.parametrize("service", ["first", "second"])
def test_catalog_metadata_selects_each_service(
    catalog_fixture: tuple[Path, Path], service: str
) -> None:
    # given
    root, catalog = catalog_fixture

    # when
    selected = resolve.resolve_service(root, catalog, service, "deploy-preflight")

    # then
    assert selected == f"services/{service}"


@pytest.mark.parametrize(
    ("service", "capability", "message"),
    [
        ("", "deploy-preflight", "SVC_NAME is required"),
        ("../product", "deploy-preflight", "SVC_NAME must match"),
        ("unknown", "deploy-preflight", "unknown SVC_NAME"),
        ("first", "unsupported-capability", "does not support capability"),
    ],
)
def test_invalid_dispatch_fails_closed(
    catalog_fixture: tuple[Path, Path], service: str, capability: str, message: str
) -> None:
    # given
    root, catalog = catalog_fixture

    # when / then
    with pytest.raises(resolve.DispatchError, match=message):
        resolve.resolve_service(root, catalog, service, capability)


def test_product_supports_every_cataloged_capability() -> None:
    # given
    script_dir = Path(resolve.__file__).resolve().parent
    root = script_dir.parents[2]
    catalog = resolve.load_catalog(script_dir / "catalog.json")
    capabilities = catalog["services"]["product"]["capabilities"]

    # when
    paths = [
        resolve.resolve_service(root, script_dir / "catalog.json", "product", item)
        for item in capabilities
    ]

    # then
    assert paths == ["services/product-service"] * len(capabilities)


@pytest.mark.parametrize("arguments", [[], ["deploy", "status"]])
def test_dispatch_wrong_arity_preserves_failure_contract(arguments: list[str]) -> None:
    # when / then
    with pytest.raises(resolve.DispatchError, match="exactly one capability is required"):
        resolve.main(arguments)
