package service

import (
	"context"
	"errors"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/administration"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/postgres"
)

const (
	MigrationOperationMigrate = "migrate"
	MigrationOperationStatus  = "status"
)

// RunMigration owns the validated database connection for one administration operation.
func RunMigration(
	ctx context.Context,
	operation, environment, contextName, targetPath, urlPath, caPath string,
) (state string, resultErr error) {
	if operation != MigrationOperationMigrate && operation != MigrationOperationStatus {
		return "", errors.New("operation must be migrate or status")
	}
	target, err := administration.ReadTarget(targetPath, environment, contextName)
	if err != nil {
		return "", err
	}
	databaseURL, ca, err := target.MigrationConnection(urlPath, caPath)
	if err != nil {
		return "", err
	}
	db, err := postgres.OpenMigrationDatabase(ctx, databaseURL, target.Host, ca)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := db.Close(); err != nil {
			resultErr = errors.Join(
				resultErr,
				errors.New("close Payment migration database failed"),
			)
		}
	}()
	if operation == MigrationOperationMigrate {
		if err := administration.Migrate(ctx, db, target); err != nil {
			return "", err
		}
		return administration.AppliedStatus, nil
	}
	return administration.Status(ctx, db, target)
}
