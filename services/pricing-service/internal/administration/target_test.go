package administration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadTarget_ValidatesProtectedEnrollment(t *testing.T) {
	// given
	target := qaTestTarget()
	content, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, requestedContext, content, diagnostic string
		mode                                        os.FileMode
	}{
		{"approved target", target.Context, string(content), "", 0o600},
		{"wrong context", "other-context", string(content), "does not match enrollment", 0o600},
		{
			"wrong namespace", target.Context, strings.Replace(string(content), "qa-data", "other-data", 1),
			"coordinates differ from environment policy", 0o600,
		},
		{"trailing document", target.Context, string(content) + "{}", "invalid target enrollment", 0o600},
		{"writable enrollment", target.Context, string(content), "protected regular file", 0o622},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			path := writeTargetTestFile(t, "target.json", test.content)
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}

			// when
			got, err := ReadTarget(path, "qa", test.requestedContext)

			// then
			if test.diagnostic == "" {
				if err != nil || !reflect.DeepEqual(got, target) {
					t.Fatalf(
						"expected approved enrollment, got %+v (%v)",
						got,
						err,
					)
				}
			} else if got != (Target{}) || err == nil || !strings.Contains(err.Error(), test.diagnostic) {
				t.Fatalf(
					"expected enrollment refusal %q, got %v",
					test.diagnostic,
					err,
				)
			}
		})
	}
}

func TestTarget_RequiresTheEnrolledRoleAndTLSSettings(t *testing.T) {
	// given
	target := qaTestTarget()
	ca := writeTargetTestFile(t, "ca.crt", "synthetic trust root")
	migrator := "postgresql://go_pricing_migrator:synthetic-password@" + target.Host +
		":5432/pricing?sslmode=verify-full&sslrootcert=" + ca
	seeder := strings.Replace(migrator, "go_pricing_migrator", "go_pricing_seeder", 1)
	for _, test := range []struct {
		name, input, diagnostic string
		connect                 func(string, string) (string, []byte, error)
	}{
		{"approved migrator", migrator, "", target.MigrationConnection},
		{"approved seeder", seeder, "", target.SeedConnection},
		{"migrator used as seeder", migrator, "seeder target", target.SeedConnection},
		{
			"wrong host", strings.Replace(migrator, target.Host, "other-host", 1),
			"migrator target", target.MigrationConnection,
		},
		{
			"wrong port", strings.Replace(migrator, ":5432/", ":5433/", 1),
			"port differs", target.MigrationConnection,
		},
		{
			"tls downgrade", strings.Replace(migrator, "verify-full", "require", 1),
			"selected CA and TLS verify-full", target.MigrationConnection,
		},
		{
			"foreign trust root", strings.Replace(migrator, ca, "/elsewhere/ca.crt", 1),
			"selected CA and TLS verify-full", target.MigrationConnection,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			path := writeTargetTestFile(t, "database-url", test.input+"\n")

			// when
			url, trust, err := test.connect(path, ca)

			// then
			if test.diagnostic == "" {
				if err != nil || url != test.input ||
					string(trust) != "synthetic trust root" {
					t.Fatalf("expected enrolled credential and CA, got %v", err)
				}
			} else if url != "" || trust != nil || err == nil ||
				!strings.Contains(err.Error(), test.diagnostic) ||
				strings.Contains(err.Error(), "synthetic-password") {
				t.Fatalf(
					"expected redacted credential refusal %q, got %v",
					test.diagnostic,
					err,
				)
			}
		})
	}
}

func TestTarget_SeedConnectionRejectsDisabledEnrollment(t *testing.T) {
	// given
	target := qaTestTarget()
	*target.SeedAllowed = false

	// when
	url, ca, err := target.SeedConnection("", "")

	// then
	if url != "" || ca != nil || err == nil ||
		!strings.Contains(err.Error(), "seeding is disabled") {
		t.Fatalf("expected seed approval refusal before reading credentials, got %v", err)
	}
}

func qaTestTarget() Target {
	allowed, disposable := true, false
	return Target{
		Environment:      "qa",
		Context:          "qa-insurance-hub",
		DataNamespace:    "qa-data",
		ServiceNamespace: "qa-svc",
		Cluster:          "qa-postgres-pricing",
		ClusterUID:       "10000000-0000-4000-8000-000000000001",
		InstanceID:       "20000000-0000-4000-8000-000000000002",
		Database:         "pricing",
		Host:             "qa-postgres-pricing-rw.qa-data.svc.cluster.local",
		SeedAllowed:      &allowed,
		Disposable:       &disposable,
	}
}

func writeTargetTestFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
