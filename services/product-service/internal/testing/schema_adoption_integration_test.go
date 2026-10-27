//go:build integration

package integrationtest

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// This is a Q03 decision experiment, not a production administrator. Its synthetic owner/grant
// policy must be replaced by Q04's reviewed contract before delivery of the actual commands.
//
//go:embed testdata/schema-adoption/*.sql
var adoptionExperimentSQL embed.FS

const adoptionLockID int64 = 190001

// The checksum algorithm and expected value come from issue 131's accepted catalog inventory.
const adoptionSchemaQuery = `WITH schema_parts AS (
 SELECT format('column|%s|%s|%s|%s|%s|%s', ordinal_position, column_name, udt_name,
 COALESCE(character_maximum_length::text, ''), is_nullable, COALESCE(column_default, '')) value
 FROM information_schema.columns WHERE table_schema='public' AND table_name='product'
 UNION ALL
 SELECT format('constraint|%s|%s|%s', conname, contype, pg_get_constraintdef(oid, true))
 FROM pg_constraint WHERE conrelid=to_regclass('public.product'))
 SELECT md5(string_agg(value, E'\n' ORDER BY value)) FROM schema_parts`

// Validate under the very session lock Goose will retain across history setup and ApplyVersion. Returning
// an error after acquiring a lock requires our own unlock: Goose has not installed cleanup yet.
type adoptionExperimentLocker struct{ lock.SessionLocker }

func (l adoptionExperimentLocker) SessionLock(ctx context.Context, conn *sql.Conn) error {
	if err := l.SessionLocker.SessionLock(ctx, conn); err != nil {
		return err
	}
	if err := validateAdoptionExperiment(ctx, conn); err != nil {
		return errors.Join(err, l.SessionUnlock(context.WithoutCancel(ctx), conn))
	}
	return nil
}

func (l adoptionExperimentLocker) SessionUnlock(ctx context.Context, conn *sql.Conn) error {
	cleanup, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return l.SessionLocker.SessionUnlock(cleanup, conn)
}

func validateAdoptionExperiment(ctx context.Context, conn *sql.Conn) error {
	var product, history bool
	if err := conn.QueryRowContext(ctx, `SELECT to_regclass('public.product') IS NOT NULL,
 to_regclass('public.goose_db_version') IS NOT NULL`).Scan(&product, &history); err != nil {
		return err
	}
	if product {
		var checksum string
		if err := conn.QueryRowContext(ctx, adoptionSchemaQuery).
			Scan(&checksum); err != nil {
			return err
		}
		if checksum != "76fac669b254931c0709c236fdd7804b" {
			return errors.New("incompatible product columns or constraints")
		}
		var accepted bool
		err := conn.QueryRowContext(ctx, `SELECT
   c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity
   AND c.relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)
   AND (SELECT count(*) FROM pg_index WHERE indrelid=c.oid)=1
   AND (SELECT count(*) FROM pg_index i JOIN pg_class x ON x.oid=i.indexrelid
        JOIN pg_am a ON a.oid=x.relam WHERE i.indrelid=c.oid AND i.indisprimary
        AND i.indisvalid AND i.indisready AND i.indisunique AND a.amname='btree')=1
   AND NOT EXISTS(SELECT FROM pg_trigger WHERE tgrelid=c.oid AND NOT tgisinternal)
   AND NOT EXISTS(SELECT FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)
   AND NOT EXISTS(SELECT FROM aclexplode(COALESCE(c.relacl,acldefault('r',c.relowner)))
                  WHERE grantee<>c.relowner)
   AND NOT EXISTS(SELECT FROM pg_attribute WHERE attrelid=c.oid AND attacl IS NOT NULL)
   FROM pg_class c WHERE c.oid=to_regclass('public.product')`).Scan(&accepted)
		if err != nil {
			return err
		}
		if !accepted {
			return errors.New("incompatible product ownership or metadata")
		}
	}
	if history {
		var accepted bool
		err := conn.QueryRowContext(ctx, `SELECT
   count(*)=4 AND bool_and((column_name, data_type, is_nullable) IN (
   ('id','integer','NO'),('version_id','bigint','NO'),('is_applied','boolean','NO'),
   ('tstamp','timestamp without time zone','NO')))
   FROM information_schema.columns WHERE table_schema='public' AND table_name='goose_db_version'`).Scan(&accepted)
		if err != nil {
			return err
		}
		if !accepted {
			return errors.New("incompatible history columns")
		}
		// This baseline permits only Goose's initial zero and one successful version 1, never holes,
		// duplicate/up-down records or an unknown future version. Production must also check history
		// ownership, grants, constraints, sequence and the approved migration asset identity.
		err = conn.QueryRowContext(ctx, `SELECT count(*) IN (1,2) AND
   count(*) FILTER(WHERE version_id=0 AND is_applied)=1 AND
   count(*) FILTER(WHERE version_id=1 AND is_applied)<=1 AND
   bool_and(is_applied AND version_id IN (0,1)) AND
   (max(version_id)=0 OR $1) FROM public.goose_db_version`, product).Scan(&accepted)
		if err != nil {
			return err
		}
		if !accepted {
			return errors.New("inconsistent history")
		}
	}
	return nil
}

