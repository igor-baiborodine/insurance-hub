package telemetry

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
)

func TestNew_DisabledProviderAndW3CPropagation(t *testing.T) {
	// given
	settings := config.Telemetry{
		Identity:        config.DevelopmentIdentity("document-service").Identity,
		ExporterTimeout: time.Second,
	}

	// when
	provider, err := New(context.Background(), "document-service", settings)
	// then
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	_, span := provider.TracerProvider().Tracer("test").Start(context.Background(), "disabled")
	if span.IsRecording() {
		t.Error("disabled provider records spans")
	}
	span.End()
	traceID := trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	spanID := trace.SpanID{17, 18, 19, 20, 21, 22, 23, 24}
	ctx := trace.ContextWithSpanContext(
		context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
		}),
	)
	carrier := propagation.MapCarrier{}
	provider.GRPCPropagator().Inject(ctx, carrier)
	extracted := trace.SpanContextFromContext(
		provider.GRPCPropagator().Extract(context.Background(), carrier),
	)
	if carrier.Get("traceparent") != "00-"+traceID.String()+"-"+spanID.String()+"-01" ||
		extracted.TraceID() != traceID ||
		!extracted.IsRemote() {
		t.Errorf("gRPC trace context was not preserved: %v", carrier)
	}
}

func TestNew_RejectsDocumentIdentityAndExporterSettings(t *testing.T) {
	// given
	valid := config.Telemetry{
		Enabled:          true,
		Identity:         enabledIdentity(),
		ExporterTimeout:  time.Second,
		ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
	}
	for _, test := range []struct {
		name          string
		mutate        func(*config.Telemetry)
		serviceName   string
		rejectedValue string
	}{
		{"missing service", func(*config.Telemetry) {}, "", ""},
		{"service mismatch", func(*config.Telemetry) {}, "mismatched-service-marker", "mismatched-service-marker"},
		{"foreign service", func(s *config.Telemetry) {
			s.Identity.ServiceName = "foreign-service-marker"
		}, "foreign-service-marker", "foreign-service-marker"},
		{"missing endpoint", func(s *config.Telemetry) { s.ExporterEndpoint = nil }, "document-service", ""},
		{"development environment", func(s *config.Telemetry) {
			s.Identity.Environment = "development"
		}, "document-service", "development"},
		{"development version", func(s *config.Telemetry) {
			s.Identity.ServiceVersion = "development"
		}, "document-service", "development"},
		{"local instance", func(s *config.Telemetry) {
			s.Identity.ServiceInstance = "local-private-marker"
		}, "document-service", "local-private-marker"},
		{"wrong namespace", func(s *config.Telemetry) {
			s.Identity.ServiceNamespace = "private-namespace-marker"
		}, "document-service", "private-namespace-marker"},
		{"wrong implementation", func(s *config.Telemetry) {
			s.Identity.Implementation = "private-implementation-marker"
		}, "document-service", "private-implementation-marker"},
		{"noncanonical version", func(s *config.Telemetry) {
			s.Identity.ServiceVersion = "sha-private-marker"
		}, "document-service", "sha-private-marker"},
		{"zero version", func(s *config.Telemetry) {
			s.Identity.ServiceVersion = "git-000000000000"
		}, "document-service", "git-000000000000"},
		{"invalid instance", func(s *config.Telemetry) {
			s.Identity.ServiceInstance = "private-marker\n"
		}, "document-service", "private-marker"},
		{"zero exporter timeout", func(s *config.Telemetry) { s.ExporterTimeout = 0 }, "document-service", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			settings := valid
			test.mutate(&settings)

			// when
			_, err := New(
				context.Background(),
				test.serviceName,
				settings,
			)

			// then
			if err == nil {
				t.Fatal("invalid settings accepted")
			}
			if test.rejectedValue != "" &&
				strings.Contains(err.Error(), test.rejectedValue) {
				t.Fatalf("configuration value disclosed: %v", err)
			}
		})
	}
}

func enabledIdentity() identity.Identity {
	return identity.Identity{
		ServiceNamespace: "insurance-hub",
		ServiceName:      "document-service",
		ServiceVersion:   "git-000000000001",
		ServiceInstance:  "test-instance",
		Environment:      "test",
		Implementation:   "go",
	}
}

func testTelemetrySettings(timeout time.Duration) config.Telemetry {
	return config.Telemetry{
		Enabled:          true,
		Identity:         enabledIdentity(),
		ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
		ExporterTimeout:  timeout,
	}
}

func mustURL(value string) *url.URL {
	parsed, err := url.Parse(value)
	if err != nil {
		panic(err)
	}
	return parsed
}
