# Configuration Strategy for Insurance Hub Phase 4 Go Services

## Executive recommendation

Phase 4 should **replace Viper as the default with a small, environment-first configuration package built around a typed Go struct, `caarlos0/env/v11`, explicit validation, and one-time startup loading**. Kubernetes and Kustomize should remain responsible for producing environment-specific values: ConfigMaps for non-confidential settings and Secrets for credentials. The Go service should parse those values once, validate them, construct its dependencies, and then pass immutable configuration values only to the components that need them.

This is a better fit than Viper for Insurance Hub because the target architecture already specifies Kubernetes-native ConfigMaps and Secrets, 12-factor environment configuration, explicit composition, and fewer framework-level abstractions. Kubernetes can inject ConfigMap and Secret keys as environment variables, while Kustomize generators provide deterministic environment overlays and content-hashed resource names. The application therefore does not need a general-purpose configuration framework that merges defaults, files, flags, environment variables, and remote stores at runtime.[^1][^2][^3][^4]

Use the standard library directly instead of `caarlos0/env` only if avoiding a small reflection-based dependency is more important than eliminating repetitive parsing. `os.LookupEnv`, `strconv`, `time.ParseDuration`, and `net/url` are sufficient, but every service would otherwise reimplement required-value handling, type conversion, aggregation of errors, and tests. `caarlos0/env` is deliberately narrow, has no dependencies, maps environment variables into structs, supports required/non-empty fields, defaults, prefixes, common Go types, custom parsers, and injected environment maps for tests.[^5][^6][^7][^8]

Do not add live configuration reload to the baseline. Configuration changes should create a new Kustomize-generated ConfigMap or Secret name, modify the Pod template reference, and trigger a controlled rolling deployment. Kustomize appends content hashes and rewrites recognized references by default. This produces observable, reversible releases and avoids different replicas applying configuration at different times.[^4][^9]

## Current proposal

The migration analysis currently maps Micronaut YAML/properties configuration to “Viper for handling configuration from files, env vars, etc.” Elsewhere, however, the target state is more specific: service configuration should come through the environment, Kubernetes should manage configuration and secrets through ConfigMaps and Secrets, and Go services should favor standard-library or focused libraries with explicit composition.[^1]

The broader Viper capability set is therefore not a requirement. Viper supports defaults, several file formats, environment variables, flags, remote key/value stores, live file watching, and explicit value overrides; its documented precedence is explicit `Set`, flags, environment, files, remote stores, then defaults. Those features are valuable for CLI applications or products that genuinely need layered runtime configuration, but they create a second configuration-composition system inside each service in addition to Kustomize and Kubernetes.[^10][^11][^12]

## Recommended ownership model

Configuration management has three distinct layers. They should not be collapsed into one library:

| Layer | Responsibility | Insurance Hub mechanism |
|---|---|---|
| Deployment configuration | Select values for local, QA, and later production; version and review changes | Kustomize bases and overlays, Flux GitOps |
| Secret delivery | Protect and inject passwords, tokens, certificates, and keys | SOPS-encrypted Git content or an external secret store, materialized as Kubernetes Secrets |
| Process configuration | Parse injected strings, apply safe defaults, validate invariants, expose typed immutable values | Small Go `config` package using `caarlos0/env/v11` |

Kubernetes identifies ConfigMaps as the mechanism for non-confidential key/value configuration and Secrets as the mechanism for confidential data. ConfigMaps do not provide secrecy or encryption, while Kubernetes recommends encryption at rest, least-privilege RBAC, restricted container access, and consideration of external secret stores for Secrets.[^2][^13][^14]

The process configuration package should not know about Kubernetes, Kustomize, SOPS, Flux, Vault, or the Kubernetes API. It reads a process environment contract. This preserves local execution and makes the application portable while keeping deployment concerns in the deployment repository.

## Option comparison

| Option | Sources | Typed result | Complexity | Main trade-off | Phase 4 fit |
|---|---|---|---|---|---|
| `caarlos0/env/v11` | Environment; file contents through explicit `,file` fields | Struct tags, common types, custom parsers | Low; zero dependencies[^6][^8] | Reflection and tag-based schema | **Recommended default** |
| Standard library | Environment and flags | Fully explicit Go | Lowest dependency count | Repeated conversion, required-value, and error-aggregation code | Good for a shared internal loader or very small binaries |
| `sethvargo/go-envconfig` | Environment or an arbitrary lookup function | Struct tags and decoders | Low | More extensible lookup model than needed | Strong alternative |
| `cleanenv` | YAML/JSON/TOML/ENV files plus environment overrides | Struct tags | Medium | Encourages file-and-env layering even when production is env-only | Consider only if config files are a real requirement |
| Koanf | Modular providers for env, files, flags, Vault, S3, Consul, etc.; parsers for multiple formats | Can unmarshal into structs | Medium to high | Flexible source merging becomes application-owned policy | Best Viper-like alternative, but unnecessary now |
| Viper | Defaults, files, env, flags, remote stores, live watching | Map-style access or struct unmarshalling | High | Broad feature set, precedence and merge semantics, larger behavioral surface | Not recommended as default |
| Confita | Cascading env, files, flags, etcd, Consul, Vault, SSM | Struct tags | Medium to high | Runtime backend cascade couples services to config infrastructure | Not recommended |
| `godotenv` | `.env` files copied into process environment | No typed schema | Low | Local bootstrap only; not a full configuration solution | Optional developer tooling, preferably outside app code |

## Why not Viper

Viper is maintained and capable; rejection is based on fit, not project quality. Its broad source support can be appropriate where users choose configuration files and flags or where an application intentionally combines several runtime stores.[^11][^12]

