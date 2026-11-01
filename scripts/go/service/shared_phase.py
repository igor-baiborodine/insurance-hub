#!/usr/bin/env python3
"""Run configured Servicekit candidate phases for a Go service."""

from __future__ import annotations

import argparse
import os
import shutil
import sys
import tempfile
from collections.abc import Mapping
from pathlib import Path

from scripts.go.service.tooling_config import ServiceTooling, load_tooling_config
from scripts.go.service.tooling_process import run, run_cli
from scripts.python.process_runner import CommandError

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
GO_TOOLING_ROOT = REPOSITORY_ROOT / "scripts/go"
sys.path.insert(0, str(GO_TOOLING_ROOT))

from shared_candidate import context_request  # noqa: E402


def candidate_dockerignore(config: ServiceTooling) -> str:
    lines = ["*", "!go.mod", "!go.sum", "!Dockerfile", "!servicekit/", "!servicekit/**"]
    for path in config.candidate.copy_paths:
        lines.extend((f"!{path}/", f"!{path}/**"))
    lines.append("**/*_test.go")
    lines.extend(config.candidate.exclude_paths)
    return "\n".join(lines) + "\n"


def candidate_dockerfile(config: ServiceTooling) -> str:
    dockerfile = (config.module_root / "Dockerfile").read_text(encoding="utf-8")
    manifest_copy = "COPY go.mod go.sum ./\n"
    if dockerfile.count(manifest_copy) != 1:
        raise ValueError("Dockerfile manifest-copy contract changed")
    dockerfile = dockerfile.replace(manifest_copy, manifest_copy + "COPY servicekit /servicekit\n")
    entrypoint = f"ENTRYPOINT {list(config.image.entrypoint)!r}".replace("'", '"')
    if dockerfile.count(entrypoint) != 1:
        raise ValueError("Dockerfile entrypoint contract changed")
    return dockerfile.replace(
        entrypoint,
        "ARG SERVICEKIT_CANDIDATE_SHA\n"
        'LABEL insurance-hub.servicekit.candidate="${SERVICEKIT_CANDIDATE_SHA}"\n' + entrypoint,
    )


def source_build_arguments(environment: Mapping[str, str]) -> list[str]:
    arguments: list[str] = []
    for name in ("SOURCE_CREATED", "SOURCE_REPOSITORY", "SOURCE_REVISION", "SOURCE_VERSION"):
        value = environment.get(name, "")
        if not value:
            raise ValueError(f"{name} is required for candidate image identity")
        arguments.extend(("--build-arg", f"{name}={value}"))
    return arguments


def candidate_image(
    config: ServiceTooling, context: dict[str, object], environment: dict[str, str]
) -> None:
    """Build the verified library candidate and run the service-owned image contract."""
    candidate = REPOSITORY_ROOT / "libs/servicekit"
    if not candidate.is_dir():
        raise ValueError("candidate library directory is missing")
    head = context.get("head")
    alternate_value = context.get("alternate")
    if not isinstance(head, str) or not isinstance(alternate_value, str):
        raise ValueError("candidate context is incomplete")
    with tempfile.TemporaryDirectory(prefix=f"{config.service}-candidate-image-") as directory:
        build_root = Path(directory)
        for name in config.candidate.copy_paths:
            shutil.copytree(
                config.module_root / name,
                build_root / name,
                ignore=shutil.ignore_patterns("*_test.go"),
            )
        shutil.copytree(
            candidate,
            build_root / "servicekit",
            ignore=shutil.ignore_patterns("*_test.go", ".tools", "coverage.txt"),
        )
        alternate = Path(alternate_value)
        manifest = alternate.read_text(encoding="utf-8")
        replacement = str(candidate)
        if manifest.count(replacement) != 1:
            raise ValueError("candidate replacement is missing or ambiguous")
        (build_root / "go.mod").write_text(
            manifest.replace(replacement, "/servicekit"), encoding="utf-8"
        )
        shutil.copy2(alternate.with_suffix(".sum"), build_root / "go.sum")
        (build_root / "Dockerfile").write_text(candidate_dockerfile(config), encoding="utf-8")
        (build_root / ".dockerignore").write_text(candidate_dockerignore(config), encoding="utf-8")
        docker = environment.get("DOCKER", "docker")
        image = f"{config.image.name}:candidate-{head[:12]}-{os.getpid()}"
        try:
            run(
                [
                    docker,
                    "buildx",
                    "build",
                    "--build-arg",
                    f"SERVICEKIT_CANDIDATE_SHA={head}",
                    *source_build_arguments(environment),
                    "--file",
                    str(build_root / "Dockerfile"),
                    "--load",
                    "--platform",
                    config.image.platform,
                    "--provenance=false",
                    "--tag",
                    image,
                    str(build_root),
                ],
                cwd=REPOSITORY_ROOT,
                env=environment,
                timeout_seconds=1_200,
                teardown_seconds=20,
                activity=f"build {config.service} candidate image",
            )
            label = run(
                [
                    docker,
                    "image",
                    "inspect",
                    "--format",
                    '{{index .Config.Labels "insurance-hub.servicekit.candidate"}}',
                    image,
                ],
                cwd=REPOSITORY_ROOT,
                env=environment,
                timeout_seconds=30,
                activity=f"verify {config.service} candidate image source",
                capture=True,
            ).strip()
            if label != head:
                raise ValueError("image lacks the verified candidate source identity")
            run(
                [
                    "make",
                    "--no-print-directory",
                    config.candidate.image_check_target,
                    f"IMAGE_REF={image}",
                ],
                cwd=config.module_root,
                env=environment,
                timeout_seconds=900,
                teardown_seconds=20,
                activity=f"validate {config.service} candidate image",
            )
        finally:
            previous_failure = sys.exc_info()[0] is not None
            try:
                run(
                    [docker, "image", "rm", "--force", image],
                    cwd=REPOSITORY_ROOT,
                    env=environment,
                    timeout_seconds=60,
                    teardown_seconds=10,
                    activity=f"remove {config.service} candidate image",
                    capture=True,
                )
            except CommandError:
                if not previous_failure:
                    raise
                print("shared-phase: candidate image cleanup failed", file=sys.stderr)


def run_phase(config: ServiceTooling, phase: str) -> None:
    context = context_request("begin", phase)
    raw_environment = context.get("environment")
    if not isinstance(raw_environment, dict) or not all(
        isinstance(key, str) and isinstance(value, str) for key, value in raw_environment.items()
    ):
        raise ValueError("candidate context environment is invalid")
    environment = {**os.environ, **raw_environment}
    environment["SERVICEKIT_CANDIDATE_LIBRARY"] = str(REPOSITORY_ROOT / "libs/servicekit")
    run(
        ["make", "--no-print-directory", "check-candidate-servicekit"],
        cwd=config.module_root,
        env=environment,
        timeout_seconds=120,
        activity=f"verify {config.service} candidate selection",
    )
    if phase == "image":
        candidate_image(config, context, environment)
    else:
        for target in config.candidate.phase_targets[phase]:
            run(
                ["make", "--no-print-directory", target],
                cwd=config.module_root,
                env=environment,
                timeout_seconds=2_100,
                teardown_seconds=20,
                activity=f"candidate {config.service} {phase}: {target}",
            )
    context_request("end", phase)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True, type=Path)
    parser.add_argument("phase", choices=("generated", "breaking", "integration", "image"))
    arguments = parser.parse_args(argv)
    try:
        config = load_tooling_config(arguments.config)
        run_phase(config, arguments.phase)
    except (CommandError, OSError, RuntimeError, ValueError) as error:
        print(f"shared-phase: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="shared-phase"))
