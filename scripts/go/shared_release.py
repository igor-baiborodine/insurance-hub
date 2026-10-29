"""Servicekit release identity, exact publication and qualification admission."""

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
    DIGEST,
    MODULE,
    REPOSITORY,
    SHA,
    WORKFLOW,
    Budget,
    SharedError,
    digest,
    environment,
    exact,
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

    def command(self, arguments: list[str]) -> str:
        env = environment()
        if os.environ.get("GH_TOKEN"):
            env["GH_TOKEN"] = os.environ["GH_TOKEN"]
        return self.budget.run(
            ["gh", *arguments, "--repo", REPOSITORY],
            self.root,
            "verify/publish library release",
            env,
            60,
        )

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


def verify_run_jobs(provider: GitHub, run_id: int, attempt: int) -> None:
    response = provider.api(f"actions/runs/{run_id}/attempts/{attempt}/jobs?per_page=100")
    require(response.get("total_count", 101) <= 100, "release jobs pagination is unsupported")
    # The jobs API reports display names, not the workflow's job IDs.
    for name in ("Validate Go library release", "Verify published Go library"):
        matches = [job for job in response["jobs"] if job["name"] == name]
        require(
            len(matches) == 1 and matches[0].get("conclusion") == "success",
            "release required job missing or unsuccessful",
        )


def qualification(record: Any, version: str, sha: str) -> None:
    exact(
        record,
        {
            "schema",
            "module",
            "version",
            "tag",
            "source_sha",
            "source_manifest",
            "checksums",
            "producer",
            "validation",
            "public_resolution",
        },
        "qualification",
    )
    require(
        record["schema"] == 1
        and record["module"] == MODULE
        and record["version"] == version
        and record["source_sha"] == sha
        and record["tag"] == release_identity(version, sha),
        "qualification identity mismatch",
    )
    require(DIGEST.fullmatch(record["source_manifest"]), "invalid source manifest digest")
    exact(record["checksums"], {"module", "go_mod"}, "checksums")
    require(
        all(
            isinstance(v, str) and re.fullmatch(r"h1:[A-Za-z0-9+/]{43}=", v)
            for v in record["checksums"].values()
        ),
        "invalid module checksum identities",
    )
    producer = record["producer"]
    exact(producer, {"repository", "workflow", "workflow_sha", "run_id", "run_attempt"}, "producer")
    require(
        producer["repository"] == REPOSITORY
        and producer["workflow"] == WORKFLOW
        and producer["workflow_sha"] == sha,
        "untrusted qualification producer",
    )
    require(
        all(isinstance(producer[k], int) and producer[k] > 0 for k in ("run_id", "run_attempt")),
        "invalid producer run identity",
    )
    for key, names in (
        ("validation", {"library", "consumers"}),
        ("public_resolution", {"proxy", "checksums", "standalone", "container"}),
    ):
        exact(record[key], names, key)
        for result in record[key].values():
            exact(result, {"status", "identity"}, "result")
            require(
                result["status"] == "passed" and DIGEST.fullmatch(result["identity"]),
                "failed or incomplete qualification",
            )


def producer() -> dict[str, Any]:
    require(
        os.environ.get("GITHUB_REPOSITORY") == REPOSITORY
        and os.environ.get("GITHUB_REF") == "refs/heads/main"
        and os.environ.get("GITHUB_EVENT_NAME") == "push",
        "release requires a main push",
    )
    workflow_sha = os.environ.get("GITHUB_SHA", "")
    require(SHA.fullmatch(workflow_sha), "missing trusted workflow revision")
    require(
        os.environ.get("RELEASE_SHA") == workflow_sha, "release source must equal push revision"
    )
    return {
        "repository": REPOSITORY,
        "workflow": WORKFLOW,
        "workflow_sha": workflow_sha,
        "run_id": int(os.environ["GITHUB_RUN_ID"]),
        "run_attempt": int(os.environ["GITHUB_RUN_ATTEMPT"]),
    }


def verify_admission(
    provider: GitHub,
    asset: Path,
    version: str,
    sha: str,
    selected: dict[str, Any],
    trusted_workflow_sha: str,
    expected_asset_digest: str,
) -> None:
    require(DIGEST.fullmatch(expected_asset_digest), "invalid pinned qualification digest")
    asset_bytes = asset.read_bytes()
    require(digest(asset_bytes) == expected_asset_digest, "pinned qualification digest mismatch")
    record = json.loads(asset_bytes)
    qualification(record, version, sha)
    require(
        record["producer"]["workflow_sha"] == trusted_workflow_sha,
        "qualification workflow revision is not approved",
    )
    require(
        selected.get("Path") == MODULE
        and selected.get("Version") == version
        and not selected.get("Replace")
        and selected.get("Sum") == record["checksums"]["module"]
        and selected.get("GoModSum") == record["checksums"]["go_mod"],
        "selected graph/checksum mismatch",
    )
    tag = record["tag"]
    require(provider.tag_sha(tag) == sha, "release tag/source mismatch")
    release = provider.api("releases/tags/" + urllib.parse.quote(tag, safe=""))
    require(not release.get("draft"), "qualification release is still a draft")
    require(
        sum(item.get("name") == "qualification.json" for item in release.get("assets", [])) == 1,
        "published qualification asset missing or ambiguous",
    )
    with tempfile.TemporaryDirectory(prefix="servicekit-admission-") as temporary:
        provider.command(
            ["release", "download", tag, "--pattern", "qualification.json", "--dir", temporary]
        )
        published = Path(temporary) / "qualification.json"
        require(published.read_bytes() == asset_bytes, "published qualification asset changed")
        provider.command(
            [
                "attestation",
                "verify",
                str(published),
                "--signer-workflow",
                REPOSITORY + "/" + WORKFLOW,
                "--signer-digest",
                trusted_workflow_sha,
                "--source-digest",
                trusted_workflow_sha,
                "--source-ref",
                "refs/heads/main",
                "--deny-self-hosted-runners",
            ]
        )
    run_id = record["producer"]["run_id"]
    run = provider.api(f"actions/runs/{run_id}")
    require(
        run.get("path") == WORKFLOW
        and run.get("head_sha") == trusted_workflow_sha
        and run.get("event") == "push"
        and run.get("status") == "completed",
        "untrusted or unfinished release run",
    )
    verify_run_jobs(provider, run_id, record["producer"]["run_attempt"])
    require(provider.tag_sha(tag) == sha, "release tag changed during admission")


