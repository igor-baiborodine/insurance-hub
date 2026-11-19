package administration

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

const (
	schemaVersion = 1
	// AppliedStatus is the status reported for the reviewed schema version.
	AppliedStatus = "version=1"
	// The seeder uses the same transaction lock as the migrator.
	administrationLockID int64 = 190002
)

//go:embed migrations/00001_pricing.sql
var schemaSQL string

// Migrate applies the Pricing schema and records its version in one transaction.
func Migrate(ctx context.Context, db *sql.DB, target Target) error {
	migration := goose.NewGoMigration(schemaVersion, &goose.GoFunc{
		RunTx: func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(
				ctx,
				"SELECT pg_advisory_xact_lock($1)",
				administrationLockID,
			); err != nil {
				return databaseError("acquire Pricing administration lock", err)
			}
			status, err := inspect(ctx, tx, target)
			if err != nil || status == AppliedStatus {
				return err
			}
			if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
				return databaseError("apply Pricing schema version 1", err)
			}
			if _, err := tx.ExecContext(
				ctx,
				"INSERT INTO public.pricing_schema_version (version_id) VALUES ($1)",
				schemaVersion,
			); err != nil {
				return databaseError("record Pricing schema version", err)
			}
			return nil
		},
	}, nil)
	// Goose owns the transaction. Pricing creates and records its history inside that transaction
	// so a failed first migration cannot leave an initialized history table behind.
	provider, err := goose.NewProvider(goose.DialectPostgres, db, nil,
		goose.WithGoMigrations(migration), goose.WithDisableGlobalRegistry(true),
		goose.WithDisableVersioning(true),
	)
	if err != nil {
		return errors.New("cannot configure Pricing Goose migration")
	}
	if _, err := provider.ApplyVersion(ctx, schemaVersion, true); err != nil {
		var safe *operationError
		if errors.As(err, &safe) {
			return safe
		}
		return databaseError("run Pricing migration", err)
	}
	return nil
}

// operationError carries diagnostics safe to report without PostgreSQL messages or DSNs.
type operationError struct{ message string }

func (e *operationError) Error() string { return e.message }

func databaseError(operation string, err error) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, context.Canceled):
		return &operationError{operation + ": cancelled"}
	case errors.Is(err, context.DeadlineExceeded):
		return &operationError{operation + ": deadline exceeded"}
	case errors.As(err, &pgErr):
		return &operationError{fmt.Sprintf("%s (SQLSTATE %s)", operation, pgErr.Code)}
	default:
		return &operationError{operation + " failed"}
	}
}
