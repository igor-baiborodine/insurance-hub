#!/bin/bash

set -euo pipefail

NODES_MASTER="qa-master"
NODES_WORKER="qa-worker1 qa-worker2"

# If MASTER_ONLY=true, create only the master node; otherwise master + 2 workers
if [[ "${MASTER_ONLY:-}" == "true" ]]; then
    echo "MASTER_ONLY is true; only creating master node: $NODES_MASTER"
    NODES_ALL="$NODES_MASTER"
else
    echo "MASTER_ONLY not set or not true; creating master and worker nodes: $NODES_MASTER $NODES_WORKER"
    NODES_ALL="$NODES_MASTER $NODES_WORKER"
fi

for NODE in $NODES_ALL; do
    if ! lxc info "$NODE" &>/dev/null; then
        if [[ $NODES_MASTER =~ $NODE ]]; then
            LIMITS_CPU=7
            LIMITS_MEMORY=16
            ROOT_SIZE=100
        else
            LIMITS_CPU=5
            LIMITS_MEMORY=12
            ROOT_SIZE=80
        fi
        if ! lxc profile show "$NODE-profile" &>/dev/null; then
            lxc profile copy default "$NODE-profile"
        else
            echo "Profile $NODE-profile already exists, reusing."
        fi
        lxc profile set "$NODE-profile" limits.cpu "$LIMITS_CPU"
        lxc profile set "$NODE-profile" limits.memory "${LIMITS_MEMORY}GiB"
        lxc profile device set "$NODE-profile" root size "${ROOT_SIZE}GB"
        echo "Configured profile for VM $NODE:"
        lxc profile show "$NODE-profile"

        echo "Launching VM $NODE..."
        lxc launch ubuntu:24.04 --vm "$NODE" --profile "$NODE-profile"
        echo "Waiting for LXD VM agent in $NODE..."
        until lxc exec "$NODE" -- true &>/dev/null; do sleep 5; done
        echo "Ensuring network works inside $NODE..."
        lxc exec "$NODE" -- bash -c "until ping -c1 1.1.1.1 &>/dev/null; do echo Waiting for network...; sleep 5; done"
        echo "Installing prerequisites (curl, ca-certificates) in $NODE..."
        lxc exec "$NODE" -- bash -c "sudo apt-get update -y && sudo apt-get install -y curl ca-certificates"
        echo "Configuring time sync in $NODE..."
        lxc exec "$NODE" -- bash -c "sudo timedatectl set-timezone UTC && sudo apt-get update -y && sudo apt-get install -y chrony && sudo systemctl enable --now chrony"
        echo "Disabling firewall (ufw) in $NODE to ensure k3s networking works correctly..."
        lxc exec "$NODE" -- bash -c "if command -v ufw >/dev/null 2>&1; then sudo ufw disable; else echo 'ufw not found, skipping.'; fi"
        echo "Enabling iptables on bridged traffic in $NODE..."
        lxc exec "$NODE" -- bash -c '\
            sudo modprobe br_netfilter; \
            echo "net.bridge.bridge-nf-call-iptables=1" | sudo tee /etc/sysctl.d/k8s.conf; \
            sudo sysctl --system; \
        '
    else
        echo "VM $NODE already exists, skipping."
    fi
done

echo "All VM nodes have been created and prepared for k3s."