For these Kubernetes microservices, its costs outweigh those capabilities:

- **Duplicate precedence:** Kustomize already selects deployment values; Viper introduces a second precedence graph across defaults, files, environment, flags, remote stores, and explicit sets.[^11]
- **Dynamic key store:** Viper’s general model is a case-insensitive key/value hierarchy. Typed structs are available, but the application must still govern bindings and unmarshalling behavior.[^15][^10]
- **Merge edge cases:** Viper does not deep-merge complex overridden values; a complex value is replaced in full.[^11]
- **Environment subtleties:** Viper treats environment names as case-sensitive, empty environment values as unset unless configured otherwise, and reads environment values when accessed rather than caching them.[^11]
- **Testing risk:** Viper’s own documentation discourages its package-level singleton because it makes testing harder and can cause unexpected behavior.[^12]
- **Unneeded runtime features:** Watching local configuration files or remote key/value stores is unnecessary when configuration changes are deployed through Flux/Kustomize and rolled out as a release.

If Viper were retained, it should at least be instantiated with `viper.New()`, used only during bootstrap, unmarshalled with exact checking into a typed struct, and never exposed to business packages. That mitigation still provides little benefit over a narrower environment parser.

## Why caarlos0/env

`caarlos0/env/v11` has the right abstraction boundary: it converts the already-composed process environment into a typed struct and then stops. It supports built-in scalar types, `time.Duration`, URLs, pointers, slices, maps, custom parsers, required and non-empty checks, defaults, nested prefixes, and test-provided environment maps. Its repository describes the library as feature-complete, and the current v11 line remains maintained.[^6][^7][^16][^8]

The library should be treated as a bootstrap implementation detail:

1. Define a service-specific `Config` struct.
2. Initialize safe defaults in Go or tags.
3. Parse the process environment once.
4. Run explicit semantic validation.
5. Return the complete immutable value.
6. Log only a redacted summary.
7. Pass sub-configurations or concrete values through constructors.

Do not call the environment parser from repositories, gRPC handlers, Kafka consumers, or domain services. Do not retain access to a mutable global configuration registry. This is the configuration equivalent of the manual dependency-injection recommendation: values enter through the composition root and become explicit dependencies.

## Standard library alternative

The standard library is enough to build a loader. `os.LookupEnv` distinguishes an unset variable from a variable deliberately set to an empty string, unlike `os.Getenv`, which returns an empty string for both cases. `strconv` handles numeric and Boolean conversion, `time.ParseDuration` handles durations, and `net/url` parses endpoints.[^5]

A standard-library-only implementation is attractive if the project wants absolute control over syntax and error messages:

```go
func requiredEnv(name string) (string, error) {
    value, ok := os.LookupEnv(name)
    if !ok {
        return "", fmt.Errorf("%s is required", name)
    }
    if strings.TrimSpace(value) == "" {
        return "", fmt.Errorf("%s must not be empty", name)
    }
    return value, nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
    raw, ok := os.LookupEnv(name)
    if !ok {
        return fallback, nil
    }
    value, err := time.ParseDuration(raw)
    if err != nil {
        return 0, fmt.Errorf("%s: %w", name, err)
    }
    return value, nil
}
```

The drawback is multiplication: seven services will need required strings, optional strings, integers with ranges, durations, URLs, string slices, Boolean values, secret-file handling, aggregated errors, and environment-map injection for tests. If this option is selected, implement it once as a very small internal module with no service-specific keys. Otherwise, use `caarlos0/env` and keep service-specific validation local.

## Other alternatives

### sethvargo/go-envconfig

`sethvargo/go-envconfig` populates structs from environment variables or arbitrary lookup functions and supports mutators and decoders. The abstract lookup interface is useful when values need to come from a map, test fixture, or custom source without mutating the process environment.[^17][^18][^19]

It is a strong second choice. `caarlos0/env` gets the recommendation because its scope, zero-dependency design, active v11 line, required/non-empty behavior, file indirection, and test options directly cover the proposed contract. If custom lookup composition becomes central, `sethvargo/go-envconfig` should be reevaluated.

### Koanf

Koanf is the most credible alternative when the actual requirement is “Viper, but more modular.” It separates providers from parsers, supports files, environment variables, flags, S3, Vault, Consul, etcd, and other sources, and recursively merges nested maps by default. Version 2 splits providers and parsers into separate modules, so applications import only the pieces they use.[^20][^21][^22][^23]

That flexibility is not currently needed. Adding Koanf would still move source precedence and merge policy into each service. Adopt it only if Phase 4 discovers a proven requirement for layered structured configuration—for example, a large mounted rules file plus a small set of environment overrides—that cannot be represented clearly through environment variables and explicit file paths.

### cleanenv

Cleanenv reads YAML, JSON, TOML, or ENV files into a struct and then lets environment values override file values. It deliberately favors a structured configuration value instead of a dynamic key store, making it smaller and more explicit than Viper.[^24][^25][^26]

It is a good option for a non-Kubernetes binary whose primary contract is `config.yaml + environment overrides`. For Insurance Hub, the production contract should be environment variables plus explicit file mounts only where the payload is naturally a file. Using cleanenv across every service would reintroduce an application-level file precedence model without a demonstrated need.

### Confita

Confita cascades multiple backends—including environment, files, flags, etcd, Consul, Vault, and AWS SSM—into a struct. This is useful when an application must retrieve configuration directly from infrastructure services.[^27]

Insurance Hub services should instead receive secrets and configuration from Kubernetes. Direct runtime dependencies on Vault, Consul, SSM, or the Kubernetes API complicate startup, credentials, failure handling, local tests, and secret rotation. An operator or GitOps controller should materialize values into a Kubernetes Secret; the application contract remains an environment variable or mounted file.

