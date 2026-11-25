package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// EventObserver records bounded producer outcomes without keys, payloads, broker addresses or
// customer data.
type EventObserver struct {
	logger   *slog.Logger
	tracer   trace.Tracer
	events   metric.Int64Counter
	duration metric.Float64Histogram
	now      func() time.Time
}

// NewEventObserver constructs the Policy producer signal owner.
func NewEventObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
) (*EventObserver, error) {
	return newEventObserver(logger, meterProvider, tracerProvider, time.Now)
}

func newEventObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
	now func() time.Time,
) (*EventObserver, error) {
	if logger == nil || meterProvider == nil || tracerProvider == nil || now == nil {
		return nil, errors.New("create Policy event observer: dependencies are required")
	}
	meter := meterProvider.Meter("insurance-hub/service")
	events, err := meter.Int64Counter(
		"insurance_hub.policy.events",
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, errors.New("create Policy event publication counter")
	}
	// A zero baseline lets an increase over a window detect the first failed publication.
	for _, eventType := range []string{"registered", "terminated"} {
		events.Add(context.Background(), 0, metric.WithAttributes(
			attribute.String("insurance_hub.policy.event.type", eventType),
			attribute.String("insurance_hub.policy.outcome", "error"),
		))
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.policy.event.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create Policy event publication duration")
	}
	return &EventObserver{
		logger: logger, tracer: tracerProvider.Tracer("insurance-hub/service"),
		events: events, duration: duration, now: now,
	}, nil
}

// EventObservation owns one producer span and its bounded metric dimensions.
type EventObservation struct {
	observer  *EventObserver
	started   time.Time
	span      trace.Span
	eventType string
}

// Start begins one producer observation using only the fixed event type.
func (observer *EventObserver) Start(
	ctx context.Context,
	eventType string,
) (context.Context, *EventObservation) {
	eventType = boundedPolicyEventType(eventType)
	ctx, span := observer.tracer.Start(
		ctx,
		"Kafka policy event publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attribute.String("messaging.operation.type", "send")),
	)
	return ctx, &EventObservation{
		observer: observer, started: observer.now(), span: span, eventType: eventType,
	}
}

// Finish records one sanitized producer outcome.
func (observation *EventObservation) Finish(
	ctx context.Context,
	outcome string,
	err error,
) {
	outcome = boundedPolicyEventOutcome(outcome)
	attrs := []attribute.KeyValue{
		attribute.String("insurance_hub.policy.event.type", observation.eventType),
		attribute.String("insurance_hub.policy.outcome", outcome),
	}
	if err != nil {
		observation.span.SetStatus(codes.Error, "")
		observation.span.SetAttributes(attribute.String("error.type", outcome))
	}
	observation.span.SetAttributes(attrs...)
	observation.span.End()
	elapsed := observation.observer.now().Sub(observation.started)
	options := metric.WithAttributes(attrs...)
	observation.observer.events.Add(ctx, 1, options)
	observation.observer.duration.Record(ctx, elapsed.Seconds(), options)
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
	}
	observation.observer.logger.LogAttrs(
		ctx,
		level,
		"policy event publication completed",
		slog.String("event_type", observation.eventType),
		slog.String("outcome", outcome),
		slog.Float64("duration_ms", float64(elapsed)/float64(time.Millisecond)),
	)
}

func boundedPolicyEventType(eventType string) string {
	switch eventType {
	case "registered", "terminated":
		return eventType
	default:
		return "unknown"
	}
}

func boundedPolicyEventOutcome(outcome string) string {
	switch outcome {
	case "success", "error", "cancelled":
		return outcome
	default:
		return "error"
	}
}
