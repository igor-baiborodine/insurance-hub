//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	integrationtest "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/testing"
)

func TestPostgresReader(t *testing.T) {
	// given
	image := os.Getenv("PRODUCT_TEST_POSTGRES_IMAGE")
	if image != integrationtest.DefaultPostgresImage {
		t.Fatalf("PRODUCT_TEST_POSTGRES_IMAGE does not contain the pinned image")
	}
	if selection := os.Getenv(
		"PRODUCT_INTEGRATION_FIXTURE_SET",
	); selection != string(
		integrationtest.FixtureQA,
	) {
		t.Fatalf("fixture selection = %q, want qa", selection)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	baselineRoot, err := integrationtest.ResolveBaselineRoot(workingDirectory)
	if err != nil {
		t.Fatalf("resolve baseline root: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	harness, err := integrationtest.Start(ctx, integrationtest.Options{
		FixtureRoot: baselineRoot,
		Image:       image,
	})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}
	t.Cleanup(func() { _ = harness.Close() })
	pool, err := openPool(
		ctx,
		harness.RuntimeDatabaseURL(),
		1,
		3*time.Second,
	)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	poolClosed := false
	t.Cleanup(func() {
		if !poolClosed {
			pool.Close()
		}
	})
	reader, err := NewReader(pool, 2*time.Second, 5*time.Second)
	if err != nil {
		t.Fatalf("create reader: %v", err)
	}
	if reader.pool.Config().MaxConns != 1 || reader.pool.Config().MinConns != 0 {
		t.Fatalf(
			"pool bounds = %d/%d, want 1/0",
			reader.pool.Config().MaxConns,
			reader.pool.Config().MinConns,
		)
	}

	t.Run("reads the complete QA catalog through the restricted role", func(t *testing.T) {
		// given
		fixture, loadErr := harness.LoadCatalog(ctx, integrationtest.FixtureQA)
		if loadErr != nil {
			t.Fatalf("load QA catalog: %v", loadErr)
		}

		// when
		products, listErr := reader.ListProducts(ctx)
		product, found, getErr := reader.GetProduct(ctx, "CAR")

		// then
		if listErr != nil || getErr != nil || !found || product.Code != "CAR" {
			t.Fatalf(
				"QA reads: count=%d product=%+v found=%t list-error=%v get-error=%v",
				len(products),
				product,
				found,
				listErr,
				getErr,
			)
		}
		codes := make([]string, len(products))
		for index := range products {
			codes[index] = products[index].Code
		}
		sort.Strings(codes)
		if !reflect.DeepEqual(codes, fixture.DataIdentity.Codes) {
			t.Errorf("codes = %v, want %v", codes, fixture.DataIdentity.Codes)
		}
		assertNoAcquiredConnections(t, reader)
	})

	t.Run("returns empty single and missing results", func(t *testing.T) {
		// given
		if resetErr := harness.ResetRows(ctx, nil); resetErr != nil {
			t.Fatalf("reset empty: %v", resetErr)
		}

		// when
		empty, emptyErr := reader.ListProducts(ctx)
		missing, found, missingErr := reader.GetProduct(ctx, "CAR")

		// then
		if emptyErr != nil || empty == nil || len(empty) != 0 {
			t.Errorf("empty list = %v, error = %v", empty, emptyErr)
		}
		if missingErr != nil || found || missing.Code != "" {
			t.Errorf(
				"missing product = %+v, found=%t, error=%v",
				missing,
				found,
				missingErr,
			)
		}

		// given
		if resetErr := harness.ResetRows(ctx, []integrationtest.FixtureRow{
			{
				Code:                      "ONLY",
				RawLosslessDefinitionJSON: `{"covers":[{"sumInsured":12345678901234567890.2300}]}`,
			},
		}); resetErr != nil {
			t.Fatalf("reset single: %v", resetErr)
		}

		// when
		single, singleErr := reader.ListProducts(ctx)

		// then
		if singleErr != nil || len(single) != 1 || single[0].Code != "ONLY" ||
			len(single[0].Covers) != 1 || single[0].Covers[0].SumInsured == nil ||
			single[0].Covers[0].SumInsured.String() != "12345678901234567890.2300" {
			t.Errorf("single list = %+v, error=%v", single, singleErr)
		}
		assertNoAcquiredConnections(t, reader)
	})

	t.Run("preserves exact and parameterized lookup codes", func(t *testing.T) {
		// given
		rows := []integrationtest.FixtureRow{
			{Code: "Case", RawLosslessDefinitionJSON: `{}`},
			{Code: " spaced ", RawLosslessDefinitionJSON: `{}`},
			{Code: "CAR' OR '1'='1", RawLosslessDefinitionJSON: `{}`},
		}
		if resetErr := harness.ResetRows(ctx, rows); resetErr != nil {
			t.Fatalf("reset lookup rows: %v", resetErr)
		}

		for _, row := range rows {
			// when
			product, found, getErr := reader.GetProduct(ctx, row.Code)

			// then
			if getErr != nil || !found || product.Code != row.Code {
				t.Errorf(
					"exact lookup %q: product=%+v found=%t error=%v",
					row.Code,
					product,
					found,
					getErr,
				)
			}
		}
		for _, code := range []string{"case", "spaced", "CAR"} {
			if _, found, getErr := reader.GetProduct(
				ctx,
				code,
			); getErr != nil ||
				found {
				t.Errorf(
					"non-exact lookup %q: found=%t error=%v",
					code,
					found,
					getErr,
				)
			}
		}
		assertNoAcquiredConnections(t, reader)
	})

	t.Run("rejects a corrupt row without returning a partial list", func(t *testing.T) {
		// given
		if resetErr := harness.ResetRows(ctx, []integrationtest.FixtureRow{
			{Code: "GOOD", RawLosslessDefinitionJSON: `{}`},
			{
				Code:                      "CORRUPT",
				RawLosslessDefinitionJSON: `{"covers":[{"unexpected":true}]}`,
			},
		}); resetErr != nil {
			t.Fatalf("reset corrupt rows: %v", resetErr)
		}

		// when
		products, listErr := reader.ListProducts(ctx)
		product, found, getErr := reader.GetProduct(ctx, "CORRUPT")

		// then
		if products != nil || !errors.Is(listErr, application.ErrInvalidDefinition) {
			t.Errorf("corrupt list = %+v, error=%v", products, listErr)
		}
		if product.Code != "" || found ||
			!errors.Is(getErr, application.ErrInvalidDefinition) {
			t.Errorf("corrupt get = %+v, found=%t, error=%v", product, found, getErr)
		}
		assertNoAcquiredConnections(t, reader)
	})

	t.Run("preserves cancellation and releases the pool", func(t *testing.T) {
		// given
		canceledCtx, cancelCall := context.WithCancel(context.Background())
		cancelCall()

		// when
		products, listErr := reader.ListProducts(canceledCtx)

		// then
		if products != nil || !errors.Is(listErr, context.Canceled) ||
			errors.Is(listErr, application.ErrUnavailable) {
			t.Errorf("canceled list = %+v, error=%v", products, listErr)
		}
		assertNoAcquiredConnections(t, reader)
	})

	t.Run("bounds connection acquisition", func(t *testing.T) {
		// given
		heldConnection, acquireErr := reader.pool.Acquire(ctx)
		if acquireErr != nil {
			t.Fatalf("hold only connection: %v", acquireErr)
		}
		originalTimeout := reader.acquireTimeout
		reader.acquireTimeout = 25 * time.Millisecond

		// when
		products, listErr := reader.ListProducts(ctx)

		// then
		reader.acquireTimeout = originalTimeout
		heldConnection.Release()
		if products != nil || !errors.Is(listErr, context.DeadlineExceeded) ||
			errors.Is(listErr, application.ErrUnavailable) {
			t.Errorf("bounded acquisition list = %+v, error=%v", products, listErr)
		}
		assertNoAcquiredConnections(t, reader)
	})

	t.Run("real reads emit bounded database and pool signals", func(t *testing.T) {
		if _, loadErr := harness.LoadCatalog(
			ctx,
			integrationtest.FixtureQA,
		); loadErr != nil {
			t.Fatalf("load catalog: %v", loadErr)
		}
		metricReader := sdkmetric.NewManualReader()
		meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
		spanRecorder := tracetest.NewSpanRecorder()
		traceProvider := sdktrace.NewTracerProvider(
			sdktrace.WithSpanProcessor(spanRecorder),
		)
		observer, observerErr := NewObserver(pool, traceProvider, meterProvider)
		if observerErr != nil {
			t.Fatalf("new observer: %v", observerErr)
		}
		t.Cleanup(func() {
			_ = observer.Close()
			_ = meterProvider.Shutdown(context.Background())
			_ = traceProvider.Shutdown(context.Background())
		})
		observedReader, readerErr := NewReader(pool, time.Second, time.Second, observer)
		if readerErr != nil {
			t.Fatalf("observed reader: %v", readerErr)
		}
		readCtx, parent := traceProvider.Tracer("test").Start(ctx, "read")
		if _, listErr := observedReader.ListProducts(readCtx); listErr != nil {
			t.Fatalf("list: %v", listErr)
		}
		if _, found, getErr := observedReader.GetProduct(
			readCtx,
			"CAR",
		); getErr != nil ||
			!found {
			t.Fatalf("get: found=%t error=%v", found, getErr)
		}
		if _, found, getErr := observedReader.GetProduct(
			readCtx,
			"MISSING",
		); getErr != nil ||
			found {
			t.Fatalf("missing: found=%t error=%v", found, getErr)
		}
		parent.End()
		spans := spanRecorder.Ended()
		if len(spans) != 7 {
			t.Fatalf("ended spans=%d, want parent plus six database spans", len(spans))
		}
		for _, span := range spans {
			if span.Name() == "read" {
				continue
			}
			if span.Parent().SpanID() != parent.SpanContext().SpanID() {
				t.Fatalf("span %q has unexpected parent", span.Name())
			}
			for _, attr := range span.Attributes() {
				if attr.Key == "db.statement" || attr.Key == "db.query.text" {
					t.Fatalf("span %q includes SQL text", span.Name())
				}
			}
		}
		if resetErr := harness.ResetRows(ctx, []integrationtest.FixtureRow{
			{
				Code:                      "CORRUPT",
				RawLosslessDefinitionJSON: `{"covers":[{"unexpected":true}]}`,
			},
		}); resetErr != nil {
			t.Fatalf("reset corrupt row: %v", resetErr)
		}
		if _, _, corruptErr := observedReader.GetProduct(
			ctx,
			"CORRUPT",
		); !errors.Is(
			corruptErr,
			application.ErrInvalidDefinition,
		) {
			t.Fatalf("corrupt read: %v", corruptErr)
		}
		canceledCtx, cancelRead := context.WithCancel(ctx)
		cancelRead()
		if _, canceledErr := observedReader.ListProducts(
			canceledCtx,
		); !errors.Is(
			canceledErr,
			context.Canceled,
		) {
			t.Fatalf("canceled read: %v", canceledErr)
		}
		held, acquireErr := pool.Acquire(ctx)
		if acquireErr != nil {
			t.Fatalf("hold connection: %v", acquireErr)
		}
		var pressured metricdata.ResourceMetrics
		if collectErr := metricReader.Collect(ctx, &pressured); collectErr != nil {
			t.Fatalf("collect pool pressure: %v", collectErr)
		}
		used := int64(-1)
		maximum := int64(-1)
		for _, scope := range pressured.ScopeMetrics {
			for _, measurement := range scope.Metrics {
				switch measurement.Name {
				case "db.client.connection.count":
					for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
						for _, attr := range point.Attributes.ToSlice() {
							if string(
								attr.Key,
							) == "db.client.connection.state" &&
								attr.Value.AsString() == "used" {
								used = point.Value
							}
						}
					}
				case "db.client.connection.max":
					maximum = measurement.Data.(metricdata.Sum[int64]).DataPoints[0].Value
				}
			}
		}
		if used != 1 || maximum != 1 {
			t.Fatalf("pool pressure used/max=%d/%d, want 1/1", used, maximum)
		}
		observedReader.acquireTimeout = 20 * time.Millisecond
		_, timeoutErr := observedReader.ListProducts(ctx)
		held.Release()
		if !errors.Is(timeoutErr, context.DeadlineExceeded) {
			t.Fatalf("pool timeout: %v", timeoutErr)
		}
		closedPool, openErr := openPool(ctx, harness.RuntimeDatabaseURL(), 1, time.Second)
		if openErr != nil {
			t.Fatalf("open second reader pool: %v", openErr)
		}
		closedReader, closedReaderErr := NewReader(
			closedPool,
			time.Second,
			time.Second,
			observer,
		)
		if closedReaderErr != nil {
			t.Fatalf("create second reader: %v", closedReaderErr)
		}
		closedPool.Close()
		if _, unavailableErr := closedReader.ListProducts(
			ctx,
		); !errors.Is(
			unavailableErr,
			application.ErrUnavailable,
		) {
			t.Fatalf("closed pool read: %v", unavailableErr)
		}
		var result metricdata.ResourceMetrics
		if collectErr := metricReader.Collect(ctx, &result); collectErr != nil {
			t.Fatalf("collect metrics: %v", collectErr)
		}
		metrics := map[string]metricdata.Metrics{}
		for _, scope := range result.ScopeMetrics {
			for _, measurement := range scope.Metrics {
				metrics[measurement.Name] = measurement
			}
		}
		for _, name := range []string{
			"insurance_hub.product.database.operations", "db.client.operation.duration",
			"db.client.connection.count", "db.client.connection.max",
			"db.client.connection.timeouts", "db.client.connection.wait_time",
		} {
			if _, exists := metrics[name]; !exists {
				t.Errorf("missing metric %q", name)
			}
		}
		if sum, ok := metrics["db.client.connection.timeouts"].Data.(metricdata.Sum[int64]); !ok ||
			len(sum.DataPoints) != 1 ||
			sum.DataPoints[0].Value != 1 {
			t.Errorf(
				"pool timeout counter = %#v",
				metrics["db.client.connection.timeouts"],
			)
		}
		if histogram, ok := metrics["db.client.operation.duration"].Data.(metricdata.Histogram[float64]); !ok ||
			len(histogram.DataPoints) != 2 ||
			histogram.DataPoints[0].Count+histogram.DataPoints[1].Count != 4 {
			t.Errorf("query histogram = %#v", metrics["db.client.operation.duration"])
		}
		outcomes := map[string]int64{}
		var getQuerySuccess int64
		if operations, ok := metrics["insurance_hub.product.database.operations"].Data.(metricdata.Sum[int64]); ok {
			for _, point := range operations.DataPoints {
				attrs := map[string]string{}
				for _, attr := range point.Attributes.ToSlice() {
					attrs[string(attr.Key)] = attr.Value.AsString()
					if string(
						attr.Key,
					) == "insurance_hub.product.database.outcome" {
						outcomes[attr.Value.AsString()] += point.Value
					}
				}
				if attrs["insurance_hub.product.operation"] == "get" &&
					attrs["insurance_hub.product.database.stage"] == "query" &&
					attrs["insurance_hub.product.database.outcome"] == "success" {
					getQuerySuccess += point.Value
				}
			}
		}
		if getQuerySuccess != 3 || outcomes["not_found"] != 0 {
			t.Errorf(
				"missing row database classification: get-query-success=%d outcomes=%v",
				getQuerySuccess,
				outcomes,
			)
		}
		for _, outcome := range []string{"success", "invalid_data", "cancelled", "deadline_exceeded", "storage_unavailable"} {
			if outcomes[outcome] == 0 {
				t.Errorf("no %s database outcome: %v", outcome, outcomes)
			}
		}
	})

	// when
	pool.Close()
	poolClosed = true
	products, listErr := reader.ListProducts(ctx)

	// then
	if products != nil || !errors.Is(listErr, application.ErrUnavailable) {
		t.Errorf("closed-pool list = %+v, error=%v", products, listErr)
	}
}

func assertNoAcquiredConnections(t *testing.T, reader *Reader) {
	t.Helper()
	if acquired := reader.pool.Stat().AcquiredConns(); acquired != 0 {
		t.Errorf("acquired connections = %d, want 0", acquired)
	}
}
