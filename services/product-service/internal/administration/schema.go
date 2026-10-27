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

// inspect checks target identity, Product compatibility and logical migration state. The same
// function runs under Goose's session lock and inside read-only status transactions.
func inspect(ctx context.Context, db queryRower, target Target) (string, error) {
	if err := inspectPlatform(ctx, db, target, "product", "go_product_migrator"); err != nil {
		return "", err
	}
	var product, history bool
	if err := db.QueryRowContext(ctx, inspectSchemaObjectsQuery).
		Scan(&product, &history); err != nil {
		return "", fmt.Errorf("inspect schema objects: %w", err)
	}
	if product {
		if err := inspectProduct(ctx, db); err != nil {
			return "", err
		}
	}
	if !history {
		if product {
			return "unmanaged", nil
		}
		return "uninitialized", nil
	}
	return inspectHistory(ctx, db, product)
}

func inspectPlatform(
	ctx context.Context,
	db queryRower,
	target Target,
	currentUser,
	sessionUser string,
) error {
	if target.Environment == "production" && target.SeedAllowed {
		return errors.New("production target cannot allow seeding")
	}
	var accepted bool
	err := db.QueryRowContext(ctx, inspectPlatformQuery, target.Environment, target.InstanceID, target.ClusterUID,
		target.SeedAllowed, currentUser, sessionUser).
		Scan(&accepted)
	if err != nil {
		return fmt.Errorf("inspect protected target: %w", err)
	}
	if !accepted {
		return errors.New(
			"protected target identity or marker permissions differ from enrollment",
		)
	}
	return nil
}

func inspectProduct(ctx context.Context, db queryRower) error {
	var accepted bool
	if err := db.QueryRowContext(ctx, inspectProductSchemaQuery).Scan(&accepted); err != nil {
		return fmt.Errorf("inspect Product schema: %w", err)
	}
	if !accepted {
		return errors.New("incompatible Product schema or owner")
	}
	return nil
}

func inspectHistory(
	ctx context.Context,
	db queryRower,
	product bool,
) (string, error) {
	var shape bool
	err := db.QueryRowContext(ctx, inspectHistorySchemaQuery).
		Scan(&shape)
	if err != nil {
		return "", fmt.Errorf("inspect migration history shape: %w", err)
	}
	if !shape {
		return "", errors.New("incompatible migration history metadata")
	}
	var version string
	err = db.QueryRowContext(ctx, inspectHistoryRowsQuery, product).Scan(&version)
	if err != nil {
		return "", fmt.Errorf("inspect migration history rows: %w", err)
	}
	if version == "invalid" {
		return "", errors.New("inconsistent migration history")
	}
	return version, nil
}
