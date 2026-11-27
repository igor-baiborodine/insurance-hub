package administration

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"

	"github.com/pressly/goose/v3"
)

const (
	schemaVersion = 1
	// AppliedStatus is the status reported for the current Payment schema.
	AppliedStatus = "version=1"
)

//go:embed migrations/00001_payment.sql
var schemaSQL string

// Migrate applies the Payment schema and records its version in one transaction under the
// administration lock. A replay inspects the installed schema and changes nothing, so an
// interrupted run needs no special repair command.
func Migrate(ctx context.Context, db *sql.DB, target Target) error {
	migration := goose.NewGoMigration(schemaVersion, &goose.GoFunc{
		RunTx: func(ctx context.Context, tx *sql.Tx) error {
			if err := acquirePaymentLock(ctx, tx); err != nil {
				return err
			}
			status, err := inspect(ctx, tx, target)
			if err != nil {
				return err
			}
			if status == AppliedStatus {
				return nil
			}
			if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
				return fmt.Errorf("apply Payment schema version 1: %w", err)
			}
			if _, err := tx.ExecContext(
				ctx,
				recordSchemaVersionQuery,
				schemaVersion,
			); err != nil {
				return fmt.Errorf("record Payment schema version: %w", err)
			}
			return nil
		},
	}, nil)
	// Goose owns the transaction. Payment records its own history inside that transaction, so a
	// refused or failed run leaves no history or schema behind.
	provider, err := goose.NewProvider(goose.DialectPostgres, db, nil,
		goose.WithGoMigrations(migration), goose.WithDisableGlobalRegistry(true),
		goose.WithDisableVersioning(true),
	)
	if err != nil {
		return errors.New("configure Payment migration failed")
	}
	if _, err := provider.ApplyVersion(ctx, schemaVersion, true); err != nil {
		return fmt.Errorf("run Payment migration: %w", err)
	}
	return nil
}

// Status has no Goose calls. Its transaction rejects writes even though the migration login can
// SET ROLE to the Payment object owner.
func Status(parent context.Context, db *sql.DB, target Target) (state string, resultErr error) {
	tx, err := db.BeginTx(
		parent,
		&sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead},
	)
	if err != nil {
		return "", fmt.Errorf("begin read-only inspection: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("rollback inspection: %w", err),
			)
		}
	}()
	state, err = inspect(parent, tx, target)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("complete read-only inspection: %w", err)
	}
	return state, nil
}
