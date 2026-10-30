"""Versioned consumer registration and fail-closed PR obligations."""

from __future__ import annotations

import json
import re
import tempfile
from pathlib import Path
from typing import Any

import select_pr_module
import topology
from shared_candidate import locks, verify_selection
from shared_support import (
    DIRECTORY,
    MODULE,
    exact,
    extract,
    manifest_digest,
    relative,
    require,
    verify_sha,
)

PHASES = frozenset(
    {
        "format",
        "lint",
        "unit",
        "race",
        "build",
        "dependency",
        "vulnerability",
        "generated",
        "breaking",
        "integration",
        "copy",
        "image",
    }
)
REQUIRED = PHASES - {"generated", "breaking", "integration", "copy", "image"}
LIBRARY_PHASES = sorted(REQUIRED)
CI_CANDIDATE_PHASES = ("build", "unit")
ROOT_INPUTS = (
    "scripts/go/",
    "scripts/python/",
    "go-module-topology.json",
    "pyproject.toml",
    "uv.lock",
    ".python-version",
    "Makefile",
    ".github/workflows/go-pr-shared.yml",
    ".github/workflows/go-release-lib.yml",
)


def strings(value: Any, label: str, empty: bool = False) -> list[str]:
    require(isinstance(value, list) and (empty or value), f"missing {label}")
    require(all(isinstance(v, str) and v.strip() == v and v for v in value), f"invalid {label}")
    require(len(set(value)) == len(value), f"duplicate {label}")
    return value


def schema(records: Any) -> None:
    require(isinstance(records, list), "activeConsumers must be an array")
    modules, directories = set(), set()
    for item in records:
        exact(
            item,
            {
                "module",
                "directory",
                "owner",
                "status",
                "required_dependency",
                "workflows",
                "candidate_phases",
                "phase_bindings",
                "applicability",
            },
            "consumer",
        )
        require(
            item["module"] not in modules and item["directory"] not in directories,
            "duplicate consumer identity",
        )
        modules.add(item["module"])
        directories.add(relative(item["directory"]))
        require(isinstance(item["owner"], str) and item["owner"].strip(), "missing consumer owner")
        require(
            item["status"] == "active" and item["required_dependency"] == MODULE,
            "invalid active consumer dependency/status",
        )
        phases = set(strings(item["candidate_phases"], "candidate phases"))
        require(REQUIRED <= phases <= PHASES, "missing or unsupported required phase")
        require(
            isinstance(item["phase_bindings"], dict) and set(item["phase_bindings"]) == phases,
            "missing phase binding",
        )
        for binding in item["phase_bindings"].values():
            exact(binding, {"target", "modes"}, "phase binding")
            require(
                isinstance(binding["target"], str)
                and re.fullmatch(r"[a-z][a-z0-9-]*", binding["target"]),
                "invalid owning Make target",
            )
            require(
                set(strings(binding["modes"], "resolution modes")) == {"normal", "candidate"},
                "phase must support normal and candidate resolution",
            )
        require(
            isinstance(item["applicability"], dict)
            and set(item["applicability"]) == PHASES - phases,
            "missing applicability reasons",
        )
        require(
            all(isinstance(v, str) and v.strip() for v in item["applicability"].values()),
            "empty applicability reason",
        )
        require(isinstance(item["workflows"], list) and item["workflows"], "missing workflows")
        seen = set()
        for binding in item["workflows"]:
            exact(binding, {"path", "job"}, "workflow binding")
            relative(binding["path"])
            require(
                isinstance(binding["job"], str) and re.fullmatch(r"[a-zA-Z][\w-]*", binding["job"]),
                "invalid workflow job",
            )
            key = (binding["path"], binding["job"])
            require(key not in seen, "duplicate workflow binding")
            seen.add(key)


def go_tokens(text: str) -> list[str]:
    """Lex module/import identifiers, ignoring comments and retaining quoted strings."""
    return [
        m.group()
        for m in re.finditer(
            r'//[^\n]*|/\*[\s\S]*?\*/|"(?:\\.|[^"\\])*"|`[^`]*`|[^\s()]+|[()]', text
        )
        if not m.group().startswith(("//", "/*"))
    ]


