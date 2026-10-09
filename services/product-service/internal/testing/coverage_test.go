package integrationtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcceptedBaselineHasExactScenarioCoverage(t *testing.T) {
	// given
	baselineRoot, err := ResolveBaselineRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	registry := ScenarioRegistry()

	// when
	err = ValidateBaselineCoverage(baselineRoot, registry)
	// then
	if err != nil {
		t.Fatalf("validate accepted baseline coverage: %v", err)
	}
	if len(registry) < 150 {
		t.Fatalf("registry unexpectedly small: %d", len(registry))
	}
}

func TestBaselineCoverageRejectsMissingRegistration(t *testing.T) {
	// given
	baselineRoot, err := ResolveBaselineRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	registry := ScenarioRegistry()
	delete(registry, "HTTP-DIRECT-LIST-001")

	// when
	err = ValidateBaselineCoverage(baselineRoot, registry)

	// then
	if err == nil || !strings.Contains(err.Error(), "has no owner or classification") {
		t.Fatalf("error = %v", err)
	}
}

func TestBaselineCoverageRejectsFixtureModification(t *testing.T) {
	// given
	baselineRoot, err := ResolveBaselineRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	modifiedRoot := t.TempDir()
	copyAcceptedCorpus(t, baselineRoot, modifiedRoot)
	manifestPath := filepath.Join(modifiedRoot, "manifest.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest = []byte(strings.Replace(
		string(manifest),
		`"baseline": "issue-131-product-read"`,
		`"baseline": "wrong-corpus"`,
		1,
	))
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}

	// when
	err = ValidateBaselineCoverage(modifiedRoot, ScenarioRegistry())

	// then
	if err == nil || !strings.Contains(err.Error(), "accepted corpus identity changed") {
		t.Fatalf("error = %v", err)
	}
}

func copyAcceptedCorpus(t *testing.T, sourceRoot, targetRoot string) {
	t.Helper()
	for relativePath := range acceptedCorpusSHA256 {
		source := filepath.Join(sourceRoot, filepath.FromSlash(relativePath))
		target := filepath.Join(targetRoot, filepath.FromSlash(relativePath))
		content, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("read %s: %v", relativePath, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatalf("create parent for %s: %v", relativePath, err)
		}
		if err := os.WriteFile(target, content, 0o600); err != nil {
			t.Fatalf("copy %s: %v", relativePath, err)
		}
	}
}
