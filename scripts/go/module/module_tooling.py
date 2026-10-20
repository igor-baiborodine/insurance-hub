#!/usr/bin/env python3
"""Shared, self-contained implementation of Go module maintenance commands."""

from __future__ import annotations

import argparse
import difflib
import fnmatch
import hashlib
import os
import re
import shutil
import signal
import subprocess
import sys
import tempfile
import time
from collections.abc import Callable, Iterable, Mapping, Sequence
from pathlib import Path, PurePosixPath

COMMAND_TIMEOUT_SECONDS = 600
TEARDOWN_TIMEOUT_SECONDS = 10
HEARTBEAT_SECONDS = 30
GENERATED_HEADER = re.compile(r"^// Code generated .* DO NOT EDIT\.$", re.MULTILINE)


class ToolingError(RuntimeError):
    """A module-tooling contract or operation failed."""


class Terminated(Exception):
    """The CLI received a handled termination signal."""


def _handle_signal(signum: int, _frame: object) -> None:
    raise Terminated(signum)


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ToolingError(message)


def required_environment(name: str) -> str:
    value = os.environ.get(name, "")
    require(bool(value), f"{name} is required")
    return value


def command_environment(**values: str) -> dict[str, str]:
    environment = {
        "PATH": os.environ.get("PATH", ""),
        "HOME": os.environ.get("HOME", ""),
        "TMPDIR": os.environ.get("TMPDIR", "/tmp"),
    }
    for name in ("BOOTSTRAP_TRACE", "GOTOOLCHAIN"):
        if value := os.environ.get(name):
            environment[name] = value
    environment.update(values)
    return environment


def module_child_environment(**values: str) -> dict[str, str]:
    environment = command_environment(**values)
    for name in ("PRODUCT_TOOLING_REPOSITORY_ROOT", "PYTHON"):
        if value := os.environ.get(name):
            environment[name] = value
    return environment


def _terminate(process: subprocess.Popen[str]) -> None:
    if process.poll() is not None:
        process.communicate()
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        process.communicate(timeout=TEARDOWN_TIMEOUT_SECONDS)
        return
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.communicate(timeout=TEARDOWN_TIMEOUT_SECONDS)


def run_command(
    arguments: Sequence[str],
    *,
    cwd: Path,
    activity: str,
    env: Mapping[str, str] | None = None,
    capture: bool = True,
    timeout_seconds: float = COMMAND_TIMEOUT_SECONDS,
) -> subprocess.CompletedProcess[str]:
    """Run an owned child process with heartbeat, deadline, teardown, and reaping."""
    require(bool(arguments), "child command must not be empty")
    started = time.monotonic()
    deadline = started + timeout_seconds
    next_heartbeat = started + HEARTBEAT_SECONDS
    process = subprocess.Popen(
        list(arguments),
        cwd=cwd,
        env=dict(env or command_environment()),
        text=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE if capture else None,
        start_new_session=True,
    )
    try:
        while True:
            now = time.monotonic()
            remaining = deadline - now
            if remaining <= 0:
                _terminate(process)
                raise ToolingError(f"{activity} exceeded its {timeout_seconds:g}-second deadline")
            try:
                stdout, stderr = process.communicate(
                    timeout=min(remaining, max(0.1, next_heartbeat - now))
                )
                break
            except subprocess.TimeoutExpired:
                if time.monotonic() >= next_heartbeat:
                    elapsed = time.monotonic() - started
                    print(f"{activity}: still running after {elapsed:.0f}s", file=sys.stderr)
                    next_heartbeat = time.monotonic() + HEARTBEAT_SECONDS
    except BaseException:
        _terminate(process)
        raise
    result = subprocess.CompletedProcess(list(arguments), process.returncode, stdout, stderr)
    if result.returncode != 0:
        if capture and result.stdout:
            print(result.stdout, end="", file=sys.stdout)
        if capture and result.stderr:
            print(result.stderr, end="", file=sys.stderr)
        raise ToolingError(f"{activity} failed with status {result.returncode}")
    return result


