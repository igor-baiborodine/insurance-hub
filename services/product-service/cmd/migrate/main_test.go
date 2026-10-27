package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/administration"
)

func TestRunRejectsUnenrolledOrIncompleteCommand(t *testing.T) {
	temporary := t.TempDir()
	targetPath := filepath.Join(temporary, "target.json")
	urlPath := filepath.Join(temporary, "database-url")
	badTargetPath := filepath.Join(temporary, "bad-target.json")
	caPath := filepath.Join(temporary, "ca.crt")
	target := administration.Target{
		Environment:      "disposable",
		Context:          "owned-test",
		DataNamespace:    "owned",
		ServiceNamespace: "owned",
		Cluster:          "owned-product",
		ClusterUID:       "owned-cluster-uid",
		InstanceID:       "00000000-0000-4000-8000-000000000190",
		Database:         "product",
		Host:             "localhost",
		Disposable:       true,
	}
	content, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badTargetPath, []byte(`{"unexpected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	databaseURL := "postgres://go_product_migrator:synthetic@localhost:1/product?" +
		"sslmode=verify-full&sslrootcert=" + caPath
	if err := os.WriteFile(urlPath, []byte(databaseURL), 0o600); err != nil {
		t.Fatal(err)
	}
	valid := []string{
		"--operation=status",
		"--environment=disposable",
		"--context=owned-test",
		"--target-file=" + targetPath,
		"--database-url-file=" + urlPath,
		"--ca-file=" + caPath,
	}
	for _, scenario := range []struct {
		name string
		args []string
		want string
	}{
		{"missing operation", valid[1:], "operation must"},
		{"unknown operation", append([]string{"--operation=down"}, valid[1:]...), "operation must"},
		{"extra operand", append(append([]string{}, valid...), "extra"), "invalid migration command arguments"},
		{"missing enrollment", []string{"--operation=status"}, "target enrollment path is required"},
		{"invalid enrollment", []string{valid[0], valid[1], valid[2], "--target-file=" + badTargetPath}, "decode target enrollment"},
		{"missing URL file", []string{valid[0], valid[1], valid[2], valid[3]}, "database URL file is required"},
		{"unreadable URL file", []string{valid[0], valid[1], valid[2], valid[3], "--database-url-file=" + filepath.Join(temporary, "absent")}, "cannot read migrator credential"},
		{"wrong context", []string{valid[0], valid[1], "--context=other", valid[3], valid[4], valid[5]}, "target environment or context"},
		{"missing CA", valid, "selected CNPG CA file is unavailable"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var output strings.Builder
			err := run(context.Background(), scenario.args, &output)
			if err == nil || !strings.Contains(err.Error(), scenario.want) ||
				output.Len() != 0 {
				t.Fatalf("unexpected command outcome: %v, %q", err, output.String())
			}
			if strings.Contains(err.Error(), "synthetic@") {
				t.Fatal("credential leaked in diagnostic")
			}
		})
	}
	if err := os.WriteFile(caPath, []byte("synthetic-test-ca"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := run(context.Background(), valid, &output); err == nil || output.Len() != 0 {
		t.Fatalf("unreachable database was accepted: %v", err)
	}
}
