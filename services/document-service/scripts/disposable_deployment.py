#!/usr/bin/env python3
"""Run the Document foundation in one owned disposable Kubernetes namespace."""

from __future__ import annotations

import json
import os
import re
import secrets
import sys
import time
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

import document_deployment_checks as checks  # noqa: E402
from scripts.python.process_runner import CommandError  # noqa: E402


class DisposableError(RuntimeError):
    """The owned disposable deployment did not satisfy its contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise DisposableError(message)


def prepared_resources(
    text: str, namespace: str, image: str, source_revision: str
) -> list[dict[str, Any]]:
    require(bool(re.fullmatch(r"document-smoke-[0-9a-f]{8}", namespace)), "namespace is not owned")
    require(
        bool(
            re.fullmatch(r"insurance-hub-document-api:1\.[0-9]+\.[0-9]+-local\.[0-9a-f]{12}", image)
        ),
        "candidate image identity differs",
    )
    require(
        bool(re.fullmatch(r"[0-9a-f]{40}", source_revision))
        and image.endswith(source_revision[:12]),
        "candidate source identity differs",
    )
    items = checks.validate_render("local-dev", text)
    version = "git-" + source_revision[:12]
    for item in items:
        item.setdefault("metadata", {})["namespace"] = namespace
    deployment = checks.resource(items, "Deployment", "local-dev-document-api")
    deployment["spec"]["template"]["spec"]["containers"][0]["image"] = image
    deployment["metadata"]["labels"]["app.kubernetes.io/version"] = version
    deployment["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/version"] = version
    checks.resource(items, "ConfigMap", "local-dev-document-config")["data"]["SERVICE_VERSION"] = (
        version
    )
    return items


def kubectl(
    context: str, namespace: str, *arguments: str, input_text: str | None = None, timeout: int = 60
) -> str:
    binary = os.environ.get("KUBECTL", "kubectl")
    return checks.run(
        [binary, "--context", context, "--namespace", namespace, *arguments],
        input_text=input_text,
        timeout=timeout,
    )


def wait_for_namespace_absence(context: str, namespace: str) -> None:
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        result = kubectl(context, "default", "get", "namespace", namespace, "--ignore-not-found")
        if not result.strip():
            return
        time.sleep(1)
    raise DisposableError("disposable namespace cleanup exceeded 90 seconds")


def main() -> int:
    image = os.environ.get("IMAGE_REF", "")
    source_revision = os.environ.get("QUALIFICATION_SOURCE_REVISION", "")
    context = os.environ.get("QUALIFICATION_CONTEXT", "kind-local-dev-insurance-hub")
    cluster = os.environ.get("QUALIFICATION_KIND_CLUSTER", "local-dev-insurance-hub")
    namespace = "document-smoke-" + secrets.token_hex(4)
    rendered = checks.render("local-dev")
    items = prepared_resources(rendered, namespace, image, source_revision)
    namespace_manifest = yaml.safe_dump(
        {
            "apiVersion": "v1",
            "kind": "Namespace",
            "metadata": {
                "name": namespace,
                "labels": {"insurance-hub.io/document-smoke": "owned"},
            },
        },
        sort_keys=False,
    )
    created = False
    try:
        kubectl(context, "default", "apply", "-f", "-", input_text=namespace_manifest)
        created = True
        checks.run(["kind", "load", "docker-image", image, "--name", cluster], timeout=120)
        kubectl(
            context,
            namespace,
            "apply",
            "--server-side",
            "--field-manager=insurance-hub-document-smoke",
            "-f",
            "-",
            input_text=yaml.safe_dump_all(items, explicit_start=True, sort_keys=False),
        )
        kubectl(
            context,
            namespace,
            "rollout",
            "status",
            "deployment/local-dev-document-api",
            "--timeout=180s",
            timeout=210,
        )
        target = object.__new__(checks.Target)
        target.context = context
        target.namespace = namespace
        target.deployment = "local-dev-document-api"
        target.management = "local-dev-document-api-management"
        target.kubectl_binary = os.environ.get("KUBECTL", "kubectl")
        target.configuration = {"mode": "direct"}
        target.json = lambda *args: json.loads(
            kubectl(context, namespace, *args, "-o", "json") or "{}"
        )
        checks.verify(target, items)
        print("document-disposable: Echo, health and denied Pod access passed")
        return 0
    finally:
        if created:
            try:
                kubectl(
                    context,
                    "default",
                    "delete",
                    "namespace",
                    namespace,
                    "--wait=false",
                    timeout=30,
                )
                wait_for_namespace_absence(context, namespace)
            except (CommandError, DisposableError) as error:
                print(f"document-disposable: cleanup failed: {error}", file=sys.stderr)
                raise


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (CommandError, DisposableError, checks.DeploymentError, OSError, ValueError) as error:
        print(f"document-disposable: {error}", file=sys.stderr)
        raise SystemExit(1) from error