### godotenv

Godotenv loads `.env` files into the process environment or returns them as a map. It does not provide a typed schema or semantic validation, so it complements rather than replaces the recommended loader.[^28][^29]

Prefer shell or development tooling—`direnv`, Make targets, Docker Compose `env_file`, or the `godotenv` CLI—to load local variables before starting the service. If the application loads `.env` itself, do so only when explicitly enabled for local development; never silently search for `.env` in production. Commit only a non-secret `.env.example`, not an operational `.env` file.

### kelseyhightower/envconfig

The older `kelseyhightower/envconfig` package supports required and default struct tags and remains recognizable. For a new Phase 4 standard, prefer a library with a current versioned API and active release line, such as `caarlos0/env/v11`, rather than selecting an older package based mainly on familiarity.[^30]

## Configuration contract

Each executable should own one typed root configuration. The struct is an internal bootstrap concern, not a shared platform-wide mega-schema.

```go
package config

import (
    "errors"
    "fmt"
    "net/url"
    "time"

    "github.com/caarlos0/env/v11"
)

type Config struct {
    Service     ServiceConfig
    GRPC        GRPCConfig
    PostgreSQL  PostgreSQLConfig
    MinIO       MinIOConfig
    Observability ObservabilityConfig
}

type ServiceConfig struct {
    Environment string `env:"ENVIRONMENT" envDefault:"local"`
    LogLevel    string `env:"LOG_LEVEL" envDefault:"info"`
}

type GRPCConfig struct {
    Address         string        `env:"GRPC_ADDRESS" envDefault:":8080"`
    ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"20s"`
}

type PostgreSQLConfig struct {
    URL             *url.URL     `env:"POSTGRES_URL,required"`
    MaxConnections  int32        `env:"POSTGRES_MAX_CONNECTIONS" envDefault:"20"`
    MinConnections  int32        `env:"POSTGRES_MIN_CONNECTIONS" envDefault:"2"`
    ConnectTimeout  time.Duration `env:"POSTGRES_CONNECT_TIMEOUT" envDefault:"5s"`
}

type MinIOConfig struct {
    Endpoint  string `env:"MINIO_ENDPOINT,required"`
    Region    string `env:"MINIO_REGION" envDefault:"us-east-1"`
    Bucket    string `env:"MINIO_BUCKET,required"`
    AccessKey string `env:"MINIO_ACCESS_KEY,required"`
    SecretKey string `env:"MINIO_SECRET_KEY,required,unset"`
    UseTLS    bool   `env:"MINIO_USE_TLS" envDefault:"true"`
}

type ObservabilityConfig struct {
    OTLPEndpoint string  `env:"OTEL_EXPORTER_OTLP_ENDPOINT,required"`
    SampleRatio  float64 `env:"OTEL_TRACES_SAMPLER_ARG" envDefault:"1.0"`
}

func Load() (Config, error) {
    cfg, err := env.ParseAsWithOptions[Config](env.Options{
        RequiredIfNoDef: true,
    })
    if err != nil {
        return Config{}, fmt.Errorf("parse configuration: %w", err)
    }
    if err := cfg.Validate(); err != nil {
        return Config{}, fmt.Errorf("validate configuration: %w", err)
    }
    return cfg, nil
}

func (c Config) Validate() error {
    var errs []error

    if c.PostgreSQL.MinConnections > c.PostgreSQL.MaxConnections {
        errs = append(errs, errors.New(
            "POSTGRES_MIN_CONNECTIONS must not exceed POSTGRES_MAX_CONNECTIONS",
        ))
    }
    if c.PostgreSQL.MaxConnections < 1 {
        errs = append(errs, errors.New(
            "POSTGRES_MAX_CONNECTIONS must be at least 1",
        ))
    }
    if c.GRPC.ShutdownTimeout <= 0 {
        errs = append(errs, errors.New(
            "SHUTDOWN_TIMEOUT must be greater than zero",
        ))
    }
    if c.Observability.SampleRatio < 0 || c.Observability.SampleRatio > 1 {
        errs = append(errs, errors.New(
            "OTEL_TRACES_SAMPLER_ARG must be between 0 and 1",
        ))
    }

    return errors.Join(errs...)
}
```

The exact OpenTelemetry variables should follow the OpenTelemetry SDK’s supported environment contract where possible instead of being renamed behind application-specific keys. Component-owned standards reduce custom parsing and make operational behavior recognizable.

`RequiredIfNoDef` is useful as a strict policy but must be applied carefully: fields that receive defaults in Go code rather than tags need matching treatment. An alternative is to mark only external resource handles and credentials as `required` and make optionality explicit field by field.

## Validation rules

Parsing answers “is this value syntactically an integer or duration?” Validation answers “is this complete configuration safe to run?” Keep them separate.

Validate at startup:

- Required values are present and non-empty.
- URLs include permitted schemes and required host components.
- Durations and numeric ranges are operationally sensible.
- Minimum pool sizes do not exceed maximums.
- TLS policy matches the environment; production-like environments should not accidentally disable it.
- Mutually exclusive settings are not enabled together.
- Required file paths exist and have expected permissions where mounted files are used.
- Service-specific invariants are satisfied, such as a MinIO bucket being provided when document storage is enabled.

Configuration errors should terminate startup before readiness succeeds. Return all independent validation errors with `errors.Join` rather than making an operator fix one variable per restart. Do not attempt to contact PostgreSQL or MinIO inside pure configuration validation; connectivity belongs to dependency construction and readiness checks.

## Defaults policy

Use defaults only for values that are safe, environment-independent, and unsurprising:

- Listener address.
- Graceful-shutdown timeout.
- Log format.
- Conservative connection-pool limits.
- Feature behavior that is safe when omitted.

Do not default credentials, database URLs, Kafka brokers, MinIO endpoints, bucket names, or service identity. A default such as `localhost` can hide a broken Kubernetes manifest and delay failure until runtime. For production-sensitive controls—TLS disabling, destructive maintenance modes, authentication bypasses—require an explicit value or validate against the deployment environment.

Defaults belong in one place. Prefer Go/config tags for true application defaults and Kustomize overlays for deployment-specific values. Do not define the same default independently in Go, a Dockerfile, a base ConfigMap, and several overlays.

## Secrets

Use Kubernetes Secrets for confidential values and ConfigMaps only for non-confidential values. Existing Insurance Hub decisions already use Kustomize, ConfigMaps, Kubernetes Secrets, and encrypted GitOps secret handling.[^31][^32][^33][^13][^14][^2]

For ordinary credentials, inject individual Secret keys through `secretKeyRef` rather than importing an entire Secret with `envFrom`. Explicit references document least privilege and prevent an unrelated new key from silently entering the process environment. Kubernetes supports both individual `env[].valueFrom.secretKeyRef` and whole-source `envFrom` injection.[^13][^3]

For certificates, private keys, or secrets that must rotate without appearing directly in the environment, mount a Secret as a read-only volume and configure the application with a non-secret path. `caarlos0/env` also supports reading field contents from a path using its `,file` option, but explicit file-reading code may be preferable when rotation and reload semantics matter.[^6]

Never log the root config with `%+v`, serialize it to diagnostics, expose it through health endpoints, or include configuration values indiscriminately in traces. Provide an explicit `Redacted()` or `SafeSummary()` method that includes only operationally useful non-secret fields.

## Kubernetes injection

Recommended Deployment pattern:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: document-service
spec:
  template:
    spec:
      containers:
        - name: document-service
          image: document-service:latest
          envFrom:
            - configMapRef:
                name: document-service-config
          env:
            - name: POSTGRES_URL
              valueFrom:
                secretKeyRef:
                  name: document-service-database
                  key: url
            - name: MINIO_ACCESS_KEY
              valueFrom:
                secretKeyRef:
                  name: document-service-minio
                  key: access-key
            - name: MINIO_SECRET_KEY
              valueFrom:
                secretKeyRef:
                  name: document-service-minio
                  key: secret-key
```

