"""Fixed-arrival and observer decisions without a live QA target."""

from __future__ import annotations

import concurrent.futures
import http.server
import json
import os
import threading
import time

import pytest

from scripts.product_qualification import measure, observe
from scripts.product_qualification.core import QualificationError, accepted_bodies


class FakeTime:
    now = 0.0

    def clock(self) -> float:
        return self.now

    def pause(self, seconds: float) -> None:
        self.now += seconds


class ImmediatePool:
    def __init__(self, max_workers: int) -> None:
        assert max_workers == 4

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return False

    def submit(self, function, *args):
        future = concurrent.futures.Future()
        future.set_result(function(*args))
        return future


class StalledPool(ImmediatePool):
    def submit(self, _function, *_args):
        return concurrent.futures.Future()


def test_fixed_arrivals_and_operation_mix(monkeypatch) -> None:
    # given
    fake = FakeTime()
    fixture_path = measure.Path(
        "legacy/product-service/src/test/resources/product-read-baseline/http/qa.json"
    )
    accepted = accepted_bodies(fixture_path)
    bodies = {
        item["scenarioId"]: item["response"]["rawBody"].encode()
        for item in json.loads(fixture_path.read_text(encoding="utf-8"))["observations"]
    }
    monkeypatch.setattr(measure.concurrent.futures, "ThreadPoolExecutor", ImmediatePool)
    samples = []
    paths = []

    def request(path: str):
        paths.append(path)
        key = "list" if path == "/api/products" else path.rsplit("/", 1)[-1]
        scenario = "HTTP-GATEWAY-LIST-001" if key == "list" else f"HTTP-GATEWAY-GET-{key}-001"
        return 200, bodies[scenario], fake.clock(), fake.clock() + 0.1

    # when
    measure.run_stage(
        "measured",
        2,
        5,
        request,
        accepted,
        samples,
        lambda: None,
        clock=fake.clock,
        pause=fake.pause,
    )

    # then
    assert len(samples) == 10
    assert [sample.scheduled_at for sample in sorted(samples, key=lambda item: item.index)] == [
        index / 5 for index in range(10)
    ]
    assert (
        paths
        == [
            "/api/products",
            "/api/products/CAR",
            "/api/products/FAI",
            "/api/products/HSI",
            "/api/products/TRI",
        ]
        * 2
    )
    assert all(sample.correct for sample in samples)


def test_four_request_cap_causes_unserved_abort(monkeypatch) -> None:
    fake = FakeTime()
    monkeypatch.setattr(measure.concurrent.futures, "ThreadPoolExecutor", StalledPool)
    samples = []
    with pytest.raises(QualificationError, match="unserved"):
        measure.run_stage(
            "measured",
            2,
            5,
            lambda _path: None,
            {},
            samples,
            lambda: None,
            clock=fake.clock,
            pause=fake.pause,
        )
    assert len(samples) == 5
    assert {sample.index for sample in samples} == set(range(5))
    assert next(sample for sample in samples if sample.index == 4).error == "unserved"
    assert sum(sample.error == "aborted-pending" for sample in samples) == 4


def test_nearest_rank_and_failed_sample_limits() -> None:
    assert measure.nearest_rank([1, 2, 3, 4], 0.5) == 2
    assert measure.nearest_rank([1, 2, 3, 4], 0.95) == 4
    samples = [
        measure.Sample(
            "measured",
            index,
            measure.OPERATIONS[index % 5],
            float(index),
            True,
            float(index),
            float(index) + 0.1,
            100,
            200,
            True,
            None,
        )
        for index in range(1500)
    ]
    assert measure.assess(samples, 1500, 300)["operations"]["list"]["p99_ms"] == 100
    samples[-1] = measure.Sample(
        "measured", 1499, "TRI", 1499, False, None, None, None, None, False, "unserved"
    )
    with pytest.raises(QualificationError):
        measure.assess(samples, 1500, 300)
    for index in (1484, 1489, 1494, 1499):
        samples[index] = measure.Sample(
            "measured", index, "TRI", index, True, index, index + 1.001, 1001, 200, True, None
        )
    with pytest.raises(QualificationError, match="latency"):
        measure.assess(samples, 1500, 300)


def test_resource_or_control_failure_aborts(monkeypatch) -> None:
    target = {
        "pod": "qa-product-1",
        "namespace": "qa-svc",
        "uid": "one",
        "container": "product",
        "restarts": 0,
        "cpu_limit_cores": 0.3,
        "memory_limit_bytes": 1000,
    }
    database = dict(target, pod="qa-postgres-product-1", namespace="qa-data")
    monkeypatch.setattr(observe, "pod", lambda name, _namespace: {"name": name})
    monkeypatch.setattr(
        observe, "identity", lambda item: database if item["name"] == database["pod"] else target
    )
    monkeypatch.setattr(
        observe,
        "usage",
        lambda *_args: {"cpu_fraction_of_limit": 0.81, "memory_fraction_of_limit": 0.1},
    )
    watcher = observe.Observer(
        "http://127.0.0.1:9090",
        "http://127.0.0.1:18081",
        "synthetic-secret",
        [],
        target,
        target,
        database,
    )
    with pytest.raises(QualificationError, match="80%"):
        watcher.snapshot()
    monkeypatch.setattr(
        observe,
        "usage",
        lambda *_args: {"cpu_fraction_of_limit": 0.2, "memory_fraction_of_limit": 0.2},
    )
    monkeypatch.setattr(observe, "http_request", lambda *_args: (500, b"{}"))
    with pytest.raises(QualificationError, match="established"):
        watcher.snapshot()


