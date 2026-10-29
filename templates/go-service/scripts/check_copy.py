#!/usr/bin/env python3
"""Create and validate a repository-owned renamed scaffold copy."""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import shutil
import signal
import sys
import tempfile
import time
from dataclasses import dataclass
from pathlib import Path

from scaffold_process import CommandError, child_environment, run_command

WORKFLOW_SECONDS = 2_400
TEARDOWN_SECONDS = 15
SOURCE_MODULE = "github.com/igor-baiborodine/insurance-hub/templates/go-service"
COPY_MODULE = "github.com/igor-baiborodine/insurance-hub/services/example-copy"
SHARED_MODULE = "github.com/igor-baiborodine/insurance-hub/libs/servicekit"
SHARED_VERSION = "v0.2.0"
IDENTITY_REPLACEMENTS = (
    (SOURCE_MODULE, COPY_MODULE),
    ("templates/go-service", "services/example-copy"),
    ("scaffold/v1", "examplecopy/v1"),
    ("scaffold.v1", "examplecopy.v1"),
    ("scaffoldv1", "examplecopyv1"),
    ("go-service", "example-copy"),
)
IGNORED_DIRECTORIES = {".git", ".tools", ".venv", "__pycache__", ".pytest_cache", ".ruff_cache"}


class CopyError(RuntimeError):
    """The renamed scaffold copy violated its compatibility contract."""


class Terminated(BaseException):
    """The workflow received a handled termination signal."""

    def __init__(self, status: int) -> None:
        self.status = status


@dataclass(frozen=True)
class Configuration:
    module_root: Path
    repository_root: Path
    common_tooling_dir: Path
    make_arguments: tuple[str, ...]
    selected_python: Path
    go: str
    uv: str
    candidate_library: Path | None = None


def require(condition: bool, message: str) -> None:
    if not condition:
        raise CopyError(message)


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    return parser.parse_args(argv)


def configuration() -> Configuration:
    module_root = Path(__file__).resolve().parent.parent
    repository = os.environ.get("INSURANCE_HUB_ROOT")
    common = os.environ.get("COMMON_GO_TOOLING_DIR")
    python = os.environ.get("COMMON_GO_TOOLING_PYTHON")
    make_command = os.environ.get("MAKE_COMMAND", "make").split()
    require(bool(repository), "INSURANCE_HUB_ROOT is required")
    require(bool(common), "COMMON_GO_TOOLING_DIR is required")
    require(bool(python), "COMMON_GO_TOOLING_PYTHON is required")
    require(bool(make_command), "MAKE_COMMAND must name an executable")
    repository_path = Path(repository).resolve()
    common_path = Path(common).resolve()
    python_path = Path(python).absolute()
    require((repository_path / "pyproject.toml").is_file(), "repository pyproject.toml is missing")
    require((repository_path / "uv.lock").is_file(), "repository uv.lock is missing")
    require(common_path.is_dir(), f"shared Go tooling directory is missing: {common_path}")
    require(python_path.is_file(), f"selected Python is missing: {python_path}")
    candidate = os.environ.get("SERVICEKIT_CANDIDATE_LIBRARY")
    if os.environ.get("SERVICEKIT_CONTEXT_SOCKET"):
        require(bool(candidate), "candidate copy library is required")
    else:
        require(not candidate, "candidate copy requires a live owner context")
    return Configuration(
        module_root=module_root,
        repository_root=repository_path,
        common_tooling_dir=common_path,
        make_arguments=tuple(make_command),
        selected_python=python_path,
        go=os.environ.get("GO", "go"),
        uv=os.environ.get("UV", "uv"),
        candidate_library=Path(candidate).resolve(strict=True) if candidate else None,
    )


def _ignore(_: str, names: list[str]) -> set[str]:
    return {name for name in names if name in IGNORED_DIRECTORIES}