func newAdoptionProvider(t *testing.T, db *sql.DB, source fs.FS, guarded bool) *goose.Provider {
	t.Helper()
	locker, err := lock.NewPostgresSessionLocker(
		lock.WithLockID(adoptionLockID),
		lock.WithLockTimeout(1, 2),
		lock.WithUnlockTimeout(1, 2),
	)
	if err != nil {
		t.Fatal(err)
	}
	if guarded {
		locker = adoptionExperimentLocker{locker}
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, source,
		goose.WithDisableGlobalRegistry(true), goose.WithSessionLocker(locker))
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// ApplyVersion reaches the validating locker without Up's unlocked HasPending shortcut.
func applyAdoptionExperiment(ctx context.Context, provider *goose.Provider) error {
	_, err := provider.ApplyVersion(ctx, 1, true)
	// Accept only the single pinned Goose wrapper, never a joined error containing a cleanup failure.
	if errors.Unwrap(err) == goose.ErrAlreadyApplied {
		return nil
	}
	return err
}

func adoptionExec(t *testing.T, ctx context.Context, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, statement); err != nil {
		t.Fatal(err)
	}
}

func adoptionScalar(t *testing.T, ctx context.Context, db *sql.DB, query string) string {
	t.Helper()
	var result string
	if err := db.QueryRowContext(ctx, query).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func adoptionFingerprint(t *testing.T, ctx context.Context, db *sql.DB) []string {
	t.Helper()
	return []string{
		adoptionScalar(t, ctx, db, adoptionSchemaQuery),
		adoptionScalar(
			t,
			ctx,
			db,
			`SELECT md5(string_agg(code||':'||md5(definition::text),',' ORDER BY code)) FROM public.product`,
		),
		adoptionScalar(
			t,
			ctx,
			db,
			`SELECT row(oid,relfilenode,relowner,relacl)::text FROM pg_class WHERE oid='public.product'::regclass`,
		),
	}
}

// inspectAdoptionExperiment deliberately uses no Goose database method. A read-only transaction
// makes an accidental write fail even when the disposable test connection has owner privileges.
func inspectAdoptionExperiment(ctx context.Context, db *sql.DB) (string, error) {
	tx, err := db.BeginTx(
		ctx,
		&sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead},
	)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('public.goose_db_version') IS NOT NULL`).
		Scan(&exists); err != nil {
		return "", err
	}
	state := "uninitialized"
	if exists {
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT max(version_id) FROM public.goose_db_version`).
			Scan(&version); err != nil {
			return "", err
		}
		state = fmt.Sprintf("version=%d", version)
	}
	return state, tx.Commit()
}

