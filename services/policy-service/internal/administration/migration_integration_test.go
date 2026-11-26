//go:build integration

package administration_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/testing/postgresfixture"
)

const identityAllocators = "cover.id=ALWAYS,offer.id=ALWAYS,policy.id=ALWAYS," +
	"policy_outbox.id=ALWAYS,policy_version.id=ALWAYS;" +
	"sequences=cover_id_seq,offer_id_seq,policy_id_seq,policy_outbox_id_seq,policy_version_id_seq"

func TestRun_PolicyMigrationFreshTarget(t *testing.T) {
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

		refuse(t, ctx, fixture, "status", "partial Policy schema")
		refuse(t, ctx, fixture, "migrate", "partial Policy schema")
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
	if got := generatedAggregate(t, ctx, fixture, true); got != "1,1,1,1:t" {
		t.Fatalf("fresh writer insert: got %q", got)
	}
	migrated := snapshot(t, ctx, fixture)

	t.Run("retry keeps one history", func(t *testing.T) {
		// when
		fixture.Call(t, ctx, "migrate", "version=1")

		// then
		if snapshot(t, ctx, fixture) != migrated {
			t.Fatal("replay changed the schema or migration history")
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
		t.Cleanup(func() { fixture.Exec(t, ctx, restoreCoverForeignKeyActionQuery) })
		fixture.Exec(t, ctx, changeCoverForeignKeyActionQuery)

		refuse(t, ctx, fixture, "status", "incompatible Policy schema or owner")
		refuse(t, ctx, fixture, "migrate", "incompatible Policy schema or owner")
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
	if strings.Contains(state, "policy_schema_version:r") {
		history = fixture.Query(t, ctx, readMigrationHistoryQuery)
	}
	if strings.Contains(state, "cover:r") && strings.Contains(state, "offer_answers:r") {
		rows = fixture.Query(t, ctx, postgresfixture.ReadRowFingerprints)
	}
	return state + "|rows=" + rows + "|history=" + history
}

// generatedAggregate inserts one aggregate as the writer role and returns its generated IDs.
func generatedAggregate(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	commit bool,
) string {
	t.Helper()
	tx, err := fixture.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE go_policy_writer"); err != nil {
		t.Fatal(err)
	}
	var ids string
	if err := tx.QueryRow(ctx, postgresfixture.InsertGeneratedAggregate).
		Scan(&ids); err != nil {
		t.Fatal("writer insert failed:", err)
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}
