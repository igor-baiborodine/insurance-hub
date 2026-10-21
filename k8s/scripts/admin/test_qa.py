"""Behavior tests for QA LXD and kubeconfig administration."""

from __future__ import annotations

import json
import signal
import stat
import subprocess
from pathlib import Path

import pytest
import qa
import runtime
import yaml
from runtime import (
    AdminError,
    AdminSignal,
    Commands,
    WorkflowDeadline,
    WorkflowDeadlineError,
)


class FakeCommands:
    def __init__(self, snapshots=None, fail_snapshot=False, snapshot_error=None):
        self.calls = []
        self.snapshots = {node: set(snapshots or []) for node in qa.ALL_NODES}
        self.fail_snapshot = fail_snapshot
        self.snapshot_error = snapshot_error
        self.states = {node: "RUNNING" for node in qa.ALL_NODES}

    def succeeds(self, arguments, **_kwargs):
        self.calls.append(list(arguments))
        return arguments[:2] == ["lxc", "info"]

    def run(self, arguments, **_kwargs):
        self.calls.append(list(arguments))
        if arguments[:2] == ["lxc", "query"]:
            node = arguments[-1].split("/")[3]
            return json.dumps(sorted(self.snapshots[node]))
        if arguments[:2] == ["lxc", "list"] and "-c" in arguments:
            return f"{self.states[arguments[2]]}\n"
        if arguments[:2] == ["lxc", "pause"]:
            self.states[arguments[2]] = "FROZEN"
        if arguments[:2] == ["lxc", "start"]:
            self.states[arguments[2]] = "RUNNING"
        if arguments[:2] == ["lxc", "delete"]:
            node, snapshot = arguments[2].split("/", 1)
            self.snapshots[node] = {
                item for item in self.snapshots[node] if item.rsplit("/", 1)[-1] != snapshot
            }
        if arguments[:2] == ["lxc", "snapshot"]:
            if self.snapshot_error is not None:
                raise self.snapshot_error
            if self.fail_snapshot:
                raise AdminError("snapshot failed")
            node, snapshot = arguments[2:4]
            self.snapshots[node].add(f"/1.0/instances/{node}/snapshots/{snapshot}")
        return ""


def source_kubeconfig() -> dict:
    return {
        "apiVersion": "v1",
        "kind": "Config",
        "current-context": "default",
        "clusters": [
            {
                "name": "default",
                "cluster": {"server": "https://127.0.0.1:6443", "certificate-authority-data": "ca"},
            }
        ],
        "users": [{"name": "default", "user": {"client-certificate-data": "cert"}}],
        "contexts": [{"name": "default", "context": {"cluster": "default", "user": "default"}}],
    }


class PullCommands:
    def succeeds(self, arguments, **_kwargs):
        return arguments[:2] == ["lxc", "info"]

    def run(self, arguments, **_kwargs):
        if arguments[:3] == ["lxc", "list", "--format"]:
            return json.dumps(
                [
                    {
                        "name": "qa-master",
                        "state": {
                            "network": {
                                "enp5s0": {
                                    "addresses": [
                                        {
                                            "family": "inet",
                                            "scope": "global",
                                            "address": "10.0.0.10",
                                        }
                                    ]
                                }
                            }
                        },
                    }
                ]
            )
        if arguments[:3] == ["lxc", "file", "pull"]:
            Path(arguments[-1]).write_text(yaml.safe_dump(source_kubeconfig()))
        return ""


def test_kubeconfig_rewrite_is_structured_and_exact():
    # given
    source = source_kubeconfig()

    # when
    result = qa._rename_kubeconfig(source, "10.0.0.10")

    # then
    assert result["current-context"] == "qa-insurance-hub"
    assert result["clusters"] == [
        {
            "name": "qa-insurance-hub",
            "cluster": {"server": "https://10.0.0.10:6443", "certificate-authority-data": "ca"},
        }
    ]
    assert source["clusters"][0]["cluster"]["server"] == "https://127.0.0.1:6443"