Kubernetes supports ConfigMap and Secret values as environment variables or mounted files. `envFrom` is reasonable for a ConfigMap whose complete purpose is the service’s public configuration contract; explicit `secretKeyRef` is preferable for secrets.[^3][^2][^13]

Recommended Kustomize base:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - deployment.yaml
  - service.yaml

configMapGenerator:
  - name: document-service-config
    literals:
      - ENVIRONMENT=qa
      - LOG_LEVEL=info
      - GRPC_ADDRESS=:8080
      - SHUTDOWN_TIMEOUT=20s
      - POSTGRES_MAX_CONNECTIONS=20
      - POSTGRES_MIN_CONNECTIONS=2
      - POSTGRES_CONNECT_TIMEOUT=5s
      - MINIO_ENDPOINT=minio.storage.svc.cluster.local:9000
      - MINIO_BUCKET=documents
      - MINIO_REGION=us-east-1
      - MINIO_USE_TLS=false
      - OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy.observability.svc:4317
      - OTEL_TRACES_SAMPLER_ARG=1.0
```

Keep Kustomize’s name-suffix hash enabled. Generated ConfigMaps and Secrets receive content-hash suffixes, and Kustomize updates recognized workload references; changed content therefore changes the Pod template and causes a rollout. This behavior matches a startup-only configuration snapshot.[^9][^4]

## Files versus environment

The twelve-factor methodology recommends granular, orthogonal environment variables for values that vary between deploys. That does not mean forcing every structured artifact into one enormous environment string.[^34]

Use environment variables for:

- Endpoints and resource handles.
- Ports and listener addresses.
- Timeouts, pool sizes, and retry limits.
- Log levels and bounded feature switches.
- Credentials delivered from Secret keys.
- Paths to mounted files.

Use mounted files for:

- TLS certificates, certificate bundles, and private keys.
- Large structured templates.
- Policy documents or rules whose natural interface is a document.
- Vendor SDK credentials that require a file.
- Configuration too large or complex to remain understandable as independent variables.

When a ConfigMap is consumed as environment variables, running processes do not receive updates; a Pod rollout is required. Mounted ConfigMap files are eventually updated, but the application sees their new meaning only if it polls or watches and reloads them; `subPath` mounts do not receive updates. Consequently, choosing a file mount does not by itself justify hot reload.[^14][^35][^36][^37]

## Reload policy

Default policy: **configuration is immutable for the lifetime of a process**. Apply changes using Flux and Kustomize as a new release, let Kubernetes perform a rolling update, and use readiness probes to protect traffic.

This policy provides:

- One complete validated snapshot per process.
- Consistent dependency construction.
- Rollback through Git and Deployment history.
- Auditability of the exact configuration used by a Pod revision.
- No data races around mutable configuration.
- No partially-applied changes across components.

Hot reload should require a separate ADR and a proven operational need. If adopted, define which fields are reloadable, validate a complete candidate before atomic replacement, expose success/failure metrics, retain the last valid snapshot, and specify behavior across replicas. Connection strings, listener ports, credentials tied to established clients, and transaction settings are usually safer to roll through new Pods.

## Flags

The Go standard `flag` package is sufficient for operational command-line switches. Long-running services should use very few flags because Kubernetes manifests already define the process environment.[^38][^39]

Appropriate flags include:

- `--version`
- `--check-config`
- `--migrate` only if the architecture deliberately keeps migration execution in the binary
- `--config-doc` to print the configuration contract

Do not support the same setting simultaneously through a flag, environment variable, and config file unless a documented precedence requirement exists. Avoid passing secrets as command-line arguments.

## Local development

Use the same environment contract locally. Provide `.env.example` containing keys and safe sample values, but keep `.env` out of Git. Let Make, Docker Compose, or a shell environment loader populate variables before the Go process starts.

Example:

```makefile
.PHONY: run-document
run-document:
	@set -a; \
	. ./deploy/local/document-service.env; \
	set +a; \
	go run ./cmd/document-service
