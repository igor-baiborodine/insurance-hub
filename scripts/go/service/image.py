#!/usr/bin/env python3
"""Build and validate a configured Go-service runtime image."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from collections.abc import Mapping
from pathlib import Path

from scripts.go.service.tooling_config import ServiceTooling, load_tooling_config
from scripts.go.service.tooling_process import run, run_cli
from scripts.python.process_runner import CommandError

REQUIRED_BUILD = (
    "IMAGE_NAME",
    "IMAGE_TAG",
    "IMAGE_REF",
    "SOURCE_CREATED",
    "SOURCE_REPOSITORY",
    "SOURCE_REVISION",
    "SOURCE_VERSION",
    "TARGET_PLATFORM",
)
REQUIRED_IDENTITY = (
    "SOURCE_CREATED",
    "SOURCE_REPOSITORY",
    "SOURCE_REVISION",
    "SOURCE_VERSION",
)


class ImageError(ValueError):
    """A service image violates its shared build or runtime contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise ImageError(message)


def required_environment(names: tuple[str, ...], environment: Mapping[str, str]) -> dict[str, str]:
    values = {name: environment.get(name, "") for name in names}
    missing = sorted(name for name, value in values.items() if not value)
    require(not missing, f"missing required environment: {', '.join(missing)}")
    return values


def captured(
    arguments: list[str],
    *,
    config: ServiceTooling,
    environment: Mapping[str, str],
    activity: str,
) -> str:
    return run(
        arguments,
        cwd=config.module_root,
        env=environment,
        timeout_seconds=30,
        activity=activity,
        capture=True,
    ).strip()


def validate_build_identity(
    config: ServiceTooling, environment: Mapping[str, str]
) -> dict[str, str]:
    """Validate image inputs against the selected module and checked-out source."""
    values = required_environment(REQUIRED_BUILD, environment)
    revision = values["SOURCE_REVISION"]
    require(
        bool(re.fullmatch(r"[0-9a-f]{40}", revision)),
        "SOURCE_REVISION must be 40 lowercase hexadecimal characters",
    )
    head = captured(
        ["git", "rev-parse", "--verify", "HEAD"],
        config=config,
        environment=environment,
        activity=f"resolve {config.service} source revision",
    )
    require(revision == head, "SOURCE_REVISION must match the checked-out HEAD")
    created = captured(
        ["git", "show", "-s", "--format=%cI", head],
        config=config,
        environment=environment,
        activity=f"resolve {config.service} source time",
    )
    require(values["SOURCE_CREATED"] == created, "SOURCE_CREATED must match the commit time")
    version = f"git-{revision[:12]}"
    require(values["SOURCE_VERSION"] == version, f"SOURCE_VERSION must be {version}")
    image_tag = f"{config.image.local_tag_prefix}{revision[:12]}"
    require(values["IMAGE_TAG"] == image_tag, f"IMAGE_TAG must be {image_tag}")
    require(values["IMAGE_NAME"] == config.image.name, "IMAGE_NAME differs from service config")
    require(
        values["IMAGE_REF"] == f"{values['IMAGE_NAME']}:{values['IMAGE_TAG']}",
        "IMAGE_REF must be derived from IMAGE_NAME and IMAGE_TAG",
    )
    require(
        values["TARGET_PLATFORM"] == config.image.platform,
        f"TARGET_PLATFORM must be {config.image.platform}",
    )
    return values


def build_image(config: ServiceTooling, environment: Mapping[str, str]) -> None:
    values = validate_build_identity(config, environment)
    docker = environment.get("DOCKER", "docker")
    run(
        [
            docker,
            "buildx",
            "build",
            "--build-arg",
            f"SOURCE_CREATED={values['SOURCE_CREATED']}",
            "--build-arg",
            f"SOURCE_REPOSITORY={values['SOURCE_REPOSITORY']}",
            "--build-arg",
            f"SOURCE_REVISION={values['SOURCE_REVISION']}",
            "--build-arg",
            f"SOURCE_VERSION={values['SOURCE_VERSION']}",
            "--load",
            "--platform",
            values["TARGET_PLATFORM"],
            "--provenance=false",
            "--tag",
            values["IMAGE_REF"],
            "--file",
            str(config.module_root / "Dockerfile"),
            str(config.module_root),
        ],
        cwd=config.module_root,
        env=environment,
        timeout_seconds=1_200,
        teardown_seconds=20,
        activity=f"build {config.service} image",
    )
    image_id = captured(
        [docker, "image", "inspect", "--format", "{{.Id}}", values["IMAGE_REF"]],
        config=config,
        environment=environment,
        activity=f"inspect {config.service} image identity",
    )
    require(image_id.startswith("sha256:"), "built image has no content-addressed image ID")


