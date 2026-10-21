"""Bounded LXD and K3s administration for the QA cluster."""

from __future__ import annotations

import json
import os
import tempfile
import time
from pathlib import Path

import yaml
from runtime import AdminError, CommandError, Commands, require, safe_name

MASTER = "qa-master"
WORKERS = ("qa-worker1", "qa-worker2")
ALL_NODES = (MASTER, *WORKERS)


def selected_nodes(master_only: bool) -> tuple[str, ...]:
    return (MASTER,) if master_only else ALL_NODES


def _exists(commands: Commands, node: str) -> bool:
    return commands.succeeds(["lxc", "info", node], timeout=20)


def _node_status(commands: Commands, node: str) -> str:
    return (
        commands.run(["lxc", "list", node, "-c", "s", "--format", "csv"], timeout=30)
        .strip()
        .upper()
    )


def _lxc_json(commands: Commands) -> list[dict]:
    try:
        value = json.loads(commands.run(["lxc", "list", "--format", "json"], timeout=30))
    except json.JSONDecodeError as error:
        raise AdminError("LXD returned malformed node JSON") from error
    require(isinstance(value, list), "LXD node result must be a list")
    return value


def _global_ipv4(commands: Commands, node: str) -> tuple[str, str]:
    for item in _lxc_json(commands):
        if item.get("name") != node:
            continue
        networks = item.get("state", {}).get("network", {})
        for interface, state in networks.items():
            for address in state.get("addresses", []):
                if (
                    address.get("family") == "inet"
                    and address.get("scope") == "global"
                    and address.get("address") != "127.0.0.1"
                ):
                    return address["address"], interface
    raise AdminError(f"{node} has no global IPv4 address")


def _has_global_ipv4(commands: Commands, node: str) -> bool:
    try:
        _global_ipv4(commands, node)
    except AdminError, CommandError:
        return False
    return True


def _wait(commands: Commands, description: str, predicate, timeout: float = 120) -> None:
    started = time.monotonic()
    deadline = started + timeout
    heartbeat = started + 30
    while time.monotonic() < deadline:
        if predicate():
            return
        if time.monotonic() >= heartbeat:
            print(f"{description}: still waiting after {time.monotonic() - started:.0f}s")
            heartbeat = time.monotonic() + 30
        time.sleep(2)
    raise AdminError(f"{description} exceeded its {timeout:g}-second deadline")


def create_nodes(commands: Commands, master_only: bool) -> None:
    created: list[str] = []
    profiles: list[str] = []
    try:
        for node in selected_nodes(master_only):
            if _exists(commands, node):
                print(f"VM {node} already exists, preserving it.")
                continue
            master = node == MASTER
            cpu, memory, disk = ("7", "16GiB", "100GB") if master else ("5", "12GiB", "80GB")
            profile = f"{node}-profile"
            if not commands.succeeds(["lxc", "profile", "show", profile], timeout=20):
                commands.run(["lxc", "profile", "copy", "default", profile], timeout=30)
                profiles.append(profile)
            commands.run(["lxc", "profile", "set", profile, "limits.cpu", cpu], timeout=30)
            commands.run(["lxc", "profile", "set", profile, "limits.memory", memory], timeout=30)
            commands.run(
                ["lxc", "profile", "device", "set", profile, "root", "size", disk], timeout=30
            )
            commands.run(
                ["lxc", "launch", "ubuntu:24.04", "--vm", node, "--profile", profile],
                timeout=300,
                activity=f"launch {node}",
            )
            created.append(node)
            _wait(
                commands,
                f"wait for {node} agent",
                lambda node=node: commands.succeeds(
                    ["lxc", "exec", node, "--", "true"], timeout=10
                ),
                timeout=300,
            )
            _wait(
                commands,
                f"wait for {node} network",
                lambda node=node: _has_global_ipv4(commands, node),
                timeout=180,
            )
            commands.run(
                [
                    "lxc",
                    "exec",
                    node,
                    "--",
                    "bash",
                    "-ceu",
                    "apt-get update -y; apt-get install -y curl ca-certificates chrony; "
                    "timedatectl set-timezone UTC; systemctl enable --now chrony; "
                    "if command -v ufw >/dev/null; then ufw disable; fi; "
                    "modprobe br_netfilter; printf '%s\\n' 'net.bridge.bridge-nf-call-iptables=1' "
                    "> /etc/sysctl.d/k8s.conf; sysctl --system",
                ],
                timeout=600,
                activity=f"prepare {node}",
            )
    except Exception as error:
        retained = ", ".join(created) or "none"
        changed_profiles = ", ".join(profiles) or "none"
        raise AdminError(
            f"QA node creation failed; retained created nodes: {retained}; new profiles: {changed_profiles}"
        ) from error


