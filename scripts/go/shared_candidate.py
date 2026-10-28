"""Disposable candidate ownership, live phase context and resolution receipts."""

from __future__ import annotations

import json
import os
import secrets
import socket
import socketserver
import tempfile
import threading
from pathlib import Path
from typing import Any

import topology
from library_tooling import normal_guard
from shared_support import (
    DIRECTORY,
    MODULE,
    Budget,
    digest,
    environment,
    extract,
    manifest_digest,
    require,
    source_manifest,
)

SIMPLE_PHASES = {"unit", "race", "build", "lint", "vulnerability", "dependency", "format"}


def _python_executable(root: Path) -> str:
    """Return an absolute interpreter path without resolving a virtual-environment symlink."""
    return str(Path(os.environ.get("PYTHON", root / ".venv/bin/python")).absolute())


def locks(root: Path, prefix: str = "go") -> dict[str, str]:
    return {suffix: digest((root / f"{prefix}.{suffix}").read_bytes()) for suffix in ("mod", "sum")}


def selected_graph(
    root: Path, env: dict[str, str], budget: Budget, go: str = "go"
) -> list[dict[str, Any]]:
    output = budget.run([go, "list", "-m", "-json", "all"], root, "verify selected graph", env)
    return topology.decode_json_stream(output)


def verify_selection(graph: list[dict[str, Any]], candidate: Path, alternate: Path) -> None:
    record = next((r for r in graph if r["Path"] == MODULE), None)
    require(record is not None, "candidate module absent from actual graph")
    replacement = record.get("Replace", {})
    require(
        replacement.get("Dir") == str(candidate)
        and replacement.get("GoMod") == str(candidate / "go.mod"),
        "stale pin or wrong candidate directory selected",
    )
    main = next((r for r in graph if r.get("Main")), {})
    require(main.get("GoMod") == str(alternate), "alternate manifest was not selected")


class Candidate:
    """A context exists only while its controller and private socket are alive."""

    def __init__(self, checkout: Path, module: str, head: str, budget: Budget):
        self.checkout = checkout
        self.root = checkout / module
        self.library = checkout / DIRECTORY
        self.head = head
        self.budget = budget
        self.original = locks(self.root)
        self.library_manifest = manifest_digest(self.library)
        self.source = source_manifest(self.root)
        self.token = secrets.token_hex(32)
        self.receipts: dict[str, dict[str, Any]] = {}
        self.active_phases: set[str] = set()
        self.started_phases: set[str] = set()
        self.alternate = self.root / "candidate.mod"
        normal_guard(self.root, "go", budget)
        self.alternate.write_bytes((self.root / "go.mod").read_bytes())
        (self.root / "candidate.sum").write_bytes((self.root / "go.sum").read_bytes())
        env = environment(GOWORK="off", GOFLAGS=f"-mod=readonly -modfile={self.alternate}")
        budget.run(
            [
                "go",
                "mod",
                "edit",
                f"-modfile={self.alternate}",
                f"-replace={MODULE}={self.library}",
            ],
            self.root,
            "create candidate override",
            env,
        )
        self.env = env
        # Resolve only the alternate pair; any graph delta is retained in the receipts.
        budget.run(
            ["go", "mod", "tidy", f"-modfile={self.alternate}"],
            self.root,
            "resolve alternate dependency graph",
            env,
        )
        self.alternate_locks = locks(self.root, "candidate")
        self.verify()

    def verify(self) -> list[dict[str, Any]]:
        require(locks(self.root) == self.original, "original consumer manifests changed")
        require(manifest_digest(self.library) == self.library_manifest, "candidate source changed")
        current = source_manifest(self.root)
        current.pop("candidate.mod", None)
        current.pop("candidate.sum", None)
        require(current == self.source, "consumer source changed during candidate validation")
        require(
            locks(self.root, "candidate") == self.alternate_locks, "alternate manifests changed"
        )
        graph = selected_graph(self.root, self.env, self.budget)
        verify_selection(graph, self.library, self.alternate)
        return graph

    def request(self, value: dict[str, Any]) -> dict[str, Any]:
        require(
            value.get("token") == self.token and value.get("phase") in self.active_phases,
            "unissued candidate context",
        )
        require(value.get("cwd") == str(self.root), "candidate context used by another directory")
        require(value.get("operation") in {"begin", "end"}, "unsupported candidate operation")
        graph = self.verify()
        phase_name = value["phase"]
        if value["operation"] == "begin":
            require(phase_name not in self.started_phases, "duplicate candidate phase start")
            self.started_phases.add(phase_name)
        if value["operation"] == "end":
            require(phase_name in self.started_phases, "candidate phase did not start")
            require(phase_name not in self.receipts, "duplicate candidate phase receipt")
            self.receipts[phase_name] = {
                "selected_graph": graph,
                "original_locks": self.original,
                "alternate_locks": self.alternate_locks,
                "candidate_sha": self.head,
                "candidate_manifest": self.library_manifest,
                "candidate_directory": str(self.library),
                "source_manifest": digest(json.dumps(self.source, sort_keys=True).encode()),
            }
        return {"environment": self.env, "head": self.head, "alternate": str(self.alternate)}


class ContextServer(socketserver.UnixStreamServer):
    def __init__(self, path: Path, candidate: Candidate):
        self.candidate = candidate
        super().__init__(str(path), ContextRequest)
        path.chmod(0o600)


