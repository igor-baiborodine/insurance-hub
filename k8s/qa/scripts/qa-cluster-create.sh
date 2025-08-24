#!/bin/bash
set -euo pipefail

QA_CLUSTER_NAME="qa-insurance-hub"
QA_KUBECTL_CONTEXT="$QA_CLUSTER_NAME"

echo "Setting up Rancher k3s cluster on qa-master node..."

# Install k3s server on master
lxc exec qa-master -- bash -c "curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC='--write-kubeconfig-mode 644' sh -"

echo "Waiting for k3s server on qa-master to become active..."
lxc exec qa-master -- bash -c 'for i in {1..20}; do systemctl is-active k3s && break || (echo Waiting for k3s server...; sleep 5); done'

echo "Waiting for qa-master to get a valid IPv4 address and node token..."
MASTER_IP=""
for i in {1..20}; do
  MASTER_IP=$(lxc list qa-master -c 4 --format json | \
    jq -r '.[0].state.network | to_entries | map(.value.addresses // []) | add | map(select(.family=="inet" and .scope=="global" and .address != "127.0.0.1")) | .[0].address')
  [ -n "$MASTER_IP" ] && break
  echo "Waiting for qa-master IP address..."
  sleep 5
done
if [ -z "$MASTER_IP" ]; then
  echo "Failed to get qa-master IP address!" >&2
  exit 1
fi
echo "Master IP: $MASTER_IP"

TOKEN=$(lxc exec qa-master -- sudo cat /var/lib/rancher/k3s/server/node-token)
echo "Node Token: $TOKEN"

for WORKER in qa-worker1 qa-worker2; do
  echo "Installing k3s agent on $WORKER..."
  lxc exec "$WORKER" -- bash -c "curl -sfL https://get.k3s.io | K3S_URL=https://$MASTER_IP:6443 K3S_TOKEN=$TOKEN sh -"
done

echo "Waiting for all nodes to register in the cluster..."
lxc exec qa-master -- bash -c 'for i in {1..20}; do kubectl get nodes &>/dev/null && break || (echo Waiting for nodes...; sleep 5); done'

echo "Waiting for qa-master to generate kubeconfig..."
for i in {1..24}; do
  if lxc exec qa-master -- bash -c "test -s /etc/rancher/k3s/k3s.yaml"; then break; fi
  echo "Waiting for /etc/rancher/k3s/k3s.yaml to be ready..."
  sleep 5
done

echo "Pulling kubeconfig from qa-master to host..."
lxc file pull qa-master/etc/rancher/k3s/k3s.yaml "./${QA_CLUSTER_NAME}-kubeconfig"

# Adjust kubeconfig server address from 127.0.0.1 to actual master IP
MASTER_IP=""
for i in {1..20}; do
  MASTER_IP=$(lxc list qa-master -c 4 --format json | \
    jq -r '.[0].state.network | to_entries | map(.value.addresses // []) | add | map(select(.family=="inet" and .scope=="global" and .address != "127.0.0.1")) | .[0].address')
  [ -n "$MASTER_IP" ] && break
  echo "Waiting for qa-master IP address..."
  sleep 5
done
if [ -z "$MASTER_IP" ]; then
  echo "Failed to get qa-master IP address!" >&2
  exit 1
fi
echo "Master IP: $MASTER_IP"
sed -i "s/127.0.0.1/$MASTER_IP/g" "./${QA_CLUSTER_NAME}-kubeconfig"

# Rename context to qa-insurance-hub
ORIGINAL_CONTEXT=$(kubectl --kubeconfig="./${QA_CLUSTER_NAME}-kubeconfig" config current-context || echo "")
echo "Original kubeconfig context is '$ORIGINAL_CONTEXT'"
if [ "$ORIGINAL_CONTEXT" != "$QA_KUBECTL_CONTEXT" ] && [ -n "$ORIGINAL_CONTEXT" ]; then
  kubectl --kubeconfig="./${QA_CLUSTER_NAME}-kubeconfig" config rename-context "$ORIGINAL_CONTEXT" "$QA_KUBECTL_CONTEXT"
fi

echo "Merging ${QA_CLUSTER_NAME}-kubeconfig into default kubeconfig (~/.kube/config)..."
export KUBECONFIG=$HOME/.kube/config:$(pwd)/${QA_CLUSTER_NAME}-kubeconfig
kubectl config view --flatten > $HOME/.kube/config.merged && mv $HOME/.kube/config.merged $HOME/.kube/config

echo "Merged kubeconfigs. You can now switch contexts with kubectl config use-context ${QA_KUBECTL_CONTEXT}."
