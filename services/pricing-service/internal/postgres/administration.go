package postgres

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const administrationConnectionBudget = 5 * time.Second

// OpenMigrationDatabase connects as the enrolled migrator and assumes the Pricing owner role.
func OpenMigrationDatabase(
	ctx context.Context, databaseURL, host string, ca []byte,
) (*sql.DB, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("selected CA contains no certificates")
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil || config.Host != host || config.Database != "pricing" ||
		config.User != "go_pricing_migrator" || config.Port == 0 || len(config.Fallbacks) != 0 {
		return nil, errors.New("invalid Pricing PostgreSQL connection configuration")
	}
	config.ConnectTimeout = administrationConnectionBudget
	config.TLSConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: host,
		RootCAs:    roots,
	}
	config.RuntimeParams = map[string]string{
		"search_path":                         "pg_catalog",
		"statement_timeout":                   "15s",
		"lock_timeout":                        "5s",
		"idle_in_transaction_session_timeout": "15s",
	}
	db := stdlib.OpenDB(*config, stdlib.OptionAfterConnect(
		func(ctx context.Context, conn *pgx.Conn) error {
			if _, err := conn.Exec(ctx, "SET ROLE pricing"); err != nil {
				return errors.New(
					"cannot assume enrolled Pricing database owner role",
				)
			}
			return nil
		},
	))
	db.SetMaxOpenConns(1)
	dialCtx, cancel := context.WithTimeout(ctx, administrationConnectionBudget)
	defer cancel()
	if err := db.PingContext(dialCtx); err != nil {
		_ = db.Close()
		if dialCtx.Err() != nil {
			return nil, fmt.Errorf(
				"connect to enrolled Pricing database: %w",
				dialCtx.Err(),
			)
		}
		return nil, errors.New("connect to enrolled Pricing database failed")
	}
	return db, nil
}
