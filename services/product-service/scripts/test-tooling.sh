#!/usr/bin/env bash

set -euo pipefail

common_tooling_dir=${COMMON_GO_TOOLING_DIR:?COMMON_GO_TOOLING_DIR is required}
# shellcheck source=/dev/null
source "$common_tooling_dir/test-tooling-lib.sh"
tooling_initialize product-service-tooling \
	buf protoc-gen-go protoc-gen-go-grpc golangci-lint govulncheck sqlc mmdc
ln -s "$module_root/.tools/puppeteer" "$base_fixture/.tools/puppeteer"

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

fixture=$(new_fixture telemetry-zero-selection)
expect_failure telemetry-zero-selection test-integration \
	'suite telemetry selected zero tests' "$fixture" INTEGRATION_SUITE=telemetry \
	PRODUCT_TEST_FORCE_ZERO_SELECTION=1

fixture=$(new_fixture telemetry-missing-case)
mkdir -p "$fixture/.test-bin"
cat >"$fixture/.test-bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
build) exit 0 ;;
test)
	printf '%s\n' \
		'{"Action":"run","Test":"TestProductTelemetry"}' \
		'{"Action":"pass","Test":"TestProductTelemetry"}' \
		'{"Action":"pass","Test":"TestProductTelemetry/real_HTTP_and_gRPC_reads_export_one_request_observation_each"}' \
		'{"Action":"pass","Test":"TestProductTelemetry/replacement_process_has_new_metric_identity_and_cumulative_start"}' \
		'{"Action":"pass","Test":"TestProductTelemetry/disabled_mode_makes_no_exporter_connection"}' \
		'{"Action":"pass","Test":"TestProductTelemetry/refused_collector_does_not_affect_reads_or_probes"}' \
		'{"Action":"pass","Test":"TestProductTelemetry/collector_recovery_exports_without_a_Product_restart"}'
	;;
*) exit 64 ;;
esac
EOF
chmod +x "$fixture/.test-bin/go"
expect_failure telemetry-missing-case test-integration \
	'suite telemetry did not pass telemetry case SIGTERM_bounds_an_in-flight_export' \
	"$fixture" INTEGRATION_SUITE=telemetry GO="$fixture/.test-bin/go" DOCKER=true

fixture=$(new_fixture reader-missing-case)
mkdir -p "$fixture/.test-bin"
cat >"$fixture/.test-bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
test)
	printf '%s\n' \
		'{"Action":"run","Test":"TestPostgresReader"}' \
		'{"Action":"pass","Test":"TestPostgresReader"}'
	;;
*) exit 64 ;;
esac
EOF
chmod +x "$fixture/.test-bin/go"
expect_failure reader-missing-case test-integration \
	'suite reader did not pass database telemetry case' \
	"$fixture" INTEGRATION_SUITE=reader GO="$fixture/.test-bin/go" DOCKER=true

fixture=$(new_fixture go-bootstrap)
mkdir -p "$fixture/.test-bin"
cat >"$fixture/.test-bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'go %s\n' "$*" >>"${BOOTSTRAP_TRACE:?BOOTSTRAP_TRACE is required}"
case "${1:-}" in
install) exit 0 ;;
env)
	[[ "${2:-}" == "GOVERSION" ]]
	printf 'go1.27.2\n'
	;;
*) exit 64 ;;
esac
EOF
cat >"$fixture/.test-bin/forbidden-non-go-tool" <<'EOF'
#!/usr/bin/env bash
printf 'forbidden non-Go tool invoked: %s\n' "$0" >&2
exit 65
EOF
chmod +x "$fixture/.test-bin/go" "$fixture/.test-bin/forbidden-non-go-tool"
bootstrap_trace="$temporary_root/logs/go-bootstrap.trace"
expect_success go-bootstrap bootstrap-go-tools "$fixture" \
	GO="$fixture/.test-bin/go" \
	NODE="$fixture/.test-bin/forbidden-non-go-tool" \
	NPM="$fixture/.test-bin/forbidden-non-go-tool" \
	MMDC="$fixture/.test-bin/forbidden-non-go-tool" \
	PYTHON="$fixture/.test-bin/forbidden-non-go-tool" \
	DOCKER="$fixture/.test-bin/forbidden-non-go-tool" \
	KUBECTL="$fixture/.test-bin/forbidden-non-go-tool" \
	JAVA="$fixture/.test-bin/forbidden-non-go-tool" \
	BOOTSTRAP_TRACE="$bootstrap_trace"
grep -Fq 'go install github.com/bufbuild/buf/cmd/buf@' "$bootstrap_trace" ||
	tooling_fail 'bootstrap-go-tools did not invoke the Go tool installer'

fixture=$(new_fixture docs-broken-link)
printf '\n[Controlled missing link](controlled-missing.md)\n' >>"$fixture/README.md"
expect_failure docs-broken-link check-docs 'missing local link target' "$fixture" \
	DOC_FILES=README.md

fixture=$(new_fixture docs-missing-puppeteer-config)
rm -- "$fixture/puppeteer-config.json"
expect_failure docs-missing-puppeteer-config check-docs \
	'missing Puppeteer configuration' "$fixture" DOC_FILES=README.md

fixture=$(new_fixture docs-missing-sandbox-argument)
sed -i '/--no-sandbox/d' "$fixture/puppeteer-config.json"
expect_failure docs-missing-sandbox-argument check-docs \
	'missing required Chromium launch argument --no-sandbox' "$fixture" DOC_FILES=README.md

fixture=$(new_fixture docs-invalid-mermaid)
cat >"$fixture/controlled-invalid-mermaid.md" <<'EOF'
# Controlled invalid Mermaid

```mermaid
controlled invalid diagram
```
EOF
expect_failure docs-invalid-mermaid check-docs 'Mermaid render failed' "$fixture" \
	DOC_FILES=controlled-invalid-mermaid.md

printf 'test-tooling: Product controlled failures were rejected without fixture mutation\n'
