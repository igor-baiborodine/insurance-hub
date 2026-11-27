package config

import (
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	kitidentity "github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

// DefaultServiceName identifies startup diagnostics until configuration has been validated.
const DefaultServiceName = "payment-service"

const canonicalVersionReason = "must use git- followed by 12 lowercase hexadecimal " +
	"characters with at least one nonzero character outside development"

const (
	databaseURLSetting      = "PAYMENT_DATABASE_URL"
	databaseMaxConnsSetting = "DB_MAX_CONNS"
	databaseConnectSetting  = "DB_CONNECT_TIMEOUT"
	databaseQuerySetting    = "DB_QUERY_TIMEOUT"
	startupTimeoutSetting   = "STARTUP_TIMEOUT"
	objectEndpointSetting   = "MINIO_ENDPOINT"
	objectAccessKeySetting  = "MINIO_ACCESS_KEY"
	objectSecretKeySetting  = "MINIO_SECRET_KEY"
	objectCAFileSetting     = "MINIO_CA_FILE"
	objectBucketSetting     = "MINIO_BUCKET"
	objectFetchTimeout      = "MINIO_FETCH_TIMEOUT"
	kafkaBrokersSetting     = "KAFKA_BROKERS"
	kafkaRegisteredSetting  = "KAFKA_REGISTERED_TOPIC"
	kafkaCAFileSetting      = "KAFKA_CA_FILE"
	defaultBucket           = "payments-import"
	defaultRegisteredTopic  = "policy-registered"
)

// Database holds the Payment runtime connection and pool limits.
type Database struct {
	URL            DatabaseURL
	MaxConnections int32
	ConnectTimeout time.Duration
	QueryTimeout   time.Duration
}

// DatabaseURL redacts the runtime credential from ordinary formatting.
type DatabaseURL struct{ value string }

// Value returns the runtime connection URL to the PostgreSQL adapter.
func (databaseURL DatabaseURL) Value() string { return databaseURL.value }

func (DatabaseURL) String() string   { return "[REDACTED]" }
func (DatabaseURL) GoString() string { return "[REDACTED]" }

// Secret retains a credential without exposing it through ordinary formatting.
type Secret struct{ value string }

// Value returns the credential only to the adapter that needs it.
func (secret Secret) Value() string { return secret.value }

func (Secret) String() string   { return "[REDACTED]" }
func (Secret) GoString() string { return "[REDACTED]" }

// ObjectStore contains the statement-import bucket connection and fetch bound.
type ObjectStore struct {
	Endpoint     *url.URL
	AccessKey    Secret
	SecretKey    Secret
	CAFile       string
	Bucket       string
	FetchTimeout time.Duration
}

// Kafka holds the broker discovery and trust for the registration topic.
type Kafka struct {
	Brokers         []string
	RegisteredTopic string
	CAFile          string
}

// Config contains settings parsed and validated once during process startup.
type Config struct {
	Database       Database
	ObjectStore    ObjectStore
	Kafka          Kafka
	StartupTimeout time.Duration
	kitconfig.Runtime
	kitconfig.GRPC
	kitconfig.HTTPServer
	kitconfig.Telemetry
	ServiceName        string
	HTTPAddress        Address
	GRPCAddress        Address
	HealthAddress      Address
	HTTPMaxHeaderBytes int
	HTTPMaxBodyBytes   int
}

// Address uses the shared TCP address parser and preserves ephemeral ports.
type Address = kitconfig.Address

// GRPC is the shared message-size group; Payment owns its permitted ranges.
type GRPC = kitconfig.GRPC

// Identity adds Payment validation policy to the shared resource and log fields.
type Identity struct{ kitidentity.Identity }

// Telemetry is the shared exporter settings group. Identity policy stays in this package.
type Telemetry = kitconfig.Telemetry

// DevelopmentIdentity labels local startup diagnostics before configuration is validated.
func DevelopmentIdentity(serviceName string) Identity {
	return Identity{Identity: kitidentity.DevelopmentIdentity(serviceName, os.Getpid())}
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

type lookupEnv = kitconfig.Lookup

func load(lookup lookupEnv) (Config, error) {
	runtimeDefaults := kitconfig.DefaultRuntime()
	telemetryDefaults := kitconfig.DefaultTelemetry()

	serviceName, err := kitconfig.ValueOrDefault(
		lookup,
		kitconfig.EnvServiceName,
		DefaultServiceName,
	)
	if err != nil {
		return Config{}, err
	}
	if serviceName != DefaultServiceName {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvServiceName,
			"must match the service's fixed logical name",
		)
	}

	grpcAddress, err := kitconfig.LoadAddress(
		lookup, kitconfig.EnvGRPCAddress, kitconfig.DefaultGRPCAddress,
	)
	if err != nil {
		return Config{}, err
	}
	httpAddress, err := kitconfig.LoadAddress(
		lookup, kitconfig.EnvHTTPAddress, kitconfig.DefaultHTTPAddress,
	)
	if err != nil {
		return Config{}, err
	}

	healthAddress, err := kitconfig.LoadAddress(
		lookup, kitconfig.EnvHealthAddress, kitconfig.DefaultHealthAddress,
	)
	if err != nil {
		return Config{}, err
	}

	logLevelValue, err := kitconfig.ValueOrDefault(
		lookup,
		kitconfig.EnvLogLevel,
		runtimeDefaults.LogLevel.String(),
	)
	if err != nil {
		return Config{}, err
	}
	logLevel, err := kitconfig.ParseLogLevel(kitconfig.EnvLogLevel, logLevelValue)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, kitconfig.EnvShutdownTimeout, runtimeDefaults.ShutdownTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}

	otelEnabledValue, err := kitconfig.ValueOrDefault(
		lookup, kitconfig.EnvOTELEnabled, strconv.FormatBool(telemetryDefaults.Enabled),
	)
	if err != nil {
		return Config{}, err
	}
	otelEnabled, err := kitconfig.ParseBool(kitconfig.EnvOTELEnabled, otelEnabledValue)
	if err != nil {
		return Config{}, err
	}

	otelTimeout, err := kitconfig.LoadPositiveDuration(
		lookup,
		kitconfig.EnvOTELExporterOTLPTimeout,
		telemetryDefaults.ExporterTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}

	otelEndpoint, err := kitconfig.OptionalEndpoint(
		lookup,
		kitconfig.EnvOTELExporterOTLPEndpoint,
		otelEnabled,
	)
	if err != nil {
		return Config{}, err
	}

	identity, err := loadIdentity(lookup, serviceName, otelEnabled)
	if err != nil {
		return Config{}, err
	}
	requestTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, kitconfig.EnvRequestTimeout, runtimeDefaults.RequestTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}
	probeTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, kitconfig.EnvProbeTimeout, runtimeDefaults.ProbeTimeout.String(),
	)
	if err != nil {
		return Config{}, err
	}
	grpc, err := kitconfig.LoadGRPC(lookup)
	if err != nil {
		return Config{}, err
	}
	httpServer, err := kitconfig.LoadHTTPServer(lookup)
	if err != nil {
		return Config{}, err
	}
	maxHeaderBytes, err := kitconfig.LoadBoundedInt(
		lookup,
		kitconfig.EnvHTTPMaxHeaderBytes,
		strconv.Itoa(kitconfig.DefaultHTTPMaxHeaderBytes),
		1024,
		1048576,
	)
	if err != nil {
		return Config{}, err
	}
	maxBodyBytes, err := kitconfig.LoadBoundedInt(
		lookup,
		kitconfig.EnvHTTPMaxBodyBytes,
		strconv.Itoa(kitconfig.DefaultHTTPMaxBodyBytes),
		1024,
		1048576,
	)
	if err != nil {
		return Config{}, err
	}
	runtime := kitconfig.Runtime{
		RequestTimeout:  requestTimeout,
		ProbeTimeout:    probeTimeout,
		ShutdownTimeout: shutdownTimeout,
		LogLevel:        logLevel,
	}
	if err := kitconfig.ValidateRuntime(runtime); err != nil {
		return Config{}, err
	}
	telemetry := kitconfig.Telemetry{
		Identity:         identity.Identity,
		Enabled:          otelEnabled,
		ExporterEndpoint: otelEndpoint,
		ExporterTimeout:  otelTimeout,
	}
	if err := kitconfig.ValidateTelemetry(telemetry); err != nil {
		return Config{}, err
	}
	if httpServer.ReadHeaderTimeout > httpServer.ReadTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvHTTPReadHeaderTimeout,
			"must not exceed HTTP_READ_TIMEOUT",
		)
	}
	if httpServer.ReadTimeout > httpServer.WriteTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvHTTPReadTimeout,
			"must not exceed HTTP_WRITE_TIMEOUT",
		)
	}
	if runtime.RequestTimeout >= httpServer.WriteTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvRequestTimeout,
			"must be less than HTTP_WRITE_TIMEOUT",
		)
	}
	if runtime.ShutdownTimeout < runtime.RequestTimeout+otelTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvShutdownTimeout,
			"must cover REQUEST_TIMEOUT plus OTEL_EXPORTER_OTLP_TIMEOUT",
		)
	}
	listeners := []struct {
		name    string
		address Address
	}{
		{kitconfig.EnvHTTPAddress, httpAddress},
		{kitconfig.EnvGRPCAddress, grpcAddress},
		{kitconfig.EnvHealthAddress, healthAddress},
	}
	for left := range listeners {
		for right := left + 1; right < len(listeners); right++ {
			if listeners[left].address.Port() != 0 &&
				listeners[left].address.String() == listeners[right].address.String() {
				return Config{}, kitconfig.Invalid(
					listeners[right].name,
					"must be distinct from "+listeners[left].name,
				)
			}
		}
	}

	startupTimeout, err := kitconfig.LoadPositiveDuration(lookup, startupTimeoutSetting, "10s")
	if err != nil {
		return Config{}, err
	}
	database, err := loadDatabase(lookup)
	if err != nil {
		return Config{}, err
	}
	objectStore, err := loadObjectStore(lookup)
	if err != nil {
		return Config{}, err
	}
	kafka, err := loadKafka(lookup)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Database:           database,
		ObjectStore:        objectStore,
		Kafka:              kafka,
		StartupTimeout:     startupTimeout,
		Runtime:            runtime,
		GRPC:               grpc,
		HTTPServer:         httpServer,
		ServiceName:        serviceName,
		HTTPAddress:        httpAddress,
		GRPCAddress:        grpcAddress,
		HealthAddress:      healthAddress,
		HTTPMaxHeaderBytes: maxHeaderBytes,
		HTTPMaxBodyBytes:   maxBodyBytes,
		Telemetry:          telemetry,
	}, nil
}

