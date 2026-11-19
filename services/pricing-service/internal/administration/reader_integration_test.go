//go:build integration

package administration_test

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/service"
)

func verifyReaderStartup(
	t *testing.T, ctx context.Context, admin *pgx.Conn, address *url.URL, ca, password string,
) {
	t.Helper()
	t.Run("reader access and write denials", func(t *testing.T) {
		// given
		connectionURL := *address
		connectionURL.User = url.UserPassword("go_pricing_reader", password)
		connectionURL.RawQuery = url.Values{
			"sslmode":     {"verify-full"},
			"sslrootcert": {ca},
		}.Encode()
		reader, err := pgx.Connect(ctx, connectionURL.String())
		if err != nil {
			t.Fatal("could not authenticate as the disposable Pricing reader")
		}
		t.Cleanup(func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = reader.Close(closeCtx)
		})
		before := seedSnapshot(t, ctx, admin)

		// when
		got := queryString(t, ctx, reader, readTariffCodesQuery)

		// then
		if got != "CAR,FAI,HSI,TRI" {
			t.Fatalf("reader tariff codes = %q", got)
		}
		for _, statement := range []string{denyTariffUpdateQuery, denyTariffAlterQuery} {
			// given
			tx, err := reader.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// when
			_, execErr := tx.Exec(ctx, statement)
			_ = tx.Rollback(ctx)
			// then
			var pgErr *pgconn.PgError
			if !errors.As(execErr, &pgErr) || pgErr.Code != "42501" {
				t.Fatalf("want insufficient privilege (42501), got %v", execErr)
			}
		}

		// given
		t.Setenv("PRICING_DATABASE_URL", connectionURL.String())
		t.Setenv("HTTP_ADDR", "127.0.0.1:0")
		t.Setenv("GRPC_ADDR", "127.0.0.1:0")
		t.Setenv("HEALTH_ADDR", "127.0.0.1:0")
		settings, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		started := make(startupLog, 1)
		done := make(chan error, 1)

		// when
		go func() {
			done <- service.Run(runCtx, settings, slog.New(slog.NewTextHandler(started, nil)))
		}()

		// then
		select {
		case <-started:
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(12 * time.Second):
				t.Fatal("Pricing did not stop")
			}
		case err := <-done:
			t.Fatalf("Pricing stopped before serving: %v", err)
		case <-runCtx.Done():
			t.Fatal("Pricing did not start")
		}
		if seedSnapshot(t, ctx, admin) != before {
			t.Fatal("Pricing startup changed migration history or tariff data")
		}
	})
}

type startupLog chan struct{}

func (signal startupLog) Write(value []byte) (int, error) {
	if strings.Contains(string(value), "service started") {
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	return len(value), nil
}
