#!/usr/bin/env python3
"""Validate Dashboard image sales paths, health, identity and bounded shutdown."""

from __future__ import annotations

import base64
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
ADMIN_USERNAME = "elastic"
ADMIN_PASSWORD = "issue-287-image-admin-password"
READER_USERNAME = "dashboard_image_reader"
READER_PASSWORD = "issue-287-image-reader-password"


class ImageCheckError(ValueError):
    """The Dashboard runtime image violates its reviewed contract."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise ImageCheckError(message)


def command(
    arguments: list[str],
    activity: str,
    timeout: float = 30,
    environment: dict[str, str] | None = None,
) -> str:
    result = run_command(
        arguments,
        cwd=MODULE_ROOT,
        env=child_environment(environment),
        timeout_seconds=timeout,
        teardown_seconds=10,
        activity=activity,
    )
    return result.stdout.strip()


def container_logs(docker: str, container: str) -> str:
    result = run_command(
        [docker, "logs", container],
        cwd=MODULE_ROOT,
        env=child_environment(),
        timeout_seconds=30,
        teardown_seconds=10,
        activity="read Dashboard image logs",
    )
    return "\n".join(part for part in (result.stdout, result.stderr) if part).strip()


def mapped_port(docker: str, container: str, port: int) -> int:
    value = command([docker, "port", container, f"{port}/tcp"], "resolve image port")
    require(bool(re.fullmatch(r"127\.0\.0\.1:[0-9]+", value)), "unexpected port binding")
    return int(value.rsplit(":", 1)[1])


def request(
    url: str,
    body: bytes | None = None,
    *,
    method: str | None = None,
    headers: dict[str, str] | None = None,
) -> tuple[int, bytes]:
    request_headers = {"Content-Type": "application/json", "Accept": "application/json"}
    request_headers.update(headers or {})
    try:
        with urllib.request.urlopen(
            urllib.request.Request(
                url,
                data=body,
                headers=request_headers,
                method=method or ("POST" if body else "GET"),
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


def elasticsearch_request(url: str, method: str, path: str, body: dict[str, object]) -> None:
    authorization = base64.b64encode(f"{ADMIN_USERNAME}:{ADMIN_PASSWORD}".encode()).decode()
    status, response = request(
        url + path,
        json.dumps(body, separators=(",", ":")).encode(),
        method=method,
        headers={"Authorization": f"Basic {authorization}"},
    )
    require(
        status in {200, 201},
        f"Elasticsearch setup {path} failed with HTTP {status}: {response[:256]!r}",
    )


def reader_request(
    url: str,
    method: str,
    path: str,
    body: dict[str, object] | None = None,
) -> tuple[int, bytes]:
    authorization = base64.b64encode(f"{READER_USERNAME}:{READER_PASSWORD}".encode()).decode()
    payload = None if body is None else json.dumps(body, separators=(",", ":")).encode()
    return request(
        url + path,
        payload,
        method=method,
        headers={"Authorization": f"Basic {authorization}"},
    )


def wait_for_elasticsearch(url: str, seconds: int = 120) -> None:
    authorization = base64.b64encode(f"{ADMIN_USERNAME}:{ADMIN_PASSWORD}".encode()).decode()
    deadline = time.monotonic() + seconds
    last_status = "no response"
    while time.monotonic() < deadline:
        try:
            status, _ = request(
                url + "/_cluster/health?wait_for_status=yellow&timeout=1s",
                headers={"Authorization": f"Basic {authorization}"},
            )
            if status == 200:
                return
            last_status = f"HTTP {status}"
        except OSError as error:
            last_status = type(error).__name__
        time.sleep(0.25)
    raise ImageCheckError(f"Elasticsearch readiness exceeded {seconds} seconds: {last_status}")


def create_elasticsearch_fixture(url: str) -> None:
    elasticsearch_request(
        url,
        "PUT",
        "/_security/role/dashboard_image_reader",
        {
            "cluster": [],
            "indices": [
                {
                    "names": ["policy_stats"],
                    "privileges": ["read", "view_index_metadata"],
                }
            ],
        },
    )
    elasticsearch_request(
        url,
        "POST",
        "/_security/user/dashboard_image_reader",
        {"password": READER_PASSWORD, "roles": ["dashboard_image_reader"]},
    )
    elasticsearch_request(
        url,
        "PUT",
        "/policy_stats",
        {
            "settings": {"number_of_shards": 1, "number_of_replicas": 0},
            "mappings": {
                "properties": {
                    "productCode": {
                        "type": "text",
                        "fields": {"keyword": {"type": "keyword"}},
                    },
                    "agentLogin": {
                        "type": "text",
                        "fields": {"keyword": {"type": "keyword"}},
                    },
                    "from": {"type": "date"},
                    "totalPremium": {"type": "double"},
                }
            },
        },
    )
    elasticsearch_request(
        url,
        "POST",
        "/policy_stats/_doc/image-fixture?refresh=true",
        {
            "agentLogin": "safe-agent",
            "productCode": "SAFE",
            "from": "2019-06-01",
            "totalPremium": 1000.0,
        },
    )
    elasticsearch_request(
        url,
        "PUT",
        "/unrelated_stats",
        {"settings": {"number_of_shards": 1, "number_of_replicas": 0}},
    )


def verify_reader_permissions(url: str) -> None:
    checks = (
        (
            "write",
            "PUT",
            "/policy_stats/_doc/denied-write",
            {"productCode": "DENIED", "from": "2019-06-01", "totalPremium": 1.0},
        ),
        ("cluster administration", "GET", "/_cluster/settings", None),
        ("unrelated index", "GET", "/unrelated_stats/_search", None),
    )
    failures: list[str] = []
    for capability, method, path, body in checks:
        try:
            status, response = reader_request(url, method, path, body)
            if status != 403:
                failures.append(f"{capability}: HTTP {status}: {response[:256]!r}")
        except OSError as error:
            failures.append(f"{capability}: {type(error).__name__}")
    require(not failures, "Dashboard reader denials failed: " + " | ".join(failures))


def verify_runtime_identity(logs: str, version: str) -> None:
    entries: list[dict[str, object]] = []
    for line in logs.splitlines():
        try:
            entry = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(entry, dict):
            entries.append(entry)
    require(
        any(
            entry.get("msg") == "service started" and entry.get("service_version") == version
            for entry in entries
        ),
        "runtime startup log does not contain the exact source version",
    )


def cleanup(docker: str, arguments: list[str], activity: str) -> bool:
    try:
        command([docker, *arguments], activity, 60)
    except (CommandError, OSError) as error:
        print(f"dashboard-image-check: {activity} failed: {error}", file=sys.stderr)
        return False
    return True


def main() -> int:
    docker = os.environ.get("DOCKER", "docker")
    image = os.environ.get("IMAGE_REF", "")
    version = os.environ.get("SOURCE_VERSION", "")
    revision = os.environ.get("SOURCE_REVISION", "")
    elasticsearch_image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    suffix = str(os.getpid())
    network = f"dashboard-image-network-{suffix}"
    elasticsearch_container = f"dashboard-image-elasticsearch-{suffix}"
    dashboard_container = f"dashboard-image-runtime-{suffix}"
    network_owned = False
    elasticsearch_owned = False
    dashboard_owned = False
    failed = False
    try:
        require(bool(image), "IMAGE_REF is required")
        require(bool(elasticsearch_image), "ELASTICSEARCH_IMAGE is required")
        require(
            bool(re.fullmatch(r"git-(?!0{12})[0-9a-f]{12}", version)),
            "SOURCE_VERSION is invalid",
        )
        require(
            bool(re.fullmatch(r"(?!0{40})[0-9a-f]{40}", revision)),
            "SOURCE_REVISION is invalid",
        )
        require(version == f"git-{revision[:12]}", "SOURCE_VERSION differs from SOURCE_REVISION")
        command([docker, "info"], "check Docker daemon", 30)
        command([docker, "network", "create", network], "create image-check network", 30)
        network_owned = True
        elasticsearch_owned = True
        command(
            [
                docker,
                "run",
                "--detach",
                "--name",
                elasticsearch_container,
                "--network",
                network,
                "--network-alias",
                "elasticsearch",
                "--publish",
                "127.0.0.1::9200",
                "--env",
                "discovery.type=single-node",
                "--env",
                "xpack.security.enabled=true",
                "--env",
                "xpack.security.http.ssl.enabled=false",
                "--env",
                "ELASTIC_PASSWORD",
                "--env",
                "ES_JAVA_OPTS=-Xms512m -Xmx512m",
                elasticsearch_image,
            ],
            "start image-check Elasticsearch",
            180,
            {"ELASTIC_PASSWORD": ADMIN_PASSWORD},
        )
        elasticsearch_port = mapped_port(docker, elasticsearch_container, 9200)
        elasticsearch_url = f"http://127.0.0.1:{elasticsearch_port}"
        wait_for_elasticsearch(elasticsearch_url)
        create_elasticsearch_fixture(elasticsearch_url)
        verify_reader_permissions(elasticsearch_url)
        dashboard_owned = True
        command(
            [
                docker,
                "run",
                "--detach",
                "--name",
                dashboard_container,
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
                "DEPLOYMENT_ENVIRONMENT=local-dev",
                "--env",
                f"SERVICE_VERSION={version}",
                "--env",
                "OTEL_ENABLED=false",
                "--env",
                "ELASTICSEARCH_URL=http://elasticsearch:9200",
                "--env",
                "ELASTICSEARCH_INDEX=policy_stats",
                "--env",
                f"ELASTICSEARCH_USERNAME={READER_USERNAME}",
                "--env",
                "ELASTICSEARCH_PASSWORD",
                "--env",
                "ELASTICSEARCH_TIMEOUT=5s",
                "--env",
                "SALES_TRENDS_TIME_ZONE=GMT",
                image,
            ],
            "start Dashboard runtime image",
            60,
            {"ELASTICSEARCH_PASSWORD": READER_PASSWORD},
        )
        http_port = mapped_port(docker, dashboard_container, 8080)
        management_port = mapped_port(docker, dashboard_container, 8081)
        wait_status(f"http://127.0.0.1:{management_port}/readyz", (200, b"ready\n"))
        require(
            request(f"http://127.0.0.1:{management_port}/livez") == (200, b"live\n"),
            "liveness differs",
        )
        sales_operations = (
            (
                "total-sales",
                "/dashboard/totalsales",
                {"saleDateFrom": "2019-01-01", "saleDateTo": "2020-01-01"},
                {
                    "total": {"policiesCount": 1, "premiumAmount": 1000.0},
                    "perProductTotal": {"SAFE": {"policiesCount": 1, "premiumAmount": 1000.0}},
                },
            ),
            (
                "agent-sales",
                "/dashboard/agentssales",
                {
                    "agentLogin": "safe-agent",
                    "productCode": "SAFE",
                    "saleDateFrom": "2019-01-01",
                    "saleDateTo": "2020-01-01",
                },
                {"perAgentTotal": {"safe-agent": {"policiesCount": 1, "premiumAmount": 1000.0}}},
            ),
        )
        operation_failures: list[str] = []
        for name, path, payload, expected in sales_operations:
            try:
                status, body = request(
                    f"http://127.0.0.1:{http_port}{path}",
                    json.dumps(payload).encode(),
                )
                require(
                    status == 200 and json.loads(body) == expected,
                    f"image {name} response differs",
                )
            except (ImageCheckError, OSError, ValueError, json.JSONDecodeError) as error:
                operation_failures.append(f"{name}: {error}")
        require(
            not operation_failures, "image sales checks failed: " + " | ".join(operation_failures)
        )
        command(
            [docker, "stop", "--time", "10", elasticsearch_container],
            "stop image-check Elasticsearch",
            20,
        )
        wait_status(f"http://127.0.0.1:{management_port}/readyz", (503, b"not ready\n"))
        require(
            request(f"http://127.0.0.1:{management_port}/livez") == (200, b"live\n"),
            "liveness failed after dependency loss",
        )
        dependency_failures: list[str] = []
        safe_failure = b'{"code":14,"message":"service unavailable","details":[]}'
        for name, path, payload, _expected in sales_operations:
            try:
                status, body = request(
                    f"http://127.0.0.1:{http_port}{path}",
                    json.dumps(payload).encode(),
                )
                require(
                    status == 503 and body == safe_failure,
                    f"{name} dependency failure response differs",
                )
            except (ImageCheckError, OSError, ValueError) as error:
                dependency_failures.append(f"{name}: {error}")
        require(
            not dependency_failures,
            "image dependency checks failed: " + " | ".join(dependency_failures),
        )
        started = time.monotonic()
        command(
            [docker, "stop", "--time", "10", dashboard_container],
            "stop Dashboard image",
            20,
        )
        require(time.monotonic() - started <= 15, "image shutdown exceeded the owned bound")
        exit_code = command(
            [docker, "inspect", "--format", "{{.State.ExitCode}}", dashboard_container],
            "inspect Dashboard image exit",
        )
        require(exit_code == "0", "image exited unsuccessfully")
        logs = container_logs(docker, dashboard_container)
        require("configuration failed" not in logs, "image reported configuration failure")
        verify_runtime_identity(logs, version)
    except (ImageCheckError, CommandError, OSError, ValueError, json.JSONDecodeError) as error:
        print(f"dashboard-image-check: {error}", file=sys.stderr)
        failed = True
    finally:
        if dashboard_owned and not cleanup(
            docker,
            ["rm", "--force", dashboard_container],
            "remove Dashboard image container",
        ):
            failed = True
        if elasticsearch_owned and not cleanup(
            docker,
            ["rm", "--force", elasticsearch_container],
            "remove image-check Elasticsearch",
        ):
            failed = True
        if network_owned and not cleanup(
            docker,
            ["network", "rm", network],
            "remove image-check network",
        ):
            failed = True
    if failed:
        return 1
    print("dashboard-image-check: sales, dependency, health, identity and shutdown passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
