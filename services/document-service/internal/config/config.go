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
const DefaultServiceName = "document-service"

const canonicalVersionReason = "must use git- followed by 12 lowercase hexadecimal " +
	"characters with at least one nonzero character outside development"

const (
	databaseURLSetting             = "DOCUMENT_DATABASE_URL"
	databaseMaxConnsSetting        = "DB_MAX_CONNS"
	databaseConnectTimeoutSetting  = "DB_CONNECT_TIMEOUT"
	databaseAcquireTimeoutSetting  = "DB_ACQUIRE_TIMEOUT"
	databaseQueryTimeoutSetting    = "DB_QUERY_TIMEOUT"
	startupTimeoutSetting          = "STARTUP_TIMEOUT"
	objectEndpointSetting          = "MINIO_ENDPOINT"
	objectAccessKeySetting         = "MINIO_ACCESS_KEY"
	objectSecretKeySetting         = "MINIO_SECRET_KEY"
	objectCAFileSetting            = "MINIO_CA_FILE"
	objectBucketSetting            = "MINIO_BUCKET"
	objectFetchTimeoutSetting      = "MINIO_FETCH_TIMEOUT"
	maximumDocumentBytesSetting    = "DOCUMENT_MAX_BYTES"
	maximumResponseBytesSetting    = "DOCUMENT_RESPONSE_MAX_BYTES"
	maximumHTTPResponseSetting     = "HTTP_RESPONSE_MAX_BYTES"
	maximumConcurrentLookupSetting = "DOCUMENT_MAX_CONCURRENT_LOOKUPS"
	EnvEventWorkersEnabled         = "EVENT_WORKERS_ENABLED"
	EnvKafkaBrokers                = "KAFKA_BROKERS"
	EnvKafkaGroup                  = "KAFKA_GROUP"
	EnvKafkaRegisteredTopic        = "KAFKA_REGISTERED_TOPIC"
	EnvKafkaCAFile                 = "KAFKA_CA_FILE"
	EnvJSReportURL                 = "JSREPORT_URL"
	EnvWriterDatabaseURL           = "DOCUMENT_WRITER_DATABASE_URL"
	EnvWriterObjectAccessKey       = "MINIO_WRITER_ACCESS_KEY"
	EnvWriterObjectSecretKey       = "MINIO_WRITER_SECRET_KEY"
	defaultGRPCBytes               = 24 * 1024 * 1024
	defaultMaximumDocumentBytes    = 8 * 1024 * 1024
	defaultMaximumResponseBytes    = 16 * 1024 * 1024
	defaultMaximumHTTPResponse     = 24 * 1024 * 1024
	defaultKafkaGroup              = "document-service"
	defaultRegisteredTopic         = "policy-registered"
	defaultRendererConnectTimeout  = 2 * time.Second
	defaultRendererRequestTimeout  = 8 * time.Second
	defaultUploadTimeout           = 8 * time.Second
)

// Config contains settings parsed and validated once during process startup.
type Config struct {
	kitconfig.Runtime
	kitconfig.GRPC
	kitconfig.HTTPServer
	kitconfig.Telemetry
	ServiceName              string
	HTTPAddress              Address
	GRPCAddress              Address
	HealthAddress            Address
	HTTPMaxHeaderBytes       int
	HTTPMaxBodyBytes         int
	HTTPMaxResponseBytes     int
	Database                 Database
	ObjectStore              ObjectStore
	Events                   Events
	StartupTimeout           time.Duration
	MaximumDocumentBytes     int
	MaximumResponseBytes     int
	MaximumConcurrentLookups int
}

// Address uses the shared TCP address parser and preserves ephemeral ports.
type Address = kitconfig.Address

// GRPC is the shared message-size group; Document owns its permitted ranges.
type GRPC = kitconfig.GRPC

// Identity adds Document validation policy to the shared resource and log fields.
type Identity struct{ kitidentity.Identity }

// Telemetry is the shared exporter settings group. Identity policy stays in this package.
type Telemetry = kitconfig.Telemetry

// Secret retains a credential without exposing it through ordinary formatting.
type Secret struct{ value string }

// Value returns the credential only to the object-store adapter.
func (secret Secret) Value() string { return secret.value }

func (Secret) String() string   { return "[REDACTED]" }
func (Secret) GoString() string { return "[REDACTED]" }

