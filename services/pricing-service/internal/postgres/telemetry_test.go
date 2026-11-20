package postgres

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/application"
)

const privateMarker = "PRIVATE-MARKER-415"

type observedSignals struct {
	metricReader  *sdkmetric.ManualReader
	spanRecorder  *tracetest.SpanRecorder
	traceProvider *sdktrace.TracerProvider
	meterProvider *sdkmetric.MeterProvider
}

func newObservedSignals(t *testing.T) observedSignals {
	t.Helper()
	signals := observedSignals{
		metricReader: sdkmetric.NewManualReader(),
		spanRecorder: tracetest.NewSpanRecorder(),
	}
	signals.meterProvider = sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(signals.metricReader),
	)
	signals.traceProvider = sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(signals.spanRecorder),
	)
	t.Cleanup(func() {
		_ = signals.meterProvider.Shutdown(context.Background())
		_ = signals.traceProvider.Shutdown(context.Background())
	})
	return signals
}

func (signals observedSignals) collect(t *testing.T) map[string]metricdata.Metrics {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := signals.metricReader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	measurements := map[string]metricdata.Metrics{}
	for _, scope := range collected.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			measurements[measurement.Name] = measurement
		}
	}
	return measurements
}

// lazyPool creates a pool that dials only when a read acquires a connection.
func lazyPool(t *testing.T, address string) *pgxpool.Pool {
	t.Helper()
	configuration, err := pgxpool.ParseConfig(
		"postgres://go_pricing_reader@" + address + "/pricing?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	configuration.MinConns = 0
	configuration.MaxConns = 2
	configuration.ConnConfig.ConnectTimeout = time.Second
	pool, err := pgxpool.NewWithConfig(context.Background(), configuration)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// silentListener accepts connections and never answers the PostgreSQL startup message.
func silentListener(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var accepted []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepted = append(accepted, connection)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, connection := range accepted {
			_ = connection.Close()
		}
	})
	return listener.Addr().String()
}

func operationPoints(
	t *testing.T,
	measurements map[string]metricdata.Metrics,
) map[string]int64 {
	t.Helper()
	measurement, ok := measurements["insurance_hub.pricing.database.operations"]
	if !ok {
		t.Fatal("missing insurance_hub.pricing.database.operations")
	}
	points := map[string]int64{}
	for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
		if point.Attributes.Len() != 3 {
			t.Errorf("unexpected database operation attributes: %v", point.Attributes)
		}
		operation, _ := point.Attributes.Value("insurance_hub.pricing.operation")
		stage, _ := point.Attributes.Value("insurance_hub.pricing.database.stage")
		outcome, _ := point.Attributes.Value("insurance_hub.pricing.database.outcome")
		points[operation.AsString()+"/"+stage.AsString()+"/"+outcome.AsString()] += point.Value
	}
	return points
}

func assertNoMarker(
	t *testing.T,
	spans []sdktrace.ReadOnlySpan,
	measurements map[string]metricdata.Metrics,
) {
	t.Helper()
	for _, span := range spans {
		for _, attr := range span.Attributes() {
			if strings.Contains(attr.Value.String(), privateMarker) ||
				attr.Key == "db.statement" || attr.Key == "db.query.text" {
				t.Errorf("unsafe span attribute on %s: %v", span.Name(), attr)
			}
		}
		if strings.Contains(span.Status().Description, privateMarker) {
			t.Errorf("unsafe span status on %s", span.Name())
		}
	}
	for name, measurement := range measurements {
		var sets []attribute.Set
		switch data := measurement.Data.(type) {
		case metricdata.Sum[int64]:
			for _, point := range data.DataPoints {
				sets = append(sets, point.Attributes)
			}
		case metricdata.Histogram[float64]:
			for _, point := range data.DataPoints {
				sets = append(sets, point.Attributes)
			}
		}
		for _, set := range sets {
			for _, attr := range set.ToSlice() {
				if strings.Contains(attr.Value.String(), privateMarker) {
					t.Errorf("unsafe %s label: %v", name, attr)
				}
			}
		}
	}
}

