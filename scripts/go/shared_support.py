"""Bounded I/O and immutable source identities for shared-library tooling."""

from __future__ import annotations

import hashlib
import io
import json
import os
import re
import signal
import stat
import sys
import tarfile
import time
from pathlib import Path, PurePosixPath
from typing import Any

import topology

MODULE = "github.com/igor-baiborodine/insurance-hub/libs/servicekit"
DIRECTORY = "libs/servicekit"
REPOSITORY = "igor-baiborodine/insurance-hub"
WORKFLOW = ".github/workflows/go-release-lib.yml"
SHA = re.compile(r"[0-9a-f]{40}")
DIGEST = re.compile(r"[0-9a-f]{64}")


class SharedError(topology.TopologyError):
    """A shared-module contract failed."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise SharedError(message)


def exact(value: Any, keys: set[str], label: str) -> None:
    require(isinstance(value, dict) and set(value) == keys, f"invalid {label} fields")


def digest(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def read_json(path: Path) -> Any:
    return json.loads(path.read_text())


def write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def relative(value: str) -> str:
    require(isinstance(value, str), "path must be a string")
    path = PurePosixPath(value)
    require(
        value
        and value == path.as_posix()
        and not path.is_absolute()
        and not any(p in {".", ".."} for p in path.parts)
        and not any(c.isspace() for c in value),
        "unsafe relative path",
    )
    return value


def environment(**extra: str) -> dict[str, str]:
    result = {
        key: os.environ[key]
        for key in (
            "PATH",
            "HOME",
            "TMPDIR",
            "GOTOOLCHAIN",
            "GOPATH",
            "GOCACHE",
            "GOMODCACHE",
            "GOPROXY",
            "GOSUMDB",
            "GOPRIVATE",
            "GONOSUMDB",
            "GONOPROXY",
            "GOENV",
        )
        if key in os.environ
    }
    result.update(extra)
    return result


class Budget:
    """One monotonic work deadline; process cleanup has its own 10-second budget."""

    def __init__(self, seconds: float = 2400):
        self.deadline = time.monotonic() + seconds

    def run(
        self,
        args: list[str],
        cwd: Path,
        activity: str,
        env: dict[str, str] | None = None,
        limit: float | None = None,
    ) -> str:
        remaining = self.deadline - time.monotonic()
        require(remaining > 0, f"{activity}: overall deadline exceeded")
        print(f"{activity}: starting", file=sys.stderr)
        result = topology.run_command(
            args,
            cwd=cwd,
            activity=activity,
            env=env or environment(),
            timeout_seconds=min(remaining, limit or remaining),
        )
        print(f"{activity}: complete", file=sys.stderr)
        return result.stdout


def git(root: Path, *args: str) -> str:
    return Budget(60).run(["git", *args], root, "inspect Git", limit=60)


def verify_sha(root: Path, sha: str) -> None:
    require(SHA.fullmatch(sha), "expected full Git SHA")
    require(git(root, "rev-parse", f"{sha}^{{commit}}").strip() == sha, "wrong commit identity")


def source_manifest(root: Path) -> dict[str, dict[str, str]]:
    result = {}
    for path in sorted(root.rglob("*")):
        parts = path.relative_to(root).parts
        if len(parts) == 1 and parts[0] in {"coverage.txt", "integration-coverage.txt"}:
            continue
        if any(p in {".git", ".tools", "__pycache__", ".pytest_cache", ".venv"} for p in parts):
            continue
        require(not path.is_symlink(), "source symlink is unsupported")
        if path.is_file():
            result[path.relative_to(root).as_posix()] = {
                "sha256": digest(path.read_bytes()),
                "mode": "100755" if path.stat().st_mode & stat.S_IXUSR else "100644",
            }
    return result


def manifest_digest(root: Path) -> str:
    return digest(json.dumps(source_manifest(root), sort_keys=True).encode())


def extract(root: Path, sha: str, destination: Path) -> None:
    """Extract regular Git blobs only. No archive traversal, symlinks or submodules."""
    verify_sha(root, sha)
    expected = {}
    for entry in git(root, "ls-tree", "-rz", "--full-tree", sha).split("\0"):
        if not entry:
            continue
        metadata, name = entry.split("\t", 1)
        mode, kind, blob = metadata.split()
        if kind == "commit":
            require(name == "ai/framework", "unsupported candidate submodule")
            continue
        require(mode in {"100644", "100755"}, "unsupported Git source mode")
        expected[name] = blob
    observed = set()
    result = topology.run_command(
        ["git", "archive", "--format=tar", sha],
        cwd=root,
        activity="extract Git source",
        text=False,
        timeout_seconds=60,
    )
    with tarfile.open(fileobj=io.BytesIO(result.stdout)) as archive:
        for member in archive:
            name = member.name.rstrip("/")
            relative(name)
            require(member.isdir() or member.isfile(), "unsupported Git source mode")
            output = destination / name
            if member.isdir():
                output.mkdir(parents=True, exist_ok=True)
                continue
            output.parent.mkdir(parents=True, exist_ok=True)
            stream = archive.extractfile(member)
            require(stream is not None, "missing source blob")
            content = stream.read()
            blob = hashlib.sha1(b"blob " + str(len(content)).encode() + b"\0" + content).hexdigest()
            require(expected.get(name) == blob, "archive changed Git source content")
            observed.add(name)
            output.write_bytes(content)
            output.chmod(0o755 if member.mode & stat.S_IXUSR else 0o644)
    require(observed == set(expected), "archive omitted Git source content")


def cli_entry(main: Any) -> None:
    def interrupted(signum: int, _frame: object) -> None:
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGTERM, interrupted)
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130) from None
    except (OSError, ValueError, SharedError, topology.TopologyError) as error:
        print(f"shared tooling: {error}", file=sys.stderr)
        raise SystemExit(1) from None
    except KeyError, TypeError, AttributeError:
        print("shared tooling: malformed input or missing required environment", file=sys.stderr)
        raise SystemExit(1) from None
