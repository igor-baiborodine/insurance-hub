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

func TestRun_PolicyWriterAccess(t *testing.T) {
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
	writer := writerConnection(t, ctx, fixture.WriterURL("policy"))

	// then
	if !accessMatches(t, ctx, fixture) {
		t.Fatal("migrated access differs from the contract")
	}
	var ids string
	if err := writer.QueryRow(ctx, postgresfixture.InsertGeneratedAggregate).
		Scan(&ids); err != nil ||
		ids != "1,1,1,1:t" {
		t.Fatalf("writer aggregate insert: got %q (%v)", ids, err)
	}
	var intent int64
	if err := writer.QueryRow(ctx, insertWriterOutboxIntentQuery).Scan(&intent); err != nil ||
		intent != 1 {
		t.Fatalf("writer outbox insert: got %d (%v)", intent, err)
	}
	if tag, err := writer.Exec(
		ctx,
		updateWriterOfferQuery,
	); err != nil ||
		tag.RowsAffected() != 1 {
		t.Fatalf("writer offer update: %v", err)
	}

	for _, test := range []struct{ name, statement string }{
		{"create table", "CREATE TABLE public.denied_policy (id integer)"},
		{"alter table", "ALTER TABLE public.policy ADD COLUMN denied integer"},
		{"delete rows", "DELETE FROM public.cover"},
		{"truncate outbox", "TRUNCATE public.policy_outbox"},
		{"read migration history", "SELECT count(*) FROM public.policy_schema_version"},
		{"change enrollment", "UPDATE policy_admin.target_identity SET environment = 'qa'"},
		{"assume owner", "SET ROLE policy"},
		{"reset identity allocator", "SELECT setval('public.offer_id_seq', 1, false)"},
	} {
		t.Run("denies "+test.name, func(t *testing.T) {
			// when
			_, err := writer.Exec(ctx, test.statement)

			// then
			requirePermissionDenied(t, err)
		})
	}
	t.Run("denies another service database", func(t *testing.T) {
		// when
		connection, err := pgx.Connect(ctx, fixture.WriterURL("pricing"))

		// then
		if connection != nil {
			_ = connection.Close(ctx)
		}
		requirePermissionDenied(t, err)
	})
	t.Run("verification rejects extra privilege", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.Exec(t, ctx, revokeWriterDeleteQuery) })
		fixture.Exec(t, ctx, grantWriterDeleteQuery)

		// when / then
		if accessMatches(t, ctx, fixture) {
			t.Fatal("writer DELETE grant passed access verification")
		}
	})
	t.Run("verification rejects writer owner membership", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.Exec(t, ctx, revokeWriterOwnerQuery) })
		fixture.Exec(t, ctx, grantWriterOwnerQuery)

		// when / then
		if accessMatches(t, ctx, fixture) {
			t.Fatal("writer owner membership passed access verification")
		}
	})
	t.Run("verification rejects writer sequence control", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.Exec(t, ctx, revokeWriterSequenceUpdateQuery) })
		fixture.Exec(t, ctx, grantWriterSequenceUpdateQuery)

		// when / then
		if accessMatches(t, ctx, fixture) {
			t.Fatal("writer sequence UPDATE grant passed access verification")
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

// writerConnection logs in as the writer role over verified TLS, as the workload does.
func writerConnection(t *testing.T, ctx context.Context, databaseURL string) *pgx.Conn {
	t.Helper()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect as Policy writer:", err)
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
