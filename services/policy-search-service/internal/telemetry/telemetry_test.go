package telemetry

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
)

func TestNew_DisabledProviderAndHTTPPropagation(t *testing.T) {
	// given
	settings := config.Telemetry{
		Identity:        config.DevelopmentIdentity("policy-search-service"),
		ExporterTimeout: time.Second,
	}
	provider, err := New(context.Background(), "policy-search-service", settings)
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
	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/policies", nil)
	if err != nil {
		t.Fatal(err)
	}

	// when
	provider.HTTPPropagator().Inject(ctx, propagation.HeaderCarrier(request.Header))
	w3cParent := trace.SpanContextFromContext(provider.HTTPPropagator().Extract(
		context.Background(),
		propagation.HeaderCarrier(http.Header{
			"Traceparent": {"00-" + traceID.String() + "-" + spanID.String() + "-01"},
		}),
	))
	b3Parent := trace.SpanContextFromContext(provider.HTTPPropagator().Extract(
		context.Background(),
		propagation.HeaderCarrier(http.Header{
			"X-B3-Traceid": {traceID.String()},
			"X-B3-Spanid":  {spanID.String()},
			"X-B3-Sampled": {"1"},
		}),
	))

	// then
	if request.Header.Get("Traceparent") !=
		"00-"+traceID.String()+"-"+spanID.String()+"-01" ||
		request.Header.Get("X-B3-Traceid") != traceID.String() ||
		request.Header.Get("X-B3-Spanid") != spanID.String() ||
		request.Header.Get("X-B3-Sampled") != "1" {
		t.Errorf("HTTP trace context injection = %v", request.Header)
	}
	if w3cParent.TraceID() != traceID || w3cParent.SpanID() != spanID ||
		!w3cParent.IsRemote() {
		t.Errorf(
			"W3C parent = %s/%s remote=%t",
			w3cParent.TraceID(),
			w3cParent.SpanID(),
			w3cParent.IsRemote(),
		)
	}
	if b3Parent.TraceID() != traceID || b3Parent.SpanID() != spanID ||
		!b3Parent.IsRemote() {
		t.Errorf(
			"B3 parent = %s/%s remote=%t",
			b3Parent.TraceID(),
			b3Parent.SpanID(),
			b3Parent.IsRemote(),
		)
	}
}

func TestNew_RejectsInvalidIdentityAndExporterSettings(t *testing.T) {
	// given
	valid := config.Telemetry{
		Enabled:          true,
		Identity:         enabledIdentity(),
		ExporterTimeout:  time.Second,
		ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
	}
	for _, test := range []struct {
		name        string
		mutate      func(*config.Telemetry)
		serviceName string
	}{
		{"missing service", func(*config.Telemetry) {}, ""},
		{"service mismatch", func(*config.Telemetry) {}, "other"},
		{"missing endpoint", func(s *config.Telemetry) { s.ExporterEndpoint = nil }, "policy-search-service"},
		{"development identity", func(s *config.Telemetry) {
			s.Identity = config.DevelopmentIdentity("policy-search-service")
		}, "policy-search-service"},
		{"wrong namespace", func(s *config.Telemetry) { s.Identity.ServiceNamespace = "other" }, "policy-search-service"},
		{"wrong implementation", func(s *config.Telemetry) { s.Identity.Implementation = "java" }, "policy-search-service"},
		{"invalid instance", func(s *config.Telemetry) { s.Identity.ServiceInstance = "private-marker\n" }, "policy-search-service"},
		{"zero exporter timeout", func(s *config.Telemetry) { s.ExporterTimeout = 0 }, "policy-search-service"},
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
			if strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("configuration value disclosed: %v", err)
			}
		})
	}
}

func enabledIdentity() identity.Identity {
	return identity.Identity{
		ServiceNamespace: "insurance-hub",
		ServiceName:      "policy-search-service",
		ServiceVersion:   "test-revision",
		ServiceInstance:  "123e4567-e89b-42d3-a456-426614174000",
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
