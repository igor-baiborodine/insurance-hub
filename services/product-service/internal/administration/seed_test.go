package administration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var errSyntheticSeedInsert = errors.New("synthetic later insert failure")

var acceptedSeedChecksums = map[string]string{
	"CAR": "915ca1a8e507fdb4bdb7039cde7e2998",
	"FAI": "df87da1ca51acaabf8b0717c9d2a666d",
	"HSI": "d4c5d818a6a1225de8a189e0309f6b38",
	"TRI": "4097333f55b5abb72892d714e9b2b1a4",
}

var acceptedSeedCodes = []string{"CAR", "FAI", "HSI", "TRI"}

func baseSeedCatalogPath(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate seed test source")
	}
	return filepath.Join(filepath.Dir(filename), "seeds", "00001_catalog.json")
}

type seedFailureConnector struct{ connection *seedFailureConnection }

func (c seedFailureConnector) Connect(context.Context) (driver.Conn, error) {
	return c.connection, nil
}
func (seedFailureConnector) Driver() driver.Driver { return seedFailureDriver{} }

type seedFailureDriver struct{}

func (seedFailureDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type seedFailureConnection struct {
	beginAttempts  int
	checksumChecks int
	insertAttempts int
	rolledBack     bool
	failInsert     bool
	commitFailure  bool
	pending        []seedRow
	committed      []seedRow
}

func (*seedFailureConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not expected")
}
func (*seedFailureConnection) Close() error { return nil }

func (*seedFailureConnection) Begin() (driver.Tx, error) { return nil, errors.New("BeginTx required") }

func (c *seedFailureConnection) BeginTx(
	context.Context,
	driver.TxOptions,
) (driver.Tx, error) {
	c.beginAttempts++
	c.pending = nil
	return seedFailureTransaction{connection: c}, nil
}

func (c *seedFailureConnection) ExecContext(
	_ context.Context,
	query string,
	arguments []driver.NamedValue,
) (driver.Result, error) {
	if !strings.Contains(query, "INSERT INTO public.product") {
		return driver.RowsAffected(0), nil
	}
	c.insertAttempts++
	if c.failInsert && c.insertAttempts == 3 {
		return nil, errSyntheticSeedInsert
	}
	if len(arguments) != 2 {
		return nil, errors.New("unexpected product seed arguments")
	}
	code, codeOK := arguments[0].Value.(string)
	definition, definitionOK := arguments[1].Value.(string)
	if !codeOK || !definitionOK {
		return nil, errors.New("unexpected product seed argument types")
	}
	c.pending = append(c.pending, seedRow{Code: code, RawLosslessDefinitionJSON: definition})
	return driver.RowsAffected(1), nil
}

func (c *seedFailureConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "SELECT md5($1::jsonb::text)"):
		if c.checksumChecks >= len(acceptedSeedCodes) {
			return nil, errors.New("unexpected checksum validation")
		}
		checksum := acceptedSeedChecksums[acceptedSeedCodes[c.checksumChecks]]
		c.checksumChecks++
		return &syntheticRows{values: []driver.Value{checksum}}, nil
	case query == inspectProductSchemaQuery:
		return &syntheticRows{values: []driver.Value{productSchemaIdentity}}, nil
	case query == inspectProductMetadataQuery:
		return &syntheticRows{values: []driver.Value{true}}, nil
	case query == inspectHistorySchemaQuery:
		return &syntheticRows{values: []driver.Value{true}}, nil
	case query == inspectHistoryRowsQuery:
		return &syntheticRows{values: []driver.Value{"version=1"}}, nil
	default:
		return &syntheticRows{values: []driver.Value{true}}, nil
	}
}

type seedFailureTransaction struct{ connection *seedFailureConnection }

func (tx seedFailureTransaction) Commit() error {
	if tx.connection.commitFailure {
		return errors.New("synthetic commit acknowledgement failure")
	}
	tx.connection.committed = append(tx.connection.committed, tx.connection.pending...)
	tx.connection.pending = nil
	return nil
}

func (tx seedFailureTransaction) Rollback() error {
	tx.connection.rolledBack = true
	tx.connection.pending = nil
	return nil
}