// DatabaseURL retains the restricted reader URL without exposing credentials.
type DatabaseURL struct{ value string }

// Value returns the URL only to the PostgreSQL adapter.
func (databaseURL DatabaseURL) Value() string { return databaseURL.value }

func (DatabaseURL) String() string   { return "[REDACTED]" }
func (DatabaseURL) GoString() string { return "[REDACTED]" }

// Database contains the reader connection, pool and deadline settings.
type Database struct {
	URL            DatabaseURL
	MaxConnections int32
	ConnectTimeout time.Duration
	AcquireTimeout time.Duration
	QueryTimeout   time.Duration
}

// ObjectStore contains the read-only policies bucket connection and fetch bound.
type ObjectStore struct {
	Endpoint     *url.URL
	AccessKey    Secret
	SecretKey    Secret
	CAFile       string
	Bucket       string
	FetchTimeout time.Duration
}

// Events contains the opt-in registration worker and separate writer settings.
// Disabled workers require and retain no generation dependency settings.
type Events struct {
	Enabled                bool
	Brokers                []string
	Group                  string
	RegisteredTopic        string
	CAFile                 string
	JSReportOrigin         *url.URL
	WriterDatabase         Database
	WriterObjectStore      ObjectStore
	RendererConnectTimeout time.Duration
	RendererRequestTimeout time.Duration
	UploadTimeout          time.Duration
}

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
		lookup, kitconfig.EnvRequestTimeout, "4s",
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
	grpcReceiveBytes, err := kitconfig.LoadBoundedInt(
		lookup,
		kitconfig.EnvGRPCMaxReceiveBytes,
		strconv.Itoa(defaultGRPCBytes),
		1,
		defaultGRPCBytes,
	)
	if err != nil {
		return Config{}, err
	}
	grpcSendBytes, err := kitconfig.LoadBoundedInt(
		lookup,
		kitconfig.EnvGRPCMaxSendBytes,
		strconv.Itoa(defaultGRPCBytes),
		1,
		defaultGRPCBytes,
	)
	if err != nil {
		return Config{}, err
	}
	grpc := kitconfig.GRPC{MaxReceiveBytes: grpcReceiveBytes, MaxSendBytes: grpcSendBytes}
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
	database, err := loadDatabase(lookup)
	if err != nil {
		return Config{}, err
	}
	objectStore, err := loadObjectStore(lookup)
	if err != nil {
		return Config{}, err
	}
	events, err := loadEvents(lookup, identity.Environment, database, objectStore)
	if err != nil {
		return Config{}, err
	}
	startupTimeout, err := kitconfig.LoadPositiveDuration(lookup, startupTimeoutSetting, "10s")
	if err != nil {
		return Config{}, err
	}
	maximumDocumentBytes, err := kitconfig.LoadBoundedInt(
		lookup, maximumDocumentBytesSetting, strconv.Itoa(defaultMaximumDocumentBytes), 1,
		defaultMaximumDocumentBytes,
	)
	if err != nil {
		return Config{}, err
	}
	maximumResponseBytes, err := kitconfig.LoadBoundedInt(
		lookup, maximumResponseBytesSetting, strconv.Itoa(defaultMaximumResponseBytes),
		maximumDocumentBytes, defaultMaximumResponseBytes,
	)
	if err != nil {
		return Config{}, err
	}
	maximumHTTPResponseBytes, err := kitconfig.LoadBoundedInt(
		lookup, maximumHTTPResponseSetting, strconv.Itoa(defaultMaximumHTTPResponse),
		maximumResponseBytes, defaultMaximumHTTPResponse,
	)
	if err != nil {
		return Config{}, err
	}
	maximumConcurrentLookups, err := kitconfig.LoadBoundedInt(
		lookup, maximumConcurrentLookupSetting, "2", 1, 32,
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

	return Config{
		Runtime:                  runtime,
		GRPC:                     grpc,
		HTTPServer:               httpServer,
		ServiceName:              serviceName,
		HTTPAddress:              httpAddress,
		GRPCAddress:              grpcAddress,
		HealthAddress:            healthAddress,
		HTTPMaxHeaderBytes:       maxHeaderBytes,
		HTTPMaxBodyBytes:         maxBodyBytes,
		HTTPMaxResponseBytes:     maximumHTTPResponseBytes,
		Database:                 database,
		ObjectStore:              objectStore,
		Events:                   events,
		StartupTimeout:           startupTimeout,
		MaximumDocumentBytes:     maximumDocumentBytes,
		MaximumResponseBytes:     maximumResponseBytes,
		MaximumConcurrentLookups: maximumConcurrentLookups,
		Telemetry:                telemetry,
	}, nil
}

