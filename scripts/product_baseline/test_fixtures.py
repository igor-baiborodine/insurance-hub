"""Behavior tests for Product baseline fixture validation."""

from __future__ import annotations

import json
import os
import subprocess
import sys
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


@pytest.mark.parametrize("part", ["http", "access"])
@pytest.mark.parametrize("location", ["request", "response", "nested-array"])
def test_accept_rejects_recursive_bearer_tokens_without_replacing_destination(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    part: str,
    location: str,
) -> None:
    # given
    source = fixtures.FIXTURE_ROOT / part / "local-dev.json"
    document = fixtures.load_document(source)
    observation = next(
        item for item in document["observations"] if item["response"]["status"] == 200
    )
    if location == "request":
        observation["request"]["reviewMarker"] = "Bearer synthetic-review-marker"
    elif location == "response":
        raw = json.dumps({"reviewMarker": "Bearer synthetic-review-marker"})
        observation["response"]["rawBody"] = raw
        observation["response"]["rawBodySha256"] = fixtures.hashlib.sha256(raw.encode()).hexdigest()
    else:
        document["reviewMarkers"] = [{"values": ["Bearer synthetic-review-marker"]}]
    candidate = tmp_path / f"{part}-candidate.json"
    candidate.write_text(json.dumps(document), encoding="utf-8")
    destination_root = tmp_path / "accepted"
    destination_root.mkdir()
    destination = destination_root / "local-dev.json"
    destination.write_bytes(b"preserved-destination\n")
    monkeypatch.setitem(accept.DESTINATIONS, part, destination_root)

    # when
    status = accept.main([part, str(candidate)])

    # then
    assert status == 2
    assert destination.read_bytes() == b"preserved-destination\n"
    assert not list(destination_root.glob(".local-dev.json.*"))


def test_redacted_bearer_value_remains_allowed() -> None:
    # given
    document = {"authorization": "Bearer <redacted>"}

    # when / then
    fixtures.require_no_bearer_tokens(document, Path("controlled.json"), "fixture")


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


def write_preflight_fake(path: Path) -> None:
    path.write_text(
        f"#!{sys.executable}\n"
        "import json, os, pathlib, sys\n"
        "name = pathlib.Path(sys.argv[0]).name\n"
        "with pathlib.Path(os.environ['PREFLIGHT_FAKE_LOG']).open('a') as output:\n"
        "    output.write(json.dumps([name, *sys.argv[1:]]) + '\\n')\n"
        "args = ' '.join(sys.argv[1:])\n"
        "if name == 'docker': print('24 client, 24 server')\n"
        "elif name == 'mvn': print('Apache Maven 3.9.9\\nJava version: 14.0.2')\n"
        "elif name == 'psql': print('psql (PostgreSQL) 17.6' if '--version' in sys.argv else 'product|public|product\\n4')\n"
        "elif name == 'kubectl':\n"
        "    if 'current-context' in args: print('kind-local-dev-insurance-hub')\n"
        "    elif 'auth can-i' in args: print('yes')\n"
        "    elif 'readyReplicas' in args: print('1')\n"
        "    elif 'addresses' in args: print('127.0.0.1')\n"
        "    elif '.data.username' in args: print('cHJvZHVjdA==')\n"
        "    elif '.data.password' in args: print('cGFzc3dvcmQ=')\n",
        encoding="utf-8",
    )
    path.chmod(0o755)


