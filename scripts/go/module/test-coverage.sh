#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_root=$(CDPATH= cd -- "$script_dir/../../.." && pwd -P)
python=${PYTHON:-$repository_root/.venv/bin/python}
exec "$python" -m pytest -q -m 'not integration and not acceptance' \
	"$script_dir/test_module_tooling.py"
