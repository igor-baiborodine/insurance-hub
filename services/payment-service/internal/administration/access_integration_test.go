//go:build integration

package administration_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/administration/provisioning"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/postgresfixture"
)

const (
	missingAccountID = int64(999999)
	statementName    = "bankStatements_2026_8_2.csv"
)

func TestRun_PaymentWriterAccess(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	if !accessMatches(t, ctx, fixture) {
		t.Fatal("provisioned access before migration differs from the contract")
	}
	fixture.Call(t, ctx, "migrate", "version=1")
	fixture.Exec(t, ctx, grantWriterReceiptUpdateQuery)
	if accessMatches(t, ctx, fixture) {
		t.Fatal("former table-wide receipt UPDATE grant passed access verification")
	}
	fixture.Call(t, ctx, "migrate", "version=1")

	// when
	writer := writerConnection(t, ctx, fixture.WriterURL("payment"))
	var account int64
	err := writer.QueryRow(ctx, insertWriterRegistrationQuery,
		"SYN-POLICY-490", "acct-490", "120.00", "2026-01-01").Scan(&account)
	// then
	if err != nil {
		t.Fatal("writer registration:", err)
	}
	if !accessMatches(t, ctx, fixture) {
		t.Fatal("migrated access differs from the contract")
	}

	t.Run("registration identities are unique independently", func(t *testing.T) {
		for _, test := range []struct{ policy, number, constraint string }{
			{"SYN-POLICY-490", "acct-491", "policy_account_policy_number_key"},
			{"SYN-POLICY-491", "acct-490", "policy_account_policy_account_number_key"},
		} {
			// when
			_, err := writer.Exec(ctx, insertWriterRegistrationQuery,
				test.policy, test.number, "120.00", "2026-01-01")

			// then
			requireViolation(t, err, "23505", test.constraint)
		}
	})
	t.Run("registration replay facts are required", func(t *testing.T) {
		// when
		_, missingPremium := writer.Exec(ctx, insertWriterAccountWithoutPremiumQuery)
		_, missingCover := writer.Exec(ctx, insertWriterAccountWithoutCoverQuery)

		// then
		requireNotNullColumn(t, missingPremium, "registered_premium")
		requireNotNullColumn(t, missingCover, "registered_cover_from")
	})
	t.Run("entries need an account and a supported kind", func(t *testing.T) {
		// when
		_, orphan := writer.Exec(
			ctx,
			insertWriterEntryQuery,
			missingAccountID,
			"in_payment",
			nil,
		)
		_, unsupported := writer.Exec(
			ctx,
			insertWriterEntryQuery,
			account,
			"inpayment",
			nil,
		)

		// then
		requireViolation(t, orphan, "23503", "accounting_entry_policy_account_id_fkey")
		requireViolation(t, unsupported, "23514", "accounting_entry_kind_check")
	})
	t.Run("replay distinguishes a changed registration", func(t *testing.T) {
		for _, test := range []struct {
			premium, coverFrom string
			same               bool
		}{
			{"120.00", "2026-01-01", true},
			{"120.01", "2026-01-01", false},
			{"120.00", "2026-01-02", false},
		} {
			// when
			var same bool
			err := writer.QueryRow(ctx, readWriterRegistrationMatchQuery,
				"SYN-POLICY-490", test.premium, test.coverFrom).Scan(&same)

			// then
			if err != nil || same != test.same {
				t.Fatalf(
					"replay %s on %s: got %t (%v)",
					test.premium,
					test.coverFrom,
					same,
					err,
				)
			}
		}
	})
	t.Run("statement receipt identity and states", func(t *testing.T) {
		// given
		digest, changed := strings.Repeat("a", 64), strings.Repeat("b", 64)
		var receipt int64
		if err := writer.QueryRow(ctx, insertWriterReceiptQuery,
			statementName, digest, "review").Scan(&receipt); err != nil {
			t.Fatal("writer receipt insert:", err)
		}

		// when
		_, duplicate := writer.Exec(
			ctx,
			insertWriterReceiptQuery,
			statementName,
			digest,
			"review",
		)
		_, changedErr := writer.Exec(
			ctx,
			insertWriterReceiptQuery,
			statementName,
			changed,
			"review",
		)
		_, badDigest := writer.Exec(
			ctx,
			insertWriterReceiptQuery,
			"other.csv",
			"ABC",
			"review",
		)
		_, badState := writer.Exec(
			ctx,
			insertWriterReceiptQuery,
			"other.csv",
			digest,
			"done",
		)

		// then
		requireViolation(t, duplicate, "23505", "statement_receipt_source_name_sha256_key")
		if changedErr != nil {
			t.Fatal("changed content under the same name:", changedErr)
		}
		requireReceiptFacts(t, ctx, writer, statementName, "receipts=2;digests=2;credits=0")
		requireViolation(t, badDigest, "23514", "statement_receipt_sha256_check")
		requireViolation(t, badState, "23514", "statement_receipt_state_check")
		for _, state := range []string{"posted", "archived"} {
			if tag, err := writer.Exec(
				ctx,
				updateWriterReceiptStateQuery,
				receipt,
				state,
			); err != nil ||
				tag.RowsAffected() != 1 {
				t.Fatalf("receipt state %s: %v", state, err)
			}
		}
	})
	t.Run("posted receipt commits with two equal credits", func(t *testing.T) {
		// when
		err := pgx.BeginFunc(ctx, writer, func(tx pgx.Tx) error {
			return postStatement(
				ctx,
				tx,
				"bankStatements_2026_8_3.csv",
				account,
				account,
			)
		})
		// then
		if err != nil {
			t.Fatal("writer posting:", err)
		}
		requireReceiptFacts(t, ctx, writer, "bankStatements_2026_8_3.csv",
			"receipts=1;digests=1;credits=2")
		var balance string
		if err := writer.QueryRow(ctx, readWriterBalanceQuery, "acct-490", "2026-08-01").
			Scan(&balance); err != nil || balance != "-99.58" {
			t.Fatalf(
				"balance after two 10.21 credits on a 120.00 debit: got %q (%v)",
				balance,
				err,
			)
		}
	})
	t.Run("failed posting keeps neither receipt nor credits", func(t *testing.T) {
		// when
		err := pgx.BeginFunc(ctx, writer, func(tx pgx.Tx) error {
			return postStatement(
				ctx,
				tx,
				"bankStatements_2026_8_4.csv",
				account,
				missingAccountID,
			)
		})

		// then
		requireViolation(t, err, "23503", "accounting_entry_policy_account_id_fkey")
		requireReceiptFacts(t, ctx, writer, "bankStatements_2026_8_4.csv",
			"receipts=0;digests=0;credits=0")
	})

	rows := fixture.Query(t, ctx, readBusinessCountsQuery)
	for _, test := range []struct{ name, statement string }{
		{"create table", "CREATE TABLE public.denied_payment (id integer)"},
		{"alter table", "ALTER TABLE public.policy_account ADD COLUMN denied integer"},
		{"update account", "UPDATE public.policy_account SET updated = updated"},
		{"update entry", "UPDATE public.accounting_entry SET amount = 0"},
		{"change receipt source name", "UPDATE public.statement_receipt SET source_name = source_name"},
		{"change receipt digest", "UPDATE public.statement_receipt SET sha256 = sha256"},
		{"delete entries", "DELETE FROM public.accounting_entry"},
		{"delete receipts", "DELETE FROM public.statement_receipt"},
		{"truncate entries", "TRUNCATE public.accounting_entry"},
		{"read migration history", "SELECT count(*) FROM public.payment_schema_version"},
		{"write migration history", "INSERT INTO public.payment_schema_version VALUES (2)"},
		{"change enrollment", "UPDATE payment_admin.target_identity SET environment = 'qa'"},
		{"assume owner", "SET ROLE payment"},
		{"reset identity allocator", "SELECT setval('public.accounting_entry_id_seq', 1, false)"},
	} {
		t.Run("denies "+test.name, func(t *testing.T) {
			// when
			_, err := writer.Exec(ctx, test.statement)

			// then
			requireViolation(t, err, "42501", "")
		})
	}
	if got := fixture.Query(t, ctx, readBusinessCountsQuery); got != rows {
		t.Fatalf("denied writer statements changed rows: %q, then %q", rows, got)
	}
	t.Run("verification rejects extra privilege", func(t *testing.T) {
		// given
		t.Cleanup(func() { fixture.Exec(t, ctx, revokeWriterDeleteQuery) })
		fixture.Exec(t, ctx, grantWriterDeleteQuery)

		// when / then
		if accessMatches(t, ctx, fixture) {
			t.Fatal("writer DELETE grant passed access verification")
		}
	})
}

