"""Tests for disposable Product catalog round-trip ownership."""

from __future__ import annotations

from collections.abc import Callable

import pytest

from scripts.product_baseline import catalog_roundtrip
from scripts.python.process_runner import CommandError


def configure_roundtrip(
    monkeypatch: pytest.MonkeyPatch,
    command: Callable[..., str],
) -> None:
    monkeypatch.setenv("BASELINE_ENV", "local-dev")
    monkeypatch.setattr(catalog_roundtrip.fixtures, "validate_part", lambda *_args: None)
    monkeypatch.setattr(
        catalog_roundtrip.fixtures,
        "load_document",
        lambda *_args: {"rows": []},
    )
    monkeypatch.setattr(catalog_roundtrip.secrets, "token_hex", lambda _size: "owned")
    monkeypatch.setattr(catalog_roundtrip, "command", command)


def test_partial_container_creation_attempts_owned_cleanup(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    commands: list[list[str]] = []

    def fail_creation(arguments: list[str], **_kwargs: object) -> str:
        commands.append(arguments)
        if arguments[1] == "run":
            raise CommandError("controlled uncertain create result")
        if arguments[1] == "inspect":
            return "owned\n"
        return ""

    configure_roundtrip(monkeypatch, fail_creation)

    # when
    status = catalog_roundtrip.main([])

    # then
    assert status == 2
    assert [command[1:3] for command in commands] == [
        ["run", "--detach"],
        ["inspect", "--format"],
        ["rm", "-f"],
    ]
    assert commands[0][commands[0].index("--name") + 1] == commands[2][-1]
    assert commands[0][commands[0].index("--label") + 1] == (
        f"{catalog_roundtrip.OWNER_LABEL}=owned"
    )
    assert "Validated" not in capsys.readouterr().out


def test_successful_roundtrip_reports_cleanup_failure(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    def fail_cleanup(arguments: list[str], **_kwargs: object) -> str:
        if arguments[1] == "inspect":
            return "owned\n"
        if arguments[1] == "rm":
            raise CommandError("controlled cleanup failure")
        return ""

    configure_roundtrip(monkeypatch, fail_cleanup)

    # when
    status = catalog_roundtrip.main([])

    # then
    output = capsys.readouterr()
    assert status == 2
    assert "could not remove owned container" in output.err
    assert "controlled cleanup failure" in output.err
    assert "Validated" not in output.out


def test_successful_roundtrip_removes_owned_container(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    commands: list[list[str]] = []

    def succeed(arguments: list[str], **_kwargs: object) -> str:
        commands.append(arguments)
        if arguments[1] == "inspect":
            return "owned\n"
        return ""

    configure_roundtrip(monkeypatch, succeed)

    # when
    status = catalog_roundtrip.main([])

    # then
    assert status == 0
    assert commands[-1][1:3] == ["rm", "-f"]
    assert "Validated local-dev catalog" in capsys.readouterr().out


def test_roundtrip_refuses_to_remove_container_with_different_owner(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    commands: list[list[str]] = []

    def mismatched_owner(arguments: list[str], **_kwargs: object) -> str:
        commands.append(arguments)
        if arguments[1] == "inspect":
            return "different-owner\n"
        return ""

    configure_roundtrip(monkeypatch, mismatched_owner)

    # when
    status = catalog_roundtrip.main([])

    # then
    output = capsys.readouterr()
    assert status == 2
    assert not any(arguments[1] == "rm" for arguments in commands)
    assert "ownership label does not match" in output.err
    assert "Validated" not in output.out
