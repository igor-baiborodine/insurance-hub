#!/usr/bin/env python3
"""Validate the Policy Search image's search and dependency lifecycle."""

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

from scripts.python.process_runner import CommandError, run_command  # noqa: E402, I001

from scaffold_process import child_environment  # noqa: E402

MODULE_ROOT = Path(__file__).resolve().parent.parent


class ImageCheckError(ValueError):
    """The runtime image violates its reviewed contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise ImageCheckError(message)


def environment() -> dict[str, str]:
    return child_environment({"IMAGE_REF": os.environ.get("IMAGE_REF", "")})


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


def request(url: str, body: bytes | None = None, method: str = "GET") -> tuple[int, bytes]:
    headers = {"Content-Type": "application/json"} if body is not None else {}
    try:
        with urllib.request.urlopen(
            urllib.request.Request(url, data=body, headers=headers, method=method), timeout=2
        ) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def wait_status(url: str, expected: int, description: str, seconds: int = 30) -> None:
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            if request(url)[0] == expected:
                return
        except OSError:
            pass
        time.sleep(0.2)
    raise ImageCheckError(f"{description} did not reach HTTP {expected} within {seconds} seconds")


def create_fixture(endpoint: str) -> None:
    mapping = {
        "mappings": {
            "dynamic": "strict",
            "properties": {
                "number": {"type": "keyword"},
                "policyHolder": {"type": "text"},
                "dateFrom": {"type": "date", "format": "strict_date"},
                "dateTo": {"type": "date", "format": "strict_date"},
            },
        }
    }
    require(
        request(
            endpoint + "/policy-views",
            json.dumps(mapping).encode(),
            "PUT",
        )[0]
        == 200,
        "image fixture index creation failed",
    )
    document = {
        "number": "POLICY-SYNTHETIC-001",
        "policyHolder": "Synthetic Person",
        "dateFrom": "2024-05-01",
        "dateTo": "2025-04-30",
    }
    require(
        request(
            endpoint + "/policy-views/_doc/accepted?refresh=true",
            json.dumps(document).encode(),
            "PUT",
        )[0]
        in {200, 201},
        "image fixture document creation failed",
    )


def main() -> int:
    docker = os.environ.get("DOCKER", "docker")
    image = os.environ.get("IMAGE_REF", "")
    elasticsearch_image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    container = ""
    elasticsearch = ""
    network = ""
    failed = False
    try:
        require(bool(image), "IMAGE_REF is required")
        require(bool(elasticsearch_image), "ELASTICSEARCH_IMAGE is required")
        suffix = f"{os.getpid()}-{time.monotonic_ns()}"
        network = f"policy-search-image-{suffix}"
        elasticsearch_name = f"policy-search-es-{suffix}"
        command([docker, "network", "create", network], "create image-check network")
        elasticsearch = command(
            [
                docker,
                "run",
                "--detach",
                "--name",
                elasticsearch_name,
                "--network",
                network,
                "--publish",
                "127.0.0.1::9200",
                "--env",
                "discovery.type=single-node",
                "--env",
                "xpack.security.enabled=false",
                "--env",
                "xpack.security.http.ssl.enabled=false",
                "--env",
                "ES_JAVA_OPTS=-Xms512m -Xmx512m",
                elasticsearch_image,
            ],
            "start image-check Elasticsearch",
            60,
        )
        elasticsearch_port = mapped_port(docker, elasticsearch, 9200)
        elasticsearch_endpoint = f"http://127.0.0.1:{elasticsearch_port}"
        wait_status(elasticsearch_endpoint, 200, "image-check Elasticsearch")
        create_fixture(elasticsearch_endpoint)
        container = command(
            [
                docker,
                "run",
                "--detach",
                "--network",
                network,
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
                f"ELASTICSEARCH_URL=http://{elasticsearch_name}:9200",
                image,
            ],
            "start runtime image",
            60,
        )
        http_port = mapped_port(docker, container, 8080)
        management_port = mapped_port(docker, container, 8081)
        wait_status(f"http://127.0.0.1:{management_port}/readyz", 200, "runtime readiness")
        require(
            request(f"http://127.0.0.1:{management_port}/livez") == (200, b"live\n"),
            "liveness differs",
        )
        require(
            request(f"http://127.0.0.1:{http_port}/policies?q=Synthetic%20Person")[0] == 200,
            "image search differs",
        )
        require(
            request(f"http://127.0.0.1:{http_port}/policies?q=")[0] == 200,
            "image explicit empty query differs",
        )
        require(
            request(f"http://127.0.0.1:{http_port}/policies")[0] == 400,
            "image absent query was accepted",
        )
        command(
            [docker, "network", "disconnect", network, elasticsearch],
            "disconnect image-check dependency",
        )
        wait_status(
            f"http://127.0.0.1:{management_port}/readyz",
            503,
            "runtime dependency-loss readiness",
        )
        require(
            request(f"http://127.0.0.1:{management_port}/livez") == (200, b"live\n"),
            "liveness failed with dependency loss",
        )
        command(
            [docker, "network", "connect", network, elasticsearch],
            "reconnect image-check dependency",
        )
        wait_status(
            f"http://127.0.0.1:{management_port}/readyz",
            200,
            "runtime recovered readiness",
            seconds=90,
        )
        started = time.monotonic()
        command([docker, "stop", "--time", "10", container], "stop runtime image", 20)
        require(time.monotonic() - started <= 15, "image shutdown exceeded the owned bound")
        logs = command([docker, "logs", container], "read runtime logs")
        require("configuration failed" not in logs, "runtime image reported configuration failure")
    except (ImageCheckError, CommandError, OSError, ValueError) as error:
        print(f"policy-search-image-check: {error}", file=sys.stderr)
        failed = True
    finally:
        if container:
            try:
                command([docker, "rm", "--force", container], "remove runtime image container")
            except CommandError, OSError:
                print("policy-search-image-check: container cleanup failed", file=sys.stderr)
                failed = True
        if elasticsearch:
            try:
                command(
                    [docker, "rm", "--force", elasticsearch],
                    "remove image-check Elasticsearch",
                )
            except CommandError, OSError:
                print("policy-search-image-check: Elasticsearch cleanup failed", file=sys.stderr)
                failed = True
        if network:
            try:
                command([docker, "network", "rm", network], "remove image-check network")
            except CommandError, OSError:
                print("policy-search-image-check: network cleanup failed", file=sys.stderr)
                failed = True
    if failed:
        return 1
    print(
        f"policy-search-image-check: image={image} search=passed "
        "dependency-lifecycle=passed shutdown=passed"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
