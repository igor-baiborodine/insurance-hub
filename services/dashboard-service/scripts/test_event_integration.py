"""Fast tests for the disposable Dashboard event fixture runner."""

from __future__ import annotations

import stat
from pathlib import Path

import event_integration as integration
import pytest
from scripts.python.process_runner import CommandError


def configure(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> None:
    monkeypatch.setenv("DOCKER", "docker-controlled")
    monkeypatch.setenv("GO", "go-controlled")
    monkeypatch.setenv("ELASTICSEARCH_IMAGE", "elasticsearch@sha256:controlled")
    monkeypatch.setenv("KAFKA_IMAGE", "kafka@sha256:controlled")
    monkeypatch.setenv(
        "EVENT_INTEGRATION_COVERAGE_FILE",
        str(tmp_path / "controlled-event-coverage.txt"),
    )
    for name in integration.AMBIENT_TARGET_VARIABLES:
        monkeypatch.delenv(name, raising=False)


def prepare_owned_run(
    monkeypatch: pytest.MonkeyPatch,
    *,
    go_failure: str | None = None,
    cleanup_failure: bool = False,
) -> list[tuple[list[str], dict[str, object]]]:
    calls: list[tuple[list[str], dict[str, object]]] = []

    def fake_certificates(root: Path, _environment: dict[str, str]) -> Path:
        certificate = root / "ca.crt"
        certificate.write_text("controlled public CA\n", encoding="utf-8")
        return certificate

    def controlled_run(arguments: list[str], **options: object) -> str:
        command = list(arguments)
        calls.append((command, options))
        if command[:2] == ["docker-controlled", "port"]:
            return "127.0.0.1:49200\n"
        if command[0] == "go-controlled":
            if go_failure is not None:
                raise CommandError(go_failure)
            coverage = next(value for value in command if value.startswith("-coverprofile="))
            Path(coverage.split("=", 1)[1]).write_text("mode: atomic\n", encoding="utf-8")
            return "\n".join(
                (
                    '{"Action":"pass","Test":"TestEventFixture_OracleDefinesIndependentCases"}',
                    '{"Action":"pass","Test":"TestEventFixture_ProductionWorkerProjectsRegistration"}',
                    "",
                )
            )
        if (
            cleanup_failure
            and command[1:3] == ["rm", "--force"]
            and "dashboard-event-kafka" in command[-1]
        ):
            raise CommandError("controlled cleanup failure")
        return ""

    monkeypatch.setattr(integration, "run", controlled_run)
    monkeypatch.setattr(integration, "create_kafka_certificates", fake_certificates)
    monkeypatch.setattr(integration, "wait_for_elasticsearch", lambda _url: None)
    monkeypatch.setattr(integration, "wait_for_kafka_port", lambda _port: None)
    monkeypatch.setattr(
        integration,
        "wait_for_kafka_metadata",
        lambda _docker, _container, _environment: None,
    )
    monkeypatch.setattr(integration, "loopback_port", lambda: 49092)
    return calls


def test_main_rejects_ambient_target_without_running_commands_or_leaking_value(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    configure(monkeypatch, tmp_path)
    sentinel = "synthetic-secret-target.invalid"
    monkeypatch.setenv("DASHBOARD_TEST_EVENT_OWNERSHIP", sentinel)
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


def test_main_passes_only_exact_issue_290_coordinates_to_go(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    # given
    configure(monkeypatch, tmp_path)
    calls = prepare_owned_run(monkeypatch)

    # when
    status = integration.main()

    # then
    assert status == 0
    go_call = next(call for call in calls if call[0][0] == "go-controlled")
    environment = go_call[1]["env"]
    assert isinstance(environment, dict)
    assert environment["DASHBOARD_TEST_REGISTERED_TOPIC"] == "policy-registered-issue-290"
    assert environment["DASHBOARD_TEST_CONSUMER_GROUP"] == "dashboard-service-issue-290"
    assert environment["DASHBOARD_TEST_EVENT_INDEX"] == "policy_stats_issue_290"
    assert environment["DASHBOARD_TEST_EVENT_OWNERSHIP"] == "issue-290-disposable"
    assert "policy-registered" not in {
        environment["DASHBOARD_TEST_REGISTERED_TOPIC"],
        environment["DASHBOARD_TEST_CONSUMER_GROUP"],
    }
    assert go_call[0][-1] == "./internal/testing"
    assert "-tags=integration" in go_call[0]


def test_main_propagates_test_failure_and_removes_owned_containers_in_reverse_order(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    # given
    configure(monkeypatch, tmp_path)
    calls = prepare_owned_run(monkeypatch, go_failure="controlled fixture failure")

    # when
    status = integration.main()

    # then
    assert status == 1
    removals = [command for command, _options in calls if command[1:3] == ["rm", "--force"]]
    assert len(removals) == 2
    assert "dashboard-event-kafka" in removals[0][-1]
    assert "dashboard-event-elasticsearch" in removals[1][-1]


def test_main_reports_cleanup_failure_and_continues_reverse_cleanup(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    configure(monkeypatch, tmp_path)
    calls = prepare_owned_run(monkeypatch, cleanup_failure=True)

    # when
    status = integration.main()

    # then
    assert status == 1
    removals = [command for command, _options in calls if command[1:3] == ["rm", "--force"]]
    assert len(removals) == 2
    assert "cleanup failed" in capsys.readouterr().err


def test_main_starts_pinned_dependencies_on_loopback_with_kafka_tls(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    # given
    configure(monkeypatch, tmp_path)
    calls = prepare_owned_run(monkeypatch)

    # when
    assert integration.main() == 0

    # then
    containers = [
        command
        for command, _options in calls
        if command[:3] == ["docker-controlled", "run", "--detach"]
    ]
    assert len(containers) == 2
    elasticsearch = next(command for command in containers if "elasticsearch@" in command[-1])
    kafka = next(command for command in containers if "kafka@" in command[-1])
    assert elasticsearch[elasticsearch.index("--publish") + 1] == "127.0.0.1::9200"
    assert kafka[kafka.index("--publish") + 1] == "127.0.0.1:49092:9092"
    joined = "\n".join(kafka)
    assert "KAFKA_ADVERTISED_LISTENERS=SSL://127.0.0.1:49092" in joined
    assert "KAFKA_SSL_KEYSTORE_FILENAME=server.p12" in joined
    assert "/etc/kafka/secrets:ro" in joined
    assert "PLAINTEXT://127.0.0.1:49092" not in joined


def test_create_kafka_certificates_keeps_password_out_of_arguments_and_files_private(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    # given
    commands: list[list[str]] = []

    def controlled_run(arguments: list[str], **_options: object) -> str:
        command = list(arguments)
        commands.append(command)
        for flag in ("-keyout", "-out"):
            if flag in command:
                Path(command[command.index(flag) + 1]).touch()
        return ""

    monkeypatch.setattr(integration, "run", controlled_run)

    # when
    certificate = integration.create_kafka_certificates(tmp_path, {})

    # then
    assert certificate == tmp_path / "ca.crt"
    assert all("changeit" not in argument for command in commands for argument in command)
    for name in (
        "ca.key",
        "server.key",
        "server.p12",
        "openssl-password",
        "key-password",
        "keystore-password",
    ):
        assert stat.S_IMODE((tmp_path / name).stat().st_mode) == 0o600


def test_passed_tests_ignores_non_json_and_package_records() -> None:
    # when
    passed = integration.passed_tests(
        'not-json\n{"Action":"pass","Package":"example"}\n'
        '{"Action":"pass","Test":"TestEventFixture_Expected"}\n'
    )

    # then
    assert passed == {"TestEventFixture_Expected"}
