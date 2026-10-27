//go:build integration

package integrationtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/administration"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	productgrpc "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/grpc"
	producthttp "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/http"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/postgres"
)

type seedDatabase struct {
	admin     *sql.DB
	seeders   []*sql.DB
	readerURL string
	target    administration.Target
}

func TestProductSeed(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate seed integration test source")
	}
	catalogPath := filepath.Join(
		filepath.Dir(filename),
		"..",
		"administration",
		"seeds",
		"base.json",
	)
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
	database := startSeedDatabase(t, ctx, root)
	clear := func(t *testing.T) {
		t.Helper()
		if _, err := database.admin.ExecContext(
			ctx,
			`DELETE FROM public.product`,
		); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("empty catalog matches accepted storage and transports", func(t *testing.T) {
		// given
		clear(t)

		// when
		result, seedErr := administration.Seed(
			ctx, database.seeders[0], database.target, catalogPath,
		)

		// then
		if seedErr != nil ||
			result != (administration.SeedResult{Inserted: 4, Existing: 0}) {
			t.Fatalf("fresh seed result = %+v, error = %v", result, seedErr)
		}
		assertSeedStorage(t, ctx, database.admin, fixture)
		assertSeedTransportParity(t, ctx, root, database.readerURL)
	})

	t.Run("repeat reports every accepted product as existing", func(t *testing.T) {
		// given
		clear(t)
		if _, err := administration.Seed(
			ctx, database.seeders[0], database.target, catalogPath,
		); err != nil {
			t.Fatal(err)
		}

		// when
		result, seedErr := administration.Seed(
			ctx, database.seeders[0], database.target, catalogPath,
		)

		// then
		if seedErr != nil ||
			result != (administration.SeedResult{Inserted: 0, Existing: 4}) {
			t.Fatalf("repeat seed result = %+v, error = %v", result, seedErr)
		}
		assertSeedStorage(t, ctx, database.admin, fixture)
	})

	t.Run("same engine applies a supplemental environment catalog", func(t *testing.T) {
		// given
		clear(t)
		if _, err := administration.Seed(
			ctx, database.seeders[0], database.target, catalogPath,
		); err != nil {
			t.Fatal(err)
		}
		rawDefinition := `{"name":"Staging product","covers":[],"questions":[]}`
		var checksum string
		if err := database.admin.QueryRowContext(
			ctx,
			`SELECT md5($1::jsonb::text)`,
			rawDefinition,
		).Scan(&checksum); err != nil {
			t.Fatal(err)
		}
		rawIdentity := sha256.Sum256([]byte(rawDefinition))
		catalog := map[string]any{
			"schemaVersion":  1,
			"seedVersion":    1,
			"scenarioId":     "staging-extra",
			"provenance":     "synthetic integration catalog",
			"sourceRevision": "staging-v1",
			"sourcePath":     "catalog/staging.json",
			"rows": []map[string]string{{
				"code":                      "STAGING-SPECIAL",
				"checksum":                  checksum,
				"rawDefinitionSha256":       hex.EncodeToString(rawIdentity[:]),
				"rawLosslessDefinitionJson": rawDefinition,
			}},
		}
		content, err := json.Marshal(catalog)
		if err != nil {
			t.Fatal(err)
		}
		supplementalPath := filepath.Join(t.TempDir(), "staging.json")
		if err := os.WriteFile(supplementalPath, content, 0o600); err != nil {
			t.Fatal(err)
		}

		// when
		first, firstErr := administration.Seed(
			ctx, database.seeders[0], database.target, supplementalPath,
		)
		replay, replayErr := administration.Seed(
			ctx, database.seeders[0], database.target, supplementalPath,
		)

		// then
		if firstErr != nil || replayErr != nil ||
			first != (administration.SeedResult{Inserted: 1, Existing: 0}) ||
			replay != (administration.SeedResult{Inserted: 0, Existing: 1}) {
			t.Fatalf(
				"supplemental results = %+v/%+v, errors = %v/%v",
				first, replay, firstErr, replayErr,
			)
		}
		if got := adoptionScalar(
			t, ctx, database.admin, `SELECT count(*)::text FROM public.product`,
		); got != "5" {
			t.Fatalf("supplemental seed row count = %s, want 5", got)
		}
	})

	t.Run("partial modified catalog preserves existing and unrelated rows", func(t *testing.T) {
		// given
		clear(t)
		for _, statement := range []string{
			`INSERT INTO public.product VALUES('CAR','{"modified":true,"amount":1.2300}')`,
			`INSERT INTO public.product VALUES('EXTRA','{"retained":true}')`,
		} {
			if _, err := database.admin.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		before := []string{
			adoptionScalar(
				t,
				ctx,
				database.admin,
				`SELECT definition::text FROM public.product WHERE code='CAR'`,
			),
			adoptionScalar(
				t,
				ctx,
				database.admin,
				`SELECT definition::text FROM public.product WHERE code='EXTRA'`,
			),
		}

		// when
		result, seedErr := administration.Seed(
			ctx, database.seeders[0], database.target, catalogPath,
		)

		// then
		if seedErr != nil ||
			result != (administration.SeedResult{Inserted: 3, Existing: 1}) {
			t.Fatalf("partial seed result = %+v, error = %v", result, seedErr)
		}
		after := []string{
			adoptionScalar(
				t,
				ctx,
				database.admin,
				`SELECT definition::text FROM public.product WHERE code='CAR'`,
			),
			adoptionScalar(
				t,
				ctx,
				database.admin,
				`SELECT definition::text FROM public.product WHERE code='EXTRA'`,
			),
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("existing rows changed: %v -> %v", before, after)
		}
		if got := adoptionScalar(
			t,
			ctx,
			database.admin,
			`SELECT count(*)::text FROM public.product`,
		); got != "5" {
			t.Fatalf("partial seed row count = %s, want 5", got)
		}
	})

	t.Run("concurrent seeds serialize without duplicates", func(t *testing.T) {
		// given
		clear(t)
		results := make(chan administration.SeedResult, 2)
		errors := make(chan error, 2)
		var start sync.WaitGroup
		start.Add(1)
		for _, seeder := range database.seeders {
			go func() {
				start.Wait()
				result, seedErr := administration.Seed(
					ctx, seeder, database.target, catalogPath,
				)
				results <- result
				errors <- seedErr
			}()
		}
		start.Done()

		// when
		observed := []administration.SeedResult{<-results, <-results}
		observedErrors := []error{<-errors, <-errors}

		// then
		for _, seedErr := range observedErrors {
			if seedErr != nil {
				t.Fatal(seedErr)
			}
		}
		sort.Slice(
			observed,
			func(i, j int) bool { return observed[i].Inserted > observed[j].Inserted },
		)
		want := []administration.SeedResult{
			{Inserted: 4, Existing: 0},
			{Inserted: 0, Existing: 4},
		}
		if !reflect.DeepEqual(observed, want) {
			t.Fatalf("concurrent results = %+v, want %+v", observed, want)
		}
		assertSeedStorage(t, ctx, database.admin, fixture)
	})

	t.Run("cancelled lock leaves catalog empty", func(t *testing.T) {
		// given
		clear(t)
		connection, err := database.admin.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = connection.Close() }()
		if _, err := connection.ExecContext(
			ctx,
			`SELECT pg_advisory_lock(190001)`,
		); err != nil {
			t.Fatal(err)
		}
		bounded, stop := context.WithTimeout(ctx, 100*time.Millisecond)
		defer stop()

		// when
		_, seedErr := administration.Seed(
			bounded, database.seeders[0], database.target, catalogPath,
		)

		// then
		if seedErr == nil || bounded.Err() != context.DeadlineExceeded {
			t.Fatalf("cancelled seed result: %v", seedErr)
		}
		if _, err := connection.ExecContext(
			ctx,
			`SELECT pg_advisory_unlock(190001)`,
		); err != nil {
			t.Fatal(err)
		}
		if got := adoptionScalar(
			t,
			ctx,
			database.admin,
			`SELECT count(*)::text FROM public.product`,
		); got != "0" {
			t.Fatalf("cancelled seed inserted %s rows", got)
		}
	})

	t.Run("lost acknowledgement replay is insert only", func(t *testing.T) {
		// given
		clear(t)
		first, firstErr := administration.Seed(
			ctx, database.seeders[0], database.target, catalogPath,
		)
		if firstErr != nil || first.Inserted != 4 {
			t.Fatalf("initial seed result = %+v, error = %v", first, firstErr)
		}

		// when
		replay, replayErr := administration.Seed(
			ctx, database.seeders[0], database.target, catalogPath,
		)

		// then
		if replayErr != nil ||
			replay != (administration.SeedResult{Inserted: 0, Existing: 4}) {
			t.Fatalf("replay result = %+v, error = %v", replay, replayErr)
		}
		assertSeedStorage(t, ctx, database.admin, fixture)
	})

	t.Run("disabled target rejects before writes", func(t *testing.T) {
		// given
		clear(t)
		disabled := database.target
		disabled.SeedAllowed = false

		// when
		_, seedErr := administration.Seed(
			ctx, database.seeders[0], disabled, catalogPath,
		)

		// then
		if seedErr == nil {
			t.Fatal("disabled target was seeded")
		}
		if got := adoptionScalar(
			t,
			ctx,
			database.admin,
			`SELECT count(*)::text FROM public.product`,
		); got != "0" {
			t.Fatalf("disabled target inserted %s rows", got)
		}
	})
}

