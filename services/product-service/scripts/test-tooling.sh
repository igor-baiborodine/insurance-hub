#!/bin/sh
set -eu
python=${COMMON_GO_TOOLING_PYTHON:?COMMON_GO_TOOLING_PYTHON is required}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
module_root=$(CDPATH= cd -- "$script_dir/.." && pwd -P)
repository_root=${PRODUCT_TOOLING_REPOSITORY_ROOT:-$(CDPATH= cd -- "$script_dir/../../.." && pwd -P)}
tooling_root=${TOOLING_REPOSITORY_ROOT:-$repository_root}

PYTHONDONTWRITEBYTECODE=1 "$python" -c \
  'import platform; raise SystemExit(0 if (platform.python_implementation(), platform.python_version()) == ("CPython", "3.14.8") else "test-tooling requires CPython 3.14.8")'
PYTHONPATH="$tooling_root" exec "$python" -m scripts.go.service.tooling_runner \
  --config "$module_root/go-service-tooling.json" \
  --repository-root "$repository_root" --tooling-root "$tooling_root" suite
