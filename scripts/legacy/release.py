"""Legacy CI selection, artifact, dependency and QA image helpers."""

from __future__ import annotations

import argparse
import os
import re
import signal
import subprocess
import sys
import tempfile
import time
import xml.etree.ElementTree as ET
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
NAME = re.compile(r"[a-z][a-z0-9-]*")
VERSION = re.compile(r"1\.[0-9]+\.[0-9]+")


def require_name(value: str) -> str:
    if not NAME.fullmatch(value):
        raise ValueError("invalid legacy module name")
    return value


def run_git(*args: str, env: dict[str, str] | None = None, deadline: float | None = None) -> str:
    started = time.monotonic()
    limit = min(started + 60, deadline) if deadline is not None else started + 60
    process = subprocess.Popen(
        ["git", *args],
        cwd=ROOT,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        start_new_session=True,
    )
    try:
        while True:
            remaining = limit - time.monotonic()
            if remaining <= 0:
                raise RuntimeError(f"Git {args[0]} exceeded its deadline")
            try:
                stdout, _stderr = process.communicate(timeout=min(30, remaining))
                break
            except subprocess.TimeoutExpired:
                print(
                    f"Git {args[0]} still running after {time.monotonic() - started:.0f}s",
                    file=sys.stderr,
                )
        if process.returncode:
            raise RuntimeError(f"Git {args[0]} failed (status {process.returncode})")
        return stdout
    except BaseException:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.communicate(timeout=5)
        raise


def select(paths: list[str], kind: str) -> str | None:
    modules = set()
    for path in paths:
        parts = path.split("/")
        if len(parts) < 3 or parts[0] != "legacy":
            continue
        module = parts[1]
        if not NAME.fullmatch(module):
            continue
        if kind == "api" and module.endswith("-api"):
            modules.add(module)
        elif kind == "service" and (
            module.endswith("-service") or module in ("command-bus", "agent-portal-gateway")
        ):
            modules.add(module)
        elif kind == "pr" and (
            module.endswith(("-api", "-service"))
            or module in ("command-bus", "agent-portal-gateway")
        ):
            modules.add(module)
    if len(modules) > 1:
        raise ValueError(f"multiple legacy modules changed: {', '.join(sorted(modules))}")
    if not modules and kind != "pr":
        raise ValueError("no changed legacy module detected")
    return next(iter(modules), None)


def select_jar(module: str, target: Path) -> Path:
    require_name(module)
    matches = sorted(
        path
        for path in target.glob(f"{module}*.jar")
        if not any(suffix in path.stem for suffix in ("sources", "javadoc", "tests"))
    )
    if len(matches) != 1:
        raise ValueError(f"expected one deployable JAR for {module}, found {len(matches)}")
    return matches[0]


def image_name(module: str) -> str:
    require_name(module)
    if module == "agent-portal-gateway":
        return "agent-portal-gateway-legacy"
    if module == "documents-service":
        return "document-api-legacy"
    if not module.endswith("-service"):
        raise ValueError("image build needs a service module")
    return f"{module.removesuffix('-service')}-api-legacy"


def manifest_path(module: str) -> Path:
    require_name(module)
    folder = "document" if module == "documents-service" else module.removesuffix("-service")
    return ROOT / "k8s/overlays/qa/svc" / folder / "legacy/deployment-patch.yaml"


def update_manifest(path: Path, image: str, version: str) -> bool:
    if not VERSION.fullmatch(version):
        raise ValueError("legacy version must be 1.x.x")
    source = path.read_text(encoding="utf-8")
    document = yaml.safe_load(source)
    if not isinstance(document, dict) or document.get("kind") != "Deployment":
        raise ValueError("expected a Deployment manifest")
    try:
        containers = document["spec"]["template"]["spec"]["containers"]
    except (KeyError, TypeError) as error:
        raise ValueError("Deployment manifest is missing containers") from error
    if not isinstance(containers, list):
        raise ValueError("Deployment containers must be a list")
    matches = [
        entry
        for entry in containers
        if isinstance(entry, dict)
        and isinstance(entry.get("image"), str)
        and entry["image"].split(":", 1)[0] == image
    ]
    if len(matches) != 1:
        raise ValueError(f"expected one container image {image}, found {len(matches)}")
    old = matches[0]["image"]
    new = f"{image}:{version}"
    if old == new:
        return False
    # Locate the parsed scalar in the original text to preserve comments and layout.
    node = yaml.compose(source)
    scalars = []

    def visit(item: yaml.Node) -> None:
        if isinstance(item, yaml.ScalarNode) and item.value == old:
            scalars.append(item)
        elif isinstance(item, yaml.MappingNode):
            for key, value in item.value:
                visit(key)
                visit(value)
        elif isinstance(item, yaml.SequenceNode):
            for value in item.value:
                visit(value)

    visit(node)
    if len(scalars) != 1:
        raise ValueError("container image scalar is ambiguous")
    scalar = scalars[0]
    path.write_text(
        source[: scalar.start_mark.index] + new + source[scalar.end_mark.index :], encoding="utf-8"
    )
    return True