def public_verify(root: Path, version: str, sha: str, source_digest: str) -> dict[str, Any]:
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
        image = "servicekit-qualification-" + digest(temporary.encode())[:16]
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
        evidence = {
            "version": version,
            "source_sha": sha,
            "checksums": {"module": download["Sum"], "go_mod": download["GoModSum"]},
            "builder": BUILDER,
            "smoke_manifest": manifest_digest(consumer),
        }
        identity = digest(json.dumps(evidence, sort_keys=True).encode())
        return {
            **evidence,
            "results": {
                name: {"status": "passed", "identity": identity}
                for name in ("proxy", "checksums", "standalone", "container")
            },
        }


def publish_qualification(provider: GitHub, asset: Path, version: str, sha: str) -> None:
    record = json.loads(asset.read_text())
    qualification(record, version, sha)
    verify_run_jobs(provider, record["producer"]["run_id"], record["producer"]["run_attempt"])
    tag = release_identity(version, sha)
    require(provider.tag_sha(tag) == sha, "tag changed before qualification")
    provider.command(
        [
            "attestation",
            "verify",
            str(asset),
            "--signer-workflow",
            REPOSITORY + "/" + WORKFLOW,
            "--signer-digest",
            record["producer"]["workflow_sha"],
            "--source-ref",
            "refs/heads/main",
        ]
    )
    release = provider.api("releases/tags/" + urllib.parse.quote(tag, safe=""), absent=True)
    if release and release.get("assets"):
        with tempfile.TemporaryDirectory(prefix="servicekit-resume-") as temporary:
            provider.command(
                ["release", "download", tag, "--pattern", "qualification.json", "--dir", temporary]
            )
            previous_asset = Path(temporary) / "qualification.json"
            previous = json.loads(previous_asset.read_text())
            qualification(previous, version, sha)
            require(
                previous["source_manifest"] == record["source_manifest"]
                and previous["checksums"] == record["checksums"]
                and previous["producer"]["workflow_sha"] == record["producer"]["workflow_sha"],
                "existing qualification has different source/checksums or control revision",
            )
            verify_run_jobs(
                provider, previous["producer"]["run_id"], previous["producer"]["run_attempt"]
            )
            provider.command(
                [
                    "attestation",
                    "verify",
                    str(previous_asset),
                    "--signer-workflow",
                    REPOSITORY + "/" + WORKFLOW,
                    "--signer-digest",
                    record["producer"]["workflow_sha"],
                    "--source-ref",
                    "refs/heads/main",
                ]
            )
            require(provider.tag_sha(tag) == sha, "tag changed before resume")
            if release.get("draft"):
                provider.command(["release", "edit", tag, "--draft=false"])
            final = provider.api("releases/tags/" + urllib.parse.quote(tag, safe=""))
            require(not final.get("draft"), "resume did not publish release")
            require(provider.tag_sha(tag) == sha, "tag changed during resume publication")
        return
    require(release is None or release.get("draft"), "published release lacks qualification asset")
    if release is None:
        provider.command(
            [
                "release",
                "create",
                tag,
                "--draft",
                "--verify-tag",
                "--title",
                tag,
                "--notes",
                f"Qualified servicekit {version} at {sha}. See qualification.json for checksums, source and validation identities.",
            ]
        )
    # Recheck an uncertain creation; never overwrite assets from a competing operation.
    release = provider.api("releases/tags/" + urllib.parse.quote(tag, safe=""))
    if release.get("assets"):
        with tempfile.TemporaryDirectory(prefix="servicekit-existing-") as temporary:
            provider.command(
                ["release", "download", tag, "--pattern", "qualification.json", "--dir", temporary]
            )
            require(
                (Path(temporary) / "qualification.json").read_bytes() == asset.read_bytes(),
                "existing qualification asset differs; inspect prior publication",
            )
    else:
        provider.command(["release", "upload", tag, str(asset)])
    require(provider.tag_sha(tag) == sha, "tag changed during qualification")
    provider.command(["release", "edit", tag, "--draft=false"])
    release = provider.api("releases/tags/" + urllib.parse.quote(tag, safe=""))
    require(not release.get("draft"), "publication readback found a draft")
    with tempfile.TemporaryDirectory(prefix="servicekit-publication-") as temporary:
        provider.command(
            ["release", "download", tag, "--pattern", "qualification.json", "--dir", temporary]
        )
        published = Path(temporary) / "qualification.json"
        require(
            published.read_bytes() == asset.read_bytes(), "published qualification asset differs"
        )
        provider.command(
            [
                "attestation",
                "verify",
                str(published),
                "--signer-workflow",
                REPOSITORY + "/" + WORKFLOW,
                "--signer-digest",
                record["producer"]["workflow_sha"],
                "--source-ref",
                "refs/heads/main",
            ]
        )
    require(provider.tag_sha(tag) == sha, "tag changed after qualification publication")