// postStatement records one posted receipt and a credit for each account in the caller's
// transaction, as statement posting does.
func postStatement(ctx context.Context, tx pgx.Tx, name string, accounts ...int64) error {
	var receipt int64
	if err := tx.QueryRow(ctx, insertWriterReceiptQuery,
		name, strings.Repeat("c", 64), "posted").Scan(&receipt); err != nil {
		return err
	}
	for _, account := range accounts {
		if _, err := tx.Exec(
			ctx,
			insertWriterEntryQuery,
			account,
			"in_payment",
			receipt,
		); err != nil {
			return err
		}
	}
	return nil
}

func requireReceiptFacts(t *testing.T, ctx context.Context, writer *pgx.Conn, name, want string) {
	t.Helper()
	var facts string
	if err := writer.QueryRow(ctx, readWriterReceiptFactsQuery, name).
		Scan(&facts); err != nil || facts != want {
		t.Fatalf("receipt facts for %s: want %q, got %q (%v)", name, want, facts, err)
	}
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
		t.Fatal("connect as Payment writer:", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = connection.Close(ctx)
	})
	return connection
}

// requireViolation requires the SQLSTATE and, when given, the violated constraint.
func requireViolation(t *testing.T, err error, code, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code ||
		(constraint != "" && pgErr.ConstraintName != constraint) {
		t.Fatalf("expected SQLSTATE %s %s, got %v", code, constraint, err)
	}
}

func requireNotNullColumn(t *testing.T, err error, column string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23502" || pgErr.ColumnName != column {
		t.Fatalf("expected NOT NULL violation on %s, got %v", column, err)
	}
}
