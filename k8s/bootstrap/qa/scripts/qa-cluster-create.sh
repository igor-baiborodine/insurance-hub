#!/bin/bash

set -euo pipefail

QA_CLUSTER_NAME="qa-insurance-hub"
MASTER_NODE="qa-master"
WORKER_NODES=("qa-worker1" "qa-worker2")

MASTER_ONLY="${MASTER_ONLY:-false}"

echo "MASTER_ONLY=$MASTER_ONLY"

echo "Waiting for $MASTER_NODE to get a valid IPv4 address..."
MASTER_IP=""
for i in {1..20}; do
  MASTER_IP=$(lxc list "$MASTER_NODE" -c 4 --format json | \
    jq -r '.[0].state.network | to_entries | map(.value.addresses // []) | add | map(select(.family=="inet" and .scope=="global" and .address != "127.0.0.1")) | .[0].address')
  [ -n "$MASTER_IP" ] && break
  echo "Waiting for $MASTER_NODE IP address..."
  sleep 5
done
if [ -z "$MASTER_IP" ]; then
  echo "Failed to get $MASTER_NODE IP address!" >&2
  exit 1
fi
echo "Master IP: $MASTER_IP"

MASTER_IFACE=$(lxc list "$MASTER_NODE" -c 4 --format json | \
  jq -r '.[0].state.network | to_entries[] | select(.value.addresses[].family == "inet" and .value.addresses[].scope == "global") | .key')
if [ -z "$MASTER_IFACE" ]; then
  echo "Failed to get $MASTER_NODE network interface!" >&2
  exit 1
fi
echo "Master Interface: $MASTER_IFACE"

echo "Setting up Rancher k3s cluster on $MASTER_NODE node..."

lxc exec "$MASTER_NODE" -- bash -c "curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC='server --disable=traefik --write-kubeconfig-mode 644 --tls-san=$MASTER_IP --flannel-iface=$MASTER_IFACE' sh -"

echo "Waiting for k3s server on $MASTER_NODE to become active..."
lxc exec "$MASTER_NODE" -- bash -c 'for i in {1..20}; do systemctl is-active k3s && break || (echo Waiting for k3s server...; sleep 5); done'

if [[ "$MASTER_ONLY" != "true" ]]; then
  TOKEN=$(lxc exec "$MASTER_NODE" -- sudo cat /var/lib/rancher/k3s/server/node-token)
  echo "Node Token: $TOKEN"

  for WORKER in "${WORKER_NODES[@]}"; do
    echo "Installing k3s agent on $WORKER..."
    lxc exec "$WORKER" -- bash -c "curl -sfL https://get.k3s.io | K3S_URL=https://$MASTER_IP:6443 K3S_TOKEN=$TOKEN INSTALL_K3S_EXEC='agent --flannel-iface=$MASTER_IFACE' sh -"
  done
else
  echo "MASTER_ONLY=true, skipping worker node installation."
fi

echo "Waiting for nodes to register in the cluster..."
lxc exec "$MASTER_NODE" -- bash -c 'for i in {1..20}; do kubectl get nodes &>/dev/null && break || (echo Waiting for nodes...; sleep 5); done'

if [[ "$MASTER_ONLY" == "true" ]]; then
  echo "Waiting for master node to appear in the cluster..."
  lxc exec "$MASTER_NODE" -- bash -c '
    for i in {1..24}; do
      COUNT=$(kubectl get nodes --no-headers 2>/dev/null | wc -l || echo 0)
      [ "$COUNT" -ge 1 ] && echo "Master node is registered." && break
      echo "Waiting for master node..."
      sleep 5
    done
  '
else
  echo "Waiting for all nodes to appear in the cluster..."
  lxc exec "$MASTER_NODE" -- bash -c '
    for i in {1..24}; do
      COUNT=$(kubectl get nodes --no-headers 2>/dev/null | wc -l || echo 0)
      [ "$COUNT" -ge 3 ] && echo "All nodes are registered." && break
      echo "Waiting for all nodes..."
      sleep 5
    done
  '
fi

echo "Waiting for $MASTER_NODE to generate kubeconfig..."
for i in {1..24}; do
  if lxc exec "$MASTER_NODE" -- bash -c "test -s /etc/rancher/k3s/k3s.yaml"; then break; fi
  echo "Waiting for /etc/rancher/k3s/k3s.yaml to be ready..."
  sleep 5
done

echo "Waiting for CoreDNS addon to become ready..."
lxc exec "$MASTER_NODE" -- bash -c '
  for i in {1..24}; do
    READY=$(kubectl -n kube-system get deploy coredns -o jsonpath="{.status.readyReplicas}" 2>/dev/null || echo 0);
    [ "$READY" != "" ] && [ "$READY" -ge 1 ] && echo "CoreDNS is Ready." && break;
    echo "Waiting for CoreDNS to be Ready...";
    sleep 5;
  done;
'

echo "Applying custom CoreDNS configuration to fix DNS resolution..."
lxc exec "$MASTER_NODE" -- kubectl apply -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: coredns-custom
  namespace: kube-system
data:
  rewrite.override: |
    rewrite name kubernetes.default kubernetes.default.svc.cluster.local
EOF

if [[ "$MASTER_ONLY" == "true" ]]; then
  echo "QA cluster '$QA_CLUSTER_NAME' has been successfully created with master node only."
else
  echo "QA cluster '$QA_CLUSTER_NAME' has been successfully created with master and 2 worker nodes."
fi