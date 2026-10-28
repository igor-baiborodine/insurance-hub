package telemetry

import (
	"context"
	"math"
	"runtime/metrics"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRegisterRuntime_CollectsCurrentProcessIncludingGCCycles(t *testing.T) {
	// given
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	registration, err := registerRuntime(provider)
	if err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = registration.Unregister()
		_ = provider.Shutdown(context.Background())
	})
	// when
	var result metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &result); err != nil {
		t.Fatalf("collect runtime metrics: %v", err)
	}
	// then
	got := map[string]metricdata.Metrics{}
	for _, scope := range result.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			got[measurement.Name] = measurement
		}
	}
	for _, name := range []string{
		"go.memory.used", "go.memory.allocated",
		"go.memory.allocations", "go.memory.gc.goal", "go.memory.gc.cycles",
		"go.goroutine.count", "go.processor.limit",
	} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing runtime instrument %q", name)
		}
	}
	limitSample := []metrics.Sample{{Name: "/gc/gomemlimit:bytes"}}
	metrics.Read(limitSample)
	_, hasLimit := got["go.memory.limit"]
	if hasLimit != (limitSample[0].Value.Uint64() != math.MaxInt64) {
		t.Errorf("memory limit presence does not match configured Go limit")
	}
	if got["go.memory.gc.cycles"].Unit != "{gc_cycle}" ||
		got["go.processor.limit"].Unit != "{thread}" {
		t.Errorf("runtime convention units: cycles=%q processors=%q",
			got["go.memory.gc.cycles"].Unit, got["go.processor.limit"].Unit)
	}
	if used, ok := got["go.memory.used"].Data.(metricdata.Sum[int64]); !ok ||
		len(
			used.DataPoints,
		) != 2 || used.DataPoints[0].Value < 0 || used.DataPoints[1].Value < 0 {
		t.Errorf("stack/other memory values = %#v", got["go.memory.used"])
	}
	if cycles, ok := got["go.memory.gc.cycles"].Data.(metricdata.Sum[int64]); !ok ||
		len(cycles.DataPoints) != 1 || !cycles.IsMonotonic {
		t.Errorf("GC cycle counter = %#v", got["go.memory.gc.cycles"])
	}
}
