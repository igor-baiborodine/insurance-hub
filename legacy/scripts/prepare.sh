#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
root=$(CDPATH= cd -- "$script_dir/../.." && pwd -P)
python=${PYTHON:-"$root/.venv/bin/python"}
"$root/scripts/python/runtime_check.sh" "$python"
PYTHONPATH="$root/scripts/legacy" exec "$python" "$script_dir/prepare.py" "$@"
