package integrationtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAndLoad_AuthoritativeQACatalog(t *testing.T) {
	// given
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	// when
	baselineRoot, err := ResolveBaselineRoot(workingDirectory)
	if err != nil {
		t.Fatalf("resolve baseline root: %v", err)
	}

	// then
	if !strings.HasSuffix(
		filepath.ToSlash(baselineRoot),
		"legacy/product-service/src/test/resources/product-read-baseline",
	) {
		t.Errorf("baseline root = %q, want authoritative checkout path", baselineRoot)
	}
	fixture, loadErr := LoadCatalog(baselineRoot, FixtureQA)
	if loadErr != nil {
		t.Fatalf("load QA fixture: %v", loadErr)
	}
	if fixture.DataIdentity.RowCount != 4 || len(fixture.Rows) != 4 {
		t.Errorf(
			"QA row count = %d/%d, want 4/4",
			fixture.DataIdentity.RowCount,
			len(fixture.Rows),
		)
	}
	if fixture.SchemaIdentity.Value != "76fac669b254931c0709c236fdd7804b" {
		t.Errorf("QA schema identity = %s", fixture.SchemaIdentity.Value)
	}
}

func TestFixtureLoading_RejectsMissingAndUnknownInputs(t *testing.T) {
	// given
	missingRoot := t.TempDir()

	// when
	_, missingErr := LoadCatalog(missingRoot, FixtureQA)
	_, unknownErr := LoadCatalog(missingRoot, FixtureSet("local-dev"))
	_, resolveErr := ResolveBaselineRoot(missingRoot)

	// then
	if missingErr == nil || !strings.Contains(missingErr.Error(), "qa") {
		t.Errorf("missing fixture error = %v", missingErr)
	}
	if unknownErr == nil || !strings.Contains(unknownErr.Error(), "unknown fixture set") {
		t.Errorf("unknown fixture error = %v", unknownErr)
	}
	if resolveErr == nil || !strings.Contains(resolveErr.Error(), "not found") {
		t.Errorf("resolve missing root error = %v", resolveErr)
	}
}
