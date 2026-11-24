package administration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTarget_ValidatesProtectedEnrollment(t *testing.T) {
	// given
	content := localDevTargetJSON(t)
	for _, test := range []struct {
		name, context, content, diagnostic string
		mode                               os.FileMode
	}{
		{"approved target", "kind-local-dev-insurance-hub", content, "", 0o600},
		{"wrong context", "other-context", content, "does not match enrollment", 0o600},
		{
			"wrong cluster", "kind-local-dev-insurance-hub",
			strings.ReplaceAll(content, "local-dev-postgres-policy", "local-dev-postgres-pricing"),
			"coordinates differ from environment policy", 0o600,
		},
		{
			"unknown field", "kind-local-dev-insurance-hub",
			strings.Replace(content, "{", `{"seedAllowed":true,`, 1),
			"invalid target enrollment", 0o600,
		},
		{"readable enrollment", "kind-local-dev-insurance-hub", content, "private regular file", 0o644},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			path := writeTestFile(t, "target.json", test.content, test.mode)

			// when
			got, err := ReadTarget(path, "local-dev", test.context)

			// then
			if test.diagnostic == "" {
				if err != nil ||
					got.Host != "local-dev-postgres-policy-rw.local-dev-all.svc.cluster.local" {
					t.Fatalf(
						"expected approved enrollment, got %+v (%v)",
						got,
						err,
					)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.diagnostic) {
				t.Fatalf("expected refusal %q, got %v", test.diagnostic, err)
			}
		})
	}
}

func TestMigrationConnection_RequiresVerifiedMigratorURL(t *testing.T) {
	// given
	path := writeTestFile(t, "target.json", localDevTargetJSON(t), 0o600)
	target, err := ReadTarget(path, "local-dev", "kind-local-dev-insurance-hub")
	if err != nil {
		t.Fatal(err)
	}
	ca := writeTestFile(t, "ca.crt", "certificate", 0o600)
	base := "postgresql://go_policy_migrator:secret@" + target.Host + ":5432/policy" +
		"?sslmode=verify-full&sslrootcert=" + ca
	for _, test := range []struct{ name, url, diagnostic string }{
		{"direct connection", base, ""},
		{"loopback tunnel", base + "&hostaddr=127.0.0.1&port=15432", ""},
		{"writer credential", strings.Replace(base, "go_policy_migrator", writerRole, 1), "does not match"},
		{"relaxed TLS", strings.Replace(base, "verify-full", "require", 1), "TLS verify-full"},
		{"remote tunnel", base + "&hostaddr=10.0.0.1&port=15432", "loopback"},
		{"privileged tunnel port", base + "&hostaddr=127.0.0.1&port=80", "loopback"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			credential := writeTestFile(t, "database-url", test.url, 0o600)

			// when
			got, gotCA, err := target.MigrationConnection(credential, ca)

			// then
			if test.diagnostic == "" {
				if err != nil || got != test.url || string(gotCA) != "certificate" {
					t.Fatalf("expected accepted connection, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.diagnostic) ||
				strings.Contains(err.Error(), "secret") {
				t.Fatalf(
					"expected redacted refusal %q, got %v",
					test.diagnostic,
					err,
				)
			}
		})
	}
}

func localDevTargetJSON(t *testing.T) string {
	t.Helper()
	disposable := false
	content, err := json.Marshal(Target{
		Environment:      "local-dev",
		Context:          "kind-local-dev-insurance-hub",
		DataNamespace:    "local-dev-all",
		ServiceNamespace: "local-dev-all",
		Cluster:          "local-dev-postgres-policy",
		ClusterUID:       "11111111-1111-4111-8111-111111111111",
		InstanceID:       "22222222-2222-4222-8222-222222222222",
		Database:         "policy",
		Host:             "local-dev-postgres-policy-rw.local-dev-all.svc.cluster.local",
		Disposable:       &disposable,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func writeTestFile(t *testing.T, name, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}
