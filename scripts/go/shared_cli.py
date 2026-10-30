"""Make-owned shared-module PR and release commands."""

from __future__ import annotations

import argparse
import json
import os
import tempfile
from pathlib import Path

import shared_candidate
import shared_registration
import shared_release
import topology
from shared_support import (
    DIRECTORY,
    MODULE,
    Budget,
    cli_entry,
    environment,
    extract,
    git,
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


def release_expected(checkout: Path, sha: str) -> dict:
    inventory = topology.load_inventory(checkout / "go-module-topology.json")
    active = shared_registration.registration_check(checkout, inventory)
    require(
        (checkout / DIRECTORY / "go.mod").read_text().splitlines()[0] == "module " + MODULE,
        "release module identity mismatch",
    )
    obligations = [
        {"module": DIRECTORY, "phase": phase, "mode": "normal", "target": "check-go-pr"}
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
        "obligations": obligations,
        "inputs": {
            module: {
                "source_manifest": manifest_digest(checkout / module),
                "original_locks": shared_candidate.locks(checkout / module),
            }
            for module in {item["module"] for item in obligations}
        },
        "candidate_manifest": manifest_digest(checkout / DIRECTORY),
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


def release_plan(sha: str) -> None:
    require(shared_release.release_source() == sha, "release source mismatch")
    with tempfile.TemporaryDirectory(prefix="servicekit-release-plan-") as temporary:
        checkout = Path(temporary) / "source"
        extract(ROOT, sha, checkout)
        expected = release_expected(checkout, sha)
        matrix = release_matrix(expected)
    if output := os.environ.get("GITHUB_OUTPUT"):
        with Path(output).open("a") as stream:
            stream.write("matrix=" + json.dumps(matrix) + "\n")
    print(
        json.dumps(
            {
                "source_sha": sha,
                "active_consumers": expected["active_consumers"],
                "matrix": matrix,
            },
            indent=2,
        )
    )


def release_validation(sha: str, module: str) -> dict:
    require(shared_release.release_source() == sha, "release source mismatch")
    with tempfile.TemporaryDirectory(prefix="servicekit-release-validation-") as temporary:
        checkout = Path(temporary) / "source"
        extract(ROOT, sha, checkout)
        expected = release_module_expected(release_expected(checkout, sha), module)
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
            "module": module,
            "obligations": len(results),
        }


def release_preparation(version: str, sha: str) -> dict:
    shared_release.release_identity(version, sha)
    require(shared_release.release_source() == sha, "release source mismatch")
    with tempfile.TemporaryDirectory(prefix="servicekit-release-prepare-") as temporary:
        checkout = Path(temporary) / "source"
        extract(ROOT, sha, checkout)
        expected = release_expected(checkout, sha)
        return {
            "version": version,
            "source_sha": sha,
            "source_manifest": expected["candidate_manifest"],
            "planned_modules": [item["module"] for item in release_matrix(expected)["include"]],
        }


def release_command(action: str, work: Path) -> None:
    sha = os.environ["RELEASE_SHA"]
    if action == "release-plan":
        release_plan(sha)
        return
    if action == "release-validate":
        result = release_validation(sha, os.environ["MODULE_DIR"])
        print(json.dumps(result, indent=2))
        return
    version = os.environ["RELEASE_VERSION"]
    if action == "release-prepare":
        write_json(work / "validation.json", release_preparation(version, sha))
        return
    tag = shared_release.release_identity(version, sha)
    require(shared_release.release_source() == sha, "release source mismatch")
    provider = shared_release.GitHub(ROOT)
    validation = read_json(work / "validation.json")
    require(
        validation["version"] == version and validation["source_sha"] == sha,
        "validation source identity mismatch",
    )
    if action == "release-tag":
        shared_release.publish_tag(provider, version, sha)
    elif action == "release-verify":
        require(provider.tag_sha(tag) == sha, "public verification tag mismatch")
        shared_release.public_verify(ROOT, version, sha, validation["source_manifest"])
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
    work = ROOT / ".work/servicekit"
    if args.action.startswith("release-"):
        release_command(args.action, work)
    elif args.action == "local-full":
        local_full_validation()
    elif args.action == "phase":
        if directory := os.environ.get("SERVICEKIT_PHASE_DIR"):
            os.chdir(Path(directory).resolve(strict=True))
        shared_candidate.phase(os.environ["SERVICEKIT_PHASE"])
    else:
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
