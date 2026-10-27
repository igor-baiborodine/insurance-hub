#!/usr/bin/env python3
"""Validate Product image identity, packaged commands/assets, startup, reads, and shutdown."""

from __future__ import annotations

import json
import os
import re
import sys
import tarfile
import tempfile
from collections.abc import Sequence
from pathlib import Path

from integration_test import passed_tests
from product_process import ProductCommandError, run, run_cli

MODULE_ROOT = Path(__file__).resolve().parent.parent
MIGRATION_ASSET = "usr/share/insurance-hub/product/migrations/00001_product.sql"
CATALOG_ASSET = "usr/share/insurance-hub/product/catalogs/base.json"
PACKAGED_FILES = {
    "product-service",
    "product-migrate",
    "product-seed",
    MIGRATION_ASSET,
    CATALOG_ASSET,
}
EXPECTED_ASSETS = {
    MIGRATION_ASSET: MODULE_ROOT / "internal/administration/migrations/00001_product.sql",
    CATALOG_ASSET: MODULE_ROOT / "internal/administration/seeds/base.json",
}


ADMINISTRATION_CASES = tuple(
    f"TestProductAdministrationImage/{operation}/{case}"
    for operation in ("migrate", "seed")
    for case in (
        "signal",
        "lock_timeout",
        "partial_start_cleanup",
        "connection_failure",
        "lost_commit_acknowledgement",
    )
) + ("TestProductAdministrationImage/seed_mid_batch_rollback",)


def validate_image_tests(log: Path) -> None:
    required = {"TestProductRuntimeImage", "TestProductAdministrationImage", *ADMINISTRATION_CASES}
    missing = sorted(required - passed_tests(log))
    require(not missing, f"image qualification did not pass required cases: {' '.join(missing)}")


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


def validate_image_contents(names: set[str]) -> None:
    """Reject incomplete packages and source/development leakage."""
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
    unexpected_sql = sorted(
        name for name in names if name.endswith(".sql") and name != MIGRATION_ASSET
    )
    require(
        not unexpected_sql,
        f"runtime image contains unexpected SQL content: {' '.join(unexpected_sql)}",
    )


def validate_image_assets(contents: dict[str, bytes]) -> None:
    """Require packaged administration assets to match their reviewed sources exactly."""
    for packaged, source in EXPECTED_ASSETS.items():
        require(source.is_file(), f"reviewed administration asset is missing: {source}")
        require(
            contents.get(packaged) == source.read_bytes(),
            f"packaged administration asset differs from source: {packaged}",
        )


def check_packaged_command(
    image: str, entrypoint: str, arguments: list[str], expected: str
) -> None:
    with tempfile.NamedTemporaryFile(prefix="product-command-", suffix=".log") as stream:
        log = Path(stream.name)
        run(
            ["docker", "run", "--rm", "--entrypoint", entrypoint, image, *arguments],
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
                members = {
                    member.name.removeprefix("./"): member for member in archive.getmembers()
                }
                names = set(members)
                assets: dict[str, bytes] = {}
                for path in EXPECTED_ASSETS:
                    stream = archive.extractfile(members[path]) if path in members else None
                    if stream is not None:
                        assets[path] = stream.read()
            validate_image_contents(names)
            validate_image_assets(assets)
            captured(["docker", "rm", container], "remove Product image inspection container")
            container = ""
            check_packaged_command(
                image,
                "/product-migrate",
                ["--operation=status"],
                '"msg":"administration failed"',
            )
            check_packaged_command(
                image,
                "/product-seed",
                [f"--catalog-file=/{CATALOG_ASSET}"],
                '"msg":"administration failed"',
            )
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
            try:
                run(
                    [
                        "bash",
                        str(MODULE_ROOT / "scripts/stream-test-log.sh"),
                        str(temporary / "image-tests.json"),
                        "go",
                        "test",
                        "-mod=readonly",
                        "-count=1",
                        "-json",
                        "-race",
                        "-tags=image",
                        "-run",
                        "^(TestProductRuntimeImage|TestProductAdministrationImage)$",
                        "-timeout=7m",
                        "./internal/testing",
                    ],
                    cwd=MODULE_ROOT,
                    env=environment,
                    timeout_seconds=480,
                    teardown_seconds=15,
                    activity="validate Product runtime image",
                )
            finally:
                result = temporary / "image-tests.json"
                if result.is_file():
                    sys.stdout.write(result.read_text(encoding="utf-8"))
            validate_image_tests(temporary / "image-tests.json")
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
        f"product-image-check: image={image} contents=passed administration=passed "
        "startup=passed runtime=passed signal=passed"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="product-image-check"))