func loadDatabase(lookup lookupEnv) (Database, error) {
	databaseURL, err := loadDatabaseURL(lookup)
	if err != nil {
		return Database{}, err
	}
	maxConnections, err := kitconfig.LoadBoundedInt(lookup, databaseMaxConnsSetting, "4", 1, 16)
	if err != nil {
		return Database{}, err
	}
	connectTimeout, err := kitconfig.LoadPositiveDuration(lookup, databaseConnectSetting, "3s")
	if err != nil {
		return Database{}, err
	}
	queryTimeout, err := kitconfig.LoadPositiveDuration(lookup, databaseQuerySetting, "1s")
	if err != nil {
		return Database{}, err
	}
	return Database{
		URL:            databaseURL,
		MaxConnections: int32(maxConnections),
		ConnectTimeout: connectTimeout,
		QueryTimeout:   queryTimeout,
	}, nil
}

func loadDatabaseURL(lookup lookupEnv) (DatabaseURL, error) {
	value, err := required(lookup, databaseURLSetting)
	if err != nil {
		return DatabaseURL{}, err
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") ||
		parsed.User == nil || parsed.Path != "/payment" ||
		!kitconfig.ValidHost(parsed.Hostname()) || parsed.Fragment != "" {
		return DatabaseURL{}, kitconfig.Invalid(
			databaseURLSetting,
			"must identify the Payment database",
		)
	}
	if port := parsed.Port(); port != "" {
		if number, err := strconv.ParseUint(port, 10, 16); err != nil || number == 0 {
			return DatabaseURL{}, kitconfig.Invalid(
				databaseURLSetting,
				"must use a valid port",
			)
		}
	}
	password, ok := parsed.User.Password()
	params, err := url.ParseQuery(parsed.RawQuery)
	if !ok || password == "" || err != nil || len(params) != 2 ||
		len(params["sslmode"]) != 1 || params.Get("sslmode") != "verify-full" ||
		len(params["sslrootcert"]) != 1 || params.Get("sslrootcert") == "" {
		return DatabaseURL{}, kitconfig.Invalid(
			databaseURLSetting,
			"must require TLS hostname and CA verification",
		)
	}
	return DatabaseURL{value: value}, nil
}

func loadObjectStore(lookup lookupEnv) (ObjectStore, error) {
	endpointValue, err := required(lookup, objectEndpointSetting)
	if err != nil {
		return ObjectStore{}, err
	}
	endpoint, parseErr := url.Parse(endpointValue)
	if parseErr != nil || endpoint.Opaque != "" || endpoint.User != nil ||
		endpoint.RawQuery != "" ||
		endpoint.Fragment != "" ||
		endpoint.Hostname() == "" ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		(endpoint.Path != "" && endpoint.Path != "/") {
		return ObjectStore{}, kitconfig.Invalid(
			objectEndpointSetting,
			"must be an HTTP or HTTPS origin URL",
		)
	}
	endpoint.Path = ""
	accessKey, err := required(lookup, objectAccessKeySetting)
	if err != nil {
		return ObjectStore{}, err
	}
	secretKey, err := required(lookup, objectSecretKeySetting)
	if err != nil {
		return ObjectStore{}, err
	}
	caFile, _ := lookup(objectCAFileSetting)
	if endpoint.Scheme == "https" && strings.TrimSpace(caFile) == "" {
		return ObjectStore{}, kitconfig.Invalid(
			objectCAFileSetting,
			"is required for HTTPS object storage",
		)
	}
	bucket, err := kitconfig.ValueOrDefault(lookup, objectBucketSetting, defaultBucket)
	if err != nil {
		return ObjectStore{}, err
	}
	if bucket != defaultBucket {
		return ObjectStore{}, kitconfig.Invalid(
			objectBucketSetting,
			"must match the fixed payments-import bucket",
		)
	}
	fetchTimeout, err := kitconfig.LoadPositiveDuration(lookup, objectFetchTimeout, "3s")
	if err != nil {
		return ObjectStore{}, err
	}
	return ObjectStore{
		Endpoint:     endpoint,
		AccessKey:    Secret{value: accessKey},
		SecretKey:    Secret{value: secretKey},
		CAFile:       caFile,
		Bucket:       bucket,
		FetchTimeout: fetchTimeout,
	}, nil
}

func loadKafka(lookup lookupEnv) (Kafka, error) {
	brokersValue, err := required(lookup, kafkaBrokersSetting)
	if err != nil {
		return Kafka{}, err
	}
	brokers := strings.Split(brokersValue, ",")
	for index := range brokers {
		brokers[index] = strings.TrimSpace(brokers[index])
		if brokers[index] == "" || !validBroker(brokers[index]) {
			return Kafka{}, kitconfig.Invalid(
				kafkaBrokersSetting,
				"must contain comma-separated host:port brokers",
			)
		}
	}
	registered, err := kitconfig.ValueOrDefault(
		lookup,
		kafkaRegisteredSetting,
		defaultRegisteredTopic,
	)
	if err != nil {
		return Kafka{}, err
	}
	if registered != defaultRegisteredTopic {
		return Kafka{}, kitconfig.Invalid(
			kafkaRegisteredSetting,
			"must select the Policy registration topic",
		)
	}
	caFile, err := required(lookup, kafkaCAFileSetting)
	if err != nil {
		return Kafka{}, err
	}
	return Kafka{Brokers: brokers, RegisteredTopic: registered, CAFile: caFile}, nil
}

func required(lookup lookupEnv, name string) (string, error) {
	value, exists := lookup(name)
	if !exists || value == "" {
		return "", kitconfig.Invalid(name, "is required")
	}
	return value, nil
}

func validBroker(value string) bool {
	parsed, err := url.Parse("tcp://" + value)
	return err == nil && parsed.Host == value && kitconfig.ValidHost(parsed.Hostname()) &&
		parsed.Port() != ""
}

func loadIdentity(lookup lookupEnv, serviceName string, enabled bool) (Identity, error) {
	identity := DevelopmentIdentity(serviceName)
	fields := []struct {
		name  string
		value *string
	}{
		{kitconfig.EnvDeploymentEnvironment, &identity.Environment},
		{kitconfig.EnvServiceVersion, &identity.ServiceVersion},
		{kitconfig.EnvServiceInstanceID, &identity.ServiceInstance},
	}
	for _, field := range fields {
		fallback := *field.value
		if enabled {
			fallback = ""
		}
		value, err := kitconfig.ValueOrDefault(lookup, field.name, fallback)
		if err != nil {
			return Identity{}, err
		}
		*field.value = value
	}
	if err := identity.Validate(enabled); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

// Validate rejects incomplete identity without echoing supplied values.
func (identity Identity) Validate(enabled bool) error {
	if identity.ServiceName != DefaultServiceName {
		return kitconfig.Invalid(
			kitconfig.EnvServiceName,
			"must match the service's fixed logical name",
		)
	}
	if identity.ServiceNamespace != "insurance-hub" || identity.Implementation != "go" {
		return kitconfig.Invalid(
			"telemetry identity",
			"must use the insurance-hub namespace and Go implementation",
		)
	}
	for _, field := range []struct{ name, value string }{
		{kitconfig.EnvServiceName, identity.ServiceName},
		{kitconfig.EnvDeploymentEnvironment, identity.Environment},
		{kitconfig.EnvServiceVersion, identity.ServiceVersion},
		{kitconfig.EnvServiceInstanceID, identity.ServiceInstance},
	} {
		if !validIdentityValue(field.value) {
			return kitconfig.Invalid(
				field.name,
				"must contain 1 through 128 ASCII letters, digits, dots, underscores or hyphens",
			)
		}
	}
	if identity.Environment != "development" &&
		!validCanonicalVersion(identity.ServiceVersion) {
		return kitconfig.Invalid(
			kitconfig.EnvServiceVersion,
			canonicalVersionReason,
		)
	}
	if enabled &&
		(identity.Environment == "development" || identity.ServiceVersion == "development" || strings.HasPrefix(identity.ServiceInstance, "local-")) {
		return kitconfig.Invalid(
			"telemetry identity",
			"must use explicit non-development environment, version and instance when enabled",
		)
	}
	return nil
}

func validIdentityValue(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') &&
			ch != '.' &&
			ch != '_' &&
			ch != '-' {
			return false
		}
	}
	return true
}

func validCanonicalVersion(value string) bool {
	if len(value) != len("git-")+12 || !strings.HasPrefix(value, "git-") {
		return false
	}
	nonzero := false
	for _, ch := range value[len("git-"):] {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
		if ch != '0' {
			nonzero = true
		}
	}
	return nonzero
}
