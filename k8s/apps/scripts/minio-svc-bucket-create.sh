#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
	printf '%s\n' 'usage: minio-svc-bucket-create.sh <environment> <service> <bucket>' >&2
	exit 2
fi
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd -P)
python=${PYTHON:-"$repository_root/.venv/bin/python"}
"$repository_root/scripts/python/runtime_check.sh" "$python"
ENV_NAME=$1 SVC_NAME=$2 BUCKET_NAME=$3 PYTHONDONTWRITEBYTECODE=1 \
	exec "$python" "$repository_root/k8s/scripts/admin/dispatch.py" minio bucket
