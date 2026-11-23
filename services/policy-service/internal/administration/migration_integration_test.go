//go:build integration

package administration_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/service"
)

const (
	postgresImage = "postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193"
	clusterUID    = "11111111-1111-4111-8111-111111111111"
	instanceID    = "22222222-2222-4222-8222-222222222222"

	// Independently stated C02 expectations for the Java-shaped fixture before new writes.
	javaFacts = "counts=1/2/2/1/2/4;quote=123.45;offerCovers=123.45;" +
		"versions=1:123.45:123.45:2027-11-13:2026-11-13,2:3.71:3.71:2026-11-23:2026-11-24;" +
		"orphans=0;answers=NUM_OF_CLAIM=0,TYPE=APARTMENT"
	managedAllocators = "cover.id=ALWAYS,goose_db_version.id=BY DEFAULT,offer.id=ALWAYS," +
		"policy.id=ALWAYS,policy_outbox.id=ALWAYS,policy_version.id=ALWAYS;" +
		"sequences=cover_id_seq," +
		"goose_db_version_id_seq,offer_id_seq,policy_id_seq,policy_outbox_id_seq," +
		"policy_version_id_seq"
)

func TestRun_PolicyMigrationFreshTarget(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := newFixture(t, ctx)
	before := fixture.snapshot(t, ctx)

	// when
	fixture.call(t, ctx, "status", "uninitialized")

	// then
	if fixture.snapshot(t, ctx) != before {
		t.Fatal("status changed the fresh schema or initialized migration history")
	}

	t.Run("mismatched enrollment", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.writeTarget(t, instanceID) })
		fixture.writeTarget(t, clusterUID)

		fixture.refuse(t, ctx, "migrate", "target identity or marker permissions differ")
	})
	t.Run("partial schema", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.exec(t, ctx, dropPartialSchemaQuery) })
		fixture.exec(t, ctx, setupPartialSchemaQuery)

		fixture.refuse(t, ctx, "status", "partial Policy schema")
		fixture.refuse(t, ctx, "migrate", "partial Policy schema")
	})

	// when two operators race on the fresh target
	var group sync.WaitGroup
	results := make([]string, 2)
	failures := make([]error, 2)
	for index := range results {
		group.Go(func() {
			results[index], failures[index] = fixture.run(ctx, "migrate")
		})
	}
	group.Wait()

	// then
	for index := range results {
		if failures[index] != nil || results[index] != "version=1" {
			t.Fatalf(
				"concurrent migrate %d: got %q (%v)",
				index,
				results[index],
				failures[index],
			)
		}
	}
	fixture.call(t, ctx, "status", "version=1")
	if got := fixture.query(t, ctx, readAllocatorCatalogQuery); got != managedAllocators {
		t.Fatalf("fresh allocators: got %q", got)
	}
	if history := fixture.query(
		t,
		ctx,
		readMigrationHistoryQuery,
	); !strings.HasPrefix(history, "0:true:") ||
		!strings.Contains(history, ",1:true:") ||
		strings.Count(history, ",") != 1 {
		t.Fatalf("expected one base and one applied history row, got %q", history)
	}
	if got := fixture.generatedAggregate(t, ctx, true); got != "1,1,1,1:t" {
		t.Fatalf("fresh runtime insert: got %q", got)
	}
	migrated := fixture.snapshot(t, ctx)

	t.Run("retry keeps one history", func(t *testing.T) {
		// when
		fixture.call(t, ctx, "migrate", "version=1")

		// then
		if fixture.snapshot(t, ctx) != migrated {
			t.Fatal("replay changed the schema or migration history")
		}
	})

	t.Run("invalid history", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.exec(t, ctx, deleteUnknownHistoryQuery) })
		fixture.exec(t, ctx, insertUnknownHistoryQuery)

		fixture.refuse(t, ctx, "status", "inconsistent migration history")
		fixture.refuse(t, ctx, "migrate", "inconsistent migration history")
	})
}