def cluster_create(commands: Commands, master_only: bool) -> None:
    nodes = selected_nodes(master_only)
    missing = [node for node in nodes if not _exists(commands, node)]
    require(not missing, f"required QA nodes do not exist: {', '.join(missing)}")
    _wait(
        commands,
        "wait for qa-master network",
        lambda: _has_global_ipv4(commands, MASTER),
        timeout=120,
    )
    master_ip, interface = _global_ipv4(commands, MASTER)
    server_exec = (
        f"server --disable=traefik --write-kubeconfig-mode 600 --tls-san={master_ip} "
        f"--flannel-iface={interface}"
    )
    commands.run(
        [
            "lxc",
            "exec",
            MASTER,
            "--",
            "env",
            f"INSTALL_K3S_EXEC={server_exec}",
            "sh",
            "-ceu",
            "curl -sfL https://get.k3s.io | sh -",
        ],
        timeout=600,
        activity="install K3s server",
    )
    _wait(
        commands,
        "wait for K3s server",
        lambda: commands.succeeds(["lxc", "exec", MASTER, "--", "systemctl", "is-active", "k3s"]),
        timeout=180,
    )
    if not master_only:
        token = commands.run(
            ["lxc", "exec", MASTER, "--", "cat", "/var/lib/rancher/k3s/server/node-token"],
            timeout=30,
            activity="read K3s join token",
        ).strip()
        require(token, "K3s join token is empty")
        for worker in WORKERS:
            remote = f"/run/insurance-hub-k3s-token-{os.getpid()}"
            primary_error: BaseException | None = None
            try:
                commands.run(
                    ["lxc", "file", "push", "-", f"{worker}{remote}", "--mode", "0600"],
                    input_text=token + "\n",
                    timeout=30,
                    activity=f"transfer protected join token to {worker}",
                )
                agent_exec = f"agent --flannel-iface={interface} --token-file={remote}"
                commands.run(
                    [
                        "lxc",
                        "exec",
                        worker,
                        "--",
                        "env",
                        f"K3S_URL=https://{master_ip}:6443",
                        f"INSTALL_K3S_EXEC={agent_exec}",
                        "sh",
                        "-ceu",
                        "curl -sfL https://get.k3s.io | sh -",
                    ],
                    timeout=600,
                    activity=f"install K3s agent on {worker}",
                )
            except BaseException as error:
                primary_error = error
                raise
            finally:
                try:
                    commands.run(["lxc", "exec", worker, "--", "rm", "-f", remote], timeout=30)
                except Exception as error:
                    if primary_error is None:
                        raise AdminError(
                            f"K3s installed but token cleanup failed on {worker}"
                        ) from error
                    primary_error.add_note(f"protected token cleanup also failed on {worker}")
    expected = 1 if master_only else 3

    def nodes_ready() -> bool:
        try:
            output = commands.run(
                ["lxc", "exec", MASTER, "--", "kubectl", "get", "nodes", "-o", "json"],
                timeout=20,
            )
            items = json.loads(output).get("items", [])
        except AdminError, CommandError, json.JSONDecodeError:
            return False
        return len(items) >= expected and all(
            any(
                condition.get("type") == "Ready" and condition.get("status") == "True"
                for condition in item.get("status", {}).get("conditions", [])
            )
            for item in items
        )

    _wait(commands, "wait for QA nodes", nodes_ready, timeout=300)
    _wait(
        commands,
        "wait for CoreDNS",
        lambda: commands.succeeds(
            [
                "lxc",
                "exec",
                MASTER,
                "--",
                "kubectl",
                "-n",
                "kube-system",
                "rollout",
                "status",
                "deployment/coredns",
                "--timeout=15s",
            ],
            timeout=20,
        ),
        timeout=180,
    )
    coredns = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": "coredns-custom", "namespace": "kube-system"},
        "data": {
            "rewrite.override": "rewrite name kubernetes.default kubernetes.default.svc.cluster.local\n"
        },
    }
    commands.run(
        ["lxc", "exec", MASTER, "--", "kubectl", "apply", "-f", "-"],
        input_text=json.dumps(coredns),
        timeout=60,
    )


