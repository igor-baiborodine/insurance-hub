package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/quick"

	"go.opentelemetry.io/otel/attribute"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
)

func TestDependencyObserver_RecordsOnlyBoundedOutcomes(t *testing.T) {
	// given
	tests := []struct {
		name       string
		operation  string
		dependency string
		result     string
		err        error
		want       string
	}{
		{
			"postgres success",
			OperationFindDocuments,
			DependencyPostgreSQL,
			DependencyResultSuccess,
			nil,
			"success",
		},
		{
			"object fetch failed",
			OperationFindDocuments,
			DependencyObjectStore,
			DependencyResultFetchFailed,
			nil,
			"fetch_failed",
		},
		{
			"cancelled",
			OperationGenerateDocument,
			DependencyPostgreSQL,
			DependencyResultSuccess,
			context.Canceled,
			"cancelled",
		},
		{
			"deadline",
			OperationGenerateDocument,
			DependencyObjectStore,
			DependencyResultSuccess,
			context.DeadlineExceeded,
			"deadline_exceeded",
		},
		{
			"safe error",
			"private-operation",
			"private-dependency",
			DependencyResultSuccess,
			errors.New("private-error"),
			"error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			var output bytes.Buffer
			recorder := tracetest.NewSpanRecorder()
			tracerProvider := sdktrace.NewTracerProvider(
				sdktrace.WithSpanProcessor(recorder),
			)
			t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
			observer, err := NewDependencyObserver(
				slog.New(slog.NewJSONHandler(&output, nil)),
				metricnoop.NewMeterProvider(),
				tracerProvider,
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, request := observer.Start(
				context.Background(),
				test.operation,
				test.dependency,
			)

			// when
			request.Finish(ctx, test.result, test.err)

			// then
			if !bytes.Contains(output.Bytes(), []byte(`"outcome":"`+test.want+`"`)) {
				t.Fatalf("log = %s", output.Bytes())
			}
			combined := output.String()
			for _, span := range recorder.Ended() {
				combined += span.Name()
				for _, item := range span.Attributes() {
					combined += string(item.Key) + item.Value.AsString()
				}
			}
			if strings.Contains(combined, "private-dependency") ||
				strings.Contains(combined, "private-operation") ||
				strings.Contains(combined, "private-error") {
				t.Fatalf("unbounded value emitted: %s", combined)
			}
		})
	}
}

func TestDependencyObserver_RecordsGenerationStageOnMetricsAndClientSpan(t *testing.T) {
	// given
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		_ = tracerProvider.Shutdown(context.Background())
		_ = meterProvider.Shutdown(context.Background())
	})
	observer, err := NewDependencyObserver(
		slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		meterProvider,
		tracerProvider,
	)
	if err != nil {
		t.Fatal(err)
	}

	// when
	ctx, request := observer.Start(
		context.Background(),
		OperationGenerateDocument,
		DependencyJSReport,
	)
	request.Finish(ctx, DependencyResultSuccess, nil)

	// then
	wantAttributes := attribute.NewSet(
		attribute.String("insurance_hub.document.dependency", DependencyJSReport),
		attribute.String("insurance_hub.document.operation", OperationGenerateDocument),
		attribute.String("insurance_hub.document.stage", "render"),
		attribute.String("insurance_hub.document.outcome", DependencyResultSuccess),
	)
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	measurementCount := 0
	for _, scope := range metrics.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			switch data := measurement.Data.(type) {
			case metricdata.Sum[int64]:
				if measurement.Name == "insurance_hub.document.dependencies" {
					measurementCount++
					assertDependencyAttributes(
						t,
						data.DataPoints[0].Attributes,
						wantAttributes,
					)
				}
			case metricdata.Histogram[float64]:
				if measurement.Name == "insurance_hub.document.dependency.duration" {
					measurementCount++
					assertDependencyAttributes(
						t,
						data.DataPoints[0].Attributes,
						wantAttributes,
					)
				}
			}
		}
	}
	spans := recorder.Ended()
	if measurementCount != 2 || len(spans) != 1 ||
		spans[0].Name() != "jsreport generate_document" ||
		spans[0].SpanKind().String() != "client" {
		t.Fatalf("measurements = %d, spans = %+v", measurementCount, spans)
	}
	gotSpanAttributes := attribute.NewSet(spans[0].Attributes()...)
	assertDependencyAttributes(t, gotSpanAttributes, wantAttributes)
}

