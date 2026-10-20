"""Controlled Go topology, CI, boundary, and aggregate validation cases."""

from __future__ import annotations

import copy
import hashlib
import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path

import pytest

import select_pr_module
import topology

REPOSITORY_ROOT = Path(topology.__file__).resolve().parents[2]


def file_snapshot(root: Path) -> dict[str, str]:
    return {
        path.relative_to(root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
        for path in sorted(root.rglob("*"))
        if path.is_file() and ".git" not in path.relative_to(root).parts
    }


def topology_source_snapshot(root: Path) -> dict[str, str]:
    result: dict[str, str] = {}
    for path in sorted(root.rglob("*")):
        if not path.is_file():
            continue
        relative = path.relative_to(root)
        if any(
            part in {".git", ".tools", "node_modules", "third_party", "vendor"}
            for part in relative.parts
        ):
            continue
        selected = (
            path.suffix in {".go", ".mk"}
            or path.name in {"go.mod", "go.sum", "go.work", "go.work.sum", "Makefile"}
            or relative.as_posix() == "go-module-topology.json"
            or (relative.parts[:2] == ("scripts", "go") and path.suffix in {".py", ".sh"})
        )
        if selected:
            result[relative.as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


def fixture_environment(root: Path, inventory_path: Path) -> dict[str, str]:
    (root / ".go-topology-test-fixture").touch()
    return {
        **os.environ,
        "GO_TOPOLOGY_FIXTURE_INVENTORY": str(inventory_path),
        "GO_TOPOLOGY_FIXTURE_ROOT": str(root),
        "GO_TOPOLOGY_TEST_MODE": "1",
        "PYTHON": sys.executable,
    }


def run_root_make_fixture(
    root: Path, inventory_path: Path, target: str
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["make", "--no-print-directory", target, f"PYTHON={sys.executable}"],
        cwd=REPOSITORY_ROOT,
        env=fixture_environment(root, inventory_path),
        check=False,
        text=True,
        capture_output=True,
        timeout=60,
    )


def run_topology_launcher(
    root: Path, inventory_path: Path, launcher: str
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [str(REPOSITORY_ROOT / "scripts" / "go" / launcher)],
        cwd=REPOSITORY_ROOT,
        env=fixture_environment(root, inventory_path),
        check=False,
        text=True,
        capture_output=True,
        timeout=60,
    )


def git(root: Path, *arguments: str) -> str:
    return subprocess.run(
        ["git", *arguments],
        cwd=root,
        check=True,
        text=True,
        capture_output=True,
        timeout=10,
    ).stdout.strip()


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


@pytest.mark.parametrize("target", ["go-topology-check", "go-modules-check"])
def test_approved_workspace_fails_closed_at_public_make_targets(
    tmp_path: Path, target: str
) -> None:
    # given
    value = inventory()
    value["workspaces"] = [
        {
            "directory": ".",
            "owner": "fixture owner",
            "use": ["module"],
            "replacements": [],
            "validationTarget": "check",
            "ciWorkflow": ".github/workflows/test.yml",
        }
    ]
    write_module(tmp_path, value["modules"][0])
    write_workflow(tmp_path)
    inventory_path = write_inventory(tmp_path, value)
    environment = fixture_environment(tmp_path, inventory_path)
    before = file_snapshot(tmp_path)

    # when
    result = subprocess.run(
        ["make", "--no-print-directory", target, f"PYTHON={sys.executable}"],
        cwd=REPOSITORY_ROOT,
        env=environment,
        check=False,
        text=True,
        capture_output=True,
        timeout=60,
    )

    # then
    assert result.returncode != 0
    assert "approved workspaces are not supported" in result.stderr
    assert file_snapshot(tmp_path) == before


@pytest.mark.parametrize(
    ("case", "message"),
    [
        ("missing-module", "missing inventory module manifest"),
        ("missing-sum", "missing inventory module checksum"),
        ("identity", "module identity mismatch"),
        ("missing-target", "missing validation target"),
        ("unexpected", "unlisted developer-local manifest"),
        ("workspace", "unlisted developer-local manifest: go.work"),
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
    elif case == "workspace":
        (tmp_path / "go.work").write_text("go 1.23\n\nuse ./module\n", encoding="utf-8")
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
        ("      - 'module/**/*.go'\n", "", "missing pull_request path trigger"),
        ("      - 'module/go.mod'\n", "", "missing pull_request path trigger"),
        ("      - 'module/**/testdata/**'\n", "", "missing pull_request path trigger"),
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
            'make -C "$MODULE_DIR" bootstrap-go-tools',
            'make -C "$MODULE_DIR" verify-tools',
            "selected-module validation contract",
        ),
        (
            "COVERAGE_FILE: ${{ github.workspace }}/${{ steps.select.outputs.coverage_file }}",
            "COVERAGE_FILE: ${{ steps.select.outputs.coverage_file }}",
            "selected-module validation contract",
        ),
        (
            "FORMAT_BASE: ${{ steps.select.outputs.format_base }}",
            "FORMAT_BASE: ${{ github.event.pull_request.base.sha }}",
            "selected-module validation contract",
        ),
        ('rm -f -- "$COVERAGE_FILE"\n', "", "selected-module validation contract"),
        (
            "steps.select.outputs.module_role != 'service-scaffold'",
            "steps.select.outputs.module_dir != ''",
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


def test_selector_uses_merge_base_to_head_and_reports_it_for_formatting(tmp_path: Path) -> None:
    # given
    git(tmp_path, "init", "--quiet", "--initial-branch=main")
    git(tmp_path, "config", "user.name", "Topology Test")
    git(tmp_path, "config", "user.email", "topology@example.invalid")
    (tmp_path / "README.md").write_text("base\n", encoding="utf-8")
    git(tmp_path, "add", "README.md")
    git(tmp_path, "commit", "--quiet", "-m", "base")
    merge_base = git(tmp_path, "rev-parse", "HEAD")
    git(tmp_path, "checkout", "--quiet", "-b", "feature")
    changed = tmp_path / "services" / "product-service" / "internal" / "change.go"
    changed.parent.mkdir(parents=True)
    changed.write_text("package internal\n", encoding="utf-8")
    git(tmp_path, "add", changed.relative_to(tmp_path).as_posix())
    git(tmp_path, "commit", "--quiet", "-m", "feature")
    head = git(tmp_path, "rev-parse", "HEAD")
    git(tmp_path, "checkout", "--quiet", "main")
    changed.parent.mkdir(parents=True, exist_ok=True)
    changed.write_text("package internal\n", encoding="utf-8")
    git(tmp_path, "add", changed.relative_to(tmp_path).as_posix())
    git(tmp_path, "commit", "--quiet", "-m", "base tip")
    base = git(tmp_path, "rev-parse", "HEAD")
    before = file_snapshot(tmp_path)

    # when
    actual_merge_base, paths = select_pr_module.changed_paths(tmp_path, base, head)
    outputs = select_pr_module.selection_outputs(paths, actual_merge_base)

    # then
    assert actual_merge_base == merge_base
    assert paths == ["services/product-service/internal/change.go"]
    assert outputs["module_dir"] == "services/product-service"
    assert outputs["format_base"] == merge_base
    assert file_snapshot(tmp_path) == before


def boundary_fixture(tmp_path: Path, case: str) -> tuple[Path, dict[str, object]]:
    consumer = module_record("consumer", "example.test/insurance-hub/consumer")
    modules = [consumer]
    write_module(tmp_path, consumer)
    if case != "own":
        role = {
            "approved": "public-contract",
            "private": "business-service",
            "scaffold": "service-scaffold",
            "unapproved": "public-shared",
        }.get(case, "public-shared")
        owner = module_record("owner", "example.test/insurance-hub/owner", role)
        if case == "approved":
            owner["consumers"] = [str(consumer["modulePath"])]
        elif case == "unapproved":
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
        ("approved", "passed"),
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
    if case in {"own", "approved"}:
        topology.boundary_check(root, value)
        assert message in capsys.readouterr().out
    else:
        with pytest.raises(topology.TopologyError, match=message):
            topology.boundary_check(root, value)


def test_approved_public_contract_passes_real_boundary_launcher(tmp_path: Path) -> None:
    # given
    root, value = boundary_fixture(tmp_path, "approved")
    inventory_path = write_inventory(root, value)
    environment = fixture_environment(root, inventory_path)
    before = file_snapshot(root)

    # when
    result = subprocess.run(
        [str(REPOSITORY_ROOT / "scripts" / "go" / "check-boundaries.sh")],
        cwd=REPOSITORY_ROOT,
        env=environment,
        check=False,
        text=True,
        capture_output=True,
        timeout=60,
    )

    # then
    assert result.returncode == 0, result.stderr
    assert "go-boundary-check: passed 2 module graph" in result.stdout
    assert file_snapshot(root) == before


def test_local_replacement_control_and_public_checks_preserve_rejection(tmp_path: Path) -> None:
    # given
    consumer = module_record("consumer", "example.test/insurance-hub/consumer")
    owner = module_record(
        "dependency",
        "example.test/insurance-hub/dependency",
        "public-contract",
        consumers=[str(consumer["modulePath"])],
    )
    consumer_dir = write_module(tmp_path, consumer)
    write_module(tmp_path, owner)
    with (consumer_dir / "go.mod").open("a", encoding="utf-8") as stream:
        stream.write(
            "\nrequire example.test/insurance-hub/dependency v0.0.0\n"
            "replace example.test/insurance-hub/dependency => ../dependency\n"
        )
    (consumer_dir / "module.go").write_text(
        'package module\n\nimport dependency "example.test/insurance-hub/dependency"\n\n'
        "var Value = dependency.Value\n",
        encoding="utf-8",
    )
    inventory_path = write_inventory(tmp_path, inventory([consumer, owner]))
    write_workflow(tmp_path)
    fixture_environment(tmp_path, inventory_path)
    before = file_snapshot(tmp_path)

    # when
    resolution = subprocess.run(
        ["go", "list", "-mod=readonly", "./..."],
        cwd=consumer_dir,
        env={**os.environ, "GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"},
        check=False,
        text=True,
        capture_output=True,
        timeout=60,
    )
    topology_result = run_root_make_fixture(tmp_path, inventory_path, "go-topology-check")
    aggregate_result = run_root_make_fixture(tmp_path, inventory_path, "go-modules-check")

    # then
    assert resolution.returncode == 0, resolution.stderr
    for result in (topology_result, aggregate_result):
        assert result.returncode != 0
        assert "unapproved filesystem replacement ../dependency" in result.stderr
    assert file_snapshot(tmp_path) == before


def test_ambient_workspace_control_cannot_mask_standalone_failure(tmp_path: Path) -> None:
    # given
    ambient_root = tmp_path / "ambient"
    root = ambient_root / "repo"
    root.mkdir(parents=True)
    consumer = module_record("consumer", "example.test/insurance-hub/consumer")
    owner = module_record(
        "dependency",
        "example.test/insurance-hub/dependency",
        "public-contract",
        consumers=[str(consumer["modulePath"])],
    )
    consumer_dir = write_module(root, consumer)
    write_module(root, owner)
    with (consumer_dir / "go.mod").open("a", encoding="utf-8") as stream:
        stream.write("\nrequire example.test/insurance-hub/dependency v0.0.0\n")
    (consumer_dir / "module.go").write_text(
        'package module\n\nimport dependency "example.test/insurance-hub/dependency"\n\n'
        "var Value = dependency.Value\n",
        encoding="utf-8",
    )
    workspace = ambient_root / "go.work"
    workspace.write_text(
        "go 1.23\n\nuse (\n\t./repo/consumer\n\t./repo/dependency\n)\n",
        encoding="utf-8",
    )
    inventory_path = write_inventory(root, inventory([consumer, owner]))
    fixture_environment(root, inventory_path)
    before = file_snapshot(ambient_root)

    # when
    resolution = subprocess.run(
        ["go", "list", "-mod=readonly", "./..."],
        cwd=consumer_dir,
        env={
            **os.environ,
            "GOWORK": str(workspace),
            "GOPROXY": "off",
            "GOSUMDB": "off",
        },
        check=False,
        text=True,
        capture_output=True,
        timeout=60,
    )
    standalone = run_topology_launcher(root, inventory_path, "check-boundaries.sh")

    # then
    assert resolution.returncode == 0, resolution.stderr
    assert standalone.returncode != 0
    assert "rule=incomplete-package-graph" in standalone.stderr
    assert file_snapshot(ambient_root) == before


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


def test_aggregate_propagates_python_tooling_input_to_each_module(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    modules = [
        module_record(directory="scaffold", module_path="example.test/scaffold"),
        module_record(directory="product", module_path="example.test/product"),
    ]
    for record in modules:
        record["validationVariables"] = ["FORMAT_SCOPE"]
        observed = tmp_path / str(record["directory"]) / "observed.txt"
        target = f'@printf "%s\\n%s\\n" "$(PYTHON)" "$(FORMAT_SCOPE)" > "{observed}"'
        write_module(tmp_path, record, target_body=target)
    value = inventory(modules)
    monkeypatch.setenv("GO_MODULE_CALLER_VARIABLES", "FORMAT_SCOPE PYTHON")
    monkeypatch.setenv("FORMAT_SCOPE", "changed")
    monkeypatch.setenv("PYTHON", sys.executable)
    monkeypatch.setenv("MAKE_COMMAND", "make")

    # when
    topology.modules_check(tmp_path, value)

    # then
    for record in modules:
        observed = tmp_path / str(record["directory"]) / "observed.txt"
        assert observed.read_text(encoding="utf-8").splitlines() == [
            sys.executable,
            "changed",
        ]


def test_root_aggregate_accepts_explicit_python_override(tmp_path: Path) -> None:
    # given
    value = inventory()
    value["modules"][0]["validationVariables"] = ["FORMAT_SCOPE"]
    observed = tmp_path / "module" / "observed.txt"
    target = f'@printf "%s\\n%s\\n" "$(PYTHON)" "$(FORMAT_SCOPE)" > "{observed}"'
    write_module(tmp_path, value["modules"][0], target_body=target)
    write_workflow(tmp_path)
    inventory_path = write_inventory(tmp_path, value)
    (tmp_path / ".go-topology-test-fixture").touch()
    environment = {
        **os.environ,
        "GO_TOPOLOGY_FIXTURE_INVENTORY": str(inventory_path),
        "GO_TOPOLOGY_FIXTURE_ROOT": str(tmp_path),
        "GO_TOPOLOGY_TEST_MODE": "1",
    }

    # when
    result = subprocess.run(
        [
            "make",
            "--no-print-directory",
            "go-modules-check",
            f"PYTHON={sys.executable}",
            "FORMAT_SCOPE=all",
        ],
        cwd=REPOSITORY_ROOT,
        env=environment,
        check=False,
        text=True,
        capture_output=True,
        timeout=60,
    )

    # then
    assert result.returncode == 0, result.stderr
    assert observed.read_text(encoding="utf-8").splitlines() == [sys.executable, "all"]


def test_topology_launcher_propagates_signal_and_preserves_fixture(tmp_path: Path) -> None:
    # given
    root = tmp_path / "fixture"
    root.mkdir()
    value = inventory()
    write_module(root, value["modules"][0])
    write_workflow(root)
    inventory_path = write_inventory(root, value)
    environment = fixture_environment(root, inventory_path)
    control = tmp_path / "control"
    fake_bin = control / "bin"
    fake_bin.mkdir(parents=True)
    ready = control / "go-ready"
    child_pid = control / "go.pid"
    fake_go = fake_bin / "go"
    fake_go.write_text(
        "#!/bin/sh\n"
        f'printf "%s" "$$" > "{child_pid}"\n'
        f': > "{ready}"\n'
        "trap 'exit 143' HUP INT TERM\n"
        "while :; do sleep 1; done\n",
        encoding="utf-8",
    )
    fake_go.chmod(0o755)
    environment["PATH"] = f"{fake_bin}{os.pathsep}{environment['PATH']}"
    before = file_snapshot(root)
    process = subprocess.Popen(
        [str(REPOSITORY_ROOT / "scripts" / "go" / "check-topology.sh")],
        cwd=REPOSITORY_ROOT,
        env=environment,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    deadline = time.monotonic() + 5
    while not ready.is_file() and process.poll() is None and time.monotonic() < deadline:
        time.sleep(0.01)
    assert ready.is_file(), process.communicate(timeout=5)

    # when
    process.send_signal(signal.SIGTERM)
    _, stderr = process.communicate(timeout=5)

    # then
    assert process.returncode == 143, stderr
    assert file_snapshot(root) == before
    pid = int(child_pid.read_text(encoding="utf-8"))
    with pytest.raises(ProcessLookupError):
        os.kill(pid, 0)


def test_current_repository_topology_is_read_only() -> None:
    # given
    root = Path(topology.__file__).resolve().parents[2]
    loaded = topology.load_inventory(root / "go-module-topology.json")
    before = topology_source_snapshot(root)

    # when
    errors, _ = topology.topology_errors(root, loaded)
    topology.ci_check(root, loaded)
    topology.boundary_check(root, loaded)

    # then
    assert errors == []
    assert topology_source_snapshot(root) == before
