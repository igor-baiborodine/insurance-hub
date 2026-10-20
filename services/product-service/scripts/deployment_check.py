#!/usr/bin/env python3
"""Render and validate Product deployment overlays without cluster contact."""

from __future__ import annotations

import json
import os
import sys
import tempfile
from collections.abc import Sequence
from pathlib import Path

import test_deployment_checks
import yaml

import check_render

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
if str(REPOSITORY_ROOT) not in sys.path:
    sys.path.insert(0, str(REPOSITORY_ROOT))

from scripts.python.process_runner import CommandError, run_command  # noqa: E402

OVERLAYS = {
    "local-go": "k8s/overlays/local-dev/svc/product",
    "local-legacy": "k8s/overlays/local-dev/svc/product/legacy",
    "qa-go": "k8s/overlays/qa/svc/product",
    "qa-legacy": "k8s/overlays/qa/svc/product/legacy",
    "qa-aggregate": "k8s/overlays/qa/svc",
}


class DeploymentCheckError(RuntimeError):
    """The offline Product deployment gate could not be completed."""


def render(kubectl: str, source: Path) -> list[dict[str, object]]:
    try:
        result = run_command(
            [kubectl, "kustomize", str(source)],
            cwd=REPOSITORY_ROOT,
            env=os.environ,
            timeout_seconds=60,
            teardown_seconds=5,
            activity=f"render {source.relative_to(REPOSITORY_ROOT)}",
        )
        documents = [item for item in yaml.safe_load_all(result.stdout) if item is not None]
    except (CommandError, OSError, yaml.YAMLError) as error:
        raise DeploymentCheckError(f"could not render {source.name}: {error}") from error
    if not documents or not all(isinstance(item, dict) for item in documents):
        raise DeploymentCheckError(f"rendered resources are missing or malformed: {source}")
    return documents


def main(_argv: Sequence[str] | None = None) -> int:
    kubectl = os.environ.get("KUBECTL", "kubectl")
    try:
        with tempfile.TemporaryDirectory(prefix="product-deploy-check-") as directory:
            temporary = Path(directory)
            paths: dict[str, Path] = {}
            for name, relative in OVERLAYS.items():
                path = temporary / f"{name}.json"
                path.write_text(
                    json.dumps(render(kubectl, REPOSITORY_ROOT / relative)), encoding="utf-8"
                )
                paths[name] = path
            arguments = [
                "--local-go",
                str(paths["local-go"]),
                "--local-legacy",
                str(paths["local-legacy"]),
                "--qa-go",
                str(paths["qa-go"]),
                "--qa-legacy",
                str(paths["qa-legacy"]),
                "--qa-aggregate",
                str(paths["qa-aggregate"]),
            ]
            if check_render.main(arguments) != 0:
                return 1
            test_arguments = ["--checker", str(Path(check_render.__file__).resolve()), *arguments]
            if test_deployment_checks.main(test_arguments) != 0:
                return 1
    except (DeploymentCheckError, OSError, TypeError, ValueError) as error:
        print(f"product-deploy-check: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
