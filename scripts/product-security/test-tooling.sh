#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_root=$(CDPATH= cd -- "$script_dir/../.." && pwd -P)
python=${PYTHON:-$repository_root/.venv/bin/python}
cd "$repository_root"
exec "$python" -m pytest -q \
  scripts/product_security/test_validators.py \
  services/product-service/scripts/test_check_access_results.py
