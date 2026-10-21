#!/bin/sh
set -eu

if [ "$#" -ne 3 ]; then
	printf '%s\n' 'usage: minio-svc-user-with-policy-create.sh <service> <environment> <policy-file>' >&2
	exit 2
fi
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd -P)
python=${PYTHON:-"$repository_root/.venv/bin/python"}
"$repository_root/scripts/python/runtime_check.sh" "$python"
SVC_NAME=$1 ENV_NAME=$2 POLICY_FILE=$3 PYTHONDONTWRITEBYTECODE=1 \
	exec "$python" "$repository_root/k8s/scripts/admin/dispatch.py" minio user-policy
