package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"
)

func TestNew_WritesJSONWithServiceIdentityAndLevelFiltering(t *testing.T) {
	var output bytes.Buffer
	log := logger.New(&output, "go-service", slog.LevelInfo)

	log.DebugContext(context.Background(), "filtered")
	log.InfoContext(context.Background(), "started", slog.String("component", "test"))

	entries := decodeEntries(t, output.String())
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry["service"] != "go-service" || entry["level"] != "INFO" ||
		entry["msg"] != "started" || entry["component"] != "test" {
		t.Errorf("unexpected log entry: %#v", entry)
	}
}

func TestLogRequired_PreservesCallerContractBelowDiagnosticThreshold(t *testing.T) {
	var output bytes.Buffer
	log := logger.NewIdentity(&output, testIdentity(), slog.LevelError)

	logger.LogRequired(
		context.Background(),
		log,
		slog.LevelInfo,
		"Product request completed",
		slog.String("classification", "client_error"),
	)

	entries := decodeEntries(t, output.String())
	if len(entries) != 1 || entries[0]["msg"] != "Product request completed" ||
		entries[0]["level"] != "INFO" || entries[0]["classification"] != "client_error" {
		t.Fatalf("unexpected required record: %#v", entries)
	}
}

func TestNew_AddsTraceCorrelationFromContext(t *testing.T) {
	traceID := trace.TraceID{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	spanID := trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(
		trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
		},
	))
	var output bytes.Buffer

	logger.New(&output, "go-service", slog.LevelInfo).InfoContext(ctx, "correlated")

	entries := decodeEntries(t, output.String())
	if len(entries) != 1 || entries[0]["trace_id"] != traceID.String() ||
		entries[0]["span_id"] != spanID.String() {
		t.Errorf("unexpected trace correlation: %#v", entries)
	}
}

func TestNewIdentity_RedactsAttributesRecursivelyAndUsesUTC(t *testing.T) {
	const privateMarker = "private-marker"
	type profile struct {
		AccessToken string           `json:"accessToken"`
		Children    []map[string]any `json:"children"`
		Safe        string           `json:"safe"`
	}
	var output bytes.Buffer
	log := logger.NewIdentity(&output, testIdentity(), slog.LevelDebug).With(
		slog.String("authorization", privateMarker),
		slog.Any("bound", map[string]any{"credential": privateMarker, "safe": "kept"}),
	)

	log.ErrorContext(
		context.Background(),
		"safe message",
		slog.Any("error", errors.New(privateMarker)),
		slog.Group("details", slog.String("password", privateMarker)),
		slog.Any("profile", profile{
			AccessToken: privateMarker,
			Children: []map[string]any{
				{"response_payload": privateMarker, "safe": "child-kept"},
			},
			Safe: "kept",
		}),
		slog.Any(
			"unsupported",
			map[string]any{"secret": privateMarker, "stream": make(chan int)},
		),
	)

	if strings.Contains(output.String(), privateMarker) {
		t.Fatalf("captured logs expose private marker: %s", output.String())
	}
	entries := decodeEntries(t, output.String())
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}
	entry := entries[0]
	for key, want := range map[string]string{
		"service_namespace":           "insurance-hub",
		"service_name":                "go-service",
		"service_version":             "test-revision",
		"service_instance_id":         "test-instance",
		"deployment_environment_name": "test",
		"service_implementation":      "go",
		"authorization":               "[REDACTED]",
		"error":                       "[REDACTED]",
	} {
		if entry[key] != want {
			t.Errorf("%s = %v, want %s", key, entry[key], want)
		}
	}
	assertRedactedMap(t, entry["bound"], "credential", "safe", "kept")
	details, ok := entry["details"].(map[string]any)
	if !ok || details["password"] != "[REDACTED]" {
		t.Errorf("unexpected nested group: %#v", entry["details"])
	}
	profileValue, ok := entry["profile"].(map[string]any)
	if !ok || profileValue["accessToken"] != "[REDACTED]" || profileValue["safe"] != "kept" {
		t.Fatalf("unexpected sanitized profile: %#v", entry["profile"])
	}
	children, ok := profileValue["children"].([]any)
	if !ok || len(children) != 1 {
		t.Fatalf("unexpected sanitized children: %#v", profileValue["children"])
	}
	assertRedactedMap(t, children[0], "response_payload", "safe", "child-kept")
	if entry["unsupported"] != "[REDACTED]" {
		t.Errorf("unsupported structured value = %#v, want redacted", entry["unsupported"])
	}
	if timestamp, ok := entry["time"].(string); !ok || !strings.HasSuffix(timestamp, "Z") {
		t.Errorf("timestamp is not UTC: %v", entry["time"])
	}
}

func TestNew_RedactsSensitiveHandlerGroupsAndBoundAttributes(t *testing.T) {
	const privateMarker = "private-marker"
	var output bytes.Buffer
	log := logger.New(&output, "go-service", slog.LevelInfo)

	log.WithGroup("customer").Info("safe message", "name", privateMarker)
	log.WithGroup("request").With("name", privateMarker).
		WithGroup("details").Info("safe message", "note", privateMarker)
	log.WithGroup("public").WithGroup("payment").Info(
		"safe message", "note", privateMarker,
	)
	log.WithGroup("public").Info("safe message", "name", "kept")

	if strings.Contains(output.String(), privateMarker) {
		t.Fatalf("sensitive handler group exposed private marker: %s", output.String())
	}
	entries := decodeEntries(t, output.String())
	if len(entries) != 4 {
		t.Fatalf("entry count = %d, want 4", len(entries))
	}
	if customer := entries[0]["customer"].(map[string]any); customer["name"] != "[REDACTED]" {
		t.Errorf("customer name = %#v", customer["name"])
	}
	request := entries[1]["request"].(map[string]any)
	if request["name"] != "[REDACTED]" ||
		request["details"].(map[string]any)["note"] != "[REDACTED]" {
		t.Errorf("request group was not fully redacted: %#v", request)
	}
	if payment := entries[2]["public"].(map[string]any)["payment"].(map[string]any); payment["note"] != "[REDACTED]" {
		t.Errorf("payment note = %#v", payment["note"])
	}
	if public := entries[3]["public"].(map[string]any); public["name"] != "kept" {
		t.Errorf("safe group name = %#v", public["name"])
	}
}

func testIdentity() identity.Identity {
	return identity.Identity{
		ServiceNamespace: "insurance-hub",
		ServiceName:      "go-service",
		ServiceVersion:   "test-revision",
		ServiceInstance:  "test-instance",
		Environment:      "test",
		Implementation:   "go",
	}
}

func assertRedactedMap(
	t *testing.T,
	value any,
	redactedKey string,
	safeKey string,
	wantSafe any,
) {
	t.Helper()
	values, ok := value.(map[string]any)
	if !ok || values[redactedKey] != "[REDACTED]" || values[safeKey] != wantSafe {
		t.Errorf("unexpected sanitized map: %#v", value)
	}
}

func decodeEntries(t *testing.T, output string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode log entry %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}
