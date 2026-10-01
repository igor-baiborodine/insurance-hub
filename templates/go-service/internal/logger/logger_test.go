package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/logger"
	"go.opentelemetry.io/otel/trace"
)

func TestNewWritesJSONWithServiceIdentityAndLevelFiltering(t *testing.T) {
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

func TestNewAddsTraceCorrelationFromContext(t *testing.T) {
	traceID := trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	spanID := trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID: spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	var output bytes.Buffer

	logger.New(&output, "go-service", slog.LevelInfo).InfoContext(ctx, "correlated")

	entries := decodeEntries(t, output.String())
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}
	if entries[0]["trace_id"] != traceID.String() || entries[0]["span_id"] != spanID.String() {
		t.Errorf("unexpected trace correlation: %#v", entries[0])
	}
}

func TestNewRedactsSensitiveAttributesAtEveryLevel(t *testing.T) {
	const privateMarker = "private-marker"
	var output bytes.Buffer
	log := logger.New(&output, "go-service", slog.LevelDebug).With(
		slog.String("accessToken", privateMarker),
	)
	levels := []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError}
	for _, level := range levels {
		log.Log(context.Background(), level, "safe message",
			slog.String("request_payload", privateMarker),
			slog.String("customerID", privateMarker),
			slog.Group("details", slog.String("password", privateMarker)),
		)
	}

	if strings.Contains(output.String(), privateMarker) {
		t.Fatalf("captured logs expose private marker: %s", output.String())
	}
	entries := decodeEntries(t, output.String())
	if len(entries) != len(levels) {
		t.Fatalf("entry count = %d, want %d", len(entries), len(levels))
	}
	for _, entry := range entries {
		if entry["service"] != "go-service" || entry["accessToken"] != "[REDACTED]" ||
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
