#!/usr/bin/env python3
"""Check generic Go release and ordinary validation workflow contracts."""

from copy import deepcopy
from fnmatch import fnmatchcase
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[3]


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def runs(job: dict) -> str:
    return "\n".join(step.get("run", "") for step in job["steps"])


def validate_release(workflow: dict) -> None:
    require(set(workflow["on"]) == {"push"}, "release must be push-only")
    require(workflow["on"]["push"] == {
        "branches": ["main"], "paths": ["services/*-service/**"],
    }, "release must select canonical-main standard Go service changes only")
    require(workflow["permissions"] == {"contents": "write", "packages": "write"},
            "release permissions must be limited to repository contents and packages")
    require(workflow["concurrency"] == {
        "group": "go-service-release", "cancel-in-progress": "false",
    }, "release concurrency must serialize without cancellation")

    job = workflow["jobs"]["release"]
    require(job["name"] == "Release changed Go service" and job["runs-on"] == "ubuntu-24.04",
            "generic release job identity differs")
    require(job["if"] == "github.repository == 'igor-baiborodine/insurance-hub'",
            "release must be restricted to the canonical repository")
    require(job["env"] == {
        "REGISTRY": "ghcr.io", "SOURCE_REVISION": "${{ github.sha }}",
    }, "release source or registry environment differs")

    steps = job["steps"]
    checkout = steps[0]
    require(checkout["uses"] == "actions/checkout@v6" and checkout["with"] == {
        "ref": "${{ github.sha }}", "fetch-depth": "0",
    }, "release must check out complete history at the triggering source revision")
    commands = runs(job)
    require("make go-svc-release-select" in commands and
            "RELEASE_OUTPUT=\"$GITHUB_OUTPUT\"" in commands,
            "release must use the shared exact-service selector")
    require("npx -y monotag tag" in commands and '--path "$MODULE_PATH"' in commands and
            '--prefix "services/$MODULE_NAME/v"' in commands and
            "--min-version 1.0.0" in commands and "--max-version 1.999.999" in commands,
            "release must calculate a scoped 1.x version")
    require(commands.count("docker build \\") == 1 and commands.count("docker push ") == 2 and
            '--tag "$image:$RELEASE_VERSION"' in commands and
            '--tag "$image:sha-$short_sha"' in commands,
            "release must build once and push semantic and short-SHA image tags")
    require("make go-svc-release-update-qa" in commands and
            "git push origin HEAD:main" in commands and
            'git tag -a "$RELEASE_TAG" -m' in commands and
            'git push origin "$RELEASE_TAG"' in commands,
            "release must create the scoped tag and commit the semantic QA tag")

    tag_steps = [step for step in steps if step.get("name") == "Create scoped source tag"]
    require(len(tag_steps) == 1 and tag_steps[0].get("env") == {
                "MODULE_NAME": "${{ steps.service.outputs.module }}",
                "RELEASE_TAG": "${{ steps.monotag.outputs.tag }}",
                "RELEASE_VERSION": "${{ steps.monotag.outputs.version }}",
            }, "release scoped tag environment differs")
    tag_commands = tag_steps[0].get("run", "")
    require('test "$(git rev-parse HEAD)" = "$SOURCE_REVISION"' in tag_commands and
            'git config user.name "github-actions[bot]"' in tag_commands and
            'git config user.email "github-actions[bot]@users.noreply.github.com"' in tag_commands and
            'git tag -a "$RELEASE_TAG" -m "Release $MODULE_NAME $RELEASE_VERSION"' in tag_commands and
            'git push origin "$RELEASE_TAG"' in tag_commands,
            "release must tag the triggering source with the legacy Git commands")
    names = [step.get("name") for step in steps]
    require(names.index("Build once and push semantic and source tags") <
            names.index("Create scoped source tag") <
            names.index("Commit semantic QA image tag") <
            names.index("Create GitHub Release"),
            "release mutation order differs")

    release_steps = [step for step in steps if step.get("uses") == "ncipollo/release-action@v1"]
    require(len(release_steps) == 1 and release_steps[0]["with"] == {
        "allowUpdates": "false", "draft": "false", "makeLatest": "true",
        "name": "${{ steps.monotag.outputs.tag }}", "tag": "${{ steps.monotag.outputs.tag }}",
        "bodyFile": "${{ steps.monotag.outputs.notes }}", "token": "${{ secrets.GITHUB_TOKEN }}",
    }, "release must create one non-draft GitHub Release from the scoped source tag")
    forbidden = ("workflow_dispatch", "git worktree", "@sha256:", "upload-artifact", "setup-go",
                 "go test", "kubectl", "kind create", "postgres-svc-role", "image-publish",
                 "release-finalize")
    require(not any(value in str(workflow) for value in forbidden),
            "release contains recovery, digest, package, full-CI, or deployment behavior")


