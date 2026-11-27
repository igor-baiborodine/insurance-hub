package postgres

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/postgres/dbgen"
)

const storageCleanupBudget = 5 * time.Second

// TxBeginner starts transactions on the process-owned writer pool.
type TxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// Store reads and writes Payment accounts through the runtime writer pool.
type Store struct {
	pool TxBeginner
}

// NewStore wraps the process-owned writer pool. The caller owns closing it.
func NewStore(pool TxBeginner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("create Payment store: pool is required")
	}
	return &Store{pool: pool}, nil
}

// ListAccounts returns the account projections in database-defined order.
func (store *Store) ListAccounts(
	ctx context.Context,
) (result []application.Account, resultErr error) {
	resultErr = store.readOnly(ctx, func(queries *dbgen.Queries) error {
		rows, err := queries.ListAccounts(ctx)
		if err != nil {
			return fmt.Errorf("read payment accounts: %w", err)
		}
		result = make([]application.Account, 0, len(rows))
		for _, row := range rows {
			account, err := accountProjection(
				row.PolicyAccountNumber,
				row.PolicyNumber,
				row.Created,
				row.Updated,
			)
			if err != nil {
				return err
			}
			result = append(result, account)
		}
		return nil
	})
	return result, resultErr
}

// ReadAccountBalance returns one account and its signed balance on the inclusive date.
func (store *Store) ReadAccountBalance(
	ctx context.Context,
	accountNumber string,
	businessDate time.Time,
) (result application.AccountBalance, resultErr error) {
	resultErr = store.readOnly(ctx, func(queries *dbgen.Queries) error {
		row, err := queries.GetAccountBalance(ctx, dbgen.GetAccountBalanceParams{
			BusinessDate:  pgtype.Date{Time: businessDate, Valid: true},
			AccountNumber: accountNumber,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return application.ErrAccountNotFound
		}
		if err != nil {
			return fmt.Errorf("read payment account balance: %w", err)
		}
		account, err := accountProjection(
			row.PolicyAccountNumber,
			row.PolicyNumber,
			row.Created,
			row.Updated,
		)
		if err != nil {
			return err
		}
		balance, err := decimalValue(row.Balance)
		if err != nil {
			return fmt.Errorf("read payment account balance: %w", err)
		}
		result = application.AccountBalance{Account: account, Balance: balance}
		return nil
	})
	return result, resultErr
}

// StoreRegistration creates one account and expected debit, or compares a durable replay.
func (store *Store) StoreRegistration(
	ctx context.Context,
	registration application.AccountRegistration,
) error {
	return store.write(ctx, func(queries *dbgen.Queries) error {
		coverageStart := pgtype.Date{Time: registration.CoverageStart, Valid: true}
		accountID, err := queries.InsertPolicyAccount(
			ctx,
			dbgen.InsertPolicyAccountParams{
				PolicyNumber:        registration.PolicyNumber,
				PolicyAccountNumber: registration.PolicyAccountNumber,
				RegisteredPremium:   registration.Premium,
				RegisteredCoverFrom: coverageStart,
				RegisteredAt: pgtype.Timestamp{
					Time:  registration.RegisteredAt,
					Valid: true,
				},
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			same, err := queries.RegistrationMatches(
				ctx,
				dbgen.RegistrationMatchesParams{
					RegisteredPremium:   registration.Premium,
					RegisteredCoverFrom: coverageStart,
					PolicyNumber:        registration.PolicyNumber,
				},
			)
			if err != nil {
				return fmt.Errorf("compare payment registration: %w", err)
			}
			if !same.Valid {
				return errors.New("compare payment registration: result is missing")
			}
			if !same.Bool {
				return application.ErrRegistrationConflict
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("insert payment account: %w", err)
		}
		registeredAt := registration.RegisteredAt.UTC()
		creationDate := pgtype.Date{
			Time: time.Date(
				registeredAt.Year(),
				registeredAt.Month(),
				registeredAt.Day(),
				0,
				0,
				0,
				0,
				time.UTC,
			),
			Valid: true,
		}
		if err := queries.InsertExpectedPayment(
			ctx,
			dbgen.InsertExpectedPaymentParams{
				PolicyAccountID: accountID,
				CreationDate:    creationDate,
				EffectiveDate:   coverageStart,
				Amount:          registration.Premium,
			},
		); err != nil {
			return fmt.Errorf("insert expected payment: %w", err)
		}
		return nil
	})
}

func (store *Store) readOnly(
	ctx context.Context,
	work func(*dbgen.Queries) error,
) (resultErr error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return beginError(ctx, "begin Payment read transaction", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			storageCleanupBudget,
		)
		defer cancel()
		if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("roll back Payment read transaction: %w", err),
			)
		}
	}()
	if err := work(dbgen.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Payment read transaction: %w", err)
	}
	return nil
}

func (store *Store) write(
	ctx context.Context,
	work func(*dbgen.Queries) error,
) (resultErr error) {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.ReadCommitted,
		AccessMode: pgx.ReadWrite,
	})
	if err != nil {
		return beginError(ctx, "begin Payment registration transaction", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			storageCleanupBudget,
		)
		defer cancel()
		if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("roll back Payment registration transaction: %w", err),
			)
		}
	}()
	if err := work(dbgen.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Payment registration transaction: %w", err)
	}
	return nil
}

func beginError(ctx context.Context, operation string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", operation, ctxErr)
	}
	if _, connect := errors.AsType[*pgconn.ConnectError](err); connect {
		return fmt.Errorf(
			"%s: %w: %w",
			operation,
			application.ErrStorageUnavailable,
			err,
		)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func accountProjection(
	accountNumber string,
	policyNumber string,
	created pgtype.Timestamp,
	updated pgtype.Timestamp,
) (application.Account, error) {
	if !created.Valid || !updated.Valid {
		return application.Account{}, errors.New(
			"read payment account: timestamp is missing",
		)
	}
	return application.Account{
		PolicyAccountNumber: accountNumber,
		PolicyNumber:        policyNumber,
		Created:             created.Time.UTC(),
		Updated:             updated.Time.UTC(),
	}, nil
}

func decimalValue(value pgtype.Numeric) (string, error) {
	if !value.Valid || value.NaN || value.InfinityModifier != pgtype.Finite {
		return "", errors.New("balance has an invalid numeric representation")
	}
	encoded, err := value.Value()
	if err != nil {
		return "", err
	}
	decimal, ok := encoded.(string)
	if !ok {
		return "", errors.New("balance has an unexpected numeric representation")
	}
	number, ok := new(big.Rat).SetString(decimal)
	if !ok {
		return "", errors.New("balance cannot be represented exactly")
	}
	return number.FloatString(2), nil
}
