package administration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStatus_UsesReadOnlySnapshotAndPreservesCallerDeadline(t *testing.T) {
	// given
	for _, test := range []struct {
		name, state, diagnostic string
		catalogError            error
	}{
		{name: "uninitialized", state: "uninitialized"},
		{
			name:         "catalog failure",
			diagnostic:   "locate Pricing migration history and tariff schema failed",
			catalogError: errors.New("private database detail"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			seedAllowed := true
			target := Target{SeedAllowed: &seedAllowed}
			connection := &transactionTestConnection{
				query: func(query string) ([]driver.Value, error) {
					switch query {
					case inspectPlatformQuery, inspectPlatformMarkerQuery:
						return []driver.Value{true}, nil
					case inspectSchemaObjectsQuery:
						return []driver.Value{
							false,
							false,
						}, test.catalogError
					}
					return nil, errors.New("unexpected status query")
				},
			}
			db := openTransactionTestDB(t, connection)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			deadline, _ := ctx.Deadline()

			// when
			got, err := Status(ctx, db, target)

			// then
			if got != test.state {
				t.Errorf("status = %q, want %q", got, test.state)
			}
			if test.diagnostic == "" {
				if err != nil {
					t.Errorf("unexpected status error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.diagnostic) ||
				strings.Contains(err.Error(), test.catalogError.Error()) {
				t.Errorf("status error = %v, want redacted catalog failure", err)
			}
			expected := driver.TxOptions{
				Isolation: driver.IsolationLevel(
					sql.LevelRepeatableRead,
				),
				ReadOnly: true,
			}
			if connection.options != expected || !connection.deadline.Equal(deadline) {
				t.Errorf(
					"status used transaction options %+v and deadline %v",
					connection.options,
					connection.deadline,
				)
			}
			if len(connection.executed) != 0 || connection.begins != 1 ||
				connection.commits != 0 || connection.rollbacks != 1 {
				t.Errorf(
					"status executed %d statements with %d begins, %d commits, %d rollbacks",
					len(
						connection.executed,
					),
					connection.begins,
					connection.commits,
					connection.rollbacks,
				)
			}
		})
	}
}
