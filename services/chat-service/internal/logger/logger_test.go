package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	kitlogger "github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"
	"go.opentelemetry.io/otel/trace"

	chatlogger "github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/logger"
)

func TestLifecycleLogs_AreRequiredStructuredAndPreserveAvailableCorrelation(t *testing.T) {
	// given
	var output bytes.Buffer
	log := kitlogger.New(&output, "chat-service", slog.LevelError)
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

	// when
	chatlogger.LogHandshake(
		ctx,
		log,
		slog.LevelInfo,
		slog.String("outcome", "accepted"),
		slog.Float64("duration_ms", 1.25),
	)
	chatlogger.LogConnectionOpened(log)
	chatlogger.LogConnectionClosed(log, slog.String("reason", "normal"))
	chatlogger.LogBroadcastFailure(
		log,
		slog.String("event", "message"),
		slog.String("outcome", "queue_full"),
	)
	chatlogger.LogShutdownFailure(context.Background(), log)

	// then
	entries := decodeEntries(t, output.String())
	wantMessages := []string{
		"chat WebSocket handshake completed",
		"chat WebSocket connection opened",
		"chat WebSocket connection closed",
		"chat broadcast delivery failed",
		"chat service shutdown failed",
	}
	if len(entries) != len(wantMessages) {
		t.Fatalf("log entry count = %d, want %d", len(entries), len(wantMessages))
	}
	for index, entry := range entries {
		if entry["msg"] != wantMessages[index] || entry["service"] != "chat-service" {
			t.Errorf("log entry %d = %#v", index, entry)
		}
	}
	if entries[0]["trace_id"] != traceID.String() || entries[0]["span_id"] != spanID.String() {
		t.Errorf("handshake trace correlation = %#v", entries[0])
	}
	for index, entry := range entries[1:] {
		if _, found := entry["trace_id"]; found {
			t.Errorf("background log entry %d included trace_id: %#v", index+1, entry)
		}
		if _, found := entry["span_id"]; found {
			t.Errorf("background log entry %d included span_id: %#v", index+1, entry)
		}
	}
	if entries[0]["outcome"] != "accepted" || entries[0]["duration_ms"] != 1.25 ||
		entries[2]["reason"] != "normal" || entries[3]["event"] != "message" ||
		entries[3]["outcome"] != "queue_full" {
		t.Errorf("structured lifecycle fields = %#v", entries)
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
