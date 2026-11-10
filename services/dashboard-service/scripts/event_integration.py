#!/usr/bin/env python3
"""Validate Dashboard event fixtures against owned disposable Kafka and Elasticsearch."""

from __future__ import annotations

import json
import os
import socket
import sys
import tempfile
import time
import urllib.request
from pathlib import Path

from scripts.go.service.tooling_process import run, run_cli
from scripts.python.process_runner import CommandError
from service_process import child_environment

MODULE_ROOT = Path(__file__).resolve().parent.parent
REGISTERED_TOPIC = "policy-registered"
CONSUMER_GROUP = "dashboard-service"
INDEX = "policy_stats"
OWNERSHIP = "dashboard-disposable"
AMBIENT_TARGET_VARIABLES = (
    "DASHBOARD_TEST_ELASTICSEARCH_URL",
    "DASHBOARD_TEST_KAFKA_BROKERS",
    "DASHBOARD_TEST_EVENT_OWNERSHIP",
    "DASHBOARD_TEST_KAFKA_CA_FILE",
    "DASHBOARD_TEST_REGISTERED_TOPIC",
    "DASHBOARD_TEST_CONSUMER_GROUP",
    "DASHBOARD_TEST_EVENT_INDEX",
)


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


def wait_for_kafka_metadata(
    docker: str,
    container: str,
    environment: dict[str, str],
    deadline_seconds: float = 120,
) -> None:
    deadline = time.monotonic() + deadline_seconds
    while time.monotonic() < deadline:
        try:
            run(
                [
                    docker,
                    "exec",
                    container,
                    "/opt/kafka/bin/kafka-topics.sh",
                    "--bootstrap-server",
                    "127.0.0.1:9094",
                    "--list",
                ],
                cwd=MODULE_ROOT,
                env=environment,
                timeout_seconds=10,
                activity="wait for disposable Dashboard Kafka metadata",
                capture=True,
            )
            return
        except CommandError, OSError:
            time.sleep(0.25)
    raise IntegrationError(f"Kafka metadata readiness exceeded {deadline_seconds:g}s")


