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

// EventObserver records bounded registration outcomes and worker state without event values.
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
}

// NewEventObserver constructs the Payment event signals.
func NewEventObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
	workerEnabled bool,
) (*EventObserver, error) {
	return newEventObserver(
		logger,
		meterProvider,
		tracerProvider,
		workerEnabled,
		time.Now,
	)
}

func newEventObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
	workerEnabled bool,
	now func() time.Time,
) (*EventObserver, error) {
	if logger == nil || meterProvider == nil || tracerProvider == nil || now == nil {
		return nil, errors.New("create event observer: logger, providers and clock are required")
	}
	meter := meterProvider.Meter("insurance-hub/service")
	events, err := meter.Int64Counter(
		"insurance_hub.payment.events",
		metric.WithUnit("{event}"),
	)
	if err != nil {
		return nil, errors.New("create event counter")
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.payment.event.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create event duration")
	}
	commitErrors, err := meter.Int64Counter(
		"insurance_hub.payment.event.commit.failures",
		metric.WithUnit("{failure}"),
	)
	if err != nil {
		return nil, errors.New("create event commit failure counter")
	}
	enabled, err := meter.Int64ObservableGauge(
		"insurance_hub.payment.event.worker.enabled",
		metric.WithUnit("{worker}"),
	)
	if err != nil {
		return nil, errors.New("create event worker enabled gauge")
	}
	inFlightAge, err := meter.Float64ObservableGauge(
		"insurance_hub.payment.event.worker.in_flight_age",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create event worker in-flight age gauge")
	}
	observer := &EventObserver{
		logger:       logger,
		tracer:       tracerProvider.Tracer("insurance-hub/service"),
		events:       events,
		duration:     duration,
		commitErrors: commitErrors,
		now:          now,
		worker:       eventWorkerState{enabled: workerEnabled},
	}
	registration, err := meter.RegisterCallback(
		func(ctx context.Context, measurements metric.Observer) error {
			attrs := metric.WithAttributes(eventTypeAttribute())
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
			return nil
		},
		enabled,
		inFlightAge,
	)
	if err != nil {
		return nil, errors.New("register event worker gauges")
	}
	observer.registration = registration
	return observer, nil
}

// Start begins one registration observation and returns its bounded completion callback.
func (observer *EventObserver) Start(
	ctx context.Context,
) (context.Context, func(context.Context, string, error)) {
	if observer.worker.enabled {
		observer.worker.started.Store(observer.now().UnixNano())
	}
	started := observer.now()
	ctx, span := observer.tracer.Start(
		ctx,
		"Kafka policy registration",
		trace.WithSpanKind(trace.SpanKindConsumer),
	)
	return ctx, func(finishCtx context.Context, outcome string, err error) {
		defer observer.worker.started.Store(0)
		outcome = boundedEventOutcome(outcome)
		attributes := []attribute.KeyValue{
			eventTypeAttribute(),
			attribute.String("insurance_hub.payment.outcome", outcome),
		}
		if err != nil {
			span.SetStatus(codes.Error, "")
			span.SetAttributes(attribute.String("error.type", outcome))
		}
		span.SetAttributes(attributes...)
		span.End()
		elapsed := observer.now().Sub(started)
		options := metric.WithAttributes(attributes...)
		observer.events.Add(finishCtx, 1, options)
		observer.duration.Record(finishCtx, elapsed.Seconds(), options)
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelWarn
		}
		observer.logger.LogAttrs(
			finishCtx,
			level,
			"policy registration completed",
			slog.String("event_type", registrationEventType),
			slog.String("outcome", outcome),
			slog.Float64("duration_ms", float64(elapsed)/float64(time.Millisecond)),
		)
	}
}

// RecordCommitFailure records a fixed failure without broker or registration details.
func (observer *EventObserver) RecordCommitFailure(ctx context.Context) {
	attributes := metric.WithAttributes(eventTypeAttribute())
	observer.commitErrors.Add(ctx, 1, attributes)
	observer.logger.ErrorContext(
		ctx,
		"policy registration offset commit failed",
		slog.String("event_type", registrationEventType),
	)
}

func eventTypeAttribute() attribute.KeyValue {
	return attribute.String("insurance_hub.payment.event.type", registrationEventType)
}

func boundedEventOutcome(outcome string) string {
	switch outcome {
	case "success", "held", "commit_uncertain", "cancelled":
		return outcome
	default:
		return "held"
	}
}