def test_kubeconfig_rewrite_rejects_unexpected_server():
    # given
    source = source_kubeconfig()
    source["clusters"][0]["cluster"]["server"] = "https://shared.example:6443"

    # when / then
    with pytest.raises(AdminError, match="unexpected"):
        qa._rename_kubeconfig(source, "10.0.0.10")


def test_global_ipv4_ignores_kubernetes_bridge_addresses() -> None:
    # given
    commands = FakeCommands()

    def run(arguments, **_kwargs):
        commands.calls.append(list(arguments))
        return json.dumps(
            [
                {
                    "name": "qa-master",
                    "state": {
                        "network": {
                            "cni0": {
                                "addresses": [
                                    {"family": "inet", "scope": "global", "address": "10.42.0.1"}
                                ]
                            },
                            "enp5s0": {
                                "addresses": [
                                    {
                                        "family": "inet",
                                        "scope": "global",
                                        "address": "10.43.248.183",
                                    }
                                ]
                            },
                        }
                    },
                }
            ]
        )

    commands.run = run

    # when
    address, interface = qa._global_ipv4(commands, "qa-master")

    # then
    assert (address, interface) == ("10.43.248.183", "enp5s0")


def test_snapshot_failure_resumes_every_paused_node():
    # given
    commands = FakeCommands(fail_snapshot=True)

    # when / then
    with pytest.raises(AdminError, match="snapshot failed"):
        qa.snapshot_nodes(commands, "baseline")
    starts = [call[-1] for call in commands.calls if call[:2] == ["lxc", "start"]]
    assert starts == ["qa-worker2", "qa-worker1", "qa-master"]


@pytest.mark.parametrize(
    "interruption",
    [
        pytest.param(KeyboardInterrupt(), id="sigint"),
        pytest.param(AdminSignal(signal.SIGTERM), id="sigterm"),
    ],
)
def test_snapshot_interruption_resumes_every_paused_node_and_preserves_signal(interruption):
    # given
    commands = FakeCommands(snapshot_error=interruption)

    # when / then
    with pytest.raises(type(interruption)) as raised:
        qa.snapshot_nodes(commands, "baseline")
    assert raised.value is interruption
    starts = [call[-1] for call in commands.calls if call[:2] == ["lxc", "start"]]
    assert starts == ["qa-worker2", "qa-worker1", "qa-master"]
    assert all(commands.states[node] == "RUNNING" for node in qa.ALL_NODES)


@pytest.mark.parametrize("state", ["RUNNING", "STOPPED"])
def test_existing_snapshot_is_replaced_without_deleting_running_or_stopped_vm(state):
    # given
    commands = FakeCommands(snapshots=["/1.0/instances/qa-master/snapshots/baseline"])
    commands.states = {node: state for node in qa.ALL_NODES}
    expected_resources = {
        "qa-master": "qa-master/baseline",
        "qa-worker1": "qa-worker1/baseline",
        "qa-worker2": "qa-worker2/baseline",
    }

    # when
    qa.snapshot_nodes(commands, "baseline")

    # then
    for node in qa.ALL_NODES:
        delete = ["lxc", "delete", expected_resources[node]]
        snapshot = ["lxc", "snapshot", node, "baseline"]
        assert commands.calls.index(delete) < commands.calls.index(snapshot)
        assert ["lxc", "delete", node, "baseline"] not in commands.calls
        assert any(
            call[:2] == ["lxc", "query"] and call[-1].endswith(f"/{node}/snapshots")
            for call in commands.calls[commands.calls.index(snapshot) + 1 :]
        )


def test_snapshot_does_not_start_a_preexisting_stopped_node():
    # given
    class MixedStateCommands(FakeCommands):
        def run(self, arguments, **kwargs):
            if (
                arguments[:2] == ["lxc", "list"]
                and "-c" in arguments
                and arguments[2] == "qa-worker2"
            ):
                self.calls.append(list(arguments))
                return "STOPPED\n"
            return super().run(arguments, **kwargs)

    commands = MixedStateCommands()

    # when
    qa.snapshot_nodes(commands, "baseline")

    # then
    assert ["lxc", "pause", "qa-worker2"] not in commands.calls
    assert ["lxc", "start", "qa-worker2"] not in commands.calls