@pytest.mark.parametrize("interrupted,expected_code", [(False, 1), (True, 130)])
def test_partial_measurement_retains_secret_free_evidence_and_cleans_up(
    tmp_path, monkeypatch, interrupted: bool, expected_code: int
) -> None:
    # given
    token = tmp_path / "token"
    token.write_text("synthetic-secret-token", encoding="utf-8")
    token.chmod(0o600)
    output = tmp_path / "partial.json"
    target = {
        "pod": "qa-product-1",
        "namespace": "qa-svc",
        "uid": "one",
        "container": "product",
        "restarts": 0,
        "cpu_limit_cores": 0.3,
        "memory_limit_bytes": 1000,
    }
    monkeypatch.setattr(measure, "pod", lambda *_args: {})
    monkeypatch.setattr(measure, "identity", lambda _value: target)
    closed = []

    class FakeHTTP:
        def __init__(self, _url: str, _token: str) -> None:
            pass

        def request(self, _path: str):
            raise AssertionError("fake stage must not dispatch")

        def close(self) -> None:
            closed.append("http")

    class FakeObserver:
        def __init__(self, *_args) -> None:
            self.records = []
            self.failure = None

        def close(self) -> None:
            closed.append("observer")

    def stage(name, _seconds, _rate, _request, _accepted, samples, _watch) -> None:
        samples.append(measure.Sample(name, 0, "list", 0, True, 0, 0.1, 100, 200, True, None))
        if interrupted:
            raise measure.Interrupted(130)
        raise QualificationError("synthetic abort")

    monkeypatch.setattr(measure, "ReusableHTTP", FakeHTTP)
    monkeypatch.setattr(measure, "Observer", FakeObserver)
    monkeypatch.setattr(measure, "run_stage", stage)
    args = [
        "--context",
        "qa-insurance-hub",
        "--namespace",
        "qa-svc",
        "--phase",
        "java",
        "--revision",
        "main@sha1:" + "a" * 40,
        "--image",
        "sha256:abc",
        "--preview-url",
        "http://127.0.0.1:18080",
        "--token-file",
        str(token),
        "--output",
        str(output),
        "--prometheus-url",
        "http://127.0.0.1:19091",
        "--established-url",
        "http://127.0.0.1:18081",
        "--gateway-pod",
        "qa-gateway-1",
        "--product-pod",
        "qa-product-1",
        "--database-pod",
        "qa-postgres-product-1",
    ]

    # when
    code = measure.main(args)

    # then
    assert code == expected_code
    evidence = json.loads(output.read_text(encoding="utf-8"))
    assert evidence["status"] == "incomplete"
    assert len(evidence["warmup"]) == 1
    assert "synthetic-secret-token" not in output.read_text(encoding="utf-8")
    assert os.stat(output).st_mode & 0o777 == 0o600
    assert closed == ["observer", "http"]


def test_reusable_http_connection_and_hard_deadline(monkeypatch) -> None:
    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def do_GET(self) -> None:
            if self.path == "/slow":
                time.sleep(0.3)
            try:
                self.send_response(200)
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
    monkeypatch.setattr(measure, "DEADLINE_SECONDS", 0.1)
    client = measure.ReusableHTTP(f"http://127.0.0.1:{server.server_port}", "synthetic")
    try:
        assert client.request("/fast")[0] == 200
        assert client.request("/fast")[0] == 200
        assert len(client.connections) == 1
        started = time.monotonic()
        with pytest.raises((QualificationError, OSError)):
            client.request("/slow")
        assert time.monotonic() - started < 0.25
    finally:
        client.close()
        server.shutdown()
        server.server_close()
        thread.join(timeout=1)


def test_wrong_measurement_context_stops_before_qa_contact(tmp_path, monkeypatch) -> None:
    monkeypatch.setattr(measure, "pod", lambda *_args: pytest.fail("unexpected QA contact"))
    output = tmp_path / "wrong.json"
    assert (
        measure.main(
            [
                "--context",
                "local-dev",
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
                str(tmp_path / "missing"),
                "--output",
                str(output),
                "--prometheus-url",
                "http://127.0.0.1:19091",
                "--established-url",
                "http://127.0.0.1:18081",
                "--gateway-pod",
                "qa-gateway-1",
                "--product-pod",
                "qa-product-1",
                "--database-pod",
                "qa-postgres-product-1",
            ]
        )
        == 1
    )
    assert not output.exists()
