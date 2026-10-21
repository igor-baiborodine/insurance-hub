"""Focused QA Product qualification contract checks."""

from __future__ import annotations

import http.server
import json
import threading
import time
from copy import deepcopy

import pytest

from scripts.product_qualification import core
from scripts.product_qualification.core import (
    CODES,
    FIXTURE,
    QualificationError,
    accepted_bodies,
    by_code,
    check_reads,
    compare_body,
    decode_body,
    load_token,
    main,
    validate_target,
)


def fixture_responses() -> tuple[dict[str, object], dict[str, bytes]]:
    accepted = accepted_bodies(FIXTURE)
    source = json.loads(FIXTURE.read_text(encoding="utf-8"))
    responses = {}
    for observation in source["observations"]:
        if observation["boundary"] == "gateway":
            responses[observation["request"]["path"]] = observation["response"]["rawBody"].encode()
    return accepted, responses


def test_successful_case_set_and_negative_paths() -> None:
    accepted, responses = fixture_responses()
    calls = []

    def request(path: str, token: str | None) -> tuple[int, bytes]:
        calls.append((path, token))
        if not token:
            return 401, b""
        if path in responses:
            return 200, responses[path]
        return 404, json.dumps(
            {
                "message": "Page Not Found",
                "_links": {"self": {"href": path, "templated": False}},
            }
        ).encode()

    observed = check_reads(accepted, request, "synthetic-token")
    assert set(observed) == {"list", *CODES}
    assert len(calls) == 8
    assert calls[-3:] == [
        ("/api/products/MISSING", "synthetic-token"),
        ("/api/products/car", "synthetic-token"),
        ("/api/products", None),
    ]


@pytest.mark.parametrize(
    "change", ["empty", "duplicate", "missing", "extra", "scale", "presence", "order"]
)
def test_catalog_drift_is_rejected(change: str) -> None:
    accepted = accepted_bodies(FIXTURE)
    actual = deepcopy(accepted["list"])
    assert isinstance(actual, list)
    if change == "empty":
        actual.clear()
    elif change == "duplicate":
        actual.append(deepcopy(actual[0]))
    elif change == "missing":
        actual.pop()
    elif change == "extra":
        actual.append({"code": "EXTRA"})
    elif change == "scale":
        actual[1]["covers"][0]["sumInsured"] = decode_body(b"200000.0")
    elif change == "presence":
        actual[1]["covers"][0].pop("sumInsured")
    else:
        actual[1]["covers"].reverse()
    with pytest.raises(QualificationError):
        compare_body(accepted["list"], actual, "list")


def test_top_level_order_is_irrelevant_but_codes_must_be_unique() -> None:
    accepted = accepted_bodies(FIXTURE)["list"]
    compare_body(accepted, list(reversed(accepted)), "list")
    with pytest.raises(QualificationError, match="duplicate"):
        by_code([accepted[0], accepted[0]])


def test_bad_response_and_fallback_are_rejected() -> None:
    accepted, responses = fixture_responses()

    def request(path: str, token: str | None) -> tuple[int, bytes]:
        if path == "/api/products":
            return 200, b"[]"
        return 200, responses.get(path, b"{}")

    with pytest.raises(QualificationError):
        check_reads(accepted, request, "synthetic-token")
    with pytest.raises(QualificationError, match="invalid JSON"):
        decode_body(b"{")


@pytest.mark.parametrize(
    "url",
    [
        "https://127.0.0.1:18080",
        "http://qa-gateway:80",
        "http://127.0.0.1:18080/api",
        "http://127.0.0.1:18080?x=1",
    ],
)
def test_non_forward_targets_are_rejected(url: str) -> None:
    with pytest.raises(QualificationError):
        validate_target(url)


