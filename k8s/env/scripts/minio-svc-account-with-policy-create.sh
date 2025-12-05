#!/bin/bash
set -euo pipefail

SVC_NAME="$1"
ENV_NAME="$2"
POLICY_FILE="$3"

NAMESPACE="${ENV_NAME}-minio-${SVC_NAME}"
echo "Using NAMESPACE=${NAMESPACE}"
MINIO_TENANT_ALIAS="${NAMESPACE}"
MINIO_CONFIG_SECRET_NAME="${NAMESPACE}-storage-config"

echo "Retrieving MinIO root credentials from secret '${MINIO_CONFIG_SECRET_NAME}' in namespace '${NAMESPACE}'..."
# Extract and base64 decode MINIO_ROOT_USER
MINIO_ROOT_USER=$(kubectl get secret "${MINIO_CONFIG_SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.config\.env}' | base64 -d | grep MINIO_ROOT_USER | cut -d'"' -f2)
# Extract and base64 decode MINIO_ROOT_PASSWORD
MINIO_ROOT_PASSWORD=$(kubectl get secret "${MINIO_CONFIG_SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.config\.env}' | base64 -d | grep MINIO_ROOT_PASSWORD | cut -d'"' -f2)

if [ -z "${MINIO_ROOT_USER}" ] || [ -z "${MINIO_ROOT_PASSWORD}" ]; then
    echo "ERROR: Failed to retrieve MinIO root credentials from secret '${MINIO_CONFIG_SECRET_NAME}'." >&2
    exit 1
fi

MINIO_CONSOLE_POD_NAME=$(kubectl get pods -n "${NAMESPACE}" -l v1.min.io/tenant="${NAMESPACE}" -o name | head -n 1)
if [ -z "${MINIO_CONSOLE_POD_NAME}" ]; then
    echo "ERROR: MinIO console pod not found in namespace '${NAMESPACE}'. Is the tenant deployed for '${SVC_NAME}'?" >&2
    exit 1
fi

MINIO_S3_API_HOSTNAME="${NAMESPACE}-hl" # Headless service DNS name

echo "Adding MinIO alias for tenant '${SVC_NAME}' S3 API (if not existing)..."
kubectl exec -it "${MINIO_CONSOLE_POD_NAME}" -n "${NAMESPACE}" -- \
  mc alias set "${MINIO_TENANT_ALIAS}" "http://${MINIO_S3_API_HOSTNAME}:9000" "${MINIO_ROOT_USER}" "${MINIO_ROOT_PASSWORD}" || true

POLICY_NAME=$(basename "${POLICY_FILE}" .json)

echo "Copying local policy file '${POLICY_FILE}' into MinIO console pod '${MINIO_CONSOLE_POD_NAME}'..."
kubectl cp "${POLICY_FILE}" "${NAMESPACE}/${MINIO_CONSOLE_POD_NAME}:/tmp/policy.json"

echo "Applying MinIO policy '${POLICY_NAME}'..."
kubectl exec -it "${MINIO_CONSOLE_POD_NAME}" -n "${NAMESPACE}" -- \
  mc admin policy add "${MINIO_TENANT_ALIAS}" "${POLICY_NAME}" /tmp/policy.json

echo "Attaching policy '${POLICY_NAME}' to user '${SVC_NAME}' for tenant in namespace '${NAMESPACE}'..."
kubectl exec -it "${MINIO_CONSOLE_POD_NAME}" -n "${NAMESPACE}" -- \
  mc admin policy set "${MINIO_TENANT_ALIAS}" "${POLICY_NAME}" user="${SVC_NAME}"

echo "✅ Policy '${POLICY_NAME}' applied and attached to user '${SVC_NAME}'."