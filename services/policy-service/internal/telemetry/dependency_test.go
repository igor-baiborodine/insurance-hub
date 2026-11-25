package telemetry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestDependencyObserver_RecordsLatestBoundedReadiness(t *testing.T) {
	// given
	const marker = "private-dependency-marker"
	reader := sdkmetric.NewManualReader()
	observer, err := NewDependencyObserver(
		sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// when
	observer.Record(ctx, "database", nil)
	observer.Record(ctx, "kafka", nil)
	observer.Record(ctx, "database", errors.New(marker))
	observer.Record(ctx, marker, nil)

	// then
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &metrics); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, scope := range metrics.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != "insurance_hub.policy.dependency.ready" {
				continue
			}
			for _, point := range recorded.Data.(metricdata.Gauge[int64]).DataPoints {
				dependency, _ := point.Attributes.Value(
					"insurance_hub.policy.dependency",
				)
				got[dependency.AsString()] = point.Value
			}
		}
	}
	want := map[string]int64{"database": 0, "kafka": 1, "unknown": 1}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("dependency readiness = %v, want %v", got, want)
	}
	if strings.Contains(fmt.Sprint(metrics), marker) {
		t.Errorf("private marker escaped into metrics: %v", metrics)
	}
}

func TestNewDependencyObserver_RequiresMeterProvider(t *testing.T) {
	if observer, err := NewDependencyObserver(nil); observer != nil || err == nil {
		t.Fatalf("NewDependencyObserver() = (%v, %v)", observer, err)
	}
}
