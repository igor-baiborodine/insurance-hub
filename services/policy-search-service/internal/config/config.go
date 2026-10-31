package config

import (
	"crypto/rand"
	"encoding/hex"
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
	httpAddressSetting             = "HTTP_ADDR"
	elasticsearchURLSetting        = "ELASTICSEARCH_URL"
	elasticsearchUsernameSetting   = "ELASTICSEARCH_USERNAME"
	elasticsearchPasswordSetting   = "ELASTICSEARCH_PASSWORD"
	elasticsearchCACertFileSetting = "ELASTICSEARCH_CA_CERT_FILE"
	elasticsearchTimeoutSetting    = "ELASTICSEARCH_TIMEOUT"
	startupTimeoutSetting          = "STARTUP_TIMEOUT"
)

// Config contains the immutable settings parsed and validated during process startup.
type Config struct {
	kitconfig.Runtime
	ServiceName    string
	HTTPAddress    Address
	HealthAddress  Address
	Elasticsearch  Elasticsearch
	StartupTimeout time.Duration
	kitconfig.HTTPServer
	kitconfig.Telemetry
}

// Elasticsearch contains the verified endpoint, credentials, trust roots and request bound.
type Elasticsearch struct {
	URL      string
	Username string
	Password Secret
	CACert   []byte
	Timeout  time.Duration
}

// Secret prevents credentials from being exposed through ordinary formatting.
type Secret struct{ value string }

// Value returns the credential only to the runtime composition boundary.
func (secret Secret) Value() string { return secret.value }

func (Secret) String() string   { return "[REDACTED]" }
func (Secret) GoString() string { return "[REDACTED]" }

// Address is a validated TCP listen address with a host and numeric port.
type Address = kitconfig.Address

// Identity is the application-owned resource and log identity. Kubernetes fields are added by Alloy.
type Identity = kitidentity.Identity

// HTTPServer contains business HTTP server time bounds.
type HTTPServer = kitconfig.HTTPServer

// Telemetry contains the optional OTLP trace and metric export configuration.
type Telemetry = kitconfig.Telemetry

// DevelopmentIdentity labels startup diagnostics before configuration is validated.
func DevelopmentIdentity(serviceName string) Identity {
	return kitidentity.DevelopmentIdentity(serviceName, os.Getpid())
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

	httpAddress, err := kitconfig.LoadAddress(
		lookup, httpAddressSetting, kitconfig.DefaultHTTPAddress,
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
	elasticsearchSettings, err := loadElasticsearch(lookup)
	if err != nil {
		return Config{}, err
	}
	startupTimeout, err := kitconfig.LoadPositiveDuration(
		lookup, startupTimeoutSetting, "10s",
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
	if otelEnabled && otelTimeout != telemetryDefaults.ExporterTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvOTELExporterOTLPTimeout,
			"must be 2s when telemetry is enabled",
		)
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
	httpServer, err := kitconfig.LoadHTTPServer(lookup)
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
		Identity:         identity,
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
	if runtime.RequestTimeout > httpServer.WriteTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvRequestTimeout,
			"must not exceed HTTP_WRITE_TIMEOUT",
		)
	}
	if otelTimeout > runtime.ShutdownTimeout {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvOTELExporterOTLPTimeout,
			"must not exceed SHUTDOWN_TIMEOUT",
		)
	}
	if httpAddress.String() == healthAddress.String() {
		return Config{}, kitconfig.Invalid(
			kitconfig.EnvHealthAddress,
			"must be distinct from HTTP_ADDR",
		)
	}

	return Config{
		Runtime:        runtime,
		HTTPServer:     httpServer,
		ServiceName:    serviceName,
		HTTPAddress:    httpAddress,
		HealthAddress:  healthAddress,
		Elasticsearch:  elasticsearchSettings,
		StartupTimeout: startupTimeout,
		Telemetry:      telemetry,
	}, nil
}