```

Alternatively, `godotenv` can load `.env` values and preserves already-set environment variables by default. Prefer this as developer tooling instead of an automatic production code path. This keeps behavior explicit and avoids a binary silently changing its source precedence because a file happens to exist in the working directory.[^40][^28]

## Testing strategy

Configuration requires its own table-driven tests. `caarlos0/env` accepts an explicit environment map through parser options, so tests do not need global `os.Setenv` mutation.[^7][^6]

```go
func TestLoad(t *testing.T) {
    tests := []struct {
        name    string
        env     map[string]string
        wantErr string
    }{
        {
            name: "valid",
            env: map[string]string{
                "POSTGRES_URL":                 "postgres://app:secret@db:5432/documents",
                "MINIO_ENDPOINT":               "minio:9000",
                "MINIO_BUCKET":                 "documents",
                "MINIO_ACCESS_KEY":             "access",
                "MINIO_SECRET_KEY":             "secret",
                "OTEL_EXPORTER_OTLP_ENDPOINT":  "http://alloy:4317",
            },
        },
        {
            name: "invalid pool bounds",
            env: map[string]string{
                "POSTGRES_URL":                 "postgres://app:secret@db:5432/documents",
                "POSTGRES_MIN_CONNECTIONS":     "30",
                "POSTGRES_MAX_CONNECTIONS":     "20",
                "MINIO_ENDPOINT":               "minio:9000",
                "MINIO_BUCKET":                 "documents",
                "MINIO_ACCESS_KEY":             "access",
                "MINIO_SECRET_KEY":             "secret",
                "OTEL_EXPORTER_OTLP_ENDPOINT":  "http://alloy:4317",
            },
            wantErr: "POSTGRES_MIN_CONNECTIONS",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            cfg, err := parse(env.Options{Environment: tt.env})
            if tt.wantErr != "" {
                require.ErrorContains(t, err, tt.wantErr)
                return
            }
            require.NoError(t, err)
            require.NotZero(t, cfg.GRPC.ShutdownTimeout)
        })
    }
}
```

Test these categories:

- Minimal valid environment.
- Every required variable missing.
- Required variable present but empty.
- Invalid Boolean, integer, float, duration, and URL syntax.
- Boundary values and cross-field invariants.
- Defaults.
- Secret redaction.
- Unknown-variable behavior if the project adds an allowlist check.
- Rendered Kustomize manifests against the declared configuration contract.

Add a CI check that extracts or generates the expected variable list from the config schema and compares it with Kustomize overlays. The `caarlos0/env` ecosystem includes field metadata that can support documentation generation, and the project points to `envdoc` for generating environment-variable documentation from tags.[^6]

## Startup integration

Load configuration at the top of `main`, before constructing clients or starting goroutines:

```go
func run(ctx context.Context) error {
    cfg, err := config.Load()
    if err != nil {
        return err
    }

    logger := logging.New(cfg.Service.LogLevel)
    logger.Info("configuration loaded", "config", cfg.SafeSummary())

    pool, err := postgres.NewPool(ctx, cfg.PostgreSQL)
    if err != nil {
        return fmt.Errorf("create postgres pool: %w", err)
    }
    defer pool.Close()

    objectStore, err := objectstore.New(cfg.MinIO)
    if err != nil {
        return fmt.Errorf("create object store: %w", err)
    }

    app := application.New(
        postgres.NewRepository(pool),
        objectStore,
    )

    return server.Run(ctx, cfg.GRPC, app)
}
```

Pass `PostgreSQLConfig` to the PostgreSQL adapter, `MinIOConfig` to the object-storage adapter, and server settings to the server. Avoid passing the root `Config` everywhere; doing so hides dependencies and makes unrelated settings available to every package.

## Service-by-service fit

| Service | Configuration characteristics | Recommendation |
|---|---|---|
| `document-service` | PostgreSQL, MinIO, gRPC, chromedp/PDF, observability | First configuration pilot; environment variables plus mounted CA/cert files if required |
| `product-service` | PostgreSQL, JSONB query tuning, gRPC, observability | Environment-only startup config; product definitions remain data, not deployment config |
| `dashboard-service` | PostgreSQL, Elasticsearch, gRPC gateway | Typed nested config for two clients; do not put dashboard definitions into env vars |
| `policy-search-service` | Elasticsearch and gRPC | Small environment-only contract; good validation test case |
| `chat-service` | PostgreSQL, WebSocket/gRPC limits, Kafka if used | Typed durations and connection limits; bounded switches only |
| `pricing-service` | PostgreSQL and expression engine | Engine safety limits in config; tariff rules remain versioned database data |
| `policy-service` | PostgreSQL, Kafka, gRPC, security, observability | Strict required settings and fail-fast validation; no silent endpoint defaults |
| `payment-service` | PostgreSQL, Kafka, MinIO, external integrations | Explicit secret references, TLS files where needed, strongest redaction and validation |

Business data must not drift into process configuration. Insurance product definitions, tariff rules, dashboard layouts, policy data, and feature state requiring audit history belong in PostgreSQL or a dedicated control plane. Environment configuration should remain deploy-level wiring and operational policy.

## Pilot plan

Use `document-service`, already identified as the first low-risk Phase 4 migration, to establish the standard:

1. Inventory every Micronaut property and classify it as code constant, deploy configuration, secret, business data, or obsolete compatibility setting.
2. Define a typed Go `Config` and explicit validation rules.
3. Implement parsing with `caarlos0/env/v11`.
4. Build Kustomize ConfigMap and Secret references for local and QA overlays.
5. Keep generator hash suffixes enabled and verify that a config change updates the Deployment Pod template.[^4][^9]
6. Add table-driven loader tests and a rendered-manifest contract check.
7. Verify that logs, traces, errors, diagnostics, and `/health` endpoints never disclose secret values.
8. Document each variable, default, sensitivity classification, and restart behavior.
9. Reuse the pattern for `product-service` and adjust the standard only when evidence reveals a gap.

Evaluate Koanf or cleanenv only if the pilot proves that structured configuration files are a real cross-service requirement. Do not choose a broad framework preemptively.

## Decision triggers

| Observed requirement | Response |
|---|---|
| Fixed startup settings injected by Kubernetes | Keep `caarlos0/env` |
| Tiny executable with three or four settings | Standard library may be simpler |
| Custom test or secret lookup composition dominates | Consider `sethvargo/go-envconfig` |
| Large structured file plus deterministic environment overrides is required | Consider cleanenv |
| Several runtime providers and parsers are genuinely required | Consider Koanf |
| Interactive CLI with user-selected files, flags, and remote stores | Viper or Koanf may be appropriate for that executable |
| Configuration must change without Pod replacement | Create a separate hot-reload ADR; do not solve it merely by selecting Viper |
| Dynamic business settings need audit/version/rollout | Store them in PostgreSQL or a dedicated control plane, not process config |

## ADR proposal

**Title:** Use typed environment configuration for Phase 4 Go services

**Status:** Proposed

**Context:** The current migration document proposes Viper to replace Micronaut configuration. The target architecture already standardizes on Kubernetes ConfigMaps and Secrets, Kustomize-managed deployments, 12-factor environment configuration, and explicit Go composition. Most Go services therefore need one typed startup snapshot, not runtime merging of files, flags, remote stores, and environment values.[^1]

**Decision:** Go services will define service-owned typed configuration structs and load them once at startup from environment variables using `caarlos0/env/v11`. Configuration will be semantically validated before dependencies are constructed. Kubernetes ConfigMaps provide non-confidential values, Kubernetes Secrets provide confidential values, and Kustomize/Flux owns environment-specific composition and rollout. Config objects are immutable after startup and are not global. Only required sub-configurations are passed through constructors. Structured files are mounted and parsed explicitly only when the payload is naturally file-shaped. Viper is not part of the default service stack.

**Consequences:** Services gain a small, typed, testable configuration boundary with simple precedence and fail-fast startup. Configuration changes roll out through Kubernetes rather than mutating running processes. The project must maintain per-service schemas, validation, redaction, and deployment-contract tests. A future need for layered files or live reload will require a separate design decision rather than being implicitly enabled by a framework.

## Migration-document changes

Replace:

```markdown
| Configuration | Micronaut Configuration: YAML, Properties | Viper for handling configuration from files, env vars, etc. |
```

with:

```markdown
| Configuration | Micronaut Configuration: YAML, Properties | Service-owned typed Go configuration loaded once at startup from environment variables using `caarlos0/env/v11`, followed by explicit semantic validation. Kubernetes ConfigMaps supply non-sensitive values, Secrets supply credentials, and Kustomize/Flux owns per-environment composition and rollout. Use explicit mounted files only for naturally file-shaped data such as certificates. |
```

Add under **Phase 4 → Establish Go Development Standards**:

```markdown
- **Configuration:** Each Go executable owns a typed configuration struct. Load it once at startup from environment variables with `caarlos0/env/v11`, validate all syntax and cross-field invariants before constructing dependencies, and fail fast with redacted errors. Pass only required sub-configurations through constructors; do not use global configuration state. Use Kustomize-generated ConfigMaps for non-sensitive values and Kubernetes Secrets for credentials, retaining content-hash suffixes so configuration changes trigger controlled rolling deployments. Configuration is immutable for the process lifetime by default. Mount files explicitly for certificates or other naturally file-shaped artifacts. Do not use Viper unless a separate ADR demonstrates a requirement for layered runtime sources that cannot be handled by this model.
```

Add a standards checklist:

```markdown
- Required variables have no silent deployment-specific defaults.
- Defaults are safe and declared in one place.
- Configuration is parsed and validated before readiness.
- Secret values are referenced explicitly and never logged.
- The root config is not passed through business packages.
- Kustomize-rendered manifests are checked against the service's environment contract in CI.
- Configuration changes use GitOps rollout; hot reload requires a dedicated ADR.
```

## Final position

Viper is a valid general-purpose configuration framework, and Koanf is the strongest modular alternative when multiple sources and file formats are truly necessary. Neither is needed as the baseline for Insurance Hub. **Use Kubernetes and Kustomize to compose deployment configuration, `caarlos0/env/v11` to bind the resulting environment into typed Go structs, explicit validation to enforce semantics, and constructor injection to distribute immutable values.**

---

## References

1. [system-overview-and-migration-analysis.md](https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/attachments/62895318/e482fa40-2b67-4529-9335-b46b94524a53/system-overview-and-migration-analysis.md?AWSAccessKeyId=ASIA2F3EMEYEYX4R35MJ&Signature=k7pKnPVqTftglz7zHirzxCO3jk8%3D&x-amz-security-token=IQoJb3JpZ2luX2VjEIb%2F%2F%2F%2F%2F%2F%2F%2F%2F%2FwEaCXVzLWVhc3QtMSJHMEUCIAYTgLu%2F0EYXyXT4d0LSbl378KXiVI7c0V8PB%2FyNzVHiAiEAhVFTp6eotKRMB7ZlrRP2lfxmoAIH1CUd6W%2BKIxOTQp8q8wQIThABGgw2OTk3NTMzMDk3MDUiDMxRiDKg11e1Smtj4irQBLF%2FXb8fbYs3NTQ5u14eix%2FnuIhCPYQ10gdH%2BIJwFLiy8h9g72cp0gxkXnjwHXkHgLIz%2FF08EP6zBKCE%2BcZgzBpJudlFdbzPF97jZJtqFNAVBCilAOy7N9swj8oBhxHzK6fSDUzGdpELHbDq8XUqAee3yyFMpRLTDw%2F1gD4IArI9atkr6L%2Bcy5pA2nGAmIo2Bi3ylaa%2FCXRc70lOwUkevd7%2BjbGaaqGCWHvEK%2BNkc85D2y8iyzFWYrceRDw7vOEpFa4ffNBKSRG19HXPBuzadGnB9i2q%2Fk7vKx4OnTAo%2BLVfPbXcMehMzWghshy3kiOaiJEpvTjqYhcpJMYHtNvsQhWKAK1CurPALuTICJJPPinZwn7GP6WEIqiTl0WOGeDm2bUDDqvmXOLxWMw6occgjFK4MMvOn9G3Rj%2FYzrw6PZa6M9qFH3obk82x485tfpiNfnwDRafbsmHNGFklghFGLfOv8XwUqPcXkKQfbMbU7F525NomXCHlIGtXyqe2IvCbMbQUD9KpYD3AsypGoNqsQE%2FBAAOwAtlVqY9JLC2FvF4DximNAKuzBXgjFrevufs6j1xalCcR7rjtSle9i847KSxIxgVo2JK00%2BjWsCTDpPZttg0d1zNDtG1HUjOKZgV9%2BAp0J7IY7K%2FVpJOPDTPVvB84nQSP68JMC88AnWf9Ewe%2B6M5V%2B6H26HfcRV0slruDIFi9YYdAk239zvi7mH%2B8URG9oniE1EuPpcAfpRN9DZ2M%2B%2BMBduZ0JDYb2KLqxnAaHxU4ZjOfU%2Bh8kBcdyz5zd0Aw0IPv1QY6mAHYBwo4l7FtbHQ4Qcg72%2FucvW3oSUY0EwdzGXD5vda8KQeqaZTkt%2FBgQO65g8r709HM4pYytZtRgVDMu2uhvy0BlX3zLLlrT6e3Y6hgtaFTSWZdrliCkAYnIA5jHVmk4c1YIVhHmH8GiSN8yHOisjzM2cb3PNwL1NGeGcg1JvXoRkg8YXEfyTY2othinmMSmmz9fyWePeFF9g%3D%3D&Expires=1790693283) - !-- START doctoc generated TOC please keep comment here to allow auto update -- !-- DONT EDIT THIS S...

2. [Configuration](https://kubernetes.io/docs/concepts/configuration/) - Configuration mechanisms within Kubernetes.

3. [Define Environment Variables for a Container - Kubernetes](https://kubernetes.io/docs/tasks/inject-data-application/define-environment-variable-container/) - This page shows how to define environment variables for a container in a Kubernetes Pod. Before you ...

4. [Declarative Management of Kubernetes Objects Using ...](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/kustomization/) - The generated ConfigMaps and Secrets have a content hash suffix appended. This ensures that a new Co...

5. [os package](https://pkg.go.dev/os) - LookupEnv retrieves the value of the environment variable named by the key. If the variable is prese...

6. [GitHub - caarlos0/env: A simple, zero-dependencies library to parse environment variables into structs](https://github.com/caarlos0/env/) - A simple, zero-dependencies library to parse environment variables into structs - caarlos0/env

7. [env/env.go at main · caarlos0/env](https://github.com/caarlos0/env/blob/main/env.go) - A simple, zero-dependencies library to parse environment variables into structs - caarlos0/env

8. [caarlos0/env: A simple, zero-dependencies library to parse ...](https://github.com/caarlos0/env) - A simple, zero-dependencies library to parse environment variables into structs. Installation. go ge...

9. [configMapGenerator](https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/configmapgenerator/) - Generate ConfigMap resources.

10. [vendor/github.com/spf13/viper/README.md](https://go.googlesource.com/gddo/+/refs/heads/master/vendor/github.com/spf13/viper/README.md)

11. [spf13/viper: Go configuration with fangs](https://github.com/spf13/viper) - Viper has full support for environment variables. NOTE Unlike other configuration sources, environme...

12. [README.md - spf13/viper](https://github.com/spf13/viper/blob/master/README.md) - Viper comes with a global instance (singleton) out of the box. using it is generally discouraged as ...

13. [Secrets](https://kubernetes.io/docs/concepts/configuration/secret/) - A Secret is an object that contains a small amount of sensitive data such as a password, a token, or...

14. [ConfigMaps - Kubernetes](https://kubernetes.io/docs/concepts/configuration/configmap/) - A ConfigMap is an API object used to store non-confidential data in key-value pairs. Pods can consum...

15. [Consider env vars when unmarshalling · Issue #188 · spf13/viper](https://github.com/spf13/viper/issues/188) - Is this possible? UPDATE: yes it is, see #188 (comment) type Config struct { BindPort int `mapstruct...

