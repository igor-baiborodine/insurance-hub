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
	errAssumeOwner       = errors.New("cannot assume Policy database owner role")
	errInvalidCA         = errors.New("selected CA contains no certificates")
	errInvalidConnection = errors.New("invalid Policy PostgreSQL connection configuration")
)

// OpenMigrationDatabase connects as the enrolled migrator over verified TLS and assumes the
// Policy owner role on every pooled connection.
func OpenMigrationDatabase(
	ctx context.Context, databaseURL, host string, ca []byte,
) (*sql.DB, error) {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errInvalidCA
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil || config.Host != host || config.Database != "policy" ||
		config.User != "go_policy_migrator" || config.Port == 0 || len(config.Fallbacks) != 0 {
		return nil, errInvalidConnection
	}
	if err := configureTunnel(config); err != nil {
		return nil, err
	}
	config.ConnectTimeout = administrationConnectionBudget
	config.TLSConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: host,
		RootCAs:    roots,
	}
	db := stdlib.OpenDB(*config, stdlib.OptionAfterConnect(
		func(ctx context.Context, conn *pgx.Conn) error {
			if _, err := conn.Exec(ctx, assumeOwnerQuery); err != nil {
				return errAssumeOwner
			}
			_, err := conn.Exec(ctx, administrationSettingsQuery)
			return err
		},
	))
	db.SetMaxOpenConns(2)
	dialCtx, cancel := context.WithTimeout(ctx, administrationConnectionBudget)
	defer cancel()
	if err := db.PingContext(dialCtx); err != nil {
		_ = db.Close()
		if errors.Is(err, errAssumeOwner) {
			return nil, errAssumeOwner
		}
		if dialCtx.Err() != nil {
			return nil, fmt.Errorf(
				"connect to enrolled Policy database: %w",
				dialCtx.Err(),
			)
		}
		return nil, errors.New("connect to enrolled Policy database failed")
	}
	return db, nil
}

// configureTunnel resolves only the enrolled host name to a validated loopback address, so TLS
// still verifies the cluster-internal server name.
func configureTunnel(config *pgx.ConnConfig) error {
	hostAddress, tunneled := config.RuntimeParams["hostaddr"]
	if !tunneled {
		return nil
	}
	if hostAddress != "127.0.0.1" {
		return errors.New("invalid PostgreSQL tunnel configuration")
	}
	delete(config.RuntimeParams, "hostaddr")
	enrolledHost := config.Host
	config.LookupFunc = func(_ context.Context, host string) ([]string, error) {
		if host != enrolledHost {
			return nil, errors.New("PostgreSQL tunnel hostname differs from enrollment")
		}
		return []string{hostAddress}, nil
	}
	return nil
}
