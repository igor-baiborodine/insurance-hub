package postgres

import (
	"context"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/postgres/dbgen"
)

func TestNewStore_RequiresPool(t *testing.T) {
	store, err := NewStore(nil)
	if store != nil || err == nil {
		t.Fatalf("NewStore(nil) = (%v, %v), want error", store, err)
	}
}

func TestStoreReadOnly_UsesBoundedReadTransaction(t *testing.T) {
	// given
	tx := &fakeTx{rollbackErr: pgx.ErrTxClosed}
	beginner := &fakeTxBeginner{tx: tx}
	store, err := NewStore(beginner)
	if err != nil {
		t.Fatal(err)
	}
	workCalls := 0

	// when
	err = store.readOnly(t.Context(), func(*dbgen.Queries) error {
		workCalls++
		return nil
	})
	// then
	if err != nil {
		t.Fatal(err)
	}
	if beginner.options.IsoLevel != pgx.RepeatableRead ||
		beginner.options.AccessMode != pgx.ReadOnly {
		t.Fatalf("transaction options = %+v", beginner.options)
	}
	if workCalls != 1 || tx.commitCalls != 1 || tx.rollbackCalls != 1 {
		t.Fatalf(
			"calls = work %d, commit %d, rollback %d",
			workCalls,
			tx.commitCalls,
			tx.rollbackCalls,
		)
	}
}

func TestStoreReadOnly_PreservesFailures(t *testing.T) {
	workErr := errors.New("work marker")
	commitErr := errors.New("commit marker")
	rollbackErr := errors.New("rollback marker")

	for _, test := range []struct {
		name        string
		beginErr    error
		workErr     error
		commitErr   error
		rollbackErr error
		want        []error
	}{
		{
			name:     "begin",
			beginErr: errors.New("begin marker"),
		},
		{
			name:        "work and rollback",
			workErr:     workErr,
			rollbackErr: rollbackErr,
			want:        []error{workErr, rollbackErr},
		},
		{
			name:      "commit",
			commitErr: commitErr,
			want:      []error{commitErr},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			tx := &fakeTx{
				commitErr:   test.commitErr,
				rollbackErr: test.rollbackErr,
			}
			beginner := &fakeTxBeginner{tx: tx, err: test.beginErr}
			store, err := NewStore(beginner)
			if err != nil {
				t.Fatal(err)
			}

			// when
			err = store.readOnly(t.Context(), func(*dbgen.Queries) error {
				return test.workErr
			})

			// then
			if err == nil {
				t.Fatal("readOnly() error = nil")
			}
			for _, want := range test.want {
				if !errors.Is(err, want) {
					t.Errorf("readOnly() error = %v, want %v", err, want)
				}
			}
		})
	}
}