func loadElasticsearch(lookup lookupEnv) (Elasticsearch, error) {
	endpoint, err := kitconfig.ValueOrDefault(
		lookup, elasticsearchURLSetting, "http://127.0.0.1:9200",
	)
	if err != nil {
		return Elasticsearch{}, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		!kitconfig.ValidHost(parsed.Hostname()) || parsed.Port() == "" ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Elasticsearch{}, kitconfig.Invalid(
			elasticsearchURLSetting,
			"must be an HTTP or HTTPS URL with a host and numeric port",
		)
	}
	port, err := strconv.ParseUint(parsed.Port(), 10, 16)
	if err != nil || port == 0 {
		return Elasticsearch{}, kitconfig.Invalid(
			elasticsearchURLSetting,
			"must be an HTTP or HTTPS URL with a host and numeric port",
		)
	}
	username, usernameSet := lookup(elasticsearchUsernameSetting)
	password, passwordSet := lookup(elasticsearchPasswordSetting)
	if usernameSet && strings.TrimSpace(username) == "" {
		return Elasticsearch{}, kitconfig.Invalid(
			elasticsearchUsernameSetting, "must not be blank",
		)
	}
	if passwordSet && password == "" {
		return Elasticsearch{}, kitconfig.Invalid(
			elasticsearchPasswordSetting, "must not be empty",
		)
	}
	if usernameSet != passwordSet {
		return Elasticsearch{}, kitconfig.Invalid(
			elasticsearchUsernameSetting,
			"must be supplied together with ELASTICSEARCH_PASSWORD",
		)
	}
	timeout, err := kitconfig.LoadPositiveDuration(
		lookup, elasticsearchTimeoutSetting, "5s",
	)
	if err != nil {
		return Elasticsearch{}, err
	}
	var caCert []byte
	if path, exists := lookup(elasticsearchCACertFileSetting); exists {
		if strings.TrimSpace(path) == "" {
			return Elasticsearch{}, kitconfig.Invalid(
				elasticsearchCACertFileSetting, "must not be blank",
			)
		}
		caCert, err = os.ReadFile(path)
		if err != nil {
			return Elasticsearch{}, kitconfig.Invalid(
				elasticsearchCACertFileSetting,
				"must be a readable certificate file",
			)
		}
		if len(caCert) == 0 {
			return Elasticsearch{}, kitconfig.Invalid(
				elasticsearchCACertFileSetting, "must not be empty",
			)
		}
		if parsed.Scheme != "https" {
			return Elasticsearch{}, kitconfig.Invalid(
				elasticsearchCACertFileSetting,
				"requires an HTTPS Elasticsearch URL",
			)
		}
	}
	return Elasticsearch{
		URL: endpoint, Username: username, Password: Secret{value: password},
		CACert: caCert, Timeout: timeout,
	}, nil
}

func loadIdentity(lookup lookupEnv, serviceName string, enabled bool) (Identity, error) {
	defaultEnvironment := "development"
	if enabled {
		defaultEnvironment = ""
	}
	environment, err := kitconfig.ValueOrDefault(
		lookup,
		kitconfig.EnvDeploymentEnvironment,
		defaultEnvironment,
	)
	if err != nil {
		return Identity{}, err
	}
	version, err := kitconfig.ValueOrDefault(
		lookup,
		kitconfig.EnvServiceVersion,
		"development",
	)
	if err != nil {
		return Identity{}, err
	}
	instance, err := kitconfig.ValueOrDefault(lookup, kitconfig.EnvServiceInstanceID, "")
	if err != nil {
		return Identity{}, err
	}
	if environment == "test" && instance != "" {
		return Identity{}, kitconfig.Invalid(
			kitconfig.EnvServiceInstanceID,
			"must be generated in test mode",
		)
	}
	if environment == "test" {
		instance, err = randomInstanceID()
		if err != nil {
			return Identity{}, kitconfig.Invalid(
				kitconfig.EnvServiceInstanceID,
				"could not generate test instance ID",
			)
		}
	} else if environment == "development" && instance == "" {
		instance = DevelopmentIdentity(serviceName).ServiceInstance
	}
	identity := Identity{
		ServiceNamespace: "insurance-hub",
		ServiceName:      serviceName,
		ServiceVersion:   version,
		ServiceInstance:  instance,
		Environment:      environment,
		Implementation:   "go",
	}
	if err := kitidentity.ValidateDeployment(
		identity,
		DefaultServiceName,
		enabled,
	); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func randomInstanceID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" +
		encoded[16:20] + "-" + encoded[20:], nil
}
