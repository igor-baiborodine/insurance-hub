"""Controlled Go topology, CI, boundary, and aggregate validation cases."""

from __future__ import annotations

import copy
import json
import os
import sys
import time
from pathlib import Path

import pytest

import topology


def test_run_command_kills_term_resistant_descendant_after_leader_exits(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    descendant_pid = tmp_path / "descendant.pid"
    descendant = (
        "import os, pathlib, signal, time; "
        "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
        f"pathlib.Path({str(descendant_pid)!r}).write_text(str(os.getpid())); "
        "time.sleep(60)"
    )
    leader = (
        "import pathlib, subprocess, sys, time; "
        f"ready=pathlib.Path({str(descendant_pid)!r}); "
        f"subprocess.Popen([sys.executable, '-c', {descendant!r}]); "
        "deadline=time.monotonic()+5; "
        "\nwhile not ready.is_file() and time.monotonic() < deadline: time.sleep(0.01)"
    )
    monkeypatch.setattr(topology, "TEARDOWN_TIMEOUT_SECONDS", 1, raising=False)
    started = time.monotonic()

    # when / then
    with pytest.raises(topology.TopologyError, match="deadline"):
        topology.run_command(
            [sys.executable, "-c", leader],
            cwd=tmp_path,
            activity="exited leader",
            env={"PATH": os.environ.get("PATH", "")},
            timeout_seconds=0.2,
        )
    assert time.monotonic() - started < 2
    pid = int(descendant_pid.read_text(encoding="utf-8"))
    with pytest.raises(ProcessLookupError):
        os.kill(pid, 0)


def module_record(
    directory: str = "module",
    module_path: str = "example.test/insurance-hub/module",
    role: str = "business-service",
    consumers: list[str] | None = None,
) -> dict[str, object]:
    return {
        "directory": directory,
        "modulePath": module_path,
        "role": role,
        "owner": "fixture owner",
        "supportedModes": ["standalone"],
        "validationTarget": "check-go-pr",
        "validationVariables": [],
        "packageScope": "fixture ./...",
        "prerequisites": [],
        "consumers": consumers or [module_path],
        "localReplacements": [],
        "ciWorkflow": ".github/workflows/test.yml",
    }


def inventory(modules: list[dict[str, object]] | None = None) -> dict[str, object]:
    return {
        "schemaVersion": 1,
        "repositoryModulePrefix": "example.test/insurance-hub/",
        "modules": modules or [module_record()],
        "workspaces": [],
        "exclusions": [
            {
                "pathPattern": ".git/**",
                "category": "repository-metadata",
                "reason": "fixture metadata",
            },
            {
                "pathPattern": "**/testdata/**",
                "category": "fixture",
                "reason": "fixture-only module",
            },
        ],
    }


def write_inventory(root: Path, value: dict[str, object]) -> Path:
    path = root / "go-module-topology.json"
    path.write_text(json.dumps(value), encoding="utf-8")
    return path


def write_module(root: Path, record: dict[str, object], *, target_body: str = "@true") -> Path:
    directory = root / str(record["directory"])
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "go.mod").write_text(
        f"module {record['modulePath']}\n\ngo 1.23\n", encoding="utf-8"
    )
    (directory / "go.sum").write_text("", encoding="utf-8")
    (directory / "module.go").write_text("package module\n\nconst Value = 1\n", encoding="utf-8")
    (directory / "Makefile").write_text(
        f".PHONY: check-go-pr\ncheck-go-pr:\n\t{target_body}\n", encoding="utf-8"
    )
    return directory


def workflow_text(directory: str = "module") -> str:
    return f"""name: Fixture
on:
  pull_request:
    branches:
      - main
    paths:
      - '{directory}/**/*.go'
      - '{directory}/go.mod'
      - '{directory}/go.sum'
      - '{directory}/**/testdata/**'
permissions:
  contents: read
concurrency:
  cancel-in-progress: true
jobs:
  validate:
    steps:
      - uses: actions/checkout@v6
        with:
          ref: ${{{{ github.event.pull_request.head.sha }}}}
          fetch-depth: 0
      - uses: astral-sh/setup-uv@v7
        with:
          version: '0.13.1'
      - run: make python-toolchain-setup
      - env:
          PR_BASE: base
          PR_HEAD: head
        run: >-
          ./.venv/bin/python scripts/go/select_pr_module.py
          --base "$PR_BASE" --head "$PR_HEAD" --output "$GITHUB_OUTPUT"
      - uses: actions/setup-go@v7
        with:
          cache-dependency-path: ${{{{ steps.select.outputs.go_sum }}}}
      - uses: actions/cache@v5
        with:
          path: ${{{{ steps.select.outputs.module_dir }}}}/.tools
      - run: make -C "$MODULE_DIR" bootstrap-go-tools
      - env:
          COVERAGE_FILE: ${{{{ github.workspace }}}}/${{{{ steps.select.outputs.coverage_file }}}}
          FORMAT_BASE: ${{{{ steps.select.outputs.format_base }}}}
        run: |
          rm -f -- "$COVERAGE_FILE"
          rm -f -- "$MODULE_DIR/integration-coverage.txt"
          make -C "$MODULE_DIR" "$VALIDATION_TARGET" FORMAT_SCOPE=changed FORMAT_BASE="$FORMAT_BASE"
      - if: steps.select.outputs.module_role != 'service-scaffold'
        uses: codecov/codecov-action@v7
        with:
          files: ${{{{ steps.select.outputs.coverage_files }}}}
          flags: ${{{{ steps.select.outputs.coverage_flag }}}}
          disable_search: true
          fail_ci_if_error: true
"""


