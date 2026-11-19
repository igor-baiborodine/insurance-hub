package administration

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

func TestMigrate_RollsBackBeforeWritingWhenTargetIsRejected(t *testing.T) {
	// given
	connection := &transactionTestConnection{
		exec: func(query string) error {
			if query == acquireAdministrationLockQuery {
				return nil
			}
			return errors.New("unexpected migration write")
		},
		query: func(query string) ([]driver.Value, error) {
			if query == inspectPlatformQuery {
				return []driver.Value{false}, nil
			}
			return nil, errors.New("unexpected migration query")
		},
	}
	db := openTransactionTestDB(t, connection)

	// when
	err := Migrate(t.Context(), db, Target{})

	// then
	if err == nil || !strings.Contains(err.Error(), "connected role differs from enrollment") {
		t.Fatalf("expected target refusal, got %v", err)
	}
	if len(connection.executed) != 1 ||
		connection.executed[0] != acquireAdministrationLockQuery {
		t.Fatalf(
			"migration executed %d statements, want only the lock",
			len(connection.executed),
		)
	}
	if connection.begins != 1 || connection.commits != 0 || connection.rollbacks != 1 {
		t.Fatalf(
			"migration transactions = %d begins, %d commits, %d rollbacks",
			connection.begins, connection.commits, connection.rollbacks,
		)
	}
}
