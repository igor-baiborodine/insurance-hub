#!/usr/bin/env bash

set -euo pipefail

common_tooling_dir=${COMMON_GO_TOOLING_DIR:?COMMON_GO_TOOLING_DIR is required}
# shellcheck source=/dev/null
source "$common_tooling_dir/test-tooling-lib.sh"
tooling_initialize product-service-tooling \
	buf protoc-gen-go protoc-gen-go-grpc golangci-lint govulncheck sqlc

fixture=$(new_fixture format-new-file)
printf 'package config\n\nfunc controlled( ) { }\n' >"$fixture/internal/config/controlled.go"
expect_failure format-new-file format-check 'internal/config/controlled.go' "$fixture" \
	FORMAT_FILES=internal/config/controlled.go

fixture=$(new_fixture dependency)
sed -i 's/google.golang.org\/grpc v1\.83\.2/google.golang.org\/grpc v1.84.0/' \
	"$fixture/go.mod"
expect_failure dependency check-deps 'go.mod is not synchronized with update-deps' "$fixture"

fixture=$(new_fixture proto-breaking)
sed -i '/string code = 1;/s/= 1;/= 20;/' \
	"$fixture/api/product/v1/product_service.proto"
expect_failure proto-breaking check-proto-breaking 'Previously present field "1"' "$fixture"

fixture=$(new_fixture proto-generated-modified)
printf '\n// controlled drift\n' >>"$fixture/gen/product/v1/product_service.pb.go"
expect_failure proto-generated-modified check-proto-drift \
	'generated output differs from regeneration' "$fixture"

fixture=$(new_fixture proto-generated-unexpected)
printf 'controlled unexpected output\n' >"$fixture/gen/product/v1/unexpected.txt"
expect_failure proto-generated-unexpected check-proto-drift \
	'unexpected output in generated tree' "$fixture"

fixture=$(new_fixture proto-generated-missing)
rm -- "$fixture/gen/product/v1/product_service_grpc.pb.go"
expect_failure proto-generated-missing check-proto-drift 'missing expected output' "$fixture"

fixture=$(new_fixture sql-generated-modified)
printf '\n// controlled drift\n' >>"$fixture/internal/postgres/dbgen/queries.sql.go"
expect_failure sql-generated-modified check-sql-drift \
	'generated output differs from regeneration' "$fixture"

fixture=$(new_fixture sql-generated-unexpected)
printf 'controlled unexpected output\n' >"$fixture/internal/postgres/dbgen/unexpected.txt"
expect_failure sql-generated-unexpected check-sql-drift \
	'unexpected output in generated tree' "$fixture"

fixture=$(new_fixture sql-generated-missing)
rm -- "$fixture/internal/postgres/dbgen/querier.go"
expect_failure sql-generated-missing check-sql-drift 'missing expected output' "$fixture"

fixture=$(new_fixture integration-empty-selection)
expect_failure integration-empty-selection test-integration \
	'INTEGRATION_SUITE must not be empty' "$fixture" INTEGRATION_SUITE=

fixture=$(new_fixture integration-zero-selection)
expect_failure integration-zero-selection test-integration \
	'suite all selected zero tests' "$fixture" PRODUCT_TEST_FORCE_ZERO_SELECTION=1

printf 'test-tooling: Product controlled failures were rejected without fixture mutation\n'
