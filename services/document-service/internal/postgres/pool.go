package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
)

// OpenPool constructs and verifies the bounded process-owned Document reader pool.
func OpenPool(ctx context.Context, settings config.Database) (*pgxpool.Pool, error) {
	if settings.MaxConnections <= 0 || settings.ConnectTimeout <= 0 {
		return nil, errors.New("open PostgreSQL pool: limits must be positive")
	}
	poolConfig, err := pgxpool.ParseConfig(settings.URL.Value())
	if err != nil {
		return nil, unavailable("parse connection settings", err)
	}
	poolConfig.MaxConns = settings.MaxConnections
	poolConfig.MinConns = 0
	poolConfig.ConnConfig.ConnectTimeout = settings.ConnectTimeout
	connectCtx, cancel := context.WithTimeout(ctx, settings.ConnectTimeout)
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

// CheckReadAccess verifies the Document metadata table through the restricted reader identity.
func CheckReadAccess(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("check PostgreSQL read access: pool is required")
	}
	var available bool
	if err := pool.QueryRow(
		ctx,
		"SELECT EXISTS (SELECT 1 FROM public.policy_document)",
	).Scan(&available); err != nil {
		return classifyContext("verify Document read access", ctx, err)
	}
	return nil
}
