package administration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// inspect checks the target, Document schema/sequence compatibility and migration history.
// The same helpers serve locked migration inspection and genuinely read-only status.
func inspect(ctx context.Context, db queryRower, target Target) (string, error) {
	if err := inspectPlatform(ctx, db, target, "document", "go_document_migrator"); err != nil {
		return "", err
	}
	var table, sequence, history, owned bool
	if err := db.QueryRowContext(ctx, inspectSchemaObjectsQuery).
		Scan(&table, &sequence, &history, &owned); err != nil {
		return "", fmt.Errorf("inspect schema objects: %w", err)
	}
	if !owned || table != sequence {
		return "", errors.New("foreign or partial Document schema")
	}
	if table {
		if err := inspectDocument(ctx, db); err != nil {
			return "", err
		}
	}
	if !history {
		if table {
			return "unmanaged", nil
		}
		return "uninitialized", nil
	}
	return inspectHistory(ctx, db, table)
}

func inspectPlatform(
	ctx context.Context,
	db queryRower,
	target Target,
	currentUser, sessionUser string,
) error {
	var accepted bool
	if err := db.QueryRowContext(ctx, inspectPlatformQuery, target.Environment, target.InstanceID, target.ClusterUID, currentUser, sessionUser).
		Scan(&accepted); err != nil {
		return fmt.Errorf("inspect protected target: %w", err)
	}
	if !accepted {
		return errors.New(
			"protected target identity or marker permissions differ from enrollment",
		)
	}
	return nil
}

func inspectDocument(ctx context.Context, db queryRower) error {
	var accepted bool
	if err := db.QueryRowContext(ctx, inspectDocumentSchemaQuery).Scan(&accepted); err != nil {
		return fmt.Errorf("inspect Document schema: %w", err)
	}
	if !accepted {
		return errors.New("incompatible Document schema or owner")
	}
	return inspectSequence(ctx, db)
}

func inspectHistory(ctx context.Context, db queryRower, table bool) (string, error) {
	var shape bool
	if err := db.QueryRowContext(ctx, inspectHistorySchemaQuery).Scan(&shape); err != nil {
		return "", fmt.Errorf("inspect migration history shape: %w", err)
	}
	if !shape {
		return "", errors.New("incompatible migration history metadata")
	}
	var version string
	if err := db.QueryRowContext(ctx, inspectHistoryRowsQuery, table).
		Scan(&version); err != nil {
		return "", fmt.Errorf("inspect migration history rows: %w", err)
	}
	if version == "invalid" {
		return "", errors.New("inconsistent migration history")
	}
	return version, nil
}

func inspectSequence(ctx context.Context, db queryRower) error {
	var valid bool
	err := db.QueryRowContext(ctx, inspectSequenceQuery).Scan(&valid)
	if err != nil {
		return fmt.Errorf("inspect sequence metadata: %w", err)
	}
	if !valid {
		return errors.New("incompatible Hibernate sequence or owner")
	}
	// Do not mutate nextval/setval, or synthesize a new position from max(id).
	// Java's unpooled increment-1 allocator can only advance this safe position.
	err = db.QueryRowContext(ctx, inspectSequenceProgressQuery).Scan(&valid)
	if err != nil {
		return fmt.Errorf("inspect sequence progress: %w", err)
	}
	if !valid {
		return errors.New("unsafe or exhausted Hibernate sequence; no repair performed")
	}
	return nil
}
