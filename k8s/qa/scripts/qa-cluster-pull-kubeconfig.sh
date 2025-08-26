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
    jq -r '.[0].state.network | to_entries | map(.value.addresses // []) | add
      | map(select(.family=="inet" and .address != "127.0.0.1")) | .[0].address')
  [ -n "$MASTER_IP" ] && break
  echo "Waiting for qa-master IP address..."
  sleep 5
done
echo "Master IP: $MASTER_IP"
sed -i "s/127.0.0.1/$MASTER_IP/g" "./${QA_CLUSTER_NAME}-kubeconfig"

# Rename context to qa-insurance-hub
ORIGINAL_CONTEXT=$(kubectl --kubeconfig="./${QA_CLUSTER_NAME}-kubeconfig" config current-context || echo "")
echo "Original kubeconfig context is '$ORIGINAL_CONTEXT'"
if [ "$ORIGINAL_CONTEXT" != "$QA_KUBECTL_CONTEXT" ] && [ -n "$ORIGINAL_CONTEXT" ]; then
  kubectl --kubeconfig="./${QA_CLUSTER_NAME}-kubeconfig" config rename-context "$ORIGINAL_CONTEXT" "$QA_KUBECTL_CONTEXT"
fi

echo "Successfully pulled kubeconfig from qa-master and renamed context to '$QA_KUBECTL_CONTEXT'"