def parse_coverage(
    profile: Path,
    minimum: int,
    module_path: str,
    exclusions: Sequence[str],
) -> tuple[int, int]:
    require(profile.is_file(), f"coverage-check: profile not found: {profile}")
    require(0 <= minimum <= 100, "coverage-check: minimum must be an integer from 0 to 100")
    require(
        bool(module_path)
        and not any(character.isspace() for character in module_path)
        and not module_path.endswith("/"),
        "coverage-check: module path must be a non-empty normalized value",
    )
    for exclusion in exclusions:
        path = PurePosixPath(exclusion)
        require(
            bool(exclusion)
            and not path.is_absolute()
            and exclusion == path.as_posix()
            and "." not in path.parts
            and ".." not in path.parts,
            f"coverage-check: invalid excluded relative path: {exclusion}",
        )
    lines = profile.read_text(encoding="utf-8").splitlines()
    require(
        bool(lines) and lines[0] in {"mode: set", "mode: count", "mode: atomic"},
        "coverage-check: invalid coverage profile header",
    )
    total = 0
    covered = 0
    prefix = f"{module_path}/"
    entry = re.compile(r"^(.*):[0-9]+\.[0-9]+,[0-9]+\.[0-9]+ ([0-9]+) ([0-9]+)$")
    for line_number, line in enumerate(lines[1:], start=2):
        match = entry.fullmatch(line)
        require(bool(match), f"coverage-check: invalid profile entry at line {line_number}")
        assert match is not None
        source, statements_text, count_text = match.groups()
        require(
            source.startswith(prefix),
            f"coverage-check: profile entry is outside module {module_path}: {source}",
        )
        relative = source.removeprefix(prefix)
        if any(
            relative == excluded or relative.startswith(f"{excluded}/") for excluded in exclusions
        ):
            continue
        statements = int(statements_text)
        total += statements
        if int(count_text) > 0:
            covered += statements
    require(total > 0, "coverage-check: no included statements in profile")
    require(100 * covered >= minimum * total, "coverage-check: below minimum")
    return covered, total


def coverage_command(arguments: argparse.Namespace) -> int:
    covered, total = parse_coverage(
        arguments.profile,
        arguments.minimum,
        arguments.module_path,
        arguments.exclusions,
    )
    percentage = 100 * covered / total
    print(
        f"coverage-check: {percentage:.2f}% ({covered}/{total} statements; "
        f"minimum {arguments.minimum}%)"
    )
    return 0


def coverage_minimum(value: str) -> int:
    if re.fullmatch(r"(?:100|[0-9]{1,2})", value) is None:
        raise argparse.ArgumentTypeError("coverage-check: minimum must be an integer from 0 to 100")
    return int(value)


def coverage_module_path(value: str) -> str:
    if not value or any(character.isspace() for character in value) or value.endswith("/"):
        raise argparse.ArgumentTypeError(
            "coverage-check: module path must be a non-empty normalized value"
        )
    return value


def coverage_exclusion(value: str) -> str:
    path = PurePosixPath(value)
    if (
        not value
        or path.is_absolute()
        or value != path.as_posix()
        or "." in path.parts
        or ".." in path.parts
    ):
        raise argparse.ArgumentTypeError(f"coverage-check: invalid excluded relative path: {value}")
    return value


def copy_module(source: Path, destination: Path, tooling: Path) -> None:
    def ignore(path: str, names: list[str]) -> set[str]:
        current = Path(path)
        ignored: set[str] = set()
        if current == source and ".tools" in names:
            ignored.add(".tools")
        return ignored

    shutil.copytree(source, destination, ignore=ignore)
    isolated_tooling = destination / ".common-go-tooling"
    shutil.copytree(tooling, isolated_tooling, dirs_exist_ok=True)


