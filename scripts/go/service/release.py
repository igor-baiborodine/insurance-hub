#!/usr/bin/env python3
"""Prepare catalog-selected releases without service or environment branches."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from decimal import ROUND_DOWN, Decimal, InvalidOperation
from pathlib import Path, PurePosixPath

import yaml

try:
    from .configuration import ConfigurationError, checkout_path, service_record, target_record
except ImportError:
    from configuration import ConfigurationError, checkout_path, service_record, target_record

ROOT = Path(__file__).resolve().parents[3]
SHA = re.compile(r"[0-9a-f]{40}")
SERVICE = re.compile(r"[a-z][a-z0-9-]*")
VERSION = re.compile(r"1\.[0-9]+\.[0-9]+")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+")
RELEASE_ROOT_FILES = frozenset({".dockerignore", "Dockerfile", "go.mod", "go.sum"})
RELEASE_DIRECTORIES = frozenset({"cmd", "gen", "internal"})
GIT_TIMEOUT_SECONDS = 30
DURATION = re.compile(r"([0-9]+(?:\.[0-9]*)?|\.[0-9]+)(ns|us|µs|μs|ms|s|m|h)")


class ReleaseSelectionError(RuntimeError):
    """A release input does not satisfy the shared Go service convention."""


@dataclass(frozen=True)
class Selection:
    module: str
    module_path: str
    service: str
    registry_image: str


@dataclass(frozen=True)
class ServiceRelease:
    source_commit: str
    image_digest: str
    asset_hashes: dict[str, str]
    target_revision: str
    credential_revision: str
    migration_timeout: str
    identity_sha256: str

    @property
    def release_id(self) -> str:
        return self.identity_sha256[:16]


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ReleaseSelectionError(message)


def release_module(value: str) -> str | None:
    path = PurePosixPath(value)
    if len(path.parts) < 3 or path.parts[0] != "services":
        return None

    module = path.parts[1]
    if not module.endswith("-service"):
        return None

    relative = path.parts[2:]
    if len(relative) == 1:
        return module if relative[0] in RELEASE_ROOT_FILES else None
    if relative[0] not in RELEASE_DIRECTORIES or relative[-1].endswith("_test.go"):
        return None
    if relative[:2] == ("internal", "testing"):
        return None
    if relative[:3] == ("internal", "postgres", "sql"):
        return None
    return module


def image_entry(lines: list[str], record: dict) -> int:
    expected_name = record["localImage"]
    expected_new_name = record["registryImage"]
    entries: list[tuple[str, str, int]] = []
    for index, line in enumerate(lines):
        match = re.fullmatch(r"  - name: (\S+)\n?", line)
        if not match:
            continue
        new_name = ""
        new_tag_index = -1
        for child_index in range(index + 1, len(lines)):
            child = lines[child_index]
            if re.match(r"  - name: ", child) or (child and not child[0].isspace()):
                break
            name_match = re.fullmatch(r"    newName: (\S+)\n?", child)
            if name_match:
                new_name = name_match.group(1)
            if re.fullmatch(r"    newTag: \S+\n?", child):
                require(new_tag_index == -1, "target image entry has multiple newTag values")
                new_tag_index = child_index
        entries.append((match.group(1), new_name, new_tag_index))

    matches = [entry for entry in entries if entry[0] == expected_name]
    require(
        len(matches) == 1,
        f"target Kustomization must contain exactly one {expected_name} image entry",
    )
    name, new_name, tag_index = matches[0]
    require(
        name == expected_name and new_name == expected_new_name,
        f"target image entry must publish {expected_new_name}",
    )
    require(tag_index >= 0, "target image entry must contain exactly one newTag")
    return tag_index


def select_service(paths: list[str], root: Path, repository: str) -> Selection:
    require(bool(REPOSITORY.fullmatch(repository)), "repository must use owner/name format")
    modules = {module for value in paths if (module := release_module(value)) is not None}
    require(
        len(modules) == 1,
        "release push must change build inputs for exactly one services/<service>-service module",
    )

    module = next(iter(modules))
    service = module.removesuffix("-service")
    require(bool(SERVICE.fullmatch(service)), "service name must match [a-z][a-z0-9-]*")
    module_path = f"services/{module}"
    record = service_record(root, service)
    require(record["modulePath"] == module_path, "module differs from catalog")
    require((root / module_path / "Dockerfile").is_file(), "module Dockerfile is missing")
    targets = record.get("releaseTargets")
    require(isinstance(targets, list) and bool(targets), "release targets are missing")
    for name in targets:
        target = target_record(root, record, name)
        require(target["mode"] == "flux", "automatic release target must be Flux owned")
        path = checkout_path(root, target["overlay"]) / "kustomization.yaml"
        require(path.is_file(), "target Kustomization is missing")
        image_entry(path.read_text().splitlines(keepends=True), record)
    return Selection(module, module_path, service, record["registryImage"])


def changed_paths(root: Path, before: str, after: str) -> list[str]:
    require(
        bool(SHA.fullmatch(before)) and set(before) != {"0"},
        "release before revision must be a nonzero full SHA",
    )
    require(bool(SHA.fullmatch(after)), "release after revision must be a full SHA")
    for revision in (before, after):
        result = subprocess.run(
            ["git", "cat-file", "-e", f"{revision}^{{commit}}"],
            cwd=root,
            check=False,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=GIT_TIMEOUT_SECONDS,
            env={"PATH": os.environ.get("PATH", ""), "GIT_CONFIG_NOSYSTEM": "1"},
        )
        require(result.returncode == 0, f"release revision is unavailable: {revision}")
    result = subprocess.run(
        ["git", "diff", "--name-only", before, after],
        cwd=root,
        check=True,
        text=True,
        capture_output=True,
        timeout=GIT_TIMEOUT_SECONDS,
        env={"PATH": os.environ.get("PATH", ""), "GIT_CONFIG_NOSYSTEM": "1"},
    )
    return result.stdout.splitlines()


def overlay_tag(root: Path, record: dict, target: dict, version: str) -> tuple[Path, str]:
    require(bool(VERSION.fullmatch(version)), "release version must be a 1.x semantic version")
    path = checkout_path(root, target["overlay"]) / "kustomization.yaml"
    require(path.is_file(), "target Kustomization is missing")
    lines = path.read_text().splitlines(keepends=True)
    tag_index = image_entry(lines, record)
    newline = "\n" if lines[tag_index].endswith("\n") else ""
    lines[tag_index] = f"    newTag: {version}{newline}"
    return path, "".join(lines)


def normalized_timeout(value: str, policy: dict) -> str:
    if value.startswith("+"):
        value = value[1:]
    position = 0
    nanoseconds = Decimal(0)
    units = {
        "ns": Decimal(1),
        "us": Decimal(1_000),
        "µs": Decimal(1_000),
        "μs": Decimal(1_000),
        "ms": Decimal(1_000_000),
        "s": Decimal(1_000_000_000),
        "m": Decimal(60_000_000_000),
        "h": Decimal(3_600_000_000_000),
    }
    try:
        for match in DURATION.finditer(value):
            require(match.start() == position, "migration timeout must be a Go duration")
            component = Decimal(match.group(1)) * units[match.group(2)]
            nanoseconds += component.to_integral_value(rounding=ROUND_DOWN)
            position = match.end()
    except InvalidOperation as error:
        raise ReleaseSelectionError("migration timeout must be a Go duration") from error
    require(position == len(value) and position > 0, "migration timeout must be a Go duration")
    require(
        nanoseconds > 0 and nanoseconds <= policy["maxMigrationSeconds"] * 1_000_000_000,
        "migration timeout exceeds the configured command budget",
    )
    require(
        nanoseconds
        + Decimal((policy["seedSeconds"] + policy["minimumJobReserveSeconds"]) * 1_000_000_000)
        <= policy["jobDeadlineSeconds"] * 1_000_000_000,
        "migration, seed and minimum reserve budgets exceed the Job deadline",
    )
    for divisor, suffix in (
        (1_000_000_000, "s"),
        (1_000_000, "ms"),
        (1_000, "us"),
        (1, "ns"),
    ):
        if nanoseconds % divisor == 0:
            return f"{int(nanoseconds / divisor)}{suffix}"
    raise AssertionError("unreachable timeout normalization")


def sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def service_release(
    root: Path,
    service: str,
    target_name: str,
    source_commit: str,
    image_digest: str,
) -> ServiceRelease:
    require(bool(SHA.fullmatch(source_commit)), "source commit must be a full SHA")
    require(bool(re.fullmatch(r"sha256:[0-9a-f]{64}", image_digest)), "image digest is invalid")
    record = service_record(root, service)
    target = target_record(root, record, target_name)
    initialization = target.get("initialization")
    require(initialization is not None, "target has no initialization capability")
    policy = record["administration"]
    hashes = {
        name: sha256_file(checkout_path(root, path)) for name, path in policy["assets"].items()
    }
    timeout = normalized_timeout(initialization["migrationTimeout"], policy)
    payload = {
        "service": service,
        "target": target_name,
        "assets": hashes,
        "sourceCommit": source_commit,
        "imageDigest": image_digest,
        "targetRevision": initialization["targetRevision"],
        "credentialRevision": initialization["credentialRevision"],
        "migrationTimeout": timeout,
    }
    identity = hashlib.sha256(
        json.dumps(payload, sort_keys=True, separators=(",", ":")).encode()
    ).hexdigest()
    return ServiceRelease(
        source_commit,
        image_digest,
        hashes,
        payload["targetRevision"],
        payload["credentialRevision"],
        timeout,
        identity,
    )


def release_annotations(release: ServiceRelease) -> dict[str, str]:
    return {
        "insurance-hub.io/release-identity": release.identity_sha256,
        "insurance-hub.io/source-revision": release.source_commit,
        "insurance-hub.io/image-digest": release.image_digest,
        "insurance-hub.io/asset-hashes": json.dumps(release.asset_hashes, sort_keys=True),
        "insurance-hub.io/target-revision": release.target_revision,
        "insurance-hub.io/credential-revision": release.credential_revision,
        "insurance-hub.io/migration-timeout": release.migration_timeout,
    }


def release_documents(
    release: ServiceRelease, record: dict, target: dict, repository: str
) -> list[dict]:
    require(bool(REPOSITORY.fullmatch(repository)), "repository must use owner/name format")
    flux = target["flux"]
    initialization = target["initialization"]
    suffix = f"-{release.release_id}-a{initialization['attempt']}"
    source_name = f"{flux['sourcePrefix']}-{release.release_id}"
    init_name = initialization["jobName"] + suffix
    annotations = release_annotations(release)
    source = {
        "apiVersion": "source.toolkit.fluxcd.io/v1",
        "kind": "GitRepository",
        "metadata": {
            "name": source_name,
            "namespace": flux["namespace"],
            "annotations": annotations,
        },
        "spec": {
            "interval": "5m",
            "url": f"https://github.com/{repository}",
            "ref": {"commit": release.source_commit},
        },
    }
    image = {
        "name": record["localImage"],
        "newName": record["registryImage"],
        "digest": release.image_digest,
    }

    def owner(name: str, path: str, timeout: str) -> dict:
        return {
            "apiVersion": "kustomize.toolkit.fluxcd.io/v1",
            "kind": "Kustomization",
            "metadata": {"name": name, "namespace": flux["namespace"], "annotations": annotations},
            "spec": {
                "interval": "5m",
                "timeout": timeout,
                "prune": True,
                "wait": True,
                "force": False,
                "sourceRef": {"kind": "GitRepository", "name": source_name},
                "path": "./" + path,
                "images": [image],
            },
        }

    initialize = owner(init_name, initialization["overlay"], flux["initializationTimeout"])
    initialize["spec"].update(
        {
            "retryInterval": "1m",
            "nameSuffix": suffix,
            "postBuild": {"substitute": {"MIGRATION_TIMEOUT": release.migration_timeout}},
            "healthChecks": [
                {
                    "apiVersion": "batch/v1",
                    "kind": "Job",
                    "name": init_name,
                    "namespace": target["namespace"],
                }
            ],
        }
    )
    api = owner(flux["apiName"], target["overlay"], flux["apiTimeout"])
    api["spec"]["dependsOn"] = [{"name": init_name}]
    return [source, initialize, api]


def retained_release_documents(
    existing: list[object], generated: list[dict], target: dict
) -> list[dict]:
    """Retain immutable release prerequisites while replacing the stable API owner."""
    flux = target["flux"]
    initialization = target["initialization"]
    source_prefix = flux["sourcePrefix"]
    init_prefix = initialization["jobName"]
    api_name = flux["apiName"]
    namespace = flux["namespace"]
    source_pattern = re.compile(rf"{re.escape(source_prefix)}-([0-9a-f]{{16}})")
    init_pattern = re.compile(rf"{re.escape(init_prefix)}-([0-9a-f]{{16}})-a([1-9][0-9]*)")
    retained: list[dict] = []
    retained_by_key: dict[tuple[str, str, str], dict] = {}
    source_identities: set[str] = set()
    initialization_identities: list[str] = []
    api_count = 0

    def identity(document: dict, name: str) -> str:
        metadata = document["metadata"]
        annotations = metadata.get("annotations", {})
        require(
            isinstance(annotations, dict),
            f"retained release annotations are invalid: {name}",
        )
        release_identity = annotations.get("insurance-hub.io/release-identity", "")
        source_revision = annotations.get("insurance-hub.io/source-revision", "")
        image_digest = annotations.get("insurance-hub.io/image-digest", "")
        require(
            bool(re.fullmatch(r"[0-9a-f]{64}", release_identity)),
            f"retained release identity is invalid: {name}",
        )
        require(
            bool(SHA.fullmatch(source_revision)),
            f"retained release source revision is invalid: {name}",
        )
        require(
            bool(re.fullmatch(r"sha256:[0-9a-f]{64}", image_digest)),
            f"retained release image digest is invalid: {name}",
        )
        return release_identity

    def add(document: object, *, current: bool) -> None:
        nonlocal api_count
        require(isinstance(document, dict), "release bundle contains a non-document value")
        metadata = document.get("metadata")
        require(isinstance(metadata, dict), "release bundle document metadata is missing")
        name = metadata.get("name", "")
        document_namespace = metadata.get("namespace", "")
        kind = document.get("kind", "")
        require(
            isinstance(name, str) and name,
            "release bundle document name is missing",
        )
        require(document_namespace == namespace, f"release bundle namespace differs: {name}")

        if name == api_name:
            require(kind == "Kustomization", "stable API owner kind differs")
            api_count += 1
            require(api_count == 1, "release bundle contains multiple stable API owners")
            return

        release_identity = identity(document, name)
        release_id = release_identity[:16]
        spec = document.get("spec")
        require(isinstance(spec, dict), f"retained release spec is invalid: {name}")
        if kind == "GitRepository":
            match = source_pattern.fullmatch(name)
            require(
                match is not None and match.group(1) == release_id,
                f"release ID collision or unmanaged source: {name}",
            )
            require(
                document.get("apiVersion") == "source.toolkit.fluxcd.io/v1",
                f"retained release source API differs: {name}",
            )
            require(
                spec.get("ref")
                == {"commit": metadata["annotations"]["insurance-hub.io/source-revision"]},
                f"retained release source ref differs: {name}",
            )
            source_identities.add(release_identity)
        elif kind == "Kustomization":
            match = init_pattern.fullmatch(name)
            require(
                match is not None and match.group(1) == release_id,
                f"release ID collision or unmanaged prerequisite: {name}",
            )
            require(
                document.get("apiVersion") == "kustomize.toolkit.fluxcd.io/v1",
                f"retained prerequisite API differs: {name}",
            )
            require(
                spec.get("sourceRef")
                == {"kind": "GitRepository", "name": f"{source_prefix}-{release_id}"},
                f"retained prerequisite source differs: {name}",
            )
            health_checks = spec.get("healthChecks", [])
            require(
                any(
                    check.get("kind") == "Job"
                    and check.get("name") == name
                    and check.get("namespace") == target["namespace"]
                    for check in health_checks
                    if isinstance(check, dict)
                ),
                f"retained prerequisite health check differs: {name}",
            )
            initialization_identities.append(release_identity)
        else:
            raise ReleaseSelectionError(
                f"release bundle contains unmanaged document: {kind}/{name}"
            )

        key = (kind, document_namespace, name)
        previous = retained_by_key.get(key)
        if previous is not None:
            require(current and previous == document, f"release bundle document conflicts: {name}")
            return
        retained_by_key[key] = document
        retained.append(document)

    for document in existing:
        add(document, current=False)
    if existing:
        require(api_count == 1, "release bundle stable API owner is missing")
        require(
            bool(source_identities) and set(initialization_identities) == source_identities,
            "release bundle sources and prerequisites are incomplete",
        )
    for document in generated[:-1]:
        add(document, current=True)
    require(
        set(initialization_identities) == source_identities,
        "release bundle sources and prerequisites are incomplete",
    )

    api = generated[-1]
    require(
        api.get("kind") == "Kustomization"
        and api.get("metadata", {}).get("name") == api_name
        and api.get("metadata", {}).get("namespace") == namespace,
        "generated stable API owner differs from target",
    )
    return [*retained, api]


def prepare_release(
    root: Path,
    service: str,
    repository: str,
    version: str,
    source_commit: str,
    image_digest: str,
    target_name: str = "",
) -> list[Path]:
    require(bool(VERSION.fullmatch(version)), "release version must be a 1.x semantic version")
    require(bool(REPOSITORY.fullmatch(repository)), "repository must use owner/name format")
    require(bool(SHA.fullmatch(source_commit)), "source commit must be a full SHA")
    require(bool(re.fullmatch(r"sha256:[0-9a-f]{64}", image_digest)), "image digest is invalid")
    record = service_record(root, service)
    targets = [target_name] if target_name else record.get("releaseTargets", [])
    require(isinstance(targets, list) and bool(targets), "release targets are missing")
    planned: dict[Path, str] = {}
    for name in targets:
        target = target_record(root, record, name)
        require(target["mode"] == "flux", "release target must be Flux owned")
        if target.get("initialization") is None:
            path, content = overlay_tag(root, record, target, version)
        else:
            release = service_release(root, service, name, source_commit, image_digest)
            path = checkout_path(root, target["flux"]["bundle"])
            existing = list(yaml.safe_load_all(path.read_text())) if path.exists() else []
            documents = retained_release_documents(
                existing,
                release_documents(release, record, target, repository),
                target,
            )
            content = yaml.safe_dump_all(
                documents,
                explicit_start=True,
                sort_keys=False,
            )
        require(path not in planned, "release targets share an output path")
        planned[path] = content
    # Validate every target before writing any output; replace each complete file atomically.
    for path, content in planned.items():
        path.parent.mkdir(parents=True, exist_ok=True)
        descriptor, name = tempfile.mkstemp(prefix=".release-", dir=path.parent)
        temporary = Path(name)
        try:
            with os.fdopen(descriptor, "w") as output:
                output.write(content)
            temporary.replace(path)
        finally:
            temporary.unlink(missing_ok=True)
    return list(planned)


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser()
    commands = result.add_subparsers(dest="command", required=True)
    select = commands.add_parser("select")
    for field in ("before", "after", "repository"):
        select.add_argument("--" + field, required=True)
    select.add_argument("--output", required=True, type=Path)
    prepare = commands.add_parser("prepare")
    identity = commands.add_parser("identity")
    for command in (prepare, identity):
        for field in ("service", "source-commit", "image-digest"):
            command.add_argument("--" + field, required=True)
        command.add_argument("--target", default="", required=command is identity)
    prepare.add_argument("--repository", required=True)
    prepare.add_argument("--version", required=True)
    prepare.add_argument(
        "--output",
        required=True,
        type=Path,
        help="NUL-delimited repository-relative paths for git staging",
    )
    return result


def main(argv: list[str] | None = None) -> int:
    arguments = parser().parse_args(argv)
    if arguments.command == "select":
        selected = select_service(
            changed_paths(ROOT, arguments.before, arguments.after), ROOT, arguments.repository
        )
        with arguments.output.open("a") as output:
            for name, value in selected.__dict__.items():
                output.write(f"{name}={value}\n")
        print(f"go-service-release: selected {selected.module}")
    elif arguments.command == "identity":
        print(
            service_release(
                ROOT,
                arguments.service,
                arguments.target,
                arguments.source_commit,
                arguments.image_digest,
            ).identity_sha256
        )
    else:
        paths = prepare_release(
            ROOT,
            arguments.service,
            arguments.repository,
            arguments.version,
            arguments.source_commit,
            arguments.image_digest,
            arguments.target,
        )
        arguments.output.write_bytes(
            b"".join(str(path.relative_to(ROOT)).encode() + b"\0" for path in paths)
        )
        print(f"go-service-release: prepared {len(paths)} configured release file(s)")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except subprocess.TimeoutExpired:
        print("go-service-release: Git command exceeded its 30-second deadline", file=sys.stderr)
        raise SystemExit(1) from None
    except (
        OSError,
        subprocess.SubprocessError,
        ReleaseSelectionError,
        ConfigurationError,
        ValueError,
        yaml.YAMLError,
    ) as error:
        print(f"go-service-release: {error}", file=sys.stderr)
        raise SystemExit(1) from None
