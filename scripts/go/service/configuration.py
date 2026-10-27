"""Validated service and deployment target metadata shared by release and deployment."""

from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any


class ConfigurationError(ValueError):
    """The selected service or target has invalid configuration."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ConfigurationError(message)


def checkout_path(root: Path, value: str) -> Path:
    require(
        isinstance(value, str) and bool(re.fullmatch(r"[A-Za-z0-9_./-]+", value)),
        "catalog path is invalid",
    )
    path = Path(value)
    require(not path.is_absolute() and ".." not in path.parts, "catalog path must be relative")
    result = root / path
    require(result.resolve().is_relative_to(root.resolve()), "catalog path escapes checkout")
    return result


def service_record(root: Path, service: str) -> dict[str, Any]:
    require(bool(re.fullmatch(r"[a-z][a-z0-9-]*", service)), "service name is invalid")
    data = json.loads((root / "scripts/go/service/catalog.json").read_text())
    require(data.get("schemaVersion") == 1, "unsupported service catalog version")
    record = data.get("services", {}).get(service)
    require(isinstance(record, dict), "service is not registered")
    for field in ("modulePath", "localImage", "registryImage"):
        require(isinstance(record.get(field), str) and bool(record[field]), f"missing {field}")
    checkout_path(root, record["modulePath"])
    for field in ("localImage", "registryImage"):
        require(
            bool(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9./_-]*", record[field])), f"invalid {field}"
        )
    return record


def target_record(root: Path, record: dict[str, Any], name: str) -> dict[str, Any]:
    require(bool(re.fullmatch(r"[a-z][a-z0-9-]*", name)), "target name is invalid")
    target = record.get("deploymentTargets", {}).get(name)
    require(isinstance(target, dict), "deployment target is not registered")
    require(target.get("mode") in {"direct", "flux"}, "unsupported deployment ownership mode")
    for field in ("context", "namespace", "dataNamespace"):
        require(isinstance(target.get(field), str) and bool(target[field]), f"missing {field}")
    checkout_path(root, target.get("overlay", ""))
    initialization = target.get("initialization")
    if initialization is not None:
        require(isinstance(initialization, dict), "invalid initialization configuration")
        checkout_path(root, initialization.get("overlay", ""))
        require(
            bool(re.fullmatch(r"[a-z][a-z0-9-]{0,35}", initialization.get("jobName", ""))),
            "invalid initialization Job name",
        )
        require(
            type(initialization.get("attempt")) is int and 1 <= initialization["attempt"] <= 99,
            "initialization attempt must be between 1 and 99",
        )
        require(
            type(initialization.get("waitSeconds")) is int and initialization["waitSeconds"] > 0,
            "initialization wait must be positive",
        )
        for field in ("targetRevision", "credentialRevision"):
            require(
                bool(
                    re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,63}", initialization.get(field, ""))
                ),
                f"invalid {field}",
            )
        administration = record.get("administration", {})
        assets = administration.get("assets")
        require(isinstance(assets, dict) and bool(assets), "administration assets are missing")
        for label, value in assets.items():
            require(bool(re.fullmatch(r"[a-z][a-z0-9-]*", label)), "invalid asset label")
            require(checkout_path(root, value).is_file(), "administration asset is missing")
        for field in ("maxMigrationSeconds", "remainingJobSeconds", "jobDeadlineSeconds"):
            require(
                type(administration.get(field)) is int and administration[field] > 0,
                f"invalid {field}",
            )
    if target["mode"] == "flux":
        flux = target.get("flux", {})
        for field in ("namespace", "sourcePrefix", "apiName", "sharedSource", "sharedOwner"):
            require(
                bool(re.fullmatch(r"[a-z][a-z0-9-]{0,40}", flux.get(field, ""))), f"invalid {field}"
            )
        checkout_path(root, flux.get("bundle", ""))
        checkout_path(root, flux.get("sharedPath", ""))
    else:
        require(
            bool(re.fullmatch(r"[a-z][a-z0-9-]*", target.get("kindCluster", ""))),
            "direct target requires a Kind cluster",
        )
    return target
