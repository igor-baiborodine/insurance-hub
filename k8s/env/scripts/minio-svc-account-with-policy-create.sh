#!/bin/bash
set -euo pipefail

SVC_NAME="$1"
ENV_NAME="$2"
POLICY_FILE="$3"

NAMESPACE="${ENV_NAME}-minio-${SVC_NAME}"
echo "Using NAMESPACE=${NAMESPACE}"
MINIO_TENANT_ALIAS="${NAMESPACE}"
MINIO_STORAGE_CONFIG_SECRET_NAME="${NAMESPACE}-storage-config"
MINIO_SVC_CREDS_SECRET_NAME="${NAMESPACE}-svc-creds"

echo "Retrieving MinIO root credentials from secret '${MINIO_STORAGE_CONFIG_SECRET_NAME}' in namespace '${NAMESPACE}'..."
MINIO_ROOT_USER=$(kubectl get secret "${MINIO_STORAGE_CONFIG_SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.config\.env}' | base64 -d | grep MINIO_ROOT_USER | cut -d'"' -f2)
MINIO_ROOT_PASSWORD=$(kubectl get secret "${MINIO_STORAGE_CONFIG_SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.config\.env}' | base64 -d | grep MINIO_ROOT_PASSWORD | cut -d'"' -f2)
if [ -z "${MINIO_ROOT_USER}" ] || [ -z "${MINIO_ROOT_PASSWORD}" ]; then
    echo "ERROR: Failed to retrieve MinIO root user credentials from secret '${MINIO_STORAGE_CONFIG_SECRET_NAME}'." >&2
    exit 1
fi

echo "Retrieving service account credentials from secret '${MINIO_SVC_CREDS_SECRET_NAME}' in namespace '${NAMESPACE}'..."
MINIO_SVC_ACCESS_KEY=$(kubectl get secret "${MINIO_SVC_CREDS_SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.MINIO_SVC_ACCESS_KEY}' | base64 -d)
MINIO_SVC_SECRET_KEY=$(kubectl get secret "${MINIO_SVC_CREDS_SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.MINIO_SVC_SECRET_KEY}' | base64 -d)
if [ -z "${MINIO_SVC_ACCESS_KEY}" ] || [ -z "${MINIO_SVC_SECRET_KEY}" ]; then
    echo "ERROR: Failed to retrieve MinIO service account credentials from secret '${MINIO_SVC_CREDS_SECRET_NAME}'." >&2
    exit 1
fi

MINIO_CONSOLE_POD_NAME=$(kubectl get pods -n "${NAMESPACE}" -l v1.min.io/tenant="${NAMESPACE}" -o name | head -n 1)
if [ -z "${MINIO_CONSOLE_POD_NAME}" ]; then
    echo "ERROR: MinIO console pod not found in namespace '${NAMESPACE}'. Is the tenant deployed for '${SVC_NAME}'?" >&2
    exit 1
fi

MINIO_S3_API_HOSTNAME="${NAMESPACE}-hl"
echo "Adding MinIO alias for tenant '${SVC_NAME}' S3 API (if not existing)..."
kubectl exec -it "${MINIO_CONSOLE_POD_NAME}" -n "${NAMESPACE}" -- \
  mc alias set "${MINIO_TENANT_ALIAS}" "http://${MINIO_S3_API_HOSTNAME}:9000" "${MINIO_ROOT_USER}" "${MINIO_ROOT_PASSWORD}" || true

echo "Creating MinIO service account for user '${MINIO_SVC_ACCESS_KEY}'..."
kubectl exec -it "${MINIO_CONSOLE_POD_NAME}" -n "${NAMESPACE}" -- \
  mc admin user add "${MINIO_TENANT_ALIAS}" "${MINIO_SVC_ACCESS_KEY}" "${MINIO_SVC_SECRET_KEY}" || true

POLICY_NAME=$(basename "${POLICY_FILE}" .json)
echo "Copying local policy file '${POLICY_FILE}' into MinIO console pod '${MINIO_CONSOLE_POD_NAME}'..."
kubectl cp "${POLICY_FILE}" "${NAMESPACE}/${MINIO_CONSOLE_POD_NAME}:/tmp/policy.json"

echo "Applying MinIO policy '${POLICY_NAME}'..."
kubectl exec -it "${MINIO_CONSOLE_POD_NAME}" -n "${NAMESPACE}" -- \
  mc admin policy add "${MINIO_TENANT_ALIAS}" "${POLICY_NAME}" /tmp/policy.json

echo "Attaching policy '${POLICY_NAME}' to user '${MINIO_SVC_ACCESS_KEY}' for tenant in namespace '${NAMESPACE}'..."
kubectl exec -it "${MINIO_CONSOLE_POD_NAME}" -n "${NAMESPACE}" -- \
  mc admin policy set "${MINIO_TENANT_ALIAS}" "${POLICY_NAME}" user="${MINIO_SVC_ACCESS_KEY}"

echo "✅ MinIO service account '${MINIO_SVC_ACCESS_KEY}' created and policy '${POLICY_NAME}' applied."