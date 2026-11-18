#!/usr/bin/env python3
"""Run the exact Chat candidate in one owned disposable Kind namespace."""

from __future__ import annotations

import json
import os
import re
import secrets
import signal
import sys
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

import chat_deployment_checks as checks  # noqa: E402

from scripts.go.service.configuration import service_record, target_record  # noqa: E402
from scripts.python.process_runner import CommandError  # noqa: E402


class DisposableError(RuntimeError):
    """The owned disposable deployment did not satisfy its contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise DisposableError(message)


def deployment_smoke_target() -> tuple[str, str]:
    record = service_record(ROOT, "chat")
    target = target_record(ROOT, record, "local-dev")
    require(target["mode"] == "direct", "deployment smoke target must use direct ownership")
    context = target["context"]
    cluster = target.get("kindCluster")
    require(isinstance(cluster, str) and bool(cluster), "deployment smoke Kind cluster is missing")
    require(
        os.environ.get("DEPLOYMENT_SMOKE_CONTEXT", context) == context,
        "deployment smoke context differs from the catalog",
    )
    require(
        os.environ.get("DEPLOYMENT_SMOKE_KIND_CLUSTER", cluster) == cluster,
        "deployment smoke Kind cluster differs from the catalog",
    )
    return context, cluster


def kubectl(
    context: str,
    namespace: str,
    *arguments: str,
    input_text: str | None = None,
    timeout: int = 60,
) -> str:
    binary = os.environ.get("KUBECTL", "kubectl")
    return checks.run(
        [binary, "--context", context, "--namespace", namespace, *arguments],
        input_text=input_text,
        timeout=timeout,
    )


def inspect_image(image: str, source_revision: str) -> dict[str, str]:
    require(
        bool(re.fullmatch(r"insurance-hub-chat-api:1\.0\.0-local\.[0-9a-f]{12}", image)),
        "candidate image identity differs",
    )
    require(
        bool(re.fullmatch(r"[0-9a-f]{40}", source_revision))
        and image.endswith(source_revision[:12]),
        "candidate source identity differs",
    )
    details = json.loads(
        checks.run([os.environ.get("DOCKER", "docker"), "image", "inspect", image])
    )
    require(len(details) == 1, "candidate image inspection differs")
    labels = details[0].get("Config", {}).get("Labels", {})
    version = "git-" + source_revision[:12]
    image_id = details[0].get("Id", "")
    require(
        labels.get("org.opencontainers.image.revision") == source_revision
        and labels.get("org.opencontainers.image.version") == version
        and bool(re.fullmatch(r"sha256:[0-9a-f]{64}", image_id)),
        "candidate OCI identity differs",
    )
    return {"imageId": image_id, "buildVersion": version}


def prepared_resources(
    text: str, namespace: str, image: str, source_revision: str
) -> list[dict[str, Any]]:
    require(bool(re.fullmatch(r"chat-smoke-[0-9a-f]{8}", namespace)), "namespace is not owned")
    require(
        bool(re.fullmatch(r"[0-9a-f]{40}", source_revision))
        and image.endswith(source_revision[:12]),
        "candidate source identity differs",
    )
    items = checks.validate_render("local-dev", text)
    version = "git-" + source_revision[:12]
    for item in items:
        item.setdefault("metadata", {})["namespace"] = namespace
    deployment = checks.resource(items, "Deployment", "local-dev-chat-api")
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = image
    deployment["metadata"]["labels"]["app.kubernetes.io/version"] = version
    deployment["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/version"] = version
    checks.resource(items, "ConfigMap", "local-dev-chat-config")["data"]["SERVICE_VERSION"] = (
        version
    )
    policy = checks.resource(items, "NetworkPolicy", "local-dev-chat-api")
    policy_spec = policy.setdefault("spec", {})
    policy_spec["ingress"] = []
    policy_spec["egress"] = []
    return items


def verify_target(context: str) -> None:
    binary = os.environ.get("KUBECTL", "kubectl")
    require(
        checks.run([binary, "config", "current-context"]).strip() == context,
        "active Kubernetes context differs",
    )
    checks.run([binary, "--context", context, "get", "nodes", "-o", "name"])


def create_namespace(context: str, namespace: str) -> None:
    manifest = yaml.safe_dump(
        {
            "apiVersion": "v1",
            "kind": "Namespace",
            "metadata": {
                "name": namespace,
                "labels": {"insurance-hub.io/chat-smoke": "owned"},
                "annotations": {"insurance-hub.io/chat-smoke-owner": namespace},
            },
        },
        sort_keys=False,
    )
    kubectl(context, "default", "create", "-f", "-", input_text=manifest)


def delete_namespace(context: str, namespace: str) -> None:
    text = kubectl(
        context,
        "default",
        "get",
        "namespace",
        namespace,
        "--ignore-not-found",
        "-o",
        "json",
    )
    if not text:
        return
    metadata = json.loads(text).get("metadata", {})
    require(
        metadata.get("labels", {}).get("insurance-hub.io/chat-smoke") == "owned"
        and metadata.get("annotations", {}).get("insurance-hub.io/chat-smoke-owner") == namespace,
        "refusing to delete a foreign namespace",
    )
    kubectl(
        context,
        "default",
        "delete",
        "namespace",
        namespace,
        "--ignore-not-found",
        "--wait=true",
        "--timeout=90s",
        timeout=100,
    )


def verify_pod(target: checks.Target, image: str) -> str:
    selector = "app.kubernetes.io/component=api,app.kubernetes.io/name=chat-api"
    pods = target.json("get", "pods", "-l", selector).get("items", [])
    require(len(pods) == 1, "disposable Chat Pod inventory differs")
    pod = pods[0]
    conditions = pod.get("status", {}).get("conditions", [])
    statuses = pod.get("status", {}).get("containerStatuses", [])
    containers = pod.get("spec", {}).get("containers", [])
    require(
        pod.get("status", {}).get("phase") == "Running"
        and any(
            condition.get("type") == "Ready" and condition.get("status") == "True"
            for condition in conditions
        )
        and len(statuses) == len(containers) == 1
        and statuses[0].get("name") == containers[0].get("name") == "chat-api"
        and statuses[0].get("ready") is True
        and containers[0].get("image") == image,
        "disposable Chat Pod readiness or image differs",
    )
    runtime_image_id = statuses[0].get("imageID", "")
    require(bool(runtime_image_id), "disposable Chat runtime image ID is missing")
    return runtime_image_id


def verify_deployment(context: str, namespace: str, items: list[dict[str, Any]], image: str) -> str:
    target = object.__new__(checks.Target)
    target.environment = "local-dev"
    target.context = context
    target.namespace = namespace
    target.data_namespace = namespace
    target.deployment = "local-dev-chat-api"
    target.business = "local-dev-chat-api"
    target.management = "local-dev-chat-api-management"
    target.kubectl_binary = os.environ.get("KUBECTL", "kubectl")
    target.configuration = {"mode": "direct"}
    target.json = lambda *arguments: json.loads(
        kubectl(context, namespace, *arguments, "-o", "json") or "{}"
    )
    checks.verify(target, items)
    return verify_pod(target, image)


def execute(
    context: str,
    cluster: str,
    namespace: str,
    image: str,
    source_revision: str,
    items: list[dict[str, Any]],
) -> dict[str, str]:
    created = False
    failure: BaseException | None = None
    runtime_image_id = ""
    try:
        verify_target(context)
        created = True
        create_namespace(context, namespace)
        checks.run(
            ["kind", "load", "docker-image", image, "--name", cluster],
            timeout=120,
        )
        kubectl(
            context,
            namespace,
            "apply",
            "--server-side",
            "--field-manager=insurance-hub-chat-smoke",
            "-f",
            "-",
            input_text=yaml.safe_dump_all(items, explicit_start=True, sort_keys=False),
        )
        kubectl(
            context,
            namespace,
            "rollout",
            "status",
            "deployment/local-dev-chat-api",
            "--timeout=180s",
            timeout=210,
        )
        runtime_image_id = verify_deployment(context, namespace, items, image)
    except BaseException as error:  # cleanup must also run for interruption
        failure = error

    cleanup_error: BaseException | None = None
    if created:
        try:
            delete_namespace(context, namespace)
        except BaseException as error:  # report separately without hiding the work failure
            cleanup_error = error
    if failure is not None:
        if cleanup_error is not None:
            print(f"chat-disposable: cleanup also failed: {cleanup_error}", file=sys.stderr)
        raise failure
    if cleanup_error is not None:
        raise cleanup_error
    return {
        "sourceRevision": source_revision,
        "buildVersion": "git-" + source_revision[:12],
        "imageRef": image,
        "runtimeImageId": runtime_image_id,
        "namespaceCleanup": "passed",
    }


def main() -> int:
    image = os.environ.get("IMAGE_REF", "")
    source_revision = os.environ.get("DEPLOYMENT_SMOKE_SOURCE_REVISION", "")
    context, cluster = deployment_smoke_target()
    image_identity = inspect_image(image, source_revision)
    namespace = "chat-smoke-" + secrets.token_hex(4)
    items = prepared_resources(checks.render("local-dev"), namespace, image, source_revision)
    observation = execute(context, cluster, namespace, image, source_revision, items)
    observation.update(image_identity)
    print("chat-disposable: " + json.dumps(observation, sort_keys=True))
    return 0


def cli() -> int:
    received: int | None = None

    def interrupt(signum: int, _frame: object) -> None:
        nonlocal received
        received = received or signum
        raise KeyboardInterrupt

    previous_interrupt = signal.signal(signal.SIGINT, interrupt)
    previous_terminate = signal.signal(signal.SIGTERM, interrupt)
    try:
        try:
            return main()
        except KeyboardInterrupt:
            return 128 + (received or signal.SIGINT)
        except (
            CommandError,
            DisposableError,
            checks.DeploymentError,
            OSError,
            ValueError,
        ) as error:
            print(f"chat-disposable: {error}", file=sys.stderr)
            return 1
    finally:
        signal.signal(signal.SIGINT, previous_interrupt)
        signal.signal(signal.SIGTERM, previous_terminate)


if __name__ == "__main__":
    raise SystemExit(cli())
