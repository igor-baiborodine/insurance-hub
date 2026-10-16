package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
)

var poolAttributes = metric.WithAttributes(
	attribute.String("db.client.connection.pool.name", "product-reader"),
)

// Observer measures only Product reads. The service owns its callback registration.
type Observer struct {
	tracer       trace.Tracer
	operations   metric.Int64Counter
	queryTime    metric.Float64Histogram
	waitTime     metric.Float64Histogram
	timeouts     metric.Int64Counter
	registration metric.Registration
}

func NewObserver(
	pool *pgxpool.Pool,
	tracerProvider trace.TracerProvider,
	meterProvider metric.MeterProvider,
) (*Observer, error) {
	if pool == nil || tracerProvider == nil || meterProvider == nil {
		return nil, errors.New("create PostgreSQL observer: dependencies are required")
	}
	meter := meterProvider.Meter("insurance-hub/product/postgres")
	operations, err := meter.Int64Counter(
		"insurance_hub.product.database.operations",
		metric.WithUnit("{operation}"),
	)
	if err != nil {
		return nil, err
	}
	queryTime, err := meter.Float64Histogram(
		"db.client.operation.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	waitTime, err := meter.Float64Histogram(
		"db.client.connection.wait_time",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	timeouts, err := meter.Int64Counter(
		"db.client.connection.timeouts",
		metric.WithUnit("{timeout}"),
	)
	if err != nil {
		return nil, err
	}
	count, err := meter.Int64ObservableUpDownCounter(
		"db.client.connection.count",
		metric.WithUnit("{connection}"),
	)
	if err != nil {
		return nil, err
	}
	maxCount, err := meter.Int64ObservableUpDownCounter(
		"db.client.connection.max",
		metric.WithUnit("{connection}"),
	)
	if err != nil {
		return nil, err
	}
	registration, err := meter.RegisterCallback(
		func(_ context.Context, observer metric.Observer) error {
			stats := pool.Stat()
			observer.ObserveInt64(
				count,
				int64(stats.IdleConns()),
				metric.WithAttributes(
					attribute.String(
						"db.client.connection.pool.name",
						"product-reader",
					),
					attribute.String("db.client.connection.state", "idle"),
				),
			)
			observer.ObserveInt64(
				count,
				int64(stats.AcquiredConns()),
				metric.WithAttributes(
					attribute.String(
						"db.client.connection.pool.name",
						"product-reader",
					),
					attribute.String("db.client.connection.state", "used"),
				),
			)
			observer.ObserveInt64(maxCount, int64(stats.MaxConns()), poolAttributes)
			return nil
		},
		count,
		maxCount,
	)
	if err != nil {
		return nil, err
	}
	return &Observer{
		tracer:     tracerProvider.Tracer("insurance-hub/product/postgres"),
		operations: operations, queryTime: queryTime, waitTime: waitTime,
		timeouts: timeouts, registration: registration,
	}, nil
}

func (observer *Observer) Close() error {
	if observer == nil {
		return nil
	}
	return observer.registration.Unregister()
}

func (observer *Observer) stage(
	ctx context.Context,
	operation, stage, name string,
	kind trace.SpanKind,
	work func(context.Context) error,
) error {
	if observer == nil {
		return work(ctx)
	}
	attrs := []attribute.KeyValue{
		attribute.String("insurance_hub.product.operation", operation),
		attribute.String("db.system.name", "postgresql"),
		attribute.String("db.namespace", "product"),
		attribute.String("db.collection.name", "public.product"),
		attribute.String("db.operation.name", "SELECT"),
		attribute.String("insurance_hub.product.database.stage", stage),
	}
	if stage == "query" {
		summary := "list products"
		if operation == "get" {
			summary = "get product"
		}
		attrs = append(attrs, attribute.String("db.query.summary", summary))
	}
	ctx, span := observer.tracer.Start(
		ctx,
		name,
		trace.WithSpanKind(kind),
		trace.WithAttributes(attrs...),
	)
	started := time.Now()
	err := work(ctx)
	outcome := databaseOutcome(err)
	observer.operations.Add(ctx, 1, metric.WithAttributes(
		attribute.String("insurance_hub.product.operation", operation),
		attribute.String("insurance_hub.product.database.stage", stage),
		attribute.String("insurance_hub.product.database.outcome", outcome),
	))
	if stage == "query" {
		histogramAttrs := append([]attribute.KeyValue{}, attrs[:5]...)
		if err != nil {
			histogramAttrs = append(
				histogramAttrs,
				attribute.String("error.type", outcome),
			)
		}
		observer.queryTime.Record(
			ctx,
			time.Since(started).Seconds(),
			metric.WithAttributes(histogramAttrs...),
		)
	}
	if err != nil {
		span.SetAttributes(attribute.String("error.type", outcome))
		span.SetStatus(codes.Error, "")
	}
	span.End()
	return err
}

func (observer *Observer) decode(ctx context.Context, operation string, err error) {
	if observer == nil {
		return
	}
	observer.operations.Add(ctx, 1, metric.WithAttributes(
		attribute.String("insurance_hub.product.operation", operation),
		attribute.String("insurance_hub.product.database.stage", "decode"),
		attribute.String("insurance_hub.product.database.outcome", databaseOutcome(err)),
	))
}

func databaseOutcome(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, application.ErrInvalidDefinition):
		return "invalid_data"
	default:
		return "storage_unavailable"
	}
}
