package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/metric/noop"
)

func TestNewRequestObserver_RequiresDependencies(t *testing.T) {
	provider := noop.NewMeterProvider()
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	if _, err := NewRequestObserver(nil, provider); err == nil {
		t.Fatal("missing logger accepted")
	}
	if _, err := NewRequestObserver(logger, nil); err == nil {
		t.Fatal("missing meter provider accepted")
	}
}

func TestRequestObserver_RecordsBoundedGRPCOutcomes(t *testing.T) {
	for _, test := range []struct {
		status  string
		outcome string
		level   string
		logged  string
	}{
		{status: "OK", outcome: "success", level: "INFO", logged: "OK"},
		{status: "INVALID_ARGUMENT", outcome: "invalid_request", level: "INFO", logged: "INVALID_ARGUMENT"},
		{status: "CANCELLED", outcome: "cancelled", level: "WARN", logged: "CANCELLED"},
		{status: "DEADLINE_EXCEEDED", outcome: "deadline_exceeded", level: "WARN", logged: "DEADLINE_EXCEEDED"},
		{status: "INTERNAL", outcome: "internal_error", level: "ERROR", logged: "INTERNAL"},
		{status: "private-status", outcome: "internal_error", level: "ERROR", logged: "UNKNOWN"},
	} {
		t.Run(test.status, func(t *testing.T) {
			observer, output := requestObserver(t)

			observer.StartGRPC(context.Background()).
				Finish(context.Background(), test.status)

			record := decodeCompletion(t, output)
			assertLogValue(t, record, "operation", "echo")
			assertLogValue(t, record, "transport", "grpc")
			assertLogValue(t, record, "outcome", test.outcome)
			assertLogValue(t, record, "rpc_status_code", test.logged)
			assertLogValue(t, record, "level", test.level)
			if bytes.Contains(output.Bytes(), []byte("private-status")) {
				t.Fatal("unbounded transport status was logged")
			}
		})
	}
}

func TestRequestObserver_RecordsBoundedHTTPOutcomes(t *testing.T) {
	for _, test := range []struct {
		name      string
		route     string
		status    int
		operation string
		outcome   string
		level     string
	}{
		{name: "success", route: "POST /v1/example/echo", status: 200, operation: "echo", outcome: "success", level: "INFO"},
		{name: "invalid request", route: "POST /unknown", status: 400, operation: "unknown", outcome: "invalid_request", level: "INFO"},
		{name: "cancelled", route: "POST /v1/example/echo", status: 499, operation: "echo", outcome: "cancelled", level: "WARN"},
		{name: "deadline", route: "POST /v1/example/echo", status: 504, operation: "echo", outcome: "deadline_exceeded", level: "WARN"},
		{name: "internal", route: "POST /v1/example/echo", status: 500, operation: "echo", outcome: "internal_error", level: "ERROR"},
	} {
		t.Run(test.name, func(t *testing.T) {
			observer, output := requestObserver(t)

			observer.StartHTTP(context.Background(), test.route).FinishHTTP(
				context.Background(), test.status,
			)

			record := decodeCompletion(t, output)
			assertLogValue(t, record, "operation", test.operation)
			assertLogValue(t, record, "transport", "http")
			assertLogValue(t, record, "route", test.route)
			assertLogValue(t, record, "outcome", test.outcome)
			assertLogValue(t, record, "level", test.level)
			if record["http_status_code"] != float64(test.status) {
				t.Errorf(
					"http_status_code = %v, want %d",
					record["http_status_code"],
					test.status,
				)
			}
		})
	}
}

func requestObserver(t *testing.T) (*RequestObserver, *bytes.Buffer) {
	t.Helper()
	output := &bytes.Buffer{}
	observer, err := NewRequestObserver(
		slog.New(slog.NewJSONHandler(output, nil)),
		noop.NewMeterProvider(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return observer, output
}

func decodeCompletion(t *testing.T, output *bytes.Buffer) map[string]any {
	t.Helper()
	record := map[string]any{}
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode completion log: %v", err)
	}
	return record
}

func assertLogValue(t *testing.T, record map[string]any, key string, want string) {
	t.Helper()
	if record[key] != want {
		t.Errorf("%s = %v, want %s", key, record[key], want)
	}
}
