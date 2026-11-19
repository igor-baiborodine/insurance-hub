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

func TestRun_PostgreSQLMigration(t *testing.T) {
	// Given one isolated Pricing database with platform-owned enrollment and TLS.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	admin, address, ca := disposablePostgres(t, ctx)
	password := rand.Text()
	execSQL(t, ctx, admin, `
		CREATE ROLE pricing NOLOGIN;
		ALTER DATABASE pricing OWNER TO pricing;
		CREATE ROLE go_pricing_migrator LOGIN NOINHERIT PASSWORD '`+password+`';
		GRANT pricing TO go_pricing_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE;
		CREATE SCHEMA pricing_admin AUTHORIZATION postgres;
		CREATE TABLE pricing_admin.target_identity (
			singleton boolean PRIMARY KEY CHECK (singleton),
			environment text NOT NULL, instance_id uuid NOT NULL,
			cluster_uid text NOT NULL, seed_allowed boolean NOT NULL
		);
		INSERT INTO pricing_admin.target_identity
		VALUES (true, 'disposable', '`+instanceID+`', '`+clusterUID+`', true);
		GRANT USAGE ON SCHEMA pricing_admin TO pricing, go_pricing_migrator;
		GRANT SELECT ON pricing_admin.target_identity TO pricing, go_pricing_migrator;
	`)

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

	call := func(operation, want string) {
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
			if err == nil || output != "" || strings.Contains(err.Error(), password) {
				t.Fatal("expected a redacted refusal and empty output")
			}
			return
		}
		if err != nil || output != want {
			t.Fatalf("%s: want %q, got %q (%v)", operation, want, output, err)
		}
	}

	// When status inspects an empty database, no catalog objects appear.
	snapshot := func() string {
		t.Helper()
		var state string
		err := admin.QueryRow(ctx, `SELECT coalesce(string_agg(c.relname || ':' || c.relkind::text,
			',' ORDER BY c.relname), '') FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public'`).
			Scan(&state)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := snapshot()
	call("status", "uninitialized")
	call("version", "uninitialized")
	if snapshot() != before {
		t.Fatal("status changed the fresh schema or initialized migration history")
	}

	// Then an untrusted certificate, wrong marker and wrong role all refuse mutation.
	trustedCA, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	writeCertificate(t, ca, filepath.Join(t.TempDir(), "unused-key.pem"))
	call("migrate", "")
	writeFixture(t, ca, trustedCA)
	target["instanceID"] = clusterUID
	writeTarget()
	call("migrate", "")
	target["instanceID"] = instanceID
	writeTarget()
	execSQL(t, ctx, admin, "REVOKE pricing FROM go_pricing_migrator")
	call("migrate", "")
	execSQL(t, ctx, admin,
		"GRANT pricing TO go_pricing_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE")
	if snapshot() != before {
		t.Fatal("a refused migration changed the schema or history")
	}

	call("migrate", "version=1")
	call("version", "1")
	call("status", "version=1")
	migrated := snapshot()
	versionStamp := queryString(
		t,
		ctx,
		admin,
		"SELECT applied_at::text FROM public.pricing_schema_version",
	)
	call("migrate", "version=1")
	if snapshot() != migrated ||
		queryString(
			t,
			ctx,
			admin,
			"SELECT applied_at::text FROM public.pricing_schema_version",
		) != versionStamp {
		t.Fatal("replay changed the schema or recorded migration")
	}

	// Given the Hibernate layout populated through the Java owner, with a conflicting column
	// in the second table. The first ALTER and history creation must not survive a failure.
	execSQL(t, ctx, admin, `
		SET ROLE pricing;
		DROP TABLE public.pricing_schema_version;
		ALTER TABLE public.base_price_rules DROP COLUMN position;
		ALTER TABLE public.discount_markup_rule DROP COLUMN position;
		ALTER TABLE public.discount_markup_rule ADD COLUMN position text;
		INSERT INTO public.tariff (id, code) VALUES (11, 'HSI');
		INSERT INTO public.base_price_rules (tariff_id, cover_code, price_formula) VALUES (11, 'C2', '0.25');
		INSERT INTO public.discount_markup_rule (id, type, tariff_id, apply_if_formula, param_value)
		VALUES (32, 'perc_markup', 11, 'FLOOD == "YES"', 1.50);
		RESET ROLE;
	`)
	call("status", "unmanaged")
	call("version", "uninitialized")
	call("migrate", "")
	if got := queryString(
		t,
		ctx,
		admin,
		`SELECT (to_regclass('public.pricing_schema_version') IS NULL
		AND NOT EXISTS (SELECT FROM pg_attribute WHERE attrelid = 'public.base_price_rules'::regclass
			AND attname = 'position' AND NOT attisdropped))::text`,
	); got != "true" {
		t.Fatal("failed migration left partially applied schema or history")
	}
	execSQL(t, ctx, admin, "ALTER TABLE public.discount_markup_rule DROP COLUMN position")
	call("migrate", "version=1")
	execSQL(t, ctx, admin, "SET ROLE pricing")
	got := queryString(t, ctx, admin, `SELECT concat_ws('|', t.id, t.code, b.cover_code,
		b.price_formula, b.apply_if_formula IS NULL, m.id, m.type, m.apply_if_formula, m.param_value)
		FROM public.tariff t JOIN public.base_price_rules b ON b.tariff_id = t.id
		JOIN public.discount_markup_rule m ON m.tariff_id = t.id
		WHERE b.position IS NULL AND m.position IS NULL`)
	if got != `11|HSI|C2|0.25|t|32|perc_markup|FLOOD == "YES"|1.50` {
		t.Fatalf("adoption changed Java-owned rows: %q", got)
	}
	execSQL(
		t,
		ctx,
		admin,
		"INSERT INTO public.base_price_rules (tariff_id, cover_code, price_formula) VALUES (11, 'C3', '1.00')",
	)
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

func execSQL(t *testing.T, ctx context.Context, db *pgx.Conn, statement string) {
	t.Helper()
	if _, err := db.Exec(ctx, statement); err != nil {
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
