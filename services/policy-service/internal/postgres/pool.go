package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WriterRole is the only database identity the serving workload may use.
const WriterRole = "go_policy_writer"

// PoolSettings bounds the process-owned writer pool.
type PoolSettings struct {
	URL            string
	MaxConnections int32
	ConnectTimeout time.Duration
}

// OpenPool constructs and verifies the writer pool. Only the Policy writer login over
// certificate-verified TLS is accepted.
func OpenPool(ctx context.Context, settings PoolSettings) (*pgxpool.Pool, error) {
	configuration, err := pgxpool.ParseConfig(settings.URL)
	if err != nil {
		return nil, errInvalidConnection
	}
	connection := configuration.ConnConfig
	if connection.User != WriterRole || connection.Database != "policy" ||
		connection.TLSConfig == nil || connection.TLSConfig.InsecureSkipVerify ||
		connection.TLSConfig.RootCAs == nil || len(connection.Fallbacks) != 0 {
		return nil, errors.New("invalid Policy PostgreSQL writer connection settings")
	}
	if settings.MaxConnections <= 0 || settings.ConnectTimeout <= 0 {
		return nil, errors.New("invalid Policy PostgreSQL pool limits")
	}
	configuration.MaxConns = settings.MaxConnections
	configuration.MinConns = 0
	connection.ConnectTimeout = settings.ConnectTimeout
	connection.RuntimeParams = map[string]string{"search_path": "pg_catalog"}
	dialCtx, cancel := context.WithTimeout(ctx, settings.ConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(dialCtx, configuration)
	if err != nil {
		return nil, errors.New("cannot create Policy PostgreSQL writer pool")
	}
	if err := pool.Ping(dialCtx); err != nil {
		pool.Close()
		return nil, errors.New("cannot connect to Policy PostgreSQL as writer")
	}
	return pool, nil
}

// QueryRower is the read capability of a pgx pool or connection.
type QueryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CheckReadiness verifies the writer identity and bounded access to every required table.
func CheckReadiness(ctx context.Context, database QueryRower, timeout time.Duration) error {
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var ready bool
	if err := database.QueryRow(queryCtx, inspectWriterReadinessQuery, WriterRole).
		Scan(&ready); err != nil || !ready {
		return errors.New(
			"writer identity or table access in Policy PostgreSQL is unavailable",
		)
	}
	return nil
}
