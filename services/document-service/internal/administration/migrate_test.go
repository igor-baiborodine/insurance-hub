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

var errCatalog = errors.New("synthetic catalog failure")

// This package-local fake follows Dashboard's unit-test style. The separate PostgreSQL smoke
// remains the oracle for SQL and actual privileges.
type catalogConnection struct {
	rows       map[string][]driver.Value
	failQuery  string
	readOnly   bool
	isolation  driver.IsolationLevel
	deadline   time.Time
	committed  bool
	rolledBack bool
	unlocked   bool
	writes     int
}

type (
	catalogConnector   struct{ connection *catalogConnection }
	catalogDriver      struct{}
	catalogTransaction struct{ connection *catalogConnection }
	catalogRows        struct {
		values []driver.Value
		sent   bool
	}
)

func (c catalogConnector) Connect(context.Context) (driver.Conn, error) { return c.connection, nil }
func (catalogConnector) Driver() driver.Driver                          { return catalogDriver{} }

func (catalogDriver) Open(
	string,
) (driver.Conn, error) {
	return nil, errors.New("connector required")
}

func (*catalogConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*catalogConnection) Close() error              { return nil }
func (*catalogConnection) Begin() (driver.Tx, error) { return nil, errors.New("BeginTx required") }

func (c *catalogConnection) BeginTx(
	ctx context.Context,
	options driver.TxOptions,
) (driver.Tx, error) {
	c.readOnly, c.isolation = options.ReadOnly, options.Isolation
	c.deadline, _ = ctx.Deadline()
	return catalogTransaction{c}, nil
}

func (c *catalogConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	if query == c.failQuery {
		return nil, errCatalog
	}
	if strings.Contains(query, "pg_try_advisory_lock") {
		return &catalogRows{values: []driver.Value{true}}, nil
	}
	if strings.Contains(query, "pg_advisory_unlock") {
		c.unlocked = true
		return &catalogRows{values: []driver.Value{true}}, nil
	}
	values, ok := c.rows[query]
	if !ok {
		return nil, fmt.Errorf("unexpected catalog query: %w", errCatalog)
	}
	return &catalogRows{values: values}, nil
}

func (c *catalogConnection) ExecContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Result, error) {
	c.writes++
	if query == c.failQuery {
		return nil, errCatalog
	}
	if query != applyReaderGrantsQuery {
		return nil, errors.New("unexpected write")
	}
	return driver.RowsAffected(0), nil
}
func (tx catalogTransaction) Commit() error   { tx.connection.committed = true; return nil }
func (tx catalogTransaction) Rollback() error { tx.connection.rolledBack = true; return nil }
func (r *catalogRows) Columns() []string      { return make([]string, len(r.values)) }
func (*catalogRows) Close() error             { return nil }
func (r *catalogRows) Next(values []driver.Value) error {
	if r.sent {
		return io.EOF
	}
	r.sent = true
	copy(values, r.values)
	return nil
}

func catalogDatabase(t *testing.T, state string) (*sql.DB, *catalogConnection) {
	t.Helper()
	table := state == "unmanaged" || state == "version=1"
	history := state == "pending" || state == "version=1"
	c := &catalogConnection{rows: map[string][]driver.Value{
		inspectPlatformQuery:           {true},
		inspectSchemaObjectsQuery:      {table, table, history, true},
		inspectDocumentSchemaQuery:     {true},
		inspectSequenceQuery:           {true},
		inspectSequenceProgressQuery:   {true},
		inspectHistorySchemaQuery:      {true},
		inspectHistoryRowsQuery:        {state},
		inspectDocumentPresenceQuery:   {table},
		acquireAdministrationLockQuery: {true},
	}}
	db := sql.OpenDB(catalogConnector{c})
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db, c
}

