"""Fast process-lifecycle tests for disposable Dashboard commands."""

from __future__ import annotations

import json
import os
import sys
import time
from copy import deepcopy
from pathlib import Path

import disposable_deployment
import pytest


class FakeHTTPResponse:
    def __init__(self, status: int, body: object) -> None:
        self.status = status
        self._body = json.dumps(body).encode()

    def read(self, _limit: int) -> bytes:
        return self._body


class FakeHTTPConnection:
    def __init__(self, response: FakeHTTPResponse) -> None:
        self.response = response
        self.requests: list[tuple[str, str, str, dict[str, str]]] = []

    def request(
        self,
        method: str,
        path: str,
        body: str,
        headers: dict[str, str],
    ) -> None:
        self.requests.append((method, path, body, headers))

    def getresponse(self) -> FakeHTTPResponse:
        return self.response


def rendered_items() -> list[dict[str, object]]:
    metadata = {"namespace": "local-dev-all", "labels": {}}
    return [
        {
            "apiVersion": "v1",
            "kind": "ConfigMap",
            "metadata": {**deepcopy(metadata), "name": "local-dev-dashboard-config"},
            "data": {
                "SERVICE_VERSION": "git-aaaaaaaaaaaa",
                "EVENT_WORKERS_ENABLED": "true",
                "KAFKA_BROKERS": "canonical-kafka:9093",
                "KAFKA_GROUP": "dashboard-service",
                "KAFKA_REGISTERED_TOPIC": "policy-registered",
                "KAFKA_CA_FILE": "/etc/dashboard/kafka/ca.crt",
            },
        },
        {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": {**deepcopy(metadata), "name": "local-dev-dashboard-api"},
            "spec": {
                "template": {
                    "metadata": {"labels": {}},
                    "spec": {
                        "containers": [
                            {
                                "image": "old",
                                "volumeMounts": [
                                    {
                                        "name": "kafka-ca",
                                        "mountPath": "/etc/dashboard/kafka",
                                        "readOnly": True,
                                    }
                                ],
                                "env": [
                                    {
                                        "name": "ELASTICSEARCH_PASSWORD",
                                        "valueFrom": {
                                            "secretKeyRef": {
                                                "name": "local-dev-dashboard-elasticsearch",
                                                "key": "password",
                                            }
                                        },
                                    },
                                    {
                                        "name": "ELASTICSEARCH_WRITER_USERNAME",
                                        "valueFrom": {
                                            "secretKeyRef": {
                                                "name": "local-dev-dashboard-elasticsearch-writer",
                                                "key": "username",
                                            }
                                        },
                                    },
                                    {
                                        "name": "ELASTICSEARCH_WRITER_PASSWORD",
                                        "valueFrom": {
                                            "secretKeyRef": {
                                                "name": "local-dev-dashboard-elasticsearch-writer",
                                                "key": "password",
                                            }
                                        },
                                    },
                                ],
                            }
                        ],
                        "volumes": [
                            {
                                "name": "kafka-ca",
                                "secret": {"secretName": "local-dev-dashboard-kafka-ca"},
                            }
                        ],
                    },
                }
            },
        },
        {
            "apiVersion": "networking.k8s.io/v1",
            "kind": "NetworkPolicy",
            "metadata": {**deepcopy(metadata), "name": "local-dev-dashboard-api"},
            "spec": {
                "ingress": [
                    {
                        "from": [
                            {
                                "namespaceSelector": {
                                    "matchLabels": {"kubernetes.io/metadata.name": "local-dev-all"}
                                }
                            }
                        ]
                    }
                ],
                "egress": [],
            },
        },
    ]


