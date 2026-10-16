package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/postgres/dbgen"
)

// Reader implements the application Product reader with generated, read-only PostgreSQL queries.
type Reader struct {
	pool           *pgxpool.Pool
	acquireTimeout time.Duration
	queryTimeout   time.Duration
	observer       *Observer
}

var _ application.ProductReader = (*Reader)(nil)

// ListProducts returns a complete decoded catalog or an error without a partial result.
func (reader *Reader) ListProducts(ctx context.Context) ([]domain.Product, error) {
	connection, err := reader.acquire(ctx, "list")
	if err != nil {
		return nil, err
	}
	defer connection.Release()

	queryCtx, cancel := context.WithTimeout(ctx, reader.queryTimeout)
	defer cancel()
	var rows []dbgen.Product
	err = reader.observer.stage(
		queryCtx,
		"list",
		"query",
		"SELECT products",
		trace.SpanKindClient,
		func(ctx context.Context) error {
			var queryErr error
			rows, queryErr = dbgen.New(connection).ListProducts(ctx)
			if queryErr != nil {
				return classifyContext("list products", ctx, queryErr)
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	products := make([]domain.Product, 0, len(rows))
	for _, row := range rows {
		product, decodeErr := DecodeProduct(row.Code, row.Definition)
		if decodeErr != nil {
			reader.observer.decode(queryCtx, "list", decodeErr)
			return nil, fmt.Errorf("decode listed product: %w", decodeErr)
		}
		products = append(products, product)
	}
	reader.observer.decode(queryCtx, "list", nil)
	return products, nil
}

// GetProduct performs an exact parameterized code lookup.
func (reader *Reader) GetProduct(
	ctx context.Context,
	code string,
) (domain.Product, bool, error) {
	connection, err := reader.acquire(ctx, "get")
	if err != nil {
		return domain.Product{}, false, err
	}
	defer connection.Release()

	queryCtx, cancel := context.WithTimeout(ctx, reader.queryTimeout)
	defer cancel()
	var row dbgen.Product
	missing := false
	err = reader.observer.stage(
		queryCtx,
		"get",
		"query",
		"SELECT product",
		trace.SpanKindClient,
		func(ctx context.Context) error {
			var queryErr error
			row, queryErr = dbgen.New(connection).GetProduct(ctx, code)
			if errors.Is(queryErr, pgx.ErrNoRows) {
				missing = true
				return nil
			}
			if queryErr != nil {
				return classifyContext("get product", ctx, queryErr)
			}
			return nil
		},
	)
	if missing {
		return domain.Product{}, false, nil
	}
	if err != nil {
		return domain.Product{}, false, err
	}
	product, err := DecodeProduct(row.Code, row.Definition)
	reader.observer.decode(queryCtx, "get", err)
	if err != nil {
		return domain.Product{}, false, fmt.Errorf("decode product: %w", err)
	}
	return product, true, nil
}

func (reader *Reader) acquire(ctx context.Context, operation string) (*pgxpool.Conn, error) {
	readerContext := ctx
	acquireCtx, cancel := context.WithTimeout(ctx, reader.acquireTimeout)
	defer cancel()
	var connection *pgxpool.Conn
	err := reader.observer.stage(
		acquireCtx,
		operation,
		"acquire",
		"product.db.acquire",
		trace.SpanKindInternal,
		func(ctx context.Context) error {
			started := time.Now()
			var acquireErr error
			connection, acquireErr = reader.pool.Acquire(ctx)
			if reader.observer != nil {
				reader.observer.waitTime.Record(
					ctx,
					time.Since(started).Seconds(),
					poolAttributes,
				)
				if acquireErr != nil &&
					errors.Is(ctx.Err(), context.DeadlineExceeded) &&
					readerContext.Err() == nil {
					reader.observer.timeouts.Add(ctx, 1, poolAttributes)
				}
			}
			if acquireErr != nil {
				return classifyContext("acquire connection", ctx, acquireErr)
			}
			return nil
		},
	)
	return connection, err
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
