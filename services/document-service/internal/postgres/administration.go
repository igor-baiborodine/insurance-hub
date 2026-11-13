package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const administrationConnectionBudget = 5 * time.Second

// OpenMigrationDatabase connects as the validated migration login and takes the Document owner
// role on every pooled connection.
func OpenMigrationDatabase(ctx context.Context, databaseURL string) (*sql.DB, error) {
	return openAdministrationDatabase(ctx, databaseURL)
}

func openAdministrationDatabase(
	ctx context.Context,
	databaseURL string,
) (*sql.DB, error) {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}
	config.ConnectTimeout = administrationConnectionBudget
	statements := []string{assumeOwnerQuery, administrationSettingsQuery}

	db := stdlib.OpenDB(
		*config,
		stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
			for _, statement := range statements {
				if _, err := conn.Exec(ctx, statement); err != nil {
					return err
				}
			}
			return nil
		}),
	)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(0)
	dialCtx, cancel := context.WithTimeout(ctx, administrationConnectionBudget)
	defer cancel()
	if err := db.PingContext(dialCtx); err != nil {
		_ = db.Close()
		if dialCtx.Err() != nil {
			return nil, fmt.Errorf(
				"connect to enrolled Document database: %w",
				dialCtx.Err(),
			)
		}
		return nil, errors.New("connect to enrolled Document database failed")
	}
	return db, nil
}