func loadDatabase(lookup lookupEnv) (Database, error) {
	databaseURL, err := required(lookup, databaseURLSetting)
	if err != nil {
		return Database{}, err
	}
	parsed, parseErr := url.Parse(databaseURL)
	if parseErr != nil || parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" ||
		parsed.Hostname() == "" || parsed.Path == "" || parsed.Path == "/" || parsed.Fragment != "" {
		return Database{}, kitconfig.Invalid(
			databaseURLSetting,
			"must be a PostgreSQL URL with a database",
		)
	}
	maxConnections, err := kitconfig.LoadBoundedInt(lookup, databaseMaxConnsSetting, "4", 1, 16)
	if err != nil {
		return Database{}, err
	}
	connectTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, databaseConnectTimeoutSetting, "3s",
	)
	if err != nil {
		return Database{}, err
	}
	acquireTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, databaseAcquireTimeoutSetting, "1s",
	)
	if err != nil {
		return Database{}, err
	}
	queryTimeout, err := kitconfig.LoadPositiveDuration(
		lookup,
		databaseQueryTimeoutSetting,
		"2s",
	)
	if err != nil {
		return Database{}, err
	}
	return Database{
		URL:            DatabaseURL{value: databaseURL},
		MaxConnections: int32(maxConnections),
		ConnectTimeout: connectTimeout,
		AcquireTimeout: acquireTimeout,
		QueryTimeout:   queryTimeout,
	}, nil
}

