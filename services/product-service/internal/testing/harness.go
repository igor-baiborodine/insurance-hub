package integrationtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// DefaultPostgresImage is the reproducible database image used by Product integration tests.
	DefaultPostgresImage = "postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193"

	adminRole        = "product_test_admin"
	databaseName     = "product_test"
	runtimeRole      = "go_product_reader"
	resourceTimeout  = 30 * time.Second
	containerStartup = 60 * time.Second
	credentialBytes  = 24
)

// Options selects the fixture source and pinned PostgreSQL image.
type Options struct {
	FixtureRoot string
	Image       string

	provision          func(context.Context, *pgxpool.Pool, string) error
	onContainerStarted func(*postgres.PostgresContainer)
}

// Harness owns one disposable PostgreSQL container and its setup-only connection.
type Harness struct {
	container   *postgres.PostgresContainer
	adminPool   *pgxpool.Pool
	runtimeURL  string
	fixtureRoot string

	closeOnce sync.Once
	closeErr  error
}

// Snapshot is the accepted schema/data identity observed through the restricted reader role.
type Snapshot struct {
	RowCount       int
	DataIdentity   string
	SchemaIdentity string
	Codes          []string
}

// RoleInspection records the runtime database identity and effective privileges.
type RoleInspection struct {
	CurrentUser         string
	Superuser           bool
	CreateDatabase      bool
	CreateRole          bool
	Inherit             bool
	Replication         bool
	BypassRLS           bool
	CanConnect          bool
	CanCreateInDatabase bool
	CanUseSchema        bool
	CanCreateInSchema   bool
	CanSelect           bool
	CanInsert           bool
	CanUpdate           bool
	CanDelete           bool
	CanTruncate         bool
}

// Start creates the isolated database, exact legacy table, and restricted reader identity.
func Start(ctx context.Context, options Options) (*Harness, error) {
	if _, exists := os.LookupEnv("PRODUCT_DATABASE_URL"); exists {
		return nil, errors.New(
			"start PostgreSQL harness: PRODUCT_DATABASE_URL must be unset",
		)
	}
	if options.Image == "" {
		return nil, errors.New("start PostgreSQL harness: image is required")
	}
	if !directory(options.FixtureRoot) {
		return nil, errors.New("start PostgreSQL harness: fixture root is missing")
	}

	adminPassword, err := randomCredential()
	if err != nil {
		return nil, fmt.Errorf("start PostgreSQL harness: create admin credential: %w", err)
	}
	readerPassword, err := randomCredential()
	if err != nil {
		return nil, fmt.Errorf(
			"start PostgreSQL harness: create reader credential: %w",
			err,
		)
	}

	startupCtx, cancel := context.WithTimeout(ctx, containerStartup)
	defer cancel()
	container, err := postgres.Run(
		startupCtx,
		options.Image,
		postgres.WithDatabase(databaseName),
		postgres.WithUsername(adminRole),
		postgres.WithPassword(adminPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(containerStartup),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("start PostgreSQL harness: run container: %w", err)
	}
	if options.onContainerStarted != nil {
		options.onContainerStarted(container)
	}

	harness, setupErr := configureHarness(ctx, container, options, readerPassword)
	if setupErr != nil {
		cleanupErr := terminate(container)
		return nil, errors.Join(setupErr, cleanupErr)
	}
	return harness, nil
}

func configureHarness(
	ctx context.Context,
	container *postgres.PostgresContainer,
	options Options,
	readerPassword string,
) (*Harness, error) {
	adminURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf(
			"start PostgreSQL harness: resolve admin connection: %w",
			err,
		)
	}
	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		return nil, fmt.Errorf("start PostgreSQL harness: open admin pool: %w", err)
	}

	provision := provisionDatabase
	if options.provision != nil {
		provision = options.provision
	}
	if err := provision(ctx, adminPool, readerPassword); err != nil {
		adminPool.Close()
		return nil, fmt.Errorf("start PostgreSQL harness: provision database: %w", err)
	}
	runtimeURL, err := connectionURL(adminURL, runtimeRole, readerPassword)
	if err != nil {
		adminPool.Close()
		return nil, fmt.Errorf(
			"start PostgreSQL harness: create reader connection: %w",
			err,
		)
	}
	return &Harness{
		container:   container,
		adminPool:   adminPool,
		runtimeURL:  runtimeURL,
		fixtureRoot: options.FixtureRoot,
	}, nil
}

