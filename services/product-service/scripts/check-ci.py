#!/usr/bin/env python3
"""Check generic Go release and selected-module PR validation contracts."""

from copy import deepcopy
import os
from pathlib import Path
import re
import subprocess
import tempfile

import yaml


ROOT = Path(__file__).resolve().parents[3]

QUALIFYING_PATHS = [
    "templates/go-service/**/*.go",
    "templates/go-service/go.mod",
    "templates/go-service/go.sum",
    "templates/go-service/.golangci.yml",
    "templates/go-service/buf.yaml",
    "templates/go-service/buf.lock",
    "templates/go-service/buf.gen.yaml",
    "templates/go-service/api/**/*.proto",
    "templates/go-service/testdata/contract-baseline/**",
    "services/*-service/**/*.go",
    "services/*-service/go.mod",
    "services/*-service/go.sum",
    "services/*-service/.golangci.yml",
    "services/*-service/buf.yaml",
    "services/*-service/buf.lock",
    "services/*-service/buf.gen.yaml",
    "services/*-service/sqlc.yaml",
    "services/*-service/api/**/*.proto",
    "services/*-service/internal/postgres/sql/*.sql",
    "services/*-service/testdata/contract-baseline/**",
]


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def runs(job: dict) -> str:
    return "\n".join(step.get("run", "") for step in job["steps"])


def step_named(job: dict, name: str) -> dict:
    matches = [step for step in job["steps"] if step.get("name") == name]
    require(len(matches) == 1, f"expected one workflow step named {name!r}")
    return matches[0]


def normalize(value: str) -> str:
    return " ".join(value.split())