def manifest_requirements(path: Path) -> set[str]:
    tokens = go_tokens(path.read_text())
    required = set()
    for index, token in enumerate(tokens):
        if token != "require":
            continue
        following = tokens[index + 1 :]
        if following and following[0] == "(":
            following = following[1 : following.index(")")]
        else:
            following = following[:2]
        require(len(following) % 2 == 0, "malformed require directive")
        required.update(v.strip('"') for v in following[::2])
    return required


def imports_servicekit(root: Path) -> bool:
    for path in root.rglob("*.go"):
        if any(p in {"vendor", ".tools", "testdata"} for p in path.relative_to(root).parts):
            continue
        tokens = go_tokens(path.read_text())
        for index, token in enumerate(tokens):
            if token != "import":
                continue
            following = tokens[index + 1 :]
            if following and following[0] == "(":
                following = following[1 : following.index(")")]
            else:
                following = following[:2]
            if any(
                v.strip('"`') == MODULE or v.strip('"`').startswith(MODULE + "/") for v in following
            ):
                return True
    return False


def registration_check(root: Path, inventory: dict[str, Any]) -> list[dict[str, Any]]:
    records = inventory.get("activeConsumers", [])
    schema(records)
    modules = {m["modulePath"]: m for m in inventory["modules"]}
    active = {item["module"]: item for item in records}
    library = modules.get(MODULE)
    for name, module in modules.items():
        if name == MODULE:
            continue
        directory = root / module["directory"]
        required = MODULE in manifest_requirements(directory / "go.mod")
        imported = imports_servicekit(directory)
        require(
            (required or imported) == (name in active),
            "unregistered or hidden active consumer: " + name,
        )
        if name not in active:
            continue
        require(required, "active consumer must require servicekit")
        require(library and name in library["consumers"], "unapproved servicekit consumer")
        record = active[name]
        require(
            record["directory"] == module["directory"] and record["owner"] == module["owner"],
            "consumer ownership mismatch",
        )
        # Preserve known baseline capabilities; a self-declared waiver cannot erase them.
        baseline = (
            {"generated", "breaking", "integration", "image"}
            if module["role"] == "business-service"
            else {"generated", "breaking", "copy"}
        )
        require(baseline <= set(record["candidate_phases"]), "consumer baseline phases omitted")
        makefile = directory / "Makefile"
        for binding in record["phase_bindings"].values():
            require(
                topology.target_present(makefile, binding["target"]), "missing owning phase target"
            )
        fast_targets = {record["phase_bindings"][phase]["target"] for phase in CI_CANDIDATE_PHASES}
        require(
            not fast_targets
            & {
                binding["target"]
                for phase, binding in record["phase_bindings"].items()
                if phase not in CI_CANDIDATE_PHASES
            },
            "fast candidate phases must have dedicated owning targets",
        )
        for binding in record["workflows"]:
            path = root / binding["path"]
            require(
                path.is_file()
                and re.search(rf"^  {re.escape(binding['job'])}:\s*$", path.read_text(), re.M),
                "unresolved workflow/job binding",
            )
    require(set(active) <= set(modules), "unregistered consumer module")
    return records