def file_inventory(root: Path, excluded: Iterable[Path], ignored_names: set[str]) -> dict[str, str]:
    excluded_resolved = {path.resolve() for path in excluded}
    result: dict[str, str] = {}
    for path in sorted(root.rglob("*")):
        if not path.is_file() or path.name in ignored_names:
            continue
        if any(path.resolve().is_relative_to(excluded_path) for excluded_path in excluded_resolved):
            continue
        result[path.relative_to(root).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


def deps_command(_: argparse.Namespace) -> int:
    module_root = Path(required_environment("MODULE_ROOT")).resolve(strict=True)
    tooling = Path(required_environment("COMMON_GO_TOOLING_DIR")).resolve(strict=True)
    go = os.environ.get("GO", "go")
    make = os.environ.get("MAKE_COMMAND", "make")
    for manifest in ("go.mod", "go.sum"):
        require(
            (module_root / manifest).is_file(),
            f"check-deps: required manifest is missing: {manifest}",
        )
    with tempfile.TemporaryDirectory(prefix="go-module-deps.") as temporary:
        isolated = Path(temporary) / "module"
        copy_module(module_root, isolated, tooling)
        isolated_tooling = isolated / ".common-go-tooling"
        before = file_inventory(
            isolated, [isolated / ".tools", isolated_tooling], {"go.mod", "go.sum"}
        )
        run_command(
            [
                make,
                "-C",
                str(isolated),
                "update-deps",
                f"GO={go}",
                f"COMMON_GO_TOOLING_DIR={isolated_tooling}",
            ],
            cwd=isolated,
            activity="check-deps update-deps",
            env=module_child_environment(),
            capture=False,
        )
        after = file_inventory(
            isolated, [isolated / ".tools", isolated_tooling], {"go.mod", "go.sum"}
        )
        require(before == after, "check-deps: update-deps changed files outside go.mod and go.sum")
        drift = False
        for manifest in ("go.mod", "go.sum"):
            generated = isolated / manifest
            require(generated.is_file(), f"check-deps: update-deps deleted {manifest}")
            current_lines = (
                (module_root / manifest).read_text(encoding="utf-8").splitlines(keepends=True)
            )
            generated_lines = generated.read_text(encoding="utf-8").splitlines(keepends=True)
            if current_lines != generated_lines:
                drift = True
                print(
                    f"check-deps: {manifest} is not synchronized with update-deps", file=sys.stderr
                )
                print(
                    "".join(
                        difflib.unified_diff(
                            current_lines,
                            generated_lines,
                            fromfile=f"current/{manifest}",
                            tofile=f"update-deps/{manifest}",
                        )
                    ),
                    end="",
                )
        require(not drift, "check-deps: dependency manifests have drift")
    print("check-deps: go.mod and go.sum match isolated update-deps output")
    return 0


def validate_relative(path_value: str, label: str) -> Path:
    path = PurePosixPath(path_value)
    require(
        bool(path_value)
        and not path.is_absolute()
        and "." not in path.parts
        and ".." not in path.parts,
        f"{label}: unsafe relative path: {path_value}",
    )
    return Path(*path.parts)


def generated_inventory(root: Path, generated_root: Path) -> list[str]:
    directory = root / generated_root
    if not directory.is_dir():
        return []
    return sorted(
        path.relative_to(root).as_posix()
        for path in directory.rglob("*")
        if path.is_file() or path.is_symlink()
    )


def generated_drift_command(_: argparse.Namespace) -> int:
    module_root = Path(required_environment("MODULE_ROOT")).resolve(strict=True)
    tooling = Path(required_environment("COMMON_GO_TOOLING_DIR")).resolve(strict=True)
    tools_bin = Path(required_environment("TOOLS_BIN")).resolve(strict=True)
    make = os.environ.get("MAKE_COMMAND", "make")
    check_name = required_environment("CHECK_NAME")
    target = required_environment("GENERATION_TARGET")
    inputs = required_environment("GENERATION_INPUTS").split()
    tools = required_environment("GENERATION_TOOLS").split()
    generated_root = validate_relative(required_environment("GENERATED_ROOT"), check_name)
    pattern = required_environment("GENERATED_PATTERN")
    outputs = required_environment("GENERATED_FILES").split()
    stale_pattern = os.environ.get("STALE_GENERATED_PATTERN", "")
    require(bool(outputs), f"{check_name}: no generated outputs are declared")
    require(
        len(set(outputs)) == len(outputs),
        f"{check_name}: generated output declarations contain duplicates",
    )
    for output in outputs:
        validate_relative(output, check_name)
        require(
            fnmatch.fnmatchcase(output, pattern),
            f"{check_name}: invalid generated output declaration: {output}",
        )
    for value in inputs:
        path = module_root / validate_relative(value, check_name)
        require(
            path.is_file() and os.access(path, os.R_OK),
            f"{check_name}: missing or unreadable generation input: {value}",
        )
    for tool in tools:
        require(
            (tools_bin / tool).is_file() and os.access(tools_bin / tool, os.X_OK),
            f"{check_name}: missing pinned tool: {tools_bin / tool}",
        )

    with tempfile.TemporaryDirectory(prefix="go-generated-drift.") as temporary:
        temporary_root = Path(temporary)
        generations = [temporary_root / "generation-one", temporary_root / "generation-two"]
        for generation in generations:
            (generation / ".tools" / "bin").mkdir(parents=True)
            for value in inputs:
                source = module_root / value
                destination = generation / value
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source, destination)
            for tool in tools:
                shutil.copy2(tools_bin / tool, generation / ".tools" / "bin" / tool)
            isolated_tooling = generation / ".common-go-tooling"
            shutil.copytree(tooling, isolated_tooling)
            before = file_inventory(
                generation, [generation / generated_root, isolated_tooling], set()
            )
            run_command(
                [make, "-C", str(generation), target, f"COMMON_GO_TOOLING_DIR={isolated_tooling}"],
                cwd=generation,
                activity=f"{check_name} {target}",
                env=module_child_environment(),
                capture=False,
            )
            after = file_inventory(
                generation, [generation / generated_root, isolated_tooling], set()
            )
            require(
                before == after, f"{check_name}: {target} mutated generation inputs or pinned tools"
            )
            actual = generated_inventory(generation, generated_root)
            require(
                actual == sorted(outputs),
                f"{check_name}: {generation.name} produced an undeclared output set",
            )
        for output in outputs:
            require(
                (generations[0] / output).read_bytes() == (generations[1] / output).read_bytes(),
                f"{check_name}: repeated generation is not byte-reproducible: {output}",
            )
        checkout = generated_inventory(module_root, generated_root)
        errors: list[str] = []
        for output in sorted(set(outputs) - set(checkout)):
            errors.append(f"missing expected output: {output}")
        for output in sorted(set(checkout) - set(outputs)):
            label = "unexpected output in generated tree"
            path = module_root / output
            if (
                stale_pattern
                and path.is_file()
                and re.search(stale_pattern, path.read_text(encoding="utf-8"), re.MULTILINE)
            ):
                label = "stale generated output is no longer declared"
            errors.append(f"{label}: {output}")
        for output in outputs:
            checkout_path = module_root / output
            if (
                checkout_path.is_file()
                and checkout_path.read_bytes() != (generations[0] / output).read_bytes()
            ):
                errors.append(f"generated output differs from regeneration: {output}")
        for error in errors:
            print(f"{check_name}: {error}", file=sys.stderr)
        require(not errors, f"{check_name}: generated output drift detected")
    print(f"{check_name}: exact output set and bytes match two reproducible regenerations")
    return 0


