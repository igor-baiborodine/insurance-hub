package administration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const businessTables = 6

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// inspect checks target identity, schema compatibility and logical migration state. The same
// function runs under the migration session lock and inside read-only status transactions. Only
// an empty database or one installed by these migrations is accepted.
func inspect(ctx context.Context, db queryRower, target Target) (string, error) {
	if err := inspectPlatform(ctx, db, target); err != nil {
		return "", err
	}
	var tables int
	var outbox, history bool
	if err := db.QueryRowContext(ctx, inspectSchemaObjectsQuery).
		Scan(&tables, &outbox, &history); err != nil {
		return "", fmt.Errorf("inspect schema objects: %w", err)
	}
	var installed bool
	switch {
	case tables == 0 && !outbox:
	case tables == businessTables && outbox:
		installed = true
	default:
		return "", errors.New("partial Policy schema")
	}
	if !history {
		if installed {
			return "", errors.New("installed Policy schema has no migration history")
		}
		return "uninitialized", nil
	}
	status, err := inspectHistory(ctx, db, installed)
	if err != nil {
		return "", err
	}
	if installed {
		if err := inspectPolicy(ctx, db); err != nil {
			return "", err
		}
	}
	return status, nil
}

func inspectPlatform(ctx context.Context, db queryRower, target Target) error {
	var accepted bool
	if err := db.QueryRowContext(ctx, inspectPlatformQuery, target.Environment,
		target.InstanceID, target.ClusterUID, ownerRole, migratorRole).
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

func inspectPolicy(ctx context.Context, db queryRower) error {
	var accepted bool
	if err := db.QueryRowContext(ctx, inspectPolicySchemaQuery).Scan(&accepted); err != nil {
		return fmt.Errorf("inspect Policy schema: %w", err)
	}
	if !accepted {
		return errors.New("incompatible Policy schema or owner")
	}
	if err := db.QueryRowContext(ctx, inspectIdentityProgressQuery).
		Scan(&accepted); err != nil {
		return fmt.Errorf("inspect Policy identity progress: %w", err)
	}
	if !accepted {
		return errors.New(
			"an identity allocator is behind its Policy table; no repair performed",
		)
	}
	return nil
}

func inspectHistory(ctx context.Context, db queryRower, installed bool) (string, error) {
	var shape bool
	if err := db.QueryRowContext(ctx, inspectHistorySchemaQuery).Scan(&shape); err != nil {
		return "", fmt.Errorf("inspect migration history shape: %w", err)
	}
	if !shape {
		return "", errors.New("incompatible migration history metadata")
	}
	var version string
	if err := db.QueryRowContext(ctx, inspectHistoryRowsQuery, installed).
		Scan(&version); err != nil {
		return "", fmt.Errorf("inspect migration history rows: %w", err)
	}
	if version == "invalid" {
		return "", errors.New("inconsistent migration history")
	}
	return version, nil
}
