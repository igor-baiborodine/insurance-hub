package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
)

//go:embed sql/insert_document.sql
var insertDocumentSQL string

type writerTransaction interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}

type writerPool interface {
	Begin(context.Context) (writerTransaction, error)
}

type pgxWriterPool struct{ pool *pgxpool.Pool }

func (pool pgxWriterPool) Begin(ctx context.Context) (writerTransaction, error) {
	transaction, err := pool.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	return transaction, nil
}

// Writer inserts object-backed Document metadata through the accepted sequence.
type Writer struct {
	pool           writerPool
	acquireTimeout time.Duration
	queryTimeout   time.Duration
}

var _ application.DocumentMetadataWriter = (*Writer)(nil)

// NewWriter constructs a bounded metadata writer over its process-owned writer pool.
func NewWriter(
	pool *pgxpool.Pool,
	acquireTimeout time.Duration,
	queryTimeout time.Duration,
) (*Writer, error) {
	if pool == nil {
		return nil, errors.New("create PostgreSQL writer: pool is required")
	}
	return newWriter(pgxWriterPool{pool: pool}, acquireTimeout, queryTimeout)
}

func newWriter(
	pool writerPool,
	acquireTimeout time.Duration,
	queryTimeout time.Duration,
) (*Writer, error) {
	if pool == nil || acquireTimeout <= 0 || queryTimeout <= 0 {
		return nil, errors.New("create PostgreSQL writer: pool and limits are required")
	}
	return &Writer{pool: pool, acquireTimeout: acquireTimeout, queryTimeout: queryTimeout}, nil
}

// InsertObjectKey records one UTF-8 object key in one database transaction.
func (writer *Writer) InsertObjectKey(
	ctx context.Context,
	policyNumber string,
	objectKey string,
) error {
	if policyNumber == "" || objectKey == "" || !utf8.ValidString(objectKey) {
		return errors.New("insert policy document: identity and object key are required")
	}
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, writer.acquireTimeout)
	transaction, err := writer.pool.Begin(acquireCtx)
	if err != nil {
		classified := classifyWriteFailure("begin transaction", acquireCtx, err)
		cancelAcquire()
		return classified
	}
	cancelAcquire()
	operationCtx, cancelOperation := context.WithTimeout(ctx, writer.queryTimeout)
	defer cancelOperation()
	if _, err := transaction.Exec(
		operationCtx,
		insertDocumentSQL,
		[]byte(objectKey),
		policyNumber,
	); err != nil {
		rollbackWriter(transaction, writer.queryTimeout)
		return classifyWriteFailure("insert metadata", operationCtx, err)
	}
	if err := transaction.Commit(operationCtx); err != nil {
		rollbackWriter(transaction, writer.queryTimeout)
		return classifyWriteFailure("commit metadata", operationCtx, err)
	}
	return nil
}

func rollbackWriter(transaction writerTransaction, timeout time.Duration) {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = transaction.Rollback(rollbackCtx)
}

func classifyWriteFailure(
	operation string,
	operationCtx context.Context,
	cause error,
) error {
	if err := operationCtx.Err(); err != nil {
		return fmt.Errorf("postgres %s: %w", operation, err)
	}
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf("postgres %s: %w", operation, cause)
	}
	return metadataWriteError{operation: operation, cause: cause}
}

type metadataWriteError struct {
	operation string
	cause     error
}

func (failure metadataWriteError) Error() string {
	return "postgres " + failure.operation + ": " + application.ErrUnavailable.Error()
}

func (failure metadataWriteError) Unwrap() []error {
	return []error{application.ErrUnavailable, failure.cause}
}
