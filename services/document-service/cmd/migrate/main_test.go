package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/administration"
)

func TestRun_ValidatesBeforeDialAndRedactsFailures(t *testing.T) {
	// given
	root := t.TempDir()
	ca := filepath.Join(root, "ca.crt")
	targetFile := filepath.Join(root, "target.json")
	credential := filepath.Join(root, "url")
	target := administration.Target{
		Environment:      "qa",
		Context:          "qa-insurance-hub",
		DataNamespace:    "qa-data",
		ServiceNamespace: "qa-svc",
		Cluster:          "qa-postgres-document",
		ClusterUID:       "fixture-cluster",
		InstanceID:       "00000000-0000-4000-8000-000000000362",
		Database:         "document",
		Host:             "qa-postgres-document-rw.qa-data.svc.cluster.local",
	}
	body, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(ca, "synthetic trust")
	write(targetFile, string(body))
	url := "postgresql://go_document_migrator:PASSWORD-SENTINEL-362@" + target.Host + "/document?sslmode=verify-full&sslrootcert=" + ca
	write(credential, url)
	args := []string{
		"--operation",
		"migrate",
		"--environment",
		"qa",
		"--context",
		"qa-insurance-hub",
		"--target-file",
		targetFile,
		"--database-url-file",
		credential,
		"--ca-file",
		ca,
	}
	called := false
	fake := func(context.Context, string, administration.Target, string, time.Duration) (string, error) {
		called = true
		return "", errors.New("PASSWORD-SENTINEL-362 raw SQL upstream error")
	}
	var output, diagnostics bytes.Buffer
	// when
	err = runWith(t.Context(), args, &output, &diagnostics, fake)
	// then
	if err == nil || !called {
		t.Fatal("valid migration did not delegate")
	}
	if strings.Contains(diagnostics.String(), "PASSWORD-SENTINEL") ||
		strings.Contains(diagnostics.String(), "raw SQL") {
		t.Fatal("sensitive failure leaked")
	}
	if !strings.Contains(diagnostics.String(), "operation_failed") {
		t.Fatal("safe failure classification missing")
	}
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(diagnostics.Bytes()), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["service"] != "document-migrate" || entry["error"] != "[REDACTED]" ||
		!strings.HasSuffix(entry["time"].(string), "Z") {
		t.Fatalf("common logging contract missing: %#v", entry)
	}
	called = false
	write(credential, strings.Replace(url, "verify-full", "disable", 1))
	if err := runWith(t.Context(), args, &output, &diagnostics, fake); err == nil || called {
		t.Fatal("invalid TLS reached database operation")
	}
}