def write_workflow(root: Path, text: str | None = None) -> Path:
    path = root / ".github" / "workflows" / "test.yml"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text or workflow_text(), encoding="utf-8")
    return path


@pytest.mark.parametrize(
    "case",
    [
        "malformed-json",
        "unknown-root-field",
        "wrong-schema-version",
        "empty-modules",
        "unknown-role",
        "duplicate-directory",
        "unapproved-workspace",
        "unsafe-exclusion",
    ],
)
def test_inventory_schema_rejects_invalid_contracts(tmp_path: Path, case: str) -> None:
    # given
    value = inventory()
    if case == "malformed-json":
        path = tmp_path / "go-module-topology.json"
        path.write_text("{", encoding="utf-8")
    else:
        if case == "unknown-root-field":
            value["unexpected"] = True
        elif case == "wrong-schema-version":
            value["schemaVersion"] = 2
        elif case == "empty-modules":
            value["modules"] = []
        elif case == "unknown-role":
            value["modules"][0]["role"] = "private-mystery"
        elif case == "duplicate-directory":
            value["modules"].append(copy.deepcopy(value["modules"][0]))
        elif case == "unapproved-workspace":
            value["workspaces"] = [{}]
        else:
            value["exclusions"][0]["pathPattern"] = "../escape/**"
        path = write_inventory(tmp_path, value)

    # when / then
    with pytest.raises(topology.TopologyError):
        topology.load_inventory(path)


@pytest.mark.parametrize(
    ("case", "message"),
    [
        ("missing-module", "missing inventory module manifest"),
        ("missing-sum", "missing inventory module checksum"),
        ("identity", "module identity mismatch"),
        ("missing-target", "missing validation target"),
        ("unexpected", "unlisted developer-local manifest"),
        ("replacement", "unapproved filesystem replacement"),
    ],
)
def test_topology_reports_controlled_manifest_failures(
    tmp_path: Path, case: str, message: str
) -> None:
    # given
    value = inventory()
    write_workflow(tmp_path)
    module = write_module(tmp_path, value["modules"][0])
    if case == "missing-module":
        (module / "go.mod").unlink()
    elif case == "missing-sum":
        (module / "go.sum").unlink()
    elif case == "identity":
        (module / "go.mod").write_text("module example.test/wrong\n\ngo 1.23\n", encoding="utf-8")
    elif case == "missing-target":
        (module / "Makefile").write_text("other:\n\t@true\n", encoding="utf-8")
    elif case == "unexpected":
        unexpected = tmp_path / "unexpected"
        unexpected.mkdir()
        (unexpected / "go.mod").write_text("module example.test/unexpected\n", encoding="utf-8")
    else:
        with (module / "go.mod").open("a", encoding="utf-8") as stream:
            stream.write(
                "\nrequire example.test/dependency v0.0.0\nreplace example.test/dependency => ../dependency\n"
            )
    loaded = topology.load_inventory(write_inventory(tmp_path, value))

    # when
    errors, _ = topology.topology_errors(tmp_path, loaded)

    # then
    assert any(message in error for error in errors)


@pytest.mark.parametrize(
    ("old", "new", "message"),
    [
        ("on:\n", "on:\n  push:\n", "must not run on push"),
        ("      - 'module/go.mod'\n", "", "missing pull_request path trigger"),
        (
            "      - 'module/go.sum'\n",
            "      - 'module/**'\n",
            "forbidden pull_request path trigger",
        ),
        ("uses: astral-sh/setup-uv@v7", "uses: missing/setup@v1", "managed Python setup"),
        ("version: '0.13.1'", "version: latest", "pinned uv runtime"),
        ("scripts/go/select_pr_module.py", "scripts/go/inline.sh", "Python module selector"),
        ('--base "$PR_BASE"', '--base "$PR_HEAD"', "complete PR selector inputs"),
        (
            'make -C "$MODULE_DIR" "$VALIDATION_TARGET"',
            "make check",
            "selected-module validation contract",
        ),
        (
            "files: ${{ steps.select.outputs.coverage_files }}",
            "files: coverage.txt",
            "selected-module validation contract",
        ),
        (
            "permissions:\n",
            "permissions:\n      - uses: actions/setup-java@v5\n",
            "forbidden non-Go",
        ),
    ],
)
def test_ci_coverage_rejects_contract_drift(
    tmp_path: Path, old: str, new: str, message: str
) -> None:
    # given
    value = inventory()
    text = workflow_text().replace(old, new, 1)
    write_workflow(tmp_path, text)
    loaded = topology.load_inventory(write_inventory(tmp_path, value))

    # when / then
    with pytest.raises(topology.TopologyError, match=message):
        topology.ci_check(tmp_path, loaded)


