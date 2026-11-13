package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
)

func TestDependencyObserver_RecordsOnlyBoundedOutcomes(t *testing.T) {
	// given
	tests := []struct {
		name       string
		dependency string
		result     string
		err        error
		want       string
	}{
		{"postgres success", DependencyPostgreSQL, DependencyResultSuccess, nil, "success"},
		{
			"object fetch failed",
			DependencyObjectStore,
			DependencyResultFetchFailed,
			nil,
			"fetch_failed",
		},
		{
			"cancelled",
			DependencyPostgreSQL,
			DependencyResultSuccess,
			context.Canceled,
			"cancelled",
		},
		{
			"deadline",
			DependencyObjectStore,
			DependencyResultSuccess,
			context.DeadlineExceeded,
			"deadline_exceeded",
		},
		{
			"safe error",
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
			observer, err := NewDependencyObserver(
				slog.New(slog.NewJSONHandler(&output, nil)),
				metricnoop.NewMeterProvider(),
				tracenoop.NewTracerProvider(),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, request := observer.Start(context.Background(), test.dependency)

			// when
			request.Finish(ctx, test.result, test.err)

			// then
			if !bytes.Contains(output.Bytes(), []byte(`"outcome":"`+test.want+`"`)) {
				t.Fatalf("log = %s", output.Bytes())
			}
			if bytes.Contains(output.Bytes(), []byte("private-dependency")) ||
				bytes.Contains(output.Bytes(), []byte("private-error")) {
				t.Fatalf("unbounded value logged: %s", output.Bytes())
			}
		})
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
