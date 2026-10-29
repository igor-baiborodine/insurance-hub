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
    digest,
    environment,
    extract,
    manifest_digest,
    read_json,
    require,
    write_json,
)

ROOT = Path(__file__).resolve().parents[2]


def expected_plan() -> dict:
    return shared_registration.plan(ROOT, os.environ["PR_BASE"], os.environ["PR_HEAD"])


def release_validation(version: str, sha: str) -> dict:
    producer = shared_release.producer()
    # Source code runs only in the read-only validation job.
    with tempfile.TemporaryDirectory(prefix="servicekit-release-validation-") as temporary:
        checkout = Path(temporary) / "source"
        extract(ROOT, sha, checkout)
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
        expected = {
            "base": sha,
            "head": sha,
            "obligations": obligations,
            "inputs": {
                m: {
                    "source_manifest": manifest_digest(checkout / m),
                    "original_locks": shared_candidate.locks(checkout / m),
                }
                for m in {r["module"] for r in obligations}
            },
            "candidate_manifest": manifest_digest(checkout / DIRECTORY),
        }
        results = []
        budget = Budget(3000)
        for module in sorted({r["module"] for r in obligations}):
            tools = checkout / module / ".tools/bin"
            budget.run(
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
            results.extend(shared_candidate.validate(ROOT, expected, module, tools))
        shared_registration.reconcile(expected, results)
        identity = digest(json.dumps(results, sort_keys=True).encode())
        return {
            "version": version,
            "source_sha": sha,
            "source_manifest": manifest_digest(checkout / DIRECTORY),
            "producer": producer,
            "results": {
                name: {"status": "passed", "identity": identity}
                for name in ("library", "consumers")
            },
            "phases": results,
        }


def release_command(action: str, work: Path) -> None:
    version, sha = os.environ["RELEASE_VERSION"], os.environ["RELEASE_SHA"]
    tag = shared_release.release_identity(version, sha)
    producer = shared_release.producer()
    if action == "release-validate":
        write_json(work / "validation.json", release_validation(version, sha))
        return
    provider = shared_release.GitHub(ROOT)
    validation = read_json(work / "validation.json")
    require(
        validation["version"] == version
        and validation["source_sha"] == sha
        and validation["producer"] == producer,
        "validation artifact identity mismatch",
    )
    if action == "release-tag":
        shared_release.publish_tag(provider, version, sha)
    elif action == "release-verify":
        require(provider.tag_sha(tag) == sha, "public verification tag mismatch")
        resolution = shared_release.public_verify(ROOT, version, sha, validation["source_manifest"])
        require(provider.tag_sha(tag) == sha, "tag changed during public verification")
        record = {
            "schema": 1,
            "module": MODULE,
            "version": version,
            "tag": tag,
            "source_sha": sha,
            "source_manifest": validation["source_manifest"],
            "producer": producer,
            "checksums": resolution["checksums"],
            "validation": validation["results"],
            "public_resolution": resolution["results"],
        }
        shared_release.qualification(record, version, sha)
        write_json(work / "qualification.json", record)
    else:
        shared_release.publish_qualification(provider, work / "qualification.json", version, sha)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "action",
        choices=(
            "plan",
            "validate",
            "results",
            "phase",
            "release-validate",
            "release-tag",
            "release-verify",
            "release-publish",
        ),
    )
    args = parser.parse_args()
    work = ROOT / ".work/servicekit"
    if args.action.startswith("release-"):
        release_command(args.action, work)
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
