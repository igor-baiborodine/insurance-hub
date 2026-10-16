#!/usr/bin/env bash

set -euo pipefail

go_command=${GO:?GO is required}
command -v mvn >/dev/null 2>&1 || { echo 'test-telemetry-continuity: Maven is required' >&2; exit 1; }
command -v docker >/dev/null 2>&1 || { echo 'test-telemetry-continuity: Docker is required' >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo 'test-telemetry-continuity: Docker daemon is unavailable' >&2; exit 1; }
java_version=$(java -version 2>&1 | head -n 1)
[[ $java_version == *'"14.'* ]] || { echo 'test-telemetry-continuity: Java 14 is required' >&2; exit 1; }

result_file=$(mktemp "${TMPDIR:-/tmp}/product-continuity.XXXXXX")
trap 'rm -f -- "$result_file"' EXIT
PRODUCT_CONTINUITY_REQUIRED=1 GOWORK=off "$go_command" test -mod=readonly -count=1 -race \
	-tags=integration -json -run '^TestProductContinuity$' ./internal/testing | tee "$result_file"
if ! awk '/"Action":"pass"/ && /"Test":"TestProductContinuity"/ { found=1 } END { exit !found }' "$result_file"; then
	echo 'test-telemetry-continuity: continuity test was not selected or did not pass' >&2
	exit 1
fi
