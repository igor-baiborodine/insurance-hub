"""Fixed-arrival preview Product pilot with retained partial evidence."""

from __future__ import annotations

import argparse
import concurrent.futures
import http.client
import json
import math
import os
import signal
import socket
import sys
import threading
import time
from collections.abc import Callable, Sequence
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

from scripts.product_qualification.core import (
    CODES,
    MAX_BODY,
    QualificationError,
    accepted_bodies,
    compare_body,
    decode_body,
    load_token,
    validate_target,
)
from scripts.product_qualification.observe import Observer, identity, pod

WARMUP_SECONDS = 60
WARMUP_RATE = 2
MEASURED_SECONDS = 300
MEASURED_RATE = 5
MAX_IN_FLIGHT = 4
DEADLINE_SECONDS = 5
OPERATIONS = ("list", *CODES)


@dataclass(frozen=True)
class Sample:
    stage: str
    index: int
    operation: str
    scheduled_at: float
    attempted: bool
    dispatched_at: float | None
    completed_at: float | None
    latency_ms: float | None
    status: int | None
    correct: bool
    error: str | None


class Interrupted(Exception):
    def __init__(self, code: int) -> None:
        self.code = code


class ReusableHTTP:
    """One persistent HTTP connection per worker thread; no request retries."""

    def __init__(self, base: str, token: str) -> None:
        parsed = urlsplit(base)
        self.host = parsed.hostname or ""
        self.port = parsed.port or 0
        self.token = token
        self.local = threading.local()
        self.connections: list[http.client.HTTPConnection] = []
        self.lock = threading.Lock()

    def request(self, path: str) -> tuple[int, bytes, float, float]:
        connection = getattr(self.local, "connection", None)
        if connection is None:
            connection = http.client.HTTPConnection(self.host, self.port, timeout=DEADLINE_SECONDS)
            self.local.connection = connection
            with self.lock:
                self.connections.append(connection)
        started = time.monotonic()
        expired = threading.Event()

        def expire() -> None:
            expired.set()
            if connection.sock is not None:
                try:
                    connection.sock.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass

        timer = threading.Timer(DEADLINE_SECONDS, expire)
        timer.start()
        try:
            connection.request(
                "GET",
                path,
                headers={
                    "Accept": "application/json",
                    "Authorization": f"Bearer {self.token}",
                },
            )
            response = connection.getresponse()
            body = response.read(MAX_BODY + 1)
            ended = time.monotonic()
            if expired.is_set() or ended - started > DEADLINE_SECONDS:
                raise QualificationError("request exceeded five-second deadline")
            if len(body) > MAX_BODY:
                raise QualificationError("oversized response body")
            return response.status, body, started, ended
        except QualificationError, OSError, http.client.HTTPException:
            connection.close()
            self.local.connection = None
            raise
        finally:
            timer.cancel()
            timer.join(timeout=0.2)

    def close(self) -> None:
        for connection in self.connections:
            connection.close()


def operation(index: int) -> tuple[str, str]:
    name = OPERATIONS[index % len(OPERATIONS)]
    return name, "/api/products" if name == "list" else f"/api/products/{name}"


def nearest_rank(values: list[float], percentile: float) -> float:
    if not values:
        raise QualificationError("missing latency samples")
    return sorted(values)[max(0, math.ceil(percentile * len(values)) - 1)]


def summarize(samples: list[Sample], count: int, duration: int) -> dict[str, Any]:
    result: dict[str, Any] = {
        "planned": count,
        "scheduled": len(samples),
        "attempted": sum(sample.attempted for sample in samples),
        "completed": sum(sample.completed_at is not None for sample in samples),
        "unserved": sum(sample.error == "unserved" for sample in samples),
        "achieved_rate": sum(sample.correct for sample in samples) / duration,
        "operations": {},
    }
    for name in OPERATIONS:
        values = [
            sample.latency_ms
            for sample in samples
            if sample.operation == name and sample.correct and sample.latency_ms is not None
        ]
        if values:
            result["operations"][name] = {
                "correct": len(values),
                "median_ms": nearest_rank(values, 0.5),
                "p95_ms": nearest_rank(values, 0.95),
                "p99_ms": nearest_rank(values, 0.99),
                "max_ms": max(values),
            }
    return result


