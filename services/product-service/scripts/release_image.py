#!/usr/bin/env python3
"""Explicit Product publication; Python 3.10+, standard library only."""

from __future__ import annotations

import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
REGISTRY = "ghcr.io/igor-baiborodine/insurance-hub/product-api"


class ReleaseError(Exception):
    pass


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ReleaseError(message)


def command(argv: list[str], timeout: int = 60) -> str:
    try:
        result = subprocess.run(argv, cwd=ROOT, capture_output=True, text=True,
                                timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ReleaseError(f"command unavailable or timed out: {argv[0]}") from error
    require(result.returncode == 0, f"command failed: {' '.join(argv[:3])}")
    return result.stdout.strip()


def clean() -> None:
    require(not command(["git", "status", "--porcelain", "--untracked-files=all"]),
            "release requires a clean checkout, including untracked files")


def check() -> str:
    revision = os.environ.get("RELEASE_REVISION", "")
    require(bool(re.fullmatch(r"[0-9a-f]{40}", revision)),
            "RELEASE_REVISION must be a full lowercase commit SHA")
    require(os.environ.get("GITHUB_EVENT_NAME") == "workflow_dispatch" and
            os.environ.get("GITHUB_REF") == "refs/heads/main" and
            os.environ.get("GITHUB_REPOSITORY") == "igor-baiborodine/insurance-hub",
            "publication requires an explicit workflow_dispatch from repository main")
    require(command(["git", "rev-parse", "HEAD"]) == revision,
            "checked-out HEAD differs from RELEASE_REVISION")
    command(["git", "merge-base", "--is-ancestor", revision, "origin/main"])
    clean()
    return revision


def publish() -> None:
    revision = check()
    output = Path(os.environ.get("RELEASE_METADATA", ""))
    require(output.is_absolute() and output.parent.is_dir() and not output.exists(),
            "RELEASE_METADATA must be an unused absolute path in an existing directory")
    require(not output.is_relative_to(ROOT), "release metadata must be outside the checkout")
    local = f"insurance-hub-product-api:git-{revision[:12]}"
    tag = f"{REGISTRY}:sha-{revision}"
    # Rebuild and exercise the image before any registry write. No mutable input tag is accepted.
    command(["make", "go-svc-image-check", "SVC_NAME=product"], timeout=1200)
    clean()
    image_id = command(["docker", "image", "inspect", "--format", "{{.Id}}", local])
    require(bool(re.fullmatch(r"sha256:[0-9a-f]{64}", image_id)), "invalid local image ID")
    command(["docker", "tag", local, tag])
    pushed = command(["docker", "push", tag], timeout=600)
    digests = re.findall(r"digest: (sha256:[0-9a-f]{64})", pushed)
    require(len(digests) == 1, "push did not report one digest; inspect registry before retry")
    immutable = f"{REGISTRY}@{digests[0]}"
    command(["docker", "pull", "--platform", "linux/amd64", immutable], timeout=300)
    pulled = json.loads(command(["docker", "image", "inspect", immutable]))[0]
    require(pulled["Id"] == image_id and pulled["Os"] == "linux" and
            pulled["Architecture"] == "amd64" and
            pulled["Config"]["Labels"]["org.opencontainers.image.revision"] == revision,
            "published digest does not match the validated image")
    metadata = {"revision": revision, "tag": tag, "image": immutable,
                "imageId": image_id, "platform": "linux/amd64"}
    with output.open("x", encoding="utf-8") as stream:
        json.dump(metadata, stream, indent=2)
        stream.write("\n")
    print(json.dumps(metadata, sort_keys=True))


def main() -> None:
    require(len(sys.argv) == 2 and sys.argv[1] in {"check", "publish"}, "unknown release action")
    if sys.argv[1] == "check":
        print(f"product-release: verified merged revision {check()}")
    else:
        publish()


if __name__ == "__main__":
    try:
        main()
    except (ReleaseError, OSError, ValueError, KeyError, IndexError) as error:
        print(f"product-release: {error}", file=sys.stderr)
        sys.exit(1)