func TestDatabaseObserver_CollectsPoolStateAndStageSignalsUntilClosed(t *testing.T) {
	// given
	signals := newObservedSignals(t)
	observer, err := NewObserver(
		lazyPool(t, "127.0.0.1:1"),
		signals.traceProvider,
		signals.meterProvider,
	)
	if err != nil {
		t.Fatalf("create observer: %v", err)
	}
	t.Cleanup(func() { _ = observer.Close() })

	// when
	stageErr := observer.stage(
		context.Background(),
		"calculate_price",
		"query",
		"SELECT tariff",
		trace.SpanKindClient,
		func(context.Context) error { return nil },
	)
	observer.decode(context.Background(), "calculate_price", nil)
	observer.decode(context.Background(), "calculate_price", application.ErrTariffInvalid)
	measurements := signals.collect(t)

	// then
	if stageErr != nil {
		t.Fatalf("successful query: %v", stageErr)
	}
	counts := measurements["db.client.connection.count"].Data.(metricdata.Sum[int64]).DataPoints
	if len(counts) != 2 {
		t.Fatalf("pool state points = %v", counts)
	}
	for _, point := range counts {
		state, _ := point.Attributes.Value("db.client.connection.state")
		name, _ := point.Attributes.Value("db.client.connection.pool.name")
		if (state.AsString() != "idle" && state.AsString() != "used") ||
			name.AsString() != "pricing-reader" || point.Value != 0 {
			t.Errorf("unexpected pool state point: %v", point)
		}
	}
	maximum := measurements["db.client.connection.max"].Data.(metricdata.Sum[int64]).DataPoints
	if len(maximum) != 1 || maximum[0].Value != 2 {
		t.Errorf("pool maximum = %v, want 2", maximum)
	}
	want := map[string]int64{
		"calculate_price/query/success":       1,
		"calculate_price/decode/success":      1,
		"calculate_price/decode/invalid_data": 1,
	}
	got := operationPoints(t, measurements)
	if len(got) != len(want) {
		t.Errorf("database operations = %v, want %v", got, want)
	}
	for key, count := range want {
		if got[key] != count {
			t.Errorf("database operation %s = %d, want %d", key, got[key], count)
		}
	}
	query := measurements["db.client.operation.duration"].Data.(metricdata.Histogram[float64]).DataPoints
	if len(query) != 1 || query[0].Count != 1 || query[0].Attributes.Len() != 5 {
		t.Errorf("query duration points = %v", query)
	}
	spans := signals.spanRecorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "SELECT tariff" ||
		spans[0].SpanKind() != trace.SpanKindClient || spans[0].Status().Code != codes.Unset {
		t.Fatalf("query spans = %v", spans)
	}
	querySet := attribute.NewSet(spans[0].Attributes()...)
	summary, _ := querySet.Value("db.query.summary")
	if summary.AsString() != "get tariff" {
		t.Errorf("query summary = %q", summary.AsString())
	}

	// when
	closeErr := observer.Close()
	afterClose := signals.collect(t)

	// then
	if closeErr != nil {
		t.Fatalf("close observer: %v", closeErr)
	}
	for _, name := range []string{"db.client.connection.count", "db.client.connection.max"} {
		if _, ok := afterClose[name]; ok {
			t.Errorf("pool metric %s continued after observer closed", name)
		}
	}
}

func TestReader_GetTariff_ObservesUnavailableAcquireWithinCallerTrace(t *testing.T) {
	// given
	signals := newObservedSignals(t)
	pool := lazyPool(t, "127.0.0.1:1")
	observer, err := NewObserver(pool, signals.traceProvider, signals.meterProvider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close() })
	reader, err := NewReader(pool, time.Second, time.Second, observer)
	if err != nil {
		t.Fatal(err)
	}
	ctx, parent := signals.traceProvider.Tracer("test").Start(context.Background(), "calculate")

	// when
	tariff, found, readErr := reader.GetTariff(ctx, privateMarker)
	parent.End()

	// then
	if tariff.Code != "" || found || !errors.Is(readErr, application.ErrReaderUnavailable) ||
		errors.Is(readErr, application.ErrTariffInvalid) {
		t.Fatalf("GetTariff() = (%+v, %t, %v)", tariff, found, readErr)
	}
	spans := signals.spanRecorder.Ended()
	if len(spans) != 2 || spans[0].Name() != "pricing.db.acquire" {
		t.Fatalf("read spans = %v", spans)
	}
	acquire := spans[0]
	if acquire.Parent().SpanID() != parent.SpanContext().SpanID() ||
		acquire.SpanContext().TraceID() != parent.SpanContext().TraceID() {
		t.Errorf("acquire span is disconnected from the caller trace")
	}
	attrs := attribute.NewSet(acquire.Attributes()...)
	stage, _ := attrs.Value("insurance_hub.pricing.database.stage")
	errorType, _ := attrs.Value("error.type")
	if acquire.Status().Code != codes.Error || stage.AsString() != "acquire" ||
		errorType.AsString() != "storage_unavailable" {
		t.Errorf(
			"acquire span status=%v attributes=%v",
			acquire.Status(),
			acquire.Attributes(),
		)
	}
	measurements := signals.collect(t)
	if got := operationPoints(t, measurements); len(got) != 1 ||
		got["calculate_price/acquire/storage_unavailable"] != 1 {
		t.Errorf("database operations = %v", got)
	}
	wait := measurements["db.client.connection.wait_time"].Data.(metricdata.Histogram[float64]).DataPoints
	if len(wait) != 1 || wait[0].Count != 1 {
		t.Errorf("acquire wait points = %v", wait)
	}
	if _, ok := measurements["db.client.connection.timeouts"]; ok {
		t.Error("refused connection was counted as an acquire timeout")
	}
	if _, ok := measurements["db.client.operation.duration"]; ok {
		t.Error("query duration recorded without a query")
	}
	assertNoMarker(t, spans, measurements)
}

