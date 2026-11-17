package telemetry

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
)

func TestNew_DisabledProvider(t *testing.T) {
	// given
	settings := config.Telemetry{
		Identity:        config.DevelopmentIdentity(config.DefaultServiceName).Identity,
		ExporterTimeout: time.Second,
	}

	// when
	provider, err := New(context.Background(), config.DefaultServiceName, settings)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNew_InvalidIdentityDoesNotDiscloseValues(t *testing.T) {
	// given
	const private = "private-marker"
	settings := config.Telemetry{
		Enabled: true,
		Identity: identity.Identity{
			ServiceNamespace: "insurance-hub",
			ServiceName:      private,
			ServiceVersion:   "git-123456789abc",
			ServiceInstance:  "qa-chat-1",
			Environment:      "qa",
			Implementation:   "go",
		},
		ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
		ExporterTimeout:  time.Second,
	}

	// when
	_, err := New(context.Background(), config.DefaultServiceName, settings)

	// then
	if err == nil {
		t.Fatal("mismatched identity accepted")
	}
	if strings.Contains(err.Error(), private) {
		t.Fatalf("error disclosed identity: %v", err)
	}
}

func mustURL(value string) *url.URL {
	parsed, err := url.Parse(value)
	if err != nil {
		panic(err)
	}
	return parsed
}
