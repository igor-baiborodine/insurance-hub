#!/bin/sh
set -eu

PYTHONPATH="${INSURANCE_HUB_ROOT}" PYTHONDONTWRITEBYTECODE=1 \
exec "${PYTHON}" -m scripts.go.service.tooling_runner \
  --config "${MODULE_ROOT}/go-service-tooling.json" \
  --repository-root "${INSURANCE_HUB_ROOT}" \
  --tooling-root "${INSURANCE_HUB_ROOT}" suite
