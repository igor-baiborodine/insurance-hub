"""Disposable integration tests for Product-specific Go tooling targets."""

from __future__ import annotations

import hashlib
import os
import shlex
import shutil
import sys
import time
from dataclasses import dataclass
from pathlib import Path

import pytest
from product_process import ProductCommandError, run

pytestmark = pytest.mark.integration
TOOL_NAMES = (
    "buf",
    "protoc-gen-go",
    "protoc-gen-go-grpc",
    "golangci-lint",
    "govulncheck",
    "sqlc",
)
IGNORED_DIRECTORIES = {".git", ".tools", ".venv", "__pycache__", ".pytest_cache", ".ruff_cache"}


@dataclass(frozen=True)
class Tooling:
    make_arguments: tuple[str, ...]
    repository_root: Path
    temporary_root: Path
    base_fixture: Path
    deadline: float
    go: str


def ignore_generated(_: str, names: list[str]) -> set[str]:
    return {name for name in names if name in IGNORED_DIRECTORIES}


def inventory(root: Path) -> dict[str, str]:
    result: dict[str, str] = {}
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root)
        if not path.is_file() or any(part in IGNORED_DIRECTORIES for part in relative.parts):
            continue
        result[relative.as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


@pytest.fixture(scope="module")
def tooling(tmp_path_factory: pytest.TempPathFactory) -> Tooling:
    module_root = Path(__file__).resolve().parent.parent
    repository_root = module_root.parents[1]
    common_tooling = repository_root / "scripts/go/module"
    tools_bin = module_root / ".tools/bin"
    make_arguments = tuple(shlex.split(os.environ.get("MAKE_COMMAND", "make")))
    assert make_arguments
    for tool in TOOL_NAMES:
        assert (tools_bin / tool).is_file(), f"missing pinned Product tool: {tools_bin / tool}"

    temporary_root = tmp_path_factory.mktemp("product-service-tooling")
    base = temporary_root / "base"
    shutil.copytree(module_root, base, ignore=ignore_generated)
    shutil.copytree(common_tooling, base / ".common-go-tooling", ignore=ignore_generated)
    private_bin = base / ".tools/bin"
    private_bin.mkdir(parents=True)
    for tool in TOOL_NAMES:
        (private_bin / tool).symlink_to(tools_bin / tool)
    return Tooling(
        make_arguments=make_arguments,
        repository_root=repository_root,
        temporary_root=temporary_root,
        base_fixture=base,
        deadline=time.monotonic() + 3_600,
        go=os.environ.get("GO", "go"),
    )


def new_fixture(tooling: Tooling, name: str) -> Path:
    fixture = tooling.temporary_root / name
    shutil.copytree(tooling.base_fixture, fixture, symlinks=True)
    return fixture


def run_target(
    tooling: Tooling,
    fixture: Path,
    name: str,
    target: str,
    *variables: str,
    expect_failure: str | None = None,
) -> None:
    before = inventory(fixture)
    log = tooling.temporary_root / "logs" / f"{name}.log"
    arguments = [
        *tooling.make_arguments,
        "--no-print-directory",
        "-C",
        str(fixture),
        target,
        f"GO={tooling.go}",
        f"PYTHON={sys.executable}",
        f"COMMON_GO_TOOLING_PYTHON={sys.executable}",
        f"COMMON_GO_TOOLING_DIR={fixture / '.common-go-tooling'}",
        f"PRODUCT_TOOLING_REPOSITORY_ROOT={tooling.repository_root}",
        *variables,
    ]
    remaining = tooling.deadline - time.monotonic()
    if remaining <= 0:
        pytest.fail("Product test-tooling exceeded its 3600-second overall deadline")
    if expect_failure is None:
        try:
            run(
                arguments,
                cwd=fixture,
                env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"},
                timeout_seconds=min(1_200, remaining),
                teardown_seconds=15,
                activity=f"Product test-tooling {name}",
                log_path=log,
            )
        except ProductCommandError as error:
            pytest.fail(
                f"{name} could not establish a passing {target} baseline: {error}\n"
                f"{log.read_text(encoding='utf-8')}"
            )
    else:
        with pytest.raises(ProductCommandError):
            run(
                arguments,
                cwd=fixture,
                env={**os.environ, "PYTHONDONTWRITEBYTECODE": "1"},
                timeout_seconds=min(1_200, remaining),
                teardown_seconds=15,
                activity=f"Product test-tooling {name}",
                log_path=log,
            )
        assert expect_failure in log.read_text(encoding="utf-8")
    assert before == inventory(fixture), f"{name} mutated its disposable Product checkout"


def mutate_fixture(fixture: Path, case: str) -> tuple[str, str, tuple[str, ...]]:
    if case == "format-new-file":
        path = fixture / "internal/config/controlled.go"
        path.write_text("package config\n\nfunc controlled( ) { }\n", encoding="utf-8")
        return (
            "format-check",
            "internal/config/controlled.go",
            ("FORMAT_FILES=internal/config/controlled.go",),
        )
    if case == "dependency":
        path = fixture / "go.mod"
        path.write_text(
            path.read_text(encoding="utf-8").replace(
                "google.golang.org/grpc v1.83.2", "google.golang.org/grpc v1.84.0"
            ),
            encoding="utf-8",
        )
        return "check-deps", "go.mod is not synchronized with update-deps", ()
    if case == "proto-breaking":
        path = fixture / "api/product/v1/product_service.proto"
        path.write_text(
            path.read_text(encoding="utf-8").replace("string code = 1;", "string code = 20;", 1),
            encoding="utf-8",
        )
        return "check-proto-breaking", 'Previously present field "1"', ()
    generated = fixture / "gen/product/v1"
    if case == "proto-generated-modified":
        path = generated / "product_service.pb.go"
        path.write_text(
            path.read_text(encoding="utf-8") + "\n// controlled drift\n", encoding="utf-8"
        )
        return "check-proto-drift", "generated output differs from regeneration", ()
    if case == "proto-generated-unexpected":
        (generated / "unexpected.txt").write_text(
            "controlled unexpected output\n", encoding="utf-8"
        )
        return "check-proto-drift", "unexpected output in generated tree", ()
    if case == "proto-generated-missing":
        (generated / "product_service_grpc.pb.go").unlink()
        return "check-proto-drift", "missing expected output", ()
    generated = fixture / "internal/postgres/dbgen"
    if case == "sql-generated-modified":
        path = generated / "queries.sql.go"
        path.write_text(
            path.read_text(encoding="utf-8") + "\n// controlled drift\n", encoding="utf-8"
        )
        return "check-sql-drift", "generated output differs from regeneration", ()
    if case == "sql-generated-unexpected":
        (generated / "unexpected.txt").write_text(
            "controlled unexpected output\n", encoding="utf-8"
        )
        return "check-sql-drift", "unexpected output in generated tree", ()
    if case == "sql-generated-missing":
        (generated / "querier.go").unlink()
        return "check-sql-drift", "missing expected output", ()
    if case == "build-missing-migrator":
        shutil.rmtree(fixture / "cmd/migrate")
        return "build", "expected executable migrate was not produced", ()
    if case == "build-missing-seeder":
        shutil.rmtree(fixture / "cmd/seed")
        return "build", "expected executable seed was not produced", ()
    raise AssertionError(f"unknown controlled Product tooling case: {case}")


@pytest.mark.parametrize(
    "case",
    [
        "format-new-file",
        "dependency",
        "proto-breaking",
        "proto-generated-modified",
        "proto-generated-unexpected",
        "proto-generated-missing",
        "sql-generated-modified",
        "sql-generated-unexpected",
        "sql-generated-missing",
        "build-missing-migrator",
        "build-missing-seeder",
    ],
)
def test_product_defect_is_rejected_without_mutation(tooling: Tooling, case: str) -> None:
    # given
    fixture = new_fixture(tooling, case)
    target, diagnostic, variables = mutate_fixture(fixture, case)

    # when / then
    run_target(tooling, fixture, case, target, *variables, expect_failure=diagnostic)


def test_go_bootstrap_does_not_invoke_non_go_tools(tooling: Tooling) -> None:
    # given
    fixture = new_fixture(tooling, "go-bootstrap")
    fake_bin = tooling.temporary_root / "go-bootstrap-bin"
    fake_bin.mkdir()
    trace = tooling.temporary_root / "go-bootstrap.trace"
    go = fake_bin / "go"
    go.write_text(
        "#!/bin/sh\n"
        "set -eu\n"
        'printf \'go %s\\n\' "$*" >>"$BOOTSTRAP_TRACE"\n'
        'case "${1:-}" in\n'
        "install) exit 0 ;;\n"
        "env) test \"${2:-}\" = GOVERSION; printf 'go1.27.3\\n' ;;\n"
        "*) exit 64 ;;\n"
        "esac\n",
        encoding="utf-8",
    )
    forbidden = fake_bin / "forbidden-non-go-tool"
    forbidden.write_text("#!/bin/sh\necho forbidden non-Go tool >&2\nexit 65\n", encoding="utf-8")
    go.chmod(0o755)
    forbidden.chmod(0o755)

    # when
    run_target(
        tooling,
        fixture,
        "go-bootstrap",
        "bootstrap-go-tools",
        f"GO={go}",
        f"NODE={forbidden}",
        f"NPM={forbidden}",
        f"MMDC={forbidden}",
        f"DOCKER={forbidden}",
        f"KUBECTL={forbidden}",
        f"JAVA={forbidden}",
        f"BOOTSTRAP_TRACE={trace}",
    )

    # then
    assert "go install github.com/bufbuild/buf/cmd/buf@" in trace.read_text(encoding="utf-8")
