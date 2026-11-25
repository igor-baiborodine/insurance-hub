package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestEventObserver_UsesOnlyBoundedProducerDimensions(t *testing.T) {
	// given
	clock := time.Unix(0, 0)
	observer, err := newEventObserver(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		metricnoop.NewMeterProvider(),
		tracenoop.NewTracerProvider(),
		func() time.Time {
			clock = clock.Add(time.Millisecond)
			return clock
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// when
	ctx, observation := observer.Start(context.Background(), "private-policy-marker")
	observation.Finish(ctx, "private-outcome-marker", context.Canceled)

	// then
	if observation.eventType != "unknown" ||
		boundedPolicyEventOutcome("private-outcome-marker") != "error" {
		t.Fatalf("unbounded observation = %q", observation.eventType)
	}
}

func TestEventObserver_StartsErrorSeriesAtZero(t *testing.T) {
	// given
	reader := sdkmetric.NewManualReader()
	observer, err := NewEventObserver(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		tracenoop.NewTracerProvider(),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	errorCounts := func() map[string]int64 {
		var metrics metricdata.ResourceMetrics
		if err := reader.Collect(ctx, &metrics); err != nil {
			t.Fatal(err)
		}
		got := map[string]int64{}
		for _, scope := range metrics.ScopeMetrics {
			for _, recorded := range scope.Metrics {
				if recorded.Name != "insurance_hub.policy.event.publications" {
					continue
				}
				for _, point := range recorded.Data.(metricdata.Sum[int64]).DataPoints {
					outcome, _ := point.Attributes.Value(
						"insurance_hub.policy.event.outcome",
					)
					eventType, _ := point.Attributes.Value(
						"insurance_hub.policy.event.type",
					)
					if outcome.AsString() == "error" {
						got[eventType.AsString()] = point.Value
					}
				}
			}
		}
		return got
	}

	// when
	baseline := errorCounts()
	_, observation := observer.Start(ctx, "registered")
	observation.Finish(ctx, "error", context.DeadlineExceeded)
	afterFailure := errorCounts()

	// then
	if got, want := fmt.Sprint(baseline), "map[registered:0 terminated:0]"; got != want {
		t.Errorf("baseline error series = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(afterFailure), "map[registered:1 terminated:0]"; got != want {
		t.Errorf("error series after one failure = %s, want %s", got, want)
	}
}

func TestEventObserver_RecordsFastPublicationInSubsecondBucket(t *testing.T) {
	// given
	reader := sdkmetric.NewManualReader()
	clock := time.Unix(0, 0)
	observer, err := newEventObserver(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(reader),
			sdkmetric.WithView(metricViews()...),
		),
		tracenoop.NewTracerProvider(),
		func() time.Time {
			clock = clock.Add(40 * time.Millisecond)
			return clock
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// when
	_, observation := observer.Start(ctx, "registered")
	observation.Finish(ctx, "success", nil)

	// then
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &metrics); err != nil {
		t.Fatal(err)
	}
	var bound float64
	for _, scope := range metrics.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != "insurance_hub.policy.event.publication.duration" {
				continue
			}
			for _, point := range recorded.Data.(metricdata.Histogram[float64]).DataPoints {
				for i, count := range point.BucketCounts {
					if count == 1 && i < len(point.Bounds) {
						bound = point.Bounds[i]
					}
				}
			}
		}
	}
	if bound != 0.05 {
		t.Errorf("40ms publication bucket upper bound = %v, want 0.05", bound)
	}
}

func TestNewEventObserver_RequiresDependencies(t *testing.T) {
	if observer, err := NewEventObserver(nil, nil, nil); observer != nil || err == nil {
		t.Fatalf("NewEventObserver() = (%v, %v)", observer, err)
	}
}
