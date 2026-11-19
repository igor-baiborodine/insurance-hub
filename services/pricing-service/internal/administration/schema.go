package administration

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed queries/inspect-platform.sql
var targetSQL string

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
	var matches bool
	// Check the relation and its owner before evaluating any SQL against the marker itself.
	err := tx.QueryRowContext(ctx, targetSQL).Scan(&matches)
	if err != nil {
		return "", databaseError("verify protected Pricing target and role", err)
	}
	if !matches {
		return "", &operationError{
			"Pricing target marker or connected role differs from enrollment",
		}
	}

	err = tx.QueryRowContext(ctx, `SELECT count(*) = 1 AND coalesce(bool_and(
		singleton AND environment = $1 AND instance_id = $2::uuid
		AND cluster_uid = $3 AND seed_allowed = $4), false)
		FROM pricing_admin.target_identity`,
		target.Environment, target.InstanceID, target.ClusterUID, *target.SeedAllowed,
	).Scan(&matches)
	if err != nil {
		return "", databaseError("read Pricing target marker", err)
	}
	if !matches {
		return "", &operationError{"Pricing target marker differs from enrollment"}
	}

	var hasHistory bool
	if err := tx.QueryRowContext(ctx,
		"SELECT to_regclass('public.pricing_schema_version') IS NOT NULL",
	).Scan(&hasHistory); err != nil {
		return "", databaseError("locate Pricing migration history", err)
	}
	if !hasHistory {
		var legacySchema bool
		if err := tx.QueryRowContext(ctx, `SELECT
			to_regclass('public.tariff') IS NOT NULL
			OR to_regclass('public.base_price_rules') IS NOT NULL
			OR to_regclass('public.discount_markup_rule') IS NOT NULL
			OR to_regclass('public.hibernate_sequence') IS NOT NULL`).Scan(&legacySchema); err != nil {
			return "", databaseError("inspect unmanaged Pricing schema", err)
		}
		if legacySchema {
			return "unmanaged", nil
		}
		return "uninitialized", nil
	}
	var count, version int
	if err := tx.QueryRowContext(ctx, "SELECT count(*), coalesce(max(version_id), 0) "+
		"FROM public.pricing_schema_version").Scan(&count, &version); err != nil {
		return "", databaseError("read Pricing migration history", err)
	}
	if count != 1 || version != schemaVersion {
		return "", &operationError{"unsupported Pricing migration history"}
	}

	var complete bool
	if err := tx.QueryRowContext(ctx, `SELECT count(*) = 2 FROM pg_attribute
		WHERE attrelid IN (to_regclass('public.base_price_rules'),
		                  to_regclass('public.discount_markup_rule'))
		  AND attname = 'position' AND atttypid = 'integer'::regtype AND NOT attisdropped
		  AND to_regclass('public.tariff') IS NOT NULL
		  AND to_regclass('public.hibernate_sequence') IS NOT NULL`).Scan(&complete); err != nil {
		return "", databaseError("inspect recorded Pricing schema", err)
	}
	if !complete {
		return "", &operationError{
			"Pricing migration history differs from the installed schema",
		}
	}
	return AppliedStatus, nil
}
