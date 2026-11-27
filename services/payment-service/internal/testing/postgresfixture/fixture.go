//go:build integration

// Package postgresfixture provides a disposable TLS PostgreSQL database that is enrolled and
// provisioned like a Payment CNPG target. Only integration-tagged tests use it.
package postgresfixture

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/administration/provisioning"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/service"
)

const (
	postgresImage = "postgres:17.10-alpine@sha256:742f40ea20b9ff2ff31db5458d127452988a2164df9e17441e191f3b72252193"
	// ClusterUID and InstanceID identify the disposable enrollment marker.
	ClusterUID = "11111111-1111-4111-8111-111111111111"
	InstanceID = "22222222-2222-4222-8222-222222222222"
)

//go:embed sql/setup-target-identity.sql
var setupTargetIdentityQuery string

// Fixture is one disposable Payment database with platform roles, marker and protected inputs.
type Fixture struct {
	Admin            *pgx.Conn
	Address          *url.URL
	CA               string
	TargetPath       string
	URLPath          string
	MigratorPassword string
	WriterPassword   string
}

// New starts PostgreSQL with TLS and provisions it with the platform access SQL.
func New(t *testing.T, ctx context.Context) *Fixture {
	t.Helper()
	admin, address, ca := disposablePostgres(t, ctx)
	fixture := &Fixture{
		Admin: admin, Address: address, CA: ca,
		MigratorPassword: rand.Text(), WriterPassword: rand.Text(),
		TargetPath: filepath.Join(t.TempDir(), "target.json"),
		URLPath:    filepath.Join(t.TempDir(), "database-url"),
	}
	fixture.Exec(t, ctx, fmt.Sprintf(setupTargetIdentityQuery,
		fixture.MigratorPassword, fixture.WriterPassword, InstanceID, ClusterUID))
	fixture.Exec(t, ctx, provisioning.ProvisionAccess)
	fixture.WriteTarget(t, InstanceID)
	migratorURL := *address
	migratorURL.User = url.UserPassword("go_payment_migrator", fixture.MigratorPassword)
	migratorURL.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {ca}}.Encode()
	writeFile(t, fixture.URLPath, []byte(migratorURL.String()))
	return fixture
}

// WriteTarget writes the protected enrollment with the given instance identity.
func (f *Fixture) WriteTarget(t *testing.T, instance string) {
	t.Helper()
	content, err := json.Marshal(map[string]any{
		"environment": "disposable", "context": "payment-smoke",
		"dataNamespace": "disposable", "serviceNamespace": "disposable",
		"cluster": "payment-smoke", "clusterUID": ClusterUID, "instanceID": instance,
		"database": "payment", "host": f.Address.Hostname(), "disposable": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.TargetPath, content)
}

// Run executes one operator migration command against the enrolled target.
func (f *Fixture) Run(ctx context.Context, operation string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return service.RunMigration(callCtx, operation, "disposable", "payment-smoke",
		f.TargetPath, f.URLPath, f.CA)
}

// Call requires one successful migration command with the expected output.
func (f *Fixture) Call(t *testing.T, ctx context.Context, operation, want string) {
	t.Helper()
	if got, err := f.Run(ctx, operation); err != nil || got != want {
		t.Fatalf("%s: want %q, got %q (%v)", operation, want, got, err)
	}
}

// WriterURL is the writer login over verified TLS for the named database.
func (f *Fixture) WriterURL(database string) string {
	writerURL := *f.Address
	writerURL.Path = "/" + database
	writerURL.User = url.UserPassword("go_payment_writer", f.WriterPassword)
	writerURL.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {f.CA}}.Encode()
	return writerURL.String()
}

// WriterSettings loads the production database settings for this disposable writer login.
func (f *Fixture) WriterSettings(t *testing.T) config.Database {
	t.Helper()
	t.Setenv("PAYMENT_DATABASE_URL", f.WriterURL("payment"))
	t.Setenv("MINIO_ENDPOINT", "http://minio.example:9000")
	t.Setenv("MINIO_ACCESS_KEY", "access")
	t.Setenv("MINIO_SECRET_KEY", "secret")
	t.Setenv("KAFKA_BROKERS", "kafka.example:9093")
	t.Setenv("KAFKA_CA_FILE", "/etc/payment-service/kafka/ca.crt")
	settings, err := config.Load()
	if err != nil {
		t.Fatal("load disposable writer settings:", err)
	}
	return settings.Database
}

// Exec runs fixture-owner SQL.
func (f *Fixture) Exec(t *testing.T, ctx context.Context, statement string) {
	t.Helper()
	if _, err := f.Admin.Exec(ctx, statement); err != nil {
		t.Fatal(err)
	}
}

// Query returns one text value read by the fixture owner.
func (f *Fixture) Query(t *testing.T, ctx context.Context, statement string) string {
	t.Helper()
	var value string
	if err := f.Admin.QueryRow(ctx, statement).Scan(&value); err != nil {
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
	writeFile(t, config, []byte(`listen_addresses='*'
ssl=on
ssl_cert_file='/tmp/testcontainers-go/postgres/server.cert'
ssl_key_file='/tmp/testcontainers-go/postgres/server.key'
`))
	container, err := postgres.Run(
		ctx,
		postgresImage,
		postgres.WithDatabase("payment"),
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
	writeFile(t, cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeFile(t, key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
