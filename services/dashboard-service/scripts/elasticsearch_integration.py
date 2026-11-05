#!/usr/bin/env python3
"""Run total-sales tests against one owned disposable Elasticsearch node."""

from __future__ import annotations

import base64
import json
import os
import sys
import time
import urllib.request
from pathlib import Path

from scripts.go.service.tooling_process import run, run_cli
from scripts.python.process_runner import CommandError
from service_process import child_environment

MODULE_ROOT = Path(__file__).resolve().parent.parent
TEST_USERNAME = "elastic"
TEST_PASSWORD = "issue-287-disposable-password"


class IntegrationError(ValueError):
    """The disposable Elasticsearch integration contract was not satisfied."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise IntegrationError(message)


def wait_for_elasticsearch(url: str, deadline_seconds: float = 120) -> None:
    deadline = time.monotonic() + deadline_seconds
    last_status = "no response"
    authorization = base64.b64encode(f"{TEST_USERNAME}:{TEST_PASSWORD}".encode()).decode()
    while time.monotonic() < deadline:
        try:
            request = urllib.request.Request(
                f"{url}/_cluster/health?wait_for_status=yellow&timeout=1s",
                headers={"Authorization": f"Basic {authorization}"},
            )
            with urllib.request.urlopen(request, timeout=3) as response:
                if response.status == 200:
                    return
                last_status = f"HTTP {response.status}"
        except OSError as error:
            last_status = type(error).__name__
        time.sleep(0.25)
    raise IntegrationError(f"Elasticsearch readiness exceeded {deadline_seconds:g}s: {last_status}")


def passed_tests(output: str) -> set[str]:
    passed: set[str] = set()
    for line in output.splitlines():
        try:
            record = json.loads(line)
        except json.JSONDecodeError:
            continue
        if record.get("Action") == "pass" and isinstance(record.get("Test"), str):
            passed.add(record["Test"])
    return passed


def main() -> int:
    docker = os.environ.get("DOCKER", "")
    go = os.environ.get("GO", "")
    image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    coverage = os.environ.get("ELASTICSEARCH_INTEGRATION_COVERAGE_FILE", "")
    name = f"dashboard-elasticsearch-{os.getpid()}"
    container_owned = False
    status = 0
    environment: dict[str, str] = {}
    try:
        require(docker, "DOCKER is required")
        require(go, "GO is required")
        require(image, "ELASTICSEARCH_IMAGE is required")
        require(coverage, "ELASTICSEARCH_INTEGRATION_COVERAGE_FILE is required")
        require(
            "DASHBOARD_TEST_ELASTICSEARCH_URL" not in os.environ,
            "DASHBOARD_TEST_ELASTICSEARCH_URL must be unset; the runner owns Elasticsearch",
        )
        environment = child_environment(
            {"ELASTIC_PASSWORD": TEST_PASSWORD}, preferred_executables=(docker, go)
        )
        run(
            [docker, "info"],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=30,
            activity="check Docker daemon",
            capture=True,
        )
        container_owned = True
        run(
            [
                docker,
                "run",
                "--detach",
                "--name",
                name,
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
                image,
            ],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=180,
            activity="start disposable Elasticsearch",
            capture=True,
        )
        port = run(
            [docker, "port", name, "9200/tcp"],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=15,
            activity="resolve disposable Elasticsearch port",
            capture=True,
        ).strip()
        require(port.startswith("127.0.0.1:"), "unexpected Elasticsearch port binding")
        url = f"http://{port}"
        wait_for_elasticsearch(url)
        coverage_path = MODULE_ROOT / coverage
        coverage_path.unlink(missing_ok=True)
        output = run(
            [
                go,
                "test",
                "-p=1",
                "-mod=readonly",
                "-count=1",
                "-race",
                "-tags=integration",
                "-covermode=atomic",
                "-coverpkg=./internal/elasticsearch,./internal/application,./internal/domain",
                f"-coverprofile={coverage}",
                "-json",
                "-run",
                "^TestElasticsearch(Reader_AcceptedAggregationMatrix|Readiness_RejectsIncompatibleState)$",
                "./internal/elasticsearch",
            ],
            cwd=MODULE_ROOT,
            env={
                **environment,
                "GOWORK": "off",
                "DASHBOARD_TEST_ELASTICSEARCH_URL": url,
                "DASHBOARD_TEST_ELASTICSEARCH_USERNAME": TEST_USERNAME,
                "DASHBOARD_TEST_ELASTICSEARCH_PASSWORD": TEST_PASSWORD,
            },
            timeout_seconds=600,
            teardown_seconds=20,
            activity="run Dashboard Elasticsearch integration",
            capture=True,
            reveal_failure_output=True,
        )
        sys.stdout.write(output)
        discovered = passed_tests(output)
        require(
            "TestElasticsearchReader_AcceptedAggregationMatrix" in discovered,
            f"query suite missed expected test; passed={sorted(discovered)}",
        )
        require(
            "TestElasticsearchReadiness_RejectsIncompatibleState" in discovered,
            f"readiness suite missed expected test; passed={sorted(discovered)}",
        )
        require(coverage_path.is_file(), "integration coverage profile was not produced")
    except (IntegrationError, CommandError, OSError) as error:
        print(f"test-elasticsearch-integration: {error}", file=sys.stderr)
        status = 1
    finally:
        if container_owned:
            try:
                run(
                    [docker, "rm", "--force", name],
                    cwd=MODULE_ROOT,
                    env=environment,
                    timeout_seconds=60,
                    activity="remove disposable Elasticsearch",
                    capture=True,
                )
            except (CommandError, OSError) as error:
                print(f"test-elasticsearch-integration: cleanup failed: {error}", file=sys.stderr)
                status = 1
    if status:
        return status
    print("test-elasticsearch-integration: selected=2 auth=passed cleanup=passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="test-elasticsearch-integration"))