def update_pom(source: str, module: str, version: str) -> str | None:
    require_name(module)
    if not VERSION.fullmatch(version):
        raise ValueError("legacy version must be 1.x.x")
    root = ET.fromstring(source)
    namespace = {"m": "http://maven.apache.org/POM/4.0.0"}
    dependencies = root.findall(".//m:dependency", namespace)
    selected = [
        dep for dep in dependencies if dep.findtext("m:artifactId", namespaces=namespace) == module
    ]
    if not selected:
        return None
    if len(selected) != 1 or selected[0].find("m:version", namespace) is None:
        raise ValueError(f"ambiguous or unversioned dependency on {module}")
    blocks = re.findall(r"<dependency>.*?</dependency>", source, flags=re.DOTALL)
    matching = [
        block
        for block in blocks
        if re.search(rf"<artifactId>\s*{re.escape(module)}\s*</artifactId>", block)
    ]
    if len(matching) != 1:
        raise ValueError(f"could not locate unique dependency text for {module}")
    block = matching[0]
    revised, count = re.subn(
        r"(<version>)[^<]*(</version>)", rf"\g<1>{version}\g<2>", block, count=1
    )
    if count != 1:
        raise ValueError("dependency version text is missing")
    return source.replace(block, revised, 1) if revised != block else None


def update_dependents(module: str, version: str, token: str) -> None:
    require_name(module)
    if not token:
        raise ValueError("CI_PAT is required")
    deadline = time.monotonic() + 1800
    with tempfile.TemporaryDirectory(prefix="legacy-git-") as directory:
        askpass = Path(directory) / "askpass"
        askpass.write_text(
            '#!/bin/sh\ncase "$1" in *Username*) printf "x-access-token";; '
            '*) printf "%s" "$CI_PAT";; esac\n',
            encoding="utf-8",
        )
        askpass.chmod(0o700)
        env = {
            **os.environ,
            "GIT_ASKPASS": str(askpass),
            "GIT_TERMINAL_PROMPT": "0",
            "CI_PAT": token,
        }
        run_git("config", "user.name", "github-actions[bot]", env=env, deadline=deadline)
        run_git(
            "config",
            "user.email",
            "github-actions[bot]@users.noreply.github.com",
            env=env,
            deadline=deadline,
        )
        for pom in sorted((ROOT / "legacy").glob("*/pom.xml")):
            dependent = pom.parent.name
            if dependent == module or dependent.endswith("-api"):
                continue
            changed = update_pom(pom.read_text(encoding="utf-8"), module, version)
            if changed is None:
                continue
            pom.write_text(changed, encoding="utf-8")
            print(f"Updating {dependent}", file=sys.stderr)
            run_git("add", str(pom.relative_to(ROOT)), env=env, deadline=deadline)
            run_git(
                "commit",
                "-m",
                f"chore({dependent}): bump {module} to {version}",
                env=env,
                deadline=deadline,
            )
            run_git("pull", "--rebase", "origin", "main", env=env, deadline=deadline)
            run_git("push", "origin", "main", env=env, deadline=deadline)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    chooser = sub.add_parser("select")
    chooser.add_argument("kind", choices=("pr", "api", "service"))
    chooser.add_argument("--before", required=True)
    chooser.add_argument("--after", required=True)
    chooser.add_argument("--output", required=True, type=Path)
    jar = sub.add_parser("jar")
    jar.add_argument("module")
    jar.add_argument("version")
    image = sub.add_parser("image")
    image.add_argument("module")
    manifest = sub.add_parser("manifest")
    manifest.add_argument("module")
    manifest.add_argument("repository")
    manifest.add_argument("version")
    manifest.add_argument("--allow-missing", action="store_true")
    dependents = sub.add_parser("dependents")
    dependents.add_argument("module")
    dependents.add_argument("version")
    args = parser.parse_args(argv)
    previous_term = signal.getsignal(signal.SIGTERM)

    def interrupted(_signal: int, _frame: object) -> None:
        raise InterruptedError("release operation terminated")

    signal.signal(signal.SIGTERM, interrupted)
    try:
        if args.action == "select":
            paths = run_git(
                "diff",
                "--name-only",
                f"{args.before}...{args.after}" if args.kind == "pr" else args.before,
                *([] if args.kind == "pr" else [args.after]),
            )
            module = select(paths.splitlines(), args.kind)
            if module:
                with args.output.open("a", encoding="utf-8") as output:
                    output.write(f"module={module}\nmodule_path=legacy/{module}\n")
            else:
                print("No legacy module changed")
        elif args.action == "jar":
            if not VERSION.fullmatch(args.version):
                raise ValueError("legacy version must be 1.x.x")
            target = ROOT / "legacy" / require_name(args.module) / "target"
            source = select_jar(args.module, target)
            destination = target / f"{args.module}-{args.version}.jar"
            if source != destination:
                source.rename(destination)
            print(destination)
        elif args.action == "image":
            print(image_name(args.module))
        elif args.action == "manifest":
            if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repository):
                raise ValueError("invalid repository coordinate")
            base = image_name(args.module) if args.module != "web-vue" else "web-vue-legacy"
            path = manifest_path(args.module)
            if not path.is_file():
                if args.allow_missing:
                    print(f"Warning: deployment manifest is missing: {path}", file=sys.stderr)
                    return 0
                raise ValueError(f"deployment manifest is missing: {path}")
            image = f"ghcr.io/{args.repository}/{base}"
            update_manifest(path, image, args.version)
            print(path.relative_to(ROOT))
        else:
            update_dependents(args.module, args.version, os.environ.get("CI_PAT", ""))
    except KeyboardInterrupt:
        print("Release operation interrupted", file=sys.stderr)
        return 130
    except InterruptedError:
        print("Release operation terminated", file=sys.stderr)
        return 143
    except (
        OSError,
        ValueError,
        RuntimeError,
        ET.ParseError,
        yaml.YAMLError,
        KeyError,
        TypeError,
    ) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 1
    finally:
        signal.signal(signal.SIGTERM, previous_term)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