16. [env/README.md at main · caarlos0/env](https://github.com/caarlos0/env/blob/main/README.md) - A simple, zero-dependencies library to parse environment variables into structs - caarlos0/env

17. [sethvargo/go-envconfig: A Go library for parsing struct tags ...](https://github.com/sethvargo/go-envconfig) - Envconfig populates struct field values based on environment variables or arbitrary lookup functions...

18. [go-envconfig/README.md at main · sethvargo/go-envconfig](https://github.com/sethvargo/go-envconfig/blob/main/README.md) - A Go library for parsing struct tags from environment variables. - sethvargo/go-envconfig

19. [pkg.go.dev › github › sethvargoenvconfig package - github.com/sethvargo/go-envconfig/pkg ...](https://pkg.go.dev/github.com/sethvargo/go-envconfig/pkg/envconfig) - Package envconfig populates struct fields based on environment variable values (or anything that res...

20. [knadh/koanf: Simple, extremely lightweight, extensible, ...](https://github.com/knadh/koanf) - Simple, extremely lightweight, extensible, configuration management library for Go. Supports JSON, T...

21. [koanf/README.md at master · knadh/koanf](https://github.com/knadh/koanf/blob/master/README.md) - Simple, extremely lightweight, extensible, configuration management library for Go. Supports JSON, T...

22. [Releases · knadh/koanf](https://github.com/knadh/koanf/releases) - Simple, extremely lightweight, extensible, configuration management library for Go. Supports JSON, T...

23. [providers/ directory - github.com/knadh/koanf ...](https://pkg.go.dev/github.com/knadh/koanf/providers)

24. [ilyakaznacheev/cleanenv: ✨Clean and minimalistic ...](https://github.com/ilyakaznacheev/cleanenv) - This is a simple configuration reading tool. It just does the following: reads and parses configurat...

25. [cleanenv/README.md at master · ilyakaznacheev/cleanenv](https://github.com/ilyakaznacheev/cleanenv/blob/master/README.md) - ✨Clean and minimalistic environment configuration reader for Golang - ilyakaznacheev/cleanenv

26. [cleanenv/cleanenv.go at master · ilyakaznacheev/cleanenv](https://github.com/ilyakaznacheev/cleanenv/blob/master/cleanenv.go) - Clean and minimalistic environment configuration reader for Golang - cleanenv/cleanenv.go at master ...

27. [GitHub - heetch/confita: Load configuration in cascade from multiple backends into a struct](https://github.com/heetch/confita) - Load configuration in cascade from multiple backends into a struct - heetch/confita

28. [joho/godotenv: A Go port of Ruby's dotenv library (Loads ...](https://github.com/joho/godotenv) - A Go (golang) port of the Ruby dotenv project (which loads env vars from a .env file). From the orig...

29. [godotenv/godotenv.go at main · joho/godotenv](https://github.com/joho/godotenv/blob/main/godotenv.go) - A Go port of Ruby's dotenv library (Loads environment variables from .env files) - joho/godotenv

30. [kelseyhightower/envconfig: Golang library for managing ...](https://github.com/kelseyhightower/envconfig) - Golang library for managing configuration data from environment variables - Envconfig supports the u...

31. [For a typical Go microservice deployed to Kubernetes cluster, how the application properties, for example, Postgres connection string, are configured and injected during the deployment? What are the best industry practices for that? What would be your recommendation?](https://www.perplexity.ai/search/32b5c8c3-2ee5-427a-a679-d771ae6da3fe) - Excellent question. This is a critical aspect of 12-factor app compliance and cloud-native practices...

32. [Please adjust your answer given that I'm planning to use Kustomize.](https://www.perplexity.ai/search/d5723a21-6483-4eac-94dd-51817bb1881b) - Excellent! Kustomize is the perfect fit for managing ConfigMap, Secret, and Deployment variations ac...

33. [For a given deployment patch for qa, update kustomization by adding config map generator.

apiVersion: apps/v1
kind: Deployment
metadata:
  name: auth-api-legacy
spec:
  template:
    spec:
      containers:
        - name: auth-api-legacy
          ...

...svc
namePrefix: qa-

labels:
  - includeSelectors: true
    pairs:
      environment: qa

resources:
  - ../../../../../apps/svc/auth/base/legacy

patches:
  - path: deployment-patch.yaml
    target:
      kind: Deployment
      name: auth-api-legacy](https://www.perplexity.ai/search/0e3f990e-fe5f-4c3f-8067-c911e8d37e03) - Yes. For QA, move the non-secret env vars into a ConfigMap and reference it from the Deployment. Kee...

34. [Store config in the environment](https://www.12factor.net/config) - A methodology for building modern, scalable, maintainable software-as-a-service apps.

35. [Updating Configuration via a ConfigMap - Kubernetes](https://kubernetes.io/docs/tutorials/configuration/updating-configuration-via-a-configmap/) - This page provides a step-by-step example of updating configuration within a Pod via a ConfigMap and...

36. [Configure a Pod to Use a ConfigMap](https://kubernetes.io/docs/tasks/configure-pod-container/configure-pod-configmap/) - Many applications rely on configuration which is used during either application initialization or ru...

37. [Volumes](https://kubernetes.io/docs/concepts/storage/volumes/) - Kubernetes volumes provide a way for containers in a Pod to access and share data via the filesystem...

38. [flag - Go Packages](https://pkg.go.dev/flag) - Package flag implements command-line flag parsing.

39. [go/src/flag/flag.go at master · golang/go](https://github.com/golang/go/blob/master/src/flag/flag.go?name=release) - The Go programming language. Contribute to golang/go development by creating an account on GitHub.

40. [godotenv/README.md at main · joho/godotenv](https://github.com/joho/godotenv/blob/main/README.md) - A Go port of Ruby's dotenv library (Loads environment variables from .env files) - joho/godotenv

