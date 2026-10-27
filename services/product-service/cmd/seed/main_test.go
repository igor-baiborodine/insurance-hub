package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/administration"
)

type seedCommandFixture struct {
	target      administration.Target
	targetPath  string
	urlPath     string
	caPath      string
	catalogPath string
	databaseURL string
	valid       []string
}

func newSeedCommandFixture(t *testing.T) seedCommandFixture {
	t.Helper()
	temporary := t.TempDir()
	fixture := seedCommandFixture{
		target: administration.Target{
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
		},
		targetPath:  filepath.Join(temporary, "target.json"),
		urlPath:     filepath.Join(temporary, "database-url"),
		caPath:      filepath.Join(temporary, "ca.crt"),
		catalogPath: filepath.Join(temporary, "catalog.json"),
	}
	fixture.writeTarget(t, fixture.target)
	fixture.databaseURL = "postgres://go_product_seeder:synthetic@localhost:1/product?" +
		"sslmode=verify-full&sslrootcert=" + fixture.caPath
	if err := os.WriteFile(fixture.urlPath, []byte(fixture.databaseURL), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		fixture.catalogPath,
		[]byte(`{"not":"opened-before-connect"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	fixture.valid = []string{
		"--environment=disposable",
		"--context=owned-test",
		"--target-file=" + fixture.targetPath,
		"--catalog-file=" + fixture.catalogPath,
		"--database-url-file=" + fixture.urlPath,
		"--ca-file=" + fixture.caPath,
	}
	return fixture
}

func (fixture seedCommandFixture) writeTarget(t *testing.T, target administration.Target) {
	t.Helper()
	content, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.targetPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture seedCommandFixture) writeCA(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(fixture.caPath, []byte("synthetic-test-ca"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRun_RejectsInvalidOrUnapprovedCommand(t *testing.T) {
	// given
	fixture := newSeedCommandFixture(t)
	for _, scenario := range []struct {
		name string
		args []string
		want string
	}{
		{"extra operand", append(append([]string{}, fixture.valid...), "extra"), "invalid seed command arguments"},
		{"missing enrollment", nil, "target enrollment path is required"},
		{"missing catalog file", fixture.valid[:3], "product seed catalog file is required"},
		{"missing URL file", fixture.valid[:4], "database URL file is required"},
		{"wrong context", []string{fixture.valid[0], "--context=other", fixture.valid[2], fixture.valid[3], fixture.valid[4], fixture.valid[5]}, "target environment or context"},
		{"missing CA", fixture.valid, "selected CNPG CA file is unavailable"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var output strings.Builder

			// when
			err := run(context.Background(), scenario.args, &output, io.Discard)

			// then
			if err == nil || !strings.Contains(err.Error(), scenario.want) ||
				output.Len() != 0 {
				t.Fatalf("unexpected command outcome: %v, %q", err, output.String())
			}
			if strings.Contains(err.Error(), "synthetic@") {
				t.Fatal("credential leaked in diagnostic")
			}
		})
	}

	production := fixture.target
	production.Environment = "production"
	production.Context = "production-test"
	production.SeedAllowed = false
	production.Disposable = false
	production.Host = production.Cluster + "-rw." + production.DataNamespace + ".svc.cluster.local"
	fixture.writeTarget(t, production)
	var output strings.Builder

	// when
	err := run(context.Background(), []string{
		"--environment=production",
		"--context=production-test",
		"--target-file=" + fixture.targetPath,
		"--catalog-file=" + fixture.catalogPath,
		"--database-url-file=" + fixture.urlPath,
		"--ca-file=" + fixture.caPath,
	}, &output, io.Discard)

	// then
	if err == nil || !strings.Contains(err.Error(), "does not allow seeding") ||
		output.Len() != 0 {
		t.Fatalf("production seed was accepted: %v, %q", err, output.String())
	}
}

func TestRun_DelegatesValidSeedCommand(t *testing.T) {
	// given
	fixture := newSeedCommandFixture(t)
	fixture.writeCA(t)
	var output strings.Builder
	called := false

	// when
	err := runWith(
		context.Background(),
		fixture.valid,
		&output,
		io.Discard,
		func(
			_ context.Context,
			actualURL string,
			actualTarget administration.Target,
			actualCatalogPath string,
		) (administration.SeedResult, error) {
			called = true
			if actualURL != fixture.databaseURL || actualTarget != fixture.target ||
				actualCatalogPath != fixture.catalogPath {
				t.Fatalf(
					"seed composition inputs = %q, %+v, %q",
					actualURL,
					actualTarget,
					actualCatalogPath,
				)
			}
			return administration.SeedResult{Inserted: 2, Existing: 3}, nil
		},
	)
	// then
	if err != nil {
		t.Fatalf("delegated seed failed: %v", err)
	}
	if !called || output.String() != "inserted=2 existing=3\n" {
		t.Fatalf("seed delegation = called %t, output %q", called, output.String())
	}
}

func TestRun_ReturnsDatabaseConnectionFailure(t *testing.T) {
	// given
	fixture := newSeedCommandFixture(t)
	fixture.writeCA(t)
	var output strings.Builder

	// when
	err := run(context.Background(), fixture.valid, &output, io.Discard)

	// then
	if err == nil || output.Len() != 0 {
		t.Fatalf("unreachable database was accepted: %v", err)
	}
}

func TestRun_LogsSafeStructuredFailures(t *testing.T) {
	// given
	fixture := newSeedCommandFixture(t)
	fixture.writeCA(t)
	for _, scenario := range []struct {
		name string
		err  error
		kind string
	}{
		{"secret", errors.New("postgres://secret-sentinel:password@private-host/product SELECT private_payload"), "operation_failed"},
		{"signal", context.Canceled, "cancelled"},
		{"deadline", context.DeadlineExceeded, "deadline_exceeded"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var output, diagnostics strings.Builder
			// when
			err := runWith(
				context.Background(),
				fixture.valid,
				&output,
				&diagnostics,
				func(context.Context, string, administration.Target, string) (administration.SeedResult, error) {
					return administration.SeedResult{}, scenario.err
				},
			)
			// then
			var record map[string]any
			if !errors.Is(err, scenario.err) || output.Len() != 0 ||
				json.Unmarshal([]byte(diagnostics.String()), &record) != nil {
				t.Fatalf("invalid failure output: %v", err)
			}
			if record["service"] != "product-seed" || record["operation"] != "seed" ||
				record["environment"] != "disposable" ||
				record["stage"] != "database_operation" ||
				record["error_kind"] != scenario.kind ||
				record["error"] != "[REDACTED]" {
				t.Fatalf("unexpected diagnostic: %v", record)
			}
			for _, secret := range []string{"secret-sentinel", "password@", "private-host", "private_payload"} {
				if strings.Contains(diagnostics.String(), secret) {
					t.Fatal("sensitive error content was logged")
				}
			}
		})
	}
}
