"""Tests for generic Go-service tooling boundary contracts."""

from __future__ import annotations

from pathlib import Path

import pytest

from scripts.go.service import tooling_contracts
from scripts.go.service.tooling_config import DependencyConfig, ToolingConfigError


def test_load_json_object_rejects_path_escape(tmp_path: Path) -> None:
    # given
    module = tmp_path / "module"
    module.mkdir()
    outside = tmp_path / "outside.json"
    outside.write_text("{}\n", encoding="utf-8")
    (module / "escaped.json").symlink_to(outside)

    # when / then
    with pytest.raises(ToolingConfigError, match="escapes its owner"):
        tooling_contracts.load_json_object(module, "escaped.json")


def test_dependency_arguments_apply_owner_label_and_loopback_ports() -> None:
    # given
    dependency = DependencyConfig(
        name="search",
        kind="elasticsearch",
        image=f"example.org/search@sha256:{'a' * 64}",
        ports=(9200, 9300),
    )

    # when
    arguments = tooling_contracts.dependency_container_arguments(dependency, owner="run-123")

    # then
    assert arguments == (
        "docker",
        "run",
        "--detach",
        "--name",
        "insurance-hub-run-123-search",
        "--label",
        "insurance-hub.tooling-owner=run-123",
        "--publish",
        "127.0.0.1::9200",
        "--publish",
        "127.0.0.1::9300",
        dependency.image,
    )


def test_dependency_arguments_reject_unvalidated_instance_before_command_use() -> None:
    # given
    dependency = DependencyConfig(
        name="search",
        kind="elasticsearch",
        image="example.org/search:latest",
        ports=(9200,),
    )

    # when / then
    with pytest.raises(ToolingConfigError, match="not digest-pinned"):
        tooling_contracts.dependency_container_arguments(dependency, owner="run-123")


def test_port_forward_arguments_are_bound_to_context_and_namespace() -> None:
    # given
    spec = tooling_contracts.PortForwardSpec(
        context="kind-local-dev",
        namespace="local-dev-all",
        resource="service/example-api",
        local_port=18080,
        remote_port=8080,
    )

    # when / then
    assert spec.arguments() == (
        "kubectl",
        "--context",
        "kind-local-dev",
        "--namespace",
        "local-dev-all",
        "port-forward",
        "service/example-api",
        "18080:8080",
    )


def test_secret_transport_uses_standard_input_and_redaction(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    calls: list[dict[str, object]] = []

    def fake_run(_arguments: object, **options: object) -> str:
        calls.append(options)
        return ""

    monkeypatch.setattr(tooling_contracts, "run", fake_run)

    # when
    tooling_contracts.run_secret_input(
        ["admin-client", "apply"],
        cwd=Path("."),
        secret="private-value",
        timeout_seconds=10,
        activity="apply administration",
    )

    # then
    assert calls[0]["input_text"] == "private-value\n"
    assert calls[0]["sensitive_values"] == ("private-value",)
    assert "private-value" not in calls[0]["env"].values()  # type: ignore[union-attr]


def test_owned_port_forward_reports_early_exit_and_closes_process(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # given
    class Process:
        @staticmethod
        def poll() -> int:
            return 1

    class Owned:
        process = Process()
        closed = False

        @staticmethod
        def diagnostics() -> str:
            return "controlled failure"

        def close(self) -> None:
            self.closed = True

    owned = Owned()
    monkeypatch.setattr(
        tooling_contracts.OwnedProcess,
        "start",
        lambda *_args, **_options: owned,
    )
    forward = tooling_contracts.OwnedPortForward(
        tooling_contracts.PortForwardSpec(
            context="kind-local-dev",
            namespace="local-dev-all",
            resource="service/example-api",
            local_port=18080,
            remote_port=8080,
        ),
        cwd=tmp_path,
        env={"PATH": "/usr/bin"},
        startup_seconds=1,
    )

    # when / then
    with pytest.raises(
        tooling_contracts.AdministrationError,
        match="port-forward stopped early: controlled failure",
    ):
        forward.__enter__()
    assert owned.closed


def test_json_administration_client_rejects_invalid_port() -> None:
    # when / then
    with pytest.raises(tooling_contracts.AdministrationError, match="port is invalid"):
        tooling_contracts.JSONAdministrationClient(0, "reader", "controlled")
