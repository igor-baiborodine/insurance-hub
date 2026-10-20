"""Behavior tests for Product baseline fixture validation."""

from __future__ import annotations

import json
import os
from pathlib import Path

import pytest

from scripts.product_baseline import accept, capture, fixtures, preflight


@pytest.mark.parametrize("part", fixtures.PARTS)
def test_checked_in_fixture_sets_pass(part: str) -> None:
    # when
    labels = fixtures.validate_part(part)

    # then
    expected_count = 1 if part in {"data-edges", "failures", "db-permissions"} else 2
    assert len(labels) == expected_count


@pytest.mark.parametrize(
    ("part", "mutation", "message"),
    [
        ("inventory", lambda value: value.update(schemaVersion=2), "schemaVersion"),
        ("catalog", lambda value: value["rows"].pop(), "Product rows"),
        ("http", lambda value: value["observations"].pop(), "scenario set"),
        ("access", lambda value: value["observations"].pop(), "scenario set"),
        ("data-edges", lambda value: value["cases"].pop(), "case count"),
        ("failures", lambda value: value["cases"].pop(), "case count"),
        ("db-permissions", lambda value: value["cases"].pop(), "case count"),
    ],
)
def test_malformed_fixture_is_rejected(
    tmp_path: Path, part: str, mutation: object, message: str
) -> None:
    # given
    source = fixtures.fixture_files(part, None, None)[0]
    document = fixtures.load_document(source)
    mutation(document)
    candidate = tmp_path / "candidate.json"
    candidate.write_text(json.dumps(document), encoding="utf-8")

    # when / then
    with pytest.raises(fixtures.FixtureError, match=message):
        fixtures.validate_part(part, explicit_file=str(candidate))


def test_accept_replaces_validated_fixture_atomically(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # given
    source = fixtures.FIXTURE_ROOT / "http/local-dev.json"
    destination_root = tmp_path / "accepted"
    monkeypatch.setitem(accept.DESTINATIONS, "http", destination_root)

    # when
    status = accept.main(["http", str(source)])

    # then
    accepted = destination_root / "local-dev.json"
    assert status == 0
    assert accepted.read_bytes() == source.read_bytes()
    assert accepted.stat().st_mode & 0o777 == 0o644
    assert not list(destination_root.glob(".local-dev.json.*"))


@pytest.mark.parametrize(
    ("environment", "extra", "message"),
    [
        (None, {}, "BASELINE_ENV is required"),
        ("production", {}, "Unsupported BASELINE_ENV"),
        ("local-dev", {"BASELINE_PRODUCT_DB_PORT": "5482"}, "port 5482 belongs to Pricing"),
        (
            "local-dev",
            {"BASELINE_PRODUCT_DB_TRANSPORT": "socket"},
            "BASELINE_PRODUCT_DB_TRANSPORT must be",
        ),
    ],
)
def test_preflight_rejects_invalid_target_before_commands(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
    environment: str | None,
    extra: dict[str, str],
    message: str,
) -> None:
    # given
    monkeypatch.setenv("BASELINE_PRODUCT_DB_HOST", "127.0.0.1")
    monkeypatch.setenv("BASELINE_PRODUCT_DB_PORT", "5492")
    monkeypatch.setenv("BASELINE_PRODUCT_DB_TRANSPORT", "port-forward")
    monkeypatch.delenv("BASELINE_KUBE_CONTEXT", raising=False)
    if environment is None:
        monkeypatch.delenv("BASELINE_ENV", raising=False)
    else:
        monkeypatch.setenv("BASELINE_ENV", environment)
    for name, value in extra.items():
        monkeypatch.setenv(name, value)
    monkeypatch.setattr(
        preflight,
        "command",
        lambda *_args, **_kwargs: pytest.fail("invalid target reached a command boundary"),
    )

    # when
    status = preflight.main([])

    # then
    assert status == 2
    assert message in capsys.readouterr().err


@pytest.mark.parametrize("mode", [0o644, 0o640])
def test_capture_rejects_unprotected_token_file(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, mode: int
) -> None:
    # given
    token = tmp_path / "token"
    token.write_text("header.payload.signature\n", encoding="utf-8")
    os.chmod(token, mode)
    monkeypatch.setenv("BASELINE_GATEWAY_TOKEN_FILE", str(token))

    # when / then
    with pytest.raises(capture.CaptureError, match="inaccessible to group"):
        capture.token_file()


def test_capture_reads_protected_token_without_retaining_newline(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # given
    token = tmp_path / "token"
    token.write_text("header.payload.signature\n", encoding="utf-8")
    os.chmod(token, 0o600)
    monkeypatch.setenv("BASELINE_GATEWAY_TOKEN_FILE", str(token))

    # when / then
    assert capture.token_file() == "header.payload.signature"
