package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/administration"
)

type (
	trackedConnector  struct{ connection *trackedConnection }
	trackedDriver     struct{}
	trackedConnection struct{ closed bool }
)

func (c trackedConnector) Connect(context.Context) (driver.Conn, error) { return c.connection, nil }
func (trackedConnector) Driver() driver.Driver                          { return trackedDriver{} }

func (trackedDriver) Open(
	string,
) (driver.Conn, error) {
	return nil, errors.New("connector required")
}

func (*trackedConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("synthetic database failure")
}

func (*trackedConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("synthetic database failure")
}
func (c *trackedConnection) Close() error { c.closed = true; return nil }

func TestRunMigration_BoundsOpeningAndClosesDatabaseOnFailure(t *testing.T) {
	for _, operation := range []string{MigrationOperationMigrate, MigrationOperationStatus, MigrationOperationVersion, "unsupported"} {
		t.Run(operation, func(t *testing.T) {
			// given
			connection := &trackedConnection{}
			var remaining time.Duration
			open := func(ctx context.Context, _ string) (*sql.DB, error) {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("database opening has no deadline")
				}
				remaining = time.Until(deadline)
				db := sql.OpenDB(trackedConnector{connection})
				if err := db.PingContext(ctx); err != nil {
					_ = db.Close()
					return nil, err
				}
				return db, nil
			}
			// when
			_, err := runMigration(
				t.Context(),
				"synthetic",
				administration.Target{},
				operation,
				15*time.Second,
				open,
			)
			// then
			if err == nil || !connection.closed || remaining <= 0 ||
				remaining > 15*time.Second {
				t.Fatalf("database ownership or deadline failed: %v", err)
			}
		})
	}
}

func TestRunMigration_PropagatesOpeningFailure(t *testing.T) {
	// given
	failure := errors.New("synthetic opening failure")
	// when
	_, err := runMigration(
		t.Context(),
		"synthetic",
		administration.Target{},
		MigrationOperationMigrate,
		time.Second,
		func(context.Context, string) (*sql.DB, error) { return nil, failure },
	)
	// then
	if !errors.Is(err, failure) {
		t.Fatalf("opening cause was lost: %v", err)
	}
}

func TestRunMigration_RejectsInvalidConnectionConfiguration(t *testing.T) {
	// when
	_, err := RunMigration(
		t.Context(),
		"not-a-postgres-url",
		administration.Target{},
		MigrationOperationMigrate,
		time.Second,
	)
	// then
	if err == nil {
		t.Fatal("invalid connection was accepted")
	}
}
