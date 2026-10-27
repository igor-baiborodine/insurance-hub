package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/administration"
)

type trackedConnector struct {
	closed *atomic.Bool
}

func (connector trackedConnector) Connect(context.Context) (driver.Conn, error) {
	return &trackedConnection{closed: connector.closed}, nil
}

func (trackedConnector) Driver() driver.Driver { return trackedDriver{} }

type trackedDriver struct{}

func (trackedDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("tracked driver must be opened through its connector")
}

type trackedConnection struct {
	closed *atomic.Bool
}

func (*trackedConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is unsupported")
}

func (*trackedConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("begin is unsupported")
}

func (connection *trackedConnection) Close() error {
	connection.closed.Store(true)
	return nil
}

func openTrackedDatabase(closed *atomic.Bool) openAdministrationDatabase {
	return func(ctx context.Context, _ string) (*sql.DB, error) {
		db := sql.OpenDB(trackedConnector{closed: closed})
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, err
		}
		return db, nil
	}
}

func TestRunMigration_OwnsDatabaseLifecycle(t *testing.T) {
	var closed atomic.Bool

	_, err := runMigration(
		context.Background(),
		"synthetic",
		administration.Target{},
		"unsupported",
		openTrackedDatabase(&closed),
	)

	if err == nil {
		t.Fatal("unsupported migration operation was accepted")
	}
	if !closed.Load() {
		t.Fatal("migration database was not closed")
	}
}

func TestRunSeed_OwnsDatabaseLifecycle(t *testing.T) {
	var closed atomic.Bool

	_, err := runSeed(
		context.Background(),
		"synthetic",
		administration.Target{},
		"",
		openTrackedDatabase(&closed),
	)

	if err == nil {
		t.Fatal("empty seed catalog path was accepted")
	}
	if !closed.Load() {
		t.Fatal("seed database was not closed")
	}
}

func TestRunMigration_DeadlineIncludesOpening(t *testing.T) {
	// given
	for _, operation := range []string{MigrationOperationMigrate, MigrationOperationStatus, MigrationOperationVersion} {
		t.Run(operation, func(t *testing.T) {
			openingFailure := errors.New("synthetic connection failure")
			var remaining time.Duration
			var hasDeadline bool

			// when
			_, err := runMigration(
				context.Background(),
				"synthetic",
				administration.Target{},
				operation,
				func(ctx context.Context, _ string) (*sql.DB, error) {
					deadline, ok := ctx.Deadline()
					hasDeadline = ok
					remaining = time.Until(deadline)
					return nil, openingFailure
				},
			)

			// then
			if !errors.Is(err, openingFailure) || !hasDeadline || remaining <= 0 ||
				remaining > 120*time.Second {
				t.Fatalf(
					"opening must consume the overall budget: deadline=%t remaining=%s error=%v",
					hasDeadline,
					remaining,
					err,
				)
			}
		})
	}
}
