package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
)

//go:embed sql/find_documents.sql
var findDocumentsSQL string

// Reader implements the application metadata port over the process-owned pool.
type Reader struct {
	pool                 readerPool
	acquireTimeout       time.Duration
	queryTimeout         time.Duration
	maximumDocumentBytes int
	maximumResponseBytes int
}

type readerPool interface {
	Acquire(context.Context) (readerConnection, error)
}

type readerConnection interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Release()
}

type pgxReaderPool struct{ pool *pgxpool.Pool }

func (pool pgxReaderPool) Acquire(ctx context.Context) (readerConnection, error) {
	return pool.pool.Acquire(ctx)
}

var _ application.MetadataReader = (*Reader)(nil)

// NewReader constructs a bounded read-only Document metadata adapter.
func NewReader(
	pool *pgxpool.Pool,
	acquireTimeout time.Duration,
	queryTimeout time.Duration,
	maximumDocumentBytes int,
	maximumResponseBytes int,
) (*Reader, error) {
	if pool == nil {
		return nil, errors.New("create PostgreSQL reader: pool is required")
	}
	if acquireTimeout <= 0 || queryTimeout <= 0 || maximumDocumentBytes <= 0 ||
		maximumResponseBytes <= 0 {
		return nil, errors.New("create PostgreSQL reader: limits must be positive")
	}
	return newReader(
		pgxReaderPool{pool: pool},
		acquireTimeout,
		queryTimeout,
		maximumDocumentBytes,
		maximumResponseBytes,
	)
}

func newReader(
	pool readerPool,
	acquireTimeout time.Duration,
	queryTimeout time.Duration,
	maximumDocumentBytes int,
	maximumResponseBytes int,
) (*Reader, error) {
	if pool == nil {
		return nil, errors.New("create PostgreSQL reader: pool is required")
	}
	if acquireTimeout <= 0 || queryTimeout <= 0 || maximumDocumentBytes <= 0 ||
		maximumResponseBytes <= 0 {
		return nil, errors.New("create PostgreSQL reader: limits must be positive")
	}
	return &Reader{
		pool: pool, acquireTimeout: acquireTimeout, queryTimeout: queryTimeout,
		maximumDocumentBytes: maximumDocumentBytes, maximumResponseBytes: maximumResponseBytes,
	}, nil
}

// FindByPolicyNumber returns all exact matches without ordering or deduplication.
func (reader *Reader) FindByPolicyNumber(
	ctx context.Context,
	policyNumber string,
) ([]domain.StoredDocument, error) {
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, reader.acquireTimeout)
	connection, err := reader.pool.Acquire(acquireCtx)
	if err != nil {
		classified := classifyContext("acquire connection", acquireCtx, err)
		cancelAcquire()
		return nil, classified
	}
	cancelAcquire()
	defer connection.Release()

	queryCtx, cancelQuery := context.WithTimeout(ctx, reader.queryTimeout)
	defer cancelQuery()
	rows, err := connection.Query(
		queryCtx,
		findDocumentsSQL,
		policyNumber,
		reader.maximumDocumentBytes,
	)
	if err != nil {
		return nil, classifyContext("find documents", queryCtx, err)
	}
	defer rows.Close()

	documents := make([]domain.StoredDocument, 0)
	totalStoredBytes := 0
	for rows.Next() {
		var (
			rowPolicyNumber string
			storedBytes     []byte
			storedSize      int
			storedNull      bool
		)
		if err := rows.Scan(
			&rowPolicyNumber,
			&storedBytes,
			&storedSize,
			&storedNull,
		); err != nil {
			return nil, classifyContext("decode document row", queryCtx, err)
		}
		if !storedNull && storedSize > reader.maximumDocumentBytes {
			return nil, application.ErrResponseLimit
		}
		if !storedNull && storedSize > reader.maximumResponseBytes-totalStoredBytes {
			return nil, application.ErrResponseLimit
		}
		if !storedNull {
			totalStoredBytes += storedSize
		}
		documents = append(documents, domain.StoredDocument{
			PolicyNumber: rowPolicyNumber,
			Bytes:        storedBytes,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, classifyContext("iterate documents", queryCtx, err)
	}
	return documents, nil
}

func classifyContext(operation string, operationCtx context.Context, err error) error {
	if contextErr := operationCtx.Err(); contextErr != nil {
		return fmt.Errorf("postgres %s: %w", operation, contextErr)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("postgres %s: %w", operation, err)
	}
	return unavailable(operation, err)
}

func unavailable(operation string, cause error) error {
	return readerError{operation: operation, cause: cause}
}

type readerError struct {
	operation string
	cause     error
}

func (failure readerError) Error() string {
	return "postgres " + failure.operation + ": " + application.ErrUnavailable.Error()
}

func (failure readerError) Unwrap() []error {
	return []error{application.ErrUnavailable, failure.cause}
}
