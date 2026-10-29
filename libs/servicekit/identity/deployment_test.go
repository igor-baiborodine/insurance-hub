package identity_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

const releaseInstance = "123e4567-e89b-12d3-a456-426614174000"

func deploymentIdentity() identity.Identity {
	return identity.Identity{
		ServiceNamespace: "insurance-hub",
		ServiceName:      "product-service",
		ServiceVersion:   "sha-6c4669c81d3a",
		ServiceInstance:  releaseInstance,
		Environment:      "qa",
		Implementation:   "go",
	}
}

func TestValidateDeployment_AcceptsEnvironmentKinds(t *testing.T) {
	// given
	cases := []struct {
		name                string
		environment         string
		version             string
		instance            string
		serviceName         string
		expectedServiceName string
		enabled             bool
	}{
		{
			"QA release",
			"qa",
			"sha-6c4669c81d3a",
			releaseInstance,
			"product-service",
			"product-service",
			true,
		},
		{
			"staging release",
			"staging",
			"sha-abcdef0",
			releaseInstance,
			"product-service",
			"product-service",
			true,
		},
		{
			"uppercase UUID hex",
			"staging",
			"sha-abcdef0",
			"123E4567-E89B-12D3-A456-426614174000",
			"product-service",
			"product-service",
			true,
		},
		{
			"preprod release",
			"preprod",
			"sha-0123456789abcdef",
			releaseInstance,
			"product-service",
			"product-service",
			true,
		},
		{
			"unlisted release",
			"preview-42",
			"sha-abcdef0",
			releaseInstance,
			"product-service",
			"product-service",
			true,
		},
		{
			"single-letter release",
			"a",
			"sha-abcdef0",
			releaseInstance,
			"product-service",
			"product-service",
			true,
		},
		{
			"longest release name",
			"a" + strings.Repeat("b", 62),
			"sha-abcdef0",
			releaseInstance,
			"product-service",
			"product-service",
			true,
		},
		{
			"disabled release",
			"production",
			"sha-abcdef0",
			releaseInstance,
			"copy-service",
			"product-service",
			false,
		},
		{
			"test revision and non-v4 UUID",
			"test",
			"test-config.overrides_1",
			releaseInstance,
			"product-service",
			"product-service",
			true,
		},
		{
			"disabled development",
			"development",
			"development",
			"local-123",
			"product-service",
			"product-service",
			false,
		},
		{
			"existing local prefix rule",
			"development",
			"development",
			"local-",
			"product-service",
			"product-service",
			false,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// given
			value := identity.Identity{
				ServiceNamespace: "insurance-hub",
				ServiceName:      test.serviceName,
				ServiceVersion:   test.version,
				ServiceInstance:  test.instance,
				Environment:      test.environment,
				Implementation:   "go",
			}

			// when
			err := identity.ValidateDeployment(
				value,
				test.expectedServiceName,
				test.enabled,
			)
			// then
			if err != nil {
				t.Errorf(
					"ValidateDeployment(%q) = %v, want nil",
					test.environment,
					err,
				)
			}
		})
	}
}

