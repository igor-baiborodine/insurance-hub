#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_root=$(CDPATH= cd -- "$script_dir/.." && pwd -P)
python=${PYTHON:-$repository_root/.venv/bin/python}
"$repository_root/scripts/python/runtime_check.sh" "$python"
exec "$python" "$script_dir/markdown_toc.py" "$@"
