package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
)

// OpenPool constructs and verifies the bounded process-owned PostgreSQL pool.
func OpenPool(ctx context.Context, settings config.Database) (*pgxpool.Pool, error) {
	return openPool(
		ctx,
		settings.URL.Value(),
		settings.MaxConnections,
		settings.ConnectTimeout,
	)
}

func openPool(
	ctx context.Context,
	databaseURL string,
	maxConnections int32,
	connectTimeout time.Duration,
) (*pgxpool.Pool, error) {
	if maxConnections <= 0 {
		return nil, errors.New("open PostgreSQL pool: maximum connections must be positive")
	}
	if connectTimeout <= 0 {
		return nil, errors.New("open PostgreSQL pool: connect timeout must be positive")
	}

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
	return pool, nil
}

// CheckReadAccess verifies that the existing Product table can be selected. An empty table is
// healthy, and no catalog definition is decoded by this check.
func CheckReadAccess(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("check PostgreSQL read access: pool is required")
	}
	var rowExists bool
	if err := pool.QueryRow(
		ctx,
		"SELECT EXISTS (SELECT 1 FROM public.product)",
	).Scan(&rowExists); err != nil {
		return classifyContext("verify Product read access", ctx, err)
	}
	return nil
}

// NewReader constructs the Product reader over the process-owned pool.
func NewReader(
	pool *pgxpool.Pool,
	acquireTimeout time.Duration,
	queryTimeout time.Duration,
) (*Reader, error) {
	if acquireTimeout <= 0 {
		return nil, errors.New("create PostgreSQL reader: acquire timeout must be positive")
	}
	if queryTimeout <= 0 {
		return nil, errors.New("create PostgreSQL reader: query timeout must be positive")
	}
	if pool == nil {
		return nil, errors.New("create PostgreSQL reader: pool is required")
	}
	return &Reader{
		pool:           pool,
		acquireTimeout: acquireTimeout,
		queryTimeout:   queryTimeout,
	}, nil
}