class ContextRequest(socketserver.StreamRequestHandler):
    def handle(self) -> None:
        self.request.settimeout(30)
        try:
            value = json.loads(self.rfile.readline(65536))
            response = self.server.candidate.request(value)
        except ValueError, OSError, RuntimeError:
            response = {"error": "candidate context verification failed"}
        self.wfile.write((json.dumps(response) + "\n").encode())


def context_request(operation: str, phase: str) -> dict[str, Any]:
    path = os.environ.get("SERVICEKIT_CONTEXT_SOCKET", "")
    token = os.environ.get("SERVICEKIT_CONTEXT_TOKEN", "")
    require(path and token, "candidate phase requires a live owner-issued context")
    with socket.socket(socket.AF_UNIX) as client:
        client.settimeout(120)
        client.connect(path)
        client.sendall(
            (
                json.dumps(
                    {
                        "operation": operation,
                        "phase": phase,
                        "token": token,
                        "cwd": str(Path.cwd().resolve()),
                    }
                )
                + "\n"
            ).encode()
        )
        stream = client.makefile("rb")
        value = json.loads(stream.readline(1024 * 1024))
    require("error" not in value, "candidate context verification failed")
    return value


def phase(phase_name: str) -> None:
    """Owning Make adapters call this for basic phases; unsupported phases fail closed."""
    require(phase_name in SIMPLE_PHASES, "phase needs an adoption-owned candidate adapter")
    context = context_request("begin", phase_name)
    root = Path.cwd()
    tools = Path(os.environ["TOOLS_BIN"])
    commands = {
        "unit": ["go", "test", "-count=1", "./..."],
        "race": ["go", "test", "-count=1", "-race", "./..."],
        "build": ["go", "build", "./..."],
        "lint": [str(tools / "golangci-lint"), "run", "./..."],
        "vulnerability": [str(tools / "govulncheck"), "./..."],
        "format": [str(tools / "golangci-lint"), "fmt", "--diff", "./..."],
        "dependency": ["go", "mod", "tidy", "-diff", f"-modfile={context['alternate']}"],
    }
    print(
        Budget(2100).run(
            commands[phase_name], root, "candidate " + phase_name, context["environment"]
        ),
        end="",
    )
    context_request("end", phase_name)


def validate(
    root: Path, expected: dict[str, Any], module: str, tools: Path
) -> list[dict[str, Any]]:
    obligations = [r for r in expected["obligations"] if r["module"] == module]
    require(obligations, "module has no validation obligation")
    require(len({r["mode"] for r in obligations}) == 1, "mixed resolution modes")
    mode = obligations[0]["mode"]
    budget = Budget()
    original = manifest_digest(root / module)
    reports = []
    try:
        with tempfile.TemporaryDirectory(prefix="servicekit-run-") as temporary:
            scratch = Path(temporary)
            checkout = scratch / "checkout"
            extract(root, expected["head"], checkout)
            target = checkout / module
            env = environment(PYTHON=_python_executable(root))
            arguments = [
                "make",
                "-C",
                str(target),
                f"PYTHON={env['PYTHON']}",
                f"TOOLS_BIN={tools}",
                "FORMAT_SCOPE=all",
            ]
            if mode == "candidate":
                candidate = Candidate(checkout, module, expected["head"], budget)
                socket_path = scratch / "context.sock"
                with ContextServer(socket_path, candidate) as server:
                    thread = threading.Thread(target=server.serve_forever, daemon=True)
                    thread.start()
                    try:
                        for owning_target in sorted({r["target"] for r in obligations}):
                            group = [r for r in obligations if r["target"] == owning_target]
                            candidate.active_phases = {r["phase"] for r in group}
                            child_env = {
                                **env,
                                "SERVICEKIT_CONTEXT_SOCKET": str(socket_path),
                                "SERVICEKIT_CONTEXT_TOKEN": candidate.token,
                            }
                            print(
                                budget.run(
                                    [*arguments, owning_target],
                                    root,
                                    "candidate consumer phase",
                                    child_env,
                                ),
                                end="",
                            )
                            for obligation in group:
                                require(
                                    obligation["phase"] in candidate.receipts,
                                    "owning phase did not verify candidate context",
                                )
                                reports.append(
                                    {**obligation, **candidate.receipts[obligation["phase"]]}
                                )
                    finally:
                        server.shutdown()
                        thread.join(timeout=10)
                        require(not thread.is_alive(), "candidate controller cleanup failed")
                candidate.verify()
            else:
                guarded_env = normal_guard(target, "go", budget)
                guarded_env.update(env)
                before = manifest_digest(target)
                original_locks = locks(target)
                for owning_target in sorted({r["target"] for r in obligations}):
                    print(
                        budget.run(
                            [*arguments, owning_target], root, "normal module gate", guarded_env
                        ),
                        end="",
                    )
                require(manifest_digest(target) == before, "normal gate changed source")
                graph = selected_graph(target, guarded_env, budget)
                reports = [
                    {
                        **r,
                        "original_locks": original_locks,
                        "source_manifest": before,
                        "selected_graph": graph,
                    }
                    for r in obligations
                ]
            return [
                {**r, "base": expected["base"], "head": expected["head"], "status": "passed"}
                for r in reports
            ]
    finally:
        require(manifest_digest(root / module) == original, "original checkout changed")
