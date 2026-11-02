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

// DependencyObserver records bounded Elasticsearch signals without query or policy attributes.
type DependencyObserver struct {
	logger   *slog.Logger
	tracer   trace.Tracer
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

func NewDependencyObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
) (*DependencyObserver, error) {
	if logger == nil || meterProvider == nil || tracerProvider == nil {
		return nil, errors.New(
			"create dependency observer: logger, meter provider and tracer provider are required",
		)
	}
	meter := meterProvider.Meter("insurance-hub/service")
	requests, err := meter.Int64Counter(
		"insurance_hub.policy_search.dependencies",
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, errors.New("create dependency counter")
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.policy_search.dependency.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create dependency duration")
	}
	return &DependencyObserver{
		logger: logger, tracer: tracerProvider.Tracer("insurance-hub/service"),
		requests: requests, duration: duration,
	}, nil
}

type DependencyRequest struct {
	observer  *DependencyObserver
	started   time.Time
	span      trace.Span
	operation string
}

type ElasticsearchOperation string

const (
	ElasticsearchSearch  ElasticsearchOperation = "search"
	ElasticsearchReplace ElasticsearchOperation = "replace"
)

func (observer *DependencyObserver) StartElasticsearch(
	ctx context.Context,
	operation ElasticsearchOperation,
) (context.Context, *DependencyRequest) {
	ctx, span := observer.tracer.Start(
		ctx,
		"Elasticsearch "+string(operation),
		trace.WithSpanKind(trace.SpanKindClient),
	)
	return ctx, &DependencyRequest{
		observer: observer, started: time.Now(), span: span, operation: string(operation),
	}
}

func (request *DependencyRequest) Finish(ctx context.Context, err error) {
	outcome, level := "success", slog.LevelInfo
	switch {
	case errors.Is(err, context.Canceled):
		outcome, level = "cancelled", slog.LevelWarn
	case errors.Is(err, context.DeadlineExceeded):
		outcome, level = "deadline_exceeded", slog.LevelWarn
	case err != nil:
		outcome, level = "error", slog.LevelError
	}
	elapsed := time.Since(request.started)
	attrs := []attribute.KeyValue{
		attribute.String("insurance_hub.policy_search.dependency", "elasticsearch"),
		attribute.String("insurance_hub.policy_search.operation", request.operation),
		attribute.String("insurance_hub.policy_search.outcome", outcome),
	}
	request.span.SetAttributes(attrs...)
	if err != nil {
		request.span.SetAttributes(attribute.String("error.type", outcome))
		request.span.SetStatus(codes.Error, "")
	}
	request.span.End()
	request.observer.requests.Add(ctx, 1, metric.WithAttributes(attrs...))
	request.observer.duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(attrs...))
	request.observer.logger.LogAttrs(
		ctx,
		level,
		"dependency completed",
		slog.String("dependency", "elasticsearch"),
		slog.String("operation", request.operation),
		slog.String("outcome", outcome),
		slog.Float64("duration_ms", float64(elapsed)/float64(time.Millisecond)),
	)
}
