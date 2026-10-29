package identity_test

import (
	"testing"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
)

func TestDevelopmentIdentity_ReturnsSharedFields(t *testing.T) {
	// given
	const serviceName = "copy-service"
	const processID = 123
	want := identity.Identity{
		ServiceNamespace: "insurance-hub",
		ServiceName:      "copy-service",
		ServiceVersion:   "development",
		ServiceInstance:  "local-123",
		Environment:      "development",
		Implementation:   "go",
	}

	// when
	got := identity.DevelopmentIdentity(serviceName, processID)

	// then
	if got != want {
		t.Errorf(
			"DevelopmentIdentity(%q, %d) = %+v, want %+v",
			serviceName,
			processID,
			got,
			want,
		)
	}
}

func TestDevelopmentIdentity_LeavesCallerValuesUnvalidated(t *testing.T) {
	// given
	const serviceName = ""
	const processID = 456

	// when
	got := identity.DevelopmentIdentity(serviceName, processID)

	// then
	if got.ServiceName != "" || got.ServiceInstance != "local-456" {
		t.Errorf("caller values changed: %+v", got)
	}
}
