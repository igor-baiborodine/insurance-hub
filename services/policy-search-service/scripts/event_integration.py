#!/usr/bin/env python3
"""Run event-fixture tests against owned disposable Kafka and Elasticsearch."""

from __future__ import annotations

import json
import os
import socket
import sys
import time
from pathlib import Path

from elasticsearch_integration import wait_for_elasticsearch
from scripts.go.service.tooling_process import run, run_cli
from scripts.python.process_runner import CommandError

from scaffold_process import child_environment

MODULE_ROOT = Path(__file__).resolve().parent.parent
REGISTERED_TOPIC = "policy-registered-issue-230"
TERMINATED_TOPIC = "policy-terminated-issue-230"
CONSUMER_GROUP = "policy-search-service-issue-230"
OWNERSHIP = "issue-230-disposable"


class IntegrationError(ValueError):
    """The disposable event-integration contract was not satisfied."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise IntegrationError(message)


def loopback_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def wait_for_kafka_port(port: int, deadline_seconds: float = 120) -> None:
    deadline = time.monotonic() + deadline_seconds
    while time.monotonic() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=1):
                return
        except OSError:
            time.sleep(0.25)
    raise IntegrationError(f"Kafka readiness exceeded {deadline_seconds:g}s")


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
    elasticsearch_image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    kafka_image = os.environ.get("KAFKA_IMAGE", "")
    coverage = os.environ.get("EVENT_INTEGRATION_COVERAGE_FILE", "")
    elasticsearch_name = f"policy-search-event-elasticsearch-{os.getpid()}"
    kafka_name = f"policy-search-event-kafka-{os.getpid()}"
    owned_containers: list[str] = []
    status = 0
    environment: dict[str, str] = {}
    try:
        require(docker, "DOCKER is required")
        require(go, "GO is required")
        require(elasticsearch_image, "ELASTICSEARCH_IMAGE is required")
        require(kafka_image, "KAFKA_IMAGE is required")
        require(coverage, "EVENT_INTEGRATION_COVERAGE_FILE is required")
        for name in (
            "POLICY_SEARCH_TEST_ELASTICSEARCH_URL",
            "POLICY_SEARCH_TEST_KAFKA_BROKERS",
            "POLICY_SEARCH_TEST_EVENT_OWNERSHIP",
        ):
            require(name not in os.environ, f"{name} must be unset; the runner owns dependencies")
        environment = child_environment(preferred_executables=(docker, go))
        run(
            [docker, "info"],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=30,
            activity="check Docker daemon",
            capture=True,
        )

        owned_containers.append(elasticsearch_name)
        run(
            [
                docker,
                "run",
                "--detach",
                "--name",
                elasticsearch_name,
                "--publish",
                "127.0.0.1::9200",
                "--env",
                "discovery.type=single-node",
                "--env",
                "xpack.security.enabled=false",
                "--env",
                "ES_JAVA_OPTS=-Xms512m -Xmx512m",
                elasticsearch_image,
            ],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=180,
            activity="start disposable event Elasticsearch",
            capture=True,
        )
        elasticsearch_port = run(
            [docker, "port", elasticsearch_name, "9200/tcp"],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=15,
            activity="resolve disposable event Elasticsearch port",
            capture=True,
        ).strip()
        require(
            elasticsearch_port.startswith("127.0.0.1:"),
            "unexpected Elasticsearch port binding",
        )
        elasticsearch_url = f"http://{elasticsearch_port}"
        wait_for_elasticsearch(elasticsearch_url)

        kafka_port = loopback_port()
        owned_containers.append(kafka_name)
        run(
            [
                docker,
                "run",
                "--detach",
                "--name",
                kafka_name,
                "--publish",
                f"127.0.0.1:{kafka_port}:9092",
                "--env",
                "CLUSTER_ID=MkU3OEVBNTcwNTJENDM2Qk",
                "--env",
                "KAFKA_NODE_ID=1",
                "--env",
                "KAFKA_PROCESS_ROLES=broker,controller",
                "--env",
                "KAFKA_LISTENERS=PLAINTEXT://0.0.0.0:9092,CONTROLLER://0.0.0.0:9093",
                "--env",
                f"KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://127.0.0.1:{kafka_port}",
                "--env",
                "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=PLAINTEXT:PLAINTEXT,CONTROLLER:PLAINTEXT",
                "--env",
                "KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER",
                "--env",
                "KAFKA_CONTROLLER_QUORUM_VOTERS=1@127.0.0.1:9093",
                "--env",
                "KAFKA_INTER_BROKER_LISTENER_NAME=PLAINTEXT",
                "--env",
                "KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1",
                "--env",
                "KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1",
                "--env",
                "KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1",
                "--env",
                "KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0",
                kafka_image,
            ],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=180,
            activity="start disposable event Kafka",
            capture=True,
        )
        wait_for_kafka_port(kafka_port)

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
                "-coverpkg=./...",
                f"-coverprofile={coverage}",
                "-json",
                "-run",
                "^TestEventFixture_",
                "./internal/testing",
            ],
            cwd=MODULE_ROOT,
            env={
                **environment,
                "GOWORK": "off",
                "POLICY_SEARCH_TEST_ELASTICSEARCH_URL": elasticsearch_url,
                "POLICY_SEARCH_TEST_KAFKA_BROKERS": f"127.0.0.1:{kafka_port}",
                "POLICY_SEARCH_TEST_REGISTERED_TOPIC": REGISTERED_TOPIC,
                "POLICY_SEARCH_TEST_TERMINATED_TOPIC": TERMINATED_TOPIC,
                "POLICY_SEARCH_TEST_CONSUMER_GROUP": CONSUMER_GROUP,
                "POLICY_SEARCH_TEST_EVENT_OWNERSHIP": OWNERSHIP,
            },
            timeout_seconds=600,
            teardown_seconds=20,
            activity="run policy-search event fixture integration",
            capture=True,
        )
        sys.stdout.write(output)
        expected = {
            "TestEventFixture_OracleDefinesIndependentCases",
            "TestEventFixture_RealKafkaAndElasticsearchResources",
        }
        discovered = passed_tests(output)
        require(
            expected <= discovered,
            f"event fixture suite missed expected tests; passed={sorted(discovered)}",
        )
        require(coverage_path.is_file(), "event integration coverage profile was not produced")
    except (IntegrationError, CommandError, OSError) as error:
        print(f"test-event-integration: {error}", file=sys.stderr)
        status = 1
    finally:
        for name in reversed(owned_containers):
            try:
                run(
                    [docker, "rm", "--force", name],
                    cwd=MODULE_ROOT,
                    env=environment,
                    timeout_seconds=60,
                    activity=f"remove owned container {name}",
                    capture=True,
                )
            except (CommandError, OSError) as error:
                print(f"test-event-integration: cleanup failed: {error}", file=sys.stderr)
                status = 1
    if status:
        return status
    print("test-event-integration: selected=2 cleanup=passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="test-event-integration"))
