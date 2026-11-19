package service

import (
	"context"
	"errors"
	"strings"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/administration"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/postgres"
)

const (
	MigrationOperationMigrate = "migrate"
	MigrationOperationStatus  = "status"
	MigrationOperationVersion = "version"
)

// RunMigration owns the validated database connection for one administration operation.
func RunMigration(
	ctx context.Context,
	operation, environment, contextName, targetPath, urlPath, caPath string,
) (state string, resultErr error) {
	switch operation {
	case MigrationOperationMigrate, MigrationOperationStatus, MigrationOperationVersion:
	default:
		return "", errors.New("operation must be migrate, status or version")
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
				errors.New("close Pricing migration database failed"),
			)
		}
	}()

	if operation == MigrationOperationMigrate {
		if err := administration.Migrate(ctx, db, target); err != nil {
			return "", err
		}
		return administration.AppliedStatus, nil
	}
	status, err := administration.Status(ctx, db, target)
	if err != nil {
		return "", err
	}
	if operation == MigrationOperationVersion {
		if version, applied := strings.CutPrefix(status, "version="); applied {
			return version, nil
		}
		return "uninitialized", nil
	}
	return status, nil
}

// RunSeed opens and closes the connection for the enrolled seeder operation.
func RunSeed(
	ctx context.Context, environment, contextName, targetPath, urlPath, caPath string,
) (result administration.SeedResult, resultErr error) {
	target, err := administration.ReadTarget(targetPath, environment, contextName)
	if err != nil {
		return result, err
	}
	databaseURL, ca, err := target.SeedConnection(urlPath, caPath)
	if err != nil {
		return result, err
	}
	db, err := postgres.OpenSeedDatabase(ctx, databaseURL, target.Host, ca)
	if err != nil {
		return result, err
	}
	defer func() {
		if err := db.Close(); err != nil {
			resultErr = errors.Join(
				resultErr,
				errors.New("close Pricing seed database failed"),
			)
		}
	}()
	return administration.Seed(ctx, db, target)
}
