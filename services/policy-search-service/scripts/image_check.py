#!/usr/bin/env python3
"""Validate the Policy Search runtime image and its owned Echo lifecycle."""

from __future__ import annotations

import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPOSITORY_ROOT))

from scripts.python.process_runner import CommandError, run_command  # noqa: E402

from scaffold_process import child_environment  # noqa: E402

MODULE_ROOT = Path(__file__).resolve().parent.parent


class ImageCheckError(ValueError):
    """The runtime image violates its reviewed contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise ImageCheckError(message)


def environment() -> dict[str, str]:
    return child_environment(
        {
            name: os.environ.get(name, "")
            for name in (
                "IMAGE_REF",
                "SOURCE_CREATED",
                "SOURCE_REPOSITORY",
                "SOURCE_REVISION",
                "SOURCE_VERSION",
                "TARGET_PLATFORM",
            )
        }
    )


def command(arguments: list[str], activity: str, timeout: float = 30) -> str:
    result = run_command(
        arguments,
        cwd=MODULE_ROOT,
        env=environment(),
        timeout_seconds=timeout,
        teardown_seconds=10,
        activity=activity,
    )
    return result.stdout.strip()


def mapped_port(docker: str, container: str, port: int) -> int:
    value = command([docker, "port", container, f"{port}/tcp"], "resolve image port")
    require(bool(re.fullmatch(r"127\.0\.0\.1:[0-9]+", value)), "unexpected port binding")
    return int(value.rsplit(":", 1)[1])


def request(url: str, body: bytes | None = None) -> tuple[int, bytes]:
    headers = {"Content-Type": "application/json"} if body is not None else {}
    method = "POST" if body is not None else "GET"
    try:
        with urllib.request.urlopen(
            urllib.request.Request(url, data=body, headers=headers, method=method), timeout=2
        ) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def wait_ready(url: str) -> None:
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        try:
            if request(url) == (200, b"ready\n"):
                return
        except OSError:
            pass
        time.sleep(0.2)
    raise ImageCheckError("runtime image did not become ready within 30 seconds")


def main() -> int:
    docker = os.environ.get("DOCKER", "docker")
    image = os.environ.get("IMAGE_REF", "")
    container = ""
    try:
        required = {
            name: os.environ.get(name, "")
            for name in (
                "SOURCE_CREATED",
                "SOURCE_REPOSITORY",
                "SOURCE_REVISION",
                "SOURCE_VERSION",
            )
        }
        require(bool(image), "IMAGE_REF is required")
        require(all(required.values()), "source identity is incomplete")
        inspected = json.loads(command([docker, "image", "inspect", image], "inspect image"))
        require(isinstance(inspected, list) and len(inspected) == 1, "image inspect differs")
        record = inspected[0]
        config = record.get("Config", {})
        labels = config.get("Labels", {})
        require(
            record.get("Os") == "linux" and record.get("Architecture") == "amd64",
            "image platform differs",
        )
        require(config.get("User") == "65532:65532", "runtime user differs")
        require(config.get("Entrypoint") == ["/policy-search-service"], "entrypoint differs")
        expected = {
            "org.opencontainers.image.created": required["SOURCE_CREATED"],
            "org.opencontainers.image.source": required["SOURCE_REPOSITORY"],
            "org.opencontainers.image.revision": required["SOURCE_REVISION"],
            "org.opencontainers.image.title": "Insurance Hub Policy Search API",
            "org.opencontainers.image.version": required["SOURCE_VERSION"],
        }
        require(
            all(labels.get(key) == value for key, value in expected.items()), "OCI identity differs"
        )
        container = command(
            [
                docker,
                "run",
                "--detach",
                "--publish",
                "127.0.0.1::8080",
                "--publish",
                "127.0.0.1::8081",
                "--env",
                "HTTP_ADDR=0.0.0.0:8080",
                "--env",
                "GRPC_ADDR=0.0.0.0:9090",
                "--env",
                "HEALTH_ADDR=0.0.0.0:8081",
                image,
            ],
            "start runtime image",
            60,
        )
        http_port = mapped_port(docker, container, 8080)
        management_port = mapped_port(docker, container, 8081)
        wait_ready(f"http://127.0.0.1:{management_port}/readyz")
        require(
            request(f"http://127.0.0.1:{management_port}/livez") == (200, b"live\n"),
            "liveness differs",
        )
        require(
            request(
                f"http://127.0.0.1:{http_port}/v1/example/echo",
                b'{"message":"hello"}',
            )
            == (200, b'{"message":"hello"}'),
            "image Echo differs",
        )
        require(
            request(f"http://127.0.0.1:{http_port}/v1/example/echo", b'{"message":""}')[0] == 400,
            "image invalid Echo was accepted",
        )
        started = time.monotonic()
        command([docker, "stop", "--time", "10", container], "stop runtime image", 20)
        require(time.monotonic() - started <= 15, "image shutdown exceeded the owned bound")
        logs = command([docker, "logs", container], "read runtime logs")
        require("configuration failed" not in logs, "runtime image reported configuration failure")
        container = ""
    except (ImageCheckError, CommandError, OSError, ValueError, json.JSONDecodeError) as error:
        print(f"policy-search-image-check: {error}", file=sys.stderr)
        return 1
    finally:
        if container:
            try:
                command([docker, "rm", "--force", container], "remove runtime image container")
            except CommandError, OSError:
                print("policy-search-image-check: container cleanup failed", file=sys.stderr)
    print(f"policy-search-image-check: image={image} echo=passed shutdown=passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
