//go:build integration

package administration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/administration/provisioning"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/testing/postgresfixture"
)

func TestRun_PolicyRuntimeAccess(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	fixture.Exec(t, ctx, setupUnrelatedServiceQuery)
	fixture.Exec(t, ctx, closeUnrelatedServiceQuery)
	if !accessMatches(t, ctx, fixture) {
		t.Fatal("provisioned access before migration differs from the contract")
	}
	fixture.Call(t, ctx, "migrate", "version=1")

	// when
	runtime := runtimeConnection(t, ctx, fixture.RuntimeURL("policy"))

	// then
	if !accessMatches(t, ctx, fixture) {
		t.Fatal("migrated access differs from the contract")
	}
	var ids string
	if err := runtime.QueryRow(ctx, postgresfixture.InsertGeneratedAggregate).
		Scan(&ids); err != nil ||
		ids != "1,1,1,1:t" {
		t.Fatalf("runtime aggregate insert: got %q (%v)", ids, err)
	}
	var intent int64
	if err := runtime.QueryRow(ctx, insertRuntimeOutboxIntentQuery).Scan(&intent); err != nil ||
		intent != 1 {
		t.Fatalf("runtime outbox insert: got %d (%v)", intent, err)
	}
	if tag, err := runtime.Exec(
		ctx,
		updateRuntimeOfferQuery,
	); err != nil ||
		tag.RowsAffected() != 1 {
		t.Fatalf("runtime offer update: %v", err)
	}

	for _, test := range []struct{ name, statement string }{
		{"create table", "CREATE TABLE public.denied_policy (id integer)"},
		{"alter table", "ALTER TABLE public.policy ADD COLUMN denied integer"},
		{"delete rows", "DELETE FROM public.cover"},
		{"truncate outbox", "TRUNCATE public.policy_outbox"},
		{"read migration history", "SELECT count(*) FROM public.goose_db_version"},
		{"change enrollment", "UPDATE policy_admin.target_identity SET environment = 'qa'"},
		{"assume owner", "SET ROLE policy"},
	} {
		t.Run("denies "+test.name, func(t *testing.T) {
			// when
			_, err := runtime.Exec(ctx, test.statement)

			// then
			requirePermissionDenied(t, err)
		})
	}
	t.Run("denies another service database", func(t *testing.T) {
		// when
		connection, err := pgx.Connect(ctx, fixture.RuntimeURL("pricing"))

		// then
		if connection != nil {
			_ = connection.Close(ctx)
		}
		requirePermissionDenied(t, err)
	})
	t.Run("verification rejects extra privilege", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.Exec(t, ctx, revokeRuntimeDeleteQuery) })
		fixture.Exec(t, ctx, grantRuntimeDeleteQuery)

		// when / then
		if accessMatches(t, ctx, fixture) {
			t.Fatal("runtime DELETE grant passed access verification")
		}
	})
	t.Run("verification rejects runtime owner membership", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.Exec(t, ctx, revokeRuntimeOwnerQuery) })
		fixture.Exec(t, ctx, grantRuntimeOwnerQuery)

		// when / then
		if accessMatches(t, ctx, fixture) {
			t.Fatal("runtime owner membership passed access verification")
		}
	})
}

func accessMatches(t *testing.T, ctx context.Context, fixture *postgresfixture.Fixture) bool {
	t.Helper()
	var matches bool
	if err := fixture.Admin.QueryRow(ctx, provisioning.InspectAccess).
		Scan(&matches); err != nil {
		t.Fatal(err)
	}
	return matches
}

// runtimeConnection logs in as the runtime role over verified TLS, as the workload does.
func runtimeConnection(t *testing.T, ctx context.Context, databaseURL string) *pgx.Conn {
	t.Helper()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect as Policy runtime:", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = connection.Close(ctx)
	})
	return connection
}

func requirePermissionDenied(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("expected SQLSTATE 42501, got %v", err)
	}
}