func startSeedDatabase(t *testing.T, ctx context.Context, fixtureRoot string) seedDatabase {
	t.Helper()
	harness, err := Start(ctx, Options{
		FixtureRoot: fixtureRoot,
		Image:       DefaultPostgresImage,
		provision: func(context.Context, *pgxpool.Pool, string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := harness.Close(); err != nil {
			t.Error(err)
		}
	})
	clusterAdmin := stdlib.OpenDB(*harness.adminPool.Config().ConnConfig)
	t.Cleanup(func() { _ = clusterAdmin.Close() })
	for _, query := range []string{
		`CREATE ROLE product LOGIN NOINHERIT`,
		`CREATE ROLE postgres NOLOGIN`,
		`CREATE ROLE go_product_migrator LOGIN NOINHERIT PASSWORD 'synthetic-migrator-password'`,
		`CREATE ROLE go_product_seeder LOGIN NOINHERIT PASSWORD 'synthetic-seed-password'`,
		`CREATE ROLE go_product_reader LOGIN NOINHERIT PASSWORD 'synthetic-reader-password'`,
		`GRANT product TO go_product_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE`,
		`CREATE DATABASE product OWNER product`,
		`REVOKE ALL ON DATABASE product FROM PUBLIC`,
		`GRANT CONNECT ON DATABASE product TO go_product_migrator,go_product_seeder,go_product_reader`,
	} {
		if _, err := clusterAdmin.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	adminConfig := *harness.adminPool.Config().ConnConfig
	adminConfig.Database = "product"
	admin := stdlib.OpenDB(adminConfig)
	t.Cleanup(func() { _ = admin.Close() })
	for _, query := range []string{
		`REVOKE ALL ON SCHEMA public FROM PUBLIC`,
		`GRANT USAGE ON SCHEMA public TO go_product_reader,go_product_seeder`,
		`CREATE SCHEMA product_admin AUTHORIZATION postgres`,
		`SET ROLE postgres`,
		`CREATE TABLE product_admin.target_identity(singleton boolean PRIMARY KEY CHECK(singleton),
environment text NOT NULL, instance_id uuid NOT NULL, cluster_uid text NOT NULL,
seed_allowed boolean NOT NULL)`,
		`RESET ROLE`,
		`INSERT INTO product_admin.target_identity VALUES(true,'disposable',
'00000000-0000-4000-8000-000000000190','synthetic-cluster-uid',true)`,
		`REVOKE ALL ON SCHEMA product_admin FROM PUBLIC`,
		`GRANT USAGE ON SCHEMA product_admin TO product,go_product_migrator,go_product_seeder`,
		`GRANT SELECT ON product_admin.target_identity TO product,go_product_migrator,go_product_seeder`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE ALL ON TABLES FROM PUBLIC`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product IN SCHEMA public REVOKE ALL ON TABLES FROM PUBLIC`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE ALL ON SEQUENCES FROM PUBLIC`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC`,
	} {
		if _, err := admin.ExecContext(ctx, query); err != nil {
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
	t.Cleanup(func() { _ = migrator.Close() })
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
		SeedAllowed:      true,
		Disposable:       true,
	}
	if err := administration.Migrate(ctx, migrator, target); err != nil {
		t.Fatal(err)
	}
	seeders := make([]*sql.DB, 2)
	for index := range seeders {
		seedConfig := adminConfig
		seedConfig.User = "go_product_seeder"
		seedConfig.Password = "synthetic-seed-password"
		seeders[index] = stdlib.OpenDB(seedConfig)
		t.Cleanup(func() { _ = seeders[index].Close() })
	}
	readerConfig := adminConfig
	readerConfig.User = "go_product_reader"
	readerConfig.Password = "synthetic-reader-password"
	readerURL := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(readerConfig.User, readerConfig.Password),
		Host:     net.JoinHostPort(readerConfig.Host, strconv.Itoa(int(readerConfig.Port))),
		Path:     "/product",
		RawQuery: "sslmode=disable",
	}).String()
	return seedDatabase{
		admin: admin, seeders: seeders,
		readerURL: readerURL, target: target,
	}
}

func assertSeedStorage(
	t *testing.T,
	ctx context.Context,
	admin *sql.DB,
	fixture CatalogFixture,
) {
	t.Helper()
	gotIdentity := adoptionScalar(
		t,
		ctx,
		admin,
		`SELECT md5(string_agg(code||':'||md5(definition::text),',' ORDER BY code)) FROM public.product`,
	)
	if gotIdentity != fixture.DataIdentity.Value {
		t.Fatalf(
			"seed data identity = %s, want %s",
			gotIdentity,
			fixture.DataIdentity.Value,
		)
	}
	rows, err := admin.QueryContext(
		ctx,
		`SELECT code,md5(definition::text) FROM public.product ORDER BY code`,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	index := 0
	for rows.Next() {
		var code, checksum string
		if err := rows.Scan(&code, &checksum); err != nil {
			t.Fatal(err)
		}
		if index >= len(fixture.Rows) || code != fixture.Rows[index].Code ||
			checksum != fixture.Rows[index].Checksum.Value {
			t.Fatalf("seed row %d = %s/%s", index, code, checksum)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(fixture.Rows) {
		t.Fatalf("seed row count = %d, want %d", index, len(fixture.Rows))
	}
}

func assertSeedTransportParity(
	t *testing.T,
	ctx context.Context,
	baselineRoot,
	databaseURL string,
) {
	t.Helper()
	t.Setenv("PRODUCT_DATABASE_URL", databaseURL)
	settings, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.OpenPool(ctx, settings.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var rowCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.product`).
		Scan(&rowCount); err != nil {
		t.Fatalf("seeded reader query: %v", err)
	}
	if rowCount != 4 {
		t.Fatalf("seeded reader row count = %d, want 4", rowCount)
	}
	reader, err := postgres.NewReader(
		pool,
		settings.Database.AcquireTimeout,
		settings.Database.QueryTimeout,
	)
	if err != nil {
		t.Fatal(err)
	}
	listProducts, err := application.NewListProducts(reader)
	if err != nil {
		t.Fatal(err)
	}
	getProduct, err := application.NewGetProduct(reader)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	handler, err := producthttp.NewHandler(
		logger.New(&logs, settings.ServiceName, settings.LogLevel),
		producthttp.Settings{RequestTimeout: settings.RequestTimeout},
		producthttp.ListProducts(listProducts.Execute),
		producthttp.GetProduct(getProduct.Execute),
	)
	if err != nil {
		t.Fatal(err)
	}
	httpAddress, httpClient := startHTTPIntegrationServer(
		t,
		settings,
		handler,
	)
	grpcClient := startGRPCIntegrationServer(
		t,
		settings,
		productgrpc.ListProducts(listProducts.Execute),
		productgrpc.GetProduct(getProduct.Execute),
		&logs,
	)
	httpExpectations := loadQAHTTPExpectations(t, baselineRoot)
	grpcExpectations := loadQAGRPCExpectations(t, baselineRoot)
	assertHTTPExpectation(
		t,
		httpClient,
		httpAddress,
		"/products",
		httpExpectations["HTTP-DIRECT-LIST-001"],
	)
	list, err := grpcClient.ListProducts(ctx, &productv1.ListProductsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	assertGRPCProducts(t, grpcExpectations["HTTP-DIRECT-LIST-001"], list.GetProducts())
	for _, code := range []string{"CAR", "FAI", "HSI", "TRI"} {
		assertHTTPExpectation(
			t,
			httpClient,
			httpAddress,
			"/products/"+code,
			httpExpectations["HTTP-DIRECT-GET-"+code+"-001"],
		)
		response, getErr := grpcClient.GetProduct(
			ctx,
			&productv1.GetProductRequest{Code: code},
		)
		if getErr != nil {
			t.Fatal(getErr)
		}
		assertGRPCProducts(
			t,
			grpcExpectations["HTTP-DIRECT-GET-"+code+"-001"],
			[]*productv1.Product{response.GetProduct()},
		)
	}
}
