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
    monkeypatch.setenv("SOURCE_REVISION", "0123456789abcdef0123456789abcdef01234567")
    monkeypatch.setenv("ELASTICSEARCH_IMAGE", "elasticsearch@sha256:controlled")


def test_main_rejects_missing_dependency_image_without_commands(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    configure(monkeypatch)
    monkeypatch.delenv("ELASTICSEARCH_IMAGE")
    commands: list[list[str]] = []
    monkeypatch.setattr(
        image_check,
        "command",
        lambda arguments, *_args, **_kwargs: commands.append(list(arguments)) or "",
    )

    # when
    result = image_check.main()

    # then
    assert result == 1
    assert commands == []


@pytest.mark.parametrize(
    ("version", "revision"),
    [
        ("git-000000000000", "0000000000000000000000000000000000000000"),
        ("development", "0123456789abcdef0123456789abcdef01234567"),
        ("git-fedcba987654", "0123456789abcdef0123456789abcdef01234567"),
    ],
)
def test_main_rejects_unresolved_or_mismatched_identity_without_commands(
    monkeypatch: pytest.MonkeyPatch,
    version: str,
    revision: str,
) -> None:
    configure(monkeypatch)
    monkeypatch.setenv("SOURCE_VERSION", version)
    monkeypatch.setenv("SOURCE_REVISION", revision)
    commands: list[list[str]] = []
    monkeypatch.setattr(
        image_check,
        "command",
        lambda arguments, *_args, **_kwargs: commands.append(list(arguments)) or "",
    )

    result = image_check.main()

    assert result == 1
    assert commands == []


def test_main_owns_dependency_network_reader_configuration_and_cleanup(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
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
    permission_urls: list[str] = []
    monkeypatch.setattr(
        image_check,
        "verify_reader_permissions",
        lambda url: permission_urls.append(url),
    )
    monkeypatch.setattr(image_check, "wait_status", lambda *_args, **_kwargs: None)
    requested_urls: list[str] = []

    def controlled_request(url: str, *_args: object, **_kwargs: object) -> tuple[int, bytes]:
        requested_urls.append(url)
        if url.endswith("/livez"):
            return 200, b"live\n"
        if url.endswith("/readyz"):
            return 503, b"not ready\n"
        if url.endswith("/dashboard/totalsales"):
            if any(
                command[1:4] == ["stop", "--time", "10"]
                and command[-1] == "dashboard-image-elasticsearch-4242"
                for command in commands
            ):
                return 503, b'{"code":14,"message":"service unavailable","details":[]}'
            return 200, json.dumps(
                {
                    "total": {"policiesCount": 1, "premiumAmount": 1000.0},
                    "perProductTotal": {"SAFE": {"policiesCount": 1, "premiumAmount": 1000.0}},
                }
            ).encode()
        if url.endswith("/dashboard/agentssales"):
            if any(
                command[1:4] == ["stop", "--time", "10"]
                and command[-1] == "dashboard-image-elasticsearch-4242"
                for command in commands
            ):
                return 503, b'{"code":14,"message":"service unavailable","details":[]}'
            return 200, json.dumps(
                {"perAgentTotal": {"safe-agent": {"policiesCount": 1, "premiumAmount": 1000.0}}}
            ).encode()
        if url.endswith("/dashboard/trends"):
            if any(
                command[1:4] == ["stop", "--time", "10"]
                and command[-1] == "dashboard-image-elasticsearch-4242"
                for command in commands
            ):
                return 503, b'{"code":14,"message":"service unavailable","details":[]}'
            return 200, json.dumps(
                {
                    "periodSales": [
                        {
                            "periodDate": [2019, 5, 31],
                            "period": "2019-06-01T00:00:00.000Z",
                            "policiesCount": 1,
                            "premiumAmount": 1000.0,
                        }
                    ]
                }
            ).encode()
        raise AssertionError(url)

    monkeypatch.setattr(image_check, "request", controlled_request)
    monkeypatch.setattr(
        image_check,
        "container_logs",
        lambda _docker, _container: (
            '{"msg":"service started","service_version":"git-0123456789ab"}'
        ),
    )
    monkeypatch.setattr(image_check, "verify_runtime_identity", lambda _logs, _version: None)

    # when
    result = image_check.main()

    # then
    assert result == 0
    assert fixture_urls == ["http://127.0.0.1:49200"]
    assert permission_urls == ["http://127.0.0.1:49200"]
    flattened = "\n".join(" ".join(command) for command in commands)
    assert "--network-alias elasticsearch" in flattened
    assert "ELASTICSEARCH_URL=http://elasticsearch:9200" in flattened
    assert "ELASTICSEARCH_USERNAME=dashboard_image_reader" in flattened
    assert "EVENT_WORKERS_ENABLED=false" in flattened
    assert "SALES_TRENDS_TIME_ZONE=America/Toronto" in flattened
    assert sum(url.endswith("/dashboard/trends") for url in requested_urls) == 2
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
    # given
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
    monkeypatch.setattr(image_check, "verify_reader_permissions", lambda _url: None)

    # when
    result = image_check.main()

    # then
    assert result == 1
    assert [command[1:3] for command in commands[-3:]] == [
        ["rm", "--force"],
        ["rm", "--force"],
        ["network", "rm"],
    ]


def test_reader_permissions_require_write_admin_and_index_denials(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    responses = iter(((403, b"write"), (403, b"admin"), (403, b"index")))
    calls: list[tuple[str, str]] = []

    def controlled_reader(
        _url: str, method: str, path: str, _body: dict[str, object] | None = None
    ) -> tuple[int, bytes]:
        calls.append((method, path))
        return next(responses)

    monkeypatch.setattr(image_check, "reader_request", controlled_reader)

    image_check.verify_reader_permissions("http://elasticsearch")

    assert calls == [
        ("PUT", "/policy_stats/_doc/denied-write"),
        ("GET", "/_cluster/settings"),
        ("GET", "/unrelated_stats/_search"),
    ]


@pytest.mark.parametrize("allowed_index", [0, 1, 2])
def test_reader_permissions_run_every_denial_before_reporting_drift(
    monkeypatch: pytest.MonkeyPatch, allowed_index: int
) -> None:
    responses = iter(
        (200, b"unexpected authority") if index == allowed_index else (403, b"denied")
        for index in range(3)
    )
    calls: list[str] = []

    def controlled_reader(
        _url: str, _method: str, path: str, _body: dict[str, object] | None = None
    ) -> tuple[int, bytes]:
        calls.append(path)
        return next(responses)

    monkeypatch.setattr(image_check, "reader_request", controlled_reader)

    with pytest.raises(image_check.ImageCheckError, match="reader denials failed"):
        image_check.verify_reader_permissions("http://elasticsearch")

    assert calls == [
        "/policy_stats/_doc/denied-write",
        "/_cluster/settings",
        "/unrelated_stats/_search",
    ]


def test_runtime_identity_rejects_different_revision() -> None:
    logs = json.dumps({"msg": "service started", "service_version": "git-fedcba987654"})

    with pytest.raises(image_check.ImageCheckError, match="exact source version"):
        image_check.verify_runtime_identity(logs, "git-0123456789ab")
