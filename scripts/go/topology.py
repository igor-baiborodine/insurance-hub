#!/usr/bin/env python3
"""Validate Go module topology, CI coverage, boundaries, and aggregate checks."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import signal
import subprocess
import sys
import time
from collections.abc import Mapping, Sequence
from pathlib import Path, PurePosixPath
from typing import Any

COMMAND_TIMEOUT_SECONDS = 600
HEARTBEAT_SECONDS = 30
ROLES = frozenset(
    {"service-scaffold", "business-service", "public-contract", "public-shared", "tool"}
)
CATEGORIES = frozenset(
    {
        "repository-metadata",
        "local-ticket-artifact",
        "tool-cache",
        "dependency-tree",
        "fixture",
        "generated-copy",
    }
)


class TopologyError(RuntimeError):
    """The repository Go topology contract is invalid."""


class TopologyTerminated(Exception):
    """The CLI received a handled termination signal."""


def handle_signal(signum: int, _frame: object) -> None:
    raise TopologyTerminated(signum)


def terminate_process(process: subprocess.Popen[Any]) -> None:
    if process.poll() is not None:
        process.communicate()
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        process.communicate(timeout=10)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.communicate(timeout=10)


def require(condition: bool, message: str) -> None:
    if not condition:
        raise TopologyError(message)


def command_environment(**values: str) -> dict[str, str]:
    environment = {
        "PATH": os.environ.get("PATH", ""),
        "HOME": os.environ.get("HOME", ""),
        "TMPDIR": os.environ.get("TMPDIR", "/tmp"),
    }
    environment.update(values)
    return environment


def run_command(
    arguments: Sequence[str],
    *,
    cwd: Path,
    activity: str,
    env: Mapping[str, str] | None = None,
    check: bool = True,
    text: bool = True,
    timeout_seconds: float = COMMAND_TIMEOUT_SECONDS,
) -> subprocess.CompletedProcess[Any]:
    started = time.monotonic()
    process = subprocess.Popen(
        list(arguments),
        cwd=cwd,
        env=dict(env or command_environment()),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=text,
        start_new_session=True,
    )
    try:
        while True:
            elapsed = time.monotonic() - started
            remaining = timeout_seconds - elapsed
            if remaining <= 0:
                terminate_process(process)
                raise TopologyError(f"{activity} exceeded its {timeout_seconds:g}-second deadline")
            try:
                stdout, stderr = process.communicate(timeout=min(remaining, HEARTBEAT_SECONDS))
                break
            except subprocess.TimeoutExpired:
                print(f"{activity}: still running after {elapsed:.0f}s", file=sys.stderr)
    except BaseException:
        terminate_process(process)
        raise
    result = subprocess.CompletedProcess(list(arguments), process.returncode, stdout, stderr)
    if check and result.returncode != 0:
        raise TopologyError(f"{activity} failed with status {result.returncode}")
    return result


def fixture_paths(prefix: str) -> tuple[Path, Path]:
    script_root = Path(__file__).resolve().parents[2]
    fixture_root = os.environ.get("GO_TOPOLOGY_FIXTURE_ROOT", "")
    fixture_inventory = os.environ.get("GO_TOPOLOGY_FIXTURE_INVENTORY", "")
    if fixture_root or fixture_inventory:
        require(
            os.environ.get("GO_TOPOLOGY_TEST_MODE", "0") == "1",
            f"{prefix}: fixture inputs require GO_TOPOLOGY_TEST_MODE=1",
        )
        require(
            bool(fixture_root and fixture_inventory),
            f"{prefix}: fixture root and inventory must be provided together",
        )
        root = Path(fixture_root).resolve(strict=True)
        require(
            (root / ".go-topology-test-fixture").is_file(),
            f"{prefix}: fixture root is missing its test marker",
        )
        inventory = Path(fixture_inventory).resolve(strict=True)
        require(
            inventory.is_relative_to(root),
            f"{prefix}: fixture inventory must be inside the fixture root",
        )
        return root, inventory
    return script_root, script_root / "go-module-topology.json"


def nonempty_string(value: object, path: str) -> str:
    require(
        isinstance(value, str) and bool(value) and not any(char in value for char in "\t\r\n"),
        f"go-module-topology.json: {path} must be a non-empty single-line string",
    )
    return value


def relative_directory(value: object, path: str, *, allow_root: bool = False) -> str:
    text = nonempty_string(value, path)
    pure = PurePosixPath(text)
    valid = (text == "." and allow_root) or (
        not pure.is_absolute()
        and text == pure.as_posix()
        and not text.endswith("/")
        and "." not in pure.parts
        and ".." not in pure.parts
    )
    require(
        valid, f"go-module-topology.json: {path} must be a normalized repository-relative directory"
    )
    return text


def string_array(value: object, path: str, *, allow_empty: bool) -> list[str]:
    require(isinstance(value, list), f"go-module-topology.json: {path} must be an array")
    require(allow_empty or bool(value), f"go-module-topology.json: {path} must not be empty")
    result = [nonempty_string(item, path) for item in value]
    require(len(set(result)) == len(result), f"go-module-topology.json: {path} contains duplicates")
    return result


def exact_keys(value: dict[str, Any], expected: set[str], path: str) -> None:
    difference = sorted(set(value) ^ expected)
    require(
        not difference,
        f"go-module-topology.json: {path} has missing or unknown fields: {', '.join(difference)}",
    )


def load_inventory(path: Path) -> dict[str, Any]:
    require(path.is_file(), "missing inventory: go-module-topology.json")
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise TopologyError("inventory schema validation failed") from error
    try:
        require(isinstance(value, dict), "go-module-topology.json: root must be an object")
        exact_keys(
            value,
            {"schemaVersion", "repositoryModulePrefix", "modules", "workspaces", "exclusions"},
            "root",
        )
        require(value["schemaVersion"] == 1, "go-module-topology.json: schemaVersion must equal 1")
        prefix = nonempty_string(value["repositoryModulePrefix"], "repositoryModulePrefix")
        require(
            bool(re.fullmatch(r"[A-Za-z0-9.-]+(?:/[A-Za-z0-9._~-]+)*/", prefix)),
            "go-module-topology.json: repositoryModulePrefix must be a slash-terminated module path prefix",
        )
        require(
            isinstance(value["modules"], list) and bool(value["modules"]),
            "go-module-topology.json: modules must be a non-empty array",
        )
        require(
            isinstance(value["workspaces"], list),
            "go-module-topology.json: workspaces must be an array",
        )
        require(
            isinstance(value["exclusions"], list),
            "go-module-topology.json: exclusions must be an array",
        )
        for index, module in enumerate(value["modules"]):
            require(
                isinstance(module, dict),
                f"go-module-topology.json: modules[{index}] must be an object",
            )
            exact_keys(
                module,
                {
                    "directory",
                    "modulePath",
                    "role",
                    "owner",
                    "supportedModes",
                    "validationTarget",
                    "validationVariables",
                    "packageScope",
                    "prerequisites",
                    "consumers",
                    "localReplacements",
                    "ciWorkflow",
                },
                f"modules[{index}]",
            )
            module["directory"] = relative_directory(
                module["directory"], f"modules[{index}].directory", allow_root=True
            )
            module["modulePath"] = nonempty_string(
                module["modulePath"], f"modules[{index}].modulePath"
            )
            module["role"] = nonempty_string(module["role"], f"modules[{index}].role")
            require(
                module["role"] in ROLES,
                f"go-module-topology.json: modules[{index}].role is unknown: {module['role']}",
            )
            nonempty_string(module["owner"], f"modules[{index}].owner")
            modes = string_array(
                module["supportedModes"], f"modules[{index}].supportedModes", allow_empty=False
            )
            require(
                set(modes) <= {"standalone", "workspace"},
                f"go-module-topology.json: modules[{index}].supportedModes contains an unknown mode",
            )
            target = nonempty_string(
                module["validationTarget"], f"modules[{index}].validationTarget"
            )
            require(
                bool(re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", target)),
                f"go-module-topology.json: modules[{index}].validationTarget is malformed",
            )
            variables = string_array(
                module["validationVariables"],
                f"modules[{index}].validationVariables",
                allow_empty=True,
            )
            require(
                all(re.fullmatch(r"[A-Z][A-Z0-9_]*", item) for item in variables),
                f"go-module-topology.json: modules[{index}].validationVariables contains a malformed name",
            )
            nonempty_string(module["packageScope"], f"modules[{index}].packageScope")
            string_array(
                module["prerequisites"], f"modules[{index}].prerequisites", allow_empty=True
            )
            string_array(module["consumers"], f"modules[{index}].consumers", allow_empty=False)
            require(
                isinstance(module["localReplacements"], list),
                f"go-module-topology.json: modules[{index}].localReplacements must be an array",
            )
            for replacement in module["localReplacements"]:
                require(
                    isinstance(replacement, dict),
                    "go-module-topology.json: local replacement must be an object",
                )
                exact_keys(
                    replacement, {"modulePath", "replacementPath", "reason"}, "local replacement"
                )
                for field in replacement:
                    nonempty_string(replacement[field], f"local replacement {field}")
            relative_directory(module["ciWorkflow"], f"modules[{index}].ciWorkflow")
        for index, workspace in enumerate(value["workspaces"]):
            require(
                isinstance(workspace, dict),
                f"go-module-topology.json: workspaces[{index}] must be an object",
            )
            exact_keys(
                workspace,
                {"directory", "owner", "use", "replacements", "validationTarget", "ciWorkflow"},
                f"workspaces[{index}]",
            )
        require(
            not value["workspaces"],
            "approved workspaces are not supported by current topology validation; implement go.work membership, replacement, and validation-target enforcement before adding one",
        )
        for index, exclusion in enumerate(value["exclusions"]):
            require(
                isinstance(exclusion, dict),
                f"go-module-topology.json: exclusions[{index}] must be an object",
            )
            exact_keys(exclusion, {"pathPattern", "category", "reason"}, f"exclusions[{index}]")
            pattern = nonempty_string(exclusion["pathPattern"], f"exclusions[{index}].pathPattern")
            require(
                pattern.endswith("/**") and ".." not in pattern and not pattern.startswith("/"),
                f"go-module-topology.json: exclusions[{index}].pathPattern must be a safe relative subtree pattern ending in /**",
            )
            require(
                exclusion["category"] in CATEGORIES,
                f"go-module-topology.json: exclusions[{index}].category is unknown: {exclusion['category']}",
            )
            nonempty_string(exclusion["reason"], f"exclusions[{index}].reason")
        directories = [module["directory"] for module in value["modules"]]
        module_paths = [module["modulePath"] for module in value["modules"]]
        require(len(set(directories)) == len(directories), "module directories must be unique")
        require(len(set(module_paths)) == len(module_paths), "module paths must be unique")
        require(
            all(module.startswith(prefix) for module in module_paths),
            "every module path must start with repositoryModulePrefix",
        )
        patterns = [item["pathPattern"] for item in value["exclusions"]]
        require(len(set(patterns)) == len(patterns), "exclusion path patterns must be unique")
    except TopologyError as error:
        if str(error) == "inventory schema validation failed":
            raise
        raise TopologyError(str(error)) from error
    return value


def excluded(path: str, inventory: dict[str, Any]) -> str | None:
    for item in inventory["exclusions"]:
        pattern = item["pathPattern"]
        directory = pattern.removesuffix("/**")
        if directory.startswith("**/"):
            name = directory.removeprefix("**/")
            if path.startswith(f"{name}/") or f"/{name}/" in path:
                return item["category"]
        elif path.startswith(f"{directory}/"):
            return item["category"]
    return None


def discover_manifests(
    root: Path, inventory: dict[str, Any]
) -> tuple[list[str], list[tuple[str, str]]]:
    relevant: list[str] = []
    ignored: list[tuple[str, str]] = []
    for path in root.rglob("*"):
        if not path.is_file() or path.name not in {"go.mod", "go.sum", "go.work", "go.work.sum"}:
            continue
        relative = path.relative_to(root).as_posix()
        category = excluded(relative, inventory)
        if category:
            ignored.append((relative, category))
        else:
            relevant.append(relative)
    return sorted(relevant), sorted(ignored)


def parse_go_metadata(root: Path, mod_file: str) -> dict[str, Any] | None:
    result = run_command(
        ["go", "mod", "edit", "-json", str(root / mod_file)],
        cwd=root,
        activity=f"inspect {mod_file}",
        env=command_environment(GOWORK="off"),
        check=False,
    )
    if result.returncode != 0 or not result.stdout:
        return None
    try:
        value = json.loads(result.stdout)
    except json.JSONDecodeError:
        return None
    return value if isinstance(value, dict) else None


def target_present(makefile: Path, target: str) -> bool:
    pattern = re.compile(rf"^{re.escape(target)}(?:\s*|[^A-Za-z0-9._-].*):", re.MULTILINE)
    return bool(pattern.search(makefile.read_text(encoding="utf-8")))


def topology_errors(root: Path, inventory: dict[str, Any]) -> tuple[list[str], list[str]]:
    relevant, ignored = discover_manifests(root, inventory)
    expected: set[str] = set()
    errors: list[str] = []
    for module in inventory["modules"]:
        directory = module["directory"]
        prefix = "" if directory == "." else f"{directory}/"
        mod_file = f"{prefix}go.mod"
        sum_file = f"{prefix}go.sum"
        expected.update({mod_file, sum_file})
        if not (root / mod_file).is_file():
            errors.append(f"missing inventory module manifest: {mod_file}")
            continue
        if not (root / sum_file).is_file():
            errors.append(f"missing inventory module checksum: {sum_file}")
        makefile = root / prefix / "Makefile"
        if not makefile.is_file():
            errors.append(f"missing owning Makefile for {directory}: {prefix}Makefile")
        elif not target_present(makefile, module["validationTarget"]):
            errors.append(
                f"missing validation target {module['validationTarget']} in {prefix}Makefile"
            )
        workflow = root / module["ciWorkflow"]
        if not workflow.is_file():
            errors.append(f"missing CI workflow for {directory}: {module['ciWorkflow']}")
        metadata = parse_go_metadata(root, mod_file)
        if metadata is None:
            errors.append(
                f"could not inspect effective manifest metadata for {module['modulePath']} at {mod_file}"
            )
            continue
        actual = (metadata.get("Module") or {}).get("Path", "")
        if not actual:
            errors.append(f"missing module directive: {mod_file}")
        elif actual != module["modulePath"]:
            errors.append(
                f"module identity mismatch at {mod_file}: inventory={module['modulePath']}, actual={actual}"
            )
        replacements = {
            (item["modulePath"], item["replacementPath"]) for item in module["localReplacements"]
        }
        actual_replacements = {
            (item["Old"]["Path"], item["New"]["Path"])
            for item in metadata.get("Replace") or []
            if not item["New"].get("Version")
        }
        for required, replacement in sorted(actual_replacements - replacements):
            errors.append(
                f"module {module['modulePath']} requires {required} from unapproved filesystem replacement {replacement}; standalone portability is not proven"
            )
        for required, replacement in sorted(replacements - actual_replacements):
            errors.append(
                f"module {module['modulePath']} inventory approves missing filesystem replacement {required} => {replacement}"
            )
    for path in relevant:
        if path not in expected:
            tracked = (
                run_command(
                    ["git", "ls-files", "--error-unmatch", "--", path],
                    cwd=root,
                    activity="classify manifest",
                    check=False,
                ).returncode
                == 0
            )
            errors.append(
                f"unlisted {'repository' if tracked else 'developer-local'} manifest: {path}"
            )
    for path in sorted(expected - set(relevant)):
        errors.append(f"inventory path was not discovered: {path}")
    return errors, [f"excluded: {path} [{category}]" for path, category in ignored]


def pull_request_paths(text: str) -> set[str]:
    result: set[str] = set()
    inside = False
    inside_paths = False
    for line in text.splitlines():
        if line == "  pull_request:":
            inside = True
            continue
        if inside and (line and not line[0].isspace() or re.fullmatch(r"  [A-Za-z_]+:", line)):
            break
        if inside and line == "    paths:":
            inside_paths = True
            continue
        if inside and inside_paths and re.fullmatch(r"    \S.*", line):
            inside_paths = False
        if inside_paths:
            match = re.fullmatch(r"      - '([^']+)'", line)
            if match:
                result.add(match.group(1))
    return result


def require_literal(text: str, workflow: str, literal: str, description: str) -> None:
    require(literal in text, f"workflow {workflow} is missing {description}: {literal}")


def ci_check(root: Path, inventory: dict[str, Any]) -> None:
    workflows = sorted({module["ciWorkflow"] for module in inventory["modules"]})
    for workflow in workflows:
        path = root / workflow
        require(path.is_file(), f"missing workflow: {workflow}")
        text = path.read_text(encoding="utf-8")
        require("  push:" not in text, f"workflow {workflow} must not run on push")
        for literal, description in (
            ("  pull_request:", "pull_request-only event"),
            ("      - main", "main target branch"),
            ("cancel-in-progress: true", "per-PR cancellation"),
            (
                "ref: ${{ github.event.pull_request.head.sha }}",
                "explicit pull request head checkout",
            ),
            ("fetch-depth: 0", "complete checkout history"),
            ("uses: astral-sh/setup-uv@v7", "managed Python setup"),
            ("version: '0.13.1'", "pinned uv runtime"),
            ("run: make python-toolchain-setup", "pinned Python environment setup"),
            ("scripts/go/select_pr_module.py", "Python module selector"),
            (
                '--base "$PR_BASE" --head "$PR_HEAD" --output "$GITHUB_OUTPUT"',
                "complete PR selector inputs",
            ),
            ("uses: actions/setup-go@v7", "selected-module validation contract"),
            (
                "cache-dependency-path: ${{ steps.select.outputs.go_sum }}",
                "selected-module validation contract",
            ),
            ("uses: actions/cache@v5", "selected-module validation contract"),
            (
                "path: ${{ steps.select.outputs.module_dir }}/.tools",
                "selected-module validation contract",
            ),
            ('make -C "$MODULE_DIR" bootstrap-go-tools', "selected-module validation contract"),
            (
                "COVERAGE_FILE: ${{ github.workspace }}/${{ steps.select.outputs.coverage_file }}",
                "selected-module validation contract",
            ),
            (
                "FORMAT_BASE: ${{ steps.select.outputs.format_base }}",
                "selected-module validation contract",
            ),
            ('make -C "$MODULE_DIR" "$VALIDATION_TARGET"', "selected-module validation contract"),
            (
                'FORMAT_SCOPE=changed FORMAT_BASE="$FORMAT_BASE"',
                "selected-module validation contract",
            ),
            ('rm -f -- "$COVERAGE_FILE"', "selected-module validation contract"),
            (
                "steps.select.outputs.module_role != 'service-scaffold'",
                "selected-module validation contract",
            ),
            ("uses: codecov/codecov-action@v7", "selected-module validation contract"),
            (
                "files: ${{ steps.select.outputs.coverage_files }}",
                "selected-module validation contract",
            ),
            (
                "flags: ${{ steps.select.outputs.coverage_flag }}",
                "selected-module validation contract",
            ),
            ("disable_search: true", "selected-module validation contract"),
            ("fail_ci_if_error: true", "selected-module validation contract"),
        ):
            require_literal(text, workflow, literal, description)
        triggers = pull_request_paths(text)
        for module in (item for item in inventory["modules"] if item["ciWorkflow"] == workflow):
            directory = module["directory"]
            if directory.startswith("services/") and directory.endswith("-service"):
                expected = {
                    "services/*-service/**/*.go",
                    "services/*-service/go.mod",
                    "services/*-service/go.sum",
                    "services/*-service/**/testdata/**",
                }
                require(
                    module["role"] == "business-service",
                    f"standard service {directory} must use the business-service role",
                )
                require(
                    "services/*-service/**" not in triggers,
                    f"workflow {workflow} contains forbidden pull_request path trigger for broad generic service trigger: services/*-service/**",
                )
            else:
                expected = {
                    f"{directory}/**/*.go",
                    f"{directory}/go.mod",
                    f"{directory}/go.sum",
                    f"{directory}/**/testdata/**",
                }
                require(
                    f"{directory}/**" not in triggers,
                    f"workflow {workflow} contains forbidden pull_request path trigger for broad inventory module trigger {directory}: {directory}/**",
                )
            missing = sorted(expected - triggers)
            if missing:
                raise TopologyError(
                    f"workflow {workflow} is missing pull_request path trigger for "
                    f"{directory}: {missing[0]}"
                )
        for forbidden in (
            "actions/setup-java@",
            "actions/setup-node@",
            "make go-modules-check",
            "make go-topology-test",
            "make go-scaffold-test-tooling",
            "make go-product-test-tooling",
            "make go-scaffold-check-copy",
            "make -C services/product-service check-ci",
            "make go-svc-",
            "make postgres-svc-role",
            "make product-baseline-",
            "make product-security-",
            "kubectl ",
            "docker build",
        ):
            require(
                forbidden not in text,
                f"workflow {workflow} contains forbidden non-Go or all-module execution: {forbidden}",
            )
        module_count = sum(item["ciWorkflow"] == workflow for item in inventory["modules"])
        print(
            f"ci-coverage: workflow={workflow} modules={module_count} workspaces=0 selected-module-pr-validation=present"
        )
    print(f"go-ci-coverage-check: passed {len(workflows)} workflow(s)")


def decode_json_stream(value: str) -> list[dict[str, Any]]:
    decoder = json.JSONDecoder()
    position = 0
    records: list[dict[str, Any]] = []
    while position < len(value):
        while position < len(value) and value[position].isspace():
            position += 1
        if position >= len(value):
            break
        record, position = decoder.raw_decode(value, position)
        require(isinstance(record, dict), "invalid-package-graph")
        records.append(record)
    return records


def boundary_check(root: Path, inventory: dict[str, Any]) -> None:
    modules = sorted(inventory["modules"], key=lambda item: len(item["modulePath"]), reverse=True)
    failures: list[str] = []
    for module in modules:
        directory = root if module["directory"] == "." else root / module["directory"]
        result = run_command(
            ["go", "list", "-e", "-json", "-mod=readonly", "./..."],
            cwd=directory,
            activity=f"load package graph for {module['modulePath']}",
            env=command_environment(GOWORK="off"),
            check=False,
        )
        if result.returncode != 0 or not result.stdout:
            failures.append(
                f"module={module['modulePath']} consumer-package=unknown import=unknown owner=unknown rule=package-graph-load-failed remediation=resolve standalone readonly package loading"
            )
            continue
        try:
            packages = sorted(
                decode_json_stream(result.stdout), key=lambda item: item.get("ImportPath", "")
            )
        except json.JSONDecodeError, TopologyError:
            failures.append(
                f"module={module['modulePath']} consumer-package=unknown import=unknown owner=unknown rule=invalid-package-graph remediation=resolve standalone package-list output"
            )
            continue
        if not packages:
            failures.append(
                f"module={module['modulePath']} consumer-package=unknown import=unknown owner=unknown rule=empty-package-graph remediation=ensure ./... selects the owned packages"
            )
            continue
        imports = sorted(
            {
                value
                for package in packages
                for key in ("Imports", "TestImports", "XTestImports")
                for value in package.get(key, [])
            }
        )
        print(
            f"package-graph: module={module['modulePath']} mode=standalone packages={len(packages)} direct-imports={len(imports)}"
        )
        print("  packages: " + ",".join(package.get("ImportPath", "") for package in packages))
        for package in packages:
            consumer = package.get("ImportPath", "unknown")
            for import_path in sorted(
                {
                    value
                    for key in ("Imports", "TestImports", "XTestImports")
                    for value in package.get(key, [])
                }
            ):
                if not import_path.startswith(inventory["repositoryModulePrefix"]):
                    continue
                owner = next(
                    (
                        item
                        for item in modules
                        if import_path == item["modulePath"]
                        or import_path.startswith(f"{item['modulePath']}/")
                    ),
                    None,
                )
                if owner is None:
                    failures.append(
                        f"module={module['modulePath']} consumer-package={consumer} import={import_path} owner=unknown rule=unknown-repository-owner remediation=inventory the owning module or remove the repository-local import"
                    )
                elif owner["modulePath"] == module["modulePath"]:
                    continue
                elif import_path.removeprefix(owner["modulePath"]).startswith("/internal"):
                    failures.append(
                        f"module={module['modulePath']} consumer-package={consumer} import={import_path} owner={owner['modulePath']} rule=sibling-internal-import remediation=depend on an approved public contract/shared module"
                    )
                elif owner["role"] == "service-scaffold":
                    failures.append(
                        f"module={module['modulePath']} consumer-package={consumer} import={import_path} owner={owner['modulePath']} rule=scaffold-runtime-dependency remediation=copy the scaffold structure and own runtime code independently"
                    )
                elif owner["role"] not in {"public-contract", "public-shared"}:
                    failures.append(
                        f"module={module['modulePath']} consumer-package={consumer} import={import_path} owner={owner['modulePath']} rule=non-public-cross-module-import remediation=use an approved public contract/shared module"
                    )
                elif module["modulePath"] not in owner["consumers"]:
                    failures.append(
                        f"module={module['modulePath']} consumer-package={consumer} import={import_path} owner={owner['modulePath']} rule=unapproved-consumer remediation=record the consumer after contract ownership approval"
                    )
            if package.get("Incomplete") or package.get("Error") or package.get("DepsErrors"):
                failures.append(
                    f"module={module['modulePath']} consumer-package={consumer} import=unknown owner=unknown rule=incomplete-package-graph remediation=resolve all standalone readonly imports before boundary validation"
                )
    if failures:
        raise TopologyError(
            "\n".join(f"go-boundary-check: {item}" for item in sorted(set(failures)))
        )
    print(f"go-boundary-check: passed {len(modules)} module graph(s) in standalone readonly mode")


def manifest_snapshot(root: Path) -> dict[str, str]:
    return {
        path.relative_to(root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
        for path in sorted(root.rglob("*"))
        if path.is_file() and path.name in {"go.mod", "go.sum", "go.work", "go.work.sum"}
    }


def modules_check(root: Path, inventory: dict[str, Any]) -> None:
    declared = sorted(
        {value for module in inventory["modules"] for value in module["validationVariables"]}
    )
    caller_variables = os.environ.get("GO_MODULE_CALLER_VARIABLES", "").split()
    for variable in caller_variables:
        if variable != "GOWORK" and variable not in declared:
            raise TopologyError(f"unsupported caller variable: {variable}")
    selected = {"FORMAT_SCOPE": "all"}
    selected.update(
        {name: os.environ.get(name, "") for name in caller_variables if name in declared}
    )
    before = manifest_snapshot(root)
    failures: list[str] = []
    make = os.environ.get("MAKE_COMMAND", "make")
    for module in inventory["modules"]:
        if "standalone" not in module["supportedModes"]:
            failures.append(
                f"module {module['modulePath']} does not declare required standalone mode"
            )
            continue
        directory = module["directory"]
        forwarded = {
            name: selected[name] for name in module["validationVariables"] if name in selected
        }
        print(f"module-check: {module['modulePath']}")
        arguments = [
            make,
            "--no-print-directory",
            "-C",
            directory,
            module["validationTarget"],
            *(f"{name}={value}" for name, value in forwarded.items()),
        ]
        environment = dict(os.environ)
        environment.pop("MAKEFLAGS", None)
        environment.pop("MAKEOVERRIDES", None)
        environment.pop("FORMAT_FILES", None)
        for name in declared:
            environment.pop(name, None)
        environment["GOWORK"] = "off"
        environment.update(forwarded)
        result = run_command(
            arguments,
            cwd=root,
            activity=f"validate {module['modulePath']}",
            env=environment,
            check=False,
            timeout_seconds=1800,
        )
        if result.stdout:
            print(result.stdout, end="")
        if result.stderr:
            print(result.stderr, end="", file=sys.stderr)
        if result.returncode:
            failures.append(
                f"module {module['modulePath']} target {module['validationTarget']} failed with status {result.returncode}"
            )
            print(
                f"module-result: {module['modulePath']} | failed: target status {result.returncode}"
            )
        else:
            print(f"module-result: {module['modulePath']} | passed")
    require(
        manifest_snapshot(root) == before,
        "module/workspace manifest path set or bytes changed during validation",
    )
    if failures:
        raise TopologyError("\n".join(f"go-modules-check: {item}" for item in failures))
    print(
        f"go-modules-check: passed {len(inventory['modules'])} module(s) in standalone mode; manifests unchanged"
    )


def topology_check(root: Path, inventory: dict[str, Any]) -> None:
    errors, ignored = topology_errors(root, inventory)
    for line in ignored:
        print(line)
    if errors:
        raise TopologyError("\n".join(f"go-topology-check: {item}" for item in errors))
    ci_check(root, inventory)
    boundary_check(root, inventory)
    print(f"Go module topology: {len(inventory['modules'])} module(s), 0 approved workspace(s)")
    for module in inventory["modules"]:
        print(
            f"module: {module['modulePath']} | directory={module['directory']} | role={module['role']} | owner={module['owner']} | modes={','.join(module['supportedModes'])} | validation={module['validationTarget']} | variables={','.join(module['validationVariables']) or 'none'} | scope={module['packageScope']} | prerequisites={';'.join(module['prerequisites']) or 'none'} | consumers={','.join(module['consumers'])} | replacements=none | workspace=none | ci={module['ciWorkflow']}"
        )
    print("workspaces: none approved")


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser()
    result.add_argument("command", choices=("topology", "boundaries", "ci-coverage", "modules"))
    result.add_argument("paths", nargs="*")
    return result


def main(argv: list[str] | None = None) -> int:
    arguments = parser().parse_args(argv)
    prefix = {
        "topology": "go-topology-check",
        "boundaries": "go-boundary-check",
        "ci-coverage": "go-ci-coverage-check",
        "modules": "go-modules-check",
    }[arguments.command]
    if arguments.paths:
        if arguments.command != "ci-coverage" or len(arguments.paths) != 2:
            raise TopologyError(f"{prefix}: unexpected positional arguments")
        root = Path(arguments.paths[0]).resolve(strict=True)
        inventory_path = Path(arguments.paths[1]).resolve(strict=True)
        require(root.is_dir(), f"repository root does not exist: {root}")
        require(inventory_path.is_file(), f"inventory does not exist: {inventory_path}")
    else:
        root, inventory_path = fixture_paths(prefix)
    try:
        inventory = load_inventory(inventory_path)
    except TopologyError as error:
        if str(error).startswith("go-module-topology.json"):
            raise TopologyError("inventory schema validation failed") from error
        raise
    if arguments.command == "topology":
        topology_check(root, inventory)
    elif arguments.command == "boundaries":
        boundary_check(root, inventory)
    elif arguments.command == "ci-coverage":
        ci_check(root, inventory)
    else:
        modules_check(root, inventory)
    return 0


if __name__ == "__main__":
    signal.signal(signal.SIGHUP, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130) from None
    except TopologyTerminated as error:
        raise SystemExit(128 + int(error.args[0])) from None
    except (OSError, TopologyError) as error:
        print(error, file=sys.stderr)
        raise SystemExit(1) from None
