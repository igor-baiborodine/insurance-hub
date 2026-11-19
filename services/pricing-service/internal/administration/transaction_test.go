package administration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"
)

func openTransactionTestDB(t *testing.T, connection *transactionTestConnection) *sql.DB {
	t.Helper()
	db := sql.OpenDB(transactionTestConnector{connection})
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type transactionTestConnector struct{ connection *transactionTestConnection }

func (c transactionTestConnector) Connect(context.Context) (driver.Conn, error) {
	return c.connection, nil
}

func (transactionTestConnector) Driver() driver.Driver { return transactionTestDriver{} }

type transactionTestDriver struct{}

func (transactionTestDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("connector required")
}

type transactionTestConnection struct {
	query       func(string) ([]driver.Value, error)
	exec        func(string) error
	commitError error
	options     driver.TxOptions
	deadline    time.Time
	executed    []string
	begins      int
	commits     int
	rollbacks   int
}

func (*transactionTestConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected statement preparation")
}

func (*transactionTestConnection) Close() error { return nil }

func (*transactionTestConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("BeginTx required")
}

func (c *transactionTestConnection) BeginTx(
	ctx context.Context,
	options driver.TxOptions,
) (driver.Tx, error) {
	c.begins++
	c.options = options
	c.deadline, _ = ctx.Deadline()
	return c, nil
}

func (c *transactionTestConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	if c.query == nil {
		return nil, errors.New("unexpected query")
	}
	values, err := c.query(query)
	if err != nil {
		return nil, err
	}
	return &transactionTestRow{values: values}, nil
}

func (c *transactionTestConnection) ExecContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Result, error) {
	c.executed = append(c.executed, query)
	if c.exec == nil {
		return nil, errors.New("unexpected statement")
	}
	if err := c.exec(query); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

func (c *transactionTestConnection) Commit() error {
	c.commits++
	return c.commitError
}

func (c *transactionTestConnection) Rollback() error {
	c.rollbacks++
	return nil
}

type transactionTestRow struct {
	values []driver.Value
	sent   bool
}

func (row *transactionTestRow) Columns() []string { return make([]string, len(row.values)) }
func (*transactionTestRow) Close() error          { return nil }

func (row *transactionTestRow) Next(values []driver.Value) error {
	if row.sent {
		return io.EOF
	}
	row.sent = true
	copy(values, row.values)
	return nil
}
