package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/administration"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/postgres"
)

const (
	MigrationOperationMigrate = "migrate"
	MigrationOperationStatus  = "status"
	MigrationOperationVersion = "version"
)

type openAdministrationDatabase func(context.Context, string) (*sql.DB, error)

// RunMigration constructs and owns the migration database dependency for one operation.
func RunMigration(
	ctx context.Context,
	databaseURL string,
	target administration.Target,
	operation string,
	timeout time.Duration,
) (string, error) {
	return runMigration(
		ctx,
		databaseURL,
		target,
		operation,
		timeout,
		postgres.OpenMigrationDatabase,
	)
}

func runMigration(
	ctx context.Context,
	databaseURL string,
	target administration.Target,
	operation string,
	timeout time.Duration,
	openDatabase openAdministrationDatabase,
) (state string, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	db, err := openDatabase(ctx, databaseURL)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := db.Close(); err != nil {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("close Document migration database: %w", err),
			)
		}
	}()

	switch operation {
	case MigrationOperationMigrate:
		if err := administration.Migrate(ctx, db, target); err != nil {
			return "", err
		}
		return "version=1", nil
	case MigrationOperationStatus:
		return administration.Status(ctx, db, target)
	case MigrationOperationVersion:
		state, err := administration.Status(ctx, db, target)
		if err != nil {
			return "", err
		}
		switch state {
		case "version=1":
			return "1", nil
		case "pending":
			return "0", nil
		default:
			return "uninitialized", nil
		}
	default:
		return "", errors.New("unsupported Document migration operation")
	}
}