func provisionDatabase(ctx context.Context, pool *pgxpool.Pool, readerPassword string) error {
	transaction, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin setup transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()

	statements := []string{
		"CREATE TABLE public.product (" +
			"code varchar(255) PRIMARY KEY, definition jsonb NOT NULL)",
		"REVOKE ALL ON DATABASE " + databaseName + " FROM PUBLIC",
		"REVOKE ALL ON SCHEMA public FROM PUBLIC",
		"REVOKE ALL ON ALL TABLES IN SCHEMA public FROM PUBLIC",
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + adminRole +
			" IN SCHEMA public REVOKE ALL ON TABLES FROM PUBLIC",
		"CREATE ROLE " + runtimeRole +
			" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION PASSWORD '" +
			readerPassword + "'",
		"GRANT CONNECT ON DATABASE " + databaseName + " TO " + runtimeRole,
		"GRANT USAGE ON SCHEMA public TO " + runtimeRole,
		"GRANT SELECT ON TABLE public.product TO " + runtimeRole,
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(ctx, statement); err != nil {
			return fmt.Errorf("execute setup statement: %w", err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit setup transaction: %w", err)
	}
	return nil
}

// RuntimeDatabaseURL returns the restricted reader URL for future service adapter tests.
func (harness *Harness) RuntimeDatabaseURL() string {
	return harness.runtimeURL
}

// LoadCatalog replaces catalog rows using one immutable authoritative fixture.
func (harness *Harness) LoadCatalog(
	ctx context.Context,
	fixtureSet FixtureSet,
) (CatalogFixture, error) {
	fixture, err := LoadCatalog(harness.fixtureRoot, fixtureSet)
	if err != nil {
		return CatalogFixture{}, err
	}
	if err := harness.ResetRows(ctx, fixture.Rows); err != nil {
		return CatalogFixture{}, fmt.Errorf("load catalog %s: %w", fixtureSet, err)
	}
	return fixture, nil
}

// ResetRows atomically replaces test-owned catalog rows without exposing setup access.
func (harness *Harness) ResetRows(ctx context.Context, rows []FixtureRow) error {
	transaction, err := harness.adminPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("reset rows: begin: %w", err)
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	if _, err := transaction.Exec(ctx, "TRUNCATE TABLE public.product"); err != nil {
		return fmt.Errorf("reset rows: truncate: %w", err)
	}
	for _, row := range rows {
		if row.Code == "" || row.RawLosslessDefinitionJSON == "" {
			return errors.New("reset rows: code and definition are required")
		}
		if _, err := transaction.Exec(
			ctx,
			"INSERT INTO public.product (code, definition) VALUES ($1, $2::jsonb)",
			row.Code,
			row.RawLosslessDefinitionJSON,
		); err != nil {
			return fmt.Errorf("reset rows: insert %s: %w", row.Code, err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("reset rows: commit: %w", err)
	}
	return nil
}

// Snapshot reads schema and catalog identities through the restricted runtime role.
func (harness *Harness) Snapshot(ctx context.Context) (Snapshot, error) {
	pool, err := pgxpool.New(ctx, harness.runtimeURL)
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: open reader pool: %w", err)
	}
	defer pool.Close()

	var snapshot Snapshot
	var dataIdentity *string
	err = pool.QueryRow(
		ctx,
		"SELECT count(*)::integer, "+
			"md5(string_agg(code || ':' || md5(definition::text), ',' ORDER BY code)) "+
			"FROM public.product",
	).Scan(&snapshot.RowCount, &dataIdentity)
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: query data identity: %w", err)
	}
	if dataIdentity != nil {
		snapshot.DataIdentity = *dataIdentity
	}
	err = pool.QueryRow(ctx, `
		WITH table_metadata AS (
			SELECT pg_class.oid AS table_oid
			FROM pg_class
			JOIN pg_namespace ON pg_namespace.oid = pg_class.relnamespace
			WHERE pg_namespace.nspname = 'public'
				AND pg_class.relname = 'product'
				AND pg_class.relkind IN ('r', 'p')
		),
		schema_parts AS (
			SELECT format(
				'column|%s|%s|%s|%s|%s|%s',
				ordinal_position,
				column_name,
				udt_name,
				COALESCE(character_maximum_length::text, ''),
				is_nullable,
				COALESCE(column_default, '')
			) AS value
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'product'
			UNION ALL
			SELECT format(
				'constraint|%s|%s|%s',
				pg_constraint.conname,
				pg_constraint.contype,
				pg_get_constraintdef(pg_constraint.oid, true)
			)
			FROM pg_constraint
			JOIN table_metadata ON table_metadata.table_oid = pg_constraint.conrelid
		)
		SELECT md5(string_agg(value, E'\n' ORDER BY value))
		FROM schema_parts
	`).Scan(&snapshot.SchemaIdentity)
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: query schema identity: %w", err)
	}
	rows, err := pool.Query(ctx, "SELECT code FROM public.product ORDER BY code")
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: query codes: %w", err)
	}
	snapshot.Codes, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: collect codes: %w", err)
	}
	return snapshot, nil
}