def test_merge_replaces_only_matching_named_entries():
    # given
    existing = {"clusters": [{"name": "other"}, {"name": "qa-insurance-hub", "old": True}]}
    incoming = {"clusters": [{"name": "qa-insurance-hub", "new": True}]}

    # when
    result = qa._merge_named(existing, incoming, "clusters")

    # then
    assert result == [{"name": "other"}, {"name": "qa-insurance-hub", "new": True}]


def test_invalid_snapshot_name_is_rejected_before_lxc_calls():
    # given
    commands = FakeCommands()

    # when / then
    with pytest.raises(AdminError, match="DNS-safe"):
        qa.node_action(commands, "snapshot", "../../bad")
    assert commands.calls == []


def test_cluster_join_token_uses_stdin_file_channel_and_never_argv():
    # given
    sentinel = "SENTINEL-k3s-join-token"

    class ClusterCommands:
        def __init__(self):
            self.calls = []
            self.remote_files = {worker: {} for worker in qa.WORKERS}
            self.agent_token_files = {}

        def succeeds(self, arguments, **_kwargs):
            self.calls.append((list(arguments), None))
            return True

        def run(self, arguments, *, input_text=None, **_kwargs):
            self.calls.append((list(arguments), input_text))
            if arguments[:3] == ["lxc", "file", "push"]:
                worker, path = arguments[4].split("/", 1)
                self.remote_files[worker][f"/{path}"] = (input_text, arguments[-1])
                return ""
            if arguments[:2] == ["lxc", "exec"] and any(
                argument.startswith("INSTALL_K3S_EXEC=agent") for argument in arguments
            ):
                worker = arguments[2]
                temporary, durable = arguments[-2:]
                assert self.remote_files[worker][temporary][1] == "0600"
                assert 'install -m 0600 -o root -g root "$1" "$2"' in arguments[-4]
                self.remote_files[worker][durable] = (
                    self.remote_files[worker][temporary][0],
                    "0600:root:root",
                )
                install_exec = next(
                    argument
                    for argument in arguments
                    if argument.startswith("INSTALL_K3S_EXEC=agent")
                )
                self.agent_token_files[worker] = install_exec.rsplit("--token-file=", 1)[-1]
                return ""
            if arguments[:2] == ["lxc", "exec"] and arguments[-3:-1] == ["rm", "-f"]:
                self.remote_files[arguments[2]].pop(arguments[-1], None)
                return ""
            if arguments[:2] == ["lxc", "exec"] and arguments[-3:] == [
                "systemctl",
                "restart",
                "k3s-agent",
            ]:
                worker = arguments[2]
                assert self.agent_token_files[worker] in self.remote_files[worker]
                assert not any(path.startswith("/run/") for path in self.remote_files[worker])
                return ""
            if arguments[:3] == ["lxc", "list", "--format"]:
                return json.dumps(
                    [
                        {
                            "name": "qa-master",
                            "state": {
                                "network": {
                                    "enp5s0": {
                                        "addresses": [
                                            {
                                                "family": "inet",
                                                "scope": "global",
                                                "address": "10.0.0.10",
                                            }
                                        ]
                                    }
                                }
                            },
                        }
                    ]
                )
            if arguments[-1].endswith("server/node-token"):
                return sentinel + "\n"
            if arguments[-4:] == ["get", "nodes", "-o", "json"]:
                return json.dumps(
                    {
                        "items": [
                            {"status": {"conditions": [{"type": "Ready", "status": "True"}]}}
                            for _ in range(3)
                        ]
                    }
                )
            return ""

    commands = ClusterCommands()

    # when
    qa.cluster_create(commands, master_only=False)

    # then
    assert all(sentinel not in argument for call, _ in commands.calls for argument in call)
    token_inputs = [
        input_text for call, input_text in commands.calls if call[:3] == ["lxc", "file", "push"]
    ]
    assert token_inputs == [sentinel + "\n", sentinel + "\n"]
    install_calls = [
        call
        for call, _ in commands.calls
        if call[:2] == ["lxc", "exec"]
        and any(argument.startswith("INSTALL_K3S_EXEC=agent") for argument in call)
    ]
    assert len(install_calls) == 2
    for worker, install_call in zip(qa.WORKERS, install_calls, strict=True):
        install_exec = next(
            argument for argument in install_call if argument.startswith("INSTALL_K3S_EXEC=agent")
        )
        assert f"--token-file={qa.AGENT_TOKEN_FILE}" in install_exec
        assert "/run/" not in install_exec
        temporary = f"/run/insurance-hub-k3s-token-{qa.os.getpid()}"
        cleanup = ["lxc", "exec", worker, "--", "rm", "-f", temporary]
        restart = ["lxc", "exec", worker, "--", "systemctl", "restart", "k3s-agent"]
        assert install_call[-2:] == [temporary, qa.AGENT_TOKEN_FILE]
        assert commands.calls.index((cleanup, None)) < commands.calls.index((restart, None))
        assert ["lxc", "exec", worker, "--", "rm", "-f", qa.AGENT_TOKEN_FILE] not in [
            call for call, _ in commands.calls
        ]
        assert commands.remote_files[worker][qa.AGENT_TOKEN_FILE][1] == "0600:root:root"