def test_ci_coverage_preserves_historical_path_arguments(
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    repository_root = Path(topology.__file__).resolve().parents[2]

    # when
    status = topology.main(
        [
            "ci-coverage",
            str(repository_root),
            str(repository_root / "go-module-topology.json"),
        ]
    )

    # then
    assert status == 0
    assert "go-ci-coverage-check: passed" in capsys.readouterr().out


def boundary_fixture(tmp_path: Path, case: str) -> tuple[Path, dict[str, object]]:
    consumer = module_record("consumer", "example.test/insurance-hub/consumer")
    modules = [consumer]
    write_module(tmp_path, consumer)
    if case != "own":
        role = {
            "private": "business-service",
            "scaffold": "service-scaffold",
            "unapproved": "public-shared",
        }.get(case, "public-shared")
        owner = module_record("owner", "example.test/insurance-hub/owner", role)
        if case == "unapproved":
            owner["consumers"] = [str(owner["modulePath"])]
        modules.append(owner)
        owner_dir = write_module(tmp_path, owner)
        package_dir = owner_dir / "internal" if case == "private" else owner_dir / "public"
        package_dir.mkdir()
        (package_dir / "value.go").write_text(
            "package dependency\n\nconst Value = 1\n", encoding="utf-8"
        )
        import_path = f"{owner['modulePath']}/{'internal' if case == 'private' else 'public'}"
        if case == "unknown":
            modules.pop()
        consumer_dir = tmp_path / "consumer"
        with (consumer_dir / "go.mod").open("a", encoding="utf-8") as stream:
            stream.write(
                f"\nrequire {owner['modulePath']} v0.0.0\nreplace {owner['modulePath']} => ../owner\n"
            )
        (consumer_dir / "module.go").write_text(
            f'package module\n\nimport dependency "{import_path}"\n\nvar Value = dependency.Value\n',
            encoding="utf-8",
        )
    if case == "incomplete":
        (tmp_path / "consumer" / "module.go").write_text(
            'package module\n\nimport _ "example.test/insurance-hub/missing/package"\n',
            encoding="utf-8",
        )
    return tmp_path, inventory(modules)


@pytest.mark.parametrize(
    ("case", "message"),
    [
        ("own", "passed"),
        ("private", "sibling-internal-import"),
        ("scaffold", "scaffold-runtime-dependency"),
        ("unapproved", "unapproved-consumer"),
        ("unknown", "unknown-repository-owner"),
        ("incomplete", "incomplete-package-graph"),
    ],
)
def test_boundary_cases(
    tmp_path: Path, case: str, message: str, capsys: pytest.CaptureFixture[str]
) -> None:
    # given
    root, value = boundary_fixture(tmp_path, case)

    # when / then
    if case == "own":
        topology.boundary_check(root, value)
        assert message in capsys.readouterr().out
    else:
        with pytest.raises(topology.TopologyError, match=message):
            topology.boundary_check(root, value)


@pytest.mark.parametrize("case", ["unsupported-variable", "failed-target"])
def test_aggregate_module_failures(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, case: str
) -> None:
    # given
    value = inventory()
    target = "@false" if case == "failed-target" else "@true"
    write_module(tmp_path, value["modules"][0], target_body=target)
    monkeypatch.setenv(
        "GO_MODULE_CALLER_VARIABLES", "UNSUPPORTED" if case == "unsupported-variable" else ""
    )
    monkeypatch.setenv("MAKE_COMMAND", "make")
    before = topology.manifest_snapshot(tmp_path)

    # when / then
    with pytest.raises(topology.TopologyError):
        topology.modules_check(tmp_path, value)
    assert topology.manifest_snapshot(tmp_path) == before


def test_current_repository_topology_is_read_only() -> None:
    # given
    root = Path(topology.__file__).resolve().parents[2]
    loaded = topology.load_inventory(root / "go-module-topology.json")
    before = topology.manifest_snapshot(root)

    # when
    errors, _ = topology.topology_errors(root, loaded)
    topology.ci_check(root, loaded)
    topology.boundary_check(root, loaded)

    # then
    assert errors == []
    assert topology.manifest_snapshot(root) == before
