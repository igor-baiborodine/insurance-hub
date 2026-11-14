package postgres

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
)

type fakeWriterPool struct {
	transaction writerTransaction
	err         error
}

func (pool fakeWriterPool) Begin(context.Context) (writerTransaction, error) {
	return pool.transaction, pool.err
}

type fakeWriterTransaction struct {
	query      string
	arguments  []any
	execErr    error
	commitErr  error
	committed  bool
	rolledBack bool
}

func (transaction *fakeWriterTransaction) Exec(
	_ context.Context,
	query string,
	arguments ...any,
) (pgconn.CommandTag, error) {
	transaction.query = query
	transaction.arguments = arguments
	return pgconn.CommandTag{}, transaction.execErr
}

func (transaction *fakeWriterTransaction) Commit(context.Context) error {
	transaction.committed = true
	return transaction.commitErr
}

func (transaction *fakeWriterTransaction) Rollback(context.Context) error {
	transaction.rolledBack = true
	return nil
}

func TestWriter_InsertObjectKey_UsesAcceptedTransaction(t *testing.T) {
	// given
	transaction := &fakeWriterTransaction{}
	writer, err := newWriter(fakeWriterPool{transaction: transaction}, time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	// when
	err = writer.InsertObjectKey(
		context.Background(),
		"SYN-363",
		"2026/02/SYN-363.pdf",
	)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if !transaction.committed || transaction.rolledBack {
		t.Fatalf(
			"committed = %t, rolled back = %t",
			transaction.committed,
			transaction.rolledBack,
		)
	}
	if !strings.Contains(transaction.query, "nextval('public.hibernate_sequence')") ||
		!strings.Contains(transaction.query, "INSERT INTO public.policy_document") {
		t.Fatalf("query = %q", transaction.query)
	}
	if len(transaction.arguments) != 2 ||
		!bytes.Equal(transaction.arguments[0].([]byte), []byte("2026/02/SYN-363.pdf")) ||
		transaction.arguments[1] != "SYN-363" {
		t.Fatalf("arguments = %#v", transaction.arguments)
	}
}

func TestWriter_InsertObjectKey_RollsBackFailuresSafely(t *testing.T) {
	cause := errors.New("private-password")
	tests := []struct {
		name         string
		poolError    error
		execError    error
		commitError  error
		wantRollback bool
	}{
		{"begin", cause, nil, nil, false},
		{"insert", nil, cause, nil, true},
		{"commit", nil, nil, cause, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			transaction := &fakeWriterTransaction{
				execErr: test.execError, commitErr: test.commitError,
			}
			writer, err := newWriter(
				fakeWriterPool{transaction: transaction, err: test.poolError},
				time.Second,
				time.Second,
			)
			if err != nil {
				t.Fatal(err)
			}

			// when
			err = writer.InsertObjectKey(
				context.Background(),
				"SYN-363",
				"2026/02/SYN-363.pdf",
			)

			// then
			if !errors.Is(err, application.ErrUnavailable) || !errors.Is(err, cause) ||
				strings.Contains(err.Error(), "private-password") {
				t.Fatalf("error = %v", err)
			}
			if transaction.rolledBack != test.wantRollback {
				t.Fatalf(
					"rolled back = %t, want %t",
					transaction.rolledBack,
					test.wantRollback,
				)
			}
		})
	}
}

func TestWriter_InsertObjectKey_RejectsInvalidInput(t *testing.T) {
	writer, err := newWriter(
		fakeWriterPool{transaction: &fakeWriterTransaction{}},
		time.Second,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.InsertObjectKey(context.Background(), "", "key"); err == nil {
		t.Fatal("empty policy number accepted")
	}
	if err := writer.InsertObjectKey(context.Background(), "SYN-363", ""); err == nil {
		t.Fatal("empty object key accepted")
	}
	if _, err := newWriter(nil, 0, 0); err == nil {
		t.Fatal("empty writer settings accepted")
	}
}
