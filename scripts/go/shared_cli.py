"""Make-owned shared-module PR and release commands."""

from __future__ import annotations

import argparse
import json
import os
import tempfile
from pathlib import Path

import select_pr_module
import shared_candidate
import shared_registration
import shared_release
import topology
from shared_support import (
    DIRECTORY,
    LIBRARIES,
    Budget,
    cli_entry,
    environment,
    extract,
    git,
    library_config,
    manifest_digest,
    read_json,
    require,
    write_json,
)

ROOT = Path(__file__).resolve().parents[2]


def expected_plan(profile: str = "ci") -> dict:
    return shared_registration.plan(ROOT, os.environ["PR_BASE"], os.environ["PR_HEAD"], profile)


def local_full_validation() -> None:
    require(
        git(ROOT, "rev-parse", "HEAD").strip() == os.environ["PR_HEAD"],
        "PR_HEAD is not checkout HEAD",
    )
    require(
        not git(ROOT, "status", "--porcelain"), "local full validation requires a clean checkout"
    )
    expected = expected_plan("full")
    budget = Budget(6000)
    if expected["root_checks"]:
        budget.run(
            ["make", "-C", str(ROOT / DIRECTORY), "bootstrap-go-tools"],
            ROOT,
            "prepare library tools",
            environment(PYTHON=os.environ["PYTHON"]),
        )
        budget.run(
            [
                "make",
                "python-shared-tooling-check",
                "python-shared-tooling-test-integration",
                "go-topology-check",
                "go-topology-test",
            ],
            ROOT,
            "full root tooling and topology checks",
            environment(PYTHON=os.environ["PYTHON"]),
        )
    reports = []
    for module in sorted({item["module"] for item in expected["obligations"]}):
        budget.run(
            ["make", "-C", str(ROOT / module), "bootstrap-go-tools"],
            ROOT,
            "prepare module tools",
            environment(PYTHON=os.environ["PYTHON"]),
        )
        reports.extend(
            shared_candidate.validate(ROOT, expected, module, ROOT / module / ".tools/bin")
        )
    shared_registration.reconcile(expected, reports)
    print(f"validated {len(reports)} full local obligations at {expected['head']}")


def release_expected(checkout: Path, sha: str, library_directory: str = DIRECTORY) -> dict:
    library = library_config(library_directory)
    inventory = topology.load_inventory(checkout / "go-module-topology.json")
    registered = shared_registration.registration_check(checkout, inventory)
    active = registered if library_directory == DIRECTORY else []
    require(
        (checkout / library_directory / "go.mod").read_text().splitlines()[0]
        == "module " + library["module"],
        "release module identity mismatch",
    )
    obligations = [
        {
            "module": library_directory,
            "phase": phase,
            "mode": "normal",
            "target": "check-go-pr",
        }
        for phase in shared_registration.LIBRARY_PHASES
    ]
    for consumer in active:
        for phase in consumer["candidate_phases"]:
            obligations.append(
                {
                    "module": consumer["directory"],
                    "phase": phase,
                    "mode": "candidate",
                    "target": consumer["phase_bindings"][phase]["target"],
                }
            )
    return {
        "base": sha,
        "head": sha,
        "active_consumers": len(active),
        "library": library_directory,
        "obligations": obligations,
        "inputs": {
            module: {
                "source_manifest": manifest_digest(checkout / module),
                "original_locks": shared_candidate.locks(checkout / module),
            }
            for module in {item["module"] for item in obligations}
        },
        "candidate_manifest": manifest_digest(checkout / library_directory),
    }


def release_matrix(expected: dict) -> dict:
    modules = sorted({item["module"] for item in expected["obligations"]})
    return {"include": [{"module": module} for module in modules]}


def release_module_expected(expected: dict, module: str) -> dict:
    obligations = [item for item in expected["obligations"] if item["module"] == module]
    require(obligations, "unplanned release module")
    return {
        **expected,
        "obligations": obligations,
        "inputs": {module: expected["inputs"][module]},
    }


def release_libraries(base: str, sha: str) -> list[str]:
    _, paths = select_pr_module.changed_paths(ROOT, base, sha)
    selected = [
        directory
        for directory in sorted(LIBRARIES)
        if any(
            path.startswith(directory + "/")
            and select_pr_module.is_validation_relevant_module_path(path)
            for path in paths
        )
    ]
    require(selected, "release push does not change a registered library")
    return selected


def release_plan(base: str, sha: str) -> None:
    require(shared_release.release_source() == sha, "release source mismatch")
    libraries = release_libraries(base, sha)
    with tempfile.TemporaryDirectory(prefix="library-release-plan-") as temporary:
        checkout = Path(temporary) / "source"
        extract(ROOT, sha, checkout)
        expected = {library: release_expected(checkout, sha, library) for library in libraries}
        validation = {
            "include": [
                {
                    "key": library_config(library)["key"] + "-" + item["module"].replace("/", "-"),
                    "library": library,
                    "module": item["module"],
                }
                for library in libraries
                for item in release_matrix(expected[library])["include"]
            ]
        }
        publication = {
            "include": [
                {
                    "key": library_config(library)["key"],
                    "library": library,
                    "module_path": library_config(library)["module"],
                    "tag_prefix": library_config(library)["tag_prefix"],
                }
                for library in libraries
            ]
        }
    if output := os.environ.get("GITHUB_OUTPUT"):
        with Path(output).open("a") as stream:
            stream.write("validation_matrix=" + json.dumps(validation) + "\n")
            stream.write("publish_matrix=" + json.dumps(publication) + "\n")
    print(
        json.dumps(
            {
                "source_sha": sha,
                "libraries": libraries,
                "validation_matrix": validation,
                "publish_matrix": publication,
            },
            indent=2,
        )
    )


