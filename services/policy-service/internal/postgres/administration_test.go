package postgres

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenMigrationDatabase_RejectsUntrustedInputs(t *testing.T) {
	// given
	databaseURL, ca := migrationConnectionFixture(t, "")
	for _, test := range []struct {
		name, url, host string
		ca              []byte
		want            error
	}{
		{"missing CA", databaseURL, "127.0.0.1", nil, errInvalidCA},
		{"different host", databaseURL, "different-host", ca, errInvalidConnection},
		{"runtime login", replaceUser(t, databaseURL, "go_policy_runtime"), "127.0.0.1", ca, errInvalidConnection},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			db, err := OpenMigrationDatabase(t.Context(), test.url, test.host, test.ca)

			// then
			if db != nil || !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

func TestOpenMigrationDatabase_TunnelsOnlyToLoopback(t *testing.T) {
	// given
	remote, ca := migrationConnectionFixture(t, "10.0.0.1")
	tunnel, _ := migrationConnectionFixture(t, "127.0.0.1")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// when
	_, remoteErr := OpenMigrationDatabase(t.Context(), remote, "policy-db.example", ca)
	_, tunnelErr := OpenMigrationDatabase(ctx, tunnel, "policy-db.example", ca)

	// then
	if remoteErr == nil || remoteErr.Error() != "invalid PostgreSQL tunnel configuration" {
		t.Fatalf("expected a refused remote tunnel, got %v", remoteErr)
	}
	if !errors.Is(tunnelErr, context.Canceled) {
		t.Fatalf("expected a canceled loopback connection attempt, got %v", tunnelErr)
	}
}

func migrationConnectionFixture(t *testing.T, hostAddress string) (string, []byte) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1)}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	query := url.Values{"sslmode": {"verify-full"}, "sslrootcert": {path}}
	host := "127.0.0.1:1"
	if hostAddress != "" {
		host = "policy-db.example:5432"
		query.Set("hostaddr", hostAddress)
		query.Set("port", "1")
	}
	connection := &url.URL{
		Scheme: "postgresql", Host: host, Path: "/policy",
		User:     url.UserPassword("go_policy_migrator", "synthetic"),
		RawQuery: query.Encode(),
	}
	return connection.String(), ca
}

func replaceUser(t *testing.T, databaseURL, user string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.User = url.UserPassword(user, "synthetic")
	return parsed.String()
}
