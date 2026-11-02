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

// EventObserver records bounded event outcomes without event bodies or policy identities.
type EventObserver struct {
	logger   *slog.Logger
	tracer   trace.Tracer
	events   metric.Int64Counter
	duration metric.Float64Histogram
}

func NewEventObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
) (*EventObserver, error) {
	if logger == nil || meterProvider == nil || tracerProvider == nil {
		return nil, errors.New(
			"create event observer: logger, meter provider and tracer provider are required",
		)
	}
	meter := meterProvider.Meter("insurance-hub/service")
	events, err := meter.Int64Counter(
		"insurance_hub.policy_search.events",
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, errors.New("create event counter")
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.policy_search.event.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create event duration")
	}
	return &EventObserver{
		logger:   logger,
		tracer:   tracerProvider.Tracer("insurance-hub/service"),
		events:   events,
		duration: duration,
	}, nil
}

type EventObservation struct {
	observer  *EventObserver
	started   time.Time
	span      trace.Span
	eventType string
}

func (observer *EventObserver) Start(
	ctx context.Context,
	eventType string,
) (context.Context, *EventObservation) {
	ctx, span := observer.tracer.Start(
		ctx,
		"Kafka policy event",
		trace.WithSpanKind(trace.SpanKindConsumer),
	)
	return ctx, &EventObservation{
		observer: observer, started: time.Now(), span: span, eventType: eventType,
	}
}

func (observation *EventObservation) Finish(ctx context.Context, outcome string, err error) {
	attrs := []attribute.KeyValue{
		attribute.String("insurance_hub.policy_search.event.type", observation.eventType),
		attribute.String("insurance_hub.policy_search.outcome", outcome),
	}
	if err != nil {
		observation.span.SetStatus(codes.Error, "")
		observation.span.SetAttributes(attribute.String("error.type", outcome))
	}
	observation.span.SetAttributes(attrs...)
	observation.span.End()
	elapsed := time.Since(observation.started)
	observation.observer.events.Add(ctx, 1, metric.WithAttributes(attrs...))
	observation.observer.duration.Record(
		ctx,
		elapsed.Seconds(),
		metric.WithAttributes(attrs...),
	)
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
	}
	observation.observer.logger.LogAttrs(
		ctx,
		level,
		"policy event completed",
		slog.String("event_type", observation.eventType),
		slog.String("outcome", outcome),
		slog.Float64("duration_ms", float64(elapsed)/float64(time.Millisecond)),
	)
}
