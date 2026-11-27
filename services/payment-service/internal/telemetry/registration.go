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

const registrationEventType = "registered"

// RegistrationObserver records bounded worker outcomes without registration contents.
type RegistrationObserver struct {
	logger        *slog.Logger
	tracer        trace.Tracer
	registrations metric.Int64Counter
	duration      metric.Float64Histogram
	commitErrors  metric.Int64Counter
	now           func() time.Time
}

// NewRegistrationObserver constructs the Payment registration signals.
func NewRegistrationObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
) (*RegistrationObserver, error) {
	if logger == nil || meterProvider == nil || tracerProvider == nil {
		return nil, errors.New("create registration observer: dependencies are required")
	}
	meter := meterProvider.Meter("insurance-hub/service")
	registrations, err := meter.Int64Counter(
		"insurance_hub.payment.registrations",
		metric.WithUnit("{registration}"),
	)
	if err != nil {
		return nil, errors.New("create registration counter")
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.payment.registration.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create registration duration")
	}
	commitErrors, err := meter.Int64Counter(
		"insurance_hub.payment.registration.commit.failures",
		metric.WithUnit("{failure}"),
	)
	if err != nil {
		return nil, errors.New("create registration commit failure counter")
	}
	return &RegistrationObserver{
		logger: logger, tracer: tracerProvider.Tracer("insurance-hub/service"),
		registrations: registrations, duration: duration, commitErrors: commitErrors,
		now: time.Now,
	}, nil
}

// Start begins one registration observation and returns its bounded completion callback.
func (observer *RegistrationObserver) Start(
	ctx context.Context,
) (context.Context, func(context.Context, string, error)) {
	started := observer.now()
	ctx, span := observer.tracer.Start(
		ctx,
		"Kafka policy registration",
		trace.WithSpanKind(trace.SpanKindConsumer),
	)
	return ctx, func(finishCtx context.Context, outcome string, err error) {
		outcome = boundedRegistrationOutcome(outcome)
		attributes := []attribute.KeyValue{
			attribute.String("insurance_hub.payment.event.type", registrationEventType),
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
		observer.registrations.Add(finishCtx, 1, options)
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
func (observer *RegistrationObserver) RecordCommitFailure(ctx context.Context) {
	attributes := metric.WithAttributes(
		attribute.String("insurance_hub.payment.event.type", registrationEventType),
	)
	observer.commitErrors.Add(ctx, 1, attributes)
	observer.logger.ErrorContext(
		ctx,
		"policy registration offset commit failed",
		slog.String("event_type", registrationEventType),
	)
}

func boundedRegistrationOutcome(outcome string) string {
	switch outcome {
	case "success", "held", "commit_uncertain", "cancelled":
		return outcome
	default:
		return "held"
	}
}
