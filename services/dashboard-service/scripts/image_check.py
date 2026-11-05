#!/usr/bin/env python3
"""Validate the Dashboard image Echo, health, identity and bounded shutdown."""

from __future__ import annotations

import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

REPOSITORY_ROOT = Path(
    os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])
).resolve()
sys.path.insert(0, str(REPOSITORY_ROOT))

from scripts.python.process_runner import CommandError, run_command  # noqa: E402
from service_process import child_environment  # noqa: E402

MODULE_ROOT = Path(__file__).resolve().parent.parent


class ImageCheckError(ValueError):
    """The Dashboard runtime image violates its reviewed contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise ImageCheckError(message)


def command(arguments: list[str], activity: str, timeout: float = 30) -> str:
    result = run_command(
        arguments,
        cwd=MODULE_ROOT,
        env=child_environment(),
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
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    try:
        with urllib.request.urlopen(
            urllib.request.Request(
                url, data=body, headers=headers, method="POST" if body else "GET"
            ),
            timeout=2,
        ) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def wait_status(url: str, expected: tuple[int, bytes], seconds: int = 30) -> None:
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            if request(url) == expected:
                return
        except OSError:
            pass
        time.sleep(0.2)
    raise ImageCheckError(f"{url} did not reach the expected response within {seconds} seconds")


def main() -> int:
    docker = os.environ.get("DOCKER", "docker")
    image = os.environ.get("IMAGE_REF", "")
    version = os.environ.get("SOURCE_VERSION", "")
    container = ""
    failed = False
    try:
        require(bool(image), "IMAGE_REF is required")
        require(bool(re.fullmatch(r"git-[0-9a-f]{12}", version)), "SOURCE_VERSION is invalid")
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
                "--env",
                "SERVICE_ENV=local-dev",
                "--env",
                f"SERVICE_VERSION={version}",
                "--env",
                "OTEL_ENABLED=false",
                image,
            ],
            "start Dashboard runtime image",
            60,
        )
        http_port = mapped_port(docker, container, 8080)
        management_port = mapped_port(docker, container, 8081)
        wait_status(f"http://127.0.0.1:{management_port}/readyz", (200, b"ready\n"))
        require(
            request(f"http://127.0.0.1:{management_port}/livez") == (200, b"live\n"),
            "liveness differs",
        )
        status, body = request(
            f"http://127.0.0.1:{http_port}/v1/example/echo",
            json.dumps({"message": "dashboard-image-echo"}).encode(),
        )
        require(
            status == 200 and json.loads(body) == {"message": "dashboard-image-echo"},
            "image Echo differs",
        )
        started = time.monotonic()
        command([docker, "stop", "--time", "10", container], "stop Dashboard image", 20)
        require(time.monotonic() - started <= 15, "image shutdown exceeded the owned bound")
        exit_code = command(
            [docker, "inspect", "--format", "{{.State.ExitCode}}", container],
            "inspect Dashboard image exit",
        )
        require(exit_code == "0", "image exited unsuccessfully")
        logs = command([docker, "logs", container], "read Dashboard image logs")
        require("configuration failed" not in logs, "image reported configuration failure")
    except (ImageCheckError, CommandError, OSError, ValueError, json.JSONDecodeError) as error:
        print(f"dashboard-image-check: {error}", file=sys.stderr)
        failed = True
    finally:
        if container:
            try:
                command([docker, "rm", "--force", container], "remove Dashboard image container")
            except CommandError, OSError:
                if not failed:
                    raise
                print("dashboard-image-check: container cleanup failed", file=sys.stderr)
    if failed:
        return 1
    print("dashboard-image-check: Echo, health, identity and shutdown passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
