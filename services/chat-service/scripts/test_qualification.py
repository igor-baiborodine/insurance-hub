"""Focused fake-boundary tests for private Chat qualification ownership."""

from __future__ import annotations

import argparse
import json
import urllib.error
from copy import deepcopy
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch

import pytest
import qualification

from scripts.go.service.tooling_contracts import PortForwardSpec
from scripts.go.service.tooling_process import CommandError
from scripts.go.service.tooling_resources import ResourceError


def arguments(**overrides: object) -> argparse.Namespace:
    values: dict[str, object] = {
        "action": "run",
        "environment": "local-dev",
        "context": "kind-local-dev-insurance-hub",
        "namespace": "local-dev-all",
        "service": "local-dev-chat-api",
        "source_revision": "a" * 40,
        "image_digest": f"sha256:{'b' * 64}",
        "confirm": "run-local-dev-chat-private-qualification",
        "chat_port": 18080,
        "nginx_port": 18081,
        "nginx_image": qualification.NGINX_IMAGE,
        "kubectl": "kubectl",
        "docker": "docker",
        "make": "make",
        "socat": "socat",
    }
    values.update(overrides)
    return argparse.Namespace(**values)


def documents(target: qualification.Target) -> dict[tuple[str, str], dict[str, object]]:
    chat_config = f"{target.environment}-chat-config"
    vue_config = f"{target.environment}-web-vue-config-legacy-controlled"
    return {
        ("deployment", target.deployment): {
            "metadata": {"uid": "deployment-uid"},
            "spec": {
                "replicas": 1,
                "strategy": {"type": "Recreate"},
                "template": {
                    "metadata": {
                        "labels": {"app.kubernetes.io/version": target.version},
                    },
                    "spec": {
                        "containers": [
                            {
                                "envFrom": [{"configMapRef": {"name": chat_config}}],
                            }
                        ]
                    },
                },
            },
        },
        ("configmap", chat_config): {"data": {"SERVICE_VERSION": target.version}},
        ("pods", ""): {
            "items": [
                {
                    "metadata": {"uid": "pod-uid"},
                    "status": {
                        "containerStatuses": [
                            {
                                "ready": True,
                                "imageID": (
                                    "docker-pullable://example.invalid/chat-api@"
                                    f"{target.image_digest}"
                                ),
                            }
                        ]
                    },
                }
            ]
        },
        ("deployment", f"{target.environment}-web-vue-legacy"): {
            "spec": {
                "template": {
                    "spec": {
                        "containers": [
                            {
                                "envFrom": [{"configMapRef": {"name": vue_config}}],
                            }
                        ]
                    }
                }
            }
        },
        ("configmap", vue_config): {
            "data": {
                "CHAT_URL": (f"http://{target.java_service}.{target.namespace}.svc.cluster.local")
            }
        },
    }


def getter_for(values: dict[tuple[str, str], dict[str, object]]):
    def get_json(
        _target: qualification.Target,
        kind: str,
        name: str,
        _selector: str,
    ) -> dict[str, object]:
        return deepcopy(values[(kind, name)])

    return get_json


@pytest.mark.parametrize(
    ("field", "value", "message"),
    [
        ("environment", "prod", "environment"),
        ("context", "qa-insurance-hub", "context"),
        ("namespace", "default", "namespace"),
        ("service", "local-dev-chat-api-legacy", "service"),
        ("source_revision", "0" * 40, "source revision"),
        ("image_digest", f"sha256:{'0' * 64}", "image digest"),
    ],
)
def test_target_rejects_wrong_coordinates_before_target_access(
    field: str,
    value: str,
    message: str,
) -> None:
    # given
    selected = arguments(**{field: value})

    # when / then
    with pytest.raises(qualification.QualificationError, match=message):
        qualification.Target.validated(selected)


def test_live_identity_accepts_exact_candidate_and_public_java_route() -> None:
    # given
    target = qualification.Target.validated(arguments())

    # when
    evidence = qualification.validate_live_identity(target, getter_for(documents(target)))

    # then
    assert evidence["source_revision"] == "a" * 40
    assert evidence["version"] == "git-aaaaaaaaaaaa"
    assert evidence["image_digest"] == f"sha256:{'b' * 64}"
    assert evidence["deployment_uid"] == "deployment-uid"
    assert evidence["pod_uid"] == "pod-uid"
    assert evidence["runtime_service_version"] == "git-aaaaaaaaaaaa"
    assert evidence["runtime_service_instance"] == "pod-uid"
    assert evidence["public_chat_url"].startswith("http://local-dev-chat-api-legacy.")


