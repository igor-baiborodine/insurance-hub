#!/usr/bin/env python3
"""Run bounded read-only Product security probes against one explicit target."""

from __future__ import annotations

import os
import sys
import tempfile
from collections.abc import Sequence
from pathlib import Path

from scripts.python.process_runner import CommandError, run_command

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]


class VerificationError(ValueError):
    """The Product security target is incomplete or unsafe."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise VerificationError(message)


def run(arguments: list[str], *, timeout: float, activity: str) -> str:
    return run_command(
        arguments,
        cwd=REPOSITORY_ROOT,
        env=os.environ,
        timeout_seconds=timeout,
        teardown_seconds=15,
        activity=activity,
    ).stdout.strip()


def main(_argv: Sequence[str] | None = None) -> int:
    context = os.environ.get("PRODUCT_SECURITY_TARGET_CONTEXT", "")
    namespace = os.environ.get("PRODUCT_SECURITY_TARGET_NAMESPACE", "")
    selected_environment = os.environ.get("PRODUCT_SECURITY_EXPECTED_ENV", "")
    http = os.environ.get("PRODUCT_SECURITY_HTTP_ENDPOINT", "")
    grpc = os.environ.get("PRODUCT_SECURITY_GRPC_ENDPOINT", "")
    health = os.environ.get("PRODUCT_SECURITY_HEALTH_ENDPOINT", "")
    try:
        require(bool(context), "PRODUCT_SECURITY_TARGET_CONTEXT is required")
        require(bool(namespace), "PRODUCT_SECURITY_TARGET_NAMESPACE is required")
        require(
            selected_environment in {"local-dev", "qa"},
            "PRODUCT_SECURITY_EXPECTED_ENV must be local-dev or qa",
        )
        require(bool(http and grpc and health), "all three Product endpoint inputs are required")
        require("PRODUCT_DATABASE_URL" not in os.environ, "PRODUCT_DATABASE_URL must be unset")
        if context == "isolated":
            require(namespace == "isolated", "isolated context requires isolated namespace")
            require(
                http.startswith("http://127.0.0.1:")
                and grpc.startswith("127.0.0.1:")
                and health.startswith("http://127.0.0.1:"),
                "isolated context requires loopback Product endpoints",
            )
        else:
            allowed = {
                ("local-dev", "kind-local-dev-insurance-hub", "local-dev-all"),
                ("qa", "qa-insurance-hub", "qa-svc"),
            }
            require(
                (selected_environment, context, namespace) in allowed,
                "target context and namespace do not match the selected environment",
            )
            require(
                run(
                    ["kubectl", "config", "current-context"],
                    timeout=15,
                    activity="inspect active Kubernetes context",
                )
                == context,
                "active Kubernetes context differs from the requested target",
            )
            current_namespace = (
                run(
                    ["kubectl", "config", "view", "--minify", "-o", "jsonpath={..namespace}"],
                    timeout=15,
                    activity="inspect active Kubernetes namespace",
                )
                or "default"
            )
            require(
                current_namespace == namespace,
                "active Kubernetes namespace differs from the requested target",
            )
        with tempfile.TemporaryDirectory(prefix="product-security-verify-") as directory:
            verifier = Path(directory) / "security-verifier"
            run(
                [
                    "make",
                    "-C",
                    "services/product-service",
                    "build-security-verifier",
                    f"SECURITY_VERIFIER_OUTPUT={verifier}",
                ],
                timeout=120,
                activity="build Product security verifier",
            )
            accepted = (
                REPOSITORY_ROOT
                / f"legacy/product-service/src/test/resources/product-read-baseline/access/{selected_environment}.json"
            )
            run(
                [
                    str(verifier),
                    "-context",
                    context,
                    "-namespace",
                    namespace,
                    "-http",
                    http,
                    "-grpc",
                    grpc,
                    "-health",
                    health,
                    "-accepted",
                    str(accepted),
                ],
                timeout=180,
                activity="verify Product security boundary",
            )
    except (VerificationError, CommandError, OSError) as error:
        print(f"product-security-verify: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
