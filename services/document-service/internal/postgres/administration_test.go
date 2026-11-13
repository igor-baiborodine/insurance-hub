package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestConfigureMigrationTunnel_AcceptsOnlyLoopback(t *testing.T) {
	// given
	config, err := pgx.ParseConfig(
		"postgresql://synthetic:synthetic@document/document?sslmode=disable&hostaddr=127.0.0.1&port=15432",
	)
	if err != nil {
		t.Fatal(err)
	}
	// when
	tunneled, err := configureMigrationTunnel(config)
	// then
	if err != nil || !tunneled || config.Host != "document" || config.Port != 15432 {
		t.Fatalf(
			"unexpected tunnel configuration: host=%s port=%d error=%v",
			config.Host,
			config.Port,
			err,
		)
	}
	addresses, err := config.LookupFunc(t.Context(), config.Host)
	if err != nil || len(addresses) != 1 || addresses[0] != "127.0.0.1" {
		t.Fatalf("unexpected tunnel lookup: %v %v", addresses, err)
	}
	if _, present := config.RuntimeParams["hostaddr"]; present {
		t.Fatal("hostaddr leaked into PostgreSQL runtime parameters")
	}

	foreign, err := pgx.ParseConfig(
		"postgresql://synthetic:synthetic@document/document?sslmode=disable&hostaddr=192.0.2.1&port=15432",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configureMigrationTunnel(foreign); err == nil {
		t.Fatal("foreign migration tunnel was accepted")
	}
}

func TestOpenMigrationDatabase_RejectsInvalidConfigurationAndCancellation(t *testing.T) {
	// given
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, url := range []string{"not-a-postgres-url", "postgres://synthetic@localhost/document"} {
		t.Run(url, func(t *testing.T) {
			// when
			db, err := OpenMigrationDatabase(ctx, url)
			// then
			if err == nil || db != nil {
				t.Fatal("invalid or canceled connection was accepted")
			}
			if url != "not-a-postgres-url" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was lost: %v", err)
			}
		})
	}
}
