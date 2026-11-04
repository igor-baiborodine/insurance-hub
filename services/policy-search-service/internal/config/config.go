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
const DefaultServiceName = "policy-search-service"

const (
	EnvHTTPAddress              = "HTTP_ADDR"
	EnvHTTPMaxHeaderBytes       = "HTTP_MAX_HEADER_BYTES"
	EnvHTTPMaxBodyBytes         = "HTTP_MAX_BODY_BYTES"
	EnvElasticsearchURL         = "ELASTICSEARCH_URL"
	EnvElasticsearchIndex       = "ELASTICSEARCH_INDEX"
	EnvElasticsearchUsername    = "ELASTICSEARCH_USERNAME"
	EnvElasticsearchPassword    = "ELASTICSEARCH_PASSWORD"
	EnvElasticsearchCAFile      = "ELASTICSEARCH_CA_FILE"
	EnvElasticsearchTimeout     = "ELASTICSEARCH_TIMEOUT"
	EnvEventWorkersEnabled      = "EVENT_WORKERS_ENABLED"
	EnvKafkaBrokers             = "KAFKA_BROKERS"
	EnvKafkaGroup               = "KAFKA_GROUP"
	EnvKafkaRegisteredTopic     = "KAFKA_REGISTERED_TOPIC"
	EnvKafkaTerminatedTopic     = "KAFKA_TERMINATED_TOPIC"
	EnvKafkaCAFile              = "KAFKA_CA_FILE"
	EnvWriterUsername           = "ELASTICSEARCH_WRITER_USERNAME"
	EnvWriterPassword           = "ELASTICSEARCH_WRITER_PASSWORD"
	defaultMaxHeaderBytes       = 16384
	defaultMaxBodyBytes         = 4096
	defaultElasticsearchURL     = "http://127.0.0.1:9200"
	defaultElasticsearchIndex   = "policy-views-issue-229"
	defaultElasticsearchTimeout = 5 * time.Second
	defaultKafkaGroup           = "policy-search-service"
	defaultRegisteredTopic      = "policy-registered"
	defaultTerminatedTopic      = "policy-terminated"
)

// Config contains settings parsed and validated once during process startup.
type Config struct {
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
	Elasticsearch      Elasticsearch
	Events             Events
}

// Elasticsearch contains the bounded read-only dependency settings.
type Elasticsearch struct {
	URL      string
	Index    string
	Username string
	Password string
	CAFile   string
	Timeout  time.Duration
}

// Events contains the opt-in worker settings. Disabled workers need no dependency settings.
type Events struct {
	Enabled         bool
	Brokers         []string
	Group           string
	RegisteredTopic string
	TerminatedTopic string
	CAFile          string
	Writer          Elasticsearch
}

// Address uses the shared TCP address parser and preserves ephemeral ports.
type Address = kitconfig.Address

// GRPC is the shared message-size group; Policy Search owns its permitted ranges.
type GRPC = kitconfig.GRPC

// Identity adds Policy Search validation policy to the shared resource and log fields.
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

	grpcAddress, err := kitconfig.LoadAddress(
		lookup, kitconfig.EnvGRPCAddress, kitconfig.DefaultGRPCAddress,
	)
	if err != nil {
		return Config{}, err
	}
	httpAddress, err := kitconfig.LoadAddress(
		lookup, EnvHTTPAddress, kitconfig.DefaultHTTPAddress,
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
		lookup, EnvHTTPMaxHeaderBytes, strconv.Itoa(defaultMaxHeaderBytes), 1024, 1048576,
	)
	if err != nil {
		return Config{}, err
	}
	maxBodyBytes, err := kitconfig.LoadBoundedInt(
		lookup, EnvHTTPMaxBodyBytes, strconv.Itoa(defaultMaxBodyBytes), 1024, 1048576,
	)
	if err != nil {
		return Config{}, err
	}
	elasticsearchSettings, err := loadElasticsearch(lookup)
	if err != nil {
		return Config{}, err
	}
	eventSettings, err := loadEvents(lookup, elasticsearchSettings)
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
		{EnvHTTPAddress, httpAddress},
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
		Runtime:            runtime,
		GRPC:               grpc,
		HTTPServer:         httpServer,
		ServiceName:        serviceName,
		HTTPAddress:        httpAddress,
		GRPCAddress:        grpcAddress,
		HealthAddress:      healthAddress,
		HTTPMaxHeaderBytes: maxHeaderBytes,
		HTTPMaxBodyBytes:   maxBodyBytes,
		Elasticsearch:      elasticsearchSettings,
		Events:             eventSettings,
		Telemetry:          telemetry,
	}, nil
}

