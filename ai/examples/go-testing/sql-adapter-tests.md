# Example: SQL Transaction Failure Paths

Read the [adaptation guide](README.md). Use this pattern for a `database/sql` adapter, as in campsite's
`campsite_repository_test.go`. Do not force sqlmock assertions on a GORM adapter merely to match
this example; test the chosen adapter's meaningful boundary.

**Illustrative API:** `postgres.NewPolicyRepository(db).Insert(ctx, *domain.Policy) error` begins
a transaction, inserts `ID` and `Status`, commits on success, rolls back execution failures, and
preserves error causes. The sample SQL is an explicit assumed adapter contract. Adapt it to the
actual schema; do not change production SQL to satisfy an unrelated example.

Suggested location: `internal/postgres/policy_repository_test.go`.

```go
package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/policy/internal/domain"
	"example.com/policy/internal/postgres"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestPolicyRepository_Insert(t *testing.T) {
	const id = "89acf5f5-5590-47b7-bb4a-d208dd4cbb52"
	const insertSQL = "INSERT INTO policies (policy_id, status) VALUES ($1, $2)"
	errBegin := errors.New("begin failed")
	errInsert := errors.New("insert failed")
	errCommit := errors.New("commit failed")
	tests := []struct {
		name  string
		phase string
		cause error
	}{
		{name: "commit success"},
		{name: "begin failure", phase: "begin", cause: errBegin},
		{name: "insert failure rolls back", phase: "insert", cause: errInsert},
		{name: "commit failure is returned", phase: "commit", cause: errCommit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// given
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Errorf("close mock database: %v", err)
				}
				if err := mock.ExpectationsWereMet(); err != nil {
					t.Errorf("unmet SQL expectations: %v", err)
				}
			})
			begin := mock.ExpectBegin()
			if tc.phase == "begin" {
				begin.WillReturnError(tc.cause)
			} else {
				exec := mock.ExpectExec(insertSQL).WithArgs(id, "active")
				if tc.phase == "insert" {
					exec.WillReturnError(tc.cause)
					mock.ExpectRollback()
				} else {
					exec.WillReturnResult(sqlmock.NewResult(0, 1))
					commit := mock.ExpectCommit()
					if tc.phase == "commit" {
						commit.WillReturnError(tc.cause)
					}
				}
			}
			mock.ExpectClose()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			t.Cleanup(cancel)
			repo := postgres.NewPolicyRepository(db)

			// when
			err = repo.Insert(ctx, &domain.Policy{ID: id, Status: "active"})

			// then
			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
				return
			}
			require.NoError(t, err)
		})
	}
}
```

`QueryMatcherEqual` avoids accidental regular-expression matching. Exact arguments and transaction
expectations make failure handling observable; sqlmock documents these facilities in its
[package examples](https://pkg.go.dev/github.com/DATA-DOG/go-sqlmock).
No driver rollback is expected after a commit attempt in this `database/sql` example: a deferred
rollback sees a completed transaction. Do not infer that a failed commit safely persisted nothing.

Extend read-adapter tests with scan failures and `Rows.Err` failures, asserting rows are closed.
Add domain-error mapping cases for known constraints where the adapter owns that behavior. Keep
real constraint, isolation, and atomicity assertions in the [PostgreSQL tests](postgres-integration-tests.md).
Run the owning module's format, lint, and unit-test Make targets after adaptation.
