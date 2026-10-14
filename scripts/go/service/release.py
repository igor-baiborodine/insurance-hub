#!/usr/bin/env python3
"""Select a conventional Go service release and update its QA semantic tag."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
import re
import subprocess


ROOT = Path(__file__).resolve().parents[3]
SHA = re.compile(r"[0-9a-f]{40}")
SERVICE = re.compile(r"[a-z][a-z0-9-]*")
VERSION = re.compile(r"1\.[0-9]+\.[0-9]+")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+")


class ReleaseSelectionError(RuntimeError):
    """A release input does not satisfy the shared Go service convention."""


@dataclass(frozen=True)
class Selection:
    module: str
    module_path: str
    service: str
    qa_kustomization: str


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ReleaseSelectionError(message)


def image_entry(lines: list[str], service: str, repository: str) -> int:
    expected_name = f"insurance-hub-{service}-api"
    expected_new_name = f"ghcr.io/{repository}/{service}-api"
    entries: list[tuple[str, str, int]] = []
    for index, line in enumerate(lines):
        match = re.fullmatch(r"  - name: (\S+)\n?", line)
        if not match:
            continue
        new_name = ""
        new_tag_index = -1
        for child_index in range(index + 1, len(lines)):
            child = lines[child_index]
            if re.match(r"  - name: ", child) or (child and not child[0].isspace()):
                break
            name_match = re.fullmatch(r"    newName: (\S+)\n?", child)
            if name_match:
                new_name = name_match.group(1)
            if re.fullmatch(r"    newTag: \S+\n?", child):
                require(new_tag_index == -1, "QA image entry has multiple newTag values")
                new_tag_index = child_index
        entries.append((match.group(1), new_name, new_tag_index))

    matches = [entry for entry in entries if entry[0] == expected_name]
    require(len(matches) == 1, f"QA Kustomization must contain exactly one {expected_name} image entry")
    name, new_name, tag_index = matches[0]
    require(name == expected_name and new_name == expected_new_name,
            f"QA image entry must publish {expected_new_name}")
    require(tag_index >= 0, "QA image entry must contain exactly one newTag")
    return tag_index


def select_service(paths: list[str], root: Path, repository: str) -> Selection:
    require(bool(REPOSITORY.fullmatch(repository)), "repository must use owner/name format")
    modules: set[str] = set()
    for value in paths:
        path = PurePosixPath(value)
        if len(path.parts) >= 3 and path.parts[0] == "services" and path.parts[1].endswith("-service"):
            modules.add(path.parts[1])
    require(len(modules) == 1, "release push must change exactly one services/<service>-service module")

    module = next(iter(modules))
    service = module.removesuffix("-service")
    require(bool(SERVICE.fullmatch(service)), "service name must match [a-z][a-z0-9-]*")
    module_path = f"services/{module}"
    qa_path = f"k8s/overlays/qa/svc/{service}/kustomization.yaml"
    require((root / module_path).is_dir(), f"module directory is missing: {module_path}")
    require((root / module_path / "Dockerfile").is_file(), f"module Dockerfile is missing: {module_path}/Dockerfile")
    require((root / qa_path).is_file(), f"QA Kustomization is missing: {qa_path}")
    image_entry((root / qa_path).read_text(encoding="utf-8").splitlines(keepends=True), service, repository)
    return Selection(module, module_path, service, qa_path)


def changed_paths(root: Path, before: str, after: str) -> list[str]:
    require(bool(SHA.fullmatch(before)) and set(before) != {"0"},
            "release before revision must be a nonzero full SHA")
    require(bool(SHA.fullmatch(after)), "release after revision must be a full SHA")
    for revision in (before, after):
        result = subprocess.run(
            ["git", "cat-file", "-e", f"{revision}^{{commit}}"], cwd=root, check=False,
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        require(result.returncode == 0, f"release revision is unavailable: {revision}")
    result = subprocess.run(
        ["git", "diff", "--name-only", before, after], cwd=root, check=True,
        text=True, capture_output=True,
    )
    return result.stdout.splitlines()


def update_qa_tag(root: Path, service: str, repository: str, version: str) -> Path:
    require(bool(SERVICE.fullmatch(service)), "service name must match [a-z][a-z0-9-]*")
    require(bool(REPOSITORY.fullmatch(repository)), "repository must use owner/name format")
    require(bool(VERSION.fullmatch(version)), "release version must be a 1.x semantic version")
    path = root / "k8s" / "overlays" / "qa" / "svc" / service / "kustomization.yaml"
    require(path.is_file(), f"QA Kustomization is missing: {path.relative_to(root)}")
    lines = path.read_text(encoding="utf-8").splitlines(keepends=True)
    tag_index = image_entry(lines, service, repository)
    newline = "\n" if lines[tag_index].endswith("\n") else ""
    lines[tag_index] = f"    newTag: {version}{newline}"
    path.write_text("".join(lines), encoding="utf-8")
    return path


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser()
    commands = result.add_subparsers(dest="command", required=True)
    select = commands.add_parser("select")
    select.add_argument("--before", required=True)
    select.add_argument("--after", required=True)
    select.add_argument("--repository", required=True)
    select.add_argument("--output", required=True, type=Path)
    update = commands.add_parser("update-qa")
    update.add_argument("--service", required=True)
    update.add_argument("--repository", required=True)
    update.add_argument("--version", required=True)
    return result


def main() -> None:
    arguments = parser().parse_args()
    if arguments.command == "select":
        selected = select_service(changed_paths(ROOT, arguments.before, arguments.after),
                                  ROOT, arguments.repository)
        with arguments.output.open("a", encoding="utf-8") as output:
            for name, value in selected.__dict__.items():
                output.write(f"{name}={value}\n")
        print(f"go-service-release: selected {selected.module}")
    else:
        path = update_qa_tag(ROOT, arguments.service, arguments.repository, arguments.version)
        print(f"go-service-release: updated {path.relative_to(ROOT)} to {arguments.version}")


if __name__ == "__main__":
    try:
        main()
    except (OSError, subprocess.SubprocessError, ReleaseSelectionError) as error:
        raise SystemExit(f"go-service-release: {error}") from None
