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

const (
	cleanupBudget = 5 * time.Second
)

//go:embed migrations/*.sql
var migrationSQL embed.FS

// Migrate verifies the protected target and schema under the same session lock Goose uses for
// history setup and forward migration execution. Replays validate even when all versions are applied.
func Migrate(parent context.Context, db *sql.DB, target Target) error {
	source, err := fs.Sub(migrationSQL, "migrations")
	if err != nil {
		return fmt.Errorf("load migration assets: %w", err)
	}
	sessionLocker, err := lock.NewPostgresSessionLocker(
		lock.WithLockID(documentLockID), lock.WithLockTimeout(1, 30),
		lock.WithUnlockTimeout(1, 5),
	)
	if err != nil {
		return fmt.Errorf("configure Document migration lock: %w", err)
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
		return fmt.Errorf("configure Document migration: %w", err)
	}
	for _, version := range []int64{1, 2} {
		_, err = provider.ApplyVersion(parent, version, true)
		if err != nil && !isOnlyAlreadyApplied(err) {
			return fmt.Errorf("apply Document migration version %d: %w", version, err)
		}
	}
	return applyGrants(parent, db, target)
}

// applyGrants also runs on replay, so an interrupted grant phase needs no special repair command.
func applyGrants(ctx context.Context, db *sql.DB, target Target) (resultErr error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Document grants: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("rollback Document grants: %w", err),
			)
		}
	}()
	if err := acquireDocumentLock(ctx, tx); err != nil {
		return err
	}
	status, err := inspect(ctx, tx, target)
	if err != nil {
		return fmt.Errorf("verify Document migration: %w", err)
	}
	if status != "version=2" {
		return fmt.Errorf("verify Document migration: unexpected state %q", status)
	}
	grants := []string{applyReaderGrantsQuery, applyWriterGrantsQuery}

	for _, grant := range grants {
		if _, err := tx.ExecContext(ctx, grant); err != nil {
			return fmt.Errorf("apply Document grants: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Document grants; inspect and retry safely: %w", err)
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
// SET ROLE to the Document object owner.
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
	tx, err := conn.BeginTx(ctx, nil)
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		var exists bool
		err = tx.QueryRowContext(ctx, inspectDocumentPresenceQuery).
			Scan(&exists)
		if err == nil && exists {
			_, err = tx.ExecContext(
				ctx,
				lockDocumentQuery,
			)
		}
		if err == nil {
			_, err = inspect(ctx, tx, l.target)
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
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
