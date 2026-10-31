package telemetry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestRequestObserver_RecordsBoundedRequestAndApplicationOutcomes(t *testing.T) {
	const private = "private-query-or-policy-marker"
	var logs bytes.Buffer
	observer, err := NewRequestObserver(
		slog.New(slog.NewJSONHandler(&logs, nil)),
		noop.NewTracerProvider(),
		sdkmetric.NewMeterProvider(),
		propagation.TraceContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		"https://example.test/policies?q="+private,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.TLS = nil
	request.ProtoMajor = 2
	request.Header.Set(
		"Traceparent",
		"00-0102030405060708090a0b0c0d0e0f10-1112131415161718-01",
	)

	request, state := observer.StartHTTP(request)
	spanContext := trace.SpanContextFromContext(request.Context())
	applicationErr := observer.Application(request.Context(), func(context.Context) error {
		return errors.New(private)
	})
	MarkError(request.Context(), context.DeadlineExceeded)
	state.Finish(request.Context(), http.StatusInternalServerError)

	if applicationErr == nil ||
		spanContext.TraceID().String() != "0102030405060708090a0b0c0d0e0f10" ||
		strings.Contains(logs.String(), private) ||
		!strings.Contains(logs.String(), `"outcome":"deadline_exceeded"`) ||
		!strings.Contains(logs.String(), `"http_status_code":500`) {
		t.Fatalf(
			"span=%s application=%v logs=%s",
			spanContext.TraceID(),
			applicationErr,
			logs.String(),
		)
	}
}

func TestRequestObserver_UsesSuccessDefaultsAndRejectsMissingDependencies(t *testing.T) {
	if _, err := NewRequestObserver(nil, nil, nil, nil); err == nil {
		t.Fatal("missing observer dependencies accepted")
	}
	var logs bytes.Buffer
	observer, err := NewRequestObserver(
		slog.New(slog.NewJSONHandler(&logs, nil)),
		noop.NewTracerProvider(),
		sdkmetric.NewMeterProvider(),
		propagation.TraceContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://example.test/policies?q=", nil)
	if err != nil {
		t.Fatal(err)
	}
	request, state := observer.StartHTTP(request)
	if err := observer.Application(
		request.Context(),
		func(context.Context) error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	state.Finish(request.Context(), http.StatusOK)
	if !strings.Contains(logs.String(), `"outcome":"success"`) ||
		!strings.Contains(logs.String(), `"http_status_code":200`) {
		t.Fatalf("success log = %s", logs.String())
	}
}

func TestRequestOutcomeClassificationIsBounded(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{nil, "success"},
		{context.Canceled, "cancelled"},
		{context.DeadlineExceeded, "deadline_exceeded"},
		{errors.New("private"), "internal_error"},
	} {
		if got := Classify(test.err); got != test.want {
			t.Errorf("Classify(%v) = %q, want %q", test.err, got, test.want)
		}
	}
	for _, test := range []struct {
		status int
		want   string
	}{
		{http.StatusOK, "success"},
		{http.StatusBadRequest, "invalid_request"},
		{http.StatusMethodNotAllowed, "invalid_request"},
		{http.StatusInternalServerError, "internal_error"},
	} {
		if got := outcomeFromStatus(test.status); got != test.want {
			t.Errorf("outcomeFromStatus(%d) = %q, want %q", test.status, got, test.want)
		}
	}
}

func TestRequestFinish_OmitsHTTPStatusWhenCallerDisconnects(t *testing.T) {
	var logs bytes.Buffer
	observer, err := NewRequestObserver(
		slog.New(slog.NewJSONHandler(&logs, nil)),
		noop.NewTracerProvider(),
		sdkmetric.NewMeterProvider(),
		propagation.TraceContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://example.test/policies?q=*", nil)
	if err != nil {
		t.Fatal(err)
	}
	request, state := observer.StartHTTP(request)
	MarkError(request.Context(), context.Canceled)
	state.Finish(request.Context(), 0)
	if strings.Contains(logs.String(), "http_status_code") ||
		!strings.Contains(logs.String(), `"outcome":"cancelled"`) {
		t.Fatalf("caller cancellation log = %s", logs.String())
	}
}
