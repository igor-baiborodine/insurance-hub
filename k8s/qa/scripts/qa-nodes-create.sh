#!/bin/bash

set -euo pipefail

NODES_MASTER="qa-master"
NODES_WORKER="qa-worker1 qa-worker2"
NODES_ALL="$NODES_MASTER $NODES_WORKER"

for NODE in $NODES_ALL; do
    if ! lxc info "$NODE" &>/dev/null; then
        echo "Launching VM $NODE..."
        lxc launch ubuntu:24.04 --vm "$NODE"
        echo "Waiting for LXD VM agent in $NODE..."
        until lxc exec "$NODE" -- true &>/dev/null; do sleep 5; done
        echo "Ensuring network works inside $NODE..."
        lxc exec "$NODE" -- bash -c "until ping -c1 1.1.1.1 &>/dev/null; do echo Waiting for network...; sleep 5; done"
        echo "Installing prerequisites (curl, ca-certificates) in $NODE..."
        lxc exec "$NODE" -- bash -c "sudo apt-get update -y && sudo apt-get install -y curl ca-certificates"
        echo "Disabling firewall (ufw) in $NODE to ensure k3s networking works correctly..."
        lxc exec "$NODE" -- bash -c "if command -v ufw >/dev/null 2>&1; then sudo ufw disable; else echo 'ufw not found, skipping.'; fi"
    else
        echo "VM $NODE already exists, skipping."
    fi
done

echo "All VM nodes have been created and prepared for k3s."
