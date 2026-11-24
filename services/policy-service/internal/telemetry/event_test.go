package telemetry

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
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

func TestNewEventObserver_RequiresDependencies(t *testing.T) {
	if observer, err := NewEventObserver(nil, nil, nil); observer != nil || err == nil {
		t.Fatalf("NewEventObserver() = (%v, %v)", observer, err)
	}
}
