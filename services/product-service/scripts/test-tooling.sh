#!/bin/sh
set -eu
python=${PYTHON:?PYTHON is required}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_root=${PRODUCT_TOOLING_REPOSITORY_ROOT:-$(CDPATH= cd -- "$script_dir/../../.." && pwd -P)}
cd "$repository_root"
exec "$python" -m pytest -q -p no:cacheprovider \
  scripts/product_baseline/test_compare_http.py \
  scripts/product_baseline/test_fixtures.py \
  scripts/product_security/test_validators.py \
  scripts/postgres/service/test_reader.py \
  services/product-service/scripts/test_check_access_results.py \
  services/product-service/scripts/test_deployment_operations.py \
  services/product-service/scripts/test_product_tooling.py \
  -m 'not integration and not acceptance'
