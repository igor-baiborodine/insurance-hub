#!/bin/bash

set -euo pipefail

QA_CLUSTER_NAME="qa-insurance-hub"
QA_KUBECTL_CONTEXT="$QA_CLUSTER_NAME"

echo "Pulling kubeconfig from qa-master to host..."
lxc file pull qa-master/etc/rancher/k3s/k3s.yaml "./${QA_CLUSTER_NAME}-kubeconfig"

# Adjust kubeconfig server address from 127.0.0.1 to actual master IP
MASTER_IP=""
for i in {1..20}; do
  MASTER_IP=$(lxc list qa-master -c 4 --format json | \
    jq -r '.[0].state.network.enp5s0.addresses[] | select(.family=="inet" and .address != "127.0.0.1") | .address' | head -n1)
  if [ -n "$MASTER_IP" ]; then
    break
  fi
  echo "Waiting for valid master IP address..."
  sleep 5
done

if [ -z "$MASTER_IP" ]; then
  echo "Failed to get valid master IP address" >&2
  exit 1
fi

echo "Master IP: $MASTER_IP"
sed -i "s/127.0.0.1/$MASTER_IP/g" "./${QA_CLUSTER_NAME}-kubeconfig"

# Rename context to qa-insurance-hub
ORIGINAL_CONTEXT=$(kubectl --kubeconfig="./${QA_CLUSTER_NAME}-kubeconfig" config current-context 2>/dev/null || echo "")
echo "Original kubeconfig context is '$ORIGINAL_CONTEXT'"

if [ "$ORIGINAL_CONTEXT" != "$QA_KUBECTL_CONTEXT" ] && [ -n "$ORIGINAL_CONTEXT" ]; then
  kubectl --kubeconfig="./${QA_CLUSTER_NAME}-kubeconfig" config rename-context "$ORIGINAL_CONTEXT" "$QA_KUBECTL_CONTEXT"
fi

# Merge the updated kubeconfig into the default kubeconfig
export KUBECONFIG=$HOME/.kube/config:./${QA_CLUSTER_NAME}-kubeconfig
kubectl config view --flatten > $HOME/.kube/config.merged
mv $HOME/.kube/config.merged $HOME/.kube/config

echo "Successfully pulled kubeconfig from qa-master, updated server IP and merged kubeconfig."