EXCLUDED_GO_DIRECTORIES = frozenset({"gen", "vendor", "third_party", "testdata", ".tools"})


def eligible_go_file(module_root: Path, value: str, *, explicit: bool) -> Path | None:
    normalized = value.removeprefix("./")
    relative = PurePosixPath(normalized)
    if not normalized or relative.is_absolute() or ".." in relative.parts:
        if explicit:
            raise ToolingError(f"format-go: explicit path must be module-relative: {value}")
        return None
    path = (module_root / Path(*relative.parts)).resolve()
    if not path.is_relative_to(module_root):
        if explicit:
            raise ToolingError(f"format-go: explicit path escapes the module: {value}")
        return None
    if path.suffix != ".go":
        if explicit:
            raise ToolingError(f"format-go: explicit path is not a Go file: {value}")
        return None
    module_relative = path.relative_to(module_root)
    if module_relative.parts[0] in EXCLUDED_GO_DIRECTORIES:
        if explicit:
            raise ToolingError(f"format-go: explicit path is excluded from formatting: {value}")
        return None
    if not path.is_file():
        if explicit:
            raise ToolingError(f"format-go: explicit path is not a file: {value}")
        return None
    if GENERATED_HEADER.search(path.read_text(encoding="utf-8", errors="replace")):
        if explicit:
            raise ToolingError(f"format-go: explicit path has a generated-file header: {value}")
        return None
    return module_relative


