//go:build integration

package postgres_test

import (
	"context"
	_ "embed"
	"errors"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/postgresfixture"
)

//go:embed testdata/seed-account-reads.sql
var seedAccountReadsQuery string

func TestRun_PaymentAccountReads(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	fixture.Call(t, ctx, "migrate", "version=1")
	fixture.Exec(t, ctx, seedAccountReadsQuery)
	pool, err := postgres.OpenPool(ctx, writerSettings(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	businessTime := time.Date(2025, time.December, 31, 23, 0, 0, 0, time.UTC)
	reads, err := application.NewAccountReads(store, func() time.Time { return businessTime })
	if err != nil {
		t.Fatal(err)
	}
	before := businessCounts(t, ctx, fixture)

	// when
	accounts, err := reads.List(ctx)
	// then
	if err != nil {
		t.Fatal(err)
	}
	account := findAccount(t, accounts, "SYN-ACCOUNT-A")
	if account.PolicyNumber != "SYN-POLICY-B" ||
		!account.Created.Equal(time.Date(2025, time.December, 20, 10, 0, 0, 0, time.UTC)) ||
		!account.Updated.Equal(time.Date(2026, time.January, 2, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("account projection = %+v", account)
	}

	for _, test := range []struct {
		name    string
		date    time.Time
		balance string
	}{
		{"before entries", time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC), "0.00"},
		{"first debit effective date", time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC), "-10.00"},
		{"credit effective date", time.Date(2026, 1, 7, 23, 0, 0, 0, time.UTC), "5.00"},
		{"second debit effective date", time.Date(2026, 6, 1, 23, 0, 0, 0, time.UTC), "-5.00"},
		{"outgoing debit effective date", time.Date(2026, 6, 2, 23, 0, 0, 0, time.UTC), "-7.50"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			businessTime = test.date

			// when
			got, err := reads.Balance(ctx, "SYN-ACCOUNT-A")

			// then
			if err != nil || got.Balance != test.balance {
				t.Fatalf("Balance() = (%+v, %v), want %s", got, err, test.balance)
			}
			if got.PolicyAccountNumber != "SYN-ACCOUNT-A" ||
				got.PolicyNumber != "SYN-POLICY-B" {
				t.Errorf(
					"identifiers = (%q, %q)",
					got.PolicyAccountNumber,
					got.PolicyNumber,
				)
			}
		})
	}
	t.Run("missing account", func(t *testing.T) {
		// when
		_, err := reads.Balance(ctx, "SYN-ACCOUNT-MISSING")

		// then
		if !errors.Is(err, application.ErrAccountNotFound) {
			t.Fatalf("missing Balance() error = %v, want account not found", err)
		}
	})

	// when
	after := businessCounts(t, ctx, fixture)

	// then
	if after != before {
		t.Fatalf("account reads changed rows: before %q, after %q", before, after)
	}
}

func writerSettings(t *testing.T, fixture *postgresfixture.Fixture) config.Database {
	t.Helper()
	t.Setenv("PAYMENT_DATABASE_URL", fixture.WriterURL("payment"))
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

func findAccount(t *testing.T, accounts []application.Account, number string) application.Account {
	t.Helper()
	for _, account := range accounts {
		if account.PolicyAccountNumber == number {
			return account
		}
	}
	t.Fatalf("account %q not found in %+v", number, accounts)
	return application.Account{}
}

func businessCounts(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
) string {
	t.Helper()
	return fixture.Query(t, ctx, `
SELECT format('accounts=%s;entries=%s;receipts=%s',
    (SELECT count(*) FROM public.policy_account),
    (SELECT count(*) FROM public.accounting_entry),
    (SELECT count(*) FROM public.statement_receipt));`)
}
