"""Shared deployment lifecycle with catalog policy and service-owned verification hooks."""

from __future__ import annotations

import json
import os
import re
import sys
import time
from pathlib import Path
from typing import Any

import yaml

from .configuration import checkout_path
from .release import release_annotations, service_release

ROOT = Path(__file__).resolve().parents[3]
OPERATIONS = {"deploy-preflight", "deploy-status", "image-load", "deploy", "deploy-verify", "stop"}


class DeploymentError(RuntimeError):
    """Deployment prerequisites or observed state do not satisfy the selected target."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise DeploymentError(message)


def ready(item: dict) -> bool:
    return any(
        condition.get("type") == "Ready" and condition.get("status") == "True"
        for condition in item.get("status", {}).get("conditions", [])
    )


def validate_flux_source(target: Any, *, require_release: bool) -> None:
    configuration = target.configuration
    flux = configuration["flux"]
    namespace = flux["namespace"]
    source = target.json("get", "gitrepository", flux["sharedSource"], "-n", namespace)
    shared = target.json("get", "kustomization", flux["sharedOwner"], "-n", namespace)
    require(
        source.get("spec", {}).get("ref", {}).get("branch") == flux["branch"],
        "shared source must track the configured branch",
    )
    spec = shared.get("spec", {})
    require(
        spec.get("path") == flux["sharedPath"]
        and spec.get("sourceRef") == {"kind": "GitRepository", "name": flux["sharedSource"]}
        and spec.get("prune") is True
        and spec.get("wait") is True
        and spec.get("suspend") is not True
        and ready(source)
        and ready(shared),
        "shared Flux ownership or readiness differs",
    )
    revision = source.get("status", {}).get("artifact", {}).get("revision", "")
    require(
        shared.get("status", {}).get("lastAppliedRevision") == revision,
        "shared Flux revisions differ",
    )
    expected = os.environ.get("DEPLOY_SOURCE_REVISION", "")
    require(bool(re.fullmatch(r"[0-9a-f]{40}", expected)), "DEPLOY_SOURCE_REVISION is required")
    if not require_release or configuration.get("initialization") is None:
        require(revision.endswith(expected), "Flux source revision differs from requested revision")
        return
    api = target.json(
        "get", "kustomization", flux["apiName"], "-n", namespace, "--ignore-not-found"
    )
    spec = api.get("spec", {})
    source_name = spec.get("sourceRef", {}).get("name", "")
    require(
        bool(re.fullmatch(re.escape(flux["sourcePrefix"]) + r"-[0-9a-f]{16}", source_name)),
        "release source identity differs",
    )
    release_id = source_name[-16:]
    dependencies = spec.get("dependsOn", [])
    require(len(dependencies) == 1, "release prerequisite dependency differs")
    init_name = dependencies[0].get("name", "")
    initialization = configuration["initialization"]
    require(
        bool(
            re.fullmatch(
                re.escape(initialization["jobName"] + "-" + release_id) + r"-a[1-9][0-9]?",
                init_name,
            )
        ),
        "release prerequisite identity differs",
    )
    source = target.json("get", "gitrepository", source_name, "-n", namespace)
    initialize = target.json("get", "kustomization", init_name, "-n", namespace)
    identity = (
        api.get("metadata", {}).get("annotations", {}).get("insurance-hub.io/release-identity", "")
    )
    require(
        bool(re.fullmatch(r"[0-9a-f]{64}", identity))
        and identity.startswith(release_id)
        and all(
            item.get("metadata", {}).get("annotations", {}).get("insurance-hub.io/release-identity")
            == identity
            for item in (source, initialize)
        ),
        "release annotations differ",
    )
    images = spec.get("images", [])
    require(
        source.get("spec", {}).get("ref", {}).get("commit") == expected
        and source.get("status", {}).get("artifact", {}).get("revision", "").endswith(expected)
        and len(images) == 1
        and bool(re.fullmatch(r"sha256:[0-9a-f]{64}", images[0].get("digest", "")))
        and images[0].get("name") == target.record["localImage"]
        and images[0].get("newName") == target.record["registryImage"]
        and initialize.get("spec", {}).get("images") == images
        and initialize.get("spec", {}).get("sourceRef") == spec.get("sourceRef")
        and initialize.get("spec", {}).get("path") == "./" + initialization["overlay"]
        and spec.get("path") == "./" + configuration["overlay"]
        and all(
            ready(item) and item.get("spec", {}).get("suspend") is not True
            for item in (source, initialize, api)
        ),
        "release source, prerequisite or workload is not ready at the selected revision",
    )


def local_administration_job(target: Any, image: str, run: Any) -> tuple[str, str]:
    require(
        target.configuration["mode"] == "direct",
        "direct initialization is forbidden for this target",
    )
    initialization = target.configuration["initialization"]
    try:
        details = json.loads(run(["docker", "image", "inspect", image]))[0]
        source = details["Config"]["Labels"]["org.opencontainers.image.revision"]
        digest = details["Id"]
    except (ValueError, IndexError, KeyError, TypeError) as error:
        raise DeploymentError("local administration image inspection failed") from error
    release = service_release(ROOT, target.service, target.environment, source, digest)
    overlay = checkout_path(ROOT, initialization["overlay"])
    # Resolve configured substitutions before YAML parsing; no shell interpolation is involved.
    rendered = target.kubectl("kustomize", str(overlay)).replace(
        "${MIGRATION_TIMEOUT}", release.migration_timeout
    )
    jobs = list(yaml.safe_load_all(rendered))
    require(len(jobs) == 1 and jobs[0].get("kind") == "Job", "administration render differs")
    job = jobs[0]
    require(job["metadata"]["name"] == initialization["jobName"], "initialization Job name differs")
    name = f"{initialization['jobName']}-{release.release_id}-a{initialization['attempt']}"
    job["metadata"]["name"] = name
    job["metadata"].setdefault("annotations", {}).update(release_annotations(release))
    pod = job["spec"]["template"]["spec"]
    containers = pod.get("initContainers", []) + pod.get("containers", [])
    require(
        bool(containers) and all(item["image"] == image for item in containers),
        "administration Job image differs from the API release",
    )
    migrators = [
        item
        for item in containers
        if item["name"] == target.record["administration"]["migrationContainer"]
    ]
    require(len(migrators) == 1, "migration container is missing")
    args = migrators[0]["args"]
    indexes = [index for index, value in enumerate(args) if value.startswith("--timeout=")]
    require(len(indexes) == 1, "migration timeout argument is missing or duplicated")
    args[indexes[0]] = f"--timeout={release.migration_timeout}"
    require(
        job["spec"].get("activeDeadlineSeconds")
        == target.record["administration"]["jobDeadlineSeconds"]
        and job["spec"].get("backoffLimit") == 0
        and "ttlSecondsAfterFinished" not in job["spec"],
        "Job lifecycle differs from configured budget",
    )
    return name, yaml.safe_dump(job, sort_keys=False)


def wait_for_job(target: Any, name: str) -> None:
    seconds = target.configuration["initialization"]["waitSeconds"]
    deadline = time.monotonic() + seconds
    heartbeat = time.monotonic() + 30
    while time.monotonic() < deadline:
        job = target.json("get", "job", name)
        conditions = job.get("status", {}).get("conditions", [])
        if any(
            item.get("type") == "Complete" and item.get("status") == "True" for item in conditions
        ):
            return
        if any(
            item.get("type") == "Failed" and item.get("status") == "True" for item in conditions
        ):
            raise DeploymentError(f"initialization Job/{name} failed and was retained")
        if time.monotonic() >= heartbeat:
            print(f"service-deployment: waiting for Job/{name}", file=sys.stderr)
            heartbeat = time.monotonic() + 30
        time.sleep(min(2, max(0, deadline - time.monotonic())))
    raise DeploymentError(f"initialization Job/{name} exceeded the {seconds}-second wait")


def apply(target: Any, rendered: str, manager: str) -> None:
    for dry_run in (("--dry-run=server",), ()):
        target.kubectl(
            "apply",
            "--server-side",
            *dry_run,
            f"--field-manager={manager}",
            "-f",
            "-",
            input_text=rendered,
            timeout=60,
        )


def execute(checks: Any, argv: list[str] | None = None) -> int:
    arguments = sys.argv[1:] if argv is None else argv
    require(len(arguments) == 1 and arguments[0] in OPERATIONS, "unsupported operation")
    operation = arguments[0]
    target = checks.Target()
    configuration = target.configuration
    if operation in {"image-load", "deploy", "stop"}:
        require(
            configuration["mode"] == "direct",
            "direct mutation is forbidden; use reviewed Flux state",
        )
    target.coordinates()
    checks.check_render()
    rendered, items, image = target.rendered()
    if configuration["mode"] == "flux":
        validate_flux_source(
            target, require_release=operation in {"deploy-status", "deploy-verify"}
        )
    if operation in {"deploy-preflight", "image-load", "deploy"}:
        checks.secret_and_image(target, image)
        target.live(items, require_deployment=False)
    if operation == "deploy-preflight":
        print("service-deployment: target and prerequisites passed")
    elif operation == "deploy-status":
        target.live(items, require_deployment=True)
        checks.check_requested_route(target)
    elif operation == "image-load":
        checks.run(
            ["kind", "load", "docker-image", image, "--name", configuration["kindCluster"]],
            timeout=120,
        )
    elif operation == "deploy":
        if configuration.get("initialization") is not None:
            checks.initialization_check(target)
            checks.record_snapshot("before initialization", checks.administration_snapshot(target))
            name, job = local_administration_job(target, image, checks.run)
            apply(target, job, f"insurance-hub-go-{target.service}-init")
            wait_for_job(target, name)
            checks.initialization_complete(target)
            checks.record_snapshot("after initialization", checks.administration_snapshot(target))
        before = checks.role_snapshot(target)
        checks.record_snapshot("before API deployment", before)
        apply(target, rendered, f"insurance-hub-go-{target.service}")
        checks.rollout(target)
        target.live(items, require_deployment=True)
        after = checks.role_snapshot(target)
        checks.record_snapshot("after API deployment", after)
        require(after == before, "protected state changed during API deployment")
    elif operation == "deploy-verify":
        checks.verify(target, items)
        checks.check_requested_route(target)
    elif operation == "stop":
        checks.stop(target, items)
    return 0


def release_image(target: Any) -> str:
    """Resolve the reviewed bundle's image when observing an initialized Flux release."""
    configuration = target.configuration
    flux = configuration["flux"]
    path = checkout_path(ROOT, flux["bundle"])
    require(path.is_file(), "selected release bundle is absent")
    documents = list(yaml.safe_load_all(path.read_text()))
    apis = [
        item
        for item in documents
        if isinstance(item, dict)
        and item.get("kind") == "Kustomization"
        and item.get("metadata", {}).get("name") == flux["apiName"]
    ]
    require(len(apis) == 1, "release bundle must have exactly one API owner")
    images = apis[0].get("spec", {}).get("images", [])
    require(
        len(images) == 1
        and images[0].get("name") == target.record["localImage"]
        and images[0].get("newName") == target.record["registryImage"]
        and bool(re.fullmatch(r"sha256:[0-9a-f]{64}", images[0].get("digest", ""))),
        "release bundle image differs from the selected service",
    )
    expected = os.environ.get("DEPLOY_SOURCE_REVISION", "")
    require(
        bool(re.fullmatch(r"[0-9a-f]{40}", expected))
        and apis[0]
        .get("metadata", {})
        .get("annotations", {})
        .get("insurance-hub.io/source-revision")
        == expected,
        "release bundle source differs from DEPLOY_SOURCE_REVISION",
    )
    return images[0]["newName"] + "@" + images[0]["digest"]
