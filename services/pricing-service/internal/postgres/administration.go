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

var (
	errAssumeOwner       = errors.New("cannot assume Pricing database owner role")
	errInvalidCA         = errors.New("selected CA contains no certificates")
	errInvalidConnection = errors.New("invalid Pricing PostgreSQL connection configuration")
)

// OpenMigrationDatabase connects as the enrolled migrator and assumes the Pricing owner role.
func OpenMigrationDatabase(
	ctx context.Context, databaseURL, host string, ca []byte,
) (*sql.DB, error) {
	return openAdministrationDatabase(ctx, databaseURL, host, ca, "go_pricing_migrator", true)
}

// OpenSeedDatabase connects as the seeder and keeps that restricted role.
func OpenSeedDatabase(ctx context.Context, databaseURL, host string, ca []byte) (*sql.DB, error) {
	return openAdministrationDatabase(ctx, databaseURL, host, ca, "go_pricing_seeder", false)
}

func openAdministrationDatabase(
	ctx context.Context, databaseURL, host string, ca []byte, role string, assumeOwner bool,
) (*sql.DB, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errInvalidCA
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil || config.Host != host || config.Database != "pricing" ||
		config.User != role || config.Port == 0 || len(config.Fallbacks) != 0 {
		return nil, errInvalidConnection
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
	var options []stdlib.OptionOpenDB
	if assumeOwner {
		options = append(options, stdlib.OptionAfterConnect(
			func(ctx context.Context, conn *pgx.Conn) error {
				if _, err := conn.Exec(ctx, assumeOwnerQuery); err != nil {
					return errAssumeOwner
				}
				return nil
			},
		))
	}
	db := stdlib.OpenDB(*config, options...)
	db.SetMaxOpenConns(1)
	dialCtx, cancel := context.WithTimeout(ctx, administrationConnectionBudget)
	defer cancel()
	if err := db.PingContext(dialCtx); err != nil {
		_ = db.Close()
		if errors.Is(err, errAssumeOwner) {
			return nil, errAssumeOwner
		}
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