@pytest.mark.parametrize("mutation", ["digest", "version", "public-route", "replicas"])
def test_live_identity_rejects_candidate_or_route_drift(mutation: str) -> None:
    # given
    target = qualification.Target.validated(arguments())
    values = documents(target)
    if mutation == "digest":
        values[("pods", "")]["items"][0]["status"]["containerStatuses"][0]["imageID"] = (
            f"docker-pullable://example.invalid/chat-api@sha256:{'c' * 64}"
        )
    elif mutation == "version":
        values[("configmap", "local-dev-chat-config")]["data"]["SERVICE_VERSION"] = (
            "git-cccccccccccc"
        )
    elif mutation == "public-route":
        values[("configmap", "local-dev-web-vue-config-legacy-controlled")]["data"]["CHAT_URL"] = (
            "http://local-dev-chat-api.local-dev-all.svc.cluster.local"
        )
    else:
        values[("deployment", target.deployment)]["spec"]["replicas"] = 2

    # when / then
    with pytest.raises(qualification.QualificationError):
        qualification.validate_live_identity(target, getter_for(values))


def test_nginx_command_binds_only_loopback_and_uses_unchanged_image() -> None:
    # when
    actual = qualification.nginx_arguments(
        nginx_port=18081,
        relay_directory=Path("/tmp/insurance-hub-chat-qualification-test"),
        container_name="insurance-hub-chat-qualification-390",
        docker="docker",
        nginx_image=qualification.NGINX_IMAGE,
    )

    # then
    assert "127.0.0.1:18081:80" in actual
    assert "CHAT_URL=http://unix:/run/chat-upstream/chat.sock:" in actual
    assert "/tmp/insurance-hub-chat-qualification-test:/run/chat-upstream:ro" in actual
    assert f"{qualification.NGINX_TEMPLATE}:/etc/nginx/conf.d/default.tmpl:ro" in actual
    assert "kube-dns.kube-system.svc.cluster.local:127.0.0.1" in actual
    assert actual[2:4] == ("--pull", "never")
    assert actual[-1] == qualification.NGINX_IMAGE
    assert all("0.0.0.0" not in value for value in actual)


def test_nginx_image_inspection_records_local_content_identity() -> None:
    # given
    environment = {"PATH": "/usr/bin"}
    expected = f"sha256:{'c' * 64}"

    # when
    with patch.object(qualification, "run", return_value=f"{expected}\n") as run_command:
        actual = qualification.inspect_nginx_image("docker", qualification.NGINX_IMAGE, environment)

    # then
    assert actual == expected
    run_command.assert_called_once_with(
        [
            "docker",
            "image",
            "inspect",
            qualification.NGINX_IMAGE,
            "--format",
            "{{.Id}}",
        ],
        cwd=qualification.ROOT,
        env=environment,
        timeout_seconds=15,
        activity="inspect Chat qualification Nginx image",
        capture=True,
    )


def test_relay_command_connects_unix_socket_only_to_loopback_forward() -> None:
    # when
    actual = qualification.relay_arguments(
        Path("/tmp/insurance-hub-chat-qualification-test/chat.sock"),
        18080,
        "socat",
    )

    # then
    assert actual[1].startswith("UNIX-LISTEN:/tmp/insurance-hub-chat-qualification-test/")
    assert actual[2] == "TCP:127.0.0.1:18080"


