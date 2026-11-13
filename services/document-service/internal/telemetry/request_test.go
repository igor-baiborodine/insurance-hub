package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
)

func TestNewRequestObserver_RequiresLoggerAndProvider(t *testing.T) {
	provider := metricnoop.NewMeterProvider()
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	for _, test := range []struct {
		name     string
		logger   *slog.Logger
		provider metric.MeterProvider
	}{
		{name: "missing logger", provider: provider},
		{name: "missing provider", logger: logger},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRequestObserver(test.logger, test.provider); err == nil {
				t.Fatal("missing observer dependency accepted")
			}
		})
	}
}

func TestRequestObserver_RecordsBoundedTransportOutcomes(t *testing.T) {
	var output bytes.Buffer
	observer, err := NewRequestObserver(
		slog.New(slog.NewJSONHandler(&output, nil)),
		metricnoop.NewMeterProvider(),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	type expectedRecord struct {
		transport string
		operation string
		outcome   string
		status    any
		level     string
	}
	expected := make([]expectedRecord, 0, 12)
	for _, test := range []struct {
		status     string
		normalized string
		outcome    string
		level      string
	}{
		{status: "OK", normalized: "OK", outcome: "success", level: "INFO"},
		{status: "INVALID_ARGUMENT", normalized: "INVALID_ARGUMENT", outcome: "invalid_request", level: "INFO"},
		{status: "CANCELLED", normalized: "CANCELLED", outcome: "cancelled", level: "WARN"},
		{status: "DEADLINE_EXCEEDED", normalized: "DEADLINE_EXCEEDED", outcome: "deadline_exceeded", level: "WARN"},
		{status: "INTERNAL", normalized: "INTERNAL", outcome: "internal_error", level: "ERROR"},
		{status: "private-status-marker", normalized: "UNKNOWN", outcome: "internal_error", level: "ERROR"},
	} {
		observer.StartGRPC(ctx).Finish(ctx, test.status)
		expected = append(expected, expectedRecord{
			transport: "grpc", operation: "echo", outcome: test.outcome,
			status: test.normalized, level: test.level,
		})
	}
	for _, test := range []struct {
		status  int
		outcome string
		level   string
	}{
		{status: 200, outcome: "success", level: "INFO"},
		{status: 400, outcome: "invalid_request", level: "INFO"},
		{status: 499, outcome: "cancelled", level: "WARN"},
		{status: 504, outcome: "deadline_exceeded", level: "WARN"},
		{status: 500, outcome: "internal_error", level: "ERROR"},
	} {
		observer.StartHTTP(ctx, "POST /v1/example/echo").FinishHTTP(ctx, test.status)
		expected = append(expected, expectedRecord{
			transport: "http", operation: "echo", outcome: test.outcome,
			status: float64(test.status), level: test.level,
		})
	}
	observer.StartHTTP(ctx, "private-route-marker").FinishHTTP(ctx, 404)
	expected = append(expected, expectedRecord{
		transport: "http", operation: "unknown", outcome: "invalid_request",
		status: float64(404), level: "INFO",
	})

	decoder := json.NewDecoder(&output)
	for index, want := range expected {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode record %d: %v", index, err)
		}
		if record["msg"] != "request completed" ||
			record["transport"] != want.transport ||
			record["operation"] != want.operation ||
			record["outcome"] != want.outcome ||
			record["level"] != want.level {
			t.Errorf("record %d = %v", index, record)
		}
		status := record["rpc_status_code"]
		if want.transport == "http" {
			status = record["http_status_code"]
		}
		if status != want.status {
			t.Errorf("record %d status = %v, want %v", index, status, want.status)
		}
	}
	var extra map[string]any
	if err := decoder.Decode(&extra); err == nil {
		t.Fatalf("unexpected extra record: %v", extra)
	}
}
