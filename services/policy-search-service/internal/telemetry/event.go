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

// EventObserver records bounded event outcomes without event bodies or policy identities.
type EventObserver struct {
	logger   *slog.Logger
	tracer   trace.Tracer
	events   metric.Int64Counter
	duration metric.Float64Histogram
	now      func() time.Time
	workers  map[string]*eventWorkerState
}

type eventWorkerState struct {
	enabled bool
	started atomic.Int64
}

var eventTypes = [...]string{"registered", "terminated"}

func NewEventObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
	workersEnabled bool,
) (*EventObserver, error) {
	return newEventObserver(
		logger, meterProvider, tracerProvider, workersEnabled, time.Now,
	)
}

func newEventObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
	workersEnabled bool,
	now func() time.Time,
) (*EventObserver, error) {
	if logger == nil || meterProvider == nil || tracerProvider == nil || now == nil {
		return nil, errors.New(
			"create event observer: logger, providers and clock are required",
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
	enabled, err := meter.Int64ObservableGauge(
		"insurance_hub.policy_search.event.worker.enabled",
		metric.WithUnit("{worker}"),
	)
	if err != nil {
		return nil, errors.New("create event worker enabled gauge")
	}
	inFlightAge, err := meter.Float64ObservableGauge(
		"insurance_hub.policy_search.event.worker.in_flight_age",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create event worker in-flight age gauge")
	}
	observer := &EventObserver{
		logger:   logger,
		tracer:   tracerProvider.Tracer("insurance-hub/service"),
		events:   events,
		duration: duration,
		now:      now,
		workers:  make(map[string]*eventWorkerState, len(eventTypes)),
	}
	for _, eventType := range eventTypes {
		observer.workers[eventType] = &eventWorkerState{enabled: workersEnabled}
	}
	_, err = meter.RegisterCallback(
		func(ctx context.Context, measurements metric.Observer) error {
			for _, eventType := range eventTypes {
				state := observer.workers[eventType]
				attributes := metric.WithAttributes(attribute.String(
					"insurance_hub.policy_search.event.type",
					eventType,
				))
				enabledValue := int64(0)
				if state.enabled {
					enabledValue = 1
				}
				measurements.ObserveInt64(enabled, enabledValue, attributes)
				age := 0.0
				if started := state.started.Load(); started > 0 {
					age = observer.now().Sub(time.Unix(0, started)).Seconds()
					if age < 0 {
						age = 0
					}
				}
				measurements.ObserveFloat64(inFlightAge, age, attributes)
			}
			return nil
		},
		enabled,
		inFlightAge,
	)
	if err != nil {
		return nil, errors.New("register event worker gauges")
	}
	return observer, nil
}

type EventObservation struct {
	observer  *EventObserver
	started   time.Time
	span      trace.Span
	eventType string
	worker    *eventWorkerState
}

func (observer *EventObserver) Start(
	ctx context.Context,
	eventType string,
) (context.Context, *EventObservation) {
	worker := observer.workers[eventType]
	if worker != nil && worker.enabled {
		worker.started.Store(observer.now().UnixNano())
	}
	ctx, span := observer.tracer.Start(
		ctx,
		"Kafka policy event",
		trace.WithSpanKind(trace.SpanKindConsumer),
	)
	return ctx, &EventObservation{
		observer: observer, started: observer.now(), span: span, eventType: eventType,
		worker: worker,
	}
}

func (observation *EventObservation) Finish(ctx context.Context, outcome string, err error) {
	if observation.worker != nil {
		defer observation.worker.started.Store(0)
	}
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
