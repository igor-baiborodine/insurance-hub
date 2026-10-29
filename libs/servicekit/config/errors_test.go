package config_test

import (
	"fmt"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

func TestErrorDetails_ExtractsWrappedDeploymentIdentityFailure(t *testing.T) {
	// given
	value := identity.Identity{
		ServiceNamespace: "insurance-hub",
		ServiceName:      "product-service",
		ServiceVersion:   "private-version-marker",
		ServiceInstance:  "123e4567-e89b-12d3-a456-426614174000",
		Environment:      "staging",
		Implementation:   "go",
	}
	validation := identity.ValidateDeployment(value, "product-service", true)
	if validation == nil {
		t.Fatal("invalid release version accepted")
	}

	// when
	setting, reason := config.ErrorDetails(fmt.Errorf("private-wrapper: %w", validation))

	// then
	if setting != "SERVICE_VERSION" ||
		reason != "must be an immutable sha version in a release environment" {
		t.Errorf(
			"safe details = (%q, %q), want fixed release version details",
			setting,
			reason,
		)
	}
}
