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

const productSchemaIdentity = "76fac669b254931c0709c236fdd7804b"

// inspect verifies the protected target and complete initial version-1 boundary. The same
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
		if err := inspectProduct(ctx, db, target); err != nil {
			return "", err
		}
	}
	if !history {
		if product {
			return "unmanaged", nil
		}
		return "uninitialized", nil
	}
	version, err := inspectHistory(ctx, db, product, target)
	if err != nil {
		return "", err
	}
	if version == "version=1" && target.SeedAllowed {
		if err := inspectManagedSeedGrants(ctx, db); err != nil {
			return "", err
		}
	}
	return version, nil
}

func inspectManagedSeedGrants(ctx context.Context, db queryRower) error {
	var accepted bool
	err := db.QueryRowContext(ctx, inspectManagedSeedGrantsQuery).
		Scan(&accepted)
	if err != nil {
		return fmt.Errorf("inspect managed seed grants: %w", err)
	}
	if !accepted {
		return errors.New("managed seed grants differ from enrollment")
	}
	return nil
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
		return errors.New("protected target, role or grant policy differs from enrollment")
	}
	return inspectRoles(ctx, db, target)
}

func inspectRoles(ctx context.Context, db queryRower, target Target) error {
	var accepted bool
	err := db.QueryRowContext(ctx, inspectRolesQuery,
		target.Environment, target.SeedAllowed).Scan(&accepted)
	if err != nil {
		return fmt.Errorf("inspect administration roles: %w", err)
	}
	if !accepted {
		return errors.New("administration role or grant policy differs from enrollment")
	}
	return nil
}

func inspectProduct(ctx context.Context, db queryRower, target Target) error {
	var identity string
	if err := db.QueryRowContext(ctx, inspectProductSchemaQuery).Scan(&identity); err != nil {
		return fmt.Errorf("inspect Product schema: %w", err)
	}
	if identity != productSchemaIdentity {
		return errors.New("incompatible Product columns or constraints")
	}
	var accepted bool
	err := db.QueryRowContext(ctx, inspectProductMetadataQuery, target.SeedAllowed).
		Scan(&accepted)
	if err != nil {
		return fmt.Errorf("inspect Product metadata: %w", err)
	}
	if !accepted {
		return errors.New("incompatible Product ownership, index, security or grants")
	}
	return nil
}

func inspectHistory(
	ctx context.Context,
	db queryRower,
	product bool,
	target Target,
) (string, error) {
	var shape bool
	err := db.QueryRowContext(ctx, inspectHistorySchemaQuery, target.SeedAllowed).
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