def validate_modules(workflow: dict) -> None:
    require(set(workflow["on"]) == {"push", "pull_request"},
            "Go validation must use push and pull_request only")
    expected_paths = {
        "services/product-service/Dockerfile", "scripts/go/service/release.py",
        "scripts/postgres/service/catalog.json", "scripts/product-deployment/reader.py",
        "scripts/product-baseline/test.sh", "scripts/product-security/verify.sh",
        "k8s/apps/svc/product/base/deployment.yaml",
        "k8s/overlays/local-dev/svc/product/kustomization.yaml",
        "k8s/overlays/qa/svc/product/kustomization.yaml", "k8s/flux/qa/kustomization.yaml",
        "docs/migration/phase-4/product-service/deployment-runbook.md",
        ".github/workflows/go-release-svc.yml", ".github/workflows/go-modules.yml",
    }
    for event in ("push", "pull_request"):
        trigger = workflow["on"][event]
        require(trigger["branches"] == ["main"], "Go validation must target main")
        patterns = trigger["paths"]
        for path in expected_paths:
            require(any(fnmatchcase(path, pattern) for pattern in patterns),
                    f"Go validation does not cover {event} path: {path}")

    require(workflow["permissions"] == {"contents": "read", "packages": "read"},
            "Go validation permissions must be read-only")
    job = workflow["jobs"]["validate"]
    require(job["env"] == {"GITHUB_TOKEN": "${{ secrets.GITHUB_TOKEN }}"},
            "Go validation package token wiring differs")
    java = [step for step in job["steps"] if step.get("uses") == "actions/setup-java@v5"]
    require(len(java) == 1 and java[0]["with"] == {
        "java-version": "14", "distribution": "zulu", "cache": "maven",
        "server-id": "github", "server-username": "GITHUB_ACTOR",
        "server-password": "GITHUB_TOKEN",
    }, "Go validation must retain Java baseline package access")
    commands = runs(job)
    for command in (
        "make -C services/product-service bootstrap-ci-tools",
        "make -C services/product-service check-ci",
        "make go-svc-release-test",
        "make go-svc-dispatch-test",
        "make go-svc-deploy-check SVC_NAME=product",
        "make go-svc-deploy-test-tooling SVC_NAME=product",
        "make postgres-svc-role-test",
        "make product-baseline-fixtures-check",
        "make product-baseline-test BASELINE_SUITE=db-permissions",
        "make go-svc-image-check SVC_NAME=product",
    ):
        require(command in commands, f"Go validation is missing nonpublishing check: {command}")
    require(not any(value in commands for value in ("docker push", "go-svc-release-update-qa",
                                                     "gh release", "git push")),
            "ordinary Go validation may not publish or update QA")


def main() -> None:
    def read(name: str) -> dict:
        return yaml.load((ROOT / ".github" / "workflows" / name).read_text(encoding="utf-8"),
                         Loader=yaml.BaseLoader)

    release = read("go-release-svc.yml")
    modules = read("go-modules.yml")
    validate_release(release)
    validate_modules(modules)
    require(not (ROOT / ".github/workflows/product-image-release.yml").exists() and
            not (ROOT / ".github/workflows/product-deployment.yml").exists(),
            "retired Product workflows still exist")

    corruptions = [
        lambda release, modules: release["on"].update(workflow_dispatch={}),
        lambda release, modules: release["permissions"].update(actions="write"),
        lambda release, modules: release["on"]["push"]["paths"].append("k8s/overlays/qa/svc/**"),
        lambda release, modules: release["jobs"]["release"].update({"if": "true"}),
        lambda release, modules: next(step for step in release["jobs"]["release"]["steps"]
                                      if step.get("name") == "Create scoped source tag")
        .update(run='git tag -a "$RELEASE_TAG"'),
        lambda release, modules: next(step for step in release["jobs"]["release"]["steps"]
                                      if step.get("name") == "Build once and push semantic and source tags")
        .update(run="docker build ."),
        lambda release, modules: modules["permissions"].update(packages="write"),
        lambda release, modules: modules["on"]["pull_request"]["paths"]
        .remove(".github/workflows/go-release-svc.yml"),
    ]
    for corrupt in corruptions:
        bad_release, bad_modules = deepcopy(release), deepcopy(modules)
        corrupt(bad_release, bad_modules)
        try:
            validate_release(bad_release)
            validate_modules(bad_modules)
        except (KeyError, ValueError):
            continue
        raise ValueError("controlled workflow corruption was accepted")
    print("check-ci: generic release, ordinary validation, retired workflows, and eight corruptions passed")


if __name__ == "__main__":
    main()
