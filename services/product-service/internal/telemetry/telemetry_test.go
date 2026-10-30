package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
)

func TestNew_DisabledDoesNotCreateExporter(t *testing.T) {
	// given
	settings := config.Telemetry{Enabled: false, ExporterTimeout: time.Second}
	// when
	provider, err := New(context.Background(), "product-service", settings)
	// then
	if err != nil {
		t.Fatalf("new disabled provider: %v", err)
	}
	_, span := provider.TracerProvider().Tracer("test").Start(context.Background(), "disabled")
	if span.IsRecording() || provider.MeterProvider() == nil {
		t.Error("disabled provider must use non-recording tracer and nonnil meter")
	}
	span.End()
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown disabled provider: %v", err)
	}
}

func TestNew_RejectsInvalidProductIdentityBeforeExport(t *testing.T) {
	// given
	settings := testTelemetrySettings(2 * time.Second)
	settings.Identity.ServiceName = "other-service"
	// when
	_, err := New(context.Background(), "product-service", settings)
	// then
	if err == nil || err.Error() != "initialize telemetry: service identity mismatch" {
		t.Errorf("identity mismatch = %v", err)
	}
	settings.Identity.ServiceName = "product-service"
	settings.Identity.ServiceVersion = "mutable"
	_, err = New(context.Background(), "product-service", settings)
	if err == nil || err.Error() != "initialize telemetry: invalid resource identity" {
		t.Errorf("invalid version = %v", err)
	}
}

func TestNew_ExportsProductRequestMetricWithOriginalBucketsAndIdentity(t *testing.T) {
	// given
	metricCollector := &metricCollector{
		exports: make(chan *collectormetric.ExportMetricsServiceRequest, 1),
	}
	collector := &disposableCollector{
		started: make(
			chan struct{},
			1,
		),
		accepted: make(chan struct{}, 1),
		metric:   metricCollector,
	}
	settings := testTelemetrySettings(2 * time.Second)
	settings.ExporterEndpoint = mustURL(startDisposableCollector(t, collector))
	provider, err := New(context.Background(), "product-service", settings)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	histogram, err := provider.MeterProvider().Meter("insurance-hub/product").Float64Histogram(
		"insurance_hub.product.request.duration", metric.WithUnit("s"),
	)
	if err != nil {
		t.Fatalf("create Product histogram: %v", err)
	}
	// when
	histogram.Record(context.Background(), 0.02)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := provider.ForceFlush(ctx); err != nil {
		t.Fatalf("flush Product metrics: %v", err)
	}
	// then
	select {
	case export := <-metricCollector.exports:
		for _, resource := range export.ResourceMetrics {
			attributes := map[string]string{}
			for _, attr := range resource.Resource.Attributes {
				attributes[attr.Key] = attr.Value.GetStringValue()
			}
			if attributes["service.namespace"] != "insurance-hub" ||
				attributes["service.name"] != "product-service" ||
				attributes["service.version"] != "test-telemetry" ||
				attributes["service.instance.id"] != "123e4567-e89b-42d3-a456-426614174000" ||
				attributes["deployment.environment.name"] != "test" ||
				attributes["insurance_hub.service.implementation"] != "go" {
				t.Errorf("Product metric identity = %#v", attributes)
			}
			for _, scope := range resource.ScopeMetrics {
				for _, measurement := range scope.Metrics {
					if measurement.Name != "insurance_hub.product.request.duration" {
						continue
					}
					points := measurement.GetHistogram().GetDataPoints()
					if len(points) != 1 || points[0].Count != 1 ||
						!reflect.DeepEqual(
							points[0].ExplicitBounds,
							[]float64{
								0.005,
								0.01,
								0.025,
								0.05,
								0.075,
								0.1,
								0.25,
								0.5,
								0.75,
								1,
								2.5,
								5,
								7.5,
								10,
							},
						) {
						t.Errorf(
							"Product request duration histogram = %#v",
							points,
						)
					}
					return
				}
			}
		}
		t.Error("Product request duration metric missing")
	case <-ctx.Done():
		t.Fatal("Product metric export timed out")
	}
}

func TestInstallDiagnostics_RateLimitsAndRedactsCollectorErrors(t *testing.T) {
	// given
	var output bytes.Buffer
	cleanup := InstallDiagnostics(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(cleanup)
	// when
	for range 100 {
		otel.GetErrorHandler().Handle(errors.New("private-collector-marker"))
	}
	// then
	if strings.Contains(output.String(), "private-collector-marker") {
		t.Fatal("collector error text leaked")
	}
	if got := strings.Count(output.String(), "telemetry export failed"); got != 1 {
		t.Errorf("diagnostic count = %d, want 1", got)
	}
}

func enabledProvider(t *testing.T) *Provider {
	t.Helper()
	collector := &disposableCollector{
		started: make(chan struct{}, 1), accepted: make(chan struct{}, 1),
		metric: &metricCollector{},
	}
	settings := testTelemetrySettings(2 * time.Second)
	settings.ExporterEndpoint = mustURL(startDisposableCollector(t, collector))
	provider, err := New(context.Background(), "product-service", settings)
	if err != nil {
		t.Fatalf("new enabled provider: %v", err)
	}
	return provider
}

func testTelemetrySettings(timeout time.Duration) config.Telemetry {
	return config.Telemetry{
		Enabled: true, ExporterEndpoint: mustURL("http://127.0.0.1:4317"),
		ExporterTimeout: timeout,
		Identity: config.Identity{
			ServiceNamespace: "insurance-hub", ServiceName: "product-service",
			ServiceVersion:  "test-telemetry",
			ServiceInstance: "123e4567-e89b-42d3-a456-426614174000",
			Environment:     "test", Implementation: "go",
		},
	}
}

func mustURL(value string) *url.URL {
	parsed, err := url.Parse(value)
	if err != nil {
		panic(err)
	}
	return parsed
}
