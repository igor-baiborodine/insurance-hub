"""Fast ownership and cleanup tests for the Dashboard image checker."""

from __future__ import annotations

import json

import image_check
import pytest
from scripts.python.process_runner import CommandError


def configure(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("DOCKER", "docker-controlled")
    monkeypatch.setenv("IMAGE_REF", "dashboard-image:controlled")
    monkeypatch.setenv("SOURCE_VERSION", "git-0123456789ab")
    monkeypatch.setenv("ELASTICSEARCH_IMAGE", "elasticsearch@sha256:controlled")


def test_main_rejects_missing_dependency_image_without_commands(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    configure(monkeypatch)
    monkeypatch.delenv("ELASTICSEARCH_IMAGE")
    commands: list[list[str]] = []
    monkeypatch.setattr(
        image_check,
        "command",
        lambda arguments, *_args, **_kwargs: commands.append(list(arguments)) or "",
    )

    assert image_check.main() == 1
    assert commands == []


def test_main_owns_dependency_network_reader_configuration_and_cleanup(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    configure(monkeypatch)
    commands: list[list[str]] = []
    environments: list[dict[str, str]] = []

    def controlled_command(
        arguments: list[str],
        _activity: str,
        _timeout: float = 30,
        environment: dict[str, str] | None = None,
    ) -> str:
        commands.append(list(arguments))
        environments.append(dict(environment or {}))
        if arguments[1:3] == ["port", "dashboard-image-elasticsearch-4242"]:
            return "127.0.0.1:49200"
        if arguments[1:3] == ["port", "dashboard-image-runtime-4242"]:
            return {
                "8080/tcp": "127.0.0.1:48080",
                "8081/tcp": "127.0.0.1:48081",
            }[arguments[3]]
        if arguments[1:3] == ["inspect", "--format"]:
            return "0"
        return ""

    monkeypatch.setattr(image_check.os, "getpid", lambda: 4242)
    monkeypatch.setattr(image_check, "command", controlled_command)
    monkeypatch.setattr(image_check, "wait_for_elasticsearch", lambda _url: None)
    fixture_urls: list[str] = []
    monkeypatch.setattr(
        image_check,
        "create_elasticsearch_fixture",
        lambda url: fixture_urls.append(url),
    )
    monkeypatch.setattr(image_check, "wait_status", lambda *_args, **_kwargs: None)

    def controlled_request(url: str, *_args: object, **_kwargs: object) -> tuple[int, bytes]:
        if url.endswith("/livez"):
            return 200, b"live\n"
        if url.endswith("/dashboard/totalsales"):
            return 200, json.dumps(
                {
                    "total": {"policiesCount": 1, "premiumAmount": 1000.0},
                    "perProductTotal": {"SAFE": {"policiesCount": 1, "premiumAmount": 1000.0}},
                }
            ).encode()
        raise AssertionError(url)

    monkeypatch.setattr(image_check, "request", controlled_request)

    assert image_check.main() == 0
    assert fixture_urls == ["http://127.0.0.1:49200"]
    flattened = "\n".join(" ".join(command) for command in commands)
    assert "--network-alias elasticsearch" in flattened
    assert "ELASTICSEARCH_URL=http://elasticsearch:9200" in flattened
    assert "ELASTICSEARCH_USERNAME=dashboard_image_reader" in flattened
    assert image_check.ADMIN_PASSWORD not in flattened
    assert image_check.READER_PASSWORD not in flattened
    assert {"ELASTIC_PASSWORD": image_check.ADMIN_PASSWORD} in environments
    assert {"ELASTICSEARCH_PASSWORD": image_check.READER_PASSWORD} in environments
    assert [command[1:3] for command in commands[-3:]] == [
        ["rm", "--force"],
        ["rm", "--force"],
        ["network", "rm"],
    ]


def test_main_cleans_dependency_and_network_after_dashboard_start_failure(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    configure(monkeypatch)
    monkeypatch.setattr(image_check.os, "getpid", lambda: 4343)
    commands: list[list[str]] = []

    def controlled_command(arguments: list[str], *_args: object, **_kwargs: object) -> str:
        commands.append(list(arguments))
        if arguments[1:3] == ["port", "dashboard-image-elasticsearch-4343"]:
            return "127.0.0.1:49200"
        if arguments[-1:] == ["dashboard-image:controlled"]:
            raise CommandError("controlled Dashboard start failure")
        return ""

    monkeypatch.setattr(image_check, "command", controlled_command)
    monkeypatch.setattr(image_check, "wait_for_elasticsearch", lambda _url: None)
    monkeypatch.setattr(image_check, "create_elasticsearch_fixture", lambda _url: None)

    assert image_check.main() == 1
    assert [command[1:3] for command in commands[-3:]] == [
        ["rm", "--force"],
        ["rm", "--force"],
        ["network", "rm"],
    ]
