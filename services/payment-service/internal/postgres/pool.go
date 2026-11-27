// Package postgres owns the Payment writer pool and its readiness check.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
)

// WriterRole is the only database identity the serving workload may use.
const WriterRole = "go_payment_writer"

// OpenPool constructs and verifies the writer pool. Only the Payment writer login over
// certificate-verified TLS is accepted.
func OpenPool(ctx context.Context, settings config.Database) (*pgxpool.Pool, error) {
	configuration, err := pgxpool.ParseConfig(settings.URL.Value())
	if err != nil {
		return nil, errors.New("invalid Payment PostgreSQL writer connection settings")
	}
	connection := configuration.ConnConfig
	if connection.User != WriterRole || connection.Database != "payment" ||
		connection.TLSConfig == nil || connection.TLSConfig.InsecureSkipVerify ||
		connection.TLSConfig.RootCAs == nil || len(connection.Fallbacks) != 0 {
		return nil, errors.New("invalid Payment PostgreSQL writer connection settings")
	}
	if settings.MaxConnections <= 0 || settings.ConnectTimeout <= 0 {
		return nil, errors.New("invalid Payment PostgreSQL pool limits")
	}
	configuration.MaxConns = settings.MaxConnections
	configuration.MinConns = 0
	connection.ConnectTimeout = settings.ConnectTimeout
	connection.RuntimeParams = map[string]string{"search_path": "pg_catalog"}
	dialCtx, cancel := context.WithTimeout(ctx, settings.ConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(dialCtx, configuration)
	if err != nil {
		return nil, errors.New("cannot create Payment PostgreSQL writer pool")
	}
	if err := pool.Ping(dialCtx); err != nil {
		pool.Close()
		return nil, errors.New("cannot connect to Payment PostgreSQL as writer")
	}
	return pool, nil
}

// QueryRower is the read capability of a pgx pool or connection.
type QueryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CheckReadiness runs one bounded SELECT 1 through the writer pool without reading tables.
func CheckReadiness(ctx context.Context, database QueryRower, timeout time.Duration) error {
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var result int
	if err := database.QueryRow(queryCtx, "SELECT 1").Scan(&result); err != nil || result != 1 {
		return errors.New("writer connection to Payment PostgreSQL is unavailable")
	}
	return nil
}
