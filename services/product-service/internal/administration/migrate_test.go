package administration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

var errSyntheticQuery = errors.New("synthetic catalog failure")

type failingConnector struct{ connection *failingConnection }

func (c failingConnector) Connect(context.Context) (driver.Conn, error) { return c.connection, nil }
func (failingConnector) Driver() driver.Driver                          { return failingDriver{} }

type failingDriver struct{}

func (failingDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type failingConnection struct {
	readOnly   bool
	isolation  driver.IsolationLevel
	rolledBack bool
	lock       bool
	unlocked   bool
	emptyState bool
	committed  bool
	deadline   time.Time
}

func (*failingConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not expected")
}
func (*failingConnection) Close() error              { return nil }
func (*failingConnection) Begin() (driver.Tx, error) { return nil, errors.New("BeginTx required") }

func (c *failingConnection) BeginTx(
	ctx context.Context,
	options driver.TxOptions,
) (driver.Tx, error) {
	c.readOnly = options.ReadOnly
	c.isolation = options.Isolation
	c.deadline, _ = ctx.Deadline()
	return failingTransaction{connection: c}, nil
}

func TestStatus_PreservesCallerDeadlineBeyondPreviousFixedBudget(t *testing.T) {
	// given
	connection := &failingConnection{emptyState: true}
	db := sql.OpenDB(failingConnector{connection: connection})
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	want, _ := ctx.Deadline()

	// when
	_, err := Status(ctx, db, Target{})
	// then
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if connection.deadline != want {
		t.Fatalf(
			"status replaced caller deadline: got %v, want %v",
			connection.deadline,
			want,
		)
	}
}

func (c *failingConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	if c.lock && strings.Contains(query, "pg_try_advisory_lock") {
		return &syntheticRows{values: []driver.Value{true}}, nil
	}
	if c.lock && strings.Contains(query, "pg_advisory_unlock") {
		c.unlocked = true
		return &syntheticRows{values: []driver.Value{true}}, nil
	}
	if c.emptyState {
		if query == inspectSchemaObjectsQuery {
			return &syntheticRows{values: []driver.Value{false, false}}, nil
		}
		return &syntheticRows{values: []driver.Value{true}}, nil
	}
	return nil, errSyntheticQuery
}

type syntheticRows struct {
	values []driver.Value
	sent   bool
}

func (rows *syntheticRows) Columns() []string {
	columns := make([]string, len(rows.values))
	for index := range columns {
		columns[index] = "value"
	}
	return columns
}
func (*syntheticRows) Close() error { return nil }
func (rows *syntheticRows) Next(values []driver.Value) error {
	if rows.sent {
		return io.EOF
	}
	rows.sent = true
	copy(values, rows.values)
	return nil
}

type failingTransaction struct{ connection *failingConnection }

func (tx failingTransaction) Commit() error {
	if !tx.connection.emptyState {
		return errors.New("unexpected commit")
	}
	tx.connection.committed = true
	return nil
}

func (tx failingTransaction) Rollback() error {
	tx.connection.rolledBack = true
	return nil
}

func TestStatus_RollsBackReadOnlyInspectionOnCatalogError(t *testing.T) {
	// given
	connection := &failingConnection{}
	db := sql.OpenDB(failingConnector{connection: connection})
	defer func() { _ = db.Close() }()

	// when
	state, err := Status(context.Background(), db, Target{})

	// then
	if state != "" || !errors.Is(err, errSyntheticQuery) {
		t.Fatalf("catalog failure was lost: state=%q, error=%v", state, err)
	}
	if !connection.readOnly ||
		connection.isolation != driver.IsolationLevel(sql.LevelRepeatableRead) ||
		!connection.rolledBack {
		t.Fatal(
			"inspection did not use and clean up a repeatable-read, read-only transaction",
		)
	}
}

func TestStatus_ReportsUninitializedWithoutWrite(t *testing.T) {
	// given
	connection := &failingConnection{emptyState: true}
	db := sql.OpenDB(failingConnector{connection: connection})
	defer func() { _ = db.Close() }()

	// when
	state, err := Status(context.Background(), db, Target{})

	// then
	if err != nil || state != "uninitialized" || !connection.readOnly ||
		!connection.committed || connection.rolledBack {
		t.Fatalf("read-only empty inspection failed: state=%q, error=%v", state, err)
	}
}

func TestMigration_FailsClosedWithoutWriting(t *testing.T) {
	// given
	connection := &failingConnection{}
	db := sql.OpenDB(failingConnector{connection: connection})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// when
	err := Migrate(context.Background(), db, Target{})

	// then
	if err == nil {
		t.Fatal("closed database was accepted")
	}
	if connection.rolledBack {
		t.Fatal("migration began a transaction on a closed database")
	}
}

func TestMigration_ReleasesLockWhenInspectionFails(t *testing.T) {
	// given
	connection := &failingConnection{lock: true}
	db := sql.OpenDB(failingConnector{connection: connection})
	defer func() { _ = db.Close() }()

	// when
	err := Migrate(context.Background(), db, Target{})

	// then
	if !errors.Is(err, errSyntheticQuery) || !connection.unlocked {
		t.Fatalf("inspection error or lock release lost: %v", err)
	}
}

func TestIsOnlyAlreadyApplied_RejectsCombinedCleanupFailure(t *testing.T) {
	// given
	for _, scenario := range []struct {
		name string
		err  error
		want bool
	}{
		{"direct sentinel", goose.ErrAlreadyApplied, true},
		{
			"nested wrapper",
			fmt.Errorf("provider apply: %w", fmt.Errorf("version 1: %w", goose.ErrAlreadyApplied)),
			true,
		},
		{
			"joined wrapper",
			errors.Join(fmt.Errorf("version 1: %w", goose.ErrAlreadyApplied)),
			true,
		},
		{
			"cleanup failure",
			errors.Join(
				fmt.Errorf("version 1: %w", goose.ErrAlreadyApplied),
				errors.New("unlock failed"),
			),
			false,
		},
		{
			"nested cleanup failure",
			errors.Join(errors.Join(goose.ErrAlreadyApplied, errors.New("close failed"))),
			false,
		},
		{"different error", errors.New("migration failed"), false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// when
			got := isOnlyAlreadyApplied(scenario.err)

			// then
			if got != scenario.want {
				t.Fatalf("isOnlyAlreadyApplied()=%t, want %t", got, scenario.want)
			}
		})
	}
}
