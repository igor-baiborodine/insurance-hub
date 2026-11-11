#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_root=${PRODUCT_TOOLING_REPOSITORY_ROOT:-$(CDPATH= cd -- "$script_dir/../../.." && pwd -P)}
python=${PYTHON:-$repository_root/.venv/bin/python}
PYTHONPATH="$repository_root:$script_dir" exec "$python" "$script_dir/integration_test.py" "$@"