def validate_release(workflow: dict) -> None:
    require(set(workflow["on"]) == {"push"}, "release must be push-only")
    require(workflow["on"]["push"] == {
        "branches": ["main"],
        "paths": [
            "services/*-service/**",
            "!services/*-service/scripts/check-ci.py",
            "!services/*-service/scripts/check-render.py",
            "!services/*-service/scripts/test-deployment-checks.py",
        ],
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
            '--prefix "$MODULE_NAME/v"' in commands and
            '[[ "$release_tag" == "$MODULE_NAME/v$release_version" ]]' in commands and
            "--min-version 1.0.0" in commands and "--max-version 1.999.999" in commands,
            "release must calculate a module-scoped 1.x version")
    require(commands.count("docker build \\") == 1 and commands.count("docker push ") == 2 and
            '--tag "$image:$RELEASE_VERSION"' in commands and
            '--tag "$image:sha-$short_sha"' in commands,
            "release must build once and push semantic and short-SHA image tags")
    require("make go-svc-release-update-qa" in commands and
            "git push origin HEAD:main" in commands and
            "git diff --cached --quiet" in commands and
            'git tag -a "$RELEASE_TAG" -m' in commands and
            'git push origin "$RELEASE_TAG"' in commands,
            "release must create the scoped tag and update the semantic QA tag when needed")

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
    forbidden = ("git worktree", "@sha256:", "upload-artifact", "setup-go", "go test",
                 "kubectl", "kind create", "postgres-svc-role", "image-publish",
                 "release-finalize")
    require(not any(value in str(workflow) for value in forbidden),
            "release contains recovery, digest, package, full-CI, or deployment behavior")


def path_matches(path: str, patterns: list[str]) -> bool:
    def github_glob(pattern: str) -> str:
        result = []
        index = 0
        while index < len(pattern):
            if pattern.startswith("**/", index):
                result.append("(?:.*/)?")
                index += 3
            elif pattern.startswith("**", index):
                result.append(".*")
                index += 2
            elif pattern[index] == "*":
                result.append("[^/]*")
                index += 1
            elif pattern[index] == "?":
                result.append("[^/]")
                index += 1
            else:
                result.append(re.escape(pattern[index]))
                index += 1
        return "".join(result)

    return any(re.fullmatch(github_glob(pattern), path) for pattern in patterns)


def validate_trigger_examples(patterns: list[str]) -> None:
    qualifying = [
        "templates/go-service/internal/health/health.go",
        "templates/go-service/gen/scaffold/v1/example_service.pb.go",
        "templates/go-service/go.mod",
        "templates/go-service/go.sum",
        "templates/go-service/.golangci.yml",
        "templates/go-service/buf.yaml",
        "templates/go-service/buf.lock",
        "templates/go-service/buf.gen.yaml",
        "templates/go-service/api/scaffold/v1/example_service.proto",
        "templates/go-service/testdata/contract-baseline/buf.yaml",
        "services/product-service/internal/testing/fixture_test.go",
        "services/product-service/gen/product/v1/product_service.pb.go",
        "services/product-service/go.mod",
        "services/product-service/go.sum",
        "services/product-service/.golangci.yml",
        "services/product-service/buf.yaml",
        "services/product-service/buf.lock",
        "services/product-service/buf.gen.yaml",
        "services/product-service/sqlc.yaml",
        "services/product-service/api/product/v1/product_service.proto",
        "services/product-service/internal/postgres/sql/schema.sql",
        "services/product-service/testdata/contract-baseline/buf.yaml",
        "services/claims-service/internal/testing/fixture_test.go",
        "services/claims-service/gen/claims/v1/claims_service.pb.go",
        "services/claims-service/go.mod",
        "services/claims-service/go.sum",
        "services/claims-service/.golangci.yml",
        "services/claims-service/buf.yaml",
        "services/claims-service/buf.lock",
        "services/claims-service/buf.gen.yaml",
        "services/claims-service/sqlc.yaml",
        "services/claims-service/api/claims/v1/claims_service.proto",
        "services/claims-service/internal/postgres/sql/schema.sql",
        "services/claims-service/testdata/contract-baseline/buf.yaml",
    ]
    excluded = [
        "templates/go-service/Makefile",
        "templates/go-service/README.md",
        "templates/go-service/scripts/test-tooling.sh",
        "services/product-service/Makefile",
        "services/product-service/README.md",
        "services/product-service/Dockerfile",
        "services/product-service/scripts/test-integration.sh",
        "services/claims-service/Makefile",
        "services/claims-service/README.md",
        "services/claims-service/scripts/test-integration.sh",
        "services/claims/incorrect.go",
        "services/group/claims-service/incorrect.go",
        "services/claims-service-tools/incorrect.go",
        "scripts/go/module/common.mk",
        "go-module-topology.json",
        "codecov.yml",
        ".github/workflows/go-pr-mod.yml",
        "legacy/product-service/src/test/resources/product-read-baseline/qa-products.json",
        "k8s/apps/svc/product/base/deployment.yaml",
    ]
    for path in qualifying:
        require(path_matches(path, patterns), f"Go PR trigger excludes qualifying path: {path}")
    for path in excluded:
        require(not path_matches(path, patterns), f"Go PR trigger includes excluded path: {path}")


def parse_outputs(path: Path) -> dict[str, str]:
    result = {}
    if path.exists():
        for line in path.read_text(encoding="utf-8").splitlines():
            key, value = line.split("=", 1)
            result[key] = value
    return result


def exercise_selection(selector: dict) -> None:
    cases = [
        ("root-only no-op", {"Makefile": "all:\n\t@true\n"}, 0, ""),
        ("scaffold only", {"templates/go-service/main.go": "package main\n"}, 0,
         "templates/go-service"),
        ("Product only", {"services/product-service/main.go": "package main\n"}, 0,
         "services/product-service"),
        ("future service only", {"services/claims-service/main.go": "package main\n"}, 0,
         "services/claims-service"),
        ("Go plus root auxiliary", {
            "services/product-service/main.go": "package main\n",
            "scripts/go/helper.sh": "#!/usr/bin/env bash\n",
        }, 0, "services/product-service"),
        ("qualifying plus second-module auxiliary", {
            "services/product-service/main.go": "package main\n",
            "templates/go-service/README.md": "fixture\n",
        }, 1, None),
        ("scaffold and business service", {
            "services/product-service/main.go": "package main\n",
            "templates/go-service/main.go": "package main\n",
        }, 1, None),
        ("two business services", {
            "services/product-service/main.go": "package main\n",
            "services/claims-service/README.md": "fixture\n",
        }, 1, None),
    ]
    with tempfile.TemporaryDirectory(prefix="insurance-hub-go-pr-select-") as temporary:
        fixture = Path(temporary)
        subprocess.run(["git", "init", "-q"], cwd=fixture, check=True, timeout=10)
        subprocess.run(["git", "config", "user.name", "CI Contract"], cwd=fixture, check=True,
                       timeout=10)
        subprocess.run(["git", "config", "user.email", "ci-contract@example.invalid"],
                       cwd=fixture, check=True, timeout=10)
        marker = fixture / ".fixture"
        marker.write_text("base\n", encoding="utf-8")
        subprocess.run(["git", "add", "."], cwd=fixture, check=True, timeout=10)
        subprocess.run(["git", "commit", "-qm", "base"], cwd=fixture, check=True, timeout=10)
        base = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=fixture, text=True,
                                       timeout=10).strip()

        for index, (label, changes, expected_status, expected_module) in enumerate(cases):
            subprocess.run(["git", "reset", "--hard", "-q", base], cwd=fixture, check=True,
                           timeout=10)
            subprocess.run(["git", "clean", "-fdq"], cwd=fixture, check=True, timeout=10)
            for relative, content in changes.items():
                target = fixture / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(content, encoding="utf-8")
            subprocess.run(["git", "add", "."], cwd=fixture, check=True, timeout=10)
            subprocess.run(["git", "commit", "-qm", f"case-{index}"], cwd=fixture, check=True,
                           timeout=10)
            head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=fixture,
                                           text=True, timeout=10).strip()
            output = fixture / f"github-output-{index}"
            environment = os.environ.copy()
            environment.update({"GITHUB_OUTPUT": str(output), "PR_BASE": base, "PR_HEAD": head})
            completed = subprocess.run(["bash", "-c", selector["run"]], cwd=fixture,
                                       env=environment, text=True, capture_output=True, check=False,
                                       timeout=10)
            require((completed.returncode == 0) == (expected_status == 0),
                    f"selection case {label!r} returned {completed.returncode}: "
                    f"{completed.stderr.strip()}")
            if expected_status == 0:
                actual = parse_outputs(output).get("module_dir")
                require(actual == expected_module,
                        f"selection case {label!r} chose {actual!r}, expected {expected_module!r}")
            else:
                require("Multiple Go modules changed" in completed.stderr,
                        f"selection case {label!r} did not explain the multi-module failure")


