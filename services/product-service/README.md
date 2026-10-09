# Product catalog read service

This standalone module is the Go Product pilot. It owns its build, management shell, tested direct
HTTP routing boundary, and generated Product v1 gRPC contract. Catalog application logic, the gRPC
adapter, and PostgreSQL behavior will be added in the remaining issue-132 steps. The executable
currently exposes only `GET /livez` and `GET /readyz` on the management listener. Readiness
returns `503` because no Product reader or business listener is wired yet. Do not route Product
traffic to this shell.

The module path is
`github.com/igor-baiborodine/insurance-hub/services/product-service`. It resolves with
`GOWORK=off` and has no filesystem replacement or dependency on the scaffold module. Go 1.27.2 is
required. From the repository root, use its owning Makefile:

```sh
make -C services/product-service bootstrap-tools
make -C services/product-service format FORMAT_SCOPE=all
make -C services/product-service check FORMAT_SCOPE=all
make -C services/product-service build
make -C services/product-service run
```

The direct HTTP boundary is exercised on a real loopback listener with test-owned callbacks:
`make -C services/product-service test TEST_PACKAGES=./internal/http`. The business listener is
not wired into the executable yet.

The Product v1 schema is `api/product/v1/product_service.proto`; generated Go bindings are under
`gen/product/v1`. The frozen initial compatibility baseline lives in
`testdata/contract-baseline`, separate from the scaffold Echo baseline. Use `format-proto`,
`update-proto-deps`, and `gen-proto` only when intentionally changing the schema or generated
output; `check` verifies format, lint, reproducible generation, and breaking compatibility.

`bootstrap-tools`, `update-deps`, `format`, and the protobuf maintenance targets are explicit
mutating targets. `check` is
non-mutating and currently covers tool/config verification, all handwritten Go formatting,
lint/vet, all-package race tests, executable build, dependency reproduction, and vulnerability
analysis, plus Product v1 protobuf format, lint, drift, and breaking checks. SQL checks join this
target when that capability is implemented. Tools are pinned in this Makefile and installed under
ignored `.tools/bin/`.
Normal checks use readonly module resolution. `FORMAT_SCOPE=changed` is the default for local
formatting; `FORMAT_SCOPE=all` checks every eligible handwritten Go file.

The current shell accepts `SERVICE_NAME` (default `product-service`), `HEALTH_ADDR` (default
`127.0.0.1:8080`), `LOG_LEVEL` (default `info`), `SHUTDOWN_TIMEOUT` (default `10s`), `OTEL_ENABLED`
(default `false`), `OTEL_EXPORTER_OTLP_ENDPOINT` (required only when telemetry is enabled), and
`OTEL_EXPORTER_OTLP_TIMEOUT` (default `2s`). Duration values use Go duration syntax. The
configuration loader rejects invalid values with setting-name diagnostics. Telemetry is disabled
by default and needs no collector. The full business configuration and architecture handoff will
be documented after the corresponding readers, transports, and lifecycle behavior exist.
