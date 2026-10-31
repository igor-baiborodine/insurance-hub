#!/usr/bin/env python3
"""Run the disposable policy-search Elasticsearch foundation integration suite."""

from __future__ import annotations

import json
import os
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

from scripts.python.process_runner import CommandError, run, run_cli

MODULE_ROOT = Path(__file__).resolve().parent.parent


class IntegrationError(ValueError):
    """The disposable Elasticsearch integration contract was not satisfied."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise IntegrationError(message)


def wait_for_elasticsearch(url: str, deadline_seconds: float = 120) -> None:
    deadline = time.monotonic() + deadline_seconds
    last_status = "no response"
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(
                f"{url}/_cluster/health?wait_for_status=yellow&timeout=1s", timeout=3
            ) as response:
                if response.status == 200:
                    return
                last_status = f"HTTP {response.status}"
        except OSError as error:
            last_status = type(error).__name__
        time.sleep(0.25)
    raise IntegrationError(f"Elasticsearch readiness exceeded {deadline_seconds:g}s: {last_status}")


def passed_tests(path: Path) -> set[str]:
    passed: set[str] = set()
    for line in path.read_text(encoding="utf-8").splitlines():
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
    suite = os.environ.get("INTEGRATION_SUITE", "")
    coverage = os.environ.get("INTEGRATION_COVERAGE_FILE", "")
    name = f"policy-search-step4-{os.getpid()}"
    environment = dict(os.environ)
    status = 0
    try:
        require(docker, "DOCKER is required")
        require(go, "GO is required")
        require(image, "ELASTICSEARCH_IMAGE is required")
        require(suite == "foundation", "INTEGRATION_SUITE must be foundation")
        require(coverage, "INTEGRATION_COVERAGE_FILE is required")
        require(
            "POLICY_SEARCH_TEST_ELASTICSEARCH_URL" not in os.environ,
            "POLICY_SEARCH_TEST_ELASTICSEARCH_URL must be unset; the runner owns Elasticsearch",
        )
        run(
            [docker, "info"],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=30,
            activity="check Docker daemon",
            capture=True,
        )
        with tempfile.TemporaryDirectory(prefix="policy-search-integration-") as directory:
            temporary = Path(directory)
            result = temporary / "go-test.json"
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
                    "xpack.security.enabled=false",
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
            test_environment = {
                **environment,
                "GOWORK": "off",
                "POLICY_SEARCH_TEST_ELASTICSEARCH_URL": url,
            }
            run(
                [
                    go,
                    "test",
                    "-mod=readonly",
                    "-count=1",
                    "-race",
                    "-tags=integration",
                    "-covermode=atomic",
                    "-coverpkg=./...",
                    f"-coverprofile={coverage}",
                    "-json",
                    "-run",
                    "^TestElasticsearchFoundation_",
                    "./internal/testing",
                ],
                cwd=MODULE_ROOT,
                env=test_environment,
                timeout_seconds=600,
                teardown_seconds=20,
                activity="run policy-search Elasticsearch integration",
                log_path=result,
            )
            sys.stdout.write(result.read_text(encoding="utf-8"))
            discovered = passed_tests(result)
            require(
                "TestElasticsearchFoundation_IndexesAndReadsSyntheticDocument" in discovered,
                f"foundation suite selected zero expected tests; passed={sorted(discovered)}",
            )
            require(coverage_path.is_file(), "integration coverage profile was not produced")
    except (IntegrationError, CommandError, OSError) as error:
        print(f"test-integration: {error}", file=sys.stderr)
        status = 1
    finally:
        try:
            run(
                [docker or "docker", "rm", "--force", name],
                cwd=MODULE_ROOT,
                env=environment,
                timeout_seconds=60,
                activity="remove disposable Elasticsearch",
                capture=True,
            )
        except (CommandError, OSError) as error:
            print(f"test-integration: Elasticsearch cleanup failed: {error}", file=sys.stderr)
            status = 1
    if status:
        return status
    print("test-integration: suite=foundation selected=1 cleanup=passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="test-integration"))
