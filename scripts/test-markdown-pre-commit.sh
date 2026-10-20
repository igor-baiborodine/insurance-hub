#!/bin/sh
set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
python=${PYTHON:-$repository_root/.venv/bin/python}
exec "$python" -m pytest -q "$repository_root/scripts/test_markdown_pre_commit.py"
