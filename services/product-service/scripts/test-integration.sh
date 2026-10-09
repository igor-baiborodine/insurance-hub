#!/usr/bin/env bash

set -euo pipefail

go_command=${GO:?GO is required}
docker_command=${DOCKER:?DOCKER is required}
postgres_image=${POSTGRES_IMAGE:?POSTGRES_IMAGE is required}
integration_suite=${INTEGRATION_SUITE:-}
fixture_set=${FIXTURE_SET:-}

fail() {
	printf 'test-integration: %s\n' "$*" >&2
	exit 1
}

case "$integration_suite" in
	all | harness) test_pattern='^TestPostgresHarness$' ;;
	'') fail 'INTEGRATION_SUITE must not be empty' ;;
	*) fail "unknown INTEGRATION_SUITE: $integration_suite" ;;
esac
if [[ ${PRODUCT_TEST_FORCE_ZERO_SELECTION:-0} == 1 ]]; then
	test_pattern='^NoSuchIntegrationTest$'
fi

case "$fixture_set" in
	qa) ;;
	'') fail 'FIXTURE_SET must not be empty' ;;
	*) fail "unknown FIXTURE_SET: $fixture_set" ;;
esac

if [[ ${PRODUCT_DATABASE_URL+x} == x ]]; then
	fail 'PRODUCT_DATABASE_URL must be unset; integration tests own their disposable database'
fi
command -v "$docker_command" >/dev/null 2>&1 || fail 'Docker CLI is required'
"$docker_command" info >/dev/null 2>&1 || fail 'Docker daemon is unavailable'

result_file=$(mktemp "${TMPDIR:-/tmp}/product-service-integration.XXXXXX")
cleanup() {
	rm -f -- "$result_file"
}
trap cleanup EXIT

PRODUCT_TEST_POSTGRES_IMAGE="$postgres_image" \
	PRODUCT_INTEGRATION_FIXTURE_SET="$fixture_set" \
	GOWORK=off "$go_command" test -mod=readonly -count=1 -race -tags=integration \
	-json -run "$test_pattern" ./internal/testing | tee "$result_file"

executed_count=$(
	awk '
		/"Action":"run"/ && /"Test":"TestPostgresHarness/ { count++ }
		END { print count + 0 }
	' "$result_file"
)
if [[ "$executed_count" -eq 0 ]]; then
	fail "suite $integration_suite selected zero tests"
fi
printf 'test-integration: suite=%s fixture-set=%s executed-tests=%s image=%s\n' \
	"$integration_suite" "$fixture_set" "$executed_count" "$postgres_image"
