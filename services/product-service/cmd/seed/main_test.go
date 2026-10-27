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

func TestRun_RejectsUnenrolledOrIncompleteCommand(t *testing.T) {
	temporary := t.TempDir()
	targetPath := filepath.Join(temporary, "target.json")
	urlPath := filepath.Join(temporary, "database-url")
	caPath := filepath.Join(temporary, "ca.crt")
	catalogPath := filepath.Join(temporary, "catalog.json")
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
		SeedAllowed:      true,
		Disposable:       true,
	}
	writeTarget := func(selected administration.Target) {
		t.Helper()
		content, err := json.Marshal(selected)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(targetPath, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeTarget(target)
	databaseURL := "postgres://go_product_seeder:synthetic@localhost:1/product?" +
		"sslmode=verify-full&sslrootcert=" + caPath
	if err := os.WriteFile(urlPath, []byte(databaseURL), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		catalogPath,
		[]byte(`{"not":"opened-before-connect"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	valid := []string{
		"--environment=disposable",
		"--context=owned-test",
		"--target-file=" + targetPath,
		"--catalog-file=" + catalogPath,
		"--database-url-file=" + urlPath,
		"--ca-file=" + caPath,
	}
	for _, scenario := range []struct {
		name string
		args []string
		want string
	}{
		{"extra operand", append(append([]string{}, valid...), "extra"), "invalid seed command arguments"},
		{"missing enrollment", nil, "target enrollment path is required"},
		{"missing catalog file", valid[:3], "product seed catalog file is required"},
		{"missing URL file", valid[:4], "database URL file is required"},
		{"wrong context", []string{valid[0], "--context=other", valid[2], valid[3], valid[4], valid[5]}, "target environment or context"},
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

	production := target
	production.Environment = "production"
	production.Context = "production-test"
	production.SeedAllowed = false
	production.Disposable = false
	production.Host = production.Cluster + "-rw." + production.DataNamespace + ".svc.cluster.local"
	writeTarget(production)
	var output strings.Builder
	err := run(context.Background(), []string{
		"--environment=production",
		"--context=production-test",
		"--target-file=" + targetPath,
		"--catalog-file=" + catalogPath,
		"--database-url-file=" + urlPath,
		"--ca-file=" + caPath,
	}, &output)
	if err == nil || !strings.Contains(err.Error(), "does not allow seeding") ||
		output.Len() != 0 {
		t.Fatalf("production seed was accepted: %v, %q", err, output.String())
	}

	writeTarget(target)
	if err := os.WriteFile(caPath, []byte("synthetic-test-ca"), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	called := false
	if err := runWith(
		context.Background(),
		valid,
		&output,
		func(
			_ context.Context,
			actualURL string,
			actualTarget administration.Target,
			actualCatalogPath string,
		) (administration.SeedResult, error) {
			called = true
			if actualURL != databaseURL || actualTarget != target ||
				actualCatalogPath != catalogPath {
				t.Fatalf(
					"seed composition inputs = %q, %+v, %q",
					actualURL,
					actualTarget,
					actualCatalogPath,
				)
			}
			return administration.SeedResult{Inserted: 2, Existing: 3}, nil
		},
	); err != nil {
		t.Fatalf("delegated seed failed: %v", err)
	}
	if !called || output.String() != "inserted=2 existing=3\n" {
		t.Fatalf("seed delegation = called %t, output %q", called, output.String())
	}
	output.Reset()
	if err := run(context.Background(), valid, &output); err == nil || output.Len() != 0 {
		t.Fatalf("unreachable database was accepted: %v", err)
	}
}
