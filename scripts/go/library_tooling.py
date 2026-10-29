"""Library-only Go commands: no executable, schema generator or database assumptions."""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import tempfile
from pathlib import Path

import module.module_tooling as module_tooling

from shared_support import Budget, cli_entry, environment, manifest_digest, require


def scope_check(packages: list[dict]) -> None:
    from shared_support import MODULE

    prefix = MODULE.removesuffix("libs/servicekit")
    for package in packages:
        own = package["ImportPath"].removeprefix(MODULE + "/")
        require(
            own.split("/")[0]
            in {"config", "identity", "logger", "telemetry", "health", "internal"},
            "unsupported public library package",
        )
        for kind in ("Imports", "TestImports", "XTestImports"):
            require(
                not any(
                    p.startswith(prefix) and not p.startswith(MODULE + "/")
                    for p in package.get(kind, [])
                ),
                "library imports another repository module",
            )


def normal_guard(root: Path, go: str, budget: Budget) -> dict[str, str]:
    # Inspect effective settings before overriding them. Never repair caller input.
    inherited = dict(os.environ)
    settings = json.loads(
        budget.run(
            [go, "env", "-json", "GOWORK", "GOFLAGS"],
            root,
            "inspect effective Go environment",
            inherited,
            30,
        )
    )
    require(settings["GOWORK"] in {"", "off"}, "normal resolution rejects an active workspace")
    for flags in (os.environ.get("GOFLAGS", ""), settings["GOFLAGS"]):
        require(
            not re.search(r"(?:^|\s)-modfile(?:[=\s]|$)", flags),
            "normal resolution rejects alternate module files",
        )
        require(
            not re.search(r"(?:^|\s)-mod(?:[=\s])vendor(?:\s|$)", flags),
            "normal resolution rejects vendoring",
        )
    metadata = json.loads(
        budget.run(
            [go, "mod", "edit", "-json"], root, "inspect manifest", environment(GOWORK="off"), 30
        )
    )
    from shared_support import MODULE

    require(
        not any(r["Old"]["Path"] == MODULE for r in metadata.get("Replace") or []),
        "normal resolution rejects servicekit replacement",
    )
    return environment(GOWORK="off", GOFLAGS="-mod=readonly")


def verify(root: Path, go: str, tools: Path, budget: Budget) -> None:
    import yaml

    from shared_support import MODULE

    for name in ("golangci-lint", "govulncheck"):
        require((tools / name).is_file(), "missing library tools; run bootstrap-go-tools")
    version = os.environ.get("GO_VERSION", "1.27.3")
    require(
        budget.run([go, "env", "GOVERSION"], root, "verify Go").strip() == "go" + version,
        "Go toolchain pin mismatch",
    )
    require(
        re.search(rf"^go {re.escape(version)}$", (root / "go.mod").read_text(), re.M),
        "library Go directive pin mismatch",
    )
    lint = budget.run([str(tools / "golangci-lint"), "version"], root, "verify linter")
    require(
        f"has version {os.environ.get('GOLANGCI_LINT_VERSION', 'v2.14.0')[1:]} " in lint,
        "linter pin mismatch",
    )
    vuln = budget.run(
        [str(tools / "govulncheck"), "-version"], root, "verify vulnerability scanner"
    )
    require(
        f"Scanner: govulncheck@{os.environ.get('GOVULNCHECK_VERSION', 'v1.8.0')}" in vuln,
        "vulnerability scanner pin mismatch",
    )
    budget.run(
        [str(tools / "golangci-lint"), "config", "verify"], root, "verify formatter configuration"
    )
    config = yaml.safe_load((root / ".golangci.yml").read_text())
    require(
        config["formatters"]["enable"] == ["gofumpt", "goimports", "golines"],
        "formatter policy/order mismatch",
    )
    settings = config["formatters"]["settings"]
    require(
        settings["gofumpt"]["module-path"] == MODULE
        and settings["goimports"]["local-prefixes"] == [MODULE]
        and settings["golines"] == {"max-len": 100, "tab-len": 8},
        "formatter settings drift",
    )
    require(
        config["run"].get("tests") is True
        and config["run"].get("modules-download-mode") == "readonly"
        and config["linters"].get("default") == "standard"
        and "govet" not in config["linters"].get("disable", []),
        "lint/test/readonly policy drift",
    )


def execute(action: str, root: Path, go: str, tools: Path) -> None:
    budget = Budget()
    env = normal_guard(root, go, budget)
    if action == "bootstrap":
        tools.mkdir(parents=True, exist_ok=True)
        for package in (
            "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@"
            + os.environ["GOLANGCI_LINT_VERSION"],
            "golang.org/x/vuln/cmd/govulncheck@" + os.environ["GOVULNCHECK_VERSION"],
        ):
            budget.run(
                [go, "install", package],
                root,
                "install pinned library tool",
                environment(GOWORK="off", GOBIN=str(tools)),
            )
        verify(root, go, tools, budget)
        return
    verify(root, go, tools, budget)
    before = manifest_digest(root)
    if action in {"format", "format-check"}:
        os.environ.update(
            MODULE_ROOT=str(root),
            GOLANGCI_LINT=str(tools / "golangci-lint"),
            GOLANGCI_CONFIG=".golangci.yml",
        )
        module_tooling.format_command(
            argparse.Namespace(mode="format" if action == "format" else "check")
        )
    elif action == "check-deps":
        with tempfile.TemporaryDirectory(prefix="servicekit-deps-") as temporary:
            copy = Path(temporary) / "module"
            shutil.copytree(root, copy, ignore=shutil.ignore_patterns(".tools", ".git"))
            budget.run([go, "mod", "tidy"], copy, "check dependency consistency", env)
            for name in ("go.mod", "go.sum"):
                require(
                    (copy / name).read_bytes() == (root / name).read_bytes(),
                    "dependency drift: " + name,
                )
    elif action == "check-boundaries":
        import topology

        output = budget.run(
            [go, "list", "-json", "./..."], root, "inspect runtime package boundaries", env
        )
        scope_check(topology.decode_json_stream(output))
    elif action == "update-deps":
        budget.run([go, "mod", "tidy"], root, "update library dependencies", env)
    elif action != "verify":
        packages = os.environ.get("TEST_PACKAGES", "./...").split()
        require(packages and all(not p.startswith("-") for p in packages), "invalid TEST_PACKAGES")
        commands = {
            "lint": [str(tools / "golangci-lint"), "run", "./..."],
            "test": [go, "test", "-mod=readonly", "-count=1", *packages],
            "test-race": [go, "test", "-mod=readonly", "-count=1", "-race", "./..."],
            "coverage": [
                go,
                "test",
                "-mod=readonly",
                "-count=1",
                "-race",
                "-coverprofile=coverage.txt",
                "./...",
            ],
            "build": [go, "build", "-mod=readonly", "./..."],
            "vulncheck": [str(tools / "govulncheck"), "./..."],
        }
        print(budget.run(commands[action], root, action, env), end="")
    if action not in {"format", "update-deps"}:
        require(manifest_digest(root) == before, "library check changed source")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "action",
        choices=(
            "bootstrap",
            "verify",
            "format",
            "format-check",
            "lint",
            "test",
            "test-race",
            "coverage",
            "build",
            "check-deps",
            "update-deps",
            "vulncheck",
            "check-boundaries",
        ),
    )
    args = parser.parse_args()
    root = Path(os.environ["MODULE_ROOT"]).resolve()
    execute(args.action, root, os.environ.get("GO", "go"), Path(os.environ["TOOLS_BIN"]).resolve())
    return 0


if __name__ == "__main__":
    cli_entry(main)
