package postgres

import (
	"context"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
)

const sensitiveMarker = "sensitive-marker-5c1d"

func TestOpenPool_RejectsMissingSettingsAndOwnerLogin(t *testing.T) {
	// given
	owner := loadDatabase(t, databaseURL("127.0.0.1:1", "payment", writeTestCA(t)))

	// when
	empty, emptyErr := OpenPool(t.Context(), config.Database{})
	pool, ownerErr := OpenPool(t.Context(), owner)

	// then
	if empty != nil || emptyErr == nil {
		t.Fatalf("OpenPool(empty) = (%v, %v)", empty, emptyErr)
	}
	if pool != nil || ownerErr == nil ||
		ownerErr.Error() != "invalid Payment PostgreSQL writer connection settings" {
		t.Fatalf("OpenPool(owner) = (%v, %v)", pool, ownerErr)
	}
	if strings.Contains(ownerErr.Error(), sensitiveMarker) {
		t.Error("error exposes the credential")
	}
}

func TestOpenPool_UnreachableServerFailsWithinConnectTimeout(t *testing.T) {
	tests := []struct {
		name    string
		address func(*testing.T) string
	}{
		{"refused connection", func(*testing.T) string { return "127.0.0.1:1" }},
		{"stalled server", stalledListener},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			t.Setenv("DB_CONNECT_TIMEOUT", "300ms")
			settings := loadDatabase(
				t,
				databaseURL(test.address(t), WriterRole, writeTestCA(t)),
			)
			started := time.Now()

			// when
			pool, err := OpenPool(t.Context(), settings)

			// then
			if pool != nil || err == nil ||
				err.Error() != "cannot connect to Payment PostgreSQL as writer" {
				t.Fatalf("OpenPool() = (%v, %v)", pool, err)
			}
			if elapsed := time.Since(
				started,
			); elapsed > settings.ConnectTimeout+time.Second {
				t.Errorf(
					"OpenPool() took %v, want within %v",
					elapsed,
					settings.ConnectTimeout,
				)
			}
		})
	}
}

func TestCheckReadiness(t *testing.T) {
	tests := []struct {
		name     string
		database *fakeDatabase
		wantErr  bool
	}{
		{"select one succeeds", &fakeDatabase{row: fakeRow{value: 1}}, false},
		{"unexpected value fails", &fakeDatabase{row: fakeRow{value: 2}}, true},
		{
			"query error fails",
			&fakeDatabase{row: fakeRow{err: errors.New("password " + sensitiveMarker)}},
			true,
		},
		{"stalled query fails at deadline", &fakeDatabase{block: true}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			err := CheckReadiness(
				context.Background(),
				test.database,
				50*time.Millisecond,
			)

			// then
			if test.database.sql != "SELECT 1" {
				t.Errorf("SQL = %q, want SELECT 1", test.database.sql)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("CheckReadiness() error = %v", err)
			}
			if test.wantErr && (err == nil ||
				err.Error() != "writer connection to Payment PostgreSQL is unavailable") {
				t.Errorf("CheckReadiness() error = %v, want the safe failure", err)
			}
		})
	}
}

type fakeDatabase struct {
	row   fakeRow
	block bool
	sql   string
}

func (database *fakeDatabase) QueryRow(ctx context.Context, sql string, _ ...any) pgx.Row {
	database.sql = sql
	if database.block {
		<-ctx.Done()
		return fakeRow{err: ctx.Err()}
	}
	return database.row
}

type fakeRow struct {
	value int
	err   error
}

func (row fakeRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	*dest[0].(*int) = row.value
	return nil
}

// loadDatabase obtains database settings through the production configuration loader.
func loadDatabase(t *testing.T, databaseURL string) config.Database {
	t.Helper()
	t.Setenv("PAYMENT_DATABASE_URL", databaseURL)
	t.Setenv("MINIO_ENDPOINT", "http://minio.example:9000")
	t.Setenv("MINIO_ACCESS_KEY", "access")
	t.Setenv("MINIO_SECRET_KEY", "secret")
	t.Setenv("KAFKA_BROKERS", "kafka.example:9093")
	t.Setenv("KAFKA_CA_FILE", "/etc/payment-service/kafka/ca.crt")
	settings, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return settings.Database
}

func databaseURL(address, user, caFile string) string {
	return "postgres://" + user + ":" + sensitiveMarker + "@" + address +
		"/payment?sslmode=verify-full&sslrootcert=" + caFile
}

func writeTestCA(t *testing.T) string {
	t.Helper()
	server := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "ca.crt")
	block := &pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	return path
}

// stalledListener accepts connections and never answers, so TLS negotiation stalls.
func stalledListener(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var accepted []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepted = append(accepted, connection)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		for _, connection := range accepted {
			_ = connection.Close()
		}
	})
	return listener.Addr().String()
}
