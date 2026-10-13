#!/usr/bin/env python3
"""Own an isolated GitHub-hosted runner Kind cluster; never adopt an existing one."""

from __future__ import annotations

import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile

from release_image import ROOT, ReleaseError, command, require

NAME = "local-dev-insurance-hub"
CONTEXT = f"kind-{NAME}"
NODE = "kindest/node:v1.34.0@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a"
OPERATOR = "https://github.com/cloudnative-pg/cloudnative-pg/releases/download/v1.27.0/cnpg-1.27.0.yaml"


def main() -> None:
    require(os.environ.get("GITHUB_ACTIONS") == "true" and
            os.environ.get("RUNNER_ENVIRONMENT") == "github-hosted",
            "CI cluster setup is restricted to GitHub-hosted runners; use deploy-test locally")
    require("PRODUCT_DATABASE_URL" not in os.environ, "PRODUCT_DATABASE_URL must be unset")
    require(NAME not in command(["kind", "get", "clusters"]).splitlines(),
            "refusing to adopt or delete an existing Kind cluster")
    with tempfile.TemporaryDirectory(prefix="product-ci-kube-") as directory:
        config = Path(directory) / "config"
        previous = os.environ.get("KUBECONFIG")
        os.environ["KUBECONFIG"] = str(config)
        try:
            command(["kind", "create", "cluster", "--name", NAME, "--image", NODE,
                     "--kubeconfig", str(config), "--wait", "180s"], timeout=300)
            command(["kubectl", "--context", CONTEXT, "apply", "--server-side", "-f", OPERATOR], timeout=120)
            command(["kubectl", "--context", CONTEXT, "-n", "cnpg-system", "rollout",
                     "status", "deployment/cnpg-controller-manager", "--timeout=180s"], timeout=200)
            # Stream only the harness's sanitized evidence, never raw Kubernetes logs or Secrets.
            subprocess.run(["make", "go-svc-deploy-test", "SVC_NAME=product"],
                           cwd=ROOT, check=True, timeout=1800)
        finally:
            original_failure = sys.exc_info()[0] is not None
            try:
                command(["kind", "delete", "cluster", "--name", NAME,
                         "--kubeconfig", str(config)], timeout=120)
            except ReleaseError:
                if not original_failure:
                    raise
                print("product-ci-cluster: cleanup failed after original failure", file=sys.stderr)
            finally:
                if previous is None:
                    os.environ.pop("KUBECONFIG", None)
                else:
                    os.environ["KUBECONFIG"] = previous


def interrupted(signum: int, frame: object) -> None:
    raise KeyboardInterrupt("CI cluster run interrupted")


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupted)
    try:
        main()
    except (ReleaseError, subprocess.SubprocessError) as error:
        raise SystemExit(f"product-ci-cluster: {error}")
