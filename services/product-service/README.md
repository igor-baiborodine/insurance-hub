# Product catalog read service

This standalone module is the Go Product pilot. The current step establishes its independently
owned build and management shell. Catalog HTTP, gRPC, and PostgreSQL behavior will be added in the
remaining issue-132 steps. The executable currently exposes only `GET /livez` and `GET /readyz` on
the management listener. Readiness returns `503` because no Product reader or business listener is
wired yet. Do not route Product traffic to this shell.

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

`bootstrap-tools`, `update-deps`, and `format` are explicit mutating targets. `check` is
non-mutating and currently covers tool/config verification, all handwritten Go formatting,
lint/vet, all-package race tests, executable build, dependency reproduction, and vulnerability
analysis. The Product v1 protobuf and SQL checks join this target as those capabilities are
implemented. Tools are pinned in this Makefile and installed under ignored `.tools/bin/`.
Normal checks use readonly module resolution. `FORMAT_SCOPE=changed` is the default for local
formatting; `FORMAT_SCOPE=all` checks every eligible handwritten Go file.

The current shell accepts `SERVICE_NAME` (default `product-service`), `HEALTH_ADDR` (default
`127.0.0.1:8080`), `LOG_LEVEL` (default `info`), `SHUTDOWN_TIMEOUT` (default `10s`), `OTEL_ENABLED`
(default `false`), `OTEL_EXPORTER_OTLP_ENDPOINT` (required only when telemetry is enabled), and
`OTEL_EXPORTER_OTLP_TIMEOUT` (default `2s`). Duration values use Go duration syntax. The
configuration loader rejects invalid values with setting-name diagnostics. Telemetry is disabled
by default and needs no collector. The full business configuration and architecture handoff will
be documented after the corresponding readers, transports, and lifecycle behavior exist.
