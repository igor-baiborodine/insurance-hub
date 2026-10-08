#!/usr/bin/env bash
set -euo pipefail

readonly supported_suites="smoke happy data-edges failures access db-permissions comparator all"

if [[ -z "${BASELINE_SUITE:-}" ]]; then
  echo "ERROR: BASELINE_SUITE is required. Supported suites: ${supported_suites}." >&2
  exit 2
fi

case " ${supported_suites} " in
  *" ${BASELINE_SUITE} "*) ;;
  *)
    echo "ERROR: Unsupported BASELINE_SUITE '${BASELINE_SUITE}'. Supported suites: ${supported_suites}." >&2
    exit 2
    ;;
esac