def git_paths(module_root: Path, arguments: Sequence[str]) -> list[str]:
    result = run_command(
        ["git", *arguments],
        cwd=module_root,
        activity="format-go Git selection",
        env=command_environment(GIT_CONFIG_NOSYSTEM="1"),
    )
    return [value for value in result.stdout.split("\0") if value]


def select_go_files(module_root: Path) -> tuple[str, list[Path]]:
    explicit = os.environ.get("FORMAT_FILES_SET", "0") == "1"
    values: list[str] = []
    if explicit:
        source = "FORMAT_FILES"
        values = os.environ.get("FORMAT_FILES", "").split()
    else:
        scope = os.environ.get("FORMAT_SCOPE", "changed")
        base = os.environ.get("FORMAT_BASE", "")
        if scope == "all":
            source = "FORMAT_SCOPE=all"
            values = [
                path.relative_to(module_root).as_posix() for path in module_root.rglob("*.go")
            ]
        elif scope == "changed":
            revision = base or "HEAD"
            run_command(
                ["git", "rev-parse", "--verify", "--quiet", f"{revision}^{{commit}}"],
                cwd=module_root,
                activity="format-go revision validation",
                env=command_environment(GIT_CONFIG_NOSYSTEM="1"),
            )
            source = (
                f"FORMAT_SCOPE=changed FORMAT_BASE={base}"
                if base
                else "FORMAT_SCOPE=changed against HEAD"
            )
            values = git_paths(
                module_root,
                [
                    "diff",
                    "--name-only",
                    "--diff-filter=ACMR",
                    "--relative",
                    "-z",
                    revision,
                    "--",
                    ".",
                ],
            )
            values.extend(
                git_paths(
                    module_root, ["ls-files", "--others", "--exclude-standard", "-z", "--", "*.go"]
                )
            )
        else:
            raise ToolingError(f"format-go: FORMAT_SCOPE must be 'changed' or 'all': {scope}")
    selected = {
        path
        for value in values
        if (path := eligible_go_file(module_root, value, explicit=explicit)) is not None
    }
    return source, sorted(selected)


def format_command(arguments: argparse.Namespace) -> int:
    module_root = Path(required_environment("MODULE_ROOT")).resolve(strict=True)
    formatter = required_environment("GOLANGCI_LINT")
    config = os.environ.get("GOLANGCI_CONFIG", ".golangci.yml")
    source, selected = select_go_files(module_root)
    operation = "format" if arguments.mode == "format" else "format-check"
    print(f"{operation}: selected {len(selected)} eligible handwritten Go file(s) from {source}")
    for path in selected:
        print(f"  {path.as_posix()}")
    if not selected:
        return 0
    command = [formatter, "fmt", "--config", config]
    if arguments.mode == "check":
        command.append("--diff")
        run_command(
            [*command, "--", *(str(path) for path in selected)],
            cwd=module_root,
            activity=operation,
            capture=False,
        )
        return 0
    for iteration in range(1, 6):
        before = {
            path: hashlib.sha256((module_root / path).read_bytes()).digest() for path in selected
        }
        run_command(
            [*command, "--", *(str(path) for path in selected)],
            cwd=module_root,
            activity=operation,
            capture=False,
        )
        after = {
            path: hashlib.sha256((module_root / path).read_bytes()).digest() for path in selected
        }
        if before == after:
            print(f"format: formatter pipeline converged after {iteration} pass(es)")
            return 0
    raise ToolingError("format-go: formatter pipeline did not converge after 5 passes")


def command_output(arguments: Sequence[str], module_root: Path, activity: str) -> str:
    return run_command(arguments, cwd=module_root, activity=activity).stdout.strip()