def test_render_adds_owned_dependency_reader_secret_and_exact_identity(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    items = rendered_items()
    monkeypatch.setattr(disposable_deployment.checks, "render", lambda _environment: "rendered")
    monkeypatch.setattr(
        disposable_deployment.checks,
        "validate_render",
        lambda _environment, _text: deepcopy(items),
    )
    cluster = disposable_deployment.Cluster("dashboard-disposable-test")

    _rendered, actual = disposable_deployment.render(
        cluster,
        "insurance-hub-dashboard-api:1.0.0-local.0123456789ab",
        "0123456789ab",
        "elasticsearch@example@sha256:" + "a" * 64,
    )

    deployment = disposable_deployment.checks.resource(
        actual, "Deployment", "local-dev-dashboard-api"
    )
    assert deployment["spec"]["template"]["spec"]["containers"][0]["image"].endswith("0123456789ab")
    config = disposable_deployment.checks.resource(
        actual, "ConfigMap", "local-dev-dashboard-config"
    )["data"]
    assert config["SERVICE_VERSION"] == "git-0123456789ab"
    assert config["ELASTICSEARCH_USERNAME"] == disposable_deployment.READER_USERNAME
    assert not any(name.startswith(("EVENT_", "KAFKA_")) for name in config)
    environment = deployment["spec"]["template"]["spec"]["containers"][0]["env"]
    credential_entries = [
        item
        for item in environment
        if item.get("name") in {"ELASTICSEARCH_USERNAME", "ELASTICSEARCH_PASSWORD"}
    ]
    assert credential_entries == [
        {
            "name": "ELASTICSEARCH_USERNAME",
            "valueFrom": {
                "secretKeyRef": {
                    "name": disposable_deployment.ELASTICSEARCH_NAME,
                    "key": "username",
                }
            },
        },
        {
            "name": "ELASTICSEARCH_PASSWORD",
            "valueFrom": {
                "secretKeyRef": {
                    "name": disposable_deployment.ELASTICSEARCH_NAME,
                    "key": "reader-password",
                }
            },
        },
    ]
    assert deployment["spec"]["template"]["spec"]["containers"][0]["volumeMounts"] == []
    assert deployment["spec"]["template"]["spec"]["volumes"] == []
    secret = disposable_deployment.checks.resource(
        actual, "Secret", disposable_deployment.ELASTICSEARCH_NAME
    )
    assert secret["stringData"]["username"] == disposable_deployment.READER_USERNAME
    assert secret["stringData"]["reader-password"] == disposable_deployment.READER_PASSWORD
    policy = disposable_deployment.checks.resource(
        actual, "NetworkPolicy", "local-dev-dashboard-api"
    )
    assert policy["spec"]["ingress"][0]["from"][0]["namespaceSelector"]["matchLabels"] == {
        "kubernetes.io/metadata.name": cluster.namespace
    }
    assert policy["spec"]["egress"][1]["ports"] == [{"port": 9200, "protocol": "TCP"}]


def test_event_enabled_render_uses_only_issue_290_inputs(monkeypatch: pytest.MonkeyPatch) -> None:
    items = rendered_items()
    monkeypatch.setattr(disposable_deployment.checks, "render", lambda _environment: "rendered")
    monkeypatch.setattr(
        disposable_deployment.checks,
        "validate_render",
        lambda _environment, _text: deepcopy(items),
    )
    cluster = disposable_deployment.Cluster("dashboard-disposable-test")

    _rendered, actual = disposable_deployment.render(
        cluster,
        "insurance-hub-dashboard-api:1.0.0-local.0123456789ab",
        "0123456789ab",
        "elasticsearch@example@sha256:" + "a" * 64,
        event_enabled=True,
    )

    config = disposable_deployment.checks.resource(
        actual, "ConfigMap", "local-dev-dashboard-config"
    )["data"]
    assert config["EVENT_WORKERS_ENABLED"] == "true"
    assert config["ELASTICSEARCH_INDEX"] == "policy_stats_issue_290"
    assert config["KAFKA_REGISTERED_TOPIC"] == "policy-registered-issue-290"
    deployment = disposable_deployment.checks.resource(
        actual, "Deployment", "local-dev-dashboard-api"
    )
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    environment = {item["name"]: item for item in container["env"]}
    assert environment["ELASTICSEARCH_USERNAME"]["valueFrom"]["secretKeyRef"]["name"] == (
        "local-dev-dashboard-elasticsearch-issue-290"
    )
    assert (
        environment["ELASTICSEARCH_WRITER_USERNAME"]["valueFrom"]["secretKeyRef"]["name"]
        == "local-dev-dashboard-elasticsearch-writer-issue-290"
    )
    assert container["volumeMounts"] == [
        {"name": "kafka-ca", "mountPath": "/etc/dashboard/kafka", "readOnly": True}
    ]
    policy = disposable_deployment.checks.resource(
        actual, "NetworkPolicy", "local-dev-dashboard-api"
    )
    assert policy["spec"]["egress"][-1]["ports"] == [{"port": 9093, "protocol": "TCP"}]


def test_registry_candidate_render_allows_exact_digest_pull(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    items = rendered_items()
    monkeypatch.setattr(disposable_deployment.checks, "render", lambda _environment: "rendered")
    monkeypatch.setattr(
        disposable_deployment.checks,
        "validate_render",
        lambda _environment, _text: deepcopy(items),
    )
    image = "ghcr.io/example/dashboard:1.5.0@sha256:" + "a" * 64

    _rendered, actual = disposable_deployment.render(
        disposable_deployment.Cluster("dashboard-disposable-test"),
        image,
        "0123456789ab",
        "elasticsearch@example@sha256:" + "b" * 64,
    )

    deployment = disposable_deployment.checks.resource(
        actual, "Deployment", "local-dev-dashboard-api"
    )
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    assert container["image"] == image
    assert container["imagePullPolicy"] == "IfNotPresent"


def test_run_rejects_missing_digest_qualified_dependency_before_commands(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("IMAGE_REF", "insurance-hub-dashboard-api:1.0.0-local.0123456789ab")
    monkeypatch.delenv("ELASTICSEARCH_IMAGE", raising=False)
    monkeypatch.setattr(
        disposable_deployment,
        "command",
        lambda *_args, **_kwargs: pytest.fail("command must not run"),
    )

    with pytest.raises(disposable_deployment.CheckFailed, match="digest-qualified"):
        disposable_deployment.run()


def test_qualification_oracle_has_fixed_replacement_and_query_values() -> None:
    qualification = disposable_deployment.load_qualification()

    # Given the same synthetic identity is sent twice, the second event is a replacement.
    events = qualification["events"]
    assert events[0]["expectedDocument"]["number"] == events[1]["expectedDocument"]["number"]
    assert events[1]["expectedDocument"]["totalPremium"] == 456.78
    # Then independently fixed totals reflect two documents, not three append-only records.
    assert qualification["queries"][0]["expectedResponse"]["total"] == {
        "policiesCount": 2,
        "premiumAmount": 556.78,
    }


def test_registry_candidate_requires_exact_oci_source_and_digest(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    revision = "7" * 40
    digest = "sha256:" + "8" * 64
    image = f"ghcr.io/example/dashboard:1.5.0@{digest}"
    calls: list[list[str]] = []

    def fake_command(arguments: list[str], **_kwargs: object) -> str:
        calls.append(arguments)
        if arguments[:3] == ["docker", "image", "inspect"]:
            return json.dumps(
                [
                    {
                        "Id": "sha256:" + "9" * 64,
                        "RepoDigests": [f"ghcr.io/example/dashboard@{digest}"],
                        "Config": {
                            "Labels": {
                                "org.opencontainers.image.revision": revision,
                                "org.opencontainers.image.version": "git-" + revision[:12],
                            }
                        },
                    }
                ]
            )
        return ""

    monkeypatch.setattr(disposable_deployment, "command", fake_command)

    candidate = disposable_deployment.prepare_candidate_image(image, revision)

    assert candidate.registry_digest == digest
    assert calls[0] == ["docker", "pull", image]
    assert calls[1] == ["docker", "image", "inspect", image]

    with pytest.raises(disposable_deployment.CheckFailed, match="source differs"):
        disposable_deployment.prepare_candidate_image(image, "6" * 40)


def test_kind_candidate_tag_must_resolve_to_selected_digest(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    digest = "sha256:" + "8" * 64
    image = f"ghcr.io/example/dashboard:1.5.0@{digest}"
    candidate = disposable_deployment.CandidateIdentity(
        "7" * 40,
        "git-" + "7" * 12,
        "sha256:" + "9" * 64,
        digest,
    )

    def fake_command(arguments: list[str], **_kwargs: object) -> str:
        if arguments == ["kind", "get", "nodes", "--name", disposable_deployment.KIND_CLUSTER]:
            return "kind-node\n"
        if "inspecti" in arguments:
            return json.dumps({"status": {"repoDigests": [f"ghcr.io/example/dashboard@{digest}"]}})
        return ""

    monkeypatch.setattr(disposable_deployment, "command", fake_command)

    assert disposable_deployment.prepare_kind_candidate(image, candidate) == (
        "ghcr.io/example/dashboard:1.5.0"
    )


def test_selected_deployed_rpc_must_execute_and_pass() -> None:
    passed = '{"Action":"pass","Test":"TestDeployedDirectRPC"}'
    disposable_deployment.require_go_test_passed(passed, "TestDeployedDirectRPC")

    for output in (
        '{"Action":"pass","Package":"example/internal/service"}',
        '{"Action":"pass","Test":"TestRenamedDeployedDirectRPC"}',
    ):
        with pytest.raises(disposable_deployment.CheckFailed, match="did not execute"):
            disposable_deployment.require_go_test_passed(output, "TestDeployedDirectRPC")


def test_reader_permission_denial_rejects_wrong_outcomes() -> None:
    denied = disposable_deployment.HTTPProbeResponse(
        403,
        '{"error":{"type":"security_exception"},"status":403}',
    )
    disposable_deployment.require_elasticsearch_permission_denied("reader-write", denied)

    for response in (
        disposable_deployment.HTTPProbeResponse(0, "connection refused"),
        disposable_deployment.HTTPProbeResponse(401, denied.body),
        disposable_deployment.HTTPProbeResponse(404, denied.body),
        disposable_deployment.HTTPProbeResponse(403, '{"error":{"type":"other"},"status":403}'),
    ):
        with pytest.raises(disposable_deployment.CheckFailed, match="permission denial|not JSON"):
            disposable_deployment.require_elasticsearch_permission_denied("reader-write", response)


def test_dependency_failure_requires_exact_safe_response() -> None:
    safe = '{"code":14,"message":"service unavailable","details":[]}'
    expected = disposable_deployment.HTTPProbeResponse(503, safe)
    disposable_deployment.require_http_response("dependency-total-sales", expected, 503, safe)

    for response in (
        disposable_deployment.HTTPProbeResponse(500, safe),
        disposable_deployment.HTTPProbeResponse(503, '{"message":"private dependency failure"}'),
    ):
        with pytest.raises(disposable_deployment.CheckFailed, match="response differs"):
            disposable_deployment.require_http_response(
                "dependency-total-sales", response, 503, safe
            )


def test_runtime_image_content_must_match_local_image() -> None:
    digest = "sha256:" + "a" * 64
    image = "insurance-hub-dashboard-api:1.0.0-local.0123456789ab"
    containerd = {
        "status": {
            "id": digest,
            "repoTags": [f"docker.io/library/{image}"],
        }
    }
    identity = disposable_deployment.require_matching_image_content(
        digest,
        image,
        containerd,
        f"docker.io/library/{image}@{digest}",
    )
    assert identity.local == identity.containerd == identity.pod == digest

    other = "sha256:" + "b" * 64
    for local_id, pod_id in ((other, digest), (digest, other)):
        with pytest.raises(disposable_deployment.CheckFailed, match="content differs"):
            disposable_deployment.require_matching_image_content(
                local_id,
                image,
                containerd,
                pod_id,
            )


def test_runtime_image_content_requires_exact_loaded_tag() -> None:
    digest = "sha256:" + "a" * 64
    image = "insurance-hub-dashboard-api:1.0.0-local.0123456789ab"
    with pytest.raises(disposable_deployment.CheckFailed, match="tag differs"):
        disposable_deployment.require_matching_image_content(
            digest,
            image,
            {"status": {"id": digest, "repoTags": ["docker.io/library/other:latest"]}},
            digest,
        )


def test_runtime_registry_digest_must_match_containerd_and_pod() -> None:
    digest = "sha256:" + "a" * 64
    image = "ghcr.io/example/dashboard:1.5.0"
    containerd = {
        "status": {
            "id": "sha256:" + "b" * 64,
            "repoDigests": [f"ghcr.io/example/dashboard@{digest}"],
        }
    }

    identity = disposable_deployment.require_matching_image_content(
        "sha256:" + "b" * 64,
        image,
        containerd,
        f"ghcr.io/example/dashboard@{digest}",
        digest,
    )

    assert identity.containerd == identity.pod == digest
    with pytest.raises(disposable_deployment.CheckFailed, match="Pod identity is absent"):
        disposable_deployment.require_matching_image_content(
            "sha256:" + "b" * 64,
            image,
            containerd,
            "ghcr.io/example/dashboard@sha256:" + "c" * 64,
            digest,
        )


def test_offset_parser_and_state_comparison_reject_reset() -> None:
    output = """
GROUP TOPIC PARTITION CURRENT-OFFSET LOG-END-OFFSET LAG CONSUMER-ID HOST CLIENT-ID
dashboard-service-issue-290 policy-registered-issue-290 0 3 3 0 - - -
"""
    offsets = disposable_deployment.parse_consumer_group_offsets(output)
    mapping = {"dynamic": "strict"}
    documents = [{"id": "SYN-292-001", "source": {"number": "SYN-292-001"}}]
    expected = disposable_deployment.StateSnapshot(mapping, documents, offsets)

    # When the controlled candidate resets its committed position, retention must fail.
    reset = disposable_deployment.StateSnapshot(
        mapping,
        documents,
        [disposable_deployment.OffsetSnapshot(0, 0, 3)],
    )
    with pytest.raises(disposable_deployment.CheckFailed, match="committed offsets changed"):
        disposable_deployment.require_same_state("controlled offset reset", expected, reset)


def test_query_assertion_rejects_controlled_wrong_total() -> None:
    query = {
        "name": "total-sales",
        "path": "/dashboard/totalsales",
        "request": {"saleDateFrom": "2026-01-01", "saleDateTo": "2026-02-01"},
        "expectedResponse": {"total": {"policiesCount": 2, "premiumAmount": 556.78}},
    }
    connection = FakeHTTPConnection(FakeHTTPResponse(200, query["expectedResponse"]))
    disposable_deployment.post_dashboard_query(connection, query)
    assert len(connection.requests) == 1

    # When a controlled response reports an append-only count, the fixed oracle must fail.
    wrong = FakeHTTPConnection(
        FakeHTTPResponse(200, {"total": {"policiesCount": 3, "premiumAmount": 680.23}})
    )
    with pytest.raises(disposable_deployment.CheckFailed, match="response differs"):
        disposable_deployment.post_dashboard_query(wrong, query)


def test_dashboard_state_ignores_terminating_rollout_pod() -> None:
    active = {
        "metadata": {"uid": "active"},
        "spec": {"containers": [{"image": "selected"}]},
        "status": {"containerStatuses": [{"imageID": "selected@digest", "restartCount": 0}]},
    }
    terminating = deepcopy(active)
    terminating["metadata"] = {
        "uid": "old",
        "deletionTimestamp": "2026-11-08T00:00:00Z",
    }

    class RolloutCluster:
        @staticmethod
        def json(*_arguments: str) -> dict[str, object]:
            return {"items": [terminating, active]}

    state = disposable_deployment.dashboard_pod_state(RolloutCluster())

    assert state == {
        "uid": "active",
        "image": "selected",
        "imageID": "selected@digest",
        "restartCount": 0,
    }


def assert_reaped(pid_file: Path) -> None:
    deadline = time.monotonic() + 2
    pid = int(pid_file.read_text(encoding="utf-8"))
    while time.monotonic() < deadline:
        try:
            os.kill(pid, 0)
        except ProcessLookupError:
            return
        time.sleep(0.02)
    raise AssertionError("owned disposable descendant was not reaped")


def descendant_program(pid_file: Path, *, interrupt_parent: bool) -> str:
    action = "os.kill(os.getppid(), signal.SIGINT); " if interrupt_parent else ""
    return (
        "import os, pathlib, signal, subprocess, sys, time; "
        "child=subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)']); "
        f"pathlib.Path({str(pid_file)!r}).write_text(str(child.pid)); "
        f"{action}"
        "time.sleep(60)"
    )


def test_timeout_reports_progress_and_reaps_descendant(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    pid_file = tmp_path / "timeout-child.pid"

    with pytest.raises(disposable_deployment.CheckFailed, match="deadline"):
        disposable_deployment.command(
            [sys.executable, "-c", descendant_program(pid_file, interrupt_parent=False)],
            timeout=0.3,
            heartbeat=0.1,
            teardown=2,
            activity="disposable timeout fixture",
        )

    assert "still running" in capsys.readouterr().err
    assert_reaped(pid_file)


def test_interruption_reaps_descendant(tmp_path: Path) -> None:
    pid_file = tmp_path / "interrupt-child.pid"

    with pytest.raises(KeyboardInterrupt):
        disposable_deployment.command(
            [sys.executable, "-c", descendant_program(pid_file, interrupt_parent=True)],
            timeout=5,
            heartbeat=1,
            teardown=2,
            activity="disposable interrupt fixture",
        )

    assert_reaped(pid_file)