func TestLoadSeedCatalog_ContainsAcceptedProducts(t *testing.T) {
	// when
	catalog, err := loadSeedCatalog(baseSeedCatalogPath(t))
	// then
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Rows) != len(acceptedSeedCodes) {
		t.Fatalf("seed rows = %d, want %d", len(catalog.Rows), len(acceptedSeedCodes))
	}
	for index, row := range catalog.Rows {
		if row.Code != acceptedSeedCodes[index] ||
			row.Checksum != acceptedSeedChecksums[row.Code] {
			t.Fatalf("unexpected seed row %d: %+v", index, row)
		}
	}
}

func TestDecodeSeedCatalog_RejectsInvalidInputBeforeDatabaseAccess(t *testing.T) {
	accepted, err := loadSeedCatalog(baseSeedCatalogPath(t))
	if err != nil {
		t.Fatal(err)
	}
	clone := func() seedCatalog {
		content, marshalErr := json.Marshal(accepted)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		var catalog seedCatalog
		if unmarshalErr := json.Unmarshal(content, &catalog); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
		return catalog
	}
	for _, scenario := range []struct {
		name   string
		mutate func(*seedCatalog)
		want   string
	}{
		{
			name: "duplicate code",
			mutate: func(catalog *seedCatalog) {
				catalog.Rows[1].Code = catalog.Rows[0].Code
			},
			want: "duplicated",
		},
		{
			name: "empty code",
			mutate: func(catalog *seedCatalog) {
				catalog.Rows[0].Code = ""
			},
			want: "code is invalid",
		},
		{
			name: "invalid checksum format",
			mutate: func(catalog *seedCatalog) {
				catalog.Rows[0].Checksum = "not-a-checksum"
			},
			want: "checksum format is invalid",
		},
		{
			name: "invalid definition",
			mutate: func(catalog *seedCatalog) {
				catalog.Rows[0].RawLosslessDefinitionJSON = `{"questions":[{"type":"unsupported"}]}`
			},
			want: "definition is invalid",
		},
		{
			name: "changed definition identity",
			mutate: func(catalog *seedCatalog) {
				catalog.Rows[0].RawDefinitionSHA256 = strings.Repeat("0", 64)
			},
			want: "raw definition identity does not match",
		},
		{
			name: "empty catalog",
			mutate: func(catalog *seedCatalog) {
				catalog.Rows = nil
			},
			want: "at least one product",
		},
		{
			name: "missing provenance",
			mutate: func(catalog *seedCatalog) {
				catalog.Provenance = ""
			},
			want: "metadata is incomplete",
		},
		{
			name: "unsupported schema",
			mutate: func(catalog *seedCatalog) {
				catalog.SchemaVersion++
			},
			want: "schema version is unsupported",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// given
			catalog := clone()
			scenario.mutate(&catalog)

			// when
			_, seedErr := seedCatalogRows(context.Background(), nil, Target{}, catalog)

			// then
			if seedErr == nil || !strings.Contains(seedErr.Error(), scenario.want) {
				t.Fatalf("invalid seed input result: %v", seedErr)
			}
		})
	}
}

func TestDecodeSeedCatalog_AcceptsGenericCatalog(t *testing.T) {
	accepted, err := loadSeedCatalog(baseSeedCatalogPath(t))
	if err != nil {
		t.Fatal(err)
	}
	catalog := accepted
	catalog.SeedVersion = 42
	catalog.ScenarioID = "staging-extra"
	catalog.Provenance = "environment-owned catalog"
	catalog.SourceRevision = "staging-v1"
	catalog.SourcePath = "catalog/staging.json"
	catalog.Rows = append([]seedRow(nil), accepted.Rows[0])
	catalog.Rows[0].Code = "STAGING-SPECIAL"

	content, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSeedCatalog(content)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ScenarioID != "staging-extra" || decoded.Rows[0].Code != "STAGING-SPECIAL" {
		t.Fatalf("generic catalog changed during decode: %+v", decoded)
	}
}

func TestLoadSeedCatalog_RejectsUnavailableAndOversizedFiles(t *testing.T) {
	if _, err := loadSeedCatalog(""); err == nil {
		t.Fatal("empty catalog path was accepted")
	}
	if _, err := loadSeedCatalog(t.TempDir()); err == nil {
		t.Fatal("catalog directory was accepted")
	}
	oversized := filepath.Join(t.TempDir(), "catalog.json")
	file, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxSeedCatalogBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSeedCatalog(oversized); err == nil {
		t.Fatal("oversized catalog was accepted")
	}
}