def expect_equal(label: str, expected: str, actual: str) -> None:
    require(
        expected == actual, f"verify-tools: {label} mismatch: expected '{expected}', got '{actual}'"
    )


def verify_tools_command(_: argparse.Namespace) -> int:
    module_root = Path(required_environment("MODULE_ROOT")).resolve(strict=True)
    config_name = os.environ.get("GOLANGCI_CONFIG", ".golangci.yml")
    config_path = module_root / config_name
    module_lines = (module_root / "go.mod").read_text(encoding="utf-8").splitlines()
    module_path = next((line.split()[1] for line in module_lines if line.startswith("module ")), "")
    module_go = next((line.split()[1] for line in module_lines if line.startswith("go ")), "")
    tools = {
        "buf": required_environment("BUF"),
        "protoc-gen-go": required_environment("PROTOC_GEN_GO"),
        "protoc-gen-go-grpc": required_environment("PROTOC_GEN_GO_GRPC"),
        "golangci-lint": required_environment("GOLANGCI_LINT"),
        "govulncheck": required_environment("GOVULNCHECK"),
    }
    for name, value in tools.items():
        require(
            os.access(value, os.X_OK),
            f"verify-tools: {name} is missing; run 'make bootstrap-tools'",
        )
    go = required_environment("GO")
    expect_equal("go.mod Go version", required_environment("GO_VERSION"), module_go)
    expect_equal(
        "Go toolchain version",
        f"go{module_go}",
        command_output([go, "env", "GOVERSION"], module_root, "verify Go"),
    )
    expect_equal(
        "Buf version",
        required_environment("BUF_VERSION").removeprefix("v"),
        command_output([tools["buf"], "--version"], module_root, "verify Buf"),
    )
    expect_equal(
        "protoc-gen-go version",
        f"protoc-gen-go {required_environment('PROTOC_GEN_GO_VERSION')}",
        command_output([tools["protoc-gen-go"], "--version"], module_root, "verify protoc-gen-go"),
    )
    expect_equal(
        "protoc-gen-go-grpc version",
        f"protoc-gen-go-grpc {required_environment('PROTOC_GEN_GO_GRPC_VERSION').removeprefix('v')}",
        command_output(
            [tools["protoc-gen-go-grpc"], "--version"], module_root, "verify protoc-gen-go-grpc"
        ),
    )
    lint_version_output = command_output(
        [tools["golangci-lint"], "version"], module_root, "verify golangci-lint"
    )
    match = re.match(r"golangci-lint has version ([^ ]+)", lint_version_output)
    expect_equal(
        "golangci-lint version",
        required_environment("GOLANGCI_LINT_VERSION").removeprefix("v"),
        match.group(1) if match else "",
    )
    vuln_output = command_output(
        [tools["govulncheck"], "-version"], module_root, "verify govulncheck"
    )
    match = re.search(r"^Scanner: govulncheck@(\S+)", vuln_output, re.MULTILINE)
    expect_equal(
        "govulncheck version",
        required_environment("GOVULNCHECK_VERSION"),
        match.group(1) if match else "",
    )
    for specification in filter(None, os.environ.get("EXTRA_TOOL_SPECS", "").split(";")):
        fields = specification.split("|")
        require(
            len(fields) == 4 and all(fields), "verify-tools: malformed extra tool specification"
        )
        name, path, expected, version_command = fields
        require(
            version_command == "version",
            f"verify-tools: unsupported extra tool version command: {version_command}",
        )
        require(
            os.access(path, os.X_OK), f"verify-tools: {name} is missing; run 'make bootstrap-tools'"
        )
        expect_equal(
            f"{name} version",
            expected,
            command_output([path, "version"], module_root, f"verify {name}"),
        )
    run_command(
        [tools["golangci-lint"], "config", "verify", "--config", config_name],
        cwd=module_root,
        activity="verify golangci-lint config",
    )
    config = config_path.read_text(encoding="utf-8")
    formatters = re.search(r"formatters:\n(?:.*\n)*?  enable:\n((?:    - .*\n)+)", config)
    order = (
        ",".join(line.removeprefix("    - ") for line in formatters.group(1).splitlines())
        if formatters
        else ""
    )
    expect_equal("formatter order", "gofumpt,goimports,golines", order)
    expect_equal("gofumpt module path", module_path, _config_value(config, "module-path"))
    prefixes = re.search(r"local-prefixes:\n\s+- ([^\n]+)", config)
    expect_equal("goimports local prefix", module_path, prefixes.group(1) if prefixes else "")
    expect_equal("golines maximum length", "100", _config_value(config, "max-len"))
    expect_equal("golines tab width", "8", _config_value(config, "tab-len"))
    formatter_output = command_output(
        [tools["golangci-lint"], "formatters", "--config", config_name],
        module_root,
        "verify formatters",
    )
    for formatter in ("gofumpt", "goimports", "golines"):
        require(
            re.search(rf"^{formatter}:", formatter_output, re.MULTILINE) is not None,
            f"verify-tools: {formatter} is not enabled",
        )
    linter_output = command_output(
        [tools["golangci-lint"], "linters", "--config", config_name], module_root, "verify linters"
    )
    require(
        re.search(r"^govet:", linter_output, re.MULTILINE) is not None,
        "verify-tools: govet is not enabled",
    )
    require("  tests: true" in config, "verify-tools: all-test analysis is not configured")
    require(
        "  modules-download-mode: readonly" in config,
        "verify-tools: readonly module resolution is not configured",
    )
    print(f"verified Go {module_go} and pinned module-local tools")
    print("verified golangci-lint schema v2 and formatter order: gofumpt, goimports, golines")
    print(f"verified module/import prefix: {module_path}")
    print("verified golines: 100 columns, tab width 8; readonly module loading; all tests; govet")
    return 0