func TestValidateDeployment_RejectsInvalidIdentitySafely(t *testing.T) {
	// given
	const privateMarker = "private-identity-marker"
	cases := []struct {
		name        string
		change      func(*identity.Identity)
		enabled     bool
		wantSetting string
		wantReason  string
	}{
		{
			"wrong namespace",
			func(v *identity.Identity) { v.ServiceNamespace = "other" },
			true,
			"telemetry identity",
			"must use the insurance-hub namespace and Go implementation",
		},
		{
			"wrong implementation",
			func(v *identity.Identity) { v.Implementation = "java" },
			true,
			"telemetry identity",
			"must use the insurance-hub namespace and Go implementation",
		},
		{
			"empty service name",
			func(v *identity.Identity) { v.ServiceName = "" },
			true,
			"SERVICE_NAME",
			"must not be blank or padded",
		},
		{
			"padded service name",
			func(v *identity.Identity) { v.ServiceName = " product-service" },
			true,
			"SERVICE_NAME",
			"must not be blank or padded",
		},
		{
			"wrong enabled service",
			func(v *identity.Identity) { v.ServiceName = privateMarker },
			true,
			"SERVICE_NAME",
			"must match the expected service name when telemetry is enabled",
		},
		{
			"empty environment",
			func(v *identity.Identity) { v.Environment = "" },
			true,
			"DEPLOYMENT_ENVIRONMENT",
			"must be a valid lowercase environment name",
		},
		{
			"padded environment",
			func(v *identity.Identity) { v.Environment = " staging" },
			true,
			"DEPLOYMENT_ENVIRONMENT",
			"must be a valid lowercase environment name",
		},
		{
			"uppercase environment",
			func(v *identity.Identity) { v.Environment = "Staging" },
			true,
			"DEPLOYMENT_ENVIRONMENT",
			"must be a valid lowercase environment name",
		},
		{
			"slash environment",
			func(v *identity.Identity) { v.Environment = "pre/prod" },
			true,
			"DEPLOYMENT_ENVIRONMENT",
			"must be a valid lowercase environment name",
		},
		{
			"underscore environment",
			func(v *identity.Identity) { v.Environment = "pre_prod" },
			true,
			"DEPLOYMENT_ENVIRONMENT",
			"must be a valid lowercase environment name",
		},
		{
			"trailing hyphen",
			func(v *identity.Identity) { v.Environment = "preview-" },
			true,
			"DEPLOYMENT_ENVIRONMENT",
			"must be a valid lowercase environment name",
		},
		{
			"overlong environment",
			func(v *identity.Identity) { v.Environment = "a" + strings.Repeat("b", 63) },
			true,
			"DEPLOYMENT_ENVIRONMENT",
			"must be a valid lowercase environment name",
		},
		{
			"mutable QA version",
			func(v *identity.Identity) { v.ServiceVersion = "latest" },
			true,
			"SERVICE_VERSION",
			"must be an immutable sha version in a release environment",
		},
		{
			"mutable staging version",
			func(v *identity.Identity) { v.Environment, v.ServiceVersion = "staging", privateMarker },
			true,
			"SERVICE_VERSION",
			"must be an immutable sha version in a release environment",
		},
		{
			"short release SHA",
			func(v *identity.Identity) { v.ServiceVersion = "sha-abcdef" },
			true,
			"SERVICE_VERSION",
			"must be an immutable sha version in a release environment",
		},
		{
			"local release instance",
			func(v *identity.Identity) { v.Environment, v.ServiceInstance = "preprod", "local-123" },
			true,
			"SERVICE_INSTANCE_ID",
			"must be a Pod UID in a release environment",
		},
		{
			"bad release UUID",
			func(v *identity.Identity) { v.ServiceInstance = privateMarker },
			true,
			"SERVICE_INSTANCE_ID",
			"must be a Pod UID in a release environment",
		},
		{
			"misplaced UUID dash",
			func(v *identity.Identity) {
				v.ServiceInstance = "123e4567e-89b-12d3-a456-426614174000"
			},
			true,
			"SERVICE_INSTANCE_ID",
			"must be a Pod UID in a release environment",
		},
		{
			"bad test revision",
			func(v *identity.Identity) { v.Environment, v.ServiceVersion = "test", "test-" },
			true,
			"SERVICE_VERSION",
			"must be a test revision",
		},
		{"bad test UUID", func(v *identity.Identity) {
			v.Environment, v.ServiceVersion, v.ServiceInstance = "test", "test-123", privateMarker
		}, true, "SERVICE_INSTANCE_ID", "must be a UUID in test mode"},
		{"enabled development", func(v *identity.Identity) {
			v.Environment, v.ServiceVersion, v.ServiceInstance = "development", "development", "local-123"
		}, true, "DEPLOYMENT_ENVIRONMENT", "must not be development when telemetry is enabled"},
		{"bad development version", func(v *identity.Identity) {
			v.Environment, v.ServiceVersion, v.ServiceInstance = "development", "latest", "local-123"
		}, false, "telemetry identity", "must use development identity when disabled locally"},
		{"bad development instance", func(v *identity.Identity) {
			v.Environment, v.ServiceVersion, v.ServiceInstance = "development", "development", privateMarker
		}, false, "telemetry identity", "must use development identity when disabled locally"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// given
			value := deploymentIdentity()
			test.change(&value)

			// when
			err := identity.ValidateDeployment(value, "product-service", test.enabled)

			// then
			if err == nil {
				t.Fatal("invalid identity accepted")
			}
			validation, ok := errors.AsType[*identity.ValidationError](err)
			if !ok {
				t.Fatalf("error type = %T, want *identity.ValidationError", err)
			}
			if validation.Setting() != test.wantSetting ||
				validation.Reason() != test.wantReason {
				t.Errorf(
					"details = (%q, %q), want (%q, %q)",
					validation.Setting(),
					validation.Reason(),
					test.wantSetting,
					test.wantReason,
				)
			}
			if strings.Contains(err.Error(), privateMarker) {
				t.Errorf("error exposes rejected value: %v", err)
			}
		})
	}
}

func TestValidateDeployment_WrappedErrorRetainsSafeDetails(t *testing.T) {
	// given
	value := deploymentIdentity()
	value.ServiceVersion = "private-identity-marker"

	// when
	err := fmt.Errorf(
		"private-wrapper: %w",
		identity.ValidateDeployment(value, "product-service", true),
	)

	// then
	validation, ok := errors.AsType[*identity.ValidationError](err)
	if !ok {
		t.Fatalf("wrapped error type = %T, want identity validation error", err)
	}
	if validation.Setting() != "SERVICE_VERSION" ||
		validation.Reason() != "must be an immutable sha version in a release environment" {
		t.Errorf("wrapped details = (%q, %q)", validation.Setting(), validation.Reason())
	}
	if strings.Contains(validation.Error(), "private-identity-marker") ||
		strings.Contains(validation.Error(), "private-wrapper") {
		t.Errorf("validator error exposes untrusted text: %v", validation)
	}
}
