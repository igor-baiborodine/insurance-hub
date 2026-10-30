package telemetry

import (
	"context"
	"sync"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
)

func TestProvider_ShutdownExportsRuntimeInstrumentsBeforeUnregister(t *testing.T) {
	// given
	exporter := &shutdownMetricExporter{}
	provider, err := newProvider(
		context.Background(), testTelemetrySettings(time.Second), testOptions(),
		func(context.Context, config.Telemetry) (sdktrace.SpanExporter, error) {
			return newRecordingExporter(), nil
		},
		func(context.Context, config.Telemetry) (sdkmetric.Reader, error) {
			return sdkmetric.NewPeriodicReader(
				exporter,
				sdkmetric.WithInterval(time.Hour),
			), nil
		},
		registerRuntime,
	)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	// when
	shutdownErr := provider.Shutdown(context.Background())
	// then
	if shutdownErr != nil {
		t.Fatalf("shutdown provider: %v", shutdownErr)
	}
	if exporter.exports != 1 {
		t.Fatalf("metric exports = %d, want one final export", exporter.exports)
	}
	// go.memory.limit is omitted when Go reports no configured memory limit.
	for _, name := range []string{
		"go.memory.used", "go.memory.allocated",
		"go.memory.allocations", "go.memory.gc.goal", "go.memory.gc.cycles",
		"go.goroutine.count", "go.processor.limit",
	} {
		if !exporter.has(name) {
			t.Errorf("final export lacks runtime instrument %q", name)
		}
	}
}

type shutdownMetricExporter struct {
	mu      sync.Mutex
	exports int
	names   map[string]bool
}

func (exporter *shutdownMetricExporter) Temporality(
	kind sdkmetric.InstrumentKind,
) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(kind)
}

func (exporter *shutdownMetricExporter) Aggregation(
	kind sdkmetric.InstrumentKind,
) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (exporter *shutdownMetricExporter) Export(
	_ context.Context,
	data *metricdata.ResourceMetrics,
) error {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	exporter.exports++
	if exporter.names == nil {
		exporter.names = map[string]bool{}
	}
	for _, scope := range data.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			exporter.names[measurement.Name] = true
		}
	}
	return nil
}

func (*shutdownMetricExporter) ForceFlush(context.Context) error { return nil }
func (*shutdownMetricExporter) Shutdown(context.Context) error   { return nil }

func (exporter *shutdownMetricExporter) has(name string) bool {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	return exporter.names[name]
}
