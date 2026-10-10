package integrationtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const baselineRelativePath = "legacy/product-service/src/test/resources/product-read-baseline"

// FixtureSet identifies an accepted catalog snapshot.
type FixtureSet string

const (
	// FixtureQA identifies the production-like snapshot captured by issue 131.
	FixtureQA FixtureSet = "qa"
)

// CatalogFixture is the lossless subset of an authoritative #131 catalog capture used by Go tests.
type CatalogFixture struct {
	ScenarioID     string       `json:"scenarioId"`
	Environment    FixtureSet   `json:"environment"`
	Provenance     string       `json:"provenance"`
	SourceRevision string       `json:"sourceRevision"`
	SchemaIdentity Identity     `json:"schemaIdentity"`
	DataIdentity   DataIdentity `json:"dataIdentity"`
	Rows           []FixtureRow `json:"rows"`
}

// Identity records an accepted checksum and algorithm.
type Identity struct {
	Value     string `json:"value"`
	Algorithm string `json:"algorithm"`
}

// DataIdentity records the accepted catalog codes, checksum, and row count.
type DataIdentity struct {
	Codes     []string `json:"codes"`
	Value     string   `json:"value"`
	RowCount  int      `json:"rowCount"`
	Algorithm string   `json:"algorithm"`
}

// FixtureRow contains the SQL key and exact JSON text captured from PostgreSQL.
type FixtureRow struct {
	Code                      string   `json:"code"`
	Checksum                  Identity `json:"checksum"`
	RawLosslessDefinitionJSON string   `json:"rawLosslessDefinitionJson"`
}

// ResolveBaselineRoot locates the authoritative fixture directory by walking toward the checkout
// root. It does not copy or rewrite fixture data.
func ResolveBaselineRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve baseline root: %w", err)
	}
	if info, statErr := os.Stat(current); statErr == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}

	for {
		repositoryMarker := filepath.Join(current, "go-module-topology.json")
		baselineRoot := filepath.Join(current, filepath.FromSlash(baselineRelativePath))
		if regularFile(repositoryMarker) && directory(baselineRoot) {
			return baselineRoot, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New(
				"resolve baseline root: repository fixture directory not found",
			)
		}
		current = parent
	}
}

// LoadCatalog reads one accepted catalog without altering or normalizing its JSON payload strings.
func LoadCatalog(baselineRoot string, fixtureSet FixtureSet) (CatalogFixture, error) {
	if fixtureSet != FixtureQA {
		return CatalogFixture{}, fmt.Errorf(
			"load catalog: unknown fixture set %q",
			fixtureSet,
		)
	}
	path := filepath.Join(baselineRoot, "catalog", string(fixtureSet)+".json")
	content, err := os.ReadFile(path)
	if err != nil {
		return CatalogFixture{}, fmt.Errorf("load catalog %s: %w", fixtureSet, err)
	}

	var fixture CatalogFixture
	decoderErr := json.Unmarshal(content, &fixture)
	if decoderErr != nil {
		return CatalogFixture{}, fmt.Errorf(
			"load catalog %s: decode fixture: %w",
			fixtureSet,
			decoderErr,
		)
	}
	if fixture.Environment != fixtureSet || fixture.ScenarioID == "" ||
		fixture.Provenance == "" || fixture.SourceRevision == "" {
		return CatalogFixture{}, fmt.Errorf(
			"load catalog %s: fixture identity mismatch",
			fixtureSet,
		)
	}
	if fixture.SchemaIdentity.Algorithm != "MD5" ||
		fixture.DataIdentity.Algorithm != "MD5" ||
		fixture.DataIdentity.RowCount != len(fixture.Rows) ||
		len(fixture.DataIdentity.Codes) != len(fixture.Rows) {
		return CatalogFixture{}, fmt.Errorf(
			"load catalog %s: invalid identity metadata",
			fixtureSet,
		)
	}
	for index, row := range fixture.Rows {
		if row.Code == "" || row.RawLosslessDefinitionJSON == "" ||
			row.Checksum.Algorithm != "MD5" || row.Checksum.Value == "" ||
			fixture.DataIdentity.Codes[index] != row.Code {
			return CatalogFixture{}, fmt.Errorf(
				"load catalog %s: invalid row %d",
				fixtureSet,
				index,
			)
		}
	}
	return fixture, nil
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func directory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