def exercise_coverage_path(validation: dict, upload: dict) -> None:
    module_dir = "services/claims-service"
    coverage_relative = f"{module_dir}/coverage.txt"
    with tempfile.TemporaryDirectory(prefix="insurance-hub-go-pr-coverage-") as temporary:
        fixture = Path(temporary)
        module = fixture / module_dir
        module.mkdir(parents=True)
        (module / "Makefile").write_text(
            ".PHONY: check-go-pr\n"
            "check-go-pr:\n"
            "\t@mkdir -p \"$(dir $(COVERAGE_FILE))\"\n"
            "\t@printf 'mode: atomic\\n' > \"$(COVERAGE_FILE)\"\n"
            "\t@test \"$(FORMAT_SCOPE)\" = changed\n"
            "\t@test -n \"$(FORMAT_BASE)\"\n",
            encoding="utf-8",
        )
        upload_path = fixture / coverage_relative
        upload_path.write_text("stale\n", encoding="utf-8")
        environment = os.environ.copy()
        environment.update({
            "COVERAGE_FILE": str(upload_path),
            "MODULE_DIR": module_dir,
            "MODULE_ROLE": "business-service",
            "PR_BASE": "fixture-base",
            "VALIDATION_TARGET": "check-go-pr",
        })
        completed = subprocess.run(
            ["bash", "-c", validation["run"]], cwd=fixture, env=environment,
            text=True, capture_output=True, check=False, timeout=10,
        )
        require(completed.returncode == 0,
                f"coverage path execution returned {completed.returncode}: "
                f"{completed.stderr.strip()}")
        selected_output = "${{ steps.select.outputs.coverage_file }}"
        require(upload["with"]["files"] == selected_output,
                "coverage path fixture requires the selected report output for upload")
        resolved_upload = fixture / upload["with"]["files"].replace(
            selected_output, coverage_relative)
        require(upload_path == resolved_upload and resolved_upload.read_text(encoding="utf-8") ==
                "mode: atomic\n",
                "make -C coverage output does not match the Codecov upload path")
        duplicated = module / coverage_relative
        require(not duplicated.exists(),
                "make -C generated coverage under a duplicated module path")


