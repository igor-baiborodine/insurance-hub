#!/bin/bash

set -euo pipefail

ENV_NAME="$1"
SVC_NAME="$2"
BUCKET_NAME="$3"

NAMESPACE="${ENV_NAME}-minio-${SVC_NAME}"
echo "Using NAMESPACE=${NAMESPACE}"
MINIO_TENANT_ALIAS="${NAMESPACE}"
MINIO_STORAGE_CONFIG_SECRET_NAME="${NAMESPACE}-storage-config"

if [ "${ENV_NAME}" = "qa" ]; then
    MINIO_SCHEMA="https"
else
    MINIO_SCHEMA="http"
fi

echo "Retrieving MinIO root user credentials from secret '${MINIO_STORAGE_CONFIG_SECRET_NAME}'..."
MINIO_ROOT_USER=$(kubectl get secret "${MINIO_STORAGE_CONFIG_SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.config\.env}' | base64 --decode | grep MINIO_ROOT_USER | cut -d'"' -f2)
MINIO_ROOT_PASSWORD=$(kubectl get secret "${MINIO_STORAGE_CONFIG_SECRET_NAME}" -n "${NAMESPACE}" -o jsonpath='{.data.config\.env}' | base64 --decode | grep MINIO_ROOT_PASSWORD | cut -d'"' -f2)
if [ -z "${MINIO_ROOT_USER}" ] || [ -z "${MINIO_ROOT_PASSWORD}" ]; then
    echo "ERROR: Failed to retrieve MinIO root user credentials from secret '${MINIO_STORAGE_CONFIG_SECRET_NAME}'." >&2
    exit 1
fi

MINIO_CONSOLE_POD_NAME_WITH_PREFIX=$(kubectl get pods -n "${NAMESPACE}" -l v1.min.io/tenant="${NAMESPACE}" -o name | head -n 1)
if [ -z "${MINIO_CONSOLE_POD_NAME_WITH_PREFIX}" ]; then
    echo "ERROR: MinIO console pod not found in namespace '${NAMESPACE}'. Is the tenant deployed for '${SVC_NAME}'?" >&2
    exit 1
fi
MINIO_CONSOLE_POD_NAME="${MINIO_CONSOLE_POD_NAME_WITH_PREFIX#pod/}"

MINIO_S3_API_HOSTNAME="${NAMESPACE}-hl"
echo "Adding MinIO alias for tenant '${SVC_NAME}' S3 API (if not existing)..."
kubectl exec -it "${MINIO_CONSOLE_POD_NAME}" -n "${NAMESPACE}" -- \
  mc alias set "${MINIO_TENANT_ALIAS}" "${MINIO_SCHEMA}://${MINIO_S3_API_HOSTNAME}:9000" "${MINIO_ROOT_USER}" "${MINIO_ROOT_PASSWORD}" --insecure || true

echo "Creating bucket '${BUCKET_NAME}'..."
kubectl exec -it "${MINIO_CONSOLE_POD_NAME}" -n "${NAMESPACE}" -- \
  mc mb "${MINIO_TENANT_ALIAS}/${BUCKET_NAME}" --insecure || true # '|| true' allows it to not fail if bucket already exists
kubectl exec -it "${MINIO_CONSOLE_POD_NAME}" -n "${NAMESPACE}" -- mc ls "${MINIO_TENANT_ALIAS}" --insecure

echo "✅ Bucket '${BUCKET_NAME}' created for tenant '${SVC_NAME}'."