func TestStatus_InspectsSupportedStatesWithoutWrites(t *testing.T) {
	for _, want := range []string{"uninitialized", "unmanaged", "pending", "version=1"} {
		t.Run(want, func(t *testing.T) {
			// given
			db, connection := catalogDatabase(t, want)
			ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
			defer cancel()
			deadline, _ := ctx.Deadline()
			// when
			got, err := Status(ctx, db, Target{})
			// then
			if err != nil || got != want {
				t.Fatalf("state=%q error=%v, want %q", got, err, want)
			}
			if !connection.readOnly ||
				connection.isolation != driver.IsolationLevel(
					sql.LevelRepeatableRead,
				) ||
				connection.deadline != deadline ||
				!connection.committed ||
				connection.rolledBack ||
				connection.writes != 0 {
				t.Fatal(
					"status changed the read-only transaction or caller deadline",
				)
			}
		})
	}
}

func TestStatus_RejectsUnsafeCatalogAndRollsBack(t *testing.T) {
	for _, scenario := range []struct {
		name, query string
		values      []driver.Value
	}{
		{"wrong target", inspectPlatformQuery, []driver.Value{false}},
		{"partial schema", inspectSchemaObjectsQuery, []driver.Value{true, false, true, true}},
		{"wrong document owner", inspectDocumentSchemaQuery, []driver.Value{false}},
		{"unsafe sequence metadata", inspectSequenceQuery, []driver.Value{false}},
		{"unsafe sequence progress", inspectSequenceProgressQuery, []driver.Value{false}},
		{"foreign history shape", inspectHistorySchemaQuery, []driver.Value{false}},
		{"invalid history", inspectHistoryRowsQuery, []driver.Value{"invalid"}},
		{"catalog error", inspectPlatformQuery, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// given
			db, connection := catalogDatabase(t, "version=1")
			connection.rows[scenario.query] = scenario.values
			if scenario.values == nil {
				connection.failQuery = scenario.query
			}
			// when
			state, err := Status(t.Context(), db, Target{})
			// then
			if err == nil || state != "" || !connection.rolledBack ||
				connection.committed ||
				connection.writes != 0 {
				t.Fatalf(
					"unsafe catalog was accepted or transaction leaked: state=%q error=%v",
					state,
					err,
				)
			}
			if scenario.values == nil && !errors.Is(err, errCatalog) {
				t.Fatal("catalog cause was lost")
			}
		})
	}
}

func TestApplyGrants_ReplayCommitsAndFailureRollsBack(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("grant failure=%t", fail), func(t *testing.T) {
			// given
			db, connection := catalogDatabase(t, "version=1")
			if fail {
				connection.failQuery = applyReaderGrantsQuery
			}
			// when
			err := applyGrants(t.Context(), db, Target{})
			// then
			if (err != nil) != fail || connection.committed == fail ||
				connection.rolledBack != fail ||
				connection.writes != 1 {
				t.Fatalf(
					"grant transaction did not commit or roll back correctly: %v",
					err,
				)
			}
			if fail && !errors.Is(err, errCatalog) {
				t.Fatal("grant cause was lost")
			}
		})
	}
}

func TestMigrate_ReleasesSessionLockWhenInspectionFails(t *testing.T) {
	// given
	db, connection := catalogDatabase(t, "uninitialized")
	connection.failQuery = inspectPlatformQuery
	// when
	err := Migrate(t.Context(), db, Target{})
	// then
	if !errors.Is(err, errCatalog) || !connection.unlocked || connection.writes != 0 ||
		!connection.rolledBack {
		t.Fatalf("migration lost the failure, wrote, or retained its session lock: %v", err)
	}
}

func TestIsOnlyAlreadyApplied_PreservesCleanupFailures(t *testing.T) {
	for _, scenario := range []struct {
		name string
		err  error
		want bool
	}{
		{"wrapped already applied", fmt.Errorf("apply: %w", goose.ErrAlreadyApplied), true},
		{"joined already applied", errors.Join(goose.ErrAlreadyApplied), true},
		{"joined cleanup failure", errors.Join(goose.ErrAlreadyApplied, errCatalog), false},
		{"other failure", errCatalog, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// when
			got := isOnlyAlreadyApplied(scenario.err)
			// then
			if got != scenario.want {
				t.Fatalf("got %t, want %t", got, scenario.want)
			}
		})
	}
}