def validate_modules(workflow: dict, *, run_execution_cases: bool = False) -> None:
    require(set(workflow["on"]) == {"pull_request"},
            "Go PR validation must be pull_request-only")
    trigger = workflow["on"]["pull_request"]
    require(trigger["branches"] == ["main"], "Go PR validation must target main")
    require(trigger["paths"] == QUALIFYING_PATHS,
            "Go PR validation qualifying path matrix differs")
    validate_trigger_examples(trigger["paths"])
    require(workflow["permissions"] == {"contents": "read"},
            "Go PR validation permissions must be read-only")
    require(workflow["concurrency"] == {
        "group": "go-pr-mod-${{ github.event.pull_request.number }}",
        "cancel-in-progress": "true",
    }, "Go PR validation must cancel superseded runs for the same pull request")
    require(set(workflow["jobs"]) == {"validate"}, "Go PR validation must use one validation job")

    job = workflow["jobs"]["validate"]
    require(job["name"] == "Validate selected Go module" and job["runs-on"] == "ubuntu-24.04",
            "Go PR validation job identity differs")
    checkout = job["steps"][0]
    require(checkout["uses"] == "actions/checkout@v6" and checkout["with"] == {
        "ref": "${{ github.event.pull_request.head.sha }}", "fetch-depth": "0",
    }, "Go PR validation must check out complete pull request head history")

    selector = step_named(job, "Select Go module")
    require(selector.get("id") == "select" and selector.get("shell") == "bash",
            "Go PR module selection identity differs")
    require(selector.get("env") == {
        "PR_BASE": "${{ github.event.pull_request.base.sha }}",
        "PR_HEAD": "${{ github.event.pull_request.head.sha }}",
    }, "Go PR module selection must use explicit pull request base and head revisions")
    selector_contract = [
        "set -euo pipefail",
        'git diff --name-only --diff-filter=ACDMRT -z "$PR_BASE" "$PR_HEAD"',
        "templates/go-service/*)",
        "services/*)",
        'service_path=${changed_path#services/}',
        'service_name=${service_path%%/*}',
        '[[ "$service_name" == *-service && "$service_path" == */* ]]',
        'selected["services/$service_name"]=1',
        "if (( ${#modules[@]} == 0 ))",
        "if (( ${#modules[@]} > 1 ))",
        "Multiple Go modules changed; split the pull request",
        "module_role='service-scaffold'",
        "services/*-service)",
        "module_role='business-service'",
        'echo "go_sum=$module_dir/go.sum"',
        "echo 'validation_target=check-go-pr'",
        'echo "coverage_file=$coverage_file"',
        'echo "coverage_flag=$coverage_flag"',
    ]
    for fragment in selector_contract:
        require(fragment in selector["run"], f"Go PR module selector is missing: {fragment}")
    if run_execution_cases:
        exercise_selection(selector)

    setup_go = step_named(job, "Set up Go 1.27.2")
    require(setup_go.get("if") == "steps.select.outputs.module_dir != ''" and
            setup_go.get("uses") == "actions/setup-go@v7" and setup_go.get("with") == {
                "go-version": "1.27.2", "cache": "true",
                "cache-dependency-path": "${{ steps.select.outputs.go_sum }}",
            }, "Go setup must use only the selected module dependency cache")
    tools_cache = step_named(job, "Restore selected module validation tools")
    require(tools_cache.get("if") == "steps.select.outputs.module_dir != ''" and
            tools_cache.get("uses") == "actions/cache@v5" and
            tools_cache["with"]["path"] == "${{ steps.select.outputs.module_dir }}/.tools" and
            "steps.select.outputs.module_key" in tools_cache["with"]["key"] and
            "scripts/go/module/common.mk" in tools_cache["with"]["key"],
            "pinned-tool cache must be scoped to the selected module and tool configuration")

    bootstrap = step_named(job, "Bootstrap and verify selected module tools")
    require(bootstrap.get("if") == "steps.select.outputs.module_dir != ''" and
            bootstrap.get("env") == {"MODULE_DIR": "${{ steps.select.outputs.module_dir }}"} and
            bootstrap.get("run") == 'make -C "$MODULE_DIR" bootstrap-go-tools',
            "selected module tools must be installed and version-verified after cache restore")
    validation = step_named(job, "Run selected module validation")
    require(validation.get("if") == "steps.select.outputs.module_dir != ''" and
            validation.get("env") == {
                "COVERAGE_FILE": "${{ github.workspace }}/${{ steps.select.outputs.coverage_file }}",
                "MODULE_DIR": "${{ steps.select.outputs.module_dir }}",
                "MODULE_ROLE": "${{ steps.select.outputs.module_role }}",
                "PR_BASE": "${{ github.event.pull_request.base.sha }}",
                "VALIDATION_TARGET": "${{ steps.select.outputs.validation_target }}",
            }, "selected module validation output wiring differs")
    require('rm -f -- "$COVERAGE_FILE"' in validation["run"] and
            "if [[ \"$MODULE_ROLE\" != 'service-scaffold' ]]" in validation["run"] and
            'make -C "$MODULE_DIR" "$VALIDATION_TARGET"' in validation["run"] and
            'FORMAT_SCOPE=changed FORMAT_BASE="$PR_BASE"' in validation["run"],
            "selected module validation must clear current coverage and invoke its owning target")

    upload = step_named(job, "Upload selected module coverage to Codecov")
    require(normalize(upload.get("if", "")) == normalize(
        "success() && steps.select.outputs.module_dir != '' && "
        "steps.select.outputs.module_role != 'service-scaffold'"),
        "Codecov upload must run only after successful non-scaffold validation")
    require(upload.get("uses") == "codecov/codecov-action@v7" and upload.get("with") == {
        "token": "${{ secrets.CODECOV_TOKEN }}",
        "files": "${{ steps.select.outputs.coverage_file }}",
        "flags": "${{ steps.select.outputs.coverage_flag }}",
        "name": "${{ steps.select.outputs.coverage_flag }}",
        "disable_search": "true",
        "fail_ci_if_error": "true",
    }, "Codecov upload must use only the selected module's derived current report")
    require("services/product-service" not in str(upload),
            "generic coverage upload may not hard-code Product")
    if run_execution_cases:
        exercise_coverage_path(validation, upload)

    commands = runs(job)
    forbidden = (
        "setup-java", "setup-node", "go-modules-check", "go-topology-test",
        "go-scaffold-test-tooling", "go-product-test-tooling", "go-scaffold-check-copy",
        "check-ci", "go-svc-", "postgres-svc-role", "product-baseline", "product-security",
        "kubectl", "kind create", "docker build", "git push", "gh release",
    )
    require(not any(value in str(workflow) for value in forbidden),
            "Go PR validation contains all-module, documentation, release, deployment, or "
            "image work")
    require("make -C services/product-service" not in commands and
            "make -C templates/go-service" not in commands,
            "Go PR validation may not set up or validate an unselected module")


