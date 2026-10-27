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

type migrationCommandFixture struct {
	target        administration.Target
	targetPath    string
	badTargetPath string
	urlPath       string
	caPath        string
	databaseURL   string
	valid         []string
}

func newMigrationCommandFixture(t *testing.T) migrationCommandFixture {
	t.Helper()
	temporary := t.TempDir()
	fixture := migrationCommandFixture{
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
			Disposable:       true,
		},
		targetPath:    filepath.Join(temporary, "target.json"),
		badTargetPath: filepath.Join(temporary, "bad-target.json"),
		urlPath:       filepath.Join(temporary, "database-url"),
		caPath:        filepath.Join(temporary, "ca.crt"),
	}
	content, err := json.Marshal(fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.targetPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		fixture.badTargetPath,
		[]byte(`{"unexpected":true}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	fixture.databaseURL = "postgres://go_product_migrator:synthetic@localhost:1/product?" +
		"sslmode=verify-full&sslrootcert=" + fixture.caPath
	if err := os.WriteFile(fixture.urlPath, []byte(fixture.databaseURL), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.valid = []string{
		"--operation=status",
		"--environment=disposable",
		"--context=owned-test",
		"--target-file=" + fixture.targetPath,
		"--database-url-file=" + fixture.urlPath,
		"--ca-file=" + fixture.caPath,
	}
	return fixture
}

func (fixture migrationCommandFixture) writeCA(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(fixture.caPath, []byte("synthetic-test-ca"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRun_RejectsInvalidOrUnenrolledCommand(t *testing.T) {
	// given
	fixture := newMigrationCommandFixture(t)
	for _, scenario := range []struct {
		name string
		args []string
		want string
	}{
		{"missing operation", fixture.valid[1:], "operation must"},
		{"unknown operation", append([]string{"--operation=down"}, fixture.valid[1:]...), "operation must"},
		{"extra operand", append(append([]string{}, fixture.valid...), "extra"), "invalid migration command arguments"},
		{"missing enrollment", []string{"--operation=status"}, "target enrollment path is required"},
		{"invalid enrollment", []string{fixture.valid[0], fixture.valid[1], fixture.valid[2], "--target-file=" + fixture.badTargetPath}, "decode target enrollment"},
		{"missing URL file", []string{fixture.valid[0], fixture.valid[1], fixture.valid[2], fixture.valid[3]}, "database URL file is required"},
		{"unreadable URL file", []string{fixture.valid[0], fixture.valid[1], fixture.valid[2], fixture.valid[3], "--database-url-file=" + filepath.Join(t.TempDir(), "absent")}, "cannot read migrator credential"},
		{"wrong context", []string{fixture.valid[0], fixture.valid[1], "--context=other", fixture.valid[3], fixture.valid[4], fixture.valid[5]}, "target environment or context"},
		{"missing CA", fixture.valid, "selected CNPG CA file is unavailable"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var output strings.Builder

			// when
			err := run(context.Background(), scenario.args, &output)

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
}

func TestRun_DelegatesValidMigrationCommand(t *testing.T) {
	// given
	fixture := newMigrationCommandFixture(t)
	fixture.writeCA(t)
	var output strings.Builder
	called := false

	// when
	err := runWith(
		context.Background(),
		fixture.valid,
		&output,
		func(
			_ context.Context,
			actualURL string,
			actualTarget administration.Target,
			actualOperation string,
		) (string, error) {
			called = true
			if actualURL != fixture.databaseURL || actualTarget != fixture.target ||
				actualOperation != "status" {
				t.Fatalf(
					"migration composition inputs = %q, %+v, %q",
					actualURL,
					actualTarget,
					actualOperation,
				)
			}
			return "version=1", nil
		},
	)
	// then
	if err != nil {
		t.Fatalf("delegated migration failed: %v", err)
	}
	if !called || output.String() != "version=1\n" {
		t.Fatalf("migration delegation = called %t, output %q", called, output.String())
	}
}

func TestRun_ReturnsDatabaseConnectionFailure(t *testing.T) {
	// given
	fixture := newMigrationCommandFixture(t)
	fixture.writeCA(t)
	var output strings.Builder

	// when
	err := run(context.Background(), fixture.valid, &output)

	// then
	if err == nil || output.Len() != 0 {
		t.Fatalf("unreachable database was accepted: %v", err)
	}
}
