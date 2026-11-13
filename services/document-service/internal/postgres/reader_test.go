package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
)

type fakeReaderPool struct {
	connection readerConnection
	err        error
}

func (pool fakeReaderPool) Acquire(context.Context) (readerConnection, error) {
	return pool.connection, pool.err
}

type fakeReaderConnection struct {
	rows     pgx.Rows
	queryErr error
	released bool
}

func (connection *fakeReaderConnection) Query(
	context.Context,
	string,
	...any,
) (pgx.Rows, error) {
	return connection.rows, connection.queryErr
}

func (connection *fakeReaderConnection) Release() { connection.released = true }

type fakeRows struct {
	rows    [][]any
	index   int
	scanErr error
	err     error
}

func (rows *fakeRows) Close()                                       {}
func (rows *fakeRows) Err() error                                   { return rows.err }
func (rows *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (rows *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (rows *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (rows *fakeRows) RawValues() [][]byte                          { return nil }
func (rows *fakeRows) Conn() *pgx.Conn                              { return nil }
func (rows *fakeRows) TypeMap() *pgtype.Map                         { return nil }
func (rows *fakeRows) Next() bool {
	if rows.index >= len(rows.rows) {
		return false
	}
	rows.index++
	return true
}

func (rows *fakeRows) Scan(dest ...any) error {
	if rows.scanErr != nil {
		return rows.scanErr
	}
	values := rows.rows[rows.index-1]
	*dest[0].(*string) = values[0].(string)
	*dest[1].(*[]byte) = values[1].([]byte)
	*dest[2].(*int) = values[2].(int)
	*dest[3].(*bool) = values[3].(bool)
	return nil
}

func TestPoolAndReaderRejectInvalidDependencies(t *testing.T) {
	if _, err := OpenPool(context.Background(), config.Database{}); err == nil {
		t.Fatal("invalid pool settings accepted")
	}
	if err := CheckReadAccess(context.Background(), nil); err == nil {
		t.Fatal("missing readiness pool accepted")
	}
	for _, test := range []struct {
		acquire time.Duration
		query   time.Duration
		maximum int
	}{
		{time.Second, time.Second, 1},
		{0, time.Second, 1},
		{time.Second, 0, 1},
		{time.Second, time.Second, 0},
	} {
		if _, err := NewReader(nil, test.acquire, test.query, test.maximum); err == nil {
			t.Fatal("invalid reader settings accepted")
		}
	}
}

func TestReaderErrorClassificationPreservesSafeCauses(t *testing.T) {
	cause := errors.New("private-password")
	failure := classifyContext("read", context.Background(), cause)
	if !errors.Is(failure, application.ErrUnavailable) || !errors.Is(failure, cause) ||
		strings.Contains(failure.Error(), "private-password") {
		t.Fatalf("failure = %v", failure)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if failure = classifyContext(
		"read",
		cancelled,
		cause,
	); !errors.Is(
		failure,
		context.Canceled,
	) {
		t.Fatalf("cancelled failure = %v", failure)
	}
	if !errors.Is(
		classifyContext("read", context.Background(), context.DeadlineExceeded),
		context.DeadlineExceeded,
	) {
		t.Fatal("deadline cause not preserved")
	}
}

func TestReaderFindByPolicyNumberMapsRowsAndFailures(t *testing.T) {
	rows := &fakeRows{rows: [][]any{{"SYN-362", []byte("pdf"), 3, false}}}
	connection := &fakeReaderConnection{rows: rows}
	reader, err := newReader(
		fakeReaderPool{connection: connection},
		time.Second,
		time.Second,
		8,
	)
	if err != nil {
		t.Fatal(err)
	}
	documents, err := reader.FindByPolicyNumber(context.Background(), "SYN-362")
	if err != nil || len(documents) != 1 || string(documents[0].Bytes) != "pdf" ||
		!connection.released {
		t.Fatalf("documents = (%v, %v), released = %t", documents, err, connection.released)
	}

	for _, test := range []struct {
		name string
		pool readerPool
	}{
		{"acquire", fakeReaderPool{err: errors.New("private")}},
		{"query", fakeReaderPool{connection: &fakeReaderConnection{queryErr: errors.New("private")}}},
		{"scan", fakeReaderPool{connection: &fakeReaderConnection{rows: &fakeRows{rows: [][]any{{}}, scanErr: errors.New("private")}}}},
		{"iterate", fakeReaderPool{connection: &fakeReaderConnection{rows: &fakeRows{err: errors.New("private")}}}},
		{"oversize", fakeReaderPool{connection: &fakeReaderConnection{rows: &fakeRows{rows: [][]any{{"SYN-362", []byte(nil), 9, false}}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, err := newReader(test.pool, time.Second, time.Second, 8)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.FindByPolicyNumber(
				context.Background(),
				"SYN-362",
			); err == nil {
				t.Fatal("reader failure accepted")
			}
		})
	}
}
