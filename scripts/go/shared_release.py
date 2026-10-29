"""Servicekit release identity, tag publication and public module verification."""

from __future__ import annotations

import json
import os
import re
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

from shared_support import (
    MODULE,
    REPOSITORY,
    SHA,
    Budget,
    SharedError,
    digest,
    environment,
    manifest_digest,
    require,
)

BUILDER = "docker.io/library/golang:1.27.3-alpine3.23@sha256:0208a74017e3ce41f779876f3c6e9ad5d4030904e8ac6e91b9c5d7711c48362a"
VERSION = re.compile(r"v0\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)")
API_VERSION = "2026-03-10"


def release_identity(version: str, sha: str) -> str:
    require(
        VERSION.fullmatch(version) and version != "v0.0.0", "expected canonical stable v0 version"
    )
    require(SHA.fullmatch(sha), "expected full source SHA")
    return f"libs/servicekit/{version}"


class GitHub:
    """Fixed GitHub destination, bounded requests, no credentials in argv or diagnostics."""

    def __init__(self, root: Path):
        self.root = root
        self.budget = Budget(600)

    def api(
        self,
        suffix: str,
        method: str = "GET",
        body: Any = None,
        absent: bool = False,
    ) -> Any:
        url = f"https://api.github.com/repos/{REPOSITORY}/{suffix}"
        headers = {
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": API_VERSION,
            "Content-Type": "application/json",
        }
        token = os.environ.get("GH_TOKEN")
        if token:
            headers["Authorization"] = "Bearer " + token
        request = urllib.request.Request(
            url,
            headers=headers,
            method=method,
            data=json.dumps(body).encode() if body is not None else None,
        )
        try:
            remaining = self.budget.deadline - time.monotonic()
            require(remaining > 0, "GitHub operation exceeded overall deadline")
            with urllib.request.urlopen(request, timeout=min(30, remaining)) as response:
                raw = response.read(8 * 1024 * 1024)
                return json.loads(raw) if raw else None
        except urllib.error.HTTPError as error:
            if absent and error.code == 404:
                return None
            raise SharedError(f"GitHub {method} request failed: HTTP {error.code}") from None
        except urllib.error.URLError:
            raise SharedError("GitHub request unavailable") from None

    def tag_sha(self, tag: str) -> str | None:
        ref = self.api("git/ref/tags/" + urllib.parse.quote(tag, safe=""), absent=True)
        if ref is None:
            return None
        obj = ref["object"]
        for _ in range(5):
            if obj["type"] == "commit":
                return obj["sha"]
            require(obj["type"] == "tag", "unexpected tag object")
            obj = self.api("git/tags/" + obj["sha"])["object"]
        raise SharedError("tag nesting exceeds limit")


def publish_tag(provider: GitHub, version: str, sha: str) -> None:
    tag = release_identity(version, sha)
    existing = provider.tag_sha(tag)
    if existing is None:
        tags = provider.api("git/matching-refs/tags/libs/servicekit/")
        require(tags or version == "v0.1.0", "first library release must be v0.1.0")
    require(existing in {None, sha}, "tag collision")
    if existing is None:
        try:
            provider.api("git/refs", "POST", {"ref": "refs/tags/" + tag, "sha": sha})
        except SharedError:
            # A timed-out/competing create is uncertain, never permission to overwrite.
            require(provider.tag_sha(tag) == sha, "tag creation failed or collided")
    require(provider.tag_sha(tag) == sha, "tag readback mismatch")


def release_source() -> str:
    require(
        os.environ.get("GITHUB_REPOSITORY") == REPOSITORY
        and os.environ.get("GITHUB_REF") == "refs/heads/main"
        and os.environ.get("GITHUB_EVENT_NAME") == "push",
        "release requires a main push",
    )
    sha = os.environ.get("GITHUB_SHA", "")
    require(SHA.fullmatch(sha), "missing triggering push revision")
    require(os.environ.get("RELEASE_SHA") == sha, "release source must equal push revision")
    return sha


