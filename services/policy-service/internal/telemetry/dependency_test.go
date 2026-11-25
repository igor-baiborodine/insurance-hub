package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

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

func TestDependencyObserver_ObserveCall_RecordsBoundedOutcomes(t *testing.T) {
	// given
	const marker = "private-provider-marker"
	reader := sdkmetric.NewManualReader()
	observer, err := NewDependencyObserver(sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithView(metricViews()...),
	))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	elapsed := 40 * time.Millisecond

	// when
	observer.ObserveCall(ctx, "product", "get_product", elapsed, "success")
	observer.ObserveCall(ctx, "product", "get_product", elapsed, "invalid_request")
	observer.ObserveCall(ctx, "pricing", "calculate_price", elapsed, "error")
	observer.ObserveCall(ctx, "pricing", "calculate_price", elapsed, "deadline_exceeded")
	observer.ObserveCall(ctx, "pricing", "calculate_price", elapsed, "cancelled")
	observer.ObserveCall(ctx, marker, marker, elapsed, marker)

	// then
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &metrics); err != nil {
		t.Fatal(err)
	}
	var calls []string
	var bound float64
	for _, scope := range metrics.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			switch recorded.Name {
			case "insurance_hub.policy.dependencies":
				for _, point := range recorded.Data.(metricdata.Sum[int64]).DataPoints {
					var labels []string
					for _, attr := range point.Attributes.ToSlice() {
						labels = append(
							labels,
							string(attr.Key)+"="+attr.Value.AsString(),
						)
					}
					calls = append(calls, strings.Join(labels, ","))
				}
			case "insurance_hub.policy.dependency.duration":
				point := recorded.Data.(metricdata.Histogram[float64]).DataPoints[0]
				for i, count := range point.BucketCounts {
					if count > 0 && i < len(point.Bounds) {
						bound = point.Bounds[i]
					}
				}
			}
		}
	}
	sort.Strings(calls)
	want := []string{
		"insurance_hub.policy.dependency=pricing,insurance_hub.policy.operation=calculate_price," +
			"insurance_hub.policy.outcome=cancelled",
		"insurance_hub.policy.dependency=pricing,insurance_hub.policy.operation=calculate_price," +
			"insurance_hub.policy.outcome=deadline_exceeded",
		"insurance_hub.policy.dependency=pricing,insurance_hub.policy.operation=calculate_price," +
			"insurance_hub.policy.outcome=error",
		"insurance_hub.policy.dependency=product,insurance_hub.policy.operation=get_product," +
			"insurance_hub.policy.outcome=invalid_request",
		"insurance_hub.policy.dependency=product,insurance_hub.policy.operation=get_product," +
			"insurance_hub.policy.outcome=success",
		"insurance_hub.policy.dependency=unknown,insurance_hub.policy.operation=unknown," +
			"insurance_hub.policy.outcome=unknown",
	}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Errorf("dependency calls =\n%v\nwant\n%v", calls, want)
	}
	if bound != 0.05 {
		t.Errorf("40ms dependency bucket upper bound = %v, want 0.05", bound)
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
