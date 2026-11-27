//go:build integration

package administration_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/postgresfixture"
)

const (
	identityAllocators = "accounting_entry.id=ALWAYS,policy_account.id=ALWAYS," +
		"statement_receipt.id=ALWAYS;" +
		"sequences=accounting_entry_id_seq,policy_account_id_seq,statement_receipt_id_seq"
	emptyBusinessTables = "accounts=0;receipts=0;entries=0"
)

func TestRun_PaymentMigrationFreshTarget(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	before := snapshot(t, ctx, fixture)

	// when
	fixture.Call(t, ctx, "status", "uninitialized")

	// then
	if snapshot(t, ctx, fixture) != before {
		t.Fatal("status changed the fresh schema or initialized migration history")
	}

	t.Run("mismatched enrollment", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.WriteTarget(t, postgresfixture.InstanceID) })
		fixture.WriteTarget(t, postgresfixture.ClusterUID)

		refuse(t, ctx, fixture, "migrate", "target identity or marker permissions differ")
	})
	t.Run("partial schema", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.Exec(t, ctx, dropPartialSchemaQuery) })
		fixture.Exec(t, ctx, setupPartialSchemaQuery)

		refuse(t, ctx, fixture, "status", "partial Payment schema")
		refuse(t, ctx, fixture, "migrate", "partial Payment schema")
	})

	// when two operators race on the fresh target
	var group sync.WaitGroup
	results := make([]string, 2)
	failures := make([]error, 2)
	for index := range results {
		group.Go(func() {
			results[index], failures[index] = fixture.Run(ctx, "migrate")
		})
	}
	group.Wait()

	// then
	for index := range results {
		if failures[index] != nil || results[index] != "version=1" {
			t.Fatalf(
				"concurrent migrate %d: got %q (%v)",
				index,
				results[index],
				failures[index],
			)
		}
	}
	fixture.Call(t, ctx, "status", "version=1")
	if got := fixture.Query(t, ctx, readAllocatorCatalogQuery); got != identityAllocators {
		t.Fatalf("fresh allocators: got %q", got)
	}
	history := fixture.Query(t, ctx, readMigrationHistoryQuery)
	if !strings.HasPrefix(history, "1:") || strings.Contains(history, ",") {
		t.Fatalf("expected one applied history row, got %q", history)
	}
	if got := fixture.Query(t, ctx, readBusinessCountsQuery); got != emptyBusinessTables {
		t.Fatalf("fresh business tables: got %q", got)
	}
	migrated := snapshot(t, ctx, fixture)

	t.Run("retry keeps one history", func(t *testing.T) {
		// when
		fixture.Call(t, ctx, "migrate", "version=1")

		// then
		if snapshot(t, ctx, fixture) != migrated {
			t.Fatal("replay changed the schema, migration history or business rows")
		}
	})

	t.Run("invalid history", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.Exec(t, ctx, deleteUnknownHistoryQuery) })
		fixture.Exec(t, ctx, insertUnknownHistoryQuery)

		refuse(t, ctx, fixture, "status", "inconsistent migration history")
		refuse(t, ctx, fixture, "migrate", "inconsistent migration history")
	})

	t.Run("schema drift", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.Exec(t, ctx, restoreEntryForeignKeyActionQuery) })
		fixture.Exec(t, ctx, changeEntryForeignKeyActionQuery)

		refuse(t, ctx, fixture, "status", "incompatible Payment schema or owner")
		refuse(t, ctx, fixture, "migrate", "incompatible Payment schema or owner")
	})
}

// refuse requires a redacted refusal that leaves schema, history and business rows unchanged.
func refuse(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	operation, reason string,
) {
	t.Helper()
	before := snapshot(t, ctx, fixture)
	got, err := fixture.Run(ctx, operation)
	if err == nil || got != "" || !strings.Contains(err.Error(), reason) ||
		strings.Contains(err.Error(), fixture.MigratorPassword) {
		t.Fatalf(
			"%s: expected redacted refusal %q, got %q (%v)",
			operation,
			reason,
			got,
			err,
		)
	}
	if snapshot(t, ctx, fixture) != before {
		t.Fatalf("refused %s changed the catalog, history or data", operation)
	}
}

func snapshot(t *testing.T, ctx context.Context, fixture *postgresfixture.Fixture) string {
	t.Helper()
	state := fixture.Query(t, ctx, readCatalogSnapshotQuery)
	history, rows := "", ""
	if strings.Contains(state, "payment_schema_version:r") {
		history = fixture.Query(t, ctx, readMigrationHistoryQuery)
	}
	if strings.Contains(state, "accounting_entry:r") &&
		strings.Contains(state, "statement_receipt:r") {
		rows = fixture.Query(t, ctx, readBusinessCountsQuery)
	}
	return state + "|rows=" + rows + "|history=" + history
}
