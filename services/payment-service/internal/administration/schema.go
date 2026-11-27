package administration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const businessTables = 3

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// inspect checks target identity, schema compatibility and logical migration state. The same
// function runs in the locked migration transaction and in read-only status transactions. Only an
// empty database or one installed by this migration is accepted.
func inspect(ctx context.Context, db queryRower, target Target) (string, error) {
	if err := inspectPlatform(ctx, db, target); err != nil {
		return "", err
	}
	var tables int
	var history bool
	if err := db.QueryRowContext(ctx, inspectSchemaObjectsQuery).
		Scan(&tables, &history); err != nil {
		return "", fmt.Errorf("inspect schema objects: %w", err)
	}
	if !history {
		switch tables {
		case 0:
			return "uninitialized", nil
		case businessTables:
			return "", errors.New("installed Payment schema has no migration history")
		default:
			return "", errors.New("partial Payment schema")
		}
	}
	status, err := inspectHistory(ctx, db)
	if err != nil {
		return "", err
	}
	if tables != businessTables {
		return "", errors.New("partial Payment schema")
	}
	if err := inspectPayment(ctx, db); err != nil {
		return "", err
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

func inspectPayment(ctx context.Context, db queryRower) error {
	var accepted bool
	if err := db.QueryRowContext(ctx, inspectPaymentSchemaQuery).Scan(&accepted); err != nil {
		return fmt.Errorf("inspect Payment schema: %w", err)
	}
	if !accepted {
		return errors.New("incompatible Payment schema or owner")
	}
	return nil
}

func inspectHistory(ctx context.Context, db queryRower) (string, error) {
	var shape bool
	if err := db.QueryRowContext(ctx, inspectHistorySchemaQuery).Scan(&shape); err != nil {
		return "", fmt.Errorf("inspect migration history shape: %w", err)
	}
	if !shape {
		return "", errors.New("incompatible migration history metadata")
	}
	var version string
	if err := db.QueryRowContext(ctx, inspectHistoryRowsQuery, schemaVersion).
		Scan(&version); err != nil {
		return "", fmt.Errorf("inspect migration history rows: %w", err)
	}
	if version == "invalid" {
		return "", errors.New("inconsistent migration history")
	}
	return version, nil
}
