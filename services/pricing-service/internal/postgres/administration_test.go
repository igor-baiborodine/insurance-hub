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
	// Given bad trust material, when the adapter opens, then it rejects it before dialing.
	db, err := OpenMigrationDatabase(t.Context(), "", "", nil)
	if db != nil || err == nil || err.Error() != "selected CA contains no certificates" {
		t.Fatalf("expected missing CA error, got %v", err)
	}

	databaseURL, ca := migrationConnectionFixture(t)
	db, err = OpenMigrationDatabase(t.Context(), databaseURL, "different-host", ca)
	if db != nil || err == nil ||
		err.Error() != "invalid Pricing PostgreSQL connection configuration" {
		t.Fatalf("expected enrollment mismatch error, got %v", err)
	}
}

func TestOpenMigrationDatabase_HonorsCancellation(t *testing.T) {
	// Given valid inputs and a canceled context, when the connection opens, then it stops.
	databaseURL, ca := migrationConnectionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	db, err := OpenMigrationDatabase(ctx, databaseURL, "127.0.0.1", ca)
	if db != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected a canceled connection, got %v", err)
	}
}

func migrationConnectionFixture(t *testing.T) (string, []byte) {
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
	connection := &url.URL{
		Scheme: "postgresql", Host: "127.0.0.1:1", Path: "/pricing",
		User:     url.UserPassword("go_pricing_migrator", "synthetic"),
		RawQuery: url.Values{"sslmode": {"verify-full"}, "sslrootcert": {path}}.Encode(),
	}
	return connection.String(), ca
}
