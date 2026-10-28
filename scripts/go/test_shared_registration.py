"""Independent E02/E16 obligations and active-consumer rejection cases."""

from __future__ import annotations

import copy
import json
import subprocess
from pathlib import Path

import pytest
import yaml

import select_pr_module
import shared_registration as registration
import topology
from shared_support import DIRECTORY, MODULE, SharedError, write_json

ROOT = Path(__file__).resolve().parents[2]


def fixture_tree(root: Path, active_count: int = 0) -> dict:
    inventory = json.loads((ROOT / "go-module-topology.json").read_text())
    inventory["activeConsumers"] = []
    for module in inventory["modules"]:
        path = root / module["directory"]
        path.mkdir(parents=True, exist_ok=True)
        (path / "go.mod").write_text(f"module {module['modulePath']}\n\ngo 1.27.3\n")
        (path / "go.sum").touch()
        (path / "main.go").write_text("package fixture\n")
        (path / "Makefile").write_text("check-go-pr:\n\t@true\nshared-check:\n\t@true\n")
        workflow = root / module["ciWorkflow"]
        workflow.parent.mkdir(parents=True, exist_ok=True)
        workflow.write_text("jobs:\n  validate:\n    runs-on: ubuntu-24.04\n")
    for module in inventory["modules"][:active_count]:
        path = root / module["directory"] / "go.mod"
        path.write_text(path.read_text() + f"require {MODULE} v0.1.0\n")
        phases = sorted(registration.PHASES)
        inventory["activeConsumers"].append(
            {
                "module": module["modulePath"],
                "directory": module["directory"],
                "owner": module["owner"],
                "status": "active",
                "required_dependency": MODULE,
                "workflows": [{"path": module["ciWorkflow"], "job": "validate"}],
                "candidate_phases": phases,
                "phase_bindings": {
                    p: {"target": "shared-check", "modes": ["normal", "candidate"]} for p in phases
                },
                "applicability": {},
            }
        )
    write_json(root / "go-module-topology.json", inventory)
    return inventory


@pytest.mark.parametrize("count", [0, 1, 2])
def test_shared_change_selects_exact_unchanged_consumer_obligations(
    tmp_path: Path, count: int
) -> None:
    # given
    fixture_tree(tmp_path / "base", count)
    fixture_tree(tmp_path / "head", count)
    # when
    result = registration.plan_trees(
        tmp_path / "base",
        tmp_path / "head",
        [DIRECTORY + "/health/doc.go"],
        "a" * 40,
        "b" * 40,
        "a" * 40,
    )
    # then
    assert result["active_consumers"] == count
    assert {r["module"] for r in result["obligations"] if r["mode"] == "candidate"} == set(
        ["templates/go-service", "services/product-service"][:count]
    )
    assert len(result["obligations"]) == len(registration.LIBRARY_PHASES) + count * len(
        registration.PHASES
    )


@pytest.mark.parametrize(
    "defect", ["missing", "downgrade", "duplicate", "phase", "target", "workflow", "unknown"]
)
def test_registration_cannot_hide_actual_dependency(tmp_path: Path, defect: str) -> None:
    # given
    inventory = fixture_tree(tmp_path, 1)
    item = inventory["activeConsumers"][0]
    if defect == "missing":
        inventory["activeConsumers"] = []
    elif defect == "downgrade":
        item["status"] = "inactive"
    elif defect == "duplicate":
        inventory["activeConsumers"].append(copy.deepcopy(item))
    elif defect == "phase":
        item["candidate_phases"].remove("race")
    elif defect == "target":
        item["phase_bindings"]["unit"]["target"] = "absent"
    elif defect == "workflow":
        item["workflows"][0]["job"] = "absent"
    else:
        item["surprise"] = True
    # when / then
    with pytest.raises(SharedError):
        registration.registration_check(tmp_path, inventory)


def test_import_without_requirement_is_not_hidden(tmp_path: Path) -> None:
    # given
    inventory = fixture_tree(tmp_path)
    (tmp_path / "templates/go-service/main.go").write_text(
        f'package fixture\nimport _ "{MODULE}/health"\n'
    )
    # when / then
    with pytest.raises(SharedError, match="unregistered"):
        registration.registration_check(tmp_path, inventory)


@pytest.mark.parametrize(
    "paths",
    [
        [DIRECTORY + "/README.md", "templates/go-service/README.md"],
        ["services/product-service/old.go", "templates/go-service/new.go"],
        ["libs/unregistered/go.mod"],
    ],
)
def test_complete_diff_rejects_mixed_or_unknown_ownership(tmp_path: Path, paths: list[str]) -> None:
    fixture_tree(tmp_path / "base")
    fixture_tree(tmp_path / "head")
    with pytest.raises(SharedError):
        registration.plan_trees(
            tmp_path / "base", tmp_path / "head", paths, "a" * 40, "b" * 40, "a" * 40
        )


def test_root_tooling_conservatively_selects_all_modules(tmp_path: Path) -> None:
    fixture_tree(tmp_path / "base")
    fixture_tree(tmp_path / "head")
    result = registration.plan_trees(
        tmp_path / "base",
        tmp_path / "head",
        ["scripts/go/shared_candidate.py"],
        "a" * 40,
        "b" * 40,
        "a" * 40,
    )
    assert {r["module"] for r in result["obligations"]} == {
        DIRECTORY,
        "templates/go-service",
        "services/product-service",
    }


