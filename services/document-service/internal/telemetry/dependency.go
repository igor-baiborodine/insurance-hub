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

const (
	OperationGenerateDocument   = "generate_document"
	DependencyPostgreSQL        = "postgresql"
	DependencyObjectStore       = "object_store"
	DependencyJSReport          = "jsreport"
	DependencyResultSuccess     = "success"
	DependencyResultFetchFailed = "fetch_failed"
)

// DependencyObserver records bounded dependency signals without request data.
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
		"insurance_hub.document.dependencies",
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, errors.New("create dependency counter")
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.document.dependency.duration",
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
	observer   *DependencyObserver
	started    time.Time
	span       trace.Span
	operation  string
	dependency string
	stage      string
}

func (observer *DependencyObserver) Start(
	ctx context.Context,
	operation string,
	dependency string,
) (context.Context, *DependencyRequest) {
	operation = boundedDependencyOperation(operation)
	dependency = boundedDependency(dependency)
	stage := boundedDependencyStage(operation, dependency)
	ctx, span := observer.tracer.Start(
		ctx,
		dependency+" "+operation,
		trace.WithSpanKind(trace.SpanKindClient),
	)
	return ctx, &DependencyRequest{
		observer: observer, started: time.Now(), span: span, operation: operation,
		dependency: dependency, stage: stage,
	}
}

func boundedDependencyOperation(operation string) string {
	switch operation {
	case OperationFindDocuments, OperationGenerateDocument:
		return operation
	default:
		return "unknown"
	}
}

func boundedDependency(dependency string) string {
	switch dependency {
	case DependencyPostgreSQL, DependencyObjectStore, DependencyJSReport:
		return dependency
	default:
		return "unknown"
	}
}

func boundedDependencyStage(operation, dependency string) string {
	if operation == OperationFindDocuments {
		switch dependency {
		case DependencyPostgreSQL, DependencyObjectStore:
			return "lookup"
		}
	}
	if operation == OperationGenerateDocument {
		switch dependency {
		case DependencyJSReport:
			return "render"
		case DependencyObjectStore:
			return "upload"
		case DependencyPostgreSQL:
			return "metadata"
		}
	}
	return "unknown"
}

func (request *DependencyRequest) Finish(ctx context.Context, result string, err error) {
	outcome, level := DependencyResultSuccess, slog.LevelInfo
	if result == DependencyResultFetchFailed {
		outcome, level = DependencyResultFetchFailed, slog.LevelWarn
	}
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
		attribute.String("insurance_hub.document.dependency", request.dependency),
		attribute.String("insurance_hub.document.operation", request.operation),
		attribute.String("insurance_hub.document.stage", request.stage),
		attribute.String("insurance_hub.document.outcome", outcome),
	}
	request.span.SetAttributes(attrs...)
	if err != nil || outcome == DependencyResultFetchFailed {
		request.span.SetAttributes(attribute.String("error.type", outcome))
		request.span.SetStatus(codes.Error, "")
	}
	request.span.End()
	request.observer.requests.Add(ctx, 1, metric.WithAttributes(attrs...))
	request.observer.duration.Record(ctx, elapsed.Seconds(), metric.WithAttributes(attrs...))
	request.observer.logger.LogAttrs(
		ctx, level, "dependency completed",
		slog.String("dependency", request.dependency),
		slog.String("operation", request.operation),
		slog.String("stage", request.stage),
		slog.String("outcome", outcome),
		slog.Float64("duration_ms", float64(elapsed)/float64(time.Millisecond)),
	)
}
