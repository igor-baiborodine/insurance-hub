#!/usr/bin/env python3
"""Automatic Product release orchestration; Python 3.10+, standard library only."""

from __future__ import annotations

import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[3]
SOURCE_ROOT = Path(os.environ.get("PRODUCT_RELEASE_SOURCE_ROOT", ROOT)).resolve()
REGISTRY = "ghcr.io/igor-baiborodine/insurance-hub/product-api"
REPOSITORY = "igor-baiborodine/insurance-hub"
MANIFEST = ROOT / "k8s/overlays/qa/svc/product/kustomization.yaml"
SOURCE_IMAGE_VARIABLES = (
    "IMAGE_NAME", "IMAGE_TAG", "IMAGE_REF", "SOURCE_CREATED", "SOURCE_REPOSITORY",
    "SOURCE_REVISION", "SOURCE_VERSION", "TARGET_PLATFORM",
)


class ReleaseError(Exception):
    pass


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ReleaseError(message)


def execute(argv: list[str], timeout: int = 60) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run(argv, cwd=ROOT, capture_output=True, text=True,
                              timeout=timeout, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ReleaseError(f"command unavailable or timed out: {argv[0]}") from error


def command(argv: list[str], timeout: int = 60) -> str:
    result = execute(argv, timeout)
    require(result.returncode == 0, f"command failed: {' '.join(argv[:3])}")
    return result.stdout.strip()


def source_command(argv: list[str], timeout: int = 60) -> str:
    if SOURCE_ROOT == ROOT:
        return command(argv, timeout)
    return command([argv[0], "-C", str(SOURCE_ROOT), *argv[1:]], timeout)


def validate_source_image() -> None:
    argv = ["make", "go-svc-image-check", "SVC_NAME=product"]
    if SOURCE_ROOT != ROOT:
        argv = ["make", "-C", str(SOURCE_ROOT), *argv[1:]]
    environment = os.environ.copy()
    for name in SOURCE_IMAGE_VARIABLES:
        environment.pop(name, None)
    try:
        result = subprocess.run(argv, cwd=ROOT, env=environment, timeout=1200, check=False)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ReleaseError("selected source image validation was unavailable or timed out") from error
    require(result.returncode == 0, "selected source image validation failed")


def clean(source: bool = False) -> None:
    run = source_command if source else command
    require(not run(["git", "status", "--porcelain", "--untracked-files=all"]),
            "release requires clean trusted and source checkouts")


def release_inputs() -> tuple[str, str, str, str]:
    revision = os.environ.get("RELEASE_REVISION", "")
    version = os.environ.get("RELEASE_VERSION", "")
    tag = os.environ.get("RELEASE_TAG", "")
    require(bool(re.fullmatch(r"[0-9a-f]{40}", revision)),
            "RELEASE_REVISION must be a full lowercase commit SHA")
    require(bool(re.fullmatch(r"1\.[0-9]+\.[0-9]+", version)),
            "RELEASE_VERSION must be a semantic 1.x version")
    require(tag == f"services/product-service/v{version}",
            "RELEASE_TAG does not match the Product scoped version")
    short = source_command(["git", "rev-parse", "--short", "HEAD"])
    require(bool(re.fullmatch(r"[0-9a-f]{7,40}", short)), "Git did not derive a unique short SHA")
    return revision, version, tag, short


def check() -> str:
    event = os.environ.get("GITHUB_EVENT_NAME")
    require(event in {"push", "workflow_dispatch"} and
            os.environ.get("GITHUB_REF") == "refs/heads/main" and
            os.environ.get("GITHUB_REPOSITORY") == REPOSITORY,
            "publication requires a canonical main push or explicit recovery dispatch")
    revision, _, _, _ = release_inputs()
    require(command(["git", "rev-parse", "HEAD"]) == os.environ.get("GITHUB_SHA"),
            "trusted main checkout differs from GITHUB_SHA")
    require(source_command(["git", "rev-parse", "HEAD"]) == revision,
            "selected source checkout differs from RELEASE_REVISION")
    if event == "push":
        require(os.environ.get("GITHUB_SHA") == revision,
                "push release revision differs from the event revision")
    command(["git", "merge-base", "--is-ancestor", revision, "origin/main"])
    clean()
    if SOURCE_ROOT != ROOT:
        clean(source=True)
    return revision


def metadata_path() -> Path:
    output = Path(os.environ.get("RELEASE_METADATA", ""))
    require(output.is_absolute() and output.parent.is_dir() and not output.exists(),
            "RELEASE_METADATA must be an unused absolute path in an existing directory")
    require(not output.is_relative_to(ROOT) and not output.is_relative_to(SOURCE_ROOT),
            "release metadata must be outside the checkouts")
    return output


def image_details(reference: str) -> dict:
    values = json.loads(command(["docker", "image", "inspect", reference]))
    require(isinstance(values, list) and len(values) == 1, "image inspection returned an invalid result")
    return values[0]


def digest_from_image(details: dict) -> str:
    matches = [value.split("@", 1)[1] for value in details.get("RepoDigests", [])
               if value.startswith(REGISTRY + "@")]
    require(len(set(matches)) == 1 and bool(matches), "image does not resolve to one registry digest")
    digest = matches[0]
    require(bool(re.fullmatch(r"sha256:[0-9a-f]{64}", digest)), "registry returned an invalid digest")
    return digest


def validate_registry_image(details: dict, revision: str, created: str) -> tuple[str, str]:
    labels = details.get("Config", {}).get("Labels", {})
    image_id = details.get("Id", "")
    require(bool(re.fullmatch(r"sha256:[0-9a-f]{64}", image_id)),
            "registry image has an invalid image ID")
    require(details.get("Os") == "linux" and details.get("Architecture") == "amd64" and
            labels.get("org.opencontainers.image.created") == created and
            labels.get("org.opencontainers.image.source") == f"https://github.com/{REPOSITORY}" and
            labels.get("org.opencontainers.image.revision") == revision and
            labels.get("org.opencontainers.image.version") == f"git-{revision[:12]}",
            "registry image identity differs from the selected source")
    return digest_from_image(details), image_id


def existing_tag(reference: str, revision: str, created: str) -> tuple[str, str] | None:
    probe = execute(["docker", "manifest", "inspect", reference])
    if probe.returncode != 0:
        diagnostic = (probe.stderr + probe.stdout).lower()
        require("manifest unknown" in diagnostic or "no such manifest" in diagnostic,
                "registry tag inspection failed; refusing to treat it as absent")
        return None
    command(["docker", "pull", "--platform", "linux/amd64", reference], timeout=300)
    details = image_details(reference)
    return validate_registry_image(details, revision, created)


def push_tag(local: str, reference: str) -> str:
    command(["docker", "tag", local, reference])
    pushed = command(["docker", "push", reference], timeout=600)
    digests = re.findall(r"digest: (sha256:[0-9a-f]{64})", pushed)
    require(len(set(digests)) == 1 and bool(digests),
            "push did not report one digest; inspect registry before retry")
    return digests[0]


def publish() -> None:
    revision = check()
    _, version, release_tag, short = release_inputs()
    output = metadata_path()
    local = f"insurance-hub-product-api:git-{revision[:12]}"
    references = [f"{REGISTRY}:{version}", f"{REGISTRY}:sha-{short}"]
    created = source_command(["git", "show", "-s", "--format=%cI", revision])

    # Validate the exact source before the first registry mutation.
    validate_source_image()
    clean()
    if SOURCE_ROOT != ROOT:
        clean(source=True)
    local_image_id = command(["docker", "image", "inspect", "--format", "{{.Id}}", local])
    require(bool(re.fullmatch(r"sha256:[0-9a-f]{64}", local_image_id)), "invalid local image ID")

    existing = {reference: existing_tag(reference, revision, created) for reference in references}
    accepted = [identity for identity in existing.values() if identity is not None]
    if accepted:
        require(len(accepted) == len(references) and len(set(accepted)) == 1,
                "partial or inconsistent existing release tags")
        digest, image_id = accepted[0]
    else:
        resolved = {reference: push_tag(local, reference) for reference in references}
        digests = set(resolved.values())
        require(len(digests) == 1, "semantic and short-SHA tags do not resolve to the same digest")
        digest = digests.pop()
        image_id = local_image_id
    immutable = f"{REGISTRY}@{digest}"
    command(["docker", "pull", "--platform", "linux/amd64", immutable], timeout=300)
    pulled = image_details(immutable)
    pulled_digest, pulled_image_id = validate_registry_image(pulled, revision, created)
    require(pulled_digest == digest and pulled_image_id == image_id,
            "published digest does not match the validated image")
    metadata = {"revision": revision, "version": version, "releaseTag": release_tag,
                "semanticTag": references[0], "shaTag": references[1], "image": immutable,
                "digest": digest, "imageId": image_id, "platform": "linux/amd64"}
    with output.open("x", encoding="utf-8") as stream:
        json.dump(metadata, stream, indent=2)
        stream.write("\n")
    print(json.dumps(metadata, sort_keys=True))


def read_metadata() -> tuple[Path, dict]:
    path = Path(os.environ.get("RELEASE_METADATA", ""))
    require(path.is_absolute() and path.is_file() and not path.is_relative_to(ROOT),
            "RELEASE_METADATA must identify the verified external metadata file")
    value = json.loads(path.read_text(encoding="utf-8"))
    revision, version, tag, short = release_inputs()
    expected = {"revision": revision, "version": version, "releaseTag": tag,
                "semanticTag": f"{REGISTRY}:{version}", "shaTag": f"{REGISTRY}:sha-{short}"}
    require(all(value.get(key) == item for key, item in expected.items()),
            "release metadata differs from the selected release identity")
    require(value.get("image") == f"{REGISTRY}@{value.get('digest', '')}" and
            bool(re.fullmatch(r"sha256:[0-9a-f]{64}", value.get("digest", ""))),
            "release metadata has an invalid immutable image")
    return path, value


def configure_git_identity() -> None:
    command(["git", "config", "user.name", "github-actions[bot]"])
    command(["git", "config", "user.email", "41898282+github-actions[bot]@users.noreply.github.com"])


def ensure_tag(tag: str, revision: str, version: str) -> None:
    result = execute(["git", "rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}"])
    if result.returncode == 0:
        require(result.stdout.strip() == revision, "scoped Git tag collision")
        require(command(["git", "cat-file", "-t", f"refs/tags/{tag}"]) == "tag",
                "existing scoped Git tag is not annotated")
        return
    command(["git", "tag", "-a", tag, revision, "-m", f"Release Product Go {version}"])
    command(["git", "push", "origin", f"refs/tags/{tag}"], timeout=120)


def release_body(metadata: dict) -> Path:
    notes = Path(os.environ.get("RELEASE_NOTES", ""))
    require(notes.is_absolute() and notes.is_file() and not notes.is_relative_to(ROOT) and
            not notes.is_relative_to(SOURCE_ROOT),
            "RELEASE_NOTES must be an external monotag notes file")
    body = notes.with_name("product-release-body.md")
    require(not body.exists(), "release body path already exists")
    text = notes.read_text(encoding="utf-8").rstrip() + (
        f"\n\nSource revision: `{metadata['revision']}`\n\n"
        f"Image: `{metadata['image']}`\n"
    )
    body.write_text(text, encoding="utf-8")
    return body


def ensure_github_release(tag: str, body: Path, revision: str, image: str) -> None:
    result = execute(["gh", "release", "view", tag, "--repo", REPOSITORY,
                      "--json", "tagName,body,isDraft,isPrerelease"])
    if result.returncode == 0:
        value = json.loads(result.stdout)
        identity = f"Source revision: `{revision}`\n\nImage: `{image}`"
        require(value.get("tagName") == tag and value.get("isDraft") is False and
                value.get("isPrerelease") is False and identity in value.get("body", ""),
                "existing GitHub Release differs from the accepted identity")
        return
    diagnostic = (result.stderr + result.stdout).lower()
    require("release not found" in diagnostic or "http 404" in diagnostic,
            "GitHub Release inspection failed; refusing to treat it as absent")
    command(["gh", "release", "create", tag, "--repo", REPOSITORY, "--verify-tag",
             "--title", tag, "--notes-file", str(body)], timeout=120)


def desired_manifest(text: str, digest: str) -> str:
    pattern = re.compile(
        r"(?m)^(    newName: " + re.escape(REGISTRY) +
        r"\n)    (?:newTag: [^\n]+|digest: sha256:[0-9a-f]{64})$"
    )
    updated, count = pattern.subn(r"\1    digest: " + digest, text)
    require(count == 1, "QA Product overlay image stanza is missing or ambiguous")
    return updated


def update_manifest(metadata: dict) -> None:
    revision = metadata["revision"]
    digest = metadata["digest"]
    version = metadata["version"]
    command(["git", "fetch", "origin", "main"], timeout=120)
    relative = str(MANIFEST.relative_to(ROOT))
    remote_text = command(["git", "show", f"origin/main:{relative}"]) + "\n"
    wanted = desired_manifest(remote_text, digest)
    if wanted == remote_text:
        subject = command(["git", "log", "-1", "--format=%s", "origin/main", "--", relative])
        require(subject == f"chore(k8s): update Product Go image to {version}",
                "existing QA digest was not written by this release identity")
        return
    newer_product = command([
        "git", "diff", "--name-only", f"{revision}..origin/main", "--",
        "services/product-service/Dockerfile", "services/product-service/go.mod",
        "services/product-service/go.sum", "services/product-service/cmd",
        "services/product-service/gen", "services/product-service/internal",
        "services/product-service/scripts/build-image.sh",
    ])
    require(not newer_product, "main contains a newer Product release source; refusing a stale digest update")
    command(["git", "checkout", "--detach", "origin/main"])
    MANIFEST.write_text(wanted, encoding="utf-8")
    command(["make", "go-svc-deploy-check", "SVC_NAME=product"], timeout=180)
    command(["git", "add", relative])
    staged = command(["git", "diff", "--cached", "--name-only"])
    require(staged == relative, "release attempted to stage an unexpected path")
    command(["git", "commit", "-m", f"chore(k8s): update Product Go image to {version}"])
    command(["git", "push", "origin", "HEAD:main"], timeout=120)


def finalize() -> None:
    check()
    _, metadata = read_metadata()
    revision, version, tag, _ = release_inputs()
    body = release_body(metadata)
    configure_git_identity()
    ensure_tag(tag, revision, version)
    ensure_github_release(tag, body, revision, metadata["image"])
    update_manifest(metadata)
    print(f"product-release: finalized {tag} at {metadata['image']}")


def main() -> None:
    require(len(sys.argv) == 2 and sys.argv[1] in {"check", "publish", "finalize"},
            "unknown release action")
    if sys.argv[1] == "check":
        print(f"product-release: verified source revision {check()}")
    elif sys.argv[1] == "publish":
        publish()
    else:
        finalize()


if __name__ == "__main__":
    try:
        main()
    except (ReleaseError, OSError, ValueError, KeyError, IndexError, json.JSONDecodeError) as error:
        print(f"product-release: {error}", file=sys.stderr)
        sys.exit(1)