def inspect_image_contract(
    config: ServiceTooling, environment: Mapping[str, str]
) -> dict[str, object]:
    """Validate the reusable image platform, runtime identity, and OCI labels."""
    image = environment.get("IMAGE_REF", "")
    require(bool(image), "IMAGE_REF is required")
    identity = required_environment(REQUIRED_IDENTITY, environment)
    docker = environment.get("DOCKER", "docker")
    raw = captured(
        [docker, "image", "inspect", image],
        config=config,
        environment=environment,
        activity=f"inspect {config.service} image",
    )
    inspected = json.loads(raw)
    require(
        isinstance(inspected, list) and len(inspected) == 1,
        "image inspect returned an unexpected document",
    )
    record = inspected[0]
    require(isinstance(record, dict), "image inspect record is invalid")
    image_config = record.get("Config")
    require(isinstance(image_config, dict), "image configuration is invalid")
    labels = image_config.get("Labels")
    require(isinstance(labels, dict), "image labels are invalid")
    require(
        record.get("Os") == "linux" and record.get("Architecture") == "amd64",
        "image platform is not linux/amd64",
    )
    require(image_config.get("User") == config.image.user, "runtime user differs from config")
    require(
        image_config.get("Entrypoint") == list(config.image.entrypoint)
        and not (image_config.get("Cmd") or []),
        "image entrypoint differs from config",
    )
    if config.dockerfile is not None:
        expected_ports = {f"{port}/tcp" for port in config.dockerfile.ports}
        exposed_ports = image_config.get("ExposedPorts") or {}
        require(
            isinstance(exposed_ports, dict) and set(exposed_ports) == expected_ports,
            "image exposed ports differ from config",
        )
    expected_labels = {
        "org.opencontainers.image.created": identity["SOURCE_CREATED"],
        "org.opencontainers.image.source": identity["SOURCE_REPOSITORY"],
        "org.opencontainers.image.revision": identity["SOURCE_REVISION"],
        "org.opencontainers.image.title": config.image.title,
        "org.opencontainers.image.version": identity["SOURCE_VERSION"],
    }
    require(
        all(labels.get(key) == value for key, value in expected_labels.items()),
        "image OCI identity differs from config",
    )
    return record


def wait_for_liveness(url: str, expected: bytes, deadline_seconds: float = 30) -> None:
    deadline = time.monotonic() + deadline_seconds
    last_status = "no response"
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                body = response.read()
                if response.status == 200 and body == expected:
                    return
                last_status = f"HTTP {response.status} body={body!r}"
        except (urllib.error.URLError, TimeoutError) as error:
            last_status = type(error).__name__
        time.sleep(0.1)
    raise ImageError(f"image liveness exceeded {deadline_seconds:g}s: {last_status}")


def check_lifecycle(config: ServiceTooling, environment: Mapping[str, str]) -> None:
    lifecycle = config.image.lifecycle
    require(lifecycle is not None, "service config does not declare a shared lifecycle check")
    docker = environment.get("DOCKER", "docker")
    image = environment["IMAGE_REF"]
    name = f"{config.service}-image-{os.getpid()}"
    created = False
    try:
        arguments = [
            docker,
            "run",
            "--detach",
            "--name",
            name,
            "--publish",
            f"127.0.0.1::{lifecycle.container_port}",
        ]
        for key, value in sorted(lifecycle.environment.items()):
            arguments.extend(("--env", f"{key}={value}"))
        arguments.append(image)
        captured(
            arguments,
            config=config,
            environment=environment,
            activity=f"start {config.service} image",
        )
        created = True
        port = captured(
            [docker, "port", name, f"{lifecycle.container_port}/tcp"],
            config=config,
            environment=environment,
            activity=f"resolve {config.service} health port",
        )
        require(port.startswith("127.0.0.1:"), "unexpected image health port binding")
        wait_for_liveness(f"http://{port}{lifecycle.path}", lifecycle.expected_body.encode("utf-8"))
        captured(
            [docker, "stop", "--time", str(lifecycle.stop_seconds), name],
            config=config,
            environment=environment,
            activity=f"stop {config.service} image",
        )
        exit_code = captured(
            [docker, "inspect", "--format", "{{.State.ExitCode}}", name],
            config=config,
            environment=environment,
            activity=f"inspect {config.service} image exit",
        )
        require(exit_code == "0", f"image exited with status {exit_code}")
    finally:
        if created:
            previous_failure = sys.exc_info()[0] is not None
            try:
                captured(
                    [docker, "rm", "--force", name],
                    config=config,
                    environment=environment,
                    activity=f"remove {config.service} image container",
                )
            except CommandError, OSError:
                if not previous_failure:
                    raise
                print(
                    f"go-service-image: {config.service} container cleanup failed",
                    file=sys.stderr,
                )


def check_image(config: ServiceTooling, environment: Mapping[str, str]) -> None:
    inspect_image_contract(config, environment)
    check_lifecycle(config, environment)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True, type=Path)
    parser.add_argument("action", choices=("build", "check", "inspect"))
    arguments = parser.parse_args(argv)
    try:
        config = load_tooling_config(arguments.config)
        if arguments.action == "build":
            build_image(config, os.environ)
        elif arguments.action == "check":
            check_image(config, os.environ)
        else:
            inspect_image_contract(config, os.environ)
    except (CommandError, ImageError, OSError, ValueError, json.JSONDecodeError) as error:
        print(f"go-service-image: {error}", file=sys.stderr)
        return 1
    print(f"go-service-image: service={config.service} action={arguments.action} passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="go-service-image"))
