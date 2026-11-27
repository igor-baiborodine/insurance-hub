package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewAccountReads_RequiresDependencies(t *testing.T) {
	// given
	store := &fakeAccountStore{}
	for _, test := range []struct {
		name  string
		store AccountStore
		now   func() time.Time
	}{
		{"missing store", nil, time.Now},
		{"missing clock", store, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			reads, err := NewAccountReads(test.store, test.now)

			// then
			if reads != nil || err == nil {
				t.Fatalf("NewAccountReads() = (%v, %v)", reads, err)
			}
		})
	}
}

func TestAccountReads_BalanceUsesOneUTCBusinessDate(t *testing.T) {
	// given
	store := &fakeAccountStore{balance: AccountBalance{Balance: "-10.00"}}
	clockCalls := 0
	reads, err := NewAccountReads(store, func() time.Time {
		clockCalls++
		return time.Date(
			2026,
			time.January,
			8,
			1,
			30,
			0,
			0,
			time.FixedZone("west", -5*60*60),
		)
	})
	if err != nil {
		t.Fatal(err)
	}

	// when
	got, err := reads.Balance(t.Context(), "SYN-ACCOUNT-A")

	// then
	if err != nil || got.Balance != "-10.00" {
		t.Fatalf("Balance() = (%+v, %v)", got, err)
	}
	wantDate := time.Date(2026, time.January, 8, 0, 0, 0, 0, time.UTC)
	if store.accountNumber != "SYN-ACCOUNT-A" || !store.businessDate.Equal(wantDate) {
		t.Errorf(
			"store input = (%q, %v), want account and %v",
			store.accountNumber,
			store.businessDate,
			wantDate,
		)
	}
	if clockCalls != 1 {
		t.Errorf("clock calls = %d, want 1", clockCalls)
	}
}

func TestAccountReads_ListReturnsAccounts(t *testing.T) {
	// given
	want := []Account{{PolicyAccountNumber: "SYN-ACCOUNT-A"}}
	reads, err := NewAccountReads(&fakeAccountStore{accounts: want}, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	// when
	got, err := reads.List(t.Context())

	// then
	if err != nil || len(got) != 1 || got[0] != want[0] {
		t.Fatalf("List() = (%+v, %v), want %+v", got, err, want)
	}
}

func TestAccountReads_PreservesFailures(t *testing.T) {
	// given
	storeErr := errors.New("storage marker")
	store := &fakeAccountStore{err: storeErr}
	reads, err := NewAccountReads(store, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		call func() error
	}{
		{
			name: "list",
			call: func() error {
				_, err := reads.List(t.Context())
				return err
			},
		},
		{
			name: "balance",
			call: func() error {
				_, err := reads.Balance(t.Context(), "missing")
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			err := test.call()

			// then
			if !errors.Is(err, storeErr) {
				t.Fatalf("error = %v, want storage marker", err)
			}
		})
	}
}

type fakeAccountStore struct {
	accounts      []Account
	balance       AccountBalance
	err           error
	accountNumber string
	businessDate  time.Time
}

func (store *fakeAccountStore) ListAccounts(context.Context) ([]Account, error) {
	return store.accounts, store.err
}

func (store *fakeAccountStore) ReadAccountBalance(
	_ context.Context,
	accountNumber string,
	businessDate time.Time,
) (AccountBalance, error) {
	store.accountNumber = accountNumber
	store.businessDate = businessDate
	return store.balance, store.err
}
