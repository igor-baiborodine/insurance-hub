package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/config"
)

// OpenPool constructs and verifies the bounded, process-owned reader pool.
func OpenPool(ctx context.Context, settings config.Database) (*pgxpool.Pool, error) {
	configuration, err := pgxpool.ParseConfig(settings.URL.Value())
	if err != nil || configuration.ConnConfig.User != "go_pricing_reader" ||
		configuration.ConnConfig.Database != "pricing" ||
		configuration.ConnConfig.TLSConfig == nil || configuration.ConnConfig.TLSConfig.InsecureSkipVerify ||
		configuration.ConnConfig.TLSConfig.RootCAs == nil || len(configuration.ConnConfig.Fallbacks) != 0 {
		return nil, errors.New("invalid Pricing PostgreSQL reader connection settings")
	}
	if settings.MaxConnections <= 0 || settings.ConnectTimeout <= 0 {
		return nil, errors.New("invalid Pricing PostgreSQL pool limits")
	}
	configuration.MaxConns = settings.MaxConnections
	configuration.MinConns = 0
	configuration.ConnConfig.ConnectTimeout = settings.ConnectTimeout
	configuration.ConnConfig.RuntimeParams = map[string]string{
		"search_path": "pg_catalog", "default_transaction_read_only": "on",
	}
	dialCtx, cancel := context.WithTimeout(ctx, settings.ConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(dialCtx, configuration)
	if err != nil {
		return nil, errors.New("cannot create Pricing PostgreSQL reader pool")
	}
	if err := pool.Ping(dialCtx); err != nil {
		pool.Close()
		return nil, errors.New("cannot connect to Pricing PostgreSQL as reader")
	}
	return pool, nil
}

// CheckReadAccess verifies the effective role and access to every tariff relation without writing.
func CheckReadAccess(
	ctx context.Context,
	pool *pgxpool.Pool,
	acquireTimeout, queryTimeout time.Duration,
) error {
	acquireCtx, cancelAcquire := context.WithTimeout(ctx, acquireTimeout)
	connection, err := pool.Acquire(acquireCtx)
	cancelAcquire()
	if err != nil {
		return errors.New("cannot acquire Pricing PostgreSQL reader connection")
	}
	defer connection.Release()

	queryCtx, cancelQuery := context.WithTimeout(ctx, queryTimeout)
	defer cancelQuery()
	var accepted bool
	if err := connection.QueryRow(queryCtx, inspectReaderAccessQuery).
		Scan(&accepted); err != nil ||
		!accepted {
		return errors.New("unexpected Pricing PostgreSQL reader identity or tariff access")
	}
	return nil
}
