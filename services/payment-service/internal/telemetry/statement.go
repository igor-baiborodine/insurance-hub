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

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/importer"
)

// StatementObserver records bounded importer outcomes without statement or account data.
type StatementObserver struct {
	logger       *slog.Logger
	tracer       trace.Tracer
	imports      metric.Int64Counter
	duration     metric.Float64Histogram
	registration metric.Registration
	now          func() time.Time
	enabled      bool
	started      atomic.Int64
}

// NewStatementObserver constructs the Payment statement-import signals.
func NewStatementObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
	workerEnabled bool,
) (*StatementObserver, error) {
	return newStatementObserver(
		logger,
		meterProvider,
		tracerProvider,
		workerEnabled,
		time.Now,
	)
}

func newStatementObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
	workerEnabled bool,
	now func() time.Time,
) (*StatementObserver, error) {
	if logger == nil || meterProvider == nil || tracerProvider == nil || now == nil {
		return nil, errors.New(
			"create statement observer: logger, providers and clock are required",
		)
	}
	meter := meterProvider.Meter("insurance-hub/service")
	imports, err := meter.Int64Counter(
		"insurance_hub.payment.statement.imports",
		metric.WithUnit("{tick}"),
	)
	if err != nil {
		return nil, errors.New("create statement import counter")
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.payment.statement.import.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create statement import duration")
	}
	enabled, err := meter.Int64ObservableGauge(
		"insurance_hub.payment.statement.worker.enabled",
		metric.WithUnit("{worker}"),
	)
	if err != nil {
		return nil, errors.New("create statement worker enabled gauge")
	}
	inFlightAge, err := meter.Float64ObservableGauge(
		"insurance_hub.payment.statement.worker.in_flight_age",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create statement worker in-flight age gauge")
	}
	observer := &StatementObserver{
		logger: logger, tracer: tracerProvider.Tracer("insurance-hub/service"),
		imports: imports, duration: duration, now: now, enabled: workerEnabled,
	}
	registration, err := meter.RegisterCallback(
		func(ctx context.Context, measurements metric.Observer) error {
			enabledValue := int64(0)
			if observer.enabled {
				enabledValue = 1
			}
			measurements.ObserveInt64(enabled, enabledValue)
			age := 0.0
			if started := observer.started.Load(); started > 0 {
				age = observer.now().Sub(time.Unix(0, started)).Seconds()
				if age < 0 {
					age = 0
				}
			}
			measurements.ObserveFloat64(inFlightAge, age)
			return nil
		},
		enabled,
		inFlightAge,
	)
	if err != nil {
		return nil, errors.New("register statement worker gauges")
	}
	observer.registration = registration
	return observer, nil
}

// Start begins one import tick and returns its bounded completion callback.
func (observer *StatementObserver) Start(
	ctx context.Context,
) (context.Context, func(context.Context, importer.Outcome, error)) {
	if observer.enabled {
		observer.started.Store(observer.now().UnixNano())
	}
	started := observer.now()
	ctx, span := observer.tracer.Start(ctx, "bank statement import")
	return ctx, func(finishCtx context.Context, outcome importer.Outcome, err error) {
		defer observer.started.Store(0)
		outcome = boundedStatementOutcome(outcome)
		attributes := []attribute.KeyValue{
			attribute.String(
				"insurance_hub.payment.statement.outcome",
				string(outcome),
			),
		}
		if err != nil {
			span.SetStatus(codes.Error, "")
			span.SetAttributes(attribute.String("error.type", string(outcome)))
		}
		span.SetAttributes(attributes...)
		span.End()
		elapsed := observer.now().Sub(started)
		options := metric.WithAttributes(attributes...)
		observer.imports.Add(finishCtx, 1, options)
		observer.duration.Record(finishCtx, elapsed.Seconds(), options)
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelWarn
		}
		observer.logger.LogAttrs(
			finishCtx,
			level,
			"bank statement import completed",
			slog.String("outcome", string(outcome)),
			slog.Float64("duration_ms", float64(elapsed)/float64(time.Millisecond)),
		)
	}
}

func boundedStatementOutcome(outcome importer.Outcome) importer.Outcome {
	switch outcome {
	case importer.OutcomeIdle,
		importer.OutcomeArchived,
		importer.OutcomeReview,
		importer.OutcomePostedAwaitingArchive,
		importer.OutcomeDependencyFailure:
		return outcome
	default:
		return importer.OutcomeDependencyFailure
	}
}