def test_qualify_runs_real_clients_through_nginx_and_cleans_after_failure() -> None:
    # given
    target = qualification.Target.validated(arguments())
    events: list[str] = []
    process = SimpleNamespace(process=Mock())

    class Forward:
        def __init__(self, spec: PortForwardSpec, **_options: object) -> None:
            assert spec == PortForwardSpec(
                context="kind-local-dev-insurance-hub",
                namespace="local-dev-all",
                resource="service/local-dev-chat-api",
                local_port=18080,
                remote_port=80,
            )

        def __enter__(self) -> int:
            events.append("forward-start")
            return 18080

        def __exit__(self, *_args: object) -> None:
            events.append("forward-stop")

    class Nginx:
        def __init__(self, *_args: object, **_options: object) -> None:
            pass

        def __enter__(self):
            events.append("nginx-start")
            return process

        def __exit__(self, *_args: object) -> None:
            events.append("nginx-stop")

    class Relay:
        def __init__(self, *_args: object, **_options: object) -> None:
            pass

        def __enter__(self):
            events.append("relay-start")
            return process

        def __exit__(self, *_args: object) -> None:
            events.append("relay-stop")

    def fail_verification(_port: int, _make: str, _environment: object) -> None:
        events.append("verify")
        raise qualification.QualificationError("controlled parity mismatch")

    # when / then
    with pytest.raises(qualification.QualificationError, match="parity mismatch"):
        qualification.qualify(
            target,
            arguments(),
            get_json=getter_for(documents(target)),
            forward_type=Forward,
            relay_type=Relay,
            nginx_type=Nginx,
            relay_waiter=lambda _path, _process: None,
            waiter=lambda _port, _process: None,
            verifier=fail_verification,
            nginx_image_inspector=lambda _docker, _image, _environment: f"sha256:{'c' * 64}",
            clock=lambda: "2026-11-18T00:00:00Z",
        )
    assert events == [
        "forward-start",
        "relay-start",
        "nginx-start",
        "verify",
        "nginx-stop",
        "relay-stop",
        "forward-stop",
    ]


def test_qualify_reports_identity_and_pass_only_after_cleanup(
    capsys: pytest.CaptureFixture[str],
) -> None:
    # given
    target = qualification.Target.validated(arguments())
    events: list[str] = []
    process = SimpleNamespace(process=Mock())

    class Resource:
        def __init__(self, name: str) -> None:
            self.name = name

        def __enter__(self):
            events.append(f"{self.name}-start")
            return process

        def __exit__(self, *_args: object) -> None:
            events.append(f"{self.name}-stop")

    def resource_type(name: str):
        return lambda *_args, **_options: Resource(name)

    times = iter(("2026-11-18T00:00:00Z", "2026-11-18T00:00:05Z"))

    # when
    with patch("builtins.print", side_effect=lambda value: events.append(f"report:{value}")):
        qualification.qualify(
            target,
            arguments(),
            get_json=getter_for(documents(target)),
            forward_type=resource_type("forward"),
            relay_type=resource_type("relay"),
            nginx_type=resource_type("nginx"),
            relay_waiter=lambda _path, _process: None,
            waiter=lambda _port, _process: None,
            verifier=lambda _port, _make, _environment: events.append("verify"),
            nginx_image_inspector=lambda _docker, _image, _environment: f"sha256:{'c' * 64}",
            clock=lambda: next(times),
        )

    # then
    assert events[:-1] == [
        "forward-start",
        "relay-start",
        "nginx-start",
        "verify",
        "nginx-stop",
        "relay-stop",
        "forward-stop",
    ]
    report = json.loads(events[-1].removeprefix("report:"))
    assert report["deployment_uid"] == "deployment-uid"
    assert report["pod_uid"] == "pod-uid"
    assert report["runtime_service_instance"] == "pod-uid"
    assert report["nginx_image_id"] == f"sha256:{'c' * 64}"
    assert report["owner_started_at"] == "2026-11-18T00:00:00Z"
    assert report["owner_ended_at"] == "2026-11-18T00:00:05Z"
    assert report["cleanup"] == "passed"
    assert report["real_client_parity"] == "passed"
    assert capsys.readouterr().out == ""


def test_nginx_cleanup_preserves_failure_and_closes_process() -> None:
    # given
    owned = Mock()
    owned.process.poll.return_value = None
    nginx = qualification.NginxProcess(
        ("docker", "run"),
        name="insurance-hub-chat-qualification-390",
        docker="docker",
        environment={"PATH": "/usr/bin"},
    )
    nginx.owned = owned

    # when / then
    with (
        patch.object(qualification, "run", side_effect=CommandError("controlled cleanup")),
        pytest.raises(ResourceError, match="Nginx cleanup failed"),
    ):
        nginx.__exit__(None, None, None)
    owned.close.assert_called_once_with()


def test_cleanup_failure_is_attached_to_an_existing_failure() -> None:
    # given
    owned = Mock()
    owned.process.poll.return_value = None
    nginx = qualification.NginxProcess(
        ("docker", "run"),
        name="insurance-hub-chat-qualification-390",
        docker="docker",
        environment={"PATH": "/usr/bin"},
    )
    nginx.owned = owned
    primary = qualification.QualificationError("controlled primary")

    # when
    with patch.object(qualification, "run", side_effect=CommandError("controlled cleanup")):
        nginx.__exit__(type(primary), primary, None)

    # then
    assert primary.__notes__ == ["Chat qualification Nginx cleanup also failed: controlled cleanup"]
    owned.close.assert_called_once_with()


