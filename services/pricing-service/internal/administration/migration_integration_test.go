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
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/service"
)

const (
	postgresImage = "postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193"
	clusterUID    = "11111111-1111-4111-8111-111111111111"
	instanceID    = "22222222-2222-4222-8222-222222222222"
)

func TestRun_PostgreSQLMigrationAndSeed(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	admin, address, ca := disposablePostgres(t, ctx)
	password := rand.Text()
	readerPassword := rand.Text()
	seederPassword := rand.Text()
	execSQL(
		t,
		ctx,
		admin,
		fmt.Sprintf(
			setupMigrationFixtureQuery,
			password,
			readerPassword,
			seederPassword,
			instanceID,
			clusterUID,
		),
	)

	targetPath := filepath.Join(t.TempDir(), "target.json")
	target := map[string]any{
		"environment":      "disposable",
		"context":          "pricing-smoke",
		"dataNamespace":    "disposable",
		"serviceNamespace": "disposable",
		"cluster":          "pricing-smoke",
		"clusterUID":       clusterUID,
		"instanceID":       instanceID,
		"database":         "pricing",
		"host":             address.Hostname(),
		"seedAllowed":      true,
		"disposable":       true,
	}
	writeTarget := func() {
		t.Helper()
		content, err := json.Marshal(target)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, targetPath, content)
	}
	writeTarget()

	credentialPath := filepath.Join(t.TempDir(), "database-url")
	migratorURL := *address
	migratorURL.User = url.UserPassword("go_pricing_migrator", password)
	migratorURL.RawQuery = url.Values{
		"sslmode": {"verify-full"}, "sslrootcert": {ca},
	}.Encode()
	writeFixture(t, credentialPath, []byte(migratorURL.String()))

	call := func(t *testing.T, operation, want string, refusal ...string) {
		t.Helper()
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		output, err := service.RunMigration(
			callCtx,
			operation,
			"disposable",
			"pricing-smoke",
			targetPath,
			credentialPath,
			ca,
		)
		if want == "" {
			if len(refusal) != 1 || err == nil || output != "" ||
				!strings.Contains(
					err.Error(),
					refusal[0],
				) || strings.Contains(err.Error(), password) {
				t.Fatalf(
					"expected redacted %s refusal matching %v and empty output",
					operation,
					refusal,
				)
			}
			return
		}
		if err != nil || output != want {
			t.Fatalf("%s: want %q, got %q (%v)", operation, want, output, err)
		}
	}

	snapshot := func() string {
		t.Helper()
		var state string
		err := admin.QueryRow(ctx, snapshotCatalogQuery).
			Scan(&state)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := snapshot()
	// when
	call(t, "status", "uninitialized")
	call(t, "version", "uninitialized")
	// then
	if snapshot() != before {
		t.Fatal("status changed the fresh schema or initialized migration history")
	}

	checkRefusal := func(t *testing.T, want string) {
		t.Helper()
		// given
		prior := snapshot()
		// when
		call(t, "migrate", "", want)
		// then
		if snapshot() != prior {
			t.Fatal("refused migration changed the catalog")
		}
	}

	t.Run("untrusted certificate", func(t *testing.T) {
		// given
		trustedCA, err := os.ReadFile(ca)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { writeFixture(t, ca, trustedCA) })
		writeCertificate(t, ca, filepath.Join(t.TempDir(), "unused-key.pem"))

		checkRefusal(t, "connect to enrolled Pricing database failed")
	})
	t.Run("wrong target marker", func(t *testing.T) {
		// given
		t.Cleanup(func() {
			target["instanceID"] = instanceID
			writeTarget()
		})
		target["instanceID"] = clusterUID
		writeTarget()

		checkRefusal(t, "target marker differs from enrollment")
	})
	t.Run("missing owner membership", func(t *testing.T) {
		// given
		t.Cleanup(func() { execSQL(t, ctx, admin, grantMigratorQuery) })
		execSQL(t, ctx, admin, revokeMigratorQuery)

		checkRefusal(t, "cannot assume Pricing database owner role")
	})
	t.Run("unmanaged tariff schema", func(t *testing.T) {
		// given
		t.Cleanup(func() { execSQL(t, ctx, admin, dropUnmanagedTariffQuery) })
		execSQL(t, ctx, admin, setupUnmanagedTariffQuery)
		call(t, "status", "unmanaged")

		// The failed first migration must roll back its schema and history together.
		checkRefusal(t, "apply Pricing schema version 1 (SQLSTATE 42P07)")
	})

	// when
	call(t, "migrate", "version=1")
	// then
	call(t, "version", "1")
	call(t, "status", "version=1")
	verifyOrderedSeed(t, ctx, admin, address, ca, targetPath, credentialPath, seederPassword)
	verifyReaderStartup(t, ctx, admin, address, ca, readerPassword)

	t.Run("invalid migration history schema", func(t *testing.T) {
		// given
		execSQL(t, ctx, admin, dropHistoryTimestampRequiredQuery)
		t.Cleanup(func() { execSQL(t, ctx, admin, restoreHistoryTimestampRequiredQuery) })
		before := snapshot()
		version := queryString(t, ctx, admin, readMigrationTimestampQuery)

		// when
		call(t, "status", "", "invalid Pricing migration history schema")
		call(t, "migrate", "", "invalid Pricing migration history schema")

		// then
		if snapshot() != before ||
			queryString(t, ctx, admin, readMigrationTimestampQuery) != version {
			t.Fatal("refusal changed the migration history or catalog")
		}
	})
	migrated := snapshot()
	versionStamp := queryString(
		t,
		ctx,
		admin,
		readMigrationTimestampQuery,
	)
	// when
	call(t, "migrate", "version=1")
	// then
	if snapshot() != migrated ||
		queryString(
			t,
			ctx,
			admin,
			readMigrationTimestampQuery,
		) != versionStamp {
		t.Fatal("replay changed the schema or recorded migration")
	}

	t.Run("managed version one compatibility", func(t *testing.T) {
		verifyPriorSchema(
			t,
			ctx,
			admin,
			address,
			ca,
			targetPath,
			credentialPath,
			seederPassword,
		)
	})
}

func disposablePostgres(t *testing.T, ctx context.Context) (*pgx.Conn, *url.URL, string) {
	t.Helper()
	dir := t.TempDir()
	cert, key, config := filepath.Join(
		dir,
		"cert.pem",
	), filepath.Join(
		dir,
		"key.pem",
	), filepath.Join(
		dir,
		"postgresql.conf",
	)
	writeCertificate(t, cert, key)
	writeFixture(t, config, []byte(`listen_addresses='*'
ssl=on
ssl_cert_file='/tmp/testcontainers-go/postgres/server.cert'
ssl_key_file='/tmp/testcontainers-go/postgres/server.key'
`))
	container, err := postgres.Run(
		ctx,
		postgresImage,
		postgres.WithDatabase("pricing"),
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
	der, err := x509.CreateCertificate(
		rand.Reader,
		template,
		template,
		&privateKey.PublicKey,
		privateKey,
	)
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

func execSQL(t *testing.T, ctx context.Context, db *pgx.Conn, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(ctx, statement, args...); err != nil {
		t.Fatal(err)
	}
}

func queryString(t *testing.T, ctx context.Context, db *pgx.Conn, statement string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(ctx, statement).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