def shared_ci_contract(root: Path, path: str) -> None:
    import yaml

    workflow = yaml.safe_load((root / path).read_text())
    events = workflow.get("on", workflow.get(True))
    require(
        events == {"pull_request": {"branches": ["main"]}},
        "shared workflow must cover every PR to main",
    )
    require(
        workflow.get("permissions") == {"contents": "read"}, "shared workflow must be read-only"
    )
    require(set(workflow["jobs"]) == {"plan", "validate", "result"}, "shared workflow job contract")
    require(
        workflow["jobs"]["validate"]["strategy"]["fail-fast"] is False,
        "required consumers must run independently",
    )
    require(workflow["jobs"]["result"]["if"] == "always()", "aggregate must run after failure")
    for job, target in (
        ("plan", "go-shared-plan"),
        ("validate", "go-shared-validate"),
        ("result", "go-shared-results-check"),
    ):
        require(target in json.dumps(workflow["jobs"][job]), "missing shared owner command")
    steps = workflow["jobs"]["validate"]["steps"]
    validation = next(
        (i for i, step in enumerate(steps) if step.get("run") == "make go-shared-validate"), -1
    )
    coverage = next(
        (
            i
            for i, step in enumerate(steps)
            if step.get("run") == "make -C libs/servicekit coverage"
        ),
        -1,
    )
    upload = next(
        (i for i, step in enumerate(steps) if step.get("uses") == "codecov/codecov-action@v7"),
        -1,
    )
    require(
        validation >= 0 and validation < coverage < upload,
        "missing or misordered servicekit coverage",
    )
    require(
        steps[coverage].get("if") == "matrix.module == 'libs/servicekit'",
        "servicekit coverage must run only for the library",
    )
    require(
        steps[upload].get("if") == "success() && matrix.module == 'libs/servicekit'"
        and steps[upload].get("with")
        == {
            "token": "${{ secrets.CODECOV_TOKEN }}",
            "files": "libs/servicekit/coverage.txt",
            "flags": "servicekit",
            "name": "servicekit",
            "disable_search": True,
            "handle_no_reports_found": False,
            "fail_ci_if_error": True,
        },
        "servicekit Codecov upload contract",
    )


def roots_at(root: Path, inventory: dict[str, Any]) -> set[str]:
    manifests, _ = topology.discover_manifests(root, inventory)
    require(
        not any(Path(p).name in {"go.work", "go.work.sum"} for p in manifests),
        "workspace not supported",
    )
    discovered = {str(Path(p).parent) for p in manifests if Path(p).name == "go.mod"}
    registered = {m["directory"] for m in inventory["modules"]}
    require(discovered == registered, "unknown or missing module roots")
    for module in inventory["modules"]:
        tokens = go_tokens((root / module["directory"] / "go.mod").read_text())
        require(
            len(tokens) >= 2 and tokens[:2] == ["module", module["modulePath"]],
            "module ownership disagrees with manifest",
        )
        if module["modulePath"] == MODULE:
            require(
                inventory["schemaVersion"] == 2
                and module["directory"] == DIRECTORY
                and module["role"] == "public-shared",
                "servicekit requires registered shared ownership",
            )
    return discovered


def plan_trees(
    base: Path,
    head: Path,
    paths: list[str],
    base_sha: str,
    head_sha: str,
    merge_base: str,
    profile: str = "full",
) -> dict[str, Any]:
    require(profile in {"ci", "full"}, "unknown validation profile")
    old = topology.load_inventory(base / "go-module-topology.json")
    new = topology.load_inventory(head / "go-module-topology.json")
    require(
        old["exclusions"] == new["exclusions"],
        "module exclusions cannot change during shared onboarding",
    )
    require(
        old["repositoryModulePrefix"] == new["repositoryModulePrefix"],
        "repository ownership prefix changed",
    )
    roots = roots_at(base, old) | roots_at(head, new)
    previous = registration_check(base, old)
    active = registration_check(head, new)
    for record in active:
        old_record = next((r for r in previous if r["module"] == record["module"]), None)
        if old_record:
            require(
                set(old_record["candidate_phases"]) <= set(record["candidate_phases"]),
                "active consumer phase downgrade requires baseline review",
            )
    changed = {
        root
        for root in roots
        if any(
            select_pr_module.is_validation_relevant_module_path(path)
            and (path == root or path.startswith(root + "/"))
            for path in paths
        )
    }
    require(len(changed) <= 1, "multiple Go modules changed; split the PR")
    for path in paths:
        classified = select_pr_module.classify_module(path)
        require(not classified or classified in roots, "unknown module ownership")
    require(
        not changed or changed <= {m["directory"] for m in new["modules"]},
        "module removal requires explicit onboarding review",
    )
    root_work = any(
        any(p == prefix or p.startswith(prefix) for prefix in ROOT_INPUTS) for p in paths
    )
    library_code_changed = any(
        p.startswith(DIRECTORY + "/") and not p.endswith(".md") for p in paths
    )
    obligations = []
    for module in new["modules"]:
        directory = module["directory"]
        record = next((r for r in active if r["directory"] == directory), None)
        candidate = (
            DIRECTORY in changed
            and record is not None
            and (profile == "full" or library_code_changed)
        )
        if not (directory in changed or candidate or (root_work and profile == "full")):
            continue
        mode = "candidate" if candidate else "normal"
        phases = (
            list(CI_CANDIDATE_PHASES)
            if candidate and profile == "ci"
            else record["candidate_phases"]
            if record
            else LIBRARY_PHASES
            if directory == DIRECTORY
            else ["gate"]
        )
        for phase in phases:
            target = (
                record["phase_bindings"][phase]["target"] if record else module["validationTarget"]
            )
            obligations.append(
                {"module": directory, "phase": phase, "mode": mode, "target": target}
            )
    inputs = {
        directory: {
            "source_manifest": manifest_digest(head / directory),
            "original_locks": locks(head / directory),
        }
        for directory in {r["module"] for r in obligations}
    }
    return {
        "schema": 1,
        "profile": profile,
        "base": base_sha,
        "head": head_sha,
        "merge_base": merge_base,
        "changed": sorted(changed),
        "root_checks": root_work,
        "active_consumers": len(active),
        "obligations": obligations,
        "inputs": inputs,
        "candidate_manifest": manifest_digest(head / DIRECTORY)
        if (head / DIRECTORY).is_dir()
        else None,
    }


