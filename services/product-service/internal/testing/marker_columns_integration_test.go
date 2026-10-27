//go:build integration

package integrationtest

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/administration"
)

func TestProductMarkerColumns(t *testing.T) {
	root, err := ResolveBaselineRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	database := startSeedDatabase(t, ctx, root)
	migrationConfig, err := pgx.ParseConfig(database.readerURL)
	if err != nil {
		t.Fatal(err)
	}
	migrationConfig.User = "go_product_migrator"
	migrationConfig.Password = "synthetic-migrator-password"
	migrator := stdlib.OpenDB(*migrationConfig,
		stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, `SET ROLE product`)
			return err
		}))
	t.Cleanup(func() { _ = migrator.Close() })
	catalogPath := filepath.Join("..", "administration", "seeds", "base.json")

	for _, state := range []string{"fresh", "managed"} {
		t.Run(state, func(t *testing.T) {
			for _, scenario := range []struct {
				name, role, privilege, option, probe string
			}{
				{"owner update", "product", "UPDATE", "", `UPDATE product_admin.target_identity SET seed_allowed=false`},
				{"migrator update", "go_product_migrator", "UPDATE", "", `UPDATE product_admin.target_identity SET seed_allowed=false`},
				{"seeder update", "go_product_seeder", "UPDATE", "", `UPDATE product_admin.target_identity SET seed_allowed=false`},
				{"seeder insert", "go_product_seeder", "INSERT", "", `INSERT INTO product_admin.target_identity(seed_allowed) VALUES(false)`},
				{"reader select", "go_product_reader", "SELECT", "", `SELECT seed_allowed FROM product_admin.target_identity`},
				{"seeder grant option", "go_product_seeder", "UPDATE", " WITH GRANT OPTION", `UPDATE product_admin.target_identity SET seed_allowed=false`},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					// given
					adoptionExec(
						t,
						ctx,
						database.admin,
						`DROP TABLE IF EXISTS public.product,public.goose_db_version`,
					)
					if state == "managed" {
						if err := administration.Migrate(
							ctx,
							migrator,
							database.target,
						); err != nil {
							t.Fatal(err)
						}
						adoptionExec(
							t,
							ctx,
							database.admin,
							`INSERT INTO public.product VALUES('RETAINED','{"amount":1.2300}')`,
						)
					}
					assertMarkerColumnDenied(
						t,
						ctx,
						database.admin,
						scenario.role,
						scenario.probe,
					)
					grant := scenario.privilege + " (seed_allowed) ON product_admin.target_identity"
					role := pgx.Identifier{scenario.role}.Sanitize()
					adoptionExec(
						t,
						ctx,
						database.admin,
						"GRANT "+grant+" TO "+role+scenario.option,
					)
					t.Cleanup(func() {
						adoptionExec(
							t,
							ctx,
							database.admin,
							"REVOKE "+grant+" FROM "+role,
						)
						assertMarkerColumnDenied(
							t,
							ctx,
							database.admin,
							scenario.role,
							scenario.probe,
						)
					})
					var columnAllowed, tableAllowed bool
					if err := database.admin.QueryRowContext(
						ctx,
						`SELECT has_column_privilege($1,'product_admin.target_identity','seed_allowed',$2),
 has_table_privilege($1,'product_admin.target_identity',$2)`,
						scenario.role,
						scenario.privilege,
					).Scan(&columnAllowed, &tableAllowed); err != nil {
						t.Fatal(err)
					}
					if !columnAllowed || tableAllowed {
						t.Fatal(
							"fixture must grant only a column privilege",
						)
					}
					before := markerProtectedState(t, ctx, database.admin)

					// when
					_, statusErr := administration.Status(
						ctx,
						migrator,
						database.target,
					)
					migrationErr := administration.Migrate(
						ctx,
						migrator,
						database.target,
					)
					_, seedErr := administration.Seed(
						ctx,
						database.seeders[0],
						database.target,
						catalogPath,
					)

					// then
					for operation, operationErr := range map[string]error{
						"status": statusErr, "migration": migrationErr, "seed": seedErr,
					} {
						if operationErr == nil ||
							!strings.Contains(
								operationErr.Error(),
								"marker permissions",
							) {
							t.Errorf(
								"%s did not reject marker column grant: %v",
								operation,
								operationErr,
							)
						}
					}
					if after := markerProtectedState(
						t,
						ctx,
						database.admin,
					); after != before {
						t.Errorf(
							"rejected administration changed protected state: %s -> %s",
							before,
							after,
						)
					}
				})
			}
		})
	}
}

func assertMarkerColumnDenied(t *testing.T, ctx context.Context, db *sql.DB, role, probe string) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := tx.ExecContext(
		ctx,
		"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
	); err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, probe)
	var postgresErr *pgconn.PgError
	if !errors.As(err, &postgresErr) || postgresErr.Code != "42501" {
		t.Fatalf("%s marker probe must fail with permission denial: %v", role, err)
	}
}

func markerProtectedState(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	state := adoptionScalar(t, ctx, db, `SELECT json_build_array(
 (SELECT row(oid,relowner,relacl)::text FROM pg_class WHERE oid='product_admin.target_identity'::regclass),
 (SELECT row(singleton,environment,instance_id,cluster_uid,seed_allowed)::text FROM product_admin.target_identity),
 (SELECT string_agg(row(attnum,attacl)::text,',' ORDER BY attnum) FROM pg_attribute WHERE attrelid='product_admin.target_identity'::regclass),
 to_regclass('public.product'),to_regclass('public.goose_db_version'))::text`)
	if adoptionScalar(
		t,
		ctx,
		db,
		`SELECT (to_regclass('public.product') IS NOT NULL)::text`,
	) == "true" {
		state += adoptionScalar(
			t,
			ctx,
			db,
			`SELECT coalesce(string_agg(code||':'||definition::text,',' ORDER BY code),'') FROM public.product`,
		)
	}
	if adoptionScalar(
		t,
		ctx,
		db,
		`SELECT (to_regclass('public.goose_db_version') IS NOT NULL)::text`,
	) == "true" {
		state += adoptionScalar(
			t,
			ctx,
			db,
			`SELECT string_agg(row(id,version_id,is_applied,tstamp)::text,',' ORDER BY id) FROM public.goose_db_version`,
		)
	}
	return state
}
