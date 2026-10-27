package administration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTarget_ValidateMigrationConnection(t *testing.T) {
	target := Target{
		Environment:      "qa",
		Context:          "qa-insurance-hub",
		DataNamespace:    "qa-data",
		ServiceNamespace: "qa-svc",
		Cluster:          "qa-postgres-product",
		ClusterUID:       "synthetic-cluster-uid",
		InstanceID:       "00000000-0000-4000-8000-000000000190",
		Database:         "product",
		Host:             "qa-postgres-product-rw.qa-data.svc.cluster.local",
		SeedAllowed:      true,
	}
	url := "postgres://go_product_migrator:synthetic@" + target.Host +
		"/product?sslmode=verify-full&sslrootcert=/synthetic/ca.crt"
	if err := target.ValidateMigrationConnection(
		"qa",
		"qa-insurance-hub",
		url,
		"/synthetic/ca.crt",
	); err != nil {
		t.Fatal(err)
	}
	if err := target.ValidateMigrationConnection(
		"qa",
		"qa-insurance-hub",
		strings.Replace(
			url,
			"postgres://",
			"postgresql://",
			1,
		),
		"/synthetic/ca.crt",
	); err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string]string{
		"wrong host":     "postgres://go_product_migrator:synthetic@other/product?sslmode=verify-full&sslrootcert=/synthetic/ca.crt",
		"wrong login":    "postgres://product:synthetic@" + target.Host + "/product?sslmode=verify-full&sslrootcert=/synthetic/ca.crt",
		"no TLS":         "postgres://go_product_migrator:synthetic@" + target.Host + "/product?sslmode=disable&sslrootcert=/synthetic/ca.crt",
		"extra option":   url + "&search_path=public",
		"empty password": "postgres://go_product_migrator:@" + target.Host + "/product?sslmode=verify-full&sslrootcert=/synthetic/ca.crt",
		"wrong port":     url[:strings.Index(url, "/product?")] + ":7777/product?sslmode=verify-full&sslrootcert=/synthetic/ca.crt",
	} {
		t.Run(name, func(t *testing.T) {
			if err := target.ValidateMigrationConnection(
				"qa",
				"qa-insurance-hub",
				candidate,
				"/synthetic/ca.crt",
			); err == nil {
				t.Fatal("untrusted target accepted")
			}
		})
	}
	if err := target.ValidateMigrationConnection(
		"production",
		"qa-insurance-hub",
		url,
		"/synthetic/ca.crt",
	); err == nil {
		t.Fatal("wrong environment accepted")
	}
	target.InstanceID = "not-a-uuid"
	if err := target.ValidateMigrationConnection(
		"qa",
		"qa-insurance-hub",
		url,
		"/synthetic/ca.crt",
	); err == nil {
		t.Fatal("invalid database-instance UUID accepted")
	}
	target.InstanceID = "00000000-0000-4000-8000-000000000190"
	target.Cluster = "other-cluster"
	if err := target.ValidateMigrationConnection(
		"qa",
		"qa-insurance-hub",
		url,
		"/synthetic/ca.crt",
	); err == nil {
		t.Fatal("wrong enrolled cluster accepted")
	}
	target.Cluster = "qa-postgres-product"
	target.SeedAllowed = false
	target.Environment = "production"
	target.Context = "synthetic-production"
	if err := target.ValidateMigrationConnection(
		"production",
		target.Context,
		url,
		"/synthetic/ca.crt",
	); err != nil {
		t.Fatalf("enrolled production migration was rejected: %v", err)
	}
	target.SeedAllowed = true
	if err := target.ValidateMigrationConnection(
		"production",
		target.Context,
		url,
		"/synthetic/ca.crt",
	); err == nil {
		t.Fatal("production seeding policy was accepted")
	}
	target.SeedAllowed = false
	target.Environment = "disposable"
	if err := target.ValidateMigrationConnection(
		"disposable",
		target.Context,
		url,
		"/synthetic/ca.crt",
	); err == nil {
		t.Fatal("disposable environment without owned classification accepted")
	}
	target.Environment = "qa"
	target.Context = "qa-insurance-hub"
	target.ClusterUID = ""
	if err := target.ValidateMigrationConnection(
		"qa",
		target.Context,
		url,
		"/synthetic/ca.crt",
	); err == nil {
		t.Fatal("incomplete enrollment accepted")
	}
}

func TestReadTargetRejectsUnknownAndTrailingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "target.json")
	for _, content := range []string{
		`{"environment":"qa","unexpected":true}`,
		`{"environment":"qa"}{"environment":"production"}`,
		`{"environment":"qa"}`,
		`not-json`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadTarget(path); err == nil {
			t.Fatal("malformed enrollment accepted")
		}
	}
	if _, err := ReadTarget(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("missing enrollment accepted")
	}
	valid, err := json.Marshal(Target{Environment: "qa"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	if target, err := ReadTarget(path); err != nil || target.Environment != "qa" {
		t.Fatalf("valid enrollment could not be read: %v", err)
	}
}

func TestTarget_ValidateSeedConnectionRequiresApprovedSeeder(t *testing.T) {
	target := Target{
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
	url := "postgres://go_product_seeder:synthetic@localhost:5432/product?" +
		"sslmode=verify-full&sslrootcert=/synthetic/ca.crt"
	if err := target.ValidateSeedConnection(
		"disposable",
		"owned-test",
		url,
		"/synthetic/ca.crt",
	); err != nil {
		t.Fatal(err)
	}
	staging := target
	staging.Environment = "staging"
	staging.Context = "staging-insurance-hub"
	staging.DataNamespace = "staging-data"
	staging.ServiceNamespace = "staging-svc"
	staging.Cluster = "staging-postgres-product"
	staging.Host = "staging-postgres-product-rw.staging-data.svc.cluster.local"
	staging.Disposable = false
	stagingURL := "postgres://go_product_seeder:synthetic@" + staging.Host +
		"/product?sslmode=verify-full&sslrootcert=/synthetic/ca.crt"
	if err := staging.ValidateSeedConnection(
		"staging",
		"staging-insurance-hub",
		stagingURL,
		"/synthetic/ca.crt",
	); err != nil {
		t.Fatalf("new enrolled non-production environment was rejected: %v", err)
	}
	for _, scenario := range []struct {
		name   string
		target Target
		url    string
	}{
		{"migrator credential", target, strings.Replace(url, "go_product_seeder", "go_product_migrator", 1)},
		{"seed disabled", func() Target { candidate := target; candidate.SeedAllowed = false; return candidate }(), url},
		{"production", func() Target {
			candidate := target
			candidate.Environment = "production"
			candidate.Disposable = false
			return candidate
		}(), url},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if err := scenario.target.ValidateSeedConnection(
				scenario.target.Environment,
				scenario.target.Context,
				scenario.url,
				"/synthetic/ca.crt",
			); err == nil {
				t.Fatal("unapproved seed target or credential was accepted")
			}
		})
	}
}