func TestReader_GetTariff_CountsAcquireTimeoutAsUnavailableStorage(t *testing.T) {
	// given
	signals := newObservedSignals(t)
	pool := lazyPool(t, silentListener(t))
	observer, err := NewObserver(pool, signals.traceProvider, signals.meterProvider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observer.Close() })
	reader, err := NewReader(pool, 50*time.Millisecond, time.Second, observer)
	if err != nil {
		t.Fatal(err)
	}

	// when
	_, found, readErr := reader.GetTariff(context.Background(), "TRI")

	// then
	if found || !errors.Is(readErr, application.ErrReaderUnavailable) {
		t.Fatalf("GetTariff() = (%t, %v)", found, readErr)
	}
	measurements := signals.collect(t)
	timeouts := measurements["db.client.connection.timeouts"].Data.(metricdata.Sum[int64]).DataPoints
	if len(timeouts) != 1 || timeouts[0].Value != 1 {
		t.Errorf("acquire timeouts = %v, want one", timeouts)
	}
	if got := operationPoints(
		t,
		measurements,
	); got["calculate_price/acquire/storage_unavailable"] != 1 {
		t.Errorf("database operations = %v", got)
	}
}

func TestDatabaseOutcome_SeparatesUnavailableStorageFromInvalidData(t *testing.T) {
	// given
	acquireDeadline := readerFailure{
		operation: "acquire connection",
		cause:     context.DeadlineExceeded,
	}
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"success", nil, "success"},
		{"invalid tariff", application.ErrTariffInvalid, "invalid_data"},
		{"ambiguous tariff", application.ErrTariffAmbiguous, "invalid_data"},
		{"unavailable reader", acquireDeadline, "storage_unavailable"},
		{"cancelled request", context.Canceled, "cancelled"},
		{"expired request", context.DeadlineExceeded, "deadline_exceeded"},
		{"unclassified failure", errors.New(privateMarker), "storage_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			got := databaseOutcome(test.err)

			// then
			if got != test.want {
				t.Errorf(
					"databaseOutcome(%v) = %s, want %s",
					test.err,
					got,
					test.want,
				)
			}
		})
	}
}

func TestSelectTariff_PreservesInvalidBeforeAmbiguousPrecedence(t *testing.T) {
	// given
	base := []byte(`[{"position":1,"coverCode":"C1","condition":null,"formula":"10"}]`)
	markup := []byte(`[{"position":1,"kind":"perc_markup","condition":null,"factor":"1.00"}]`)
	valid := storedTariff{code: "TRI", rawBase: base, rawMarkup: markup}
	invalid := storedTariff{code: "TRI", rawBase: []byte(`[]`), rawMarkup: markup}
	for _, test := range []struct {
		name   string
		stored []storedTariff
		want   error
	}{
		{"single tariff", []storedTariff{valid}, nil},
		{"duplicate tariffs", []storedTariff{valid, valid}, application.ErrTariffAmbiguous},
		{"invalid duplicate", []storedTariff{valid, invalid}, application.ErrTariffInvalid},
		{
			"mismatched code",
			[]storedTariff{{code: "OTHER", rawBase: base, rawMarkup: markup}},
			application.ErrTariffInvalid,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			tariff, err := selectTariff("TRI", test.stored)

			// then
			if !errors.Is(err, test.want) ||
				(test.want == nil) != (tariff.Code == "TRI") {
				t.Errorf(
					"selectTariff() = (%+v, %v), want error %v",
					tariff,
					err,
					test.want,
				)
			}
		})
	}
}

func TestDatabaseObserver_NilObserverPreservesReadBehavior(t *testing.T) {
	// given
	var observer *Observer
	want := errors.Join(application.ErrReaderUnavailable, errors.New(privateMarker))

	// when
	err := observer.stage(
		context.Background(),
		"calculate_price",
		"query",
		"SELECT tariff",
		trace.SpanKindClient,
		func(context.Context) error { return want },
	)
	observer.decode(context.Background(), "calculate_price", err)
	closeErr := observer.Close()

	// then
	if !errors.Is(err, application.ErrReaderUnavailable) || closeErr != nil {
		t.Fatalf("nil observer changed behavior: %v, %v", err, closeErr)
	}
}

func TestDatabaseObserver_RejectsMissingDependencies(t *testing.T) {
	// when
	observer, err := NewObserver(nil, nil, nil)

	// then
	if observer != nil || err == nil {
		t.Fatalf("NewObserver() = (%v, %v)", observer, err)
	}
}

func TestNewReader_RejectsMultipleObservers(t *testing.T) {
	// when
	reader, err := NewReader(&pgxpool.Pool{}, time.Second, time.Second, nil, nil)

	// then
	if reader != nil || err == nil || !strings.Contains(err.Error(), "at most one observer") {
		t.Fatalf("NewReader() = (%v, %v)", reader, err)
	}
}