@pytest.mark.parametrize(
    ("overrides", "endpoint", "host_argument", "port_argument"),
    [
        ({}, "127.0.0.1:5492", "--host=127.0.0.1", "--port=5492"),
        (
            {"BASELINE_PRODUCT_DB_HOST": "127.0.0.2", "BASELINE_PRODUCT_DB_PORT": "5544"},
            "127.0.0.2:5544",
            "--host=127.0.0.2",
            "--port=5544",
        ),
    ],
)
def test_make_preflight_applies_empty_defaults_and_explicit_overrides(
    tmp_path: Path,
    overrides: dict[str, str],
    endpoint: str,
    host_argument: str,
    port_argument: str,
) -> None:
    # given
    fake_bin = tmp_path / "bin"
    fake_bin.mkdir()
    for name in ("docker", "java", "kubectl", "mvn", "pg_isready", "psql"):
        write_preflight_fake(fake_bin / name)
    log = tmp_path / "commands.jsonl"
    environment = {
        key: value for key, value in os.environ.items() if not key.startswith("BASELINE_")
    }
    environment.update(
        PATH=f"{fake_bin}:{environment['PATH']}",
        PREFLIGHT_FAKE_LOG=str(log),
    )
    arguments = [
        "make",
        "--no-print-directory",
        "product-baseline-preflight",
        "BASELINE_ENV=local-dev",
        f"PYTHON={sys.executable}",
        *[f"{name}={value}" for name, value in overrides.items()],
    ]

    # when
    result = subprocess.run(
        arguments,
        cwd=fixtures.REPOSITORY_ROOT,
        env=environment,
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )

    # then
    assert result.returncode == 0, result.stderr
    assert f"Product database endpoint: {endpoint}" in result.stdout
    commands = [json.loads(line) for line in log.read_text(encoding="utf-8").splitlines()]
    psql = next(
        command for command in commands if command[0] == "psql" and "--version" not in command
    )
    assert host_argument in psql
    assert port_argument in psql


@pytest.mark.parametrize(
    ("values", "expected_direct", "expected_gateway", "expected_after_port"),
    [
        (
            {
                "BASELINE_PRODUCT_URL": "",
                "BASELINE_GATEWAY_URL": "",
                "BASELINE_PRODUCT_DB_PORT": "",
                "BASELINE_PRODUCT_DB_AFTER_PORT": "",
            },
            "http://127.0.0.1:19081",
            "http://127.0.0.1:19082",
            "5492",
        ),
        (
            {
                "BASELINE_PRODUCT_URL": "http://127.0.0.2:29081",
                "BASELINE_GATEWAY_URL": "http://127.0.0.2:29082",
                "BASELINE_PRODUCT_DB_PORT": "5544",
                "BASELINE_PRODUCT_DB_AFTER_PORT": "",
            },
            "http://127.0.0.2:29081",
            "http://127.0.0.2:29082",
            "5544",
        ),
        (
            {
                "BASELINE_PRODUCT_URL": "http://127.0.0.2:29081",
                "BASELINE_GATEWAY_URL": "http://127.0.0.2:29082",
                "BASELINE_PRODUCT_DB_PORT": "5544",
                "BASELINE_PRODUCT_DB_AFTER_PORT": "5644",
            },
            "http://127.0.0.2:29081",
            "http://127.0.0.2:29082",
            "5644",
        ),
    ],
)
def test_capture_applies_empty_defaults_and_explicit_overrides(
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
    values: dict[str, str],
    expected_direct: str,
    expected_gateway: str,
    expected_after_port: str,
) -> None:
    # given
    for name, value in values.items():
        monkeypatch.setenv(name, value)
    requested: list[str] = []

    def fake_request(base: str, *_args: object) -> tuple[int, str, str, int]:
        requested.append(base)
        return 200, "[]", "application/json", 2

    monkeypatch.setattr(capture, "request_json", fake_request)
    monkeypatch.setattr(capture, "run_preflight", lambda *_args, **_kwargs: {})

    # when
    capture.observation("direct", "DIRECT", "/products", None, "none", require_success=True)
    capture.observation(
        "gateway", "GATEWAY", "/api/products", "token", "valid", require_success=True
    )
    _, after_port = capture.preflight_pair(tmp_path, {})

    # then
    assert requested == [expected_direct, expected_gateway]
    assert after_port == expected_after_port


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