func TestProductSchemaAdoption(t *testing.T) {
	// given
	if os.Getenv("PRODUCT_TEST_POSTGRES_IMAGE") != DefaultPostgresImage {
		t.Fatal("pinned PostgreSQL image required")
	}
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
		if harness.container.IsRunning() {
			t.Error("disposable container still running")
		}
	})
	db := stdlib.OpenDB(*harness.adminPool.Config().ConnConfig)
	db.SetMaxOpenConns(5)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	source, err := fs.Sub(adoptionExperimentSQL, "testdata/schema-adoption")
	if err != nil {
		t.Fatal(err)
	}
	reset := func(t *testing.T) {
		t.Helper()
		adoptionExec(t, ctx, db, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	}
	legacy := func(t *testing.T) {
		t.Helper()
		adoptionExec(
			t,
			ctx,
			db,
			`CREATE TABLE public.product(code varchar(255) PRIMARY KEY,definition jsonb NOT NULL)`,
		)
		for _, row := range fixture.Rows {
			if _, err := db.ExecContext(
				ctx,
				`INSERT INTO public.product VALUES($1,$2::jsonb)`,
				row.Code,
				row.RawLosslessDefinitionJSON,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	apply := func(t *testing.T) error {
		t.Helper()
		err := applyAdoptionExperiment(ctx, newAdoptionProvider(t, db, source, true))
		return err
	}
	history := func(t *testing.T) string {
		t.Helper()
		return adoptionScalar(
			t,
			ctx,
			db,
			`SELECT string_agg(version_id::text,',' ORDER BY id) FROM public.goose_db_version`,
		)
	}

	t.Run("fresh and sqlc schema parity", func(t *testing.T) {
		// given
		reset(t)
		if got := adoptionScalar(
			t,
			ctx,
			db,
			`SELECT (to_regclass('public.product') IS NULL)::text`,
		); got != "true" {
			t.Fatal(got)
		}
		// when
		err := apply(t)
		// then
		if err != nil {
			t.Fatal(err)
		}
		migrated := adoptionScalar(t, ctx, db, adoptionSchemaQuery)
		if migrated != fixture.SchemaIdentity.Value || history(t) != "0,1" {
			t.Fatalf("unexpected schema/history: %s", migrated)
		}
		adoptionExec(t, ctx, db, `DROP TABLE public.product`)
		schema, err := os.ReadFile("../postgres/sql/schema.sql")
		if err != nil {
			t.Fatal(err)
		}
		adoptionExec(t, ctx, db, string(schema))
		if got := adoptionScalar(t, ctx, db, adoptionSchemaQuery); got != migrated {
			t.Fatalf("sqlc schema drift: %s", got)
		}
	})
	t.Run("adoption and replay preserve catalog", func(t *testing.T) {
		// given
		reset(t)
		legacy(t)
		before := adoptionFingerprint(t, ctx, db)
		if before[1] != fixture.DataIdentity.Value {
			t.Fatal("baseline identity differs")
		}
		// when
		err := apply(t)
		// then
		if err != nil {
			t.Fatal(err)
		}
		if err := apply(t); err != nil {
			t.Fatal(err)
		}
		if after := adoptionFingerprint(t, ctx, db); !reflect.DeepEqual(before, after) {
			t.Fatalf("adoption changed Product: %v -> %v", before, after)
		}
		if history(t) != "0,1" {
			t.Fatal("replay duplicated history")
		}
	})
	t.Run("strict create rejects legacy", func(t *testing.T) {
		// given
		reset(t)
		legacy(t)
		before := adoptionFingerprint(t, ctx, db)
		strict := fstest.MapFS{
			"00001_product.sql": &fstest.MapFile{
				Data: []byte(
					"-- +goose Up\nCREATE TABLE public.product(code varchar(255) PRIMARY KEY,definition jsonb NOT NULL);\n",
				),
			},
		}
		// when
		err := applyAdoptionExperiment(ctx, newAdoptionProvider(t, db, strict, true))
		// then
		if err == nil {
			t.Fatal("strict migration accepted existing table")
		}
		if history(t) != "0" ||
			!reflect.DeepEqual(before, adoptionFingerprint(t, ctx, db)) {
			t.Fatal("failed strict creation changed catalog or stamped version 1")
		}
	})
	t.Run("schema drift is rejected before history", func(t *testing.T) {
		cases := []struct{ name, sql string }{
			{"wrong type", `ALTER TABLE public.product ALTER definition TYPE text`},
			{
				"missing primary key",
				`ALTER TABLE public.product DROP CONSTRAINT product_pkey`,
			},
			{"extra column", `ALTER TABLE public.product ADD extra text`},
			{"extra index", `CREATE INDEX unexpected ON public.product(definition)`},
			{"wrong owner", `ALTER TABLE public.product OWNER TO pg_database_owner`},
			{"public grant", `GRANT SELECT ON public.product TO PUBLIC`},
			{"row security", `ALTER TABLE public.product ENABLE ROW LEVEL SECURITY`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// given
				reset(t)
				legacy(t)
				adoptionExec(t, ctx, db, tc.sql)
				before := adoptionFingerprint(t, ctx, db)
				// when
				err := apply(t)
				// then
				if err == nil {
					t.Fatal("drift was accepted")
				}
				if got := adoptionScalar(
					t,
					ctx,
					db,
					`SELECT (to_regclass('public.goose_db_version') IS NULL)::text`,
				); got != "true" {
					t.Fatal("rejection created history")
				}
				if !reflect.DeepEqual(before, adoptionFingerprint(t, ctx, db)) {
					t.Fatal("rejection changed Product")
				}
			})
		}
	})
	t.Run("existence only mutation accepts drift", func(t *testing.T) {
		// given
		reset(t)
		legacy(t)
		adoptionExec(t, ctx, db, `ALTER TABLE public.product DROP CONSTRAINT product_pkey`)
		// when
		err := applyAdoptionExperiment(ctx, newAdoptionProvider(t, db, source, false))
		// then
		if err != nil {
			t.Fatal(err)
		}
		if history(t) != "0,1" {
			t.Fatal("mutation did not reproduce silent stamping")
		}
		if err := apply(t); err == nil {
			t.Fatal("guard failed to detect mutant's incompatible managed schema")
		}
	})
	t.Run("inconsistent history is rejected", func(t *testing.T) {
		for _, scenario := range []struct{ name, statement string }{
			{"unknown version", `INSERT INTO public.goose_db_version(version_id,is_applied) VALUES(99,true)`},
			{"duplicate version", `INSERT INTO public.goose_db_version(version_id,is_applied) VALUES(1,true)`},
			{"unapplied version", `UPDATE public.goose_db_version SET is_applied=false WHERE version_id=1`},
			{"missing zero", `DELETE FROM public.goose_db_version WHERE version_id=0`},
			{"missing product", `DROP TABLE public.product`},
			{"wrong history column", `ALTER TABLE public.goose_db_version ALTER version_id TYPE text`},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				// given
				reset(t)
				legacy(t)
				if err := apply(t); err != nil {
					t.Fatal(err)
				}
				adoptionExec(t, ctx, db, scenario.statement)
				before := adoptionScalar(
					t,
					ctx,
					db,
					`SELECT md5(string_agg(row(id,version_id,is_applied,tstamp)::text,',' ORDER BY id)) FROM public.goose_db_version`,
				)
				// when
				err := apply(t)
				// then
				if err == nil {
					t.Fatal("inconsistent history accepted")
				}
				if got := adoptionScalar(
					t,
					ctx,
					db,
					`SELECT md5(string_agg(row(id,version_id,is_applied,tstamp)::text,',' ORDER BY id)) FROM public.goose_db_version`,
				); got != before {
					t.Fatal("history changed on rejection")
				}
			})
		}
	})
	t.Run("statement failure preserves populated catalog", func(t *testing.T) {
		// given
		reset(t)
		legacy(t)
		before := adoptionFingerprint(t, ctx, db)
		contents, err := fs.ReadFile(source, "00001_product.sql")
		if err != nil {
			t.Fatal(err)
		}
		broken := fstest.MapFS{"00001_product.sql": &fstest.MapFile{Data: append(
			contents,
			[]byte(
				"\nALTER TABLE public.product ADD temporary_column text; SELECT 1/0;\n",
			)...)}}
		// when
		err = applyAdoptionExperiment(ctx, newAdoptionProvider(t, db, broken, true))
		// then
		if err == nil {
			t.Fatal("injected SQL failure succeeded")
		}
		if history(t) != "0" ||
			!reflect.DeepEqual(before, adoptionFingerprint(t, ctx, db)) {
			t.Fatal("failed migration changed populated Product or applied history")
		}
		if err := apply(t); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("Goose Up bypasses validation on replay", func(t *testing.T) {
		// given
		reset(t)
		legacy(t)
		if err := apply(t); err != nil {
			t.Fatal(err)
		}
		adoptionExec(t, ctx, db, `ALTER TABLE public.product DROP CONSTRAINT product_pkey`)
		// when
		_, err := newAdoptionProvider(t, db, source, true).Up(ctx)
		// then
		if err != nil {
			t.Fatalf("expected Up shortcut to demonstrate the hazard: %v", err)
		}
		if err := apply(t); err == nil {
			t.Fatal("ApplyVersion did not validate replay")
		}
	})
	t.Run("simultaneous runners serialize", func(t *testing.T) {
		// given
		reset(t)
		legacy(t)
		before := adoptionFingerprint(t, ctx, db)
		first := newAdoptionProvider(t, db, source, true)
		second := newAdoptionProvider(t, db, source, true)
		start := make(chan struct{})
		results := make(chan error, 2)
		// when
		for _, provider := range []*goose.Provider{first, second} {
			go func() { <-start; err := applyAdoptionExperiment(ctx, provider); results <- err }()
		}
		close(start)
		// then
		var failures []error
		for range 2 {
			if err := <-results; err != nil {
				failures = append(failures, err)
			}
		}
		if err := errors.Join(failures...); err != nil {
			t.Fatal(err)
		}
		if history(t) != "0,1" ||
			!reflect.DeepEqual(before, adoptionFingerprint(t, ctx, db)) {
			t.Fatal("concurrent migration changed catalog or duplicated history")
		}
	})
	t.Run("cancellation rolls back and retries", func(t *testing.T) {
		// given
		reset(t)
		contents, err := fs.ReadFile(source, "00001_product.sql")
		if err != nil {
			t.Fatal(err)
		}
		slow := fstest.MapFS{
			"00001_product.sql": &fstest.MapFile{
				Data: append(contents, []byte("\nSELECT pg_sleep(10);\n")...),
			},
		}
		bounded, stop := context.WithTimeout(ctx, 300*time.Millisecond)
		defer stop()
		// when
		err = applyAdoptionExperiment(bounded, newAdoptionProvider(t, db, slow, true))
		// then
		if err == nil || !errors.Is(bounded.Err(), context.DeadlineExceeded) {
			t.Fatalf("expected deadline: %v", err)
		}
		if history(t) != "0" ||
			adoptionScalar(
				t,
				ctx,
				db,
				`SELECT (to_regclass('public.product') IS NULL)::text`,
			) != "true" {
			t.Fatal("failed transaction left Product or applied history")
		}
		if err := apply(t); err != nil {
			t.Fatal(err)
		}
		if history(t) != "0,1" {
			t.Fatal("recovery failed")
		}
	})
	t.Run("lock contention is bounded", func(t *testing.T) {
		// given
		reset(t)
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.ExecContext(
			ctx,
			`SELECT pg_advisory_lock($1)`,
			adoptionLockID,
		); err != nil {
			t.Fatal(err)
		}
		bounded, stop := context.WithTimeout(ctx, 100*time.Millisecond)
		defer stop()
		// when
		err = applyAdoptionExperiment(bounded, newAdoptionProvider(t, db, source, true))
		// then
		if err == nil || !errors.Is(bounded.Err(), context.DeadlineExceeded) {
			t.Fatalf("expected bounded contention: %v", err)
		}
		if adoptionScalar(
			t,
			ctx,
			db,
			`SELECT (to_regclass('public.goose_db_version') IS NULL)::text`,
		) != "true" {
			t.Fatal("blocked runner created history")
		}
		if _, err := conn.ExecContext(
			ctx,
			`SELECT pg_advisory_unlock($1)`,
			adoptionLockID,
		); err != nil {
			t.Fatal(err)
		}
		if err := apply(t); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("inspection has no writes", func(t *testing.T) {
		// given
		reset(t)
		// when
		state, err := inspectAdoptionExperiment(ctx, db)
		// then
		if err != nil || state != "uninitialized" {
			t.Fatalf("inspection: %s %v", state, err)
		}
		if adoptionScalar(
			t,
			ctx,
			db,
			`SELECT count(*)::text FROM pg_class WHERE relnamespace='public'::regnamespace`,
		) != "0" {
			t.Fatal("inspection created metadata")
		}
		legacy(t)
		if err := apply(t); err != nil {
			t.Fatal(err)
		}
		before := adoptionFingerprint(t, ctx, db)
		state, err = inspectAdoptionExperiment(ctx, db)
		if err != nil || state != "version=1" || history(t) != "0,1" ||
			!reflect.DeepEqual(before, adoptionFingerprint(t, ctx, db)) {
			t.Fatalf("managed inspection: %s %v", state, err)
		}
	})
	t.Run("Goose inspection initializes history", func(t *testing.T) {
		methods := []struct {
			name string
			call func(*goose.Provider) error
		}{
			{
				"status",
				func(p *goose.Provider) error { _, err := p.Status(ctx); return err },
			},
			{
				"version",
				func(p *goose.Provider) error { _, err := p.GetDBVersion(ctx); return err },
			},
			{
				"versions",
				func(p *goose.Provider) error { _, _, err := p.GetVersions(ctx); return err },
			},
			{
				"pending",
				func(p *goose.Provider) error { _, err := p.HasPending(ctx); return err },
			},
		}
		for _, method := range methods {
			t.Run(method.name, func(t *testing.T) {
				// given
				reset(t)
				p := newAdoptionProvider(t, db, source, true)
				// when
				err := method.call(p)
				// then
				if err != nil {
					t.Fatal(err)
				}
				if history(t) != "0" {
					t.Fatal("expected Goose initialization side effect")
				}
			})
		}
	})
}
