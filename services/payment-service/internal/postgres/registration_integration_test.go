//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/postgresfixture"
)

func TestRun_PaymentRegistrationTransaction(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	fixture.Call(t, ctx, "migrate", "version=1")
	pool, err := postgres.OpenPool(ctx, writerSettings(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	registeredAt := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.UTC)
	registrations, err := application.NewPolicyRegistrations(
		store,
		func() time.Time { return registeredAt },
	)
	if err != nil {
		t.Fatal(err)
	}
	registration := application.PolicyRegistration{
		PolicyNumber: "SYN-442-001", Premium: "120.00",
		CoverageStart: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}

	// when
	err = registrations.RegisterPolicy(ctx, registration)
	// then
	if err != nil {
		t.Fatal(err)
	}
	first := readRegistrationState(t, ctx, fixture, registration.PolicyNumber)
	if first.premium != "120.00" || first.coverageStart != "2026-01-01" ||
		first.kind != "expected_payment" || first.creationDate != "2026-01-02" ||
		first.effectiveDate != "2026-01-01" || first.amount != "120.00" ||
		first.entryCount != 1 {
		t.Fatal("first registration facts differ from the accepted contract")
	}
	assertRegistrationReads(t, ctx, store, first.accountNumber)

	t.Run("identical replay preserves the original account and debit", func(t *testing.T) {
		// when
		err := registrations.RegisterPolicy(ctx, registration)
		// then
		if err != nil {
			t.Fatal(err)
		}
		if replayed := readRegistrationState(
			t, ctx, fixture, registration.PolicyNumber,
		); replayed != first {
			t.Fatal("identical replay changed durable registration state")
		}
	})

	t.Run("changed replay facts conflict without mutation", func(t *testing.T) {
		for _, changed := range []application.PolicyRegistration{
			{
				PolicyNumber: registration.PolicyNumber, Premium: "120.01",
				CoverageStart: registration.CoverageStart,
			},
			{
				PolicyNumber: registration.PolicyNumber, Premium: registration.Premium,
				CoverageStart: registration.CoverageStart.AddDate(0, 0, 1),
			},
		} {
			// when
			err := registrations.RegisterPolicy(ctx, changed)

			// then
			if !errors.Is(err, application.ErrRegistrationConflict) {
				t.Fatalf(
					"changed replay error = %v, want registration conflict",
					err,
				)
			}
			if got := readRegistrationState(
				t, ctx, fixture, registration.PolicyNumber,
			); got != first {
				t.Fatal("conflicting replay changed durable registration state")
			}
		}
	})

	t.Run("debit failure rolls back the account before retry", func(t *testing.T) {
		// given
		rollback := application.PolicyRegistration{
			PolicyNumber: "SYN-442-ROLLBACK", Premium: "50.00",
			CoverageStart: registration.CoverageStart,
		}
		fixture.Exec(
			t,
			ctx,
			"REVOKE INSERT ON public.accounting_entry FROM go_payment_writer",
		)

		// when
		err := registrations.RegisterPolicy(ctx, rollback)
		fixture.Exec(
			t,
			ctx,
			"GRANT INSERT ON public.accounting_entry TO go_payment_writer",
		)

		// then
		if err == nil {
			t.Fatal("registration succeeded without accounting-entry INSERT access")
		}
		accounts, entries := registrationCounts(t, ctx, fixture, rollback.PolicyNumber)
		if accounts != 0 || entries != 0 {
			t.Fatal("failed debit left partial registration state")
		}
		if err := registrations.RegisterPolicy(ctx, rollback); err != nil {
			t.Fatal("retry after restored access:", err)
		}
		accounts, entries = registrationCounts(t, ctx, fixture, rollback.PolicyNumber)
		if accounts != 1 || entries != 1 {
			t.Fatal("registration retry did not create exactly one account and debit")
		}
	})

	t.Run("overlapping registrations converge", func(t *testing.T) {
		// given
		overlap := application.PolicyRegistration{
			PolicyNumber: "SYN-442-OVERLAP", Premium: "75.00",
			CoverageStart: registration.CoverageStart,
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		workers.Add(2)
		for range 2 {
			go func() {
				defer workers.Done()
				<-start
				results <- registrations.RegisterPolicy(ctx, overlap)
			}()
		}

		// when
		close(start)
		workers.Wait()
		close(results)

		// then
		for err := range results {
			if err != nil {
				t.Fatal("overlapping registration:", err)
			}
		}
		accounts, entries := registrationCounts(t, ctx, fixture, overlap.PolicyNumber)
		if accounts != 1 || entries != 1 {
			t.Fatal(
				"overlapping registrations did not converge on one account and debit",
			)
		}
	})
}

type durableRegistration struct {
	accountNumber string
	premium       string
	coverageStart string
	kind          string
	creationDate  string
	effectiveDate string
	amount        string
	entryCount    int
}

func readRegistrationState(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	policyNumber string,
) durableRegistration {
	t.Helper()
	var state durableRegistration
	err := fixture.Admin.QueryRow(ctx, `
SELECT a.policy_account_number, a.registered_premium::text,
    a.registered_cover_from::text, min(e.kind), min(e.creation_date)::text,
    min(e.effective_date)::text, min(e.amount)::text, count(e.id)::integer
FROM public.policy_account AS a
JOIN public.accounting_entry AS e ON e.policy_account_id = a.id
WHERE a.policy_number = $1
GROUP BY a.id, a.policy_account_number, a.registered_premium, a.registered_cover_from`,
		policyNumber,
	).Scan(
		&state.accountNumber,
		&state.premium,
		&state.coverageStart,
		&state.kind,
		&state.creationDate,
		&state.effectiveDate,
		&state.amount,
		&state.entryCount,
	)
	if err != nil {
		t.Fatal("read durable registration state:", err)
	}
	return state
}

func registrationCounts(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	policyNumber string,
) (int, int) {
	t.Helper()
	var accounts, entries int
	err := fixture.Admin.QueryRow(ctx, `
SELECT count(DISTINCT a.id)::integer, count(e.id)::integer
FROM public.policy_account AS a
LEFT JOIN public.accounting_entry AS e ON e.policy_account_id = a.id
WHERE a.policy_number = $1`, policyNumber).Scan(&accounts, &entries)
	if err != nil {
		t.Fatal("read registration counts:", err)
	}
	return accounts, entries
}

func assertRegistrationReads(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	accountNumber string,
) {
	t.Helper()
	businessTime := time.Date(2025, time.December, 31, 0, 0, 0, 0, time.UTC)
	reads, err := application.NewAccountReads(store, func() time.Time { return businessTime })
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := reads.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, account := range accounts {
		if account.PolicyAccountNumber == accountNumber {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("registered account is missing from the private list")
	}
	before, err := reads.Balance(ctx, accountNumber)
	if err != nil || before.Balance != "0.00" {
		t.Fatal("registered account balance before coverage differs")
	}
	businessTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	effective, err := reads.Balance(ctx, accountNumber)
	if err != nil || effective.Balance != "-120.00" {
		t.Fatal("registered account balance on coverage start differs")
	}
}