def plan(root: Path, base: str, head: str, profile: str = "full") -> dict[str, Any]:
    verify_sha(root, base)
    verify_sha(root, head)
    merge_base, paths = select_pr_module.changed_paths(root, base, head)
    with tempfile.TemporaryDirectory(prefix="shared-plan-") as temporary:
        directory = Path(temporary)
        extract(root, base, directory / "base")
        extract(root, head, directory / "head")
        return plan_trees(
            directory / "base", directory / "head", paths, base, head, merge_base, profile
        )


def reconcile(expected: dict[str, Any], reports: list[dict[str, Any]]) -> None:
    wanted = {(r["module"], r["phase"], r["mode"]) for r in expected["obligations"]}
    seen = set()
    for report in reports:
        key = (report["module"], report["phase"], report["mode"])
        require(key in wanted and key not in seen, "unexpected or duplicate phase result")
        obligation = next(
            r for r in expected["obligations"] if (r["module"], r["phase"], r["mode"]) == key
        )
        require(report.get("target") == obligation["target"], "wrong owning phase target")
        seen.add(key)
        require(
            report["base"] == expected["base"]
            and report["head"] == expected["head"]
            and report["status"] == "passed",
            "failed, skipped, stale or wrong-SHA phase result",
        )
        require(
            report.get("source_manifest")
            and report.get("original_locks")
            and report.get("selected_graph"),
            "missing phase source/graph identity",
        )
        inputs = expected["inputs"][report["module"]]
        require(
            report["source_manifest"] == inputs["source_manifest"]
            and report["original_locks"] == inputs["original_locks"],
            "phase source identity mismatch",
        )
        if report["mode"] == "candidate":
            require(
                report.get("candidate_sha") == expected["head"]
                and report.get("alternate_locks")
                and report.get("candidate_manifest"),
                "missing candidate identity",
            )
            require(
                report["candidate_manifest"] == expected["candidate_manifest"],
                "candidate manifest differs from verified head",
            )
            directory = Path(report.get("candidate_directory", ""))
            require(
                directory.is_absolute() and directory.parts[-2:] == ("libs", "servicekit"),
                "invalid candidate source directory",
            )
            main = next((r for r in report["selected_graph"] if r.get("Main")), {})
            alternate = Path(main.get("Dir", "")) / "candidate.mod"
            verify_selection(report["selected_graph"], directory, alternate)
        else:
            require(
                not any(
                    r.get("Path") == MODULE and r.get("Replace") for r in report["selected_graph"]
                ),
                "normal result used candidate replacement",
            )
    require(seen == wanted, "missing required consumer phase result")
