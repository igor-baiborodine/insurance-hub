package administration

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// AppliedStatus is the status reported for the current managed schema.
const AppliedStatus = "version=1"

const cleanupBudget = 5 * time.Second

//go:embed migrations/*.sql
var migrationSQL embed.FS

// Migrate verifies the protected target and schema under the same session lock Goose uses for
// history setup and version-1 execution. Replays validate even when version 1 is applied.
func Migrate(parent context.Context, db *sql.DB, target Target) error {
	source, err := fs.Sub(migrationSQL, "migrations")
	if err != nil {
		return fmt.Errorf("load migration assets: %w", err)
	}
	sessionLocker, err := lock.NewPostgresSessionLocker(
		lock.WithLockID(policyLockID), lock.WithLockTimeout(1, 30),
		lock.WithUnlockTimeout(1, 5),
	)
	if err != nil {
		return fmt.Errorf("configure Policy migration lock: %w", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		source,
		goose.WithDisableGlobalRegistry(true),
		goose.WithSessionLocker(
			validatingLocker{SessionLocker: sessionLocker, target: target},
		),
	)
	if err != nil {
		return fmt.Errorf("configure Policy migration: %w", err)
	}
	if _, err := provider.ApplyVersion(
		parent,
		1,
		true,
	); err != nil &&
		!isOnlyAlreadyApplied(err) {
		return fmt.Errorf("apply Policy migration: %w", err)
	}
	return applyGrants(parent, db, target)
}

// applyGrants also runs on replay, so an interrupted grant phase needs no special repair command.
func applyGrants(ctx context.Context, db *sql.DB, target Target) (resultErr error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Policy grants: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("rollback Policy grants: %w", err),
			)
		}
	}()
	if err := acquirePolicyLock(ctx, tx); err != nil {
		return err
	}
	status, err := inspect(ctx, tx, target)
	if err != nil {
		return fmt.Errorf("verify Policy migration: %w", err)
	}
	if status != AppliedStatus {
		return fmt.Errorf("verify Policy migration: unexpected state %q", status)
	}
	if _, err := tx.ExecContext(ctx, applyWriterGrantsQuery); err != nil {
		return fmt.Errorf("apply Policy writer grants: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Policy grants; inspect and retry safely: %w", err)
	}
	return nil
}

func isOnlyAlreadyApplied(err error) bool {
	if !errors.Is(err, goose.ErrAlreadyApplied) {
		return false
	}
	var combined interface{ Unwrap() []error }
	if !errors.As(err, &combined) {
		return true
	}
	causes := combined.Unwrap()
	if len(causes) == 0 {
		return false
	}
	for _, cause := range causes {
		if !isOnlyAlreadyApplied(cause) {
			return false
		}
	}
	return true
}

// Status has no Goose calls. Its transaction rejects writes even though the migration login can
// SET ROLE to the Policy object owner.
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

type validatingLocker struct {
	lock.SessionLocker
	target Target
}

func (l validatingLocker) SessionLock(ctx context.Context, conn *sql.Conn) error {
	if err := l.SessionLocker.SessionLock(ctx, conn); err != nil {
		return err
	}
	if _, err := inspect(ctx, conn, l.target); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupBudget)
		defer cancel()
		return errors.Join(err, l.SessionLocker.SessionUnlock(cleanup, conn))
	}
	return nil
}

func (l validatingLocker) SessionUnlock(ctx context.Context, conn *sql.Conn) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupBudget)
	defer cancel()
	return l.SessionLocker.SessionUnlock(cleanup, conn)
}
