#!/bin/bash
set -euo pipefail

SNAPSHOT_NAME="${1:-baseline}"

NODES_MASTER="qa-master"
NODES_WORKER=("qa-worker1" "qa-worker2")
NODES_ALL=("$NODES_MASTER" "${NODES_WORKER[@]}")

echo "Pausing all QA cluster VMs for consistent snapshots..."
for NODE in "${NODES_ALL[@]}"; do
  if lxc info "$NODE" &>/dev/null; then
    echo "Pausing VM $NODE..."
    lxc pause "$NODE"
  else
    echo "VM $NODE does not exist, skipping."
  fi
done

echo "Deleting existing '${SNAPSHOT_NAME}' snapshots if any..."
for NODE in "${NODES_ALL[@]}"; do
  if lxc info "$NODE" &>/dev/null; then
    echo "Deleting existing snapshot '${SNAPSHOT_NAME}' on $NODE if exists..."
    lxc delete "$NODE" "$SNAPSHOT_NAME" 2>/dev/null || echo "No existing snapshot '${SNAPSHOT_NAME}' on $NODE, continuing..."
  fi
done

echo "Taking snapshots named '${SNAPSHOT_NAME}'..."
for NODE in "${NODES_ALL[@]}"; do
  if lxc info "$NODE" &>/dev/null; then
    echo "Snapshotting VM $NODE..."
    lxc snapshot "$NODE" "$SNAPSHOT_NAME"
  fi
done

echo "Resuming all QA cluster VMs..."
for NODE in "${NODES_ALL[@]}"; do
  if lxc info "$NODE" &>/dev/null; then
    echo "Starting VM $NODE..."
    lxc start "$NODE"
  fi
done

echo "All QA cluster VMs snapshotted consistently as '${SNAPSHOT_NAME}':"
lxc list
