"""Fast tests for the disposable Elasticsearch integration runner."""

from __future__ import annotations

import elasticsearch_integration as integration
import pytest
from scripts.python.process_runner import CommandError


def configure(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("DOCKER", "docker-controlled")
    monkeypatch.setenv("GO", "go-controlled")
    monkeypatch.setenv("ELASTICSEARCH_IMAGE", "elasticsearch@sha256:controlled")
    monkeypatch.setenv("ELASTICSEARCH_INTEGRATION_COVERAGE_FILE", "controlled-coverage.txt")


def test_main_rejects_ambient_endpoint_without_running_commands(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    configure(monkeypatch)
    monkeypatch.setenv("POLICY_SEARCH_TEST_ELASTICSEARCH_URL", "http://ambient.invalid")
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


def test_main_removes_owned_container_after_test_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    configure(monkeypatch)
    monkeypatch.delenv("POLICY_SEARCH_TEST_ELASTICSEARCH_URL", raising=False)
    commands: list[list[str]] = []

    def controlled_run(arguments: list[str], **_options: object) -> str:
        commands.append(list(arguments))
        if arguments[:2] == ["docker-controlled", "port"]:
            return "127.0.0.1:49200\n"
        if arguments[0] == "go-controlled":
            raise CommandError("controlled test failure")
        return ""

    monkeypatch.setattr(integration, "run", controlled_run)
    monkeypatch.setattr(integration, "wait_for_elasticsearch", lambda _url: None)

    # when
    status = integration.main()

    # then
    assert status == 1
    assert commands[-1][0:3] == ["docker-controlled", "rm", "--force"]


def test_passed_tests_ignores_non_json_and_package_records() -> None:
    # when
    passed = integration.passed_tests(
        'not-json\n{"Action":"pass","Package":"example"}\n'
        '{"Action":"pass","Test":"TestElasticsearchQuery_Expected"}\n'
    )

    # then
    assert passed == {"TestElasticsearchQuery_Expected"}