def public_verify(root: Path, version: str, sha: str, source_digest: str) -> None:
    release_identity(version, sha)
    budget = Budget(900)
    url = f"https://proxy.golang.org/{MODULE}/@v/{version}.info"
    while True:
        require(time.monotonic() < budget.deadline, "public resolution exceeded 15-minute deadline")
        try:
            with urllib.request.urlopen(
                url, timeout=min(30, budget.deadline - time.monotonic())
            ) as response:
                info = json.loads(response.read(65536))
            require(info["Version"] == version, "public version mismatch")
            break
        except urllib.error.HTTPError as error:
            require(
                error.code in {404, 408, 429, 500, 502, 503, 504}, "public proxy rejected release"
            )
        except urllib.error.URLError:
            pass
        print("waiting for public module propagation", flush=True)
        time.sleep(min(10, max(0, budget.deadline - time.monotonic())))
    with tempfile.TemporaryDirectory(prefix="servicekit-public-") as temporary:
        scratch = Path(temporary)
        consumer = scratch / "consumer"
        consumer.mkdir()
        (consumer / "go.mod").write_text(
            f"module example.com/servicekit-release-check\n\ngo 1.27.3\n\nrequire {MODULE} {version}\n"
        )
        (consumer / "main.go").write_text(
            "package main\nimport (\n"
            + "".join(
                f'_ "{MODULE}/{p}"\n'
                for p in ("config", "identity", "logger", "telemetry", "health")
            )
            + ")\nfunc main() {}\n"
        )
        env = environment(
            GOWORK="off",
            GOENV="off",
            GOFLAGS="",
            GOPROXY="https://proxy.golang.org",
            GOSUMDB="sum.golang.org",
            GOPRIVATE="",
            GONOSUMDB="",
            GONOPROXY="",
            GOMODCACHE=str(scratch / "modcache"),
            GOCACHE=str(scratch / "buildcache"),
        )
        download = json.loads(
            budget.run(
                ["go", "mod", "download", "-json", MODULE + "@" + version],
                consumer,
                "public checksum verification",
                env,
                300,
            )
        )
        require(
            download.get("Path") == MODULE
            and download.get("Version") == version
            and not download.get("Error"),
            "public module resolution failed",
        )
        require(
            manifest_digest(Path(download["Dir"])) == source_digest,
            "public module content does not match release source",
        )
        budget.run(["go", "mod", "tidy"], consumer, "resolve smoke consumer", env)
        selected = json.loads(
            budget.run(
                ["go", "list", "-m", "-json", MODULE], consumer, "verify exact public version", env
            )
        )
        require(
            selected.get("Version") == version and not selected.get("Replace"),
            "wrong public selection",
        )
        budget.run(["go", "run", "-mod=readonly", "."], consumer, "standalone smoke", env)
        image = "servicekit-release-" + digest(temporary.encode())[:16]
        (consumer / "Dockerfile").write_text(
            f"FROM {BUILDER}\nWORKDIR /src\nCOPY . .\n"
            "ENV GOWORK=off GOENV=off GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org\n"
            f"RUN go mod download && test \"$(go list -m -f '{{{{.Version}}}}' {MODULE})\" = '{version}' && go run -mod=readonly .\n"
        )
        try:
            budget.run(
                ["docker", "build", "--no-cache", "-t", image, str(consumer)],
                consumer,
                "fresh container smoke",
                env,
            )
        finally:
            failed = sys.exc_info()[0] is not None
            try:
                Budget(30).run(
                    ["docker", "image", "rm", "-f", image],
                    root,
                    "remove owned smoke image",
                    limit=30,
                )
            except RuntimeError, OSError:
                if not failed:
                    raise
                print("owned smoke image cleanup failed after validation failure", file=sys.stderr)
