package postgres

import (
	"context"
	"errors"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
)

func TestDatabaseObserver_RecordsOnlyBoundedFailureAndSafeQueryMetadata(t *testing.T) {
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(context.Background())
		_ = traceProvider.Shutdown(context.Background())
	})
	meter := meterProvider.Meter("test/postgres")
	operations, err := meter.Int64Counter("insurance_hub.product.database.operations")
	if err != nil {
		t.Fatal(err)
	}
	queryTime, err := meter.Float64Histogram("db.client.operation.duration")
	if err != nil {
		t.Fatal(err)
	}
	observer := &Observer{
		tracer:     traceProvider.Tracer("test/postgres"),
		operations: operations,
		queryTime:  queryTime,
	}
	secret := "private-code-and-sql-argument"
	parentCtx, parent := traceProvider.Tracer("test").Start(context.Background(), "parent")
	wantErr := errors.Join(application.ErrUnavailable, errors.New(secret))
	gotErr := observer.stage(parentCtx, "get", "query", "SELECT product", trace.SpanKindClient,
		func(context.Context) error { return wantErr })
	parent.End()
	if !errors.Is(gotErr, application.ErrUnavailable) {
		t.Fatalf("error changed: %v", gotErr)
	}
	spans := spanRecorder.Ended()
	if len(spans) != 2 || spans[0].Name() != "SELECT product" ||
		spans[0].Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Fatalf("query trace ancestry: %#v", spans)
	}
	for _, attr := range spans[0].Attributes() {
		if attr.Value.AsString() == secret || attr.Key == "db.statement" ||
			attr.Key == "db.query.text" {
			t.Fatalf("unsafe query attribute: %v", attr)
		}
	}
	var collected metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	var outcome string
	for _, scope := range collected.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name != "insurance_hub.product.database.operations" {
				continue
			}
			for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
				for _, attr := range point.Attributes.ToSlice() {
					if string(
						attr.Key,
					) == "insurance_hub.product.database.outcome" {
						outcome = attr.Value.AsString()
					}
				}
			}
		}
	}
	if outcome != "storage_unavailable" {
		t.Fatalf("database outcome = %q", outcome)
	}
}

func TestDatabaseOutcome_ClassifiesContextAndInvalidDefinitions(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{nil, "success"},
		{context.Canceled, "cancelled"},
		{context.DeadlineExceeded, "deadline_exceeded"},
		{application.ErrInvalidDefinition, "invalid_data"},
		{application.ErrUnavailable, "storage_unavailable"},
	} {
		if got := databaseOutcome(test.err); got != test.want {
			t.Errorf("outcome(%v) = %s, want %s", test.err, got, test.want)
		}
	}
	if err := (*Observer)(nil).Close(); err != nil {
		t.Fatalf("nil observer close: %v", err)
	}
	if _, err := NewObserver(nil, nil, nil); err == nil {
		t.Error("observer accepted nil dependencies")
	}
	var nilObserver *Observer
	if err := nilObserver.stage(
		context.Background(),
		"get",
		"query",
		"SELECT product",
		trace.SpanKindClient,
		func(context.Context) error { return application.ErrUnavailable },
	); !errors.Is(
		err,
		application.ErrUnavailable,
	) {
		t.Fatalf("unobserved read changed error: %v", err)
	}
	nilObserver.decode(context.Background(), "get", nil)
}
