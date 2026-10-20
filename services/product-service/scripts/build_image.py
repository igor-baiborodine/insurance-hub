#!/usr/bin/env python3
"""Build the Product image after validating source and image identity."""

from __future__ import annotations

import os
import re
import sys
from collections.abc import Sequence
from pathlib import Path

from product_process import ProductCommandError, run, run_cli

MODULE_ROOT = Path(__file__).resolve().parent.parent
REPOSITORY_ROOT = MODULE_ROOT.parents[1]
REQUIRED = (
    "IMAGE_NAME",
    "IMAGE_TAG",
    "IMAGE_REF",
    "SOURCE_CREATED",
    "SOURCE_REPOSITORY",
    "SOURCE_REVISION",
    "SOURCE_VERSION",
    "TARGET_PLATFORM",
)


class ImageBuildError(ValueError):
    """Product image inputs do not match the checked-out source."""


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ImageBuildError(message)


def command(arguments: list[str], activity: str) -> str:
    return run(
        arguments,
        cwd=REPOSITORY_ROOT,
        env=os.environ,
        timeout_seconds=30,
        teardown_seconds=5,
        activity=activity,
        capture=True,
    ).strip()


def main(_argv: Sequence[str] | None = None) -> int:
    try:
        values = {name: os.environ.get(name, "") for name in REQUIRED}
        for name, value in values.items():
            require(bool(value), f"{name} is required")
        revision = values["SOURCE_REVISION"]
        require(
            bool(re.fullmatch(r"[0-9a-f]{40}", revision)),
            "SOURCE_REVISION must be 40 lowercase hexadecimal characters",
        )
        head = command(["git", "rev-parse", "--verify", "HEAD"], "resolve Product source revision")
        require(revision == head, "SOURCE_REVISION must match the checked-out HEAD")
        created = command(
            ["git", "show", "-s", "--format=%cI", head], "resolve Product source time"
        )
        require(
            values["SOURCE_CREATED"] == created,
            "SOURCE_CREATED must match the checked-out commit time",
        )
        version = f"git-{revision[:12]}"
        require(values["SOURCE_VERSION"] == version, f"SOURCE_VERSION must be {version}")
        require(values["IMAGE_TAG"] == version, f"IMAGE_TAG must be {version}")
        require(
            values["IMAGE_NAME"] == "insurance-hub-product-api",
            "IMAGE_NAME must be insurance-hub-product-api",
        )
        require(
            values["IMAGE_REF"] == f"{values['IMAGE_NAME']}:{values['IMAGE_TAG']}",
            "IMAGE_REF must be derived from IMAGE_NAME and IMAGE_TAG",
        )
        require(values["TARGET_PLATFORM"] == "linux/amd64", "TARGET_PLATFORM must be linux/amd64")
        run(
            [
                "docker",
                "buildx",
                "build",
                "--build-arg",
                f"SOURCE_CREATED={values['SOURCE_CREATED']}",
                "--build-arg",
                f"SOURCE_REPOSITORY={values['SOURCE_REPOSITORY']}",
                "--build-arg",
                f"SOURCE_REVISION={revision}",
                "--build-arg",
                f"SOURCE_VERSION={version}",
                "--load",
                "--platform",
                values["TARGET_PLATFORM"],
                "--provenance=false",
                "--tag",
                values["IMAGE_REF"],
                "--file",
                str(MODULE_ROOT / "Dockerfile"),
                str(MODULE_ROOT),
            ],
            cwd=REPOSITORY_ROOT,
            env=os.environ,
            timeout_seconds=1_200,
            teardown_seconds=20,
            activity="build Product image",
        )
        image_id = command(
            ["docker", "image", "inspect", "--format", "{{.Id}}", values["IMAGE_REF"]],
            "inspect Product image identity",
        )
        require(image_id.startswith("sha256:"), "built image has no content-addressed image ID")
    except (ImageBuildError, ProductCommandError, OSError) as error:
        print(f"product-image-build: {error}", file=sys.stderr)
        return 1
    print(
        f"product-image-build: image={values['IMAGE_REF']} image-id={image_id} "
        f"platform={values['TARGET_PLATFORM']} revision={revision}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="product-image-build"))