func loadEvents(lookup lookupEnv, reader Elasticsearch) (Events, error) {
	enabledValue, err := kitconfig.ValueOrDefault(lookup, EnvEventWorkersEnabled, "false")
	if err != nil {
		return Events{}, err
	}
	enabled, err := kitconfig.ParseBool(EnvEventWorkersEnabled, enabledValue)
	if err != nil {
		return Events{}, err
	}
	if !enabled {
		return Events{}, nil
	}
	brokersValue, err := requiredSetting(lookup, EnvKafkaBrokers)
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
	group, err := settingOrDefault(lookup, EnvKafkaGroup, defaultKafkaGroup)
	if err != nil {
		return Events{}, err
	}
	registered, err := settingOrDefault(lookup, EnvKafkaRegisteredTopic, defaultRegisteredTopic)
	if err != nil {
		return Events{}, err
	}
	terminated, err := settingOrDefault(lookup, EnvKafkaTerminatedTopic, defaultTerminatedTopic)
	if err != nil {
		return Events{}, err
	}
	if registered == terminated {
		return Events{}, kitconfig.Invalid(
			EnvKafkaTerminatedTopic,
			"must differ from KAFKA_REGISTERED_TOPIC",
		)
	}
	caFile, err := requiredSetting(lookup, EnvKafkaCAFile)
	if err != nil {
		return Events{}, err
	}
	username, err := requiredSetting(lookup, EnvWriterUsername)
	if err != nil {
		return Events{}, err
	}
	password, err := requiredSetting(lookup, EnvWriterPassword)
	if err != nil {
		return Events{}, err
	}
	writer := reader
	writer.Username = username
	writer.Password = password
	return Events{
		Enabled: enabled, Brokers: brokers, Group: group,
		RegisteredTopic: registered, TerminatedTopic: terminated,
		CAFile: caFile, Writer: writer,
	}, nil
}

func requiredSetting(lookup lookupEnv, name string) (string, error) {
	value, err := optionalSetting(lookup, name)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", kitconfig.Invalid(name, "is required when event workers are enabled")
	}
	return value, nil
}

func settingOrDefault(lookup lookupEnv, name, fallback string) (string, error) {
	return kitconfig.ValueOrDefault(lookup, name, fallback)
}

func validBroker(value string) bool {
	parsed, err := url.Parse("tcp://" + value)
	return err == nil && parsed.Host == value && parsed.Hostname() != "" && parsed.Port() != ""
}

func loadElasticsearch(lookup lookupEnv) (Elasticsearch, error) {
	endpoint, err := kitconfig.ValueOrDefault(
		lookup,
		EnvElasticsearchURL,
		defaultElasticsearchURL,
	)
	if err != nil {
		return Elasticsearch{}, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed == nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return Elasticsearch{}, kitconfig.Invalid(
			EnvElasticsearchURL,
			"must be an HTTP or HTTPS origin without credentials or a path",
		)
	}
	index, err := settingOrDefault(lookup, EnvElasticsearchIndex, defaultElasticsearchIndex)
	if err != nil {
		return Elasticsearch{}, err
	}
	if !validElasticsearchIndex(index) {
		return Elasticsearch{}, kitconfig.Invalid(
			EnvElasticsearchIndex,
			"must be a lowercase Elasticsearch index name",
		)
	}
	username, err := optionalSetting(lookup, EnvElasticsearchUsername)
	if err != nil {
		return Elasticsearch{}, err
	}
	password, err := optionalSetting(lookup, EnvElasticsearchPassword)
	if err != nil {
		return Elasticsearch{}, err
	}
	if (username == "") != (password == "") {
		return Elasticsearch{}, kitconfig.Invalid(
			EnvElasticsearchUsername,
			"must be configured together with ELASTICSEARCH_PASSWORD",
		)
	}
	caFile, err := optionalSetting(lookup, EnvElasticsearchCAFile)
	if err != nil {
		return Elasticsearch{}, err
	}
	if caFile != "" && parsed.Scheme != "https" {
		return Elasticsearch{}, kitconfig.Invalid(
			EnvElasticsearchCAFile,
			"requires an HTTPS Elasticsearch URL",
		)
	}
	timeout, err := kitconfig.LoadPositiveDuration(
		lookup,
		EnvElasticsearchTimeout,
		defaultElasticsearchTimeout.String(),
	)
	if err != nil {
		return Elasticsearch{}, err
	}
	if timeout > defaultElasticsearchTimeout {
		return Elasticsearch{}, kitconfig.Invalid(
			EnvElasticsearchTimeout,
			"must not exceed 5s",
		)
	}
	return Elasticsearch{
		URL:      endpoint,
		Index:    index,
		Username: username,
		Password: password,
		CAFile:   caFile,
		Timeout:  timeout,
	}, nil
}

func validElasticsearchIndex(value string) bool {
	if len(value) == 0 || len(value) > 255 || value == "." || value == ".." {
		return false
	}
	for index, char := range value {
		if index == 0 && (char == '-' || char == '_' || char == '+') {
			return false
		}
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') &&
			char != '-' && char != '_' && char != '.' {
			return false
		}
	}
	return true
}

func optionalSetting(lookup lookupEnv, name string) (string, error) {
	value, present := lookup(name)
	if present && value == "" {
		return "", kitconfig.Invalid(name, "must not be empty when set")
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
