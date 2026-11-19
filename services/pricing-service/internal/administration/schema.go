package administration

import (
	"context"
	"database/sql"
)

// Status verifies the enrolled target and inspects migration history read-only.
func Status(ctx context.Context, db *sql.DB, target Target) (string, error) {
	tx, err := db.BeginTx(
		ctx,
		&sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead},
	)
	if err != nil {
		return "", databaseError("begin read-only Pricing status", err)
	}
	defer func() { _ = tx.Rollback() }()
	return inspect(ctx, tx, target)
}

func inspect(ctx context.Context, tx *sql.Tx, target Target) (string, error) {
	if err := inspectPlatform(ctx, tx, target, databaseName, migratorRole); err != nil {
		return "", err
	}
	return inspectSchema(ctx, tx)
}

func inspectPlatform(
	ctx context.Context,
	tx *sql.Tx,
	target Target,
	effectiveRole, loginRole string,
) error {
	var matches bool
	// Check the relation and its owner before evaluating any SQL against the marker itself.
	err := tx.QueryRowContext(ctx, inspectPlatformQuery, effectiveRole, loginRole).
		Scan(&matches)
	if err != nil {
		return databaseError("verify protected Pricing target and role", err)
	}
	if !matches {
		return &operationError{
			"Pricing target marker or connected role differs from enrollment",
		}
	}

	err = tx.QueryRowContext(ctx, inspectPlatformMarkerQuery,
		target.Environment, target.InstanceID, target.ClusterUID, *target.SeedAllowed,
	).Scan(&matches)
	if err != nil {
		return databaseError("read Pricing target marker", err)
	}
	if !matches {
		return &operationError{"Pricing target marker differs from enrollment"}
	}

	return nil
}

func inspectSchema(ctx context.Context, tx *sql.Tx) (string, error) {
	var hasHistory, legacySchema bool
	if err := tx.QueryRowContext(ctx, inspectSchemaObjectsQuery).
		Scan(&legacySchema, &hasHistory); err != nil {
		return "", databaseError("locate Pricing migration history and legacy schema", err)
	}
	if !hasHistory {
		if legacySchema {
			return "unmanaged", nil
		}
		return "uninitialized", nil
	}
	status, err := inspectHistory(ctx, tx)
	if err != nil {
		return "", err
	}
	if err := inspectPricing(ctx, tx); err != nil {
		return "", err
	}
	return status, nil
}

func inspectHistory(ctx context.Context, tx *sql.Tx) (string, error) {
	var validHistory bool
	if err := tx.QueryRowContext(ctx, inspectHistorySchemaQuery).
		Scan(&validHistory); err != nil {
		return "", databaseError("inspect Pricing migration history schema", err)
	}
	if !validHistory {
		return "", &operationError{"invalid Pricing migration history schema"}
	}

	var status string
	if err := tx.QueryRowContext(ctx, inspectHistoryRowsQuery, schemaVersion).
		Scan(&status); err != nil {
		return "", databaseError("inspect Pricing migration history rows", err)
	}
	if status != AppliedStatus {
		return "", &operationError{"unsupported Pricing migration history"}
	}
	return status, nil
}

func inspectPricing(ctx context.Context, tx *sql.Tx) error {
	var complete bool
	if err := tx.QueryRowContext(ctx, inspectPricingSchemaQuery).Scan(&complete); err != nil {
		return databaseError("inspect recorded Pricing schema", err)
	}
	if !complete {
		return &operationError{
			"Pricing migration history differs from the installed schema",
		}
	}
	return nil
}
