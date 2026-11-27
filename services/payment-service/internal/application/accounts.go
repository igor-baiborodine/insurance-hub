// Package application owns Payment use cases, the ports they consume and their failures.
package application

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrAccountNotFound reports that no Payment account has the submitted account number.
	ErrAccountNotFound = errors.New("payment account not found")
	// ErrStorageUnavailable reports that Payment cannot start a storage operation.
	ErrStorageUnavailable = errors.New("payment storage unavailable")
)

// Account is the caller-visible identity and timestamp projection of one Payment account.
type Account struct {
	PolicyAccountNumber string
	PolicyNumber        string
	Created             time.Time
	Updated             time.Time
}

// AccountBalance adds the exact balance as of one UTC business date.
type AccountBalance struct {
	Account
	Balance string
}

// AccountStore reads Payment accounts and their dated balances.
type AccountStore interface {
	ListAccounts(context.Context) ([]Account, error)
	ReadAccountBalance(context.Context, string, time.Time) (AccountBalance, error)
}

// AccountReads serves account list and dated-balance queries.
type AccountReads struct {
	accounts AccountStore
	now      func() time.Time
}

// NewAccountReads wires the Payment account queries. The clock defines the UTC business date.
func NewAccountReads(accounts AccountStore, now func() time.Time) (*AccountReads, error) {
	if accounts == nil || now == nil {
		return nil, errors.New("account reads: all dependencies are required")
	}
	return &AccountReads{accounts: accounts, now: now}, nil
}

// List returns every Payment account without imposing an order.
func (reads *AccountReads) List(ctx context.Context) ([]Account, error) {
	accounts, err := reads.accounts.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list payment accounts: %w", err)
	}
	return accounts, nil
}

// Balance returns one account with its exact balance on the current UTC business date.
func (reads *AccountReads) Balance(
	ctx context.Context,
	accountNumber string,
) (AccountBalance, error) {
	instant := reads.now().UTC()
	businessDate := time.Date(
		instant.Year(), instant.Month(), instant.Day(), 0, 0, 0, 0, time.UTC,
	)
	account, err := reads.accounts.ReadAccountBalance(ctx, accountNumber, businessDate)
	if err != nil {
		return AccountBalance{}, fmt.Errorf("read payment account balance: %w", err)
	}
	return account, nil
}