func TestRun_PolicyMigrationAdoptsJavaData(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := newFixture(t, ctx)
	fixture.exec(t, ctx, setupJavaFixtureQuery)
	if got := fixture.query(t, ctx, readBusinessFactsQuery); got != javaFacts {
		t.Fatalf("Java fixture differs from the C02 baseline: %q", got)
	}
	baseline := fixture.query(t, ctx, readRowFingerprintsQuery)
	fixture.call(t, ctx, "status", "unmanaged")

	// when
	fixture.call(t, ctx, "migrate", "version=1")

	// then
	fixture.call(t, ctx, "status", "version=1")
	fixture.requireBusinessData(t, ctx, baseline)
	if got := fixture.query(t, ctx, readAllocatorCatalogQuery); got != managedAllocators {
		t.Fatalf("adopted allocators retain the Java sequence or lack identities: %q", got)
	}

	// The rolled-back insert observes the first allocation and leaves gaps behind.
	if got := fixture.generatedAggregate(t, ctx, false); got != "2,3,7,9:t" {
		t.Fatalf("first adopted allocation: got %q", got)
	}
	fixture.requireBusinessData(t, ctx, baseline)

	t.Run("replay preserves allocator progress", func(t *testing.T) {
		// given
		migrated := fixture.snapshot(t, ctx)

		// when
		fixture.call(t, ctx, "migrate", "version=1")

		// then
		if fixture.snapshot(t, ctx) != migrated {
			t.Fatal("replay changed the schema or migration history")
		}
		fixture.requireBusinessData(t, ctx, baseline)
		if got := fixture.generatedAggregate(t, ctx, true); got != "3,4,8,10:t" {
			t.Fatalf("replay rewound an allocator: got %q", got)
		}
	})

	t.Run("allocator behind its table", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.query(t, ctx, restoreOfferAllocatorQuery) })
		fixture.query(t, ctx, rewindOfferAllocatorQuery)

		fixture.refuse(t, ctx, "status", "identity allocator is behind")
		fixture.refuse(t, ctx, "migrate", "identity allocator is behind")
	})
}

type migrationFixture struct {
	admin           *pgx.Conn
	address         *url.URL
	ca              string
	targetPath      string
	urlPath         string
	password        string
	runtimePassword string
}

func newFixture(t *testing.T, ctx context.Context) *migrationFixture {
	t.Helper()
	admin, address, ca := disposablePostgres(t, ctx)
	fixture := &migrationFixture{
		admin: admin, address: address, ca: ca, password: rand.Text(),
		runtimePassword: rand.Text(),
		targetPath:      filepath.Join(t.TempDir(), "target.json"),
		urlPath:         filepath.Join(t.TempDir(), "database-url"),
	}
	fixture.exec(t, ctx, fmt.Sprintf(setupTargetIdentityQuery,
		fixture.password, fixture.runtimePassword, instanceID, clusterUID))
	// Provision with the same SQL that resource-ensure runs against CNPG.
	fixture.exec(t, ctx, provisionAccessQuery)
	fixture.writeTarget(t, instanceID)
	migratorURL := *address
	migratorURL.User = url.UserPassword("go_policy_migrator", fixture.password)
	migratorURL.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {ca}}.Encode()
	writeFixture(t, fixture.urlPath, []byte(migratorURL.String()))
	return fixture
}

