package administration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTarget_RejectsUnprotectedConnections(t *testing.T) {
	// given
	target := Target{
		Environment:      "qa",
		Context:          "qa-insurance-hub",
		DataNamespace:    "qa-data",
		ServiceNamespace: "qa-svc",
		Cluster:          "qa-postgres-document",
		ClusterUID:       "synthetic-cluster",
		InstanceID:       "00000000-0000-4000-8000-000000000362",
		Database:         "document",
		Host:             "qa-postgres-document-rw.qa-data.svc.cluster.local",
	}
	url := "postgres://go_document_migrator:synthetic@" + target.Host + "/document?sslmode=verify-full&sslrootcert=/test/ca.crt"
	// when / then
	if err := target.ValidateMigrationConnection(
		"qa",
		target.Context,
		url,
		"/test/ca.crt",
	); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"wrong owner login": strings.Replace(url, "go_document_migrator", "document", 1),
		"no verification":   strings.Replace(url, "verify-full", "require", 1),
		"foreign database":  strings.Replace(url, "/document?", "/product?", 1),
		"unexpected option": url + "&options=-crole=postgres",
		"foreign host":      strings.Replace(url, target.Host, "other", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := target.ValidateMigrationConnection(
				"qa",
				target.Context,
				value,
				"/test/ca.crt",
			); err == nil {
				t.Fatal("untrusted connection accepted")
			}
		})
	}
}

func TestReadTarget_RequiresPrivateStrictEnrollment(t *testing.T) {
	// given
	target := Target{
		Environment:      "disposable",
		Context:          "owned-container",
		DataNamespace:    "disposable",
		ServiceNamespace: "disposable",
		Cluster:          "fixture",
		ClusterUID:       "synthetic-cluster",
		InstanceID:       "00000000-0000-4000-8000-000000000362",
		Database:         "document",
		Host:             "localhost",
		Disposable:       true,
	}
	content, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	// when
	got, err := ReadTarget(path)
	// then
	if err != nil || got != target {
		t.Fatalf("enrollment=%+v error=%v", got, err)
	}

	for _, invalid := range []string{"{", "{}", string(content) + "{}", strings.Replace(string(content), "\"context\":", "\"unexpected\":", 1)} {
		t.Run(invalid, func(t *testing.T) {
			// given
			if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
				t.Fatal(err)
			}
			// when
			_, err := ReadTarget(path)
			// then
			if err == nil {
				t.Fatal("malformed or incomplete enrollment was accepted")
			}
		})
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTarget(path); err == nil {
		t.Fatal("public enrollment was accepted")
	}
	if _, err := ReadTarget(""); err == nil {
		t.Fatal("missing enrollment path was accepted")
	}
}
