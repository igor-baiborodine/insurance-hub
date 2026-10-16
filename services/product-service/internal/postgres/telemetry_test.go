package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
)

func TestDatabaseObserver_CollectsPoolStateAndSuccessfulReadSignals(t *testing.T) {
	// given
	config, err := pgxpool.ParseConfig("postgres://product@127.0.0.1:1/product")
	if err != nil {
		t.Fatal(err)
	}
	config.MinConns = 0
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	spanRecorder := tracetest.NewSpanRecorder()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(context.Background())
		_ = traceProvider.Shutdown(context.Background())
	})
	observer, err := NewObserver(pool, traceProvider, meterProvider)
	if err != nil {
		t.Fatalf("create observer: %v", err)
	}
	t.Cleanup(func() { _ = observer.Close() })

	// when
	stageErr := observer.stage(context.Background(), "list", "query", "SELECT products",
		trace.SpanKindClient, func(context.Context) error { return nil })
	observer.decode(context.Background(), "list", nil)
	observer.decode(context.Background(), "get", application.ErrInvalidDefinition)

	var collected metricdata.ResourceMetrics
	collectErr := metricReader.Collect(context.Background(), &collected)

	// then
	if stageErr != nil {
		t.Fatalf("successful query: %v", stageErr)
	}
	if collectErr != nil {
		t.Fatalf("collect database metrics: %v", collectErr)
	}
	measurements := map[string]metricdata.Metrics{}
	for _, scope := range collected.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			measurements[measurement.Name] = measurement
		}
	}
	counts := measurements["db.client.connection.count"].Data.(metricdata.Sum[int64]).DataPoints
	if len(counts) != 2 {
		t.Fatalf("pool state points = %v", counts)
	}
	for _, point := range counts {
		state, ok := point.Attributes.Value(attribute.Key("db.client.connection.state"))
		if !ok || (state.AsString() != "idle" && state.AsString() != "used") ||
			point.Value != 0 {
			t.Errorf("unexpected pool state: %v", point)
		}
		name, ok := point.Attributes.Value(attribute.Key("db.client.connection.pool.name"))
		if !ok || name.AsString() != "product-reader" {
			t.Errorf("unexpected pool name: %v", point)
		}
	}
	maximum := measurements["db.client.connection.max"].Data.(metricdata.Sum[int64]).DataPoints
	if len(maximum) != 1 || maximum[0].Value != 2 {
		t.Errorf("pool maximum = %v, want 2", maximum)
	}
	operations := measurements["insurance_hub.product.database.operations"].Data.(metricdata.Sum[int64]).DataPoints
	if len(operations) != 3 {
		t.Fatalf("database operation points = %v", operations)
	}
	want := map[string]bool{
		"list/query/success":      false,
		"list/decode/success":     false,
		"get/decode/invalid_data": false,
	}
	for _, point := range operations {
		operation, _ := point.Attributes.Value(
			attribute.Key("insurance_hub.product.operation"),
		)
		stage, _ := point.Attributes.Value(
			attribute.Key("insurance_hub.product.database.stage"),
		)
		outcome, _ := point.Attributes.Value(
			attribute.Key("insurance_hub.product.database.outcome"),
		)
		key := operation.AsString() + "/" + stage.AsString() + "/" + outcome.AsString()
		if _, ok := want[key]; !ok || point.Value != 1 {
			t.Errorf("unexpected database operation %s: %v", key, point)
		}
		want[key] = true
	}
	for key, observed := range want {
		if !observed {
			t.Errorf("missing database operation %s", key)
		}
	}
	query := measurements["db.client.operation.duration"].Data.(metricdata.Histogram[float64]).DataPoints
	if len(query) != 1 || query[0].Count != 1 {
		t.Errorf("query duration points = %v", query)
	}
	spans := spanRecorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "SELECT products" || spans[0].Status().Code != 0 {
		t.Errorf("successful query spans = %v", spans)
	}

	// when
	closeErr := observer.Close()
	var afterClose metricdata.ResourceMetrics
	afterCloseErr := metricReader.Collect(context.Background(), &afterClose)

	// then
	if closeErr != nil {
		t.Fatalf("close observer: %v", closeErr)
	}
	if afterCloseErr != nil {
		t.Fatalf("collect after close: %v", afterCloseErr)
	}
	for _, scope := range afterClose.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name == "db.client.connection.count" ||
				measurement.Name == "db.client.connection.max" {
				t.Errorf(
					"pool metric continued after observer closed: %s",
					measurement.Name,
				)
			}
		}
	}
}

func TestDatabaseObserver_RecordsOnlyBoundedFailureAndSafeQueryMetadata(t *testing.T) {
	// given
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

	// when
	gotErr := observer.stage(parentCtx, "get", "query", "SELECT product", trace.SpanKindClient,
		func(context.Context) error { return wantErr })
	parent.End()
	var collected metricdata.ResourceMetrics
	collectErr := metricReader.Collect(context.Background(), &collected)

	// then
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
	if collectErr != nil {
		t.Fatal(collectErr)
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
	// given
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
		// when
		got := databaseOutcome(test.err)

		// then
		if got != test.want {
			t.Errorf("outcome(%v) = %s, want %s", test.err, got, test.want)
		}
	}
}

func TestDatabaseObserver_NilCloseIsSafe(t *testing.T) {
	// given
	var nilObserver *Observer

	// when
	err := nilObserver.Close()
	// then
	if err != nil {
		t.Fatalf("nil observer close: %v", err)
	}
}

func TestDatabaseObserver_RejectsMissingDependencies(t *testing.T) {
	// given
	var pool *pgxpool.Pool

	// when
	_, err := NewObserver(pool, nil, nil)

	// then
	if err == nil {
		t.Error("observer accepted nil dependencies")
	}
}

func TestDatabaseObserver_NilStagePreservesReadError(t *testing.T) {
	// given
	var nilObserver *Observer
	ctx := context.Background()
	work := func(context.Context) error { return application.ErrUnavailable }

	// when
	err := nilObserver.stage(ctx, "get", "query", "SELECT product", trace.SpanKindClient, work)

	// then
	if !errors.Is(err, application.ErrUnavailable) {
		t.Fatalf("unobserved read changed error: %v", err)
	}
}

func TestDatabaseObserver_NilDecodeDoesNotPanic(t *testing.T) {
	var nilObserver *Observer
	nilObserver.decode(context.Background(), "get", nil)
}
