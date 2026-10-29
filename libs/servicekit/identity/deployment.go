package identity

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

const (
	environmentSetting     = "DEPLOYMENT_ENVIRONMENT"
	serviceNameSetting     = "SERVICE_NAME"
	serviceVersionSetting  = "SERVICE_VERSION"
	serviceInstanceSetting = "SERVICE_INSTANCE_ID"
)

var (
	releaseVersionPattern = regexp.MustCompile(`^sha-[0-9a-f]{7,40}$`)
	testVersionPattern    = regexp.MustCompile(`^test-[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ValidationError contains only fixed setting names and reasons owned by this package.
// It never retains a rejected identity value.
type ValidationError struct {
	setting string
	reason  string
}

func (err *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", err.setting, err.reason)
}

// Setting returns the setting associated with a deployment identity failure.
func (err *ValidationError) Setting() string { return err.setting }

// Reason returns a fixed, value-free explanation of a deployment identity failure.
func (err *ValidationError) Reason() string { return err.reason }

func invalid(setting, reason string) error {
	return &ValidationError{setting: setting, reason: reason}
}

// ValidateDeployment applies the opt-in deployment identity policy. The caller
// supplies its expected service name and decides when to load or generate values.
// Every valid environment except development and test uses the release identity
// format, so new release environment names need no library registration.
func ValidateDeployment(value Identity, expectedServiceName string, telemetryEnabled bool) error {
	if value.ServiceNamespace != "insurance-hub" || value.Implementation != "go" {
		return invalid(
			"telemetry identity",
			"must use the insurance-hub namespace and Go implementation",
		)
	}
	if value.ServiceName == "" || strings.TrimSpace(value.ServiceName) != value.ServiceName {
		return invalid(serviceNameSetting, "must not be blank or padded")
	}
	if telemetryEnabled && value.ServiceName != expectedServiceName {
		return invalid(
			serviceNameSetting,
			"must match the expected service name when telemetry is enabled",
		)
	}
	if !validEnvironmentName(value.Environment) {
		return invalid(environmentSetting, "must be a valid lowercase environment name")
	}

	switch value.Environment {
	case "development":
		if telemetryEnabled {
			return invalid(
				environmentSetting,
				"must not be development when telemetry is enabled",
			)
		}
		if value.ServiceVersion != "development" ||
			!strings.HasPrefix(value.ServiceInstance, "local-") {
			return invalid(
				"telemetry identity",
				"must use development identity when disabled locally",
			)
		}
	case "test":
		if !testVersionPattern.MatchString(value.ServiceVersion) {
			return invalid(serviceVersionSetting, "must be a test revision")
		}
		if !validUUID(value.ServiceInstance) {
			return invalid(serviceInstanceSetting, "must be a UUID in test mode")
		}
	default:
		if !releaseVersionPattern.MatchString(value.ServiceVersion) {
			return invalid(
				serviceVersionSetting,
				"must be an immutable sha version in a release environment",
			)
		}
		if !validUUID(value.ServiceInstance) {
			return invalid(
				serviceInstanceSetting,
				"must be a Pod UID in a release environment",
			)
		}
	}
	return nil
}

func validEnvironmentName(value string) bool {
	if len(value) == 0 || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		char := value[index]
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
		case char == '-' && index < len(value)-1:
		default:
			return false
		}
	}
	return true
}

// validUUID retains Product's existing 36-character hexadecimal-and-dash
// acceptance, without imposing a UUID version or variant.
func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' ||
		value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil
}
