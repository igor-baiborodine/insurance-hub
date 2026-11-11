#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_root=${PRODUCT_TOOLING_REPOSITORY_ROOT:-$(CDPATH= cd -- "$script_dir/../../.." && pwd -P)}
python=${PYTHON:-$repository_root/.venv/bin/python}
module_root=$(CDPATH= cd -- "$script_dir/.." && pwd -P)
PYTHONPATH="$repository_root" exec "$python" -m scripts.go.service.image \
  --config "$module_root/go-service-tooling.json" build "$@"
