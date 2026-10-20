#!/usr/bin/env python3
"""Validate Product runtime image identity, contents, startup, reads, and shutdown."""

from __future__ import annotations

import json
import os
import re
import sys
import tarfile
import tempfile
from collections.abc import Sequence
from pathlib import Path

from product_process import ProductCommandError, run, run_cli

MODULE_ROOT = Path(__file__).resolve().parent.parent


class ImageCheckError(ValueError):
    """The Product runtime image violates its contract."""


def require(condition: bool, message: str) -> None:
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


def main(_argv: Sequence[str] | None = None) -> int:
    image = os.environ.get("IMAGE_REF", "")
    container = ""
    try:
        required = {
            name: os.environ.get(name, "")
            for name in ("SOURCE_CREATED", "SOURCE_REPOSITORY", "SOURCE_REVISION", "SOURCE_VERSION")
        }
        require(bool(image), "IMAGE_REF is required")
        for name, value in required.items():
            require(bool(value), f"{name} is required")
        inspected = json.loads(
            captured(["docker", "image", "inspect", image], "inspect Product image")
        )
        require(
            isinstance(inspected, list) and len(inspected) == 1,
            "image inspect returned an unexpected document",
        )
        record = inspected[0]
        config = record.get("Config", {})
        labels = config.get("Labels", {})
        require(
            record.get("Os") == "linux" and record.get("Architecture") == "amd64",
            "image platform is not linux/amd64",
        )
        require(config.get("User") == "65532:65532", "runtime user is not 65532:65532")
        require(
            config.get("Entrypoint") == ["/product-service"] and not (config.get("Cmd") or []),
            "image entrypoint changed",
        )
        expected_labels = {
            "org.opencontainers.image.created": required["SOURCE_CREATED"],
            "org.opencontainers.image.source": required["SOURCE_REPOSITORY"],
            "org.opencontainers.image.revision": required["SOURCE_REVISION"],
            "org.opencontainers.image.title": "Insurance Hub Product API",
            "org.opencontainers.image.version": required["SOURCE_VERSION"],
        }
        require(
            all(labels.get(key) == value for key, value in expected_labels.items()),
            "image OCI identity does not match the Product contract",
        )
        with tempfile.TemporaryDirectory(prefix="insurance-hub-product-image-") as directory:
            temporary = Path(directory)
            container = captured(
                ["docker", "create", image], "create Product image inspection container"
            )
            rootfs = temporary / "rootfs.tar"
            run(
                ["docker", "export", "--output", str(rootfs), container],
                cwd=MODULE_ROOT,
                env=os.environ,
                timeout_seconds=120,
                activity="export Product image filesystem",
            )
            with tarfile.open(rootfs) as archive:
                names = {name.removeprefix("./") for name in archive.getnames()}
            require("product-service" in names, "runtime binary is missing")
            require(
                "etc/ssl/certs/ca-certificates.crt" in names, "CA certificate bundle is missing"
            )
            require(
                not any(re.search(r"(^|/)(sh|bash|ash|dash|zsh)$", name) for name in names),
                "runtime image unexpectedly contains a shell",
            )
            excluded = re.compile(
                r"(^|/)(\.tools|scripts|testdata)(/|$)|(^|/).*_test(\.go)?$|\.sql$"
            )
            require(
                not any(excluded.search(name) for name in names),
                "runtime image contains excluded development, test, or SQL content",
            )
            captured(["docker", "rm", container], "remove Product image inspection container")
            container = ""
            missing_log = temporary / "missing-config.log"
            run(
                ["docker", "run", "--rm", image],
                cwd=MODULE_ROOT,
                env=os.environ,
                timeout_seconds=60,
                activity="check Product missing-configuration failure",
                log_path=missing_log,
                expected_failure=True,
            )
            diagnostic = missing_log.read_text(encoding="utf-8")
            require(
                '"msg":"configuration failed"' in diagnostic,
                "missing configuration produced no bounded startup diagnostic",
            )
            require(
                '"error":"[REDACTED]"' in diagnostic,
                "missing configuration diagnostic was not redacted",
            )
            environment = dict(os.environ, PRODUCT_TEST_IMAGE=image, GOWORK="off")
            run(
                [
                    "go",
                    "test",
                    "-mod=readonly",
                    "-count=1",
                    "-tags=image",
                    "-run",
                    "^TestProductRuntimeImage$",
                    "-timeout=4m",
                    "./internal/testing",
                ],
                cwd=MODULE_ROOT,
                env=environment,
                timeout_seconds=300,
                teardown_seconds=15,
                activity="validate Product runtime image",
            )
    except (
        ImageCheckError,
        ProductCommandError,
        OSError,
        json.JSONDecodeError,
        tarfile.TarError,
    ) as error:
        print(f"product-image-check: {error}", file=sys.stderr)
        return 1
    finally:
        if container:
            try:
                captured(["docker", "rm", "-f", container], "cleanup Product inspection container")
            except ProductCommandError, OSError:
                print("product-image-check: inspection container cleanup failed", file=sys.stderr)
    print(
        f"product-image-check: image={image} contents=passed startup=passed runtime=passed signal=passed"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="product-image-check"))
