#!/usr/bin/env python3
"""Exercise fixture administration against an owned security-enabled Elasticsearch node."""

from __future__ import annotations

import os
import re
import secrets
import sys
import time
from pathlib import Path

ROOT = Path(os.environ.get("INSURANCE_HUB_ROOT", Path(__file__).resolve().parents[3])).resolve()
sys.path.insert(0, str(ROOT))

import fixture_administration as administration  # noqa: E402
from scripts.python.process_runner import CommandError, run_command  # noqa: E402


class IntegrationError(RuntimeError):
    """The disposable Elasticsearch permission boundary differed."""


def require(condition: object, message: str) -> None:
    if not condition:
        raise IntegrationError(message)


def command(
    arguments: list[str],
    *,
    environment: dict[str, str],
    timeout: int = 60,
) -> str:
    result = run_command(
        arguments,
        cwd=ROOT,
        env=environment,
        timeout_seconds=timeout,
        teardown_seconds=15,
        activity="Policy Search fixture permission integration",
    )
    return result.stdout.strip()


def main() -> int:
    docker = os.environ.get("DOCKER", "docker")
    image = os.environ.get("ELASTICSEARCH_IMAGE", "")
    require(image, "ELASTICSEARCH_IMAGE is required")
    password = secrets.token_urlsafe(32)
    name = f"policy-search-fixture-{os.getpid()}-{secrets.token_hex(4)}"
    environment = administration.safe_environment()
    environment["ELASTIC_PASSWORD"] = password
    created = False
    try:
        command(
            [
                docker,
                "run",
                "--detach",
                "--name",
                name,
                "--publish",
                "127.0.0.1::9200",
                "--env",
                "discovery.type=single-node",
                "--env",
                "xpack.security.enabled=true",
                "--env",
                "xpack.security.http.ssl.enabled=false",
                "--env",
                "ELASTIC_PASSWORD",
                "--env",
                "ES_JAVA_OPTS=-Xms512m -Xmx512m",
                image,
            ],
            environment=environment,
            timeout=180,
        )
        created = True
        port_value = command([docker, "port", name, "9200/tcp"], environment=environment)
        require(
            bool(re.fullmatch(r"127\.0\.0\.1:[0-9]+", port_value)),
            "unexpected Elasticsearch port binding",
        )
        port = int(port_value.rsplit(":", 1)[1])
        admin = administration.ElasticsearchClient(port, "elastic", password)
        started = time.monotonic()
        heartbeat = started + 30
        while time.monotonic() - started < 90:
            try:
                if admin.request("GET", "/")[0] == 200:
                    break
            except administration.AdministrationError:
                pass
            now = time.monotonic()
            if now >= heartbeat:
                print(
                    "policy-search-fixture-integration: waiting for Elasticsearch "
                    f"after {now - started:.0f}s",
                    file=sys.stderr,
                    flush=True,
                )
                heartbeat = now + 30
            time.sleep(1)
        else:
            raise IntegrationError("disposable Elasticsearch did not become ready")
        fixture = administration.load_fixture()
        reader_password = secrets.token_urlsafe(32)
        administration.setup_fixture(admin, fixture, reader_password)
        administration.setup_fixture(admin, fixture, reader_password)
        reader = administration.ElasticsearchClient(
            port, "go_policy_search_reader", reader_password
        )
        administration.verify_fixture(reader, fixture)
        for method, path, body, action in (
            (
                "PUT",
                f"/{fixture.index}/_doc/denied-write",
                {"number": "DENIED", "policyHolder": "Denied"},
                "document write",
            ),
            (
                "PUT",
                f"/{fixture.index}/_mapping",
                {"properties": {"denied": {"type": "keyword"}}},
                "mapping write",
            ),
            ("GET", "/policy-views/_search", None, "other-index read"),
            (
                "PUT",
                "/_security/role/denied-role",
                {"cluster": ["all"]},
                "security administration",
            ),
        ):
            status, _ = reader.request(method, path, body)
            require(status == 403, f"reader {action} was not denied")
        event_password = secrets.token_urlsafe(32)
        administration.setup_event_access(admin, fixture, event_password)
        administration.setup_event_access(admin, fixture, event_password)
        writer = administration.ElasticsearchClient(
            port, administration.EVENT_WRITER, event_password
        )
        administration.verify_event_access(writer, fixture)
        status, _ = writer.request(
            "PUT",
            f"/{fixture.index}/_doc/SYN-230-PERMISSION-CHECK",
            {
                "number": "SYN-230-PERMISSION-CHECK",
                "policyHolder": "Synthetic Permission Check",
            },
        )
        require(status in {200, 201}, "event writer document replacement was denied")
        for method, path, body, action in (
            (
                "GET",
                f"/{fixture.index}/_doc/SYN-230-PERMISSION-CHECK",
                None,
                "document read",
            ),
            ("PUT", "/policy-views-issue-229-other", {}, "other-index create"),
            (
                "DELETE",
                f"/{fixture.index}/_doc/SYN-230-PERMISSION-CHECK",
                None,
                "document delete",
            ),
        ):
            status, _ = writer.request(method, path, body)
            require(status == 403, f"event writer {action} was not denied")
        status, _ = admin.request(
            "DELETE", f"/{fixture.index}/_doc/SYN-230-PERMISSION-CHECK?refresh=true"
        )
        require(status == 200, "admin could not clean event permission document")
        administration.retire_event_access(admin)
        administration.verify_fixture(reader, fixture)
        administration.retire_fixture(admin, fixture)
        status, _ = admin.request("HEAD", f"/{fixture.index}")
        require(status == 404, "fixture retirement left the index present")
        print(
            "policy-search-fixture-integration: reader and event-writer permissions, "
            "idempotence, denials, retirement and cleanup passed"
        )
        return 0
    finally:
        if created:
            previous_failure = sys.exc_info()[0] is not None
            try:
                command(
                    [docker, "rm", "--force", name],
                    environment=environment,
                    timeout=30,
                )
            except CommandError:
                if not previous_failure:
                    raise IntegrationError("disposable Elasticsearch cleanup failed") from None
                print(
                    "policy-search-fixture-integration: cleanup failed",
                    file=sys.stderr,
                )


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (
        administration.AdministrationError,
        CommandError,
        IntegrationError,
        OSError,
        ValueError,
    ) as error:
        print(f"policy-search-fixture-integration: {error}", file=sys.stderr)
        raise SystemExit(1) from error
