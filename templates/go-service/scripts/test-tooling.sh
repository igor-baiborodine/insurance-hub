#!/bin/sh

set -eu

python=${COMMON_GO_TOOLING_PYTHON:?COMMON_GO_TOOLING_PYTHON is required}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

PYTHONDONTWRITEBYTECODE=1 "$python" -c \
  'import platform; raise SystemExit(0 if (platform.python_implementation(), platform.python_version()) == ("CPython", "3.14.8") else "test-tooling requires CPython 3.14.8")'
exec "$python" "$script_dir/tooling_runner.py"