def release_validation(sha: str, library: str, module: str) -> dict:
    require(shared_release.release_source() == sha, "release source mismatch")
    with tempfile.TemporaryDirectory(
        prefix=library_config(library)["key"] + "-release-validation-"
    ) as temporary:
        checkout = Path(temporary) / "source"
        extract(ROOT, sha, checkout)
        expected = release_module_expected(release_expected(checkout, sha, library), module)
        tools = checkout / module / ".tools/bin"
        Budget().run(
            [
                "make",
                "-C",
                str(checkout / module),
                "bootstrap-go-tools",
                f"PYTHON={os.environ['PYTHON']}",
            ],
            ROOT,
            "prepare source tools",
            environment(PYTHON=os.environ["PYTHON"]),
        )
        results = shared_candidate.validate(ROOT, expected, module, tools)
        shared_registration.reconcile(expected, results)
        return {
            "source_sha": sha,
            "library": library,
            "module": module,
            "obligations": len(results),
        }


def release_preparation(version: str, sha: str, library: str) -> dict:
    selected = library_config(library)
    shared_release.release_identity(version, sha, selected)
    require(shared_release.release_source() == sha, "release source mismatch")
    with tempfile.TemporaryDirectory(prefix=selected["key"] + "-release-prepare-") as temporary:
        checkout = Path(temporary) / "source"
        extract(ROOT, sha, checkout)
        expected = release_expected(checkout, sha, library)
        return {
            "library": library,
            "version": version,
            "source_sha": sha,
            "source_manifest": expected["candidate_manifest"],
            "planned_modules": [item["module"] for item in release_matrix(expected)["include"]],
        }


def release_command(action: str, work: Path) -> None:
    sha = os.environ["RELEASE_SHA"]
    if action == "release-plan":
        release_plan(os.environ["RELEASE_BASE"], sha)
        return
    library = os.environ["RELEASE_MODULE"]
    selected = library_config(library)
    if action == "release-validate":
        result = release_validation(sha, library, os.environ["MODULE_DIR"])
        print(json.dumps(result, indent=2))
        return
    version = os.environ["RELEASE_VERSION"]
    if action == "release-prepare":
        write_json(work / "validation.json", release_preparation(version, sha, library))
        return
    tag = shared_release.release_identity(version, sha, selected)
    require(shared_release.release_source() == sha, "release source mismatch")
    provider = shared_release.GitHub(ROOT)
    validation = read_json(work / "validation.json")
    require(
        validation["library"] == library
        and validation["version"] == version
        and validation["source_sha"] == sha,
        "validation source identity mismatch",
    )
    if action == "release-tag":
        shared_release.publish_tag(provider, version, sha, selected)
    elif action == "release-verify":
        require(provider.tag_sha(tag) == sha, "public verification tag mismatch")
        shared_release.public_verify(ROOT, version, sha, validation["source_manifest"], selected)
        require(provider.tag_sha(tag) == sha, "tag changed during public verification")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "action",
        choices=(
            "plan",
            "validate",
            "results",
            "local-full",
            "phase",
            "release-plan",
            "release-validate",
            "release-prepare",
            "release-tag",
            "release-verify",
        ),
    )
    args = parser.parse_args()
    if args.action.startswith("release-"):
        release_module = os.environ.get("RELEASE_MODULE", DIRECTORY)
        work = ROOT / ".work/go-library-release" / library_config(release_module)["key"]
        release_command(args.action, work)
    elif args.action == "local-full":
        local_full_validation()
    elif args.action == "phase":
        if directory := os.environ.get("SERVICEKIT_PHASE_DIR"):
            os.chdir(Path(directory).resolve(strict=True))
        shared_candidate.phase(os.environ["SERVICEKIT_PHASE"])
    else:
        work = ROOT / ".work/servicekit"
        expected = expected_plan()
        if args.action == "plan":
            write_json(work / "plan.json", expected)
            modules = sorted({r["module"] for r in expected["obligations"]})
            matrix = {"include": [{"module": m, "key": m.replace("/", "-")} for m in modules]}
            if output := os.environ.get("GITHUB_OUTPUT"):
                with Path(output).open("a") as stream:
                    stream.write("matrix=" + json.dumps(matrix) + "\n")
                    stream.write("has_work=" + str(bool(modules)).lower() + "\n")
                    stream.write("root_checks=" + str(expected["root_checks"]).lower() + "\n")
            print(json.dumps(expected, indent=2))
        elif args.action == "validate":
            require(
                read_json(work / "plan.json") == expected,
                "supplied plan differs from recomputed obligations",
            )
            module = os.environ["MODULE_DIR"]
            require(module in {r["module"] for r in expected["obligations"]}, "unplanned module")
            Budget().run(
                ["make", "-C", str(ROOT / module), "bootstrap-go-tools"],
                ROOT,
                "prepare module tools",
                environment(PYTHON=os.environ["PYTHON"]),
            )
            reports = shared_candidate.validate(
                ROOT, expected, module, ROOT / module / ".tools/bin"
            )
            write_json(work / "result.json", reports)
        else:
            require(os.environ.get("PLAN_RESULT") == "success", "planning failed/cancelled/skipped")
            require(
                os.environ.get("VALIDATION_RESULT")
                == ("success" if expected["obligations"] else "skipped"),
                "required validation job failed/cancelled/skipped",
            )
            reports = []
            for path in (work / "results").rglob("result.json"):
                reports.extend(read_json(path))
            shared_registration.reconcile(expected, reports)
            print(
                f"validated {len(reports)} required phases; {expected['active_consumers']} active consumers"
            )
    return 0


if __name__ == "__main__":
    cli_entry(main)
