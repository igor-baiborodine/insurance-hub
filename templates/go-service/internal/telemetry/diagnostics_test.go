package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestExportDiagnostics_RateLimitsAndRedactsCollectorErrors(t *testing.T) {
	// given
	var output bytes.Buffer
	diagnostics := &exportDiagnostics{
		logger: slog.New(slog.NewJSONHandler(&output, nil)),
	}
	// when
	for range 100 {
		diagnostics.Handle(errors.New("private-collector-marker"))
	}
	// then
	if strings.Contains(output.String(), "private-collector-marker") {
		t.Fatalf("diagnostic disclosed collector text: %s", output.String())
	}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("diagnostic lines = %d, want one", len(lines))
	}
	var record map[string]any
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("decode diagnostic: %v", err)
	}
	if record["msg"] != "telemetry export failed" || record["level"] != "WARN" {
		t.Errorf("unexpected diagnostic: %#v", record)
	}
	if diagnostics.suppressed != 99 {
		t.Errorf("suppressed failures = %d, want 99", diagnostics.suppressed)
	}
	diagnostics.last = time.Now().Add(-diagnosticInterval)
	diagnostics.Handle(errors.New("another-private-marker"))
	lines = bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("diagnostic lines after interval = %d, want two", len(lines))
	}
	if err := json.Unmarshal(lines[1], &record); err != nil {
		t.Fatalf("decode second diagnostic: %v", err)
	}
	if record["suppressed_failures"] != float64(99) ||
		strings.Contains(output.String(), "another-private-marker") {
		t.Errorf("unsafe or incomplete suppressed-failure diagnostic: %#v", record)
	}
	diagnostics.Handle(errSpanQueueSaturated)
	lines = bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 3 || !bytes.Contains(lines[2], []byte("telemetry span queue saturated")) {
		t.Errorf("queue saturation warning missing: %s", output.String())
	}
}
