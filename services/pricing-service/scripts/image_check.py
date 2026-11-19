#!/usr/bin/env python3
"""Validate Pricing image commands, contents, safe failures, and runtime isolation."""

from __future__ import annotations

import json
import os
import re
import sys
import tarfile
import tempfile
from collections.abc import Sequence
from pathlib import Path

from scripts.go.service.tooling_process import run, run_cli
from scripts.python.process_runner import CommandError

MODULE_ROOT = Path(__file__).resolve().parent.parent
PACKAGED_FILES = {"pricing-service", "pricing-migrate", "pricing-seed"}


class ImageCheckError(ValueError):
    """The Pricing runtime image violates its service-owned contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise ImageCheckError(message)


def captured(arguments: list[str], activity: str, timeout: float = 30) -> str:
    return run(
        arguments,
        cwd=MODULE_ROOT,
        env=os.environ,
        timeout_seconds=timeout,
        activity=activity,
        capture=True,
    ).strip()


def validate_image_contents(names: set[str]) -> None:
    """Require all entry points and reject development or shell leakage."""
    missing = sorted(PACKAGED_FILES - names)
    require(not missing, f"runtime image is missing packaged files: {' '.join(missing)}")
    require("etc/ssl/certs/ca-certificates.crt" in names, "CA certificate bundle is missing")
    require(
        not any(re.search(r"(^|/)(sh|bash|ash|dash|zsh)$", name) for name in names),
        "runtime image unexpectedly contains a shell",
    )
    excluded = re.compile(r"(^|/)(\.tools|scripts|testdata)(/|$)|(^|/).*_test(\.go)?$")
    require(
        not any(excluded.search(name) for name in names),
        "runtime image contains excluded development or test content",
    )


def check_packaged_command(
    image: str,
    entrypoint: str,
    arguments: list[str],
    expected: str,
    *,
    docker_options: list[str] | None = None,
) -> None:
    with tempfile.NamedTemporaryFile(prefix="pricing-command-", suffix=".log") as stream:
        log = Path(stream.name)
        run(
            [
                "docker",
                "run",
                "--rm",
                *(docker_options or []),
                "--entrypoint",
                entrypoint,
                image,
                *arguments,
            ],
            cwd=MODULE_ROOT,
            env=os.environ,
            timeout_seconds=60,
            activity=f"run packaged {entrypoint.removeprefix('/')} command",
            log_path=log,
            expected_failure=True,
        )
        require(expected in log.read_text(encoding="utf-8"), f"{entrypoint} diagnostic changed")


def main(_argv: Sequence[str] | None = None) -> int:
    image = os.environ.get("IMAGE_REF", "")
    container = ""
    try:
        require(bool(image), "IMAGE_REF is required")
        inspected = json.loads(captured(["docker", "image", "inspect", image], "inspect image"))
        require(
            isinstance(inspected, list) and len(inspected) == 1,
            "image inspect returned an unexpected document",
        )
        config = inspected[0].get("Config", {})
        require(
            config.get("Entrypoint") == ["/pricing-service"] and not (config.get("Cmd") or []),
            "image entrypoint changed",
        )
        with tempfile.TemporaryDirectory(prefix="insurance-hub-pricing-image-") as directory:
            temporary = Path(directory)
            container = captured(["docker", "create", image], "create inspection container")
            rootfs = temporary / "rootfs.tar"
            run(
                ["docker", "export", "--output", str(rootfs), container],
                cwd=MODULE_ROOT,
                env=os.environ,
                timeout_seconds=120,
                activity="export Pricing image filesystem",
            )
            with tarfile.open(rootfs) as archive:
                names = {member.name.removeprefix("./") for member in archive.getmembers()}
            validate_image_contents(names)
            captured(["docker", "rm", container], "remove inspection container")
            container = ""
            check_packaged_command(
                image,
                "/pricing-migrate",
                ["--operation=status"],
                '"msg":"pricing administration failed"',
            )
            check_packaged_command(
                image,
                "/pricing-seed",
                [],
                '"msg":"pricing administration failed"',
            )
            check_packaged_command(
                image,
                "/pricing-service",
                [],
                '"msg":"configuration failed"',
                docker_options=["--env", "SERVICE_NAME=invalid"],
            )
    except (
        ImageCheckError,
        CommandError,
        OSError,
        json.JSONDecodeError,
        tarfile.TarError,
    ) as error:
        print(f"pricing-image-check: {error}", file=sys.stderr)
        return 1
    finally:
        if container:
            try:
                captured(["docker", "rm", "--force", container], "cleanup inspection container")
            except CommandError, OSError:
                print("pricing-image-check: inspection container cleanup failed", file=sys.stderr)
    print(f"pricing-image-check: image={image} contents=passed commands=passed isolation=passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="pricing-image-check"))
