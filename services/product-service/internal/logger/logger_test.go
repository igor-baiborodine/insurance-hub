package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
)

func TestNewWritesJSONWithServiceIdentityAndLevelFiltering(t *testing.T) {
	// given
	var output bytes.Buffer
	log := logger.New(&output, "product-service", slog.LevelInfo)

	// when
	log.DebugContext(context.Background(), "filtered")
	log.InfoContext(context.Background(), "started", slog.String("component", "test"))

	// then
	entries := decodeEntries(t, output.String())
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry["service"] != "product-service" || entry["level"] != "INFO" ||
		entry["msg"] != "started" || entry["component"] != "test" {
		t.Errorf("unexpected log entry: %#v", entry)
	}
}

func TestNewAddsTraceCorrelationFromContext(t *testing.T) {
	// given
	traceID := trace.TraceID{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	spanID := trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	var output bytes.Buffer

	// when
	logger.New(&output, "product-service", slog.LevelInfo).InfoContext(ctx, "correlated")

	// then
	entries := decodeEntries(t, output.String())
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}
	if entries[0]["trace_id"] != traceID.String() || entries[0]["span_id"] != spanID.String() {
		t.Errorf("unexpected trace correlation: %#v", entries[0])
	}
}

func TestNewRedactsSensitiveAttributesAtEveryLevel(t *testing.T) {
	// given
	const privateMarker = "private-marker"
	var output bytes.Buffer
	log := logger.New(&output, "product-service", slog.LevelDebug).With(
		slog.String("accessToken", privateMarker),
	)
	levels := []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError}

	// when
	for _, level := range levels {
		log.Log(context.Background(), level, "safe message",
			slog.String("request_payload", privateMarker),
			slog.String("customerID", privateMarker),
			slog.Group("details", slog.String("password", privateMarker)),
		)
	}

	// then
	if strings.Contains(output.String(), privateMarker) {
		t.Fatalf("captured logs expose private marker: %s", output.String())
	}
	entries := decodeEntries(t, output.String())
	if len(entries) != len(levels) {
		t.Fatalf("entry count = %d, want %d", len(entries), len(levels))
	}
	for _, entry := range entries {
		if entry["service"] != "product-service" || entry["accessToken"] != "[REDACTED]" ||
			entry["request_payload"] != "[REDACTED]" ||
			entry["customerID"] != "[REDACTED]" {
			t.Errorf("unexpected redacted entry: %#v", entry)
		}
		details, ok := entry["details"].(map[string]any)
		if !ok || details["password"] != "[REDACTED]" {
			t.Errorf("unexpected nested redaction: %#v", entry["details"])
		}
	}
}

func TestNewRedactsSensitiveKeysInsideAnyValues(t *testing.T) {
	// given
	const privateMarker = "private-marker"
	type structuredDetails struct {
		AccessToken string           `json:"accessToken"`
		Profiles    []map[string]any `json:"profiles"`
		Safe        string           `json:"safe"`
	}
	var output bytes.Buffer
	log := logger.New(&output, "product-service", slog.LevelInfo).With(
		slog.Any("bound_details", map[string]any{
			"credential": privateMarker,
			"safe":       "bound-safe",
		}),
	)

	// when
	log.InfoContext(context.Background(), "structured",
		slog.Any("map_details", map[string]any{
			"password": privateMarker,
			"safe":     "map-safe",
		}),
		slog.Any("struct_details", structuredDetails{
			AccessToken: privateMarker,
			Profiles: []map[string]any{
				{"response_payload": privateMarker, "safe": "profile-safe"},
			},
			Safe: "struct-safe",
		}),
		slog.Any("list_details", []any{
			map[string]any{"customerData": privateMarker, "safe": "list-safe"},
		}),
		slog.Any("unsupported_details", map[string]any{
			"secret": privateMarker,
			"stream": make(chan int),
		}),
	)

	// then
	if strings.Contains(output.String(), privateMarker) {
		t.Fatalf("captured logs expose private marker: %s", output.String())
	}
	entries := decodeEntries(t, output.String())
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}
	entry := entries[0]
	assertRedactedMap(t, entry["bound_details"], "credential", "safe", "bound-safe")
	assertRedactedMap(t, entry["map_details"], "password", "safe", "map-safe")

	structDetails, ok := entry["struct_details"].(map[string]any)
	if !ok || structDetails["accessToken"] != "[REDACTED]" ||
		structDetails["safe"] != "struct-safe" {
		t.Fatalf("unexpected sanitized struct: %#v", entry["struct_details"])
	}
	profiles, ok := structDetails["profiles"].([]any)
	if !ok || len(profiles) != 1 {
		t.Fatalf("unexpected sanitized profiles: %#v", structDetails["profiles"])
	}
	assertRedactedMap(t, profiles[0], "response_payload", "safe", "profile-safe")

	listDetails, ok := entry["list_details"].([]any)
	if !ok || len(listDetails) != 1 {
		t.Fatalf("unexpected sanitized list: %#v", entry["list_details"])
	}
	assertRedactedMap(t, listDetails[0], "customerData", "safe", "list-safe")
	if entry["unsupported_details"] != "[REDACTED]" {
		t.Errorf(
			"unsupported structured value = %#v, want redacted",
			entry["unsupported_details"],
		)
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