def assess(samples: list[Sample], expected_count: int, duration: int) -> dict[str, Any]:
    summary = summarize(samples, expected_count, duration)
    if (
        len(samples) != expected_count
        or summary["attempted"] != expected_count
        or summary["completed"] != expected_count
        or any(not sample.correct for sample in samples)
    ):
        raise QualificationError("incomplete or incorrect measured requests")
    for name in OPERATIONS:
        group = summary["operations"].get(name)
        if (
            not group
            or group["correct"] != expected_count // len(OPERATIONS)
            or group["p95_ms"] > 500
            or group["p99_ms"] > 1000
        ):
            raise QualificationError(f"{name}: latency or count limit failed")
    return summary


def run_stage(
    name: str,
    seconds: int,
    rate: int,
    request: Callable[[str], tuple[int, bytes, float, float]],
    accepted: dict[str, Any],
    samples: list[Sample],
    watch: Callable[[], None],
    *,
    clock: Callable[[], float] = time.monotonic,
    pause: Callable[[float], None] = time.sleep,
) -> None:
    count = seconds * rate
    start = clock()
    in_flight: dict[concurrent.futures.Future, tuple[int, str, float]] = {}
    with concurrent.futures.ThreadPoolExecutor(max_workers=MAX_IN_FLIGHT) as pool:

        def collect(done: set[concurrent.futures.Future]) -> None:
            for future in done:
                index, op, scheduled = in_flight.pop(future)
                try:
                    status, body, dispatched, completed = future.result()
                    elapsed = (completed - dispatched) * 1000
                    try:
                        if status != 200:
                            raise QualificationError("wrong HTTP status")
                        compare_body(accepted[op], decode_body(body), op)
                        sample = Sample(
                            name,
                            index,
                            op,
                            scheduled,
                            True,
                            dispatched,
                            completed,
                            elapsed,
                            status,
                            elapsed <= DEADLINE_SECONDS * 1000,
                            None if elapsed <= DEADLINE_SECONDS * 1000 else "deadline",
                        )
                    except QualificationError:
                        sample = Sample(
                            name,
                            index,
                            op,
                            scheduled,
                            True,
                            dispatched,
                            completed,
                            elapsed,
                            status,
                            False,
                            "status-or-body",
                        )
                except QualificationError, OSError, http.client.HTTPException, ValueError:
                    sample = Sample(
                        name,
                        index,
                        op,
                        scheduled,
                        True,
                        None,
                        clock(),
                        None,
                        None,
                        False,
                        "request-or-body",
                    )
                samples.append(sample)
                if not sample.correct:
                    raise QualificationError(f"{name}/{op}: incorrect response or timeout")

        try:
            for index in range(count):
                watch()
                due = start + index / rate
                remaining = due - clock()
                if remaining > 0:
                    pause(remaining)
                done = {future for future in in_flight if future.done()}
                collect(done)
                op, path = operation(index)
                if clock() - due >= 1 / rate or len(in_flight) >= MAX_IN_FLIGHT:
                    samples.append(
                        Sample(
                            name, index, op, due, False, None, None, None, None, False, "unserved"
                        )
                    )
                    raise QualificationError(f"{name}/{op}: scheduled request unserved")
                future = pool.submit(request, path)
                in_flight[future] = (index, op, due)
                if index and index % (30 * rate) == 0:
                    print(
                        f"product-qualification: {name} scheduled {index}/{count}", file=sys.stderr
                    )
            while in_flight:
                watch()
                done, _ = concurrent.futures.wait(
                    in_flight, timeout=1, return_when=concurrent.futures.FIRST_COMPLETED
                )
                collect(done)
        except QualificationError, Interrupted:
            for index, op, scheduled in in_flight.values():
                samples.append(
                    Sample(
                        name,
                        index,
                        op,
                        scheduled,
                        True,
                        None,
                        None,
                        None,
                        None,
                        False,
                        "aborted-pending",
                    )
                )
            raise


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--context", required=True)
    parser.add_argument("--namespace", required=True)
    parser.add_argument("--phase", choices=("java", "go"), required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--image", required=True)
    parser.add_argument("--preview-url", required=True)
    parser.add_argument("--token-file", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--prometheus-url", required=True)
    parser.add_argument("--established-url", required=True)
    parser.add_argument("--gateway-pod", required=True)
    parser.add_argument("--product-pod", required=True)
    parser.add_argument("--database-pod", required=True)
    args = parser.parse_args(argv)
    if args.context != "qa-insurance-hub" or args.namespace != "qa-svc":
        print("product-qualification: wrong QA target", file=sys.stderr)
        return 1
    try:
        validate_target(args.preview_url)
        validate_target(args.prometheus_url)
        validate_target(args.established_url)
        if args.output.exists() or not args.revision or not args.image:
            raise QualificationError("new output path and phase identities are required")
        token = load_token(args.token_file)
        accepted = accepted_bodies(
            Path("legacy/product-service/src/test/resources/product-read-baseline/http/qa.json")
        )
        gateway = identity(pod(args.gateway_pod, "qa-svc"))
        product = identity(pod(args.product_pod, "qa-svc"))
        database = identity(pod(args.database_pod, "qa-data"))
    except QualificationError, OSError, ValueError, KeyError, TypeError:
        print("product-qualification: invalid measurement inputs", file=sys.stderr)
        return 1

    evidence: dict[str, Any] = {
        "context": args.context,
        "namespace": args.namespace,
        "phase": args.phase,
        "revision": args.revision,
        "image": args.image,
        "started_at_unix": time.time(),
        "pods": {"gateway": gateway, "product": product, "database": database},
        "profile": {
            "warmup_seconds": WARMUP_SECONDS,
            "warmup_rate": WARMUP_RATE,
            "measured_seconds": MEASURED_SECONDS,
            "measured_rate": MEASURED_RATE,
            "max_in_flight": MAX_IN_FLIGHT,
            "deadline_seconds": DEADLINE_SECONDS,
            "percentile": "nearest-rank",
        },
        "warmup": [],
        "measured": [],
        "status": "incomplete",
    }
    client = ReusableHTTP(args.preview_url, token)
    observer = Observer(
        args.prometheus_url,
        args.established_url,
        token,
        accepted["list"],
        gateway,
        product,
        database,
    )
    code = 1
    output_failed = False
    prior_int = signal.getsignal(signal.SIGINT)
    prior_term = signal.getsignal(signal.SIGTERM)

    def interrupted(signum, _frame) -> None:
        raise Interrupted(130 if signum == signal.SIGINT else 143)

    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGTERM, interrupted)
    try:
        print("product-qualification: warm-up started", file=sys.stderr)
        run_stage(
            "warmup",
            WARMUP_SECONDS,
            WARMUP_RATE,
            client.request,
            accepted,
            evidence["warmup"],
            lambda: None,
        )
        print("product-qualification: measured phase started", file=sys.stderr)
        observer.start()
        run_stage(
            "measured",
            MEASURED_SECONDS,
            MEASURED_RATE,
            client.request,
            accepted,
            evidence["measured"],
            observer.check,
        )
        observer.check()
        observer.close()
        observer.check()
        observer.records.append(observer.snapshot())
        evidence["summary"] = assess(evidence["measured"], 1500, MEASURED_SECONDS)
        evidence["status"] = "passed"
        code = 0
    except Interrupted as error:
        evidence["failure"] = "interrupted"
        code = error.code
    except QualificationError as error:
        evidence["failure"] = str(error)
    finally:
        observer.close()
        evidence["observations"] = observer.records
        if observer.failure:
            evidence["failure"] = observer.failure
            evidence["status"] = "incomplete"
            code = 1 if code == 0 else code
        client.close()
        signal.signal(signal.SIGINT, prior_int)
        signal.signal(signal.SIGTERM, prior_term)
        evidence["warmup"] = [asdict(sample) for sample in evidence["warmup"]]
        evidence["measured"] = [asdict(sample) for sample in evidence["measured"]]
        evidence["summary"] = summarize(
            [Sample(**sample) for sample in evidence["measured"]], 1500, MEASURED_SECONDS
        )
        evidence["finished_at_unix"] = time.time()
        try:
            descriptor = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, "w", encoding="utf-8") as output:
                output.write(json.dumps(evidence, indent=2) + "\n")
        except OSError:
            print("product-qualification: evidence output failed", file=sys.stderr)
            output_failed = True
    print(f"product-qualification: {evidence['status']}", file=sys.stderr)
    return 1 if output_failed else code


if __name__ == "__main__":
    raise SystemExit(main())
