//go:build integration

package integrationtest

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/administration"
)

func TestProductMigration(t *testing.T) {
	root, err := ResolveBaselineRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := LoadCatalog(root, FixtureQA)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	harness, err := Start(ctx, Options{
		FixtureRoot: root, Image: DefaultPostgresImage,
		provision: func(context.Context, *pgxpool.Pool, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := harness.Close(); err != nil {
			t.Error(err)
		}
	})
	admin := stdlib.OpenDB(*harness.adminPool.Config().ConnConfig)
	t.Cleanup(func() { _ = admin.Close() })
	setup := []string{
		`CREATE ROLE product LOGIN NOINHERIT`,
		`CREATE ROLE postgres NOLOGIN`,
		`CREATE ROLE go_product_migrator LOGIN NOINHERIT PASSWORD 'synthetic-migrator-password'`,
		`CREATE ROLE go_product_reader LOGIN NOINHERIT PASSWORD 'synthetic-reader-password'`,
		`GRANT product TO go_product_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE`,
		`CREATE DATABASE product OWNER product`,
		`REVOKE ALL ON DATABASE product FROM PUBLIC`,
		`GRANT CONNECT ON DATABASE product TO go_product_migrator,go_product_reader`,
	}
	for _, query := range setup {
		if _, err := admin.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	adminConfig := *harness.adminPool.Config().ConnConfig
	adminConfig.Database = "product"
	productAdmin := stdlib.OpenDB(adminConfig)
	t.Cleanup(func() { _ = productAdmin.Close() })
	for _, query := range []string{
		`CREATE SCHEMA product_admin AUTHORIZATION postgres`,
		`SET ROLE postgres`,
		`CREATE TABLE product_admin.target_identity(singleton boolean PRIMARY KEY CHECK(singleton),
environment text NOT NULL, instance_id uuid NOT NULL, cluster_uid text NOT NULL, seed_allowed boolean NOT NULL)`,
		`RESET ROLE`,
		`INSERT INTO product_admin.target_identity VALUES(true,'disposable','00000000-0000-4000-8000-000000000190','synthetic-cluster-uid',false)`,
		`REVOKE ALL ON SCHEMA product_admin FROM PUBLIC`,
		`GRANT USAGE ON SCHEMA product_admin TO product,go_product_migrator`,
		`GRANT SELECT ON product_admin.target_identity TO product,go_product_migrator`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE ALL ON TABLES FROM PUBLIC`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product IN SCHEMA public REVOKE ALL ON TABLES FROM PUBLIC`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE ALL ON SEQUENCES FROM PUBLIC`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC`,
	} {
		if _, err := productAdmin.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	migratorConfig := adminConfig
	migratorConfig.User = "go_product_migrator"
	migratorConfig.Password = "synthetic-migrator-password"
	migrator := stdlib.OpenDB(migratorConfig,
		stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, `SET ROLE product`)
			return err
		}))
	migrator.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = migrator.Close() })
	readerConfig := adminConfig
	readerConfig.User = "go_product_reader"
	readerConfig.Password = "synthetic-reader-password"
	reader := stdlib.OpenDB(readerConfig)
	t.Cleanup(func() { _ = reader.Close() })
	target := administration.Target{
		Environment:      "disposable",
		Context:          "synthetic-owned",
		DataNamespace:    "owned",
		ServiceNamespace: "owned",
		Cluster:          "synthetic",
		ClusterUID:       "synthetic-cluster-uid",
		InstanceID:       "00000000-0000-4000-8000-000000000190",
		Database:         "product",
		Host:             "localhost",
		Disposable:       true,
	}
	reset := func(t *testing.T) {
		t.Helper()
		for _, query := range []string{
			`DROP SCHEMA public CASCADE`,
			`CREATE SCHEMA public AUTHORIZATION product`,
			`REVOKE ALL ON SCHEMA public FROM PUBLIC`,
			`GRANT USAGE ON SCHEMA public TO go_product_reader`,
		} {
			if _, err := productAdmin.ExecContext(ctx, query); err != nil {
				t.Fatal(err)
			}
		}
	}
	legacy := func(t *testing.T) {
		t.Helper()
		conn, err := productAdmin.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.ExecContext(ctx, `SET ROLE product`); err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = conn.ExecContext(ctx, `RESET ROLE`) }()
		if _, err := conn.ExecContext(
			ctx,
			`CREATE TABLE public.product(code varchar(255) PRIMARY KEY, definition jsonb NOT NULL)`,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(
			ctx,
			`GRANT SELECT ON public.product TO go_product_reader`,
		); err != nil {
			t.Fatal(err)
		}
		for _, row := range fixture.Rows {
			if _, err := conn.ExecContext(
				ctx,
				`INSERT INTO public.product VALUES($1,$2::jsonb)`,
				row.Code,
				row.RawLosslessDefinitionJSON,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	productFingerprint := func(t *testing.T) []string {
		t.Helper()
		return []string{
			adoptionScalar(t, ctx, productAdmin, adoptionSchemaQuery),
			adoptionScalar(
				t,
				ctx,
				productAdmin,
				`SELECT md5(string_agg(code||':'||md5(definition::text),',' ORDER BY code)) FROM public.product`,
			),
			adoptionScalar(
				t,
				ctx,
				productAdmin,
				`SELECT row(oid,relfilenode,relowner)::text FROM pg_class WHERE oid='public.product'::regclass`,
			),
		}
	}
	t.Run("fresh schema and read-only status", func(t *testing.T) {
		// given
		reset(t)

		// when
		state, err := administration.Status(ctx, migrator, target)

		// then
		if err != nil || state != "uninitialized" {
			t.Fatalf("unexpected initial status %q: %v", state, err)
		}
		if got := adoptionScalar(
			t,
			ctx,
			productAdmin,
			`SELECT (to_regclass('public.goose_db_version') IS NULL)::text`,
		); got != "true" {
			t.Fatal("status created history")
		}
		if err := administration.Migrate(ctx, migrator, target); err != nil {
			t.Fatal(err)
		}
		var rowCount int
		if err := reader.QueryRowContext(ctx, `SELECT count(*) FROM public.product`).
			Scan(&rowCount); err != nil ||
			rowCount != 0 {
			t.Fatalf("reader cannot inspect fresh table: %v", err)
		}
		if _, err := reader.ExecContext(
			ctx,
			`INSERT INTO public.product VALUES('DENIED','{}')`,
		); err == nil {
			t.Fatal("reader write was permitted")
		}
		if got := adoptionScalar(
			t,
			ctx,
			productAdmin,
			adoptionSchemaQuery,
		); got != fixture.SchemaIdentity.Value {
			t.Fatal("migration differs from accepted Product schema")
		}
		if state, err = administration.Status(
			ctx,
			migrator,
			target,
		); err != nil ||
			state != "version=1" {
			t.Fatalf("unexpected managed status %q: %v", state, err)
		}
		if _, err := productAdmin.ExecContext(ctx,
			`GRANT INSERT ON public.product TO go_product_reader`); err != nil {
			t.Fatal(err)
		}
		if _, err := administration.Status(ctx, migrator, target); err == nil {
			t.Fatal("reader privilege escalation was accepted")
		}
		if _, err := productAdmin.ExecContext(ctx,
			`REVOKE INSERT ON public.product FROM go_product_reader`); err != nil {
			t.Fatal(err)
		}
		schema, err := os.ReadFile("../postgres/sql/schema.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := productAdmin.ExecContext(
			ctx,
			`DROP TABLE public.product`,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := productAdmin.ExecContext(ctx, string(schema)); err != nil {
			t.Fatal(err)
		}
		if got := adoptionScalar(
			t,
			ctx,
			productAdmin,
			adoptionSchemaQuery,
		); got != fixture.SchemaIdentity.Value {
			t.Fatal("migration and sqlc schema differ")
		}
	})
	t.Run("adoption and replay preserve modified catalog", func(t *testing.T) {
		// given
		reset(t)
		legacy(t)
		if state, err := administration.Status(
			ctx,
			migrator,
			target,
		); err != nil ||
			state != "unmanaged" {
			t.Fatalf("unexpected unmanaged status %q: %v", state, err)
		}
		if _, err := productAdmin.ExecContext(
			ctx,
			`UPDATE public.product SET definition='{"modified":true,"amount":1.2300}' WHERE code='CAR'`,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := productAdmin.ExecContext(
			ctx,
			`INSERT INTO public.product VALUES('EXTRA','{"retained":true}')`,
		); err != nil {
			t.Fatal(err)
		}
		before := productFingerprint(t)

		// when
		if err := administration.Migrate(ctx, migrator, target); err != nil {
			t.Fatal(err)
		}
		if err := administration.Migrate(ctx, migrator, target); err != nil {
			t.Fatal(err)
		}

		// then
		if after := productFingerprint(t); !reflect.DeepEqual(before, after) {
			t.Fatalf("adoption changed Product: %v -> %v", before, after)
		}
		if got := adoptionScalar(
			t,
			ctx,
			productAdmin,
			`SELECT string_agg(version_id::text,',' ORDER BY id) FROM public.goose_db_version`,
		); got != "0,1" {
			t.Fatalf("unexpected history %s", got)
		}
	})
	t.Run("target mismatch makes no history", func(t *testing.T) {
		// given
		reset(t)
		if _, err := productAdmin.ExecContext(
			ctx,
			`UPDATE product_admin.target_identity SET instance_id='00000000-0000-4000-8000-000000000191'`,
		); err != nil {
			t.Fatal(err)
		}

		// when
		if _, err := administration.Status(ctx, migrator, target); err == nil {
			t.Fatal("mismatched target was inspected as valid")
		}
		if err := administration.Migrate(ctx, migrator, target); err == nil {
			t.Fatal("mismatched target was migrated")
		}

		// then
		if got := adoptionScalar(
			t,
			ctx,
			productAdmin,
			`SELECT (to_regclass('public.goose_db_version') IS NULL)::text`,
		); got != "true" {
			t.Fatal("target rejection created history")
		}
		if _, err := productAdmin.ExecContext(
			ctx,
			`UPDATE product_admin.target_identity SET instance_id='00000000-0000-4000-8000-000000000190'`,
		); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("managed history drift is rejected", func(t *testing.T) {
		// given
		reset(t)
		if err := administration.Migrate(ctx, migrator, target); err != nil {
			t.Fatal(err)
		}
		if _, err := productAdmin.ExecContext(
			ctx,
			`UPDATE public.goose_db_version SET is_applied=false WHERE version_id=1`,
		); err != nil {
			t.Fatal(err)
		}

		// when
		if _, err := administration.Status(ctx, migrator, target); err == nil {
			t.Fatal("drifted history reported a version")
		}
		if err := administration.Migrate(ctx, migrator, target); err == nil {
			t.Fatal("drifted history was accepted")
		}

		// then
		if got := adoptionScalar(
			t,
			ctx,
			productAdmin,
			`SELECT string_agg(version_id::text||':'||is_applied::text,',' ORDER BY id) FROM public.goose_db_version`,
		); got != "0:true,1:false" {
			t.Fatal("rejection changed history")
		}
	})
	t.Run("failed migration retains zero history and retries", func(t *testing.T) {
		// given
		reset(t)
		for _, query := range []string{
			`CREATE FUNCTION product_admin.reject_product_create() RETURNS event_trigger
LANGUAGE plpgsql AS $$ BEGIN
IF EXISTS(SELECT FROM pg_event_trigger_ddl_commands() WHERE object_identity='public.product') THEN
 RAISE EXCEPTION 'synthetic Product DDL failure';
END IF;
END $$`,
			`CREATE EVENT TRIGGER reject_product_create ON ddl_command_end
WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION product_admin.reject_product_create()`,
		} {
			if _, err := productAdmin.ExecContext(ctx, query); err != nil {
				t.Fatal(err)
			}
		}

		// when
		if err := administration.Migrate(ctx, migrator, target); err == nil {
			t.Fatal("injected Product DDL failure was ignored")
		}

		// then
		if got := adoptionScalar(
			t,
			ctx,
			productAdmin,
			`SELECT string_agg(version_id::text,',' ORDER BY id) FROM public.goose_db_version`,
		); got != "0" {
			t.Fatalf("failed migration stamped history: %s", got)
		}
		if got := adoptionScalar(t, ctx, productAdmin,
			`SELECT (to_regclass('public.product') IS NULL)::text`); got != "true" {
			t.Fatal("failed Product DDL was retained")
		}
		for _, query := range []string{
			`DROP EVENT TRIGGER reject_product_create`,
			`DROP FUNCTION product_admin.reject_product_create()`,
		} {
			if _, err := productAdmin.ExecContext(ctx, query); err != nil {
				t.Fatal(err)
			}
		}
		if state, err := administration.Status(
			ctx,
			migrator,
			target,
		); err != nil ||
			state != "pending" {
			t.Fatalf("zero-history recovery state lost: %q, %v", state, err)
		}
		if err := administration.Migrate(ctx, migrator, target); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("enrolled production migrates without seed role", func(t *testing.T) {
		// given
		reset(t)
		if _, err := productAdmin.ExecContext(
			ctx,
			`UPDATE product_admin.target_identity SET environment='production'`,
		); err != nil {
			t.Fatal(err)
		}
		production := target
		production.Environment = "production"

		// when
		if err := administration.Migrate(ctx, migrator, production); err != nil {
			t.Fatal(err)
		}

		// then
		if state, err := administration.Status(
			ctx,
			migrator,
			production,
		); err != nil ||
			state != "version=1" {
			t.Fatalf("production inspection failed %q: %v", state, err)
		}
		if _, err := productAdmin.ExecContext(
			ctx,
			`UPDATE product_admin.target_identity SET environment='disposable'`,
		); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("schema drift creates no history", func(t *testing.T) {
		// given
		reset(t)
		legacy(t)
		if _, err := productAdmin.ExecContext(
			ctx,
			`ALTER TABLE public.product DROP CONSTRAINT product_pkey`,
		); err != nil {
			t.Fatal(err)
		}

		// when
		if err := administration.Migrate(ctx, migrator, target); err == nil {
			t.Fatal("schema drift accepted")
		}

		// then
		if got := adoptionScalar(
			t,
			ctx,
			productAdmin,
			`SELECT (to_regclass('public.goose_db_version') IS NULL)::text`,
		); got != "true" {
			t.Fatal("drift rejection created history")
		}
	})
	t.Run("simultaneous runners and cancelled lock", func(t *testing.T) {
		// given
		reset(t)
		results := make(chan error, 2)

		// when
		for range 2 {
			go func() { results <- administration.Migrate(ctx, migrator, target) }()
		}
		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}

		// then
		if got := adoptionScalar(
			t,
			ctx,
			productAdmin,
			`SELECT string_agg(version_id::text,',' ORDER BY id) FROM public.goose_db_version`,
		); got != "0,1" {
			t.Fatal("concurrent migration duplicated history")
		}
		conn, err := productAdmin.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(190001)`); err != nil {
			t.Fatal(err)
		}
		bounded, stop := context.WithTimeout(ctx, 100*time.Millisecond)
		defer stop()
		if err := administration.Migrate(bounded, migrator, target); err == nil ||
			!errors.Is(bounded.Err(), context.DeadlineExceeded) {
			t.Fatal("contended migration was not cancelled")
		}
		if _, err := conn.ExecContext(
			ctx,
			`SELECT pg_advisory_unlock(190001)`,
		); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("enrolled seed role receives only explicit managed reads", func(t *testing.T) {
		// given
		reset(t)
		for _, query := range []string{
			`CREATE ROLE go_product_seeder LOGIN NOINHERIT PASSWORD 'synthetic-seed-password'`,
			`GRANT CONNECT ON DATABASE product TO go_product_seeder`,
			`GRANT USAGE ON SCHEMA public,product_admin TO go_product_seeder`,
			`GRANT SELECT ON product_admin.target_identity TO go_product_seeder`,
			`UPDATE product_admin.target_identity SET seed_allowed=true`,
		} {
			if _, err := productAdmin.ExecContext(ctx, query); err != nil {
				t.Fatal(err)
			}
		}
		seededTarget := target
		seededTarget.SeedAllowed = true
		if err := administration.Migrate(ctx, migrator, seededTarget); err != nil {
			t.Fatal(err)
		}
		seedConfig := adminConfig
		seedConfig.User = "go_product_seeder"
		seedConfig.Password = "synthetic-seed-password"
		seed := stdlib.OpenDB(seedConfig)
		defer func() { _ = seed.Close() }()

		// when
		var version int
		versionErr := seed.QueryRowContext(
			ctx,
			`SELECT max(version_id) FROM public.goose_db_version`,
		).Scan(&version)
		_, historyWriteErr := seed.ExecContext(
			ctx,
			`UPDATE public.goose_db_version SET version_id=9`,
		)
		_, insertErr := seed.ExecContext(
			ctx,
			`INSERT INTO public.product VALUES('NEW','{}')`,
		)
		_, updateErr := seed.ExecContext(
			ctx,
			`UPDATE public.product SET definition='{}'`,
		)

		// then
		if versionErr != nil || version != 1 {
			t.Fatalf("seed role cannot inspect managed history: %v", versionErr)
		}
		if historyWriteErr == nil {
			t.Fatal("seed role changed migration history")
		}
		if insertErr != nil {
			t.Fatalf("seed role lacks insert privilege: %v", insertErr)
		}
		if updateErr == nil {
			t.Fatal("seed role changed existing Product rows")
		}
	})
}
