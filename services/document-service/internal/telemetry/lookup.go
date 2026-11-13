package telemetry

import (
	"context"
	"errors"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	servicelogger "github.com/igor-baiborodine/insurance-hub/services/document-service/internal/logger"
)

// LookupObserver records bounded Document lookup facts without identifiers or object keys.
type LookupObserver struct {
	logger  *slog.Logger
	lookups metric.Int64Counter
}

func NewLookupObserver(
	logger *slog.Logger,
	provider metric.MeterProvider,
) (*LookupObserver, error) {
	if logger == nil || provider == nil {
		return nil, errors.New(
			"create lookup observer: logger and meter provider are required",
		)
	}
	lookups, err := provider.Meter("insurance-hub/service").Int64Counter(
		"insurance_hub.document.lookup.results",
		metric.WithUnit("{lookup}"),
	)
	if err != nil {
		return nil, errors.New("create lookup result counter")
	}
	return &LookupObserver{logger: logger, lookups: lookups}, nil
}

// Record reports completion, partial and failure outcomes with bounded attributes.
func (observer *LookupObserver) Record(
	ctx context.Context,
	returned int,
	omitted int,
	err error,
) {
	outcome := "complete"
	if omitted > 0 {
		outcome = "partial"
	}
	switch {
	case errors.Is(err, context.Canceled):
		outcome = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		outcome = "deadline_exceeded"
	case errors.Is(err, application.ErrResponseLimit):
		outcome = "limit_exceeded"
	case errors.Is(err, application.ErrUnavailable):
		outcome = "dependency_unavailable"
	case err != nil:
		outcome = "internal_error"
	}
	attributes := []attribute.KeyValue{
		attribute.String("insurance_hub.document.operation", OperationFindDocuments),
		attribute.String("insurance_hub.document.lookup.outcome", outcome),
	}
	observer.lookups.Add(ctx, 1, metric.WithAttributes(attributes...))
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attributes...)
	if err != nil {
		span.SetAttributes(attribute.String("error.type", outcome))
		span.SetStatus(codes.Error, "")
	}
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
	}
	servicelogger.LogCompletion(
		ctx,
		observer.logger,
		level,
		slog.String("operation", OperationFindDocuments),
		slog.String("outcome", outcome),
		slog.Int("documents_returned", returned),
		slog.Int("documents_omitted", omitted),
	)
}