def _rename_kubeconfig(value: dict, master_ip: str) -> dict:
    require(value.get("apiVersion") == "v1" and value.get("kind") == "Config", "invalid kubeconfig")
    original = value.get("current-context", "")
    require(original, "source kubeconfig has no current context")
    renamed = json.loads(json.dumps(value))
    for cluster in renamed.get("clusters", []):
        server = cluster.get("cluster", {}).get("server", "")
        require(server.startswith("https://127.0.0.1:"), "source kubeconfig server is unexpected")
        cluster["cluster"]["server"] = server.replace("127.0.0.1", master_ip, 1)
        if cluster.get("name") == original:
            cluster["name"] = "qa-insurance-hub"
    for user in renamed.get("users", []):
        if user.get("name") == original:
            user["name"] = "qa-insurance-hub"
    for context in renamed.get("contexts", []):
        if context.get("name") == original:
            context["name"] = "qa-insurance-hub"
            details = context.setdefault("context", {})
            if details.get("cluster") == original:
                details["cluster"] = "qa-insurance-hub"
            if details.get("user") == original:
                details["user"] = "qa-insurance-hub"
    renamed["current-context"] = "qa-insurance-hub"
    return renamed


def _merge_named(existing: dict, incoming: dict, key: str) -> list:
    replacement_names = {item["name"] for item in incoming.get(key, [])}
    kept = [item for item in existing.get(key, []) if item.get("name") not in replacement_names]
    return [*kept, *incoming.get(key, [])]


def pull_kubeconfig(commands: Commands, destination: Path) -> None:
    require(_exists(commands, MASTER), "qa-master does not exist")
    _wait(
        commands,
        "wait for qa-master network",
        lambda: _has_global_ipv4(commands, MASTER),
        timeout=120,
    )
    master_ip, _ = _global_ipv4(commands, MASTER)
    destination = destination.expanduser().resolve()
    require(
        destination.name == "config" and destination.parent.name == ".kube",
        "unexpected kubeconfig path",
    )
    destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="insurance-hub-kubeconfig-") as directory:
        source = Path(directory) / "qa.yaml"
        commands.run(
            ["lxc", "file", "pull", f"{MASTER}/etc/rancher/k3s/k3s.yaml", str(source)],
            timeout=60,
        )
        try:
            incoming = _rename_kubeconfig(yaml.safe_load(source.read_text()), master_ip)
            existing = yaml.safe_load(destination.read_text()) if destination.exists() else {}
        except yaml.YAMLError as error:
            raise AdminError("source or existing kubeconfig contains malformed YAML") from error
        existing = existing or {"apiVersion": "v1", "kind": "Config"}
        require(
            existing.get("apiVersion") == "v1" and existing.get("kind") == "Config",
            "existing kubeconfig is invalid",
        )
        merged = dict(existing)
        for key in ("clusters", "users", "contexts"):
            merged[key] = _merge_named(existing, incoming, key)
        merged["current-context"] = "qa-insurance-hub"
        backup = destination.with_name("config.insurance-hub-backup")
        if destination.exists():
            backup.write_bytes(destination.read_bytes())
            backup.chmod(0o600)
        temporary = destination.with_name(f".config.insurance-hub-{os.getpid()}")
        try:
            fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as output:
                yaml.safe_dump(merged, output, sort_keys=False)
                output.flush()
                os.fsync(output.fileno())
            os.replace(temporary, destination)
            destination.chmod(0o600)
        finally:
            temporary.unlink(missing_ok=True)


