//go:build integration

package administration_test

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRun_PolicyRuntimeAccess(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := newFixture(t, ctx)
	fixture.exec(t, ctx, setupUnrelatedServiceQuery)
	fixture.exec(t, ctx, closeUnrelatedServiceQuery)
	if !fixture.accessMatches(t, ctx) {
		t.Fatal("provisioned access before migration differs from the contract")
	}
	fixture.call(t, ctx, "migrate", "version=1")

	// when
	runtime := fixture.runtimeConnection(t, ctx, "policy")

	// then
	if !fixture.accessMatches(t, ctx) {
		t.Fatal("migrated access differs from the contract")
	}
	var ids string
	if err := runtime.QueryRow(ctx, insertGeneratedAggregateQuery).Scan(&ids); err != nil ||
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
		connection, err := fixture.connectRuntime(ctx, "pricing")

		// then
		if connection != nil {
			_ = connection.Close(ctx)
		}
		requirePermissionDenied(t, err)
	})
	t.Run("verification rejects extra privilege", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.exec(t, ctx, revokeRuntimeDeleteQuery) })
		fixture.exec(t, ctx, grantRuntimeDeleteQuery)

		// when / then
		if fixture.accessMatches(t, ctx) {
			t.Fatal("runtime DELETE grant passed access verification")
		}
	})
	t.Run("verification rejects runtime owner membership", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.exec(t, ctx, revokeRuntimeOwnerQuery) })
		fixture.exec(t, ctx, grantRuntimeOwnerQuery)

		// when / then
		if fixture.accessMatches(t, ctx) {
			t.Fatal("runtime owner membership passed access verification")
		}
	})
}

func (f *migrationFixture) accessMatches(t *testing.T, ctx context.Context) bool {
	t.Helper()
	var matches bool
	if err := f.admin.QueryRow(ctx, inspectAccessQuery).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	return matches
}

// runtimeConnection logs in as the runtime role over verified TLS, as the workload does.
func (f *migrationFixture) runtimeConnection(
	t *testing.T,
	ctx context.Context,
	database string,
) *pgx.Conn {
	t.Helper()
	connection, err := f.connectRuntime(ctx, database)
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

func (f *migrationFixture) connectRuntime(ctx context.Context, database string) (*pgx.Conn, error) {
	runtimeURL := *f.address
	runtimeURL.Path = "/" + database
	runtimeURL.User = url.UserPassword("go_policy_runtime", f.runtimePassword)
	runtimeURL.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {f.ca}}.Encode()
	return pgx.Connect(ctx, runtimeURL.String())
}

func requirePermissionDenied(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("expected SQLSTATE 42501, got %v", err)
	}
}
