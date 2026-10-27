package administration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	productpostgres "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/postgres"
)

const (
	seedSchemaVersion    = 1
	maxSeedCatalogBytes  = 8 << 20
	maxProductCodeLength = 255
	seedOperationBudget  = 60 * time.Second
)

type seedCatalog struct {
	SchemaVersion  int       `json:"schemaVersion"`
	SeedVersion    int       `json:"seedVersion"`
	ScenarioID     string    `json:"scenarioId"`
	Provenance     string    `json:"provenance"`
	SourceRevision string    `json:"sourceRevision"`
	SourcePath     string    `json:"sourcePath"`
	Rows           []seedRow `json:"rows"`
}

type seedRow struct {
	Code                      string `json:"code"`
	Checksum                  string `json:"checksum"`
	RawDefinitionSHA256       string `json:"rawDefinitionSha256"`
	RawLosslessDefinitionJSON string `json:"rawLosslessDefinitionJson"`
}

// SeedResult reports only aggregate outcomes; definitions never enter command output.
type SeedResult struct {
	Inserted int
	Existing int
}

// Seed validates the selected catalog and enrolled database boundary before inserting every
// missing Product in one transaction. Retrying after an uncertain result is safe and insert-only.
func Seed(
	parent context.Context,
	db *sql.DB,
	target Target,
	catalogPath string,
) (SeedResult, error) {
	catalog, err := loadSeedCatalog(catalogPath)
	if err != nil {
		return SeedResult{}, err
	}
	return seedCatalogRows(parent, db, target, catalog)
}

func loadSeedCatalog(path string) (seedCatalog, error) {
	if path == "" {
		return seedCatalog{}, errors.New("product seed catalog path is required")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return seedCatalog{}, errors.New("product seed catalog file is unavailable")
	}
	if info.Size() > maxSeedCatalogBytes {
		return seedCatalog{}, errors.New("product seed catalog file exceeds size limit")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return seedCatalog{}, errors.New("cannot read product seed catalog file")
	}
	if len(content) > maxSeedCatalogBytes {
		return seedCatalog{}, errors.New("product seed catalog file exceeds size limit")
	}
	return decodeSeedCatalog(content)
}

