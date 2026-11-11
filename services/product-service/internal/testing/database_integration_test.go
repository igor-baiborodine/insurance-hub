//go:build integration

package integrationtest

import (
	"context"
	"database/sql"
	"testing"
)

// The checksum query and expected value come from issue 131's accepted catalog inventory.
const productSchemaQuery = `WITH schema_parts AS (
 SELECT format('column|%s|%s|%s|%s|%s|%s', ordinal_position, column_name, udt_name,
 COALESCE(character_maximum_length::text, ''), is_nullable, COALESCE(column_default, '')) value
 FROM information_schema.columns WHERE table_schema='public' AND table_name='product'
 UNION ALL
 SELECT format('constraint|%s|%s|%s', conname, contype, pg_get_constraintdef(oid, true))
 FROM pg_constraint WHERE conrelid=to_regclass('public.product'))
 SELECT md5(string_agg(value, E'\n' ORDER BY value)) FROM schema_parts`

func execSQL(t *testing.T, ctx context.Context, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, statement); err != nil {
		t.Fatal(err)
	}
}

func queryScalar(t *testing.T, ctx context.Context, db *sql.DB, query string) string {
	t.Helper()
	var result string
	if err := db.QueryRowContext(ctx, query).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