def _config_value(config: str, name: str) -> str:
    match = re.search(rf"^\s+{re.escape(name)}:\s*(\S+)\s*$", config, re.MULTILINE)
    return match.group(1) if match else ""


def build_command(_: argparse.Namespace) -> int:
    module_root = Path(required_environment("MODULE_ROOT")).resolve(strict=True)
    go = os.environ.get("GO", "go")
    with tempfile.TemporaryDirectory(prefix="go-module-build.") as directory:
        output = Path(directory)
        run_command(
            [go, "build", "-mod=readonly", "-o", f"{output}/", "./..."],
            cwd=module_root,
            activity="build Go module",
            env=command_environment(GOWORK="off"),
            capture=False,
        )
        require(
            (output / "server").is_file() and os.access(output / "server", os.X_OK),
            "build: expected executable server was not produced",
        )
    return 0


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser()
    commands = result.add_subparsers(dest="command", required=True)
    coverage = commands.add_parser("coverage")
    coverage.add_argument("profile", type=Path)
    coverage.add_argument("minimum", type=coverage_minimum)
    coverage.add_argument("module_path", type=coverage_module_path)
    coverage.add_argument("exclusions", nargs="*", type=coverage_exclusion)
    coverage.set_defaults(handler=coverage_command)
    commands.add_parser("deps").set_defaults(handler=deps_command)
    commands.add_parser("generated-drift").set_defaults(handler=generated_drift_command)
    formatting = commands.add_parser("format-go")
    formatting.add_argument("mode", choices=("format", "check"))
    formatting.set_defaults(handler=format_command)
    commands.add_parser("verify-tools").set_defaults(handler=verify_tools_command)
    commands.add_parser("build").set_defaults(handler=build_command)
    return result


def main(argv: list[str] | None = None) -> int:
    arguments = parser().parse_args(argv)
    handler: Callable[[argparse.Namespace], int] = arguments.handler
    return handler(arguments)


if __name__ == "__main__":
    signal.signal(signal.SIGHUP, _handle_signal)
    signal.signal(signal.SIGTERM, _handle_signal)
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130) from None
    except Terminated as error:
        raise SystemExit(128 + int(error.args[0])) from None
    except (OSError, ToolingError) as error:
        print(error, file=sys.stderr)
        raise SystemExit(1) from None
