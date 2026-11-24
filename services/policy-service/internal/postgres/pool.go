package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolSettings bounds the process-owned runtime pool.
type PoolSettings struct {
	URL            string
	MaxConnections int32
	ConnectTimeout time.Duration
}

// OpenPool constructs and verifies the runtime pool. Only the Policy runtime login over
// certificate-verified TLS is accepted.
func OpenPool(ctx context.Context, settings PoolSettings) (*pgxpool.Pool, error) {
	configuration, err := pgxpool.ParseConfig(settings.URL)
	if err != nil {
		return nil, errInvalidConnection
	}
	connection := configuration.ConnConfig
	if connection.User != "go_policy_runtime" || connection.Database != "policy" ||
		connection.TLSConfig == nil || connection.TLSConfig.InsecureSkipVerify ||
		connection.TLSConfig.RootCAs == nil || len(connection.Fallbacks) != 0 {
		return nil, errors.New("invalid Policy PostgreSQL runtime connection settings")
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
		return nil, errors.New("cannot create Policy PostgreSQL runtime pool")
	}
	if err := pool.Ping(dialCtx); err != nil {
		pool.Close()
		return nil, errors.New("cannot connect to Policy PostgreSQL as runtime")
	}
	return pool, nil
}