def test_wrong_context_fails_before_token_read(tmp_path, capsys) -> None:
    output = tmp_path / "result.json"
    result = main(
        [
            "--context",
            "wrong",
            "--namespace",
            "qa-svc",
            "--phase",
            "java",
            "--revision",
            "abc",
            "--image",
            "sha256:abc",
            "--preview-url",
            "http://127.0.0.1:18080",
            "--token-file",
            str(tmp_path / "missing-token"),
            "--output",
            str(output),
        ]
    )
    assert result == 1
    assert not output.exists()
    assert "wrong" not in capsys.readouterr().err


def test_gateway_token_file_must_be_private(tmp_path) -> None:
    token_file = tmp_path / "token"
    token_file.write_text("synthetic-secret", encoding="utf-8")
    token_file.chmod(0o644)
    with pytest.raises(QualificationError, match="private"):
        load_token(token_file)
    token_file.chmod(0o600)
    assert load_token(token_file) == "synthetic-secret"


def test_phase_output_is_secret_free_and_parity_is_sensitive(tmp_path, monkeypatch, capsys) -> None:
    _, responses = fixture_responses()

    def request(_base: str, token: str | None, path: str) -> tuple[int, bytes]:
        if not token:
            return 401, b""
        if path in responses:
            return 200, responses[path]
        return 404, json.dumps(
            {
                "message": "Page Not Found",
                "_links": {"self": {"href": path, "templated": False}},
            }
        ).encode()

    monkeypatch.setattr(core, "http_request", request)
    token_file = tmp_path / "token"
    token_file.write_text("synthetic-secret-token", encoding="utf-8")
    token_file.chmod(0o600)
    baseline = tmp_path / "java.json"
    common = [
        "--context",
        "qa-insurance-hub",
        "--namespace",
        "qa-svc",
        "--revision",
        "abc",
        "--image",
        "sha256:abc",
        "--preview-url",
        "http://127.0.0.1:18080",
        "--token-file",
        str(token_file),
    ]
    assert main([*common, "--phase", "java", "--output", str(baseline)]) == 0
    assert "synthetic-secret-token" not in baseline.read_text(encoding="utf-8")
    assert "synthetic-secret-token" not in capsys.readouterr().out
    selected = tmp_path / "go.json"
    assert (
        main(
            [
                *common,
                "--phase",
                "go",
                "--output",
                str(selected),
                "--compare-to",
                str(baseline),
            ]
        )
        == 0
    )
    assert set(json.loads(selected.read_text(encoding="utf-8"))["bodies"]) == {"list", *CODES}
    earlier = json.loads(baseline.read_text(encoding="utf-8"))
    earlier["bodies"]["FAI"] = earlier["bodies"]["FAI"].replace("200000", "200000.0", 1)
    baseline.write_text(json.dumps(earlier), encoding="utf-8")
    failed = tmp_path / "failed.json"
    assert (
        main(
            [
                *common,
                "--phase",
                "go",
                "--output",
                str(failed),
                "--compare-to",
                str(baseline),
            ]
        )
        == 1
    )
    assert not failed.exists()


def test_bounded_http_does_not_follow_redirect(monkeypatch) -> None:
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            if self.path == "/slow":
                time.sleep(0.3)
            try:
                self.send_response(302 if self.path == "/redirect" else 200)
                self.send_header(
                    "Location", "http://example.invalid/" if self.path == "/redirect" else ""
                )
                self.send_header("Content-Length", "2")
                self.end_headers()
                self.wfile.write(b"{}")
            except BrokenPipeError, ConnectionResetError:
                pass

        def log_message(self, _format, *_args) -> None:
            pass

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    monkeypatch.setattr(core, "REQUEST_DEADLINE", 0.1)
    base = f"http://127.0.0.1:{server.server_port}"
    try:
        assert core.http_request(base, "synthetic", "/redirect")[0] == 302
        started = time.monotonic()
        with pytest.raises(QualificationError):
            core.http_request(base, "synthetic", "/slow")
        assert time.monotonic() - started < 0.25
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=1)
