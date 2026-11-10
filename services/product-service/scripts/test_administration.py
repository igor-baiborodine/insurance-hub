#!/usr/bin/env python3
"""Product administration mechanics tests and disposable-test helpers."""

import re
import subprocess
import sys
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
SERVICE_SCRIPTS = Path(__file__).resolve().parent
if str(SERVICE_SCRIPTS) not in sys.path:
    sys.path.insert(0, str(SERVICE_SCRIPTS))

import administration as reader  # noqa: E402

IMAGE = (
    "postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193"
)


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def run(argv, *, input_text=None, env=None, timeout=45):
    result = subprocess.run(
        argv, input=input_text, text=True, capture_output=True, timeout=timeout, env=env
    )
    if result.returncode:
        raise AssertionError(f"test command failed: {argv[0]} (exit {result.returncode})")
    return result.stdout.strip()


def test_deployment_ca_contract() -> None:
    deployment = (ROOT / "k8s/apps/svc/product/base/deployment.yaml").read_text()
    match = re.search(r"- name: postgres-ca\n\s+mountPath: (\S+)", deployment)
    check(match is not None, "Product Deployment has no postgres-ca mount")
    check(
        reader.CA_MOUNT == f"{match.group(1)}/ca.crt",
        "reader URL CA path differs from the Product Deployment mount",
    )


def test_forward_cleanup() -> None:
    manager = object.__new__(reader.Kubernetes)
    manager.context = "kind-local-dev-insurance-hub"
    manager.data_ns = "local-dev-all"
    manager.primary = "local-dev-postgres-product-1"
    manager.hostname = "local-dev-postgres-product-rw.local-dev-all.svc.cluster.local"

    class FailedForward:
        terminated = False

        def poll(self):
            return 1

        def terminate(self):
            self.terminated = True

        def wait(self, timeout=None):
            return 1

    process = FailedForward()
    with patch.object(reader.subprocess, "Popen", return_value=process):
        try:
            with manager.connection("a" * 64, b"-----BEGIN CERTIFICATE-----\ntest\n") as runtime:
                runtime("SELECT 1")
                raise AssertionError("failed port-forward executed reader operation")
        except reader.RoleError:
            pass
    check(process.terminated, "failed port-forward was not cleaned up")


if __name__ == "__main__":
    try:
        print("product-reader-test: checking shared mechanics", flush=True)
        test_deployment_ca_contract()
        test_forward_cleanup()
    except (AssertionError, OSError, subprocess.SubprocessError) as error:
        print(f"product-reader-test: {error}", file=sys.stderr, flush=True)
        raise SystemExit(1) from None