def test_nginx_cleanup_removes_owned_container_after_run_client_exits() -> None:
    # given
    owned = Mock()
    owned.process.poll.return_value = 1
    nginx = qualification.NginxProcess(
        ("docker", "run"),
        name="insurance-hub-chat-qualification-390",
        docker="docker",
        environment={"PATH": "/usr/bin"},
    )
    nginx.owned = owned

    # when
    with patch.object(qualification, "run") as run_command:
        nginx.__exit__(None, None, None)

    # then
    run_command.assert_called_once_with(
        ["docker", "rm", "--force", "insurance-hub-chat-qualification-390"],
        cwd=qualification.ROOT,
        env={"PATH": "/usr/bin"},
        timeout_seconds=15,
        activity="remove Chat qualification Nginx container",
    )
    owned.close.assert_called_once_with()


def test_nginx_cleanup_accepts_exact_container_absence() -> None:
    # given
    owned = Mock()
    name = "insurance-hub-chat-qualification-390"
    nginx = qualification.NginxProcess(
        ("docker", "run"),
        name=name,
        docker="docker",
        environment={"PATH": "/usr/bin"},
    )
    nginx.owned = owned
    absent = CommandError(
        "controlled absence",
        stderr=f"Error response from daemon: No such container: {name}\n",
    )

    # when
    with patch.object(qualification, "run", side_effect=absent):
        nginx.__exit__(None, None, None)

    # then
    owned.close.assert_called_once_with()


def test_relay_cleanup_preserves_primary_failure() -> None:
    # given
    owned = Mock()
    owned.close.side_effect = ResourceError("controlled relay cleanup")
    relay = qualification.RelayProcess(("socat",), {"PATH": "/usr/bin"})
    relay.owned = owned
    primary = qualification.QualificationError("controlled primary")

    # when
    relay.__exit__(type(primary), primary, None)

    # then
    assert primary.__notes__ == [
        "Chat qualification relay cleanup also failed: controlled relay cleanup"
    ]


def test_nginx_readiness_timeout_is_bounded() -> None:
    # given
    process = Mock()
    process.process.poll.return_value = None

    # when / then
    with (
        patch.object(qualification.time, "monotonic", side_effect=[0, 0, 31]),
        patch.object(qualification.time, "sleep"),
        patch.object(
            qualification.urllib.request,
            "urlopen",
            side_effect=urllib.error.URLError("controlled unavailable"),
        ),
        pytest.raises(qualification.QualificationError, match="readiness timed out"),
    ):
        qualification.wait_for_nginx(18081, process, seconds=30)


def test_relay_readiness_timeout_is_bounded(tmp_path: Path) -> None:
    # given
    process = Mock()
    process.process.poll.return_value = None

    # when / then
    with (
        patch.object(qualification.time, "monotonic", side_effect=[0, 0, 11]),
        patch.object(qualification.time, "sleep"),
        pytest.raises(qualification.QualificationError, match="relay readiness timed out"),
    ):
        qualification.wait_for_relay(tmp_path / "missing.sock", process, seconds=10)


def test_verify_parity_uses_owned_real_client_target() -> None:
    # given
    environment = {"PATH": "/usr/bin"}

    # when
    with patch.object(qualification, "run") as run_command:
        qualification.verify_parity(18081, "make", environment)

    # then
    run_command.assert_called_once()
    command = run_command.call_args.args[0]
    assert command[-2] == "verify-live-java-parity"
    assert command[-1] == "JAVA_PARITY_ENDPOINT=ws://127.0.0.1:18081"
    assert run_command.call_args.kwargs["timeout_seconds"] == 60


def test_cli_diagnostic_does_not_echo_rejected_target(capsys: pytest.CaptureFixture[str]) -> None:
    # given
    sentinel = "SENTINEL-private-context"
    command = [
        "preflight",
        "--environment",
        "local-dev",
        "--context",
        sentinel,
        "--namespace",
        "local-dev-all",
        "--service",
        "local-dev-chat-api",
        "--source-revision",
        "a" * 40,
        "--image-digest",
        f"sha256:{'b' * 64}",
    ]

    # when
    status = qualification.main(command)

    # then
    assert status == 1
    assert sentinel not in capsys.readouterr().err