func TestDecodeSeedCatalog_RejectsUnknownAndTrailingContent(t *testing.T) {
	for _, content := range [][]byte{
		[]byte(`{"unexpected":true}`),
		[]byte(`{} {}`),
		[]byte(`not-json`),
	} {
		if _, err := decodeSeedCatalog(content); err == nil {
			t.Fatalf("invalid seed document was accepted: %s", content)
		}
	}
}

func TestSeed_RollsBackWhenLaterInsertFails(t *testing.T) {
	connection := &seedFailureConnection{failInsert: true}
	db := sql.OpenDB(seedFailureConnector{connection: connection})
	defer func() { _ = db.Close() }()
	target := Target{Environment: "disposable", SeedAllowed: true}

	// when
	result, err := Seed(context.Background(), db, target, baseSeedCatalogPath(t))

	// then
	if result != (SeedResult{}) || !errors.Is(err, errSyntheticSeedInsert) {
		t.Fatalf("later insert result = %+v, error = %v", result, err)
	}
	if connection.beginAttempts != 1 || connection.insertAttempts != 3 ||
		!connection.rolledBack || len(connection.pending) != 0 || len(connection.committed) != 0 {
		t.Fatalf(
			"transaction attempts = %d, insert attempts = %d, rolled back = %t, committed = %d",
			connection.beginAttempts,
			connection.insertAttempts,
			connection.rolledBack,
			len(connection.committed),
		)
	}
}

func TestSeed_RejectsMismatchedChecksumBeforeInserts(t *testing.T) {
	catalog, err := loadSeedCatalog(baseSeedCatalogPath(t))
	if err != nil {
		t.Fatal(err)
	}
	catalog.Rows[0].Checksum = strings.Repeat("0", 32)
	connection := &seedFailureConnection{}
	db := sql.OpenDB(seedFailureConnector{connection: connection})
	defer func() { _ = db.Close() }()
	target := Target{Environment: "disposable", SeedAllowed: true}

	// when
	result, err := seedCatalogRows(context.Background(), db, target, catalog)

	// then
	if result != (SeedResult{}) || err == nil ||
		!strings.Contains(err.Error(), "checksum does not match definition") {
		t.Fatalf("mismatched checksum result = %+v, error = %v", result, err)
	}
	if connection.insertAttempts != 0 || !connection.rolledBack {
		t.Fatalf(
			"mismatched checksum made %d insert attempts; rolled back = %t",
			connection.insertAttempts,
			connection.rolledBack,
		)
	}
}

func TestInsertSeedRows_PreservesSyntheticJSONRepresentation(t *testing.T) {
	connection := &seedFailureConnection{}
	db := sql.OpenDB(seedFailureConnector{connection: connection})
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	definition := `{"amount":1.2300,"nullable":null,"ordered":[{"code":"B"},{"code":"A"}]}`

	// when
	inserted, err := insertSeedRows(
		context.Background(),
		tx,
		[]seedRow{{Code: "SYN", RawLosslessDefinitionJSON: definition}},
	)
	if err == nil {
		err = tx.Commit()
	}

	// then
	if err != nil || inserted != 1 || connection.beginAttempts != 1 ||
		len(connection.committed) != 1 ||
		connection.committed[0].RawLosslessDefinitionJSON != definition {
		t.Fatalf(
			"synthetic insert = %d, begins = %d, committed = %+v, error = %v",
			inserted,
			connection.beginAttempts,
			connection.committed,
			err,
		)
	}
}

func TestSeed_ReportsUncertainCommitForSafeReplay(t *testing.T) {
	connection := &seedFailureConnection{commitFailure: true}
	db := sql.OpenDB(seedFailureConnector{connection: connection})
	defer func() { _ = db.Close() }()
	target := Target{Environment: "disposable", SeedAllowed: true}

	// when
	result, err := Seed(context.Background(), db, target, baseSeedCatalogPath(t))

	// then
	if result != (SeedResult{}) || err == nil ||
		!strings.Contains(err.Error(), "result uncertain, inspect and retry safely") {
		t.Fatalf("uncertain commit result = %+v, error = %v", result, err)
	}
}