def test_pull_kubeconfig_writes_mode_0600_from_creation_and_preserves_backup(tmp_path, monkeypatch):
    # given
    destination = tmp_path / ".kube" / "config"
    destination.parent.mkdir()
    destination.write_text(
        "apiVersion: v1\nkind: Config\nclusters:\n- name: other\n  cluster: {}\nusers: []\ncontexts: []\n"
    )

    created_modes = []
    real_fdopen = qa.os.fdopen

    def inspect_mode_at_open(descriptor, *args, **kwargs):
        created_modes.append(stat.S_IMODE(qa.os.fstat(descriptor).st_mode))
        return real_fdopen(descriptor, *args, **kwargs)

    monkeypatch.setattr(qa.os, "fdopen", inspect_mode_at_open)

    # when
    qa.pull_kubeconfig(PullCommands(), destination)

    # then
    backup = destination.with_name("config.insurance-hub-backup")
    assert created_modes == [0o600, 0o600]
    assert stat.S_IMODE(destination.stat().st_mode) == 0o600
    assert stat.S_IMODE(backup.stat().st_mode) == 0o600
    assert "qa-insurance-hub" in destination.read_text()


def test_pull_kubeconfig_replaces_backup_symlink_without_following_it(tmp_path):
    # given
    destination = tmp_path / ".kube" / "config"
    destination.parent.mkdir()
    original = b"apiVersion: v1\nkind: Config\nclusters: []\nusers: []\ncontexts: []\n"
    destination.write_bytes(original)
    symlink_target = tmp_path / "must-remain-unchanged"
    symlink_target.write_bytes(b"SENTINEL-existing-target")
    backup = destination.with_name("config.insurance-hub-backup")
    backup.symlink_to(symlink_target)

    # when
    qa.pull_kubeconfig(PullCommands(), destination)

    # then
    assert not backup.is_symlink()
    assert backup.read_bytes() == original
    assert symlink_target.read_bytes() == b"SENTINEL-existing-target"
    assert stat.S_IMODE(backup.stat().st_mode) == 0o600