def wait_for_elasticsearch(url: str, deadline_seconds: float = 120) -> None:
    deadline = time.monotonic() + deadline_seconds
    last_status = "no response"
    while time.monotonic() < deadline:
        try:
            request = urllib.request.Request(
                f"{url}/_cluster/health?wait_for_status=yellow&timeout=1s"
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


def _write_private_text(path: Path, value: str) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        stream.write(value)


def create_kafka_certificates(
    root: Path,
    environment: dict[str, str],
    openssl: str = "openssl",
) -> Path:
    ca_key = root / "ca.key"
    ca_certificate = root / "ca.crt"
    server_key = root / "server.key"
    server_request = root / "server.csr"
    server_certificate = root / "server.crt"
    extensions = root / "server.ext"
    keystore = root / "server.p12"
    password_source = root / "openssl-password"
    _write_private_text(password_source, "changeit\n")
    _write_private_text(root / "key-password", "changeit\n")
    _write_private_text(root / "keystore-password", "changeit\n")
    extensions.write_text(
        "subjectAltName=IP:127.0.0.1,DNS:localhost\nextendedKeyUsage=serverAuth\n",
        encoding="utf-8",
    )
    for arguments in (
        [
            openssl,
            "req",
            "-x509",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-days",
            "1",
            "-subj",
            "/CN=dashboard-disposable-ca",
            "-keyout",
            str(ca_key),
            "-out",
            str(ca_certificate),
        ],
        [
            openssl,
            "req",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-subj",
            "/CN=127.0.0.1",
            "-keyout",
            str(server_key),
            "-out",
            str(server_request),
        ],
        [
            openssl,
            "x509",
            "-req",
            "-days",
            "1",
            "-in",
            str(server_request),
            "-CA",
            str(ca_certificate),
            "-CAkey",
            str(ca_key),
            "-CAcreateserial",
            "-extfile",
            str(extensions),
            "-out",
            str(server_certificate),
        ],
        [
            openssl,
            "pkcs12",
            "-export",
            "-name",
            "kafka",
            "-in",
            str(server_certificate),
            "-inkey",
            str(server_key),
            "-certfile",
            str(ca_certificate),
            "-passout",
            f"file:{password_source}",
            "-out",
            str(keystore),
        ],
    ):
        run(
            arguments,
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=30,
            activity="create disposable Dashboard Kafka TLS identity",
            capture=True,
        )
    for path in (ca_key, server_key, keystore, password_source):
        path.chmod(0o600)
    for path in (ca_certificate, server_request, server_certificate, extensions):
        path.chmod(0o644)
    return ca_certificate


def _coverage_path(value: str) -> Path:
    path = Path(value)
    return path if path.is_absolute() else MODULE_ROOT / path


def main() -> int:
    docker = os.environ.get("DOCKER", "")
    go = os.environ.get("GO", "")
    elasticsearch_image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    kafka_image = os.environ.get("KAFKA_IMAGE", "")
    coverage = os.environ.get("EVENT_INTEGRATION_COVERAGE_FILE", "")
    elasticsearch_name = f"dashboard-event-elasticsearch-{os.getpid()}"
    kafka_name = f"dashboard-event-kafka-{os.getpid()}"
    kafka_secrets_init_name = f"dashboard-event-kafka-secrets-init-{os.getpid()}"
    kafka_secrets_volume = f"dashboard-event-kafka-secrets-{os.getpid()}"
    owned_containers: list[str] = []
    owned_volumes: list[str] = []
    status = 0
    environment: dict[str, str] = {}
    certificate_directory: tempfile.TemporaryDirectory[str] | None = None
    try:
        require(docker, "DOCKER is required")
        require(go, "GO is required")
        require(elasticsearch_image, "ELASTICSEARCH_IMAGE is required")
        require(kafka_image, "KAFKA_IMAGE is required")
        require(coverage, "EVENT_INTEGRATION_COVERAGE_FILE is required")
        for name in AMBIENT_TARGET_VARIABLES:
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
        certificate_directory = tempfile.TemporaryDirectory(prefix="dashboard-event-tls-")
        certificate_root = Path(certificate_directory.name)
        ca_certificate = create_kafka_certificates(certificate_root, environment)

        owned_volumes.append(kafka_secrets_volume)
        run(
            [
                docker,
                "volume",
                "create",
                "--label",
                "insurance-hub.owner=dashboard-disposable",
                kafka_secrets_volume,
            ],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=30,
            activity="create disposable Dashboard Kafka secrets volume",
            capture=True,
        )
        owned_containers.append(kafka_secrets_init_name)
        run(
            [
                docker,
                "create",
                "--name",
                kafka_secrets_init_name,
                "--user",
                "root",
                "--network",
                "none",
                "--read-only",
                "--volume",
                f"{certificate_root}:/source:ro",
                "--volume",
                f"{kafka_secrets_volume}:/target",
                "--entrypoint",
                "/bin/sh",
                kafka_image,
                "-ec",
                (
                    "cp /source/server.p12 /source/key-password "
                    "/source/keystore-password /target/; "
                    "chown -R appuser:appuser /target; "
                    "chmod 0700 /target; "
                    "chmod 0600 /target/server.p12 /target/key-password "
                    "/target/keystore-password"
                ),
            ],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=30,
            activity="create disposable Dashboard Kafka secrets initializer",
            capture=True,
        )
        run(
            [docker, "start", "--attach", kafka_secrets_init_name],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=30,
            activity="stage disposable Dashboard Kafka secrets",
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
            activity="start disposable Dashboard event Elasticsearch",
            capture=True,
        )
        elasticsearch_port = run(
            [docker, "port", elasticsearch_name, "9200/tcp"],
            cwd=MODULE_ROOT,
            env=environment,
            timeout_seconds=15,
            activity="resolve disposable Dashboard event Elasticsearch port",
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
                "--volume",
                f"{kafka_secrets_volume}:/etc/kafka/secrets:ro",
                "--env",
                "CLUSTER_ID=MkU3OEVBNTcwNTJENDM2Qk",
                "--env",
                "KAFKA_NODE_ID=1",
                "--env",
                "KAFKA_PROCESS_ROLES=broker,controller",
                "--env",
                "KAFKA_LISTENERS=SSL://0.0.0.0:9092,CONTROLLER://0.0.0.0:9093,INTERNAL://0.0.0.0:9094",
                "--env",
                f"KAFKA_ADVERTISED_LISTENERS=SSL://127.0.0.1:{kafka_port},INTERNAL://127.0.0.1:9094",
                "--env",
                "KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=SSL:SSL,CONTROLLER:PLAINTEXT,INTERNAL:PLAINTEXT",
                "--env",
                "KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER",
                "--env",
                "KAFKA_CONTROLLER_QUORUM_VOTERS=1@127.0.0.1:9093",
                "--env",
                "KAFKA_INTER_BROKER_LISTENER_NAME=INTERNAL",
                "--env",
                "KAFKA_SSL_KEYSTORE_FILENAME=server.p12",
                "--env",
                "KAFKA_SSL_KEYSTORE_TYPE=PKCS12",
                "--env",
                "KAFKA_SSL_KEYSTORE_CREDENTIALS=keystore-password",
                "--env",
                "KAFKA_SSL_KEY_CREDENTIALS=key-password",
                "--env",
                "KAFKA_SSL_CLIENT_AUTH=none",
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
            activity="start disposable Dashboard event Kafka",
            capture=True,
        )
        wait_for_kafka_port(kafka_port)
        wait_for_kafka_metadata(docker, kafka_name, environment)

        coverage_path = _coverage_path(coverage)
        coverage_path.parent.mkdir(parents=True, exist_ok=True)
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
                f"-coverprofile={coverage_path}",
                "-json",
                "-run",
                "^TestEventFixture_",
                "./internal/testing",
            ],
            cwd=MODULE_ROOT,
            env={
                **environment,
                "GOWORK": "off",
                "DASHBOARD_TEST_ELASTICSEARCH_URL": elasticsearch_url,
                "DASHBOARD_TEST_KAFKA_BROKERS": f"127.0.0.1:{kafka_port}",
                "DASHBOARD_TEST_REGISTERED_TOPIC": REGISTERED_TOPIC,
                "DASHBOARD_TEST_CONSUMER_GROUP": CONSUMER_GROUP,
                "DASHBOARD_TEST_EVENT_INDEX": INDEX,
                "DASHBOARD_TEST_EVENT_OWNERSHIP": OWNERSHIP,
                "DASHBOARD_TEST_KAFKA_CA_FILE": str(ca_certificate),
            },
            timeout_seconds=600,
            teardown_seconds=20,
            activity="run Dashboard event fixture integration",
            capture=True,
            reveal_failure_output=True,
        )
        sys.stdout.write(output)
        expected = {
            "TestEventFixture_OracleDefinesIndependentCases",
            "TestEventFixture_ProductionWorkerProjectsRegistration",
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
                    activity=f"remove owned Dashboard event container {name}",
                    capture=True,
                )
            except (CommandError, OSError) as error:
                print(f"test-event-integration: cleanup failed: {error}", file=sys.stderr)
                status = 1
        for name in reversed(owned_volumes):
            try:
                run(
                    [docker, "volume", "rm", "--force", name],
                    cwd=MODULE_ROOT,
                    env=environment,
                    timeout_seconds=30,
                    activity=f"remove owned Dashboard event volume {name}",
                    capture=True,
                )
            except (CommandError, OSError) as error:
                print(f"test-event-integration: volume cleanup failed: {error}", file=sys.stderr)
                status = 1
        if certificate_directory is not None:
            try:
                certificate_directory.cleanup()
            except OSError as error:
                print(f"test-event-integration: TLS cleanup failed: {error}", file=sys.stderr)
                status = 1
    if status:
        return status
    print("test-event-integration: selected=2 production-worker=exercised cleanup=passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(run_cli(main, name="test-event-integration"))