@pytest.mark.parametrize(
    "defect",
    [
        "missing",
        "duplicate",
        "unexpected",
        "failed",
        "skipped",
        "cancelled",
        "head",
        "mode",
        "identity",
    ],
)
def test_aggregate_rejects_incomplete_or_misattributed_results(defect: str) -> None:
    # given
    obligation = {
        "module": "templates/go-service",
        "phase": "unit",
        "mode": "candidate",
        "target": "shared-check",
    }
    expected = {"base": "a" * 40, "head": "b" * 40, "obligations": [obligation]}
    report = {
        **obligation,
        "base": "a" * 40,
        "head": "b" * 40,
        "status": "passed",
        "source_manifest": "c" * 64,
        "original_locks": {"mod": "d" * 64},
        "selected_graph": [{"Path": MODULE}],
        "candidate_sha": "b" * 40,
        "alternate_locks": {"mod": "e" * 64},
        "candidate_manifest": "f" * 64,
    }
    expected["inputs"] = {
        "templates/go-service": {"source_manifest": "c" * 64, "original_locks": {"mod": "d" * 64}}
    }
    expected["candidate_manifest"] = "f" * 64
    report["candidate_directory"] = "/tmp/owned/libs/servicekit"
    report["selected_graph"] = [
        {
            "Path": "example.com/consumer",
            "Main": True,
            "Dir": "/tmp/owned/consumer",
            "GoMod": "/tmp/owned/consumer/candidate.mod",
        },
        {
            "Path": MODULE,
            "Replace": {
                "Dir": "/tmp/owned/libs/servicekit",
                "GoMod": "/tmp/owned/libs/servicekit/go.mod",
            },
        },
    ]
    registration.reconcile(expected, [report])
    reports = [report]
    if defect == "missing":
        reports = []
    elif defect == "duplicate":
        reports.append(copy.deepcopy(report))
    elif defect in {"failed", "skipped", "cancelled"}:
        report["status"] = defect
    elif defect == "head":
        report["head"] = "c" * 40
    elif defect == "mode":
        report["mode"] = "normal"
    elif defect == "identity":
        del report["candidate_manifest"]
    else:
        report["phase"] = "unexpected"
    # when / then
    with pytest.raises(SharedError):
        registration.reconcile(expected, reports)


def test_installed_workflow_contracts() -> None:
    registration.shared_ci_contract(ROOT, ".github/workflows/go-pr-shared.yml")
    workflow = yaml.safe_load((ROOT / ".github/workflows/go-release-lib.yml").read_text())
    assert workflow.get("on", workflow.get(True)).keys() == {"workflow_dispatch"}
    assert workflow["concurrency"]["cancel-in-progress"] is False
    for name, job in workflow["jobs"].items():
        permissions = job.get("permissions", workflow["permissions"])
        assert "packages" not in permissions
        if permissions.get("contents") == "write":
            assert name in {"publish-tag", "publish-notes"}
            assert job["environment"] == "servicekit-release"
        assert job["steps"][0]["with"]["ref"] == "${{ github.sha }}"
    assert workflow["jobs"]["verify"]["needs"] == "publish-tag"
    assert workflow["jobs"]["publish-notes"]["needs"] == "verify"


def test_unknown_inventory_schema_is_rejected(tmp_path: Path) -> None:
    inventory = fixture_tree(tmp_path)
    inventory["schemaVersion"] = 99
    write_json(tmp_path / "go-module-topology.json", inventory)
    with pytest.raises(topology.TopologyError):
        topology.load_inventory(tmp_path / "go-module-topology.json")


def test_missing_library_selector_is_detected_by_actual_ci_contract(monkeypatch) -> None:
    # given: mutate only the shared classification in memory.
    original = select_pr_module.classify_module
    monkeypatch.setattr(
        select_pr_module,
        "classify_module",
        lambda path: None if path.startswith("libs/") else original(path),
    )
    inventory = topology.load_inventory(ROOT / "go-module-topology.json")
    library = next(m for m in inventory["modules"] if m["directory"] == DIRECTORY)
    # when / then
    with pytest.raises(topology.TopologyError, match="does not support"):
        topology.validate_selector_contract(library, library["ciWorkflow"])


@pytest.mark.integration
def test_git_planner_uses_verified_revisions_and_both_rename_endpoints(tmp_path: Path) -> None:
    # given
    fixture_tree(tmp_path)

    def git(*args):
        return subprocess.check_output(["git", *args], cwd=tmp_path, text=True, timeout=10).strip()

    git("init", "-q")
    git("config", "user.name", "Fixture")
    git("config", "user.email", "fixture@example.invalid")
    git("config", "commit.gpgsign", "false")
    git("add", ".")
    git("commit", "-qm", "base")
    base = git("rev-parse", "HEAD")
    (tmp_path / DIRECTORY / "main.go").write_text("package fixture\n// changed library\n")
    git("add", ".")
    git("commit", "-qm", "library")
    candidate = git("rev-parse", "HEAD")
    # when
    planned = registration.plan(tmp_path, base, candidate)
    # then
    assert planned["head"] == candidate
    assert planned["changed"] == [DIRECTORY]
    assert {r["module"] for r in planned["obligations"]} == {DIRECTORY}
    git("mv", DIRECTORY + "/main.go", "templates/go-service/moved.go")
    git("commit", "-qm", "cross module rename")
    with pytest.raises(SharedError, match="multiple Go modules"):
        registration.plan(tmp_path, candidate, git("rev-parse", "HEAD"))