def main() -> None:
    def read(name: str) -> dict:
        return yaml.load((ROOT / ".github" / "workflows" / name).read_text(encoding="utf-8"),
                         Loader=yaml.BaseLoader)

    release = read("go-release-svc.yml")
    modules = read("go-pr-mod.yml")
    validate_release(release)
    validate_modules(modules, run_execution_cases=True)
    require(not (ROOT / ".github/workflows/go-modules.yml").exists(),
            "retired all-module Go workflow still exists")
    require(not (ROOT / ".github/workflows/product-image-release.yml").exists() and
            not (ROOT / ".github/workflows/product-deployment.yml").exists(),
            "retired Product workflows still exist")

    release_corruptions = [
        lambda item: item["on"].update(workflow_dispatch={}),
        lambda item: item["permissions"].update(actions="write"),
        lambda item: item["on"]["push"]["paths"].append("k8s/overlays/qa/svc/**"),
        lambda item: item["jobs"]["release"].update({"if": "true"}),
        lambda item: step_named(item["jobs"]["release"], "Create scoped source tag")
        .update(run='git tag -a "$RELEASE_TAG"'),
        lambda item: step_named(item["jobs"]["release"],
                                "Build once and push semantic and source tags")
        .update(run="docker build ."),
    ]
    for corrupt in release_corruptions:
        bad_release = deepcopy(release)
        corrupt(bad_release)
        try:
            validate_release(bad_release)
        except (KeyError, ValueError):
            continue
        raise ValueError("controlled release workflow corruption was accepted")

    module_corruptions = [
        lambda item: item["on"].update(push={"branches": ["main"]}),
        lambda item: item["on"]["pull_request"]["paths"].pop(),
        lambda item: item["on"]["pull_request"]["paths"].append("services/*-service/**"),
        lambda item: item["jobs"]["validate"]["steps"].append(
            {"name": "Forbidden Java setup", "uses": "actions/setup-java@v5"}),
        lambda item: step_named(item["jobs"]["validate"],
                                "Bootstrap and verify selected module tools")
        .update(run='make -C "$MODULE_DIR" bootstrap-go-tools\n'
                    "make -C templates/go-service bootstrap-go-tools"),
        lambda item: step_named(item["jobs"]["validate"], "Run selected module validation")
        .update(run="echo validation omitted"),
        lambda item: step_named(item["jobs"]["validate"], "Set up Go 1.27.2")["with"]
        .update({"cache-dependency-path": "services/product-service/go.sum"}),
        lambda item: step_named(item["jobs"]["validate"], "Run selected module validation")
        .update(run=step_named(item["jobs"]["validate"], "Run selected module validation")["run"]
                .replace('rm -f -- "$COVERAGE_FILE"', ":")),
        lambda item: step_named(item["jobs"]["validate"], "Run selected module validation")
        ["env"].update({"COVERAGE_FILE": "${{ steps.select.outputs.coverage_file }}"}),
        lambda item: step_named(item["jobs"]["validate"],
                                "Upload selected module coverage to Codecov")
        .update({"if": "success()"}),
        lambda item: step_named(item["jobs"]["validate"],
                                "Bootstrap and verify selected module tools")
        .update(run='make -C "$MODULE_DIR" verify-tools'),
    ]
    for corrupt in module_corruptions:
        bad_modules = deepcopy(modules)
        corrupt(bad_modules)
        try:
            validate_modules(bad_modules)
        except (KeyError, ValueError):
            continue
        raise ValueError("controlled Go PR workflow corruption was accepted")

    print("check-ci: generic release, selected-module PR validation, eight selection cases, "
          "one coverage path execution case, and seventeen controlled corruptions passed")


if __name__ == "__main__":
    main()
