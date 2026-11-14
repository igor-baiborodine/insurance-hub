package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const registrationEventType = "registered"

// EventObserver records bounded generation outcomes and worker state without event values.
type EventObserver struct {
	logger       *slog.Logger
	tracer       trace.Tracer
	events       metric.Int64Counter
	duration     metric.Float64Histogram
	commitErrors metric.Int64Counter
	registration metric.Registration
	now          func() time.Time
	worker       eventWorkerState
}

type eventWorkerState struct {
	enabled bool
	started atomic.Int64
	failed  atomic.Bool
}

// NewEventObserver constructs the Document registration signal owner.
func NewEventObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
	workerEnabled bool,
) (*EventObserver, error) {
	return newEventObserver(logger, meterProvider, tracerProvider, workerEnabled, time.Now)
}

func newEventObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
	workerEnabled bool,
	now func() time.Time,
) (*EventObserver, error) {
	if logger == nil || meterProvider == nil || tracerProvider == nil || now == nil {
		return nil, errors.New(
			"create event observer: logger, providers and clock are required",
		)
	}
	meter := meterProvider.Meter("insurance-hub/service")
	events, err := meter.Int64Counter(
		"insurance_hub.document.events",
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, errors.New("create event counter")
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.document.event.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create event duration")
	}
	commitErrors, err := meter.Int64Counter(
		"insurance_hub.document.event.commit.failures",
		metric.WithUnit("{failure}"),
	)
	if err != nil {
		return nil, errors.New("create event commit failure counter")
	}
	enabled, err := meter.Int64ObservableGauge(
		"insurance_hub.document.event.worker.enabled",
		metric.WithUnit("{worker}"),
	)
	if err != nil {
		return nil, errors.New("create event worker enabled gauge")
	}
	inFlightAge, err := meter.Float64ObservableGauge(
		"insurance_hub.document.event.worker.in_flight_age",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create event worker in-flight age gauge")
	}
	failed, err := meter.Int64ObservableGauge(
		"insurance_hub.document.event.worker.failed",
		metric.WithUnit("{worker}"),
	)
	if err != nil {
		return nil, errors.New("create event worker failed gauge")
	}
	observer := &EventObserver{
		logger: logger, tracer: tracerProvider.Tracer("insurance-hub/service"),
		events: events, duration: duration, commitErrors: commitErrors, now: now,
		worker: eventWorkerState{enabled: workerEnabled},
	}
	registration, err := meter.RegisterCallback(
		func(ctx context.Context, measurements metric.Observer) error {
			attrs := metric.WithAttributes(eventTypeAttribute(registrationEventType))
			enabledValue := int64(0)
			if observer.worker.enabled {
				enabledValue = 1
			}
			measurements.ObserveInt64(enabled, enabledValue, attrs)
			age := 0.0
			if started := observer.worker.started.Load(); started > 0 {
				age = observer.now().Sub(time.Unix(0, started)).Seconds()
				if age < 0 {
					age = 0
				}
			}
			measurements.ObserveFloat64(inFlightAge, age, attrs)
			failedValue := int64(0)
			if observer.worker.failed.Load() {
				failedValue = 1
			}
			measurements.ObserveInt64(failed, failedValue, attrs)
			return nil
		},
		enabled,
		inFlightAge,
		failed,
	)
	if err != nil {
		return nil, errors.New("register event worker gauges")
	}
	observer.registration = registration
	return observer, nil
}

// EventObservation owns one bounded registration observation.
type EventObservation struct {
	observer  *EventObserver
	started   time.Time
	span      trace.Span
	eventType string
}

// Start begins one consumer span and marks the worker in flight.
func (observer *EventObserver) Start(
	ctx context.Context,
	eventType string,
) (context.Context, *EventObservation) {
	eventType = boundedEventType(eventType)
	if eventType == registrationEventType && observer.worker.enabled {
		observer.worker.started.Store(observer.now().UnixNano())
	}
	ctx, span := observer.tracer.Start(
		ctx,
		"Kafka policy registration",
		trace.WithSpanKind(trace.SpanKindConsumer),
	)
	return ctx, &EventObservation{
		observer: observer, started: observer.now(), span: span, eventType: eventType,
	}
}

// Finish records one safe terminal outcome and updates the failed/idle worker state.
func (observation *EventObservation) Finish(ctx context.Context, outcome string, err error) {
	if observation.eventType == registrationEventType {
		defer observation.observer.worker.started.Store(0)
	}
	outcome = boundedEventOutcome(outcome)
	switch outcome {
	case "success":
		observation.observer.worker.failed.Store(false)
	case "error":
		observation.observer.worker.failed.Store(true)
	}
	attrs := []attribute.KeyValue{
		eventTypeAttribute(observation.eventType),
		attribute.String("insurance_hub.document.outcome", outcome),
	}
	if err != nil {
		observation.span.SetStatus(codes.Error, "")
		observation.span.SetAttributes(attribute.String("error.type", outcome))
	}
	observation.span.SetAttributes(attrs...)
	observation.span.End()
	elapsed := observation.observer.now().Sub(observation.started)
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
		"policy registration completed",
		slog.String("event_type", observation.eventType),
		slog.String("outcome", outcome),
		slog.Float64("duration_ms", float64(elapsed)/float64(time.Millisecond)),
	)
}

// RecordCommitFailure records a fixed commit failure without broker or payload details.
func (observer *EventObserver) RecordCommitFailure(ctx context.Context, eventType string) {
	eventType = boundedEventType(eventType)
	observer.commitErrors.Add(
		ctx,
		1,
		metric.WithAttributes(eventTypeAttribute(eventType)),
	)
	observer.logger.ErrorContext(
		ctx,
		"policy event offset commit failed",
		slog.String("event_type", eventType),
	)
}

func eventTypeAttribute(eventType string) attribute.KeyValue {
	return attribute.String("insurance_hub.document.event.type", eventType)
}

func boundedEventType(eventType string) string {
	if eventType == registrationEventType {
		return eventType
	}
	return "unknown"
}

func boundedEventOutcome(outcome string) string {
	switch outcome {
	case "success", "malformed", "error", "cancelled":
		return outcome
	default:
		return "error"
	}
}
