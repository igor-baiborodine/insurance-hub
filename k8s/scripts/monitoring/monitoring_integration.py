#!/usr/bin/env python3
"""Query a service dashboard against a disposable Prometheus metric fixture."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
import threading
import time
import urllib.parse
import urllib.request
import uuid
from collections.abc import Sequence
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

import monitoring_runtime
from monitoring_assets import AssetError, MonitoringAssets, load_assets

PROMETHEUS_IMAGE = "quay.io/prometheus/prometheus:v3.15.0-distroless"
COMMAND_TIMEOUT = 30
CLEANUP_TIMEOUT = 15
METRIC_NAME = re.compile(r"^[a-zA-Z_:][a-zA-Z0-9_:]*$")


class HarnessError(Exception):
    """A bounded monitoring fixture failure."""


def escape_label(value: str) -> str:
    return value.replace("\\", "\\\\").replace("\n", "\\n").replace('"', '\\"')


class MetricFixture:
    def __init__(self, config: dict[str, Any]) -> None:
        self.config = config
        self.scrapes = 0
        self.lock = threading.Lock()

    def sample_value(self, value: Any) -> float:
        if isinstance(value, bool):
            raise HarnessError("metric fixture values cannot be booleans")
        if isinstance(value, (int, float)):
            return value
        if not isinstance(value, dict):
            raise HarnessError(f"unsupported metric fixture value: {value!r}")
        kind = value.get("kind")
        if kind == "linear":
            return value.get("initial", 0) + self.scrapes * value.get("step", 0)
        if kind == "unix_time":
            return time.time()
        raise HarnessError(f"unsupported metric fixture value kind: {kind!r}")

    def render(self) -> bytes:
        with self.lock:
            self.scrapes += 1
            common = self.config.get("common_labels", {})
            lines = []
            for item in self.config.get("series", []):
                name = item.get("name", "")
                if not METRIC_NAME.fullmatch(name):
                    raise HarnessError(f"invalid metric fixture name: {name!r}")
                labels: dict[str, str] = {}
                if item.get("use_common_labels"):
                    labels.update(common)
                labels.update(item.get("labels", {}))
                encoded = ",".join(
                    f'{key}="{escape_label(str(value))}"' for key, value in sorted(labels.items())
                )
                suffix = f"{{{encoded}}}" if encoded else ""
                lines.append(f"{name}{suffix} {self.sample_value(item.get('value'))}")
        return ("\n".join(lines) + "\n").encode("utf-8")


def handler_for(fixture: MetricFixture):
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            if self.path != "/metrics":
                self.send_error(404)
                return
            try:
                payload = fixture.render()
            except HarnessError as error:
                self.send_error(500, str(error))
                return
            self.send_response(200)
            self.send_header("Content-Type", "text/plain; version=0.0.4")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, *_args) -> None:
            return

    return Handler


def run(arguments: Sequence[str], timeout: int = COMMAND_TIMEOUT) -> subprocess.CompletedProcess:
    try:
        result = subprocess.run(
            list(arguments),
            capture_output=True,
            text=True,
            timeout=monitoring_runtime.teardown_timeout(timeout),
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise HarnessError(f"command failed: {arguments}: {error}") from error
    if result.returncode != 0:
        raise HarnessError(
            f"command failed ({result.returncode}): {arguments}: {result.stderr.strip()}"
        )
    return result


def read_json(url: str) -> dict:
    try:
        with urllib.request.urlopen(url, timeout=5) as response:
            return json.loads(response.read())
    except Exception as error:
        raise HarnessError(f"request failed for {url}: {error}") from error


def wait_ready(port: int) -> None:
    deadline = time.monotonic() + COMMAND_TIMEOUT
    while time.monotonic() < deadline:
        try:
            response = read_json(f"http://127.0.0.1:{port}/api/v1/status/runtimeinfo")
            if response.get("status") == "success":
                return
        except HarnessError:
            time.sleep(0.25)
    raise HarnessError("disposable Prometheus did not become ready")


def port_for(container: str) -> int:
    output = run(["docker", "port", container, "9090/tcp"]).stdout.strip()
    match = re.search(r":([0-9]+)$", output)
    if match is None:
        raise HarnessError(f"cannot parse Prometheus port: {output!r}")
    return int(match.group(1))


def query(port: int, expression: str) -> list:
    encoded = urllib.parse.urlencode({"query": expression})
    response = read_json(f"http://127.0.0.1:{port}/api/v1/query?{encoded}")
    if response.get("status") != "success":
        raise HarnessError(f"Prometheus rejected dashboard query {expression!r}: {response}")
    return response.get("data", {}).get("result", [])


def dashboard_expressions(assets: MonitoringAssets) -> list[str]:
    expressions = []
    variables = assets.fixture.get("dashboard_variables", {})
    for panel in assets.dashboard.get("panels", []):
        if panel.get("datasource", {}).get("type") != "prometheus":
            continue
        for target in panel.get("targets", []):
            expression = target.get("expr")
            if expression:
                for name, value in variables.items():
                    expression = expression.replace(f"${name}", str(value))
                expressions.append(expression.replace("$__rate_interval", "1m"))
    return expressions


def cleanup(container: str) -> None:
    try:
        subprocess.run(
            ["docker", "rm", "-f", container],
            capture_output=True,
            text=True,
            timeout=monitoring_runtime.teardown_timeout(CLEANUP_TIMEOUT),
            check=False,
        )
        remaining = run(
            [
                "docker",
                "container",
                "ls",
                "-a",
                "--filter",
                f"name=^{container}$",
                "--format",
                "{{.Names}}",
            ],
            timeout=CLEANUP_TIMEOUT,
        ).stdout.strip()
        if remaining:
            raise HarnessError(f"owned container remains after cleanup: {remaining}")
    except (OSError, subprocess.TimeoutExpired) as error:
        raise HarnessError(f"could not clean up {container}: {error}") from error


def execute(assets: MonitoringAssets) -> None:
    expressions = dashboard_expressions(assets)
    minimum = assets.contract["dashboard"]["minimum_prometheus_targets"]
    if len(expressions) < minimum:
        raise HarnessError(
            f"dashboard fixture found {len(expressions)} expressions, expected at least {minimum}"
        )
    fixture = MetricFixture(assets.fixture)
    server = ThreadingHTTPServer(("0.0.0.0", 0), handler_for(fixture))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    container = f"monitoring-{assets.environment_name}-{assets.service}-{uuid.uuid4().hex[:10]}"
    primary: BaseException | None = None
    try:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            os.chmod(root, 0o755)
            rule_name = assets.alerts_path.name
            (root / rule_name).write_text(
                assets.alerts_path.read_text(encoding="utf-8"), encoding="utf-8"
            )
            (root / "prometheus.yml").write_text(
                "global:\n"
                "  scrape_interval: 1s\n"
                "  evaluation_interval: 1s\n"
                "rule_files:\n"
                f"  - /fixture/{rule_name}\n"
                "scrape_configs:\n"
                f"  - job_name: {assets.service}-fixture\n"
                "    honor_labels: true\n"
                "    static_configs:\n"
                f'      - targets: ["host.docker.internal:{server.server_port}"]\n',
                encoding="utf-8",
            )
            os.chmod(root / rule_name, 0o644)
            os.chmod(root / "prometheus.yml", 0o644)
            run(
                [
                    "docker",
                    "run",
                    "--detach",
                    "--name",
                    container,
                    "--add-host",
                    "host.docker.internal:host-gateway",
                    "--publish",
                    "127.0.0.1::9090",
                    "--mount",
                    f"type=bind,src={root},dst=/fixture,readonly",
                    PROMETHEUS_IMAGE,
                    "--config.file=/fixture/prometheus.yml",
                    "--storage.tsdb.path=/tmp/prometheus",
                ]
            )
            port = port_for(container)
            wait_ready(port)
            minimum_scrapes = assets.contract["fixture"]["minimum_scrapes"]
            deadline = time.monotonic() + 15
            while fixture.scrapes < minimum_scrapes and time.monotonic() < deadline:
                time.sleep(0.25)
            if fixture.scrapes < minimum_scrapes:
                raise HarnessError(f"Prometheus collected only {fixture.scrapes} fixture scrapes")
            for expression in expressions:
                if not query(port, expression):
                    raise HarnessError(f"dashboard query returned no data: {expression}")
            alerts = read_json(f"http://127.0.0.1:{port}/api/v1/alerts")
            active = [
                alert
                for alert in alerts.get("data", {}).get("alerts", [])
                if alert.get("state") in {"pending", "firing"}
            ]
            if active:
                raise HarnessError(f"healthy fixture has active alerts: {active}")
            print(
                f"queried {len(expressions)} dashboard expressions across "
                f"{fixture.scrapes} collected fixture scrapes; healthy alerts are inactive"
            )
    except BaseException as error:
        primary = error
        raise
    finally:
        with monitoring_runtime.teardown_activity(45):
            cleanup_errors: list[Exception] = []
            for action in (
                server.shutdown,
                server.server_close,
                lambda: thread.join(timeout=monitoring_runtime.teardown_timeout(5)),
                lambda: cleanup(container),
            ):
                try:
                    action()
                except Exception as error:
                    cleanup_errors.append(error)
            if cleanup_errors:
                if primary is not None:
                    primary.add_note(f"monitoring cleanup also failed: {cleanup_errors}")
                else:
                    raise HarnessError(f"monitoring cleanup failed: {cleanup_errors}")


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--assets-root", type=Path, required=True)
    parser.add_argument("--environment", required=True)
    parser.add_argument("--service", required=True)
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        with (
            monitoring_runtime.controlled_signals(),
            monitoring_runtime.bounded_activity("disposable monitoring integration", 300),
        ):
            execute(load_assets(args.assets_root, args.environment, args.service))
    except monitoring_runtime.Interrupted as error:
        print(f"Monitoring fixture {error}", file=sys.stderr)
        for note in getattr(error, "__notes__", ()):
            print(note, file=sys.stderr)
        return error.status
    except (
        AssetError,
        HarnessError,
        monitoring_runtime.DeadlineExceeded,
        monitoring_runtime.TeardownExceeded,
        OSError,
    ) as error:
        print(f"Monitoring fixture failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