def node_action(commands: Commands, action: str, snapshot: str = "baseline") -> None:
    safe_name(snapshot, "snapshot name")
    if action == "snapshot":
        snapshot_nodes(commands, snapshot)
        return
    if action == "snapshots-list":
        for node in ALL_NODES:
            if not _exists(commands, node):
                print(f"Snapshots for {node}: VM does not exist")
                continue
            raw = commands.run(["lxc", "query", f"/1.0/instances/{node}/snapshots"], timeout=30)
            names = [item.rsplit("/", 1)[-1] for item in json.loads(raw)]
            print(f"Snapshots for {node}: {', '.join(names) if names else 'none'}")
        return
    existing = [node for node in ALL_NODES if _exists(commands, node)]
    if action == "suspend":
        for node in existing:
            if _node_status(commands, node) == "RUNNING":
                commands.run(["lxc", "pause", node], timeout=60)
    elif action == "resume":
        for node in existing:
            if _node_status(commands, node) != "RUNNING":
                commands.run(["lxc", "start", node], timeout=120)
    elif action == "restore":
        for node in existing:
            commands.run(["lxc", "restore", node, snapshot], timeout=300)
            if _node_status(commands, node) != "RUNNING":
                commands.run(["lxc", "start", node], timeout=120)
    elif action == "delete":
        for node in existing:
            commands.run(["lxc", "delete", node, "--force"], timeout=300)
    else:
        raise AdminError(f"unknown node action {action}")


def snapshot_nodes(commands: Commands, snapshot: str) -> None:
    existing = [node for node in ALL_NODES if _exists(commands, node)]
    running = [node for node in existing if _node_status(commands, node) == "RUNNING"]
    paused: list[str] = []
    replaced: list[str] = []
    created: list[str] = []
    primary_error: Exception | None = None
    try:
        for node in running:
            paused.append(node)
            commands.run(["lxc", "pause", node], timeout=60)
        for node in existing:
            snapshots = json.loads(
                commands.run(["lxc", "query", f"/1.0/instances/{node}/snapshots"], timeout=30)
            )
            if any(item.rsplit("/", 1)[-1] == snapshot for item in snapshots):
                commands.run(["lxc", "delete", node, snapshot], timeout=120)
                replaced.append(node)
            commands.run(["lxc", "snapshot", node, snapshot], timeout=300)
            created.append(node)
    except Exception as error:
        primary_error = error
    cleanup_errors = []
    for node in reversed(paused):
        try:
            if _node_status(commands, node) != "RUNNING":
                commands.run(["lxc", "start", node], timeout=120)
        except Exception as error:
            cleanup_errors.append((node, error))
    if primary_error is not None:
        state = (
            f"; replaced previous snapshots on {', '.join(replaced) or 'none'}"
            f"; created new snapshots on {', '.join(created) or 'none'}"
        )
        suffix = (
            f"; resume also failed for {', '.join(n for n, _ in cleanup_errors)}"
            if cleanup_errors
            else ""
        )
        raise AdminError(f"snapshot failed{state}{suffix}") from primary_error
    if cleanup_errors:
        raise AdminError(
            f"snapshots completed but resume failed for {', '.join(n for n, _ in cleanup_errors)}"
        )
