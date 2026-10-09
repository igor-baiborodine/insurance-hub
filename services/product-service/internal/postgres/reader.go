package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/postgres/dbgen"
)

// Reader implements the application Product reader with generated, read-only PostgreSQL queries.
type Reader struct {
	pool           *pgxpool.Pool
	acquireTimeout time.Duration
	queryTimeout   time.Duration
}

var _ application.ProductReader = (*Reader)(nil)

// Open creates and verifies a bounded pool using the restricted runtime database settings.
func Open(ctx context.Context, settings config.Database) (*Reader, error) {
	return open(
		ctx,
		settings.URL.Value(),
		settings.MaxConnections,
		settings.ConnectTimeout,
		settings.AcquireTimeout,
		settings.QueryTimeout,
	)
}

func open(
	ctx context.Context,
	databaseURL string,
	maxConnections int32,
	connectTimeout time.Duration,
	acquireTimeout time.Duration,
	queryTimeout time.Duration,
) (*Reader, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, unavailable("parse connection settings", err)
	}
	poolConfig.MaxConns = maxConnections
	poolConfig.MinConns = 0
	poolConfig.ConnConfig.ConnectTimeout = connectTimeout

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err != nil {
		return nil, classifyContext("create pool", connectCtx, err)
	}
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, classifyContext("verify connection", connectCtx, err)
	}
	return &Reader{
		pool:           pool,
		acquireTimeout: acquireTimeout,
		queryTimeout:   queryTimeout,
	}, nil
}

// Close releases every connection owned by the reader pool.
func (reader *Reader) Close() {
	reader.pool.Close()
}

// ListProducts returns a complete decoded catalog or an error without a partial result.
func (reader *Reader) ListProducts(ctx context.Context) ([]domain.Product, error) {
	connection, err := reader.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer connection.Release()

	queryCtx, cancel := context.WithTimeout(ctx, reader.queryTimeout)
	defer cancel()
	rows, err := dbgen.New(connection).ListProducts(queryCtx)
	if err != nil {
		return nil, classifyContext("list products", queryCtx, err)
	}

	products := make([]domain.Product, 0, len(rows))
	for _, row := range rows {
		product, decodeErr := DecodeProduct(row.Code, row.Definition)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode listed product: %w", decodeErr)
		}
		products = append(products, product)
	}
	return products, nil
}

// GetProduct performs an exact parameterized code lookup.
func (reader *Reader) GetProduct(
	ctx context.Context,
	code string,
) (domain.Product, bool, error) {
	connection, err := reader.acquire(ctx)
	if err != nil {
		return domain.Product{}, false, err
	}
	defer connection.Release()

	queryCtx, cancel := context.WithTimeout(ctx, reader.queryTimeout)
	defer cancel()
	row, err := dbgen.New(connection).GetProduct(queryCtx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Product{}, false, nil
	}
	if err != nil {
		return domain.Product{}, false, classifyContext("get product", queryCtx, err)
	}
	product, err := DecodeProduct(row.Code, row.Definition)
	if err != nil {
		return domain.Product{}, false, fmt.Errorf("decode product: %w", err)
	}
	return product, true, nil
}

func (reader *Reader) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	acquireCtx, cancel := context.WithTimeout(ctx, reader.acquireTimeout)
	defer cancel()
	connection, err := reader.pool.Acquire(acquireCtx)
	if err != nil {
		return nil, classifyContext("acquire connection", acquireCtx, err)
	}
	return connection, nil
}

func classify(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("postgres %s: %w", operation, err)
	}
	return unavailable(operation, err)
}

func classifyContext(operation string, operationCtx context.Context, err error) error {
	if contextErr := operationCtx.Err(); contextErr != nil {
		return fmt.Errorf("postgres %s: %w", operation, contextErr)
	}
	return classify(operation, err)
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