def test_pull_kubeconfig_backup_failure_preserves_original_destination(tmp_path, monkeypatch):
    # given
    destination = tmp_path / ".kube" / "config"
    destination.parent.mkdir()
    original = b"apiVersion: v1\nkind: Config\nclusters: []\nusers: []\ncontexts: []\n"
    destination.write_bytes(original)
    backup = destination.with_name("config.insurance-hub-backup")
    backup.write_bytes(b"previous-backup")
    real_replace = qa.os.replace

    def fail_backup_replace(source, target):
        if Path(target) == backup:
            raise OSError("simulated backup replacement failure")
        real_replace(source, target)

    monkeypatch.setattr(qa.os, "replace", fail_backup_replace)

    # when / then
    with pytest.raises(OSError, match="simulated backup"):
        qa.pull_kubeconfig(PullCommands(), destination)
    assert destination.read_bytes() == original
    assert backup.read_bytes() == b"previous-backup"
    assert list(destination.parent.glob(".*.insurance-hub-*")) == []


def test_wait_caps_predicate_command_to_remaining_workflow_budget(monkeypatch):
    # given
    now = [100.0]
    observed_timeouts = []
    monkeypatch.setattr(runtime.time, "monotonic", lambda: now[0])
    monkeypatch.setattr(qa.time, "monotonic", lambda: now[0])

    def expire_during_command(*_args, timeout_seconds, **_kwargs):
        observed_timeouts.append(timeout_seconds)
        now[0] += timeout_seconds
        raise runtime.CommandError("predicate command timed out")

    monkeypatch.setattr(runtime, "run_command", expire_during_command)
    commands = Commands(
        environment={"PATH": "/usr/bin"},
        deadline=WorkflowDeadline("qa-cluster create", 105.0),
    )

    # when / then
    with pytest.raises(WorkflowDeadlineError, match="overall work deadline.*probe readiness"):
        qa._wait(
            commands,
            "probe readiness",
            lambda remaining: commands.succeeds(
                ["probe"], timeout=min(30, remaining), activity="probe readiness"
            ),
            timeout=180,
        )
    assert observed_timeouts == [5.0]


def test_wait_caps_predicate_command_to_remaining_phase_budget(monkeypatch):
    # given
    now = [100.0]
    observed_timeouts = []
    monkeypatch.setattr(runtime.time, "monotonic", lambda: now[0])
    monkeypatch.setattr(qa.time, "monotonic", lambda: now[0])

    def expire_during_command(*_args, timeout_seconds, **_kwargs):
        observed_timeouts.append(timeout_seconds)
        now[0] += timeout_seconds
        raise runtime.CommandError("predicate command timed out")

    monkeypatch.setattr(runtime, "run_command", expire_during_command)
    commands = Commands(
        environment={"PATH": "/usr/bin"},
        deadline=WorkflowDeadline("qa-cluster create", 1000.0),
    )

    # when / then
    with pytest.raises(AdminError, match="probe readiness exceeded its 5-second deadline"):
        qa._wait(
            commands,
            "probe readiness",
            lambda remaining: commands.succeeds(
                ["probe"], timeout=min(30, remaining), activity="probe readiness"
            ),
            timeout=5,
        )
    assert observed_timeouts == [5.0]


def test_cleanup_budget_survives_expired_work_deadline(monkeypatch):
    # given
    now = [100.0]
    calls = []
    monkeypatch.setattr(runtime.time, "monotonic", lambda: now[0])

    def complete(arguments, **kwargs):
        kwargs["env"] = dict(kwargs["env"])
        calls.append((list(arguments), kwargs))
        return subprocess.CompletedProcess(arguments, 0, "", "")

    monkeypatch.setattr(runtime, "run_command", complete)
    commands = Commands(
        environment={"PATH": "/usr/bin"},
        deadline=WorkflowDeadline("qa-nodes snapshot", 99.0),
    )

    # when / then
    with pytest.raises(WorkflowDeadlineError):
        commands.run(["work"], timeout=30)
    with commands.cleanup_budget("resume nodes after snapshot", 10):
        commands.run(["cleanup"], timeout=30)
    assert len(calls) == 1
    assert calls[0][0] == ["cleanup"]
    assert calls[0][1]["timeout_seconds"] == 10
    assert calls[0][1]["env"][runtime.WORK_DEADLINE_LABEL_ENV] == "resume nodes after snapshot"