// DefinitionChecksums returns PostgreSQL's canonical JSONB checksum for each code.
func (harness *Harness) DefinitionChecksums(ctx context.Context) (map[string]string, error) {
	pool, err := pgxpool.New(ctx, harness.runtimeURL)
	if err != nil {
		return nil, fmt.Errorf("definition checksums: open reader pool: %w", err)
	}
	defer pool.Close()
	rows, err := pool.Query(
		ctx,
		"SELECT code, md5(definition::text) FROM public.product ORDER BY code",
	)
	if err != nil {
		return nil, fmt.Errorf("definition checksums: query: %w", err)
	}
	checksums, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct {
		code     string
		checksum string
	}, error,
	) {
		var value struct {
			code     string
			checksum string
		}
		scanErr := row.Scan(&value.code, &value.checksum)
		return value, scanErr
	})
	if err != nil {
		return nil, fmt.Errorf("definition checksums: collect: %w", err)
	}
	result := make(map[string]string, len(checksums))
	for _, checksum := range checksums {
		result[checksum.code] = checksum.checksum
	}
	return result, nil
}

// DefinitionNumber returns one JSONB numeric field as PostgreSQL text.
func (harness *Harness) DefinitionNumber(ctx context.Context, code, field string) (string, error) {
	pool, err := pgxpool.New(ctx, harness.runtimeURL)
	if err != nil {
		return "", fmt.Errorf("definition number: open reader pool: %w", err)
	}
	defer pool.Close()
	var value string
	if err := pool.QueryRow(
		ctx,
		"SELECT definition ->> $1 FROM public.product WHERE code = $2",
		field,
		code,
	).Scan(&value); err != nil {
		return "", fmt.Errorf("definition number: query: %w", err)
	}
	return value, nil
}

// InspectRole reads the runtime identity and its relevant effective privileges.
func (harness *Harness) InspectRole(ctx context.Context) (RoleInspection, error) {
	pool, err := pgxpool.New(ctx, harness.runtimeURL)
	if err != nil {
		return RoleInspection{}, fmt.Errorf("inspect role: open reader pool: %w", err)
	}
	defer pool.Close()
	var inspection RoleInspection
	err = pool.QueryRow(ctx, `
		SELECT current_user,
			rolsuper, rolcreatedb, rolcreaterole, rolinherit, rolreplication, rolbypassrls,
			has_database_privilege(current_user, current_database(), 'CONNECT'),
			has_database_privilege(current_user, current_database(), 'CREATE'),
			has_schema_privilege(current_user, 'public', 'USAGE'),
			has_schema_privilege(current_user, 'public', 'CREATE'),
			has_table_privilege(current_user, 'public.product', 'SELECT'),
			has_table_privilege(current_user, 'public.product', 'INSERT'),
			has_table_privilege(current_user, 'public.product', 'UPDATE'),
			has_table_privilege(current_user, 'public.product', 'DELETE'),
			has_table_privilege(current_user, 'public.product', 'TRUNCATE')
		FROM pg_roles
		WHERE rolname = current_user
	`).Scan(
		&inspection.CurrentUser,
		&inspection.Superuser,
		&inspection.CreateDatabase,
		&inspection.CreateRole,
		&inspection.Inherit,
		&inspection.Replication,
		&inspection.BypassRLS,
		&inspection.CanConnect,
		&inspection.CanCreateInDatabase,
		&inspection.CanUseSchema,
		&inspection.CanCreateInSchema,
		&inspection.CanSelect,
		&inspection.CanInsert,
		&inspection.CanUpdate,
		&inspection.CanDelete,
		&inspection.CanTruncate,
	)
	if err != nil {
		return RoleInspection{}, fmt.Errorf("inspect role: query: %w", err)
	}
	return inspection, nil
}

// ServerVersion returns the disposable PostgreSQL server version.
func (harness *Harness) ServerVersion(ctx context.Context) (string, error) {
	var version string
	if err := harness.adminPool.QueryRow(ctx, "SHOW server_version").
		Scan(&version); err != nil {
		return "", fmt.Errorf("server version: %w", err)
	}
	return version, nil
}

// Close releases connections and removes the disposable container within a bounded deadline.
func (harness *Harness) Close() error {
	harness.closeOnce.Do(func() {
		harness.adminPool.Close()
		harness.closeErr = terminate(harness.container)
	})
	return harness.closeErr
}

func terminate(container *postgres.PostgresContainer) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), resourceTimeout)
	defer cancel()
	if err := container.Terminate(cleanupCtx); err != nil {
		return fmt.Errorf("terminate PostgreSQL container: %w", err)
	}
	return nil
}

func connectionURL(adminURL, username, password string) (string, error) {
	parsed, err := url.Parse(adminURL)
	if err != nil {
		return "", fmt.Errorf("parse connection URL: %w", err)
	}
	parsed.User = url.UserPassword(username, password)
	return parsed.String(), nil
}

func randomCredential() (string, error) {
	value := make([]byte, credentialBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