func TestBoundedDependencyStage_RejectsUnexpectedCombinations(t *testing.T) {
	// given
	tests := []struct {
		operation  string
		dependency string
		want       string
	}{
		{OperationFindDocuments, DependencyPostgreSQL, "lookup"},
		{OperationFindDocuments, DependencyObjectStore, "lookup"},
		{OperationGenerateDocument, DependencyJSReport, "render"},
		{OperationGenerateDocument, DependencyObjectStore, "upload"},
		{OperationGenerateDocument, DependencyPostgreSQL, "metadata"},
		{OperationFindDocuments, DependencyJSReport, "unknown"},
		{"private-operation", DependencyPostgreSQL, "unknown"},
		{OperationGenerateDocument, "private-dependency", "unknown"},
	}

	for _, test := range tests {
		// when
		got := boundedDependencyStage(
			boundedDependencyOperation(test.operation),
			boundedDependency(test.dependency),
		)

		// then
		if got != test.want || strings.Contains(got, "private") {
			t.Errorf(
				"stage(%q, %q) = %q, want %q",
				test.operation,
				test.dependency,
				got,
				test.want,
			)
		}
	}
}

func TestDependencyBounds_KeepArbitraryInputsKnownOrUnknown(t *testing.T) {
	// given
	knownOperation := map[string]bool{
		OperationFindDocuments: true, OperationGenerateDocument: true, "unknown": true,
	}
	knownDependency := map[string]bool{
		DependencyPostgreSQL: true, DependencyObjectStore: true, DependencyJSReport: true,
		"unknown": true,
	}
	knownStage := map[string]bool{
		"lookup": true, "render": true, "upload": true, "metadata": true, "unknown": true,
	}

	// when / then
	err := quick.Check(func(operation, dependency string) bool {
		gotOperation := boundedDependencyOperation(operation)
		gotDependency := boundedDependency(dependency)
		stage := boundedDependencyStage(gotOperation, gotDependency)
		return knownOperation[gotOperation] && knownDependency[gotDependency] &&
			knownStage[stage]
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func assertDependencyAttributes(t *testing.T, got, want attribute.Set) {
	t.Helper()
	if !got.Equals(&want) {
		t.Fatalf("attributes = %v, want %v", got.ToSlice(), want.ToSlice())
	}
}

func TestObservers_RequireDependenciesAndRecordLookupOutcomes(t *testing.T) {
	// given
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	meter := metricnoop.NewMeterProvider()
	tracer := tracenoop.NewTracerProvider()

	// when / then
	if _, err := NewDependencyObserver(nil, meter, tracer); err == nil {
		t.Fatal("missing dependency observer logger accepted")
	}
	if _, err := NewDependencyObserver(logger, nil, tracer); err == nil {
		t.Fatal("missing dependency meter accepted")
	}
	if _, err := NewDependencyObserver(logger, meter, nil); err == nil {
		t.Fatal("missing dependency tracer accepted")
	}
	if _, err := NewLookupObserver(nil, meter); err == nil {
		t.Fatal("missing lookup logger accepted")
	}
	if _, err := NewLookupObserver(logger, nil); err == nil {
		t.Fatal("missing lookup meter accepted")
	}

	// given
	observer, err := NewLookupObserver(logger, meter)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		omitted int
		err     error
	}{
		{0, nil},
		{1, nil},
		{0, context.Canceled},
		{0, context.DeadlineExceeded},
		{0, application.ErrResponseLimit},
		{0, application.ErrUnavailable},
		{0, errors.New("unexpected")},
	}
	for _, test := range tests {
		// when / then
		observer.Record(context.Background(), 1, test.omitted, test.err)
	}
}