func TestStoreListAccounts_PreservesQueryFailure(t *testing.T) {
	queryErr := errors.New("query marker")
	store, err := NewStore(&fakeTxBeginner{tx: &fakeTx{
		queryErr:    queryErr,
		rollbackErr: pgx.ErrTxClosed,
	}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.ListAccounts(t.Context())
	if !errors.Is(err, queryErr) {
		t.Fatalf("ListAccounts() error = %v, want query marker", err)
	}
}

func TestStoreReadAccountBalance_ValidatesDatabaseValues(t *testing.T) {
	queryErr := errors.New("query marker")
	validTime := pgtype.Timestamp{Time: time.Now(), Valid: true}
	for _, test := range []struct {
		name string
		row  pgx.Row
		want string
	}{
		{
			name: "query failure",
			row:  scanRow(func(...any) error { return queryErr }),
			want: "query marker",
		},
		{
			name: "missing timestamp",
			row:  accountBalanceRow(pgtype.Timestamp{}, validTime, validNumeric(-750, -2)),
			want: "timestamp is missing",
		},
		{
			name: "invalid balance",
			row:  accountBalanceRow(validTime, validTime, pgtype.Numeric{}),
			want: "invalid numeric representation",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, err := NewStore(&fakeTxBeginner{tx: &fakeTx{
				row:         test.row,
				rollbackErr: pgx.ErrTxClosed,
			}})
			if err != nil {
				t.Fatal(err)
			}

			_, err = store.ReadAccountBalance(t.Context(), "SYN-ACCOUNT-A", time.Now())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ReadAccountBalance() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBeginError_PrefersContextFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := beginError(ctx, errors.New("begin marker"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("beginError() = %v, want context canceled", err)
	}
}

func TestBeginError_ClassifiesConnectionFailure(t *testing.T) {
	config, err := pgconn.ParseConfig("postgres://payment@127.0.0.1/payment")
	if err != nil {
		t.Fatal(err)
	}
	dialErr := errors.New("dial marker")
	config.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		return nil, dialErr
	}
	connection, connectErr := pgconn.ConnectConfig(t.Context(), config)
	if connection != nil {
		t.Cleanup(func() { _ = connection.Close(t.Context()) })
	}
	if connectErr == nil {
		t.Fatal("ConnectConfig() error = nil")
	}

	err = beginError(t.Context(), connectErr)
	if !errors.Is(err, application.ErrStorageUnavailable) ||
		!errors.Is(err, dialErr) {
		t.Fatalf("beginError() = %v, want storage unavailable and dial marker", err)
	}
}

func TestAccountProjection_ValidatesAndNormalizesTimestamps(t *testing.T) {
	west := time.FixedZone("west", -5*60*60)
	created := time.Date(2026, 1, 1, 1, 2, 3, 0, west)
	updated := created.Add(time.Hour)

	account, err := accountProjection(
		"SYN-ACCOUNT-A",
		"SYN-POLICY-B",
		pgtype.Timestamp{Time: created, Valid: true},
		pgtype.Timestamp{Time: updated, Valid: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if account.PolicyAccountNumber != "SYN-ACCOUNT-A" ||
		account.PolicyNumber != "SYN-POLICY-B" ||
		account.Created.Location() != time.UTC ||
		account.Updated.Location() != time.UTC {
		t.Fatalf("accountProjection() = %+v", account)
	}

	if _, err := accountProjection(
		"SYN-ACCOUNT-A",
		"SYN-POLICY-B",
		pgtype.Timestamp{},
		pgtype.Timestamp{Time: updated, Valid: true},
	); err == nil {
		t.Fatal("accountProjection() accepted a missing timestamp")
	}
}

func TestDecimalValue_FormatsExactSignedAmounts(t *testing.T) {
	for _, test := range []struct {
		name  string
		value pgtype.Numeric
		want  string
	}{
		{
			name: "negative fractional",
			value: pgtype.Numeric{
				Int: big.NewInt(-750), Exp: -2, Valid: true,
			},
			want: "-7.50",
		},
		{
			name: "positive whole",
			value: pgtype.Numeric{
				Int: big.NewInt(5), Valid: true,
			},
			want: "5.00",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := decimalValue(test.value)
			if err != nil || got != test.want {
				t.Fatalf("decimalValue() = (%q, %v), want %q", got, err, test.want)
			}
		})
	}
}

func accountBalanceRow(
	created pgtype.Timestamp,
	updated pgtype.Timestamp,
	balance pgtype.Numeric,
) pgx.Row {
	return scanRow(func(destinations ...any) error {
		*destinations[0].(*string) = "SYN-ACCOUNT-A"
		*destinations[1].(*string) = "SYN-POLICY-B"
		*destinations[2].(*pgtype.Timestamp) = created
		*destinations[3].(*pgtype.Timestamp) = updated
		*destinations[4].(*pgtype.Numeric) = balance
		return nil
	})
}

func validNumeric(coefficient int64, exponent int32) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(coefficient), Exp: exponent, Valid: true}
}

func TestDecimalValue_RejectsInvalidRepresentations(t *testing.T) {
	for _, value := range []pgtype.Numeric{
		{},
		{Valid: true, NaN: true},
		{Valid: true, InfinityModifier: pgtype.Infinity},
	} {
		if _, err := decimalValue(value); err == nil ||
			!strings.Contains(err.Error(), "invalid numeric") {
			t.Fatalf("decimalValue(%+v) error = %v", value, err)
		}
	}
}

type fakeTxBeginner struct {
	tx      pgx.Tx
	err     error
	options pgx.TxOptions
}

func (beginner *fakeTxBeginner) BeginTx(
	_ context.Context,
	options pgx.TxOptions,
) (pgx.Tx, error) {
	beginner.options = options
	return beginner.tx, beginner.err
}

type fakeTx struct {
	pgx.Tx
	row           pgx.Row
	queryErr      error
	commitErr     error
	rollbackErr   error
	commitCalls   int
	rollbackCalls int
}

func (tx *fakeTx) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	return nil, tx.queryErr
}

func (tx *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return tx.row
}

func (tx *fakeTx) Commit(context.Context) error {
	tx.commitCalls++
	return tx.commitErr
}

func (tx *fakeTx) Rollback(context.Context) error {
	tx.rollbackCalls++
	return tx.rollbackErr
}

type scanRow func(...any) error

func (row scanRow) Scan(destinations ...any) error {
	return row(destinations...)
}