func (f *migrationFixture) writeTarget(t *testing.T, instance string) {
	t.Helper()
	content, err := json.Marshal(map[string]any{
		"environment": "disposable", "context": "policy-smoke",
		"dataNamespace": "disposable", "serviceNamespace": "disposable",
		"cluster": "policy-smoke", "clusterUID": clusterUID, "instanceID": instance,
		"database": "policy", "host": f.address.Hostname(), "disposable": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, f.targetPath, content)
}

func (f *migrationFixture) run(ctx context.Context, operation string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return service.RunMigration(callCtx, operation, "disposable", "policy-smoke",
		f.targetPath, f.urlPath, f.ca)
}

func (f *migrationFixture) call(t *testing.T, ctx context.Context, operation, want string) {
	t.Helper()
	if got, err := f.run(ctx, operation); err != nil || got != want {
		t.Fatalf("%s: want %q, got %q (%v)", operation, want, got, err)
	}
}

// refuse requires a redacted refusal that leaves schema, history and business rows unchanged.
func (f *migrationFixture) refuse(t *testing.T, ctx context.Context, operation, reason string) {
	t.Helper()
	before := f.snapshot(t, ctx)
	got, err := f.run(ctx, operation)
	if err == nil || got != "" || !strings.Contains(err.Error(), reason) ||
		strings.Contains(err.Error(), f.password) {
		t.Fatalf(
			"%s: expected redacted refusal %q, got %q (%v)",
			operation,
			reason,
			got,
			err,
		)
	}
	if f.snapshot(t, ctx) != before {
		t.Fatalf("refused %s changed the catalog, history or data", operation)
	}
}

func (f *migrationFixture) snapshot(t *testing.T, ctx context.Context) string {
	t.Helper()
	state := f.query(t, ctx, readCatalogSnapshotQuery)
	history, rows := "", ""
	if strings.Contains(state, "goose_db_version:r") {
		history = f.query(t, ctx, readMigrationHistoryQuery)
	}
	if strings.Contains(state, "cover:r") && strings.Contains(state, "offer_answers:r") {
		rows = f.query(t, ctx, readRowFingerprintsQuery)
	}
	return state + "|rows=" + rows + "|history=" + history
}

func (f *migrationFixture) requireBusinessData(t *testing.T, ctx context.Context, rows string) {
	t.Helper()
	if got := f.query(t, ctx, readRowFingerprintsQuery); got != rows {
		t.Fatalf("business rows changed: got %q, want %q", got, rows)
	}
	if got := f.query(t, ctx, readBusinessFactsQuery); got != javaFacts {
		t.Fatalf("business facts changed: %q", got)
	}
}

// generatedAggregate inserts one aggregate as the runtime role and returns its generated IDs.
func (f *migrationFixture) generatedAggregate(
	t *testing.T,
	ctx context.Context,
	commit bool,
) string {
	t.Helper()
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE go_policy_runtime"); err != nil {
		t.Fatal(err)
	}
	var ids string
	if err := tx.QueryRow(ctx, insertGeneratedAggregateQuery).Scan(&ids); err != nil {
		t.Fatal("runtime insert failed:", err)
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func (f *migrationFixture) exec(t *testing.T, ctx context.Context, statement string) {
	t.Helper()
	if _, err := f.admin.Exec(ctx, statement); err != nil {
		t.Fatal(err)
	}
}

func (f *migrationFixture) query(t *testing.T, ctx context.Context, statement string) string {
	t.Helper()
	var value string
	if err := f.admin.QueryRow(ctx, statement).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func disposablePostgres(t *testing.T, ctx context.Context) (*pgx.Conn, *url.URL, string) {
	t.Helper()
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")
	config := filepath.Join(dir, "postgresql.conf")
	writeCertificate(t, cert, key)
	writeFixture(t, config, []byte(`listen_addresses='*'
ssl=on
ssl_cert_file='/tmp/testcontainers-go/postgres/server.cert'
ssl_key_file='/tmp/testcontainers-go/postgres/server.key'
`))
	container, err := postgres.Run(
		ctx,
		postgresImage,
		postgres.WithDatabase("policy"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword(rand.Text()),
		postgres.WithConfigFile(config),
		postgres.WithSSLCert(cert, cert, key),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(time.Minute),
		),
	)
	testcontainers.CleanupContainer(t, container, testcontainers.StopTimeout(2*time.Second))
	if err != nil {
		t.Fatal("start disposable PostgreSQL:", err)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal("resolve disposable PostgreSQL address")
	}
	address, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("parse disposable PostgreSQL address")
	}
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("connect to disposable PostgreSQL as fixture owner")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := admin.Close(ctx); err != nil {
			t.Error("close disposable PostgreSQL fixture connection")
		}
	})
	return admin, address, cert
}

func writeCertificate(t *testing.T, cert, key string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute),
		NotAfter: time.Now().Add(10 * time.Minute), DNSNames: []string{"localhost"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template,
		&privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeFixture(t, key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func writeFixture(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
