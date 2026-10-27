#!/usr/bin/env python3
"""Resolve a cataloged Go service capability to its module directory."""

from __future__ import annotations

import json
import os
import re
import sys
from pathlib import Path, PurePosixPath
from typing import Any

NAME = re.compile(r"[a-z][a-z0-9-]*")


class DispatchError(RuntimeError):
    """The service dispatch request or catalog is invalid."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise DispatchError(message)


def relative_path(value: object, field: str) -> str:
    require(isinstance(value, str) and bool(value), f"{field} must be a non-empty string")
    path = PurePosixPath(value)
    require(not path.is_absolute() and ".." not in path.parts, f"{field} must be relative")
    return value


def load_catalog(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise DispatchError("catalog schema or service metadata is invalid") from error
    require(
        isinstance(value, dict) and value.get("schemaVersion") == 1,
        "catalog schema or service metadata is invalid",
    )
    services = value.get("services")
    require(
        isinstance(services, dict) and bool(services),
        "catalog schema or service metadata is invalid",
    )
    for name, record in services.items():
        require(
            isinstance(name, str) and bool(NAME.fullmatch(name)),
            "catalog schema or service metadata is invalid",
        )
        require(isinstance(record, dict), "catalog schema or service metadata is invalid")
        for field in (
            "repositoryRoot",
            "modulePath",
            "localImage",
            "registryImage",
            "kustomizeBase",
        ):
            relative_path(record.get(field), field)
        overlays = record.get("overlays")
        require(isinstance(overlays, dict), "catalog schema or service metadata is invalid")
        environments = record.get("environments")
        require(
            isinstance(environments, list)
            and bool(environments)
            and all(isinstance(item, str) and NAME.fullmatch(item) for item in environments),
            "catalog environments are invalid",
        )
        require(set(overlays) == set(environments), "overlay environments differ")
        for environment in environments:
            relative_path(overlays[environment], f"overlays.{environment}")
        capabilities = record.get("capabilities")
        require(
            isinstance(capabilities, list)
            and bool(capabilities)
            and all(isinstance(item, str) and NAME.fullmatch(item) for item in capabilities),
            "catalog schema or service metadata is invalid",
        )
    return value


def resolve_service(repo_root: Path, catalog_path: Path, service: str, capability: str) -> str:
    require(bool(NAME.fullmatch(capability)), "capability has an invalid format")
    require(bool(service), "SVC_NAME is required")
    require(bool(NAME.fullmatch(service)), "SVC_NAME must match [a-z][a-z0-9-]*")
    catalog = load_catalog(catalog_path)
    record = catalog["services"].get(service)
    require(isinstance(record, dict), "unknown SVC_NAME")
    require(
        capability in record["capabilities"], f"service does not support capability {capability}"
    )

    repository_relative = relative_path(record["repositoryRoot"], "repositoryRoot")
    module_relative = relative_path(record["modulePath"], "modulePath")
    try:
        root = repo_root.resolve(strict=True)
        repository_path = (root / repository_relative).resolve(strict=True)
        module_path = (root / module_relative).resolve(strict=True)
    except OSError as error:
        raise DispatchError("declared repository root or module path does not exist") from error
    require(repository_path.is_relative_to(root), "declared repository root escapes the checkout")
    require(
        module_path.is_relative_to(repository_path),
        "module path escapes its declared repository root",
    )
    require((module_path / "Makefile").is_file(), "selected module does not own a Makefile")
    return module_path.relative_to(root).as_posix()


def main(argv: list[str] | None = None) -> int:
    arguments = sys.argv[1:] if argv is None else argv
    require(len(arguments) == 1, "exactly one capability is required")
    script_dir = Path(__file__).resolve().parent
    repo_root = script_dir.parents[2]
    service = os.environ.get("SVC_NAME", "")
    print(resolve_service(repo_root, script_dir / "catalog.json", service, arguments[0]))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except DispatchError as error:
        print(f"go-service-dispatch: {error}", file=sys.stderr)
        raise SystemExit(1) from None
