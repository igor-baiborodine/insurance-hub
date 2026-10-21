"""Behavior tests for QA LXD and kubeconfig administration."""

from __future__ import annotations

import json
import stat
from pathlib import Path

import pytest
import qa
from runtime import AdminError


class FakeCommands:
    def __init__(self, snapshots=None, fail_snapshot=False):
        self.calls = []
        self.snapshots = snapshots or []
        self.fail_snapshot = fail_snapshot
        self.states = {node: "RUNNING" for node in qa.ALL_NODES}

    def succeeds(self, arguments, **_kwargs):
        self.calls.append(list(arguments))
        return arguments[:2] == ["lxc", "info"]

    def run(self, arguments, **_kwargs):
        self.calls.append(list(arguments))
        if arguments[:2] == ["lxc", "query"]:
            return json.dumps(self.snapshots)
        if arguments[:2] == ["lxc", "list"] and "-c" in arguments:
            return f"{self.states[arguments[2]]}\n"
        if arguments[:2] == ["lxc", "pause"]:
            self.states[arguments[2]] = "FROZEN"
        if arguments[:2] == ["lxc", "start"]:
            self.states[arguments[2]] = "RUNNING"
        if self.fail_snapshot and arguments[:2] == ["lxc", "snapshot"]:
            raise AdminError("snapshot failed")
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


def test_snapshot_failure_resumes_every_paused_node():
    # given
    commands = FakeCommands(fail_snapshot=True)

    # when / then
    with pytest.raises(AdminError, match="snapshot failed"):
        qa.snapshot_nodes(commands, "baseline")
    starts = [call[-1] for call in commands.calls if call[:2] == ["lxc", "start"]]
    assert starts == ["qa-worker2", "qa-worker1", "qa-master"]


def test_existing_snapshot_is_replaced_before_new_snapshot():
    # given
    commands = FakeCommands(snapshots=["/1.0/instances/qa-master/snapshots/baseline"])

    # when
    qa.snapshot_nodes(commands, "baseline")

    # then
    for node in qa.ALL_NODES:
        delete = ["lxc", "delete", node, "baseline"]
        snapshot = ["lxc", "snapshot", node, "baseline"]
        assert commands.calls.index(delete) < commands.calls.index(snapshot)


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

        def succeeds(self, arguments, **_kwargs):
            self.calls.append((list(arguments), None))
            return True

        def run(self, arguments, *, input_text=None, **_kwargs):
            self.calls.append((list(arguments), input_text))
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


def test_pull_kubeconfig_writes_mode_0600_and_preserves_backup(tmp_path):
    # given
    destination = tmp_path / ".kube" / "config"
    destination.parent.mkdir()
    destination.write_text(
        "apiVersion: v1\nkind: Config\nclusters:\n- name: other\n  cluster: {}\nusers: []\ncontexts: []\n"
    )

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
                import yaml

                Path(arguments[-1]).write_text(yaml.safe_dump(source_kubeconfig()))
            return ""

    # when
    qa.pull_kubeconfig(PullCommands(), destination)

    # then
    assert stat.S_IMODE(destination.stat().st_mode) == 0o600
    assert destination.with_name("config.insurance-hub-backup").exists()
    assert "qa-insurance-hub" in destination.read_text()
