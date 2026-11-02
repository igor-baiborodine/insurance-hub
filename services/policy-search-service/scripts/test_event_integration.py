"""Fast tests for the disposable event integration runner."""

from __future__ import annotations

import signal

import event_integration as integration
import pytest
from scripts.go.service.tooling_process import ToolingSignal
from scripts.python.process_runner import CommandError


def configure(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("DOCKER", "docker-controlled")
    monkeypatch.setenv("GO", "go-controlled")
    monkeypatch.setenv("ELASTICSEARCH_IMAGE", "elasticsearch@sha256:controlled")
    monkeypatch.setenv("KAFKA_IMAGE", "kafka@sha256:controlled")
    monkeypatch.setenv("EVENT_INTEGRATION_COVERAGE_FILE", "controlled-coverage.txt")
    for name in (
        "POLICY_SEARCH_TEST_ELASTICSEARCH_URL",
        "POLICY_SEARCH_TEST_KAFKA_BROKERS",
        "POLICY_SEARCH_TEST_EVENT_OWNERSHIP",
        "POLICY_SEARCH_TEST_KAFKA_CA_FILE",
    ):
        monkeypatch.delenv(name, raising=False)


def prepare_owned_run(
    monkeypatch: pytest.MonkeyPatch,
    failure: str = "controlled test failure",
) -> list[list[str]]:
    commands: list[list[str]] = []

    def controlled_run(arguments: list[str], **_options: object) -> str:
        commands.append(list(arguments))
        if arguments[:2] == ["docker-controlled", "port"]:
            return "127.0.0.1:49200\n"
        if arguments[0] == "go-controlled":
            raise CommandError(failure)
        return ""

    monkeypatch.setattr(integration, "run", controlled_run)
    monkeypatch.setattr(integration, "wait_for_elasticsearch", lambda _url: None)
    monkeypatch.setattr(integration, "wait_for_kafka_port", lambda _port: None)
    monkeypatch.setattr(integration, "loopback_port", lambda: 49092)
    return commands


def test_main_rejects_ambient_endpoint_without_leaking_value(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    configure(monkeypatch)
    sentinel = "synthetic-secret-endpoint.invalid"
    monkeypatch.setenv("POLICY_SEARCH_TEST_KAFKA_BROKERS", sentinel)
    commands: list[list[str]] = []
    monkeypatch.setattr(
        integration,
        "run",
        lambda arguments, **_options: commands.append(list(arguments)) or "",
    )

    # when
    status = integration.main()

    # then
    assert status == 1
    assert commands == []
    captured = capsys.readouterr()
    assert sentinel not in captured.out
    assert sentinel not in captured.err


@pytest.mark.parametrize(
    "failure",
    ["controlled test failure", "controlled test exceeded its 1-second deadline"],
)
def test_main_propagates_failure_or_timeout_and_removes_owned_containers_in_reverse_order(
    monkeypatch: pytest.MonkeyPatch,
    failure: str,
) -> None:
    # given
    configure(monkeypatch)
    commands = prepare_owned_run(monkeypatch, failure)

    # when
    status = integration.main()

    # then
    assert status == 1
    removals = [command for command in commands if command[1:3] == ["rm", "--force"]]
    assert len(removals) == 2
    assert "event-kafka" in removals[0][-1]
    assert "event-elasticsearch" in removals[1][-1]


def test_run_cli_maps_interruption_after_cleanup(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    configure(monkeypatch)
    commands = prepare_owned_run(monkeypatch)

    def interrupting_run(arguments: list[str], **options: object) -> str:
        if arguments[0] == "go-controlled":
            raise ToolingSignal(signal.SIGINT)
        return next_run(arguments, **options)

    next_run = integration.run
    monkeypatch.setattr(integration, "run", interrupting_run)

    # when
    status = integration.run_cli(integration.main, name="test-event-integration")

    # then
    assert status == 130
    removals = [command for command in commands if command[1:3] == ["rm", "--force"]]
    assert len(removals) == 2


def test_main_starts_kafka_with_owned_tls_identity(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    configure(monkeypatch)
    commands = prepare_owned_run(monkeypatch)

    # when
    assert integration.main() == 1

    # then
    kafka = next(
        command
        for command in commands
        if command[:3] == ["docker-controlled", "run", "--detach"]
        and any("event-kafka" in value for value in command)
    )
    joined = "\n".join(kafka)
    assert "KAFKA_ADVERTISED_LISTENERS=SSL://127.0.0.1:49092" in joined
    assert "KAFKA_SSL_KEYSTORE_FILENAME=server.p12" in joined
    assert "/etc/kafka/secrets:ro" in joined
    assert "PLAINTEXT://127.0.0.1:49092" not in joined
    kafka_name = kafka[kafka.index("--name") + 1]
    assert any(
        command[:3] == ["docker-controlled", "exec", kafka_name]
        and "kafka-topics.sh" in command[3]
        and command[-2:] == ["127.0.0.1:9094", "--list"]
        for command in commands
    )


def test_passed_tests_ignores_non_json_and_package_records() -> None:
    # when
    passed = integration.passed_tests(
        'not-json\n{"Action":"pass","Package":"example"}\n'
        '{"Action":"pass","Test":"TestEventFixture_Expected"}\n'
    )

    # then
    assert passed == {"TestEventFixture_Expected"}