func loadEvents(
	lookup lookupEnv,
	environment string,
	readerDatabase Database,
	readerObjectStore ObjectStore,
) (Events, error) {
	enabledValue, err := kitconfig.ValueOrDefault(lookup, EnvEventWorkersEnabled, "false")
	if err != nil {
		return Events{}, err
	}
	enabled, err := kitconfig.ParseBool(EnvEventWorkersEnabled, enabledValue)
	if err != nil {
		return Events{}, err
	}
	workerSettings := []string{
		EnvKafkaBrokers,
		EnvKafkaGroup,
		EnvKafkaRegisteredTopic,
		EnvKafkaCAFile,
		EnvJSReportURL,
		EnvWriterDatabaseURL,
		EnvWriterObjectAccessKey,
		EnvWriterObjectSecretKey,
	}
	if !enabled {
		for _, name := range workerSettings {
			if _, present := lookup(name); present {
				return Events{}, kitconfig.Invalid(
					name,
					"must be unset when event processing is disabled",
				)
			}
		}
		return Events{}, nil
	}
	expectedBroker, expectedJSReport, supported := eventTargets(environment)
	if !supported {
		return Events{}, kitconfig.Invalid(
			kitconfig.EnvDeploymentEnvironment,
			"must be local-dev or qa when event processing is enabled",
		)
	}
	brokersValue, err := required(lookup, EnvKafkaBrokers)
	if err != nil {
		return Events{}, err
	}
	brokers := strings.Split(brokersValue, ",")
	for index := range brokers {
		brokers[index] = strings.TrimSpace(brokers[index])
		if brokers[index] == "" || !validBroker(brokers[index]) {
			return Events{}, kitconfig.Invalid(
				EnvKafkaBrokers,
				"must contain comma-separated host:port brokers",
			)
		}
	}
	if len(brokers) != 1 || brokers[0] != expectedBroker {
		return Events{}, kitconfig.Invalid(
			EnvKafkaBrokers,
			"must select the fixed TLS bootstrap for the deployment environment",
		)
	}
	group, err := kitconfig.ValueOrDefault(lookup, EnvKafkaGroup, defaultKafkaGroup)
	if err != nil {
		return Events{}, err
	}
	if group != defaultKafkaGroup {
		return Events{}, kitconfig.Invalid(
			EnvKafkaGroup,
			"must select the ordinary Document consumer group",
		)
	}
	topic, err := kitconfig.ValueOrDefault(
		lookup,
		EnvKafkaRegisteredTopic,
		defaultRegisteredTopic,
	)
	if err != nil {
		return Events{}, err
	}
	if topic != defaultRegisteredTopic {
		return Events{}, kitconfig.Invalid(
			EnvKafkaRegisteredTopic,
			"must select the ordinary Document registration topic",
		)
	}
	caFile, err := required(lookup, EnvKafkaCAFile)
	if err != nil {
		return Events{}, err
	}
	jsreportValue, err := required(lookup, EnvJSReportURL)
	if err != nil {
		return Events{}, err
	}
	jsreportOrigin, parseErr := url.Parse(jsreportValue)
	if parseErr != nil || jsreportValue != expectedJSReport ||
		jsreportOrigin.Scheme != "http" || jsreportOrigin.Hostname() == "" ||
		jsreportOrigin.Port() != "5488" || jsreportOrigin.User != nil ||
		jsreportOrigin.Opaque != "" || jsreportOrigin.RawQuery != "" ||
		jsreportOrigin.Fragment != "" ||
		jsreportOrigin.Path != "" {
		return Events{}, kitconfig.Invalid(
			EnvJSReportURL,
			"must select the fixed private origin for the deployment environment",
		)
	}
	writerDatabaseURL, err := required(lookup, EnvWriterDatabaseURL)
	if err != nil {
		return Events{}, err
	}
	parsedDatabaseURL, parseErr := url.Parse(writerDatabaseURL)
	if parseErr != nil ||
		parsedDatabaseURL.Scheme != "postgres" &&
			parsedDatabaseURL.Scheme != "postgresql" ||
		parsedDatabaseURL.Hostname() == "" || parsedDatabaseURL.Path == "" ||
		parsedDatabaseURL.Path == "/" || parsedDatabaseURL.Fragment != "" {
		return Events{}, kitconfig.Invalid(
			EnvWriterDatabaseURL,
			"must be a PostgreSQL URL with a database",
		)
	}
	writerAccessKey, err := required(lookup, EnvWriterObjectAccessKey)
	if err != nil {
		return Events{}, err
	}
	writerSecretKey, err := required(lookup, EnvWriterObjectSecretKey)
	if err != nil {
		return Events{}, err
	}
	writerDatabase := readerDatabase
	writerDatabase.URL = DatabaseURL{value: writerDatabaseURL}
	writerObjectStore := readerObjectStore
	writerObjectStore.AccessKey = Secret{value: writerAccessKey}
	writerObjectStore.SecretKey = Secret{value: writerSecretKey}
	return Events{
		Enabled: true, Brokers: brokers, Group: group, RegisteredTopic: topic,
		CAFile: caFile, JSReportOrigin: jsreportOrigin,
		WriterDatabase: writerDatabase, WriterObjectStore: writerObjectStore,
		RendererConnectTimeout: defaultRendererConnectTimeout,
		RendererRequestTimeout: defaultRendererRequestTimeout,
		UploadTimeout:          defaultUploadTimeout,
	}, nil
}

func eventTargets(environment string) (string, string, bool) {
	switch environment {
	case "local-dev":
		return "local-dev-kafka-kafka-bootstrap:9093",
			"http://local-dev-jsreport.local-dev-all.svc.cluster.local:5488", true
	case "qa":
		return "qa-kafka-kafka-bootstrap.qa-data.svc:9093",
			"http://qa-jsreport.qa-svc.svc.cluster.local:5488", true
	default:
		return "", "", false
	}
}

func validBroker(value string) bool {
	parsed, err := url.Parse("tcp://" + value)
	return err == nil && parsed.Host == value && parsed.Hostname() != "" && parsed.Port() != ""
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
	bucket, err := kitconfig.ValueOrDefault(lookup, objectBucketSetting, "policies")
	if err != nil {
		return ObjectStore{}, err
	}
	if bucket != "policies" {
		return ObjectStore{}, kitconfig.Invalid(
			objectBucketSetting,
			"must match the fixed policies bucket",
		)
	}
	fetchTimeout, err := kitconfig.LoadPositiveDuration(lookup, objectFetchTimeoutSetting, "3s")
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

func required(lookup lookupEnv, name string) (string, error) {
	value, exists := lookup(name)
	if !exists || value == "" {
		return "", kitconfig.Invalid(name, "is required")
	}
	return value, nil
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