def inventory(root: Path) -> dict[str, str]:
    result: dict[str, str] = {}
    for path in sorted(root.rglob("*")):
        if not path.is_file():
            continue
        relative = path.relative_to(root)
        if any(part in IGNORED_DIRECTORIES for part in relative.parts):
            continue
        result[relative.as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


def replace_identities(copy_root: Path) -> None:
    for path in sorted(copy_root.rglob("*")):
        relative = path.relative_to(copy_root)
        if not path.is_file() or relative == Path("COPYING.md"):
            continue
        if any(part in IGNORED_DIRECTORIES for part in relative.parts):
            continue
        content = path.read_text(encoding="utf-8")
        for source, replacement in IDENTITY_REPLACEMENTS:
            content = content.replace(source, replacement)
        path.write_text(content, encoding="utf-8")


def remove_make_target(content: str, target: str) -> str:
    content = re.sub(
        rf"(?m)^{re.escape(target)}:[^\n]*\n(?:\t[^\n]*\n)*",
        "",
        content,
    )
    return re.sub(rf"(?m)(?<!\S){re.escape(target)}(?=\s|$)", "", content)


def rewrite_readme(content: str) -> str:
    lines = [
        line
        for line in content.splitlines()
        if "check-foundations" not in line
        and "`make check-copy`" not in line
        and line.strip() != "make check-copy"
    ]
    start = next(index for index, line in enumerate(lines) if line == "### Root scaffold delegates")
    end = next(
        index
        for index, line in enumerate(lines[start + 1 :], start + 1)
        if line == "## Create a repository-owned service"
    )
    replacement = [
        "### Repository onboarding",
        "",
        "The scaffold-only `go-scaffold-*` root delegates do not cover this copied module.",
        "When installed as an immediate `services/*-service` directory, the generic Go PR",
        "workflow selects its qualifying inputs. Complete the inventory and ownership onboarding",
        "documented in ../../docs/migration/phase-4/go-module-topology.md.",
        "",
    ]
    return "\n".join([*lines[:start], *replacement, *lines[end:]]) + "\n"


def remove_scaffold_only_surface(copy_root: Path) -> None:
    makefile = copy_root / "Makefile"
    content = (copy_root / "Makefile").read_text(encoding="utf-8")
    content, count = re.subn(
        r"(?ms)^# BEGIN SOURCE-SCAFFOLD CANDIDATE PHASES\n.*?^# END SOURCE-SCAFFOLD CANDIDATE PHASES\n",
        "",
        content,
    )
    require(count == 1, "source scaffold candidate phase block is missing")
    makefile.write_text(
        remove_make_target(
            remove_make_target(content, "check-copy"),
            "check-foundations",
        ),
        encoding="utf-8",
    )
    for relative in (
        "scripts/check_copy.py",
        "scripts/test_check_copy.py",
        "scripts/check_foundations.py",
        "scripts/test_check_foundations.py",
        "scripts/shared_phase.py",
    ):
        (copy_root / relative).unlink()
    readme = copy_root / "README.md"
    readme.write_text(rewrite_readme(readme.read_text(encoding="utf-8")), encoding="utf-8")


def create_copy(config: Configuration, temporary_root: Path) -> Path:
    copy_root = temporary_root / "services" / "example-copy"
    shutil.copytree(config.module_root, copy_root, ignore=_ignore)

    shutil.rmtree(copy_root / "gen")
    for base in (copy_root / "api", copy_root / "testdata" / "contract-baseline" / "api"):
        source = base / "scaffold" / "v1" / "example_service.proto"
        destination = base / "examplecopy" / "v1" / "example_service.proto"
        destination.parent.mkdir(parents=True)
        source.rename(destination)
        source.parent.rmdir()
        source.parent.parent.rmdir()

    replace_identities(copy_root)
    readme = copy_root / "README.md"
    readme.write_text(
        readme.read_text(encoding="utf-8").replace(
            "# Go service scaffold", "# Example copy service", 1
        ),
        encoding="utf-8",
    )
    remove_scaffold_only_surface(copy_root)
    return copy_root


def validate_copy_surface(copy_root: Path) -> None:
    stale = re.compile(
        r"github\.com/igor-baiborodine/insurance-hub/templates/go-service|"
        r"templates/go-service|go-service|scaffold\.v1|scaffold_v1|scaffoldv1|scaffold/v1"
    )
    for path in sorted(copy_root.rglob("*")):
        relative = path.relative_to(copy_root)
        if not path.is_file() or relative == Path("COPYING.md"):
            continue
        if any(part in IGNORED_DIRECTORIES for part in relative.parts):
            continue
        require(
            not stale.search(path.read_text(encoding="utf-8")),
            f"stale active scaffold identity found in {relative}",
        )

    makefile = (copy_root / "Makefile").read_text(encoding="utf-8")
    readme = (copy_root / "README.md").read_text(encoding="utf-8")
    require(not (copy_root / ".git").exists(), "copied service unexpectedly contains .git")
    require(not (copy_root / "go.work").exists(), "copied service unexpectedly contains go.work")
    require(
        not (copy_root / "scripts" / "check_copy.py").exists(),
        "copied service retained check_copy.py",
    )
    for relative in (".python-version", "pyproject.toml", "uv.lock"):
        require(not (copy_root / relative).exists(), f"copied service retained {relative}")
    require(not (copy_root / ".venv").exists(), "copied service unexpectedly contains .venv")
    require(
        "include $(INSURANCE_HUB_ROOT)/scripts/python/toolchain.mk" in makefile,
        "copied Makefile does not use repository Python tooling",
    )
    require(
        "COMMON_GO_TOOLING_DIR ?= $(INSURANCE_HUB_ROOT)/scripts/go/module" in makefile,
        "copied Makefile does not use repository Go tooling",
    )
    require(
        "check-copy" not in makefile
        and "check-foundations" not in makefile
        and "`make check-copy`" not in readme,
        "copied service retained scaffold-only copy interface",
    )
    require("make go-scaffold-" not in readme, "copied README claims scaffold-only coverage")
    require(
        "the generic Go PR" in readme and "workflow selects its qualifying inputs" in readme,
        "copied README lacks onboarding boundary",
    )
    require(
        "../../docs/migration/phase-4/go-module-topology.md" in readme,
        "copied README lacks topology guide",
    )
    require(
        not re.search(r"(?m)^replace(?:\s|$)", (copy_root / "go.mod").read_text(encoding="utf-8")),
        "copied go.mod contains a filesystem replacement",
    )
    require(
        (copy_root / "go.mod").read_text(encoding="utf-8").splitlines()[0]
        == f"module {COPY_MODULE}",
        "copied module identity differs",
    )
    require(
        f"{SHARED_MODULE} {SHARED_VERSION}" in (copy_root / "go.mod").read_text(encoding="utf-8"),
        "copied module lost its exact shared dependency",
    )
    config_go = (copy_root / "internal" / "config" / "config.go").read_text(encoding="utf-8")
    require('const DefaultServiceName = "example-copy"' in config_go, "service identity differs")
    require(
        "kitconfig.DefaultHealthAddress" in config_go,
        "copied management listener lost the shared 8081 default",
    )
    proto_paths = (
        copy_root / "api" / "examplecopy" / "v1" / "example_service.proto",
        copy_root
        / "testdata"
        / "contract-baseline"
        / "api"
        / "examplecopy"
        / "v1"
        / "example_service.proto",
    )
    for path in proto_paths:
        content = path.read_text(encoding="utf-8")
        require("package examplecopy.v1;" in content, "copied Protobuf package differs")
        require(
            f"{COPY_MODULE}/gen/examplecopy/v1;examplecopyv1" in content,
            "copied go_package differs",
        )


def command_environment(config: Configuration) -> dict[str, str]:
    return child_environment(
        {
            "MAKE_COMMAND": " ".join(config.make_arguments),
            "UV": config.uv,
            "PYTHON": str(config.selected_python),
            "COMMON_GO_TOOLING_PYTHON": str(config.selected_python),
        },
        preferred_executables=(config.go,),
    )


def run_copy_make(
    config: Configuration,
    copy_root: Path,
    deadline: float,
    activity: str,
    target: str,
    *variables: str,
    maximum_seconds: float = 600,
    log_path: Path | None = None,
    candidate: bool = False,
) -> None:
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise CopyError(f"{activity} could not start before the overall deadline")
    child_env = command_environment(config)
    if candidate:
        require(config.candidate_library is not None, "candidate library is required")
        child_env["GOFLAGS"] = f"-mod=readonly -modfile={copy_root / 'candidate.mod'}"
        child_env["SERVICEKIT_CANDIDATE_LIBRARY"] = str(config.candidate_library)
    run_command(
        [
            *config.make_arguments,
            "--no-print-directory",
            "-C",
            str(copy_root),
            target,
            *variables,
            f"GO={config.go}",
            f"UV={config.uv}",
            f"INSURANCE_HUB_ROOT={config.repository_root}",
        ],
        cwd=copy_root,
        env=child_env,
        timeout_seconds=min(maximum_seconds, remaining),
        teardown_seconds=TEARDOWN_SECONDS,
        activity=activity,
        log_path=log_path,
    )


def execute(config: Configuration) -> None:
    deadline = time.monotonic() + WORKFLOW_SECONDS
    source_before = inventory(config.module_root)
    with tempfile.TemporaryDirectory(prefix="go-service-copy.") as temporary:
        temporary_root = Path(temporary)
        copy_root = create_copy(config, temporary_root)
        validate_copy_surface(copy_root)

        run_copy_make(
            config,
            copy_root,
            deadline,
            "check-copy repository Python check",
            "python-toolchain-check",
            maximum_seconds=120,
        )

        phases = (
            ("bootstrapping renamed tools", "bootstrap-tools", (), 900),
            ("refreshing Protobuf dependencies", "update-proto-deps", (), 300),
            ("generating bindings", "gen-proto", (), 300),
            ("refreshing Go dependencies", "update-deps", (), 600),
            ("formatting handwritten Go", "format", ("FORMAT_SCOPE=all",), 300),
        )
        for activity, target, variables, budget in phases:
            run_copy_make(
                config,
                copy_root,
                deadline,
                f"check-copy {activity}",
                target,
                *variables,
                maximum_seconds=budget,
            )

        generated = sorted(
            path.relative_to(copy_root).as_posix()
            for path in (copy_root / "gen").rglob("*")
            if path.is_file()
        )
        require(
            generated
            == [
                "gen/examplecopy/v1/example_service.pb.go",
                "gen/examplecopy/v1/example_service_grpc.pb.go",
            ],
            f"generated output path set differs: {generated}",
        )

        copy_before = inventory(copy_root)
        if config.candidate_library is not None:
            require(
                (copy_root / "candidate.mod").is_file() and (copy_root / "candidate.sum").is_file(),
                "copied candidate manifests are missing",
            )
            for activity, target in (
                ("verifying copied candidate selection", "check-candidate-servicekit"),
                ("testing renamed copy against candidate", "test-race"),
                ("building renamed copy against candidate", "build"),
            ):
                run_copy_make(
                    config,
                    copy_root,
                    deadline,
                    f"check-copy {activity}",
                    target,
                    maximum_seconds=600,
                    candidate=True,
                )
            require(
                copy_before == inventory(copy_root), "candidate validation changed copied content"
            )
            require(source_before == inventory(config.module_root), "candidate copy changed source")
            print("check-copy: renamed copy selected and tested the servicekit candidate")
            return
        run_copy_make(
            config,
            copy_root,
            deadline,
            "check-copy running non-race tests",
            "test",
            maximum_seconds=600,
        )
        coverage = temporary_root / "example-copy-coverage.out"
        coverage_log = temporary_root / "example-copy-coverage.log"
        try:
            run_copy_make(
                config,
                copy_root,
                deadline,
                "check-copy running coverage capability",
                "coverage-check",
                f"COVERAGE_FILE={coverage}",
                maximum_seconds=600,
                log_path=coverage_log,
            )
        except CommandError:
            log = coverage_log.read_text(encoding="utf-8") if coverage_log.exists() else ""
            require(
                "coverage-check: below minimum" in log,
                "coverage failed without expected threshold diagnostic",
            )
        require(
            coverage.is_file() and coverage.stat().st_size > 0,
            "coverage output is missing or empty",
        )
        run_copy_make(
            config,
            copy_root,
            deadline,
            "check-copy running aggregate checks",
            "check",
            "FORMAT_SCOPE=all",
            maximum_seconds=1_200,
        )
        run_copy_make(
            config,
            copy_root,
            deadline,
            "check-copy running copied tooling fixtures",
            "test-tooling",
            maximum_seconds=1_200,
        )
        require(copy_before == inventory(copy_root), "normal validation changed copied content")

    require(
        source_before == inventory(config.module_root),
        "renamed-copy proof changed source module content",
    )
    print(
        "check-copy: repository-owned copy passed identity, generation, test, and aggregate checks"
    )


def _terminate(_signum: int, _frame: object) -> None:
    raise Terminated(143)


def main(argv: list[str] | None = None) -> int:
    parse_args(sys.argv[1:] if argv is None else argv)
    previous = signal.signal(signal.SIGTERM, _terminate)
    try:
        execute(configuration())
    except Terminated as error:
        print("check-copy: interrupted by SIGTERM", file=sys.stderr)
        return error.status
    except KeyboardInterrupt:
        print("check-copy: interrupted by SIGINT", file=sys.stderr)
        return 130
    except (CopyError, CommandError, OSError, UnicodeError, ValueError) as error:
        print(f"check-copy: {error}", file=sys.stderr)
        return 1
    finally:
        signal.signal(signal.SIGTERM, previous)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