func decodeSeedCatalog(content []byte) (seedCatalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var catalog seedCatalog
	if err := decoder.Decode(&catalog); err != nil {
		return seedCatalog{}, fmt.Errorf("decode product seed catalog: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return seedCatalog{}, errors.New("product seed catalog has trailing content")
	}
	if err := validateSeedCatalog(catalog); err != nil {
		return seedCatalog{}, err
	}
	return catalog, nil
}

func validateSeedCatalog(catalog seedCatalog) error {
	if catalog.SchemaVersion != seedSchemaVersion {
		return errors.New("product seed catalog schema version is unsupported")
	}
	if catalog.SeedVersion <= 0 {
		return errors.New("product seed catalog version must be positive")
	}
	if strings.TrimSpace(catalog.ScenarioID) == "" ||
		strings.TrimSpace(catalog.Provenance) == "" ||
		strings.TrimSpace(catalog.SourceRevision) == "" ||
		strings.TrimSpace(catalog.SourcePath) == "" {
		return errors.New("product seed catalog metadata is incomplete")
	}
	if len(catalog.Rows) == 0 {
		return errors.New("product seed catalog must contain at least one product")
	}
	seen := make(map[string]struct{}, len(catalog.Rows))
	for _, row := range catalog.Rows {
		if row.Code == "" || strings.ContainsRune(row.Code, '\x00') ||
			utf8.RuneCountInString(row.Code) > maxProductCodeLength {
			return errors.New("product seed code is invalid")
		}
		if _, duplicate := seen[row.Code]; duplicate {
			return fmt.Errorf("product seed code %s is duplicated", row.Code)
		}
		seen[row.Code] = struct{}{}
		if _, err := hex.DecodeString(row.Checksum); err != nil || len(row.Checksum) != 32 {
			return fmt.Errorf("product seed %s checksum format is invalid", row.Code)
		}
		if _, err := hex.DecodeString(row.RawDefinitionSHA256); err != nil ||
			len(row.RawDefinitionSHA256) != 64 {
			return fmt.Errorf(
				"product seed %s raw definition identity format is invalid",
				row.Code,
			)
		}
		if _, err := productpostgres.DecodeProduct(
			row.Code,
			[]byte(row.RawLosslessDefinitionJSON),
		); err != nil {
			return fmt.Errorf(
				"product seed %s definition is invalid: %w",
				row.Code,
				err,
			)
		}
		rawIdentity := sha256.Sum256([]byte(row.RawLosslessDefinitionJSON))
		if hex.EncodeToString(rawIdentity[:]) != row.RawDefinitionSHA256 {
			return fmt.Errorf(
				"product seed %s raw definition identity does not match",
				row.Code,
			)
		}
	}
	return nil
}

func seedCatalogRows(
	parent context.Context,
	db *sql.DB,
	target Target,
	catalog seedCatalog,
) (result SeedResult, resultErr error) {
	if err := validateSeedCatalog(catalog); err != nil {
		return SeedResult{}, err
	}
	ctx, cancel := context.WithTimeout(parent, seedOperationBudget)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return SeedResult{}, fmt.Errorf("begin product seed transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("rollback product seed: %w", err),
			)
		}
	}()
	if err := acquireProductLock(ctx, tx); err != nil {
		return SeedResult{}, err
	}
	if err := inspectSeedBoundary(ctx, tx, target); err != nil {
		return SeedResult{}, err
	}
	if err := validateSeedChecksums(ctx, tx, catalog.Rows); err != nil {
		return SeedResult{}, err
	}
	inserted, err := insertSeedRows(ctx, tx, catalog.Rows)
	if err != nil {
		return SeedResult{}, err
	}
	result.Inserted = inserted
	result.Existing = len(catalog.Rows) - result.Inserted
	if err := tx.Commit(); err != nil {
		return SeedResult{}, fmt.Errorf(
			"commit product seed; result uncertain, inspect and retry safely: %w",
			err,
		)
	}
	return result, nil
}

func validateSeedChecksums(ctx context.Context, db queryRower, rows []seedRow) error {
	for _, row := range rows {
		var checksum string
		if err := db.QueryRowContext(
			ctx,
			`SELECT md5($1::jsonb::text)`,
			row.RawLosslessDefinitionJSON,
		).Scan(&checksum); err != nil {
			return fmt.Errorf("validate product seed %s checksum: %w", row.Code, err)
		}
		if checksum != row.Checksum {
			return fmt.Errorf(
				"product seed %s checksum does not match definition",
				row.Code,
			)
		}
	}
	return nil
}

func insertSeedRows(ctx context.Context, tx *sql.Tx, rows []seedRow) (int, error) {
	insertedCount := 0
	for _, row := range rows {
		command, err := tx.ExecContext(
			ctx,
			`INSERT INTO public.product(code,definition) VALUES($1,$2::jsonb)
ON CONFLICT(code) DO NOTHING`,
			row.Code,
			row.RawLosslessDefinitionJSON,
		)
		if err != nil {
			return 0, fmt.Errorf("insert product seed %s: %w", row.Code, err)
		}
		inserted, err := command.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf(
				"count product seed %s result: %w",
				row.Code,
				err,
			)
		}
		if inserted < 0 || inserted > 1 {
			return 0, fmt.Errorf(
				"count product seed %s result: unexpected affected rows %d",
				row.Code,
				inserted,
			)
		}
		insertedCount += int(inserted)
	}
	return insertedCount, nil
}

func inspectSeedBoundary(ctx context.Context, db queryRower, target Target) error {
	if target.Environment == "production" || !target.SeedAllowed {
		return errors.New("target enrollment does not allow seeding")
	}
	if err := inspectPlatform(
		ctx,
		db,
		target,
		"go_product_seeder",
		"go_product_seeder",
	); err != nil {
		return err
	}
	if err := inspectProduct(ctx, db); err != nil {
		return err
	}
	version, err := inspectHistory(ctx, db, true)
	if err != nil {
		return err
	}
	if version != "version=1" {
		return fmt.Errorf("product schema is not ready for seeding: %s", version)
	}
	return nil
}
