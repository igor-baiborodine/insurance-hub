//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/postgresfixture"
)

const (
	knownAccount     = "231232132131"
	duplicateAccount = "SYN-ACCOUNT-DUPLICATE"
)

func TestRun_PaymentStatementPosting(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	fixture.Call(t, ctx, "migrate", "version=1")
	seedStatementAccounts(t, ctx, fixture)
	pool, err := postgres.OpenPool(ctx, writerSettings(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	postings, err := application.NewStatementPostings(store, func() time.Time {
		return time.Date(2018, time.August, 2, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}

	one := parsedStatement(t, "bankStatements_2018_8_2.csv",
		"1,A,2018-08-01,"+knownAccount+",10.21\r\n")

	// when
	oneReceipt, err := postings.Post(ctx, one)
	// then
	if err != nil || oneReceipt.State != application.StatementPosted {
		t.Fatalf("post known statement = (%+v, %v)", oneReceipt, err)
	}
	assertReceiptFacts(t, ctx, fixture, one, "posted", 1, "10.21")
	assertDatedBalance(t, ctx, store, knownAccount, "0.00", "10.21")

	t.Run("matching replay resumes without another credit", func(t *testing.T) {
		// when
		replayed, err := postings.Post(ctx, one)

		// then
		if err != nil || replayed != oneReceipt {
			t.Fatalf("replay = (%+v, %v), want %+v", replayed, err, oneReceipt)
		}
		assertReceiptFacts(t, ctx, fixture, one, "posted", 1, "10.21")
		assertDatedBalance(t, ctx, store, knownAccount, "0.00", "10.21")
	})

	t.Run("equal rows remain distinct credits", func(t *testing.T) {
		// given
		row := "2,A,2018-08-01," + duplicateAccount + ",10.21\r\n"
		statement := parsedStatement(t, "bankStatements_2018_8_3.csv", row+row)

		// when
		receipt, err := postings.Post(ctx, statement)

		// then
		if err != nil || receipt.State != application.StatementPosted {
			t.Fatalf("post duplicate rows = (%+v, %v)", receipt, err)
		}
		assertReceiptFacts(t, ctx, fixture, statement, "posted", 2, "20.42")
		assertDatedBalance(t, ctx, store, duplicateAccount, "0.00", "20.42")
	})

	t.Run("unknown account holds the complete file for review", func(t *testing.T) {
		// given
		statement := parsedStatement(t, "bankStatements_2018_8_4.csv",
			"3,A,2018-08-01,"+knownAccount+",10.21\r\n"+
				"4,A,2018-08-01,SYN-ACCOUNT-UNKNOWN,99.25\r\n")

		// when
		receipt, err := postings.Post(ctx, statement)

		// then
		if err != nil || receipt.State != application.StatementReview {
			t.Fatalf("post mixed statement = (%+v, %v)", receipt, err)
		}
		assertReceiptFacts(t, ctx, fixture, statement, "review", 0, "0.00")
		assertDatedBalance(t, ctx, store, knownAccount, "0.00", "10.21")
	})

	t.Run("changed bytes under a known name stay in review", func(t *testing.T) {
		// given
		changed := parsedStatement(t, one.Snapshot.SourceName,
			"1,A,2018-08-01,"+knownAccount+",10.22\r\n")

		// when
		receipt, err := postings.Post(ctx, changed)

		// then
		if err != nil || receipt.State != application.StatementReview {
			t.Fatalf("post changed statement = (%+v, %v)", receipt, err)
		}
		assertReceiptFacts(t, ctx, fixture, changed, "review", 0, "0.00")
		assertReceiptFacts(t, ctx, fixture, one, "posted", 1, "10.21")
		assertDatedBalance(t, ctx, store, knownAccount, "0.00", "10.21")
	})

	t.Run("commit failure leaves no receipt or credit", func(t *testing.T) {
		// given
		const source = "bankStatements_2018_8_5.csv"
		statement := parsedStatement(t, source,
			"5,A,2018-08-01,"+knownAccount+",10.21\r\n")
		installCommitFailure(t, ctx, fixture)

		// when
		failedReceipt, err := postings.Post(ctx, statement)
		removeCommitFailure(t, ctx, fixture)

		// then
		if err == nil {
			t.Fatal("statement commit succeeded with the deferred failure installed")
		}
		if failedReceipt != (application.StatementReceipt{}) {
			t.Fatalf("failed commit returned a phantom receipt: %+v", failedReceipt)
		}
		receipts, entries := statementCounts(t, ctx, fixture, source)
		if receipts != 0 || entries != 0 {
			t.Fatalf("failed commit left receipts=%d entries=%d", receipts, entries)
		}
		if receipt, retryErr := postings.Post(ctx, statement); retryErr != nil ||
			receipt.State != application.StatementPosted {
			t.Fatalf("retry after commit failure = (%+v, %v)", receipt, retryErr)
		}
		assertReceiptFacts(t, ctx, fixture, statement, "posted", 1, "10.21")
	})
}

func parsedStatement(t *testing.T, sourceName, rows string) application.BankStatement {
	t.Helper()
	content := []byte(
		"TransactionId,TransactionType,AccountingDate,AccountNumber,Amount\r\n" + rows,
	)
	digest := sha256.Sum256(content)
	statement, err := application.ParseBankStatement(application.StatementSnapshot{
		SourceName: sourceName,
		SHA256:     fmt.Sprintf("%x", digest),
		Content:    content,
	})
	if err != nil {
		t.Fatal("parse synthetic statement:", err)
	}
	return statement
}

func seedStatementAccounts(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
) {
	t.Helper()
	fixture.Exec(t, ctx, `
INSERT INTO public.policy_account (
    policy_number, policy_account_number, registered_premium, registered_cover_from,
    created, updated
) VALUES
    ('SYN-POLICY-KNOWN', '`+knownAccount+`', 0, '2018-01-01',
        '2018-01-01 00:00:00', '2018-01-01 00:00:00'),
    ('SYN-POLICY-DUPLICATE', '`+duplicateAccount+`', 0, '2018-01-01',
        '2018-01-01 00:00:00', '2018-01-01 00:00:00');`)
}

func assertReceiptFacts(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	statement application.BankStatement,
	wantState string,
	wantEntries int,
	wantAmount string,
) {
	t.Helper()
	var state, creationDate, effectiveDate, amount string
	var entries int
	err := fixture.Admin.QueryRow(ctx, `
SELECT r.state, count(e.id)::integer,
    COALESCE(sum(e.amount), 0)::numeric(19, 2)::text,
    COALESCE(min(e.creation_date)::text, ''),
    COALESCE(min(e.effective_date)::text, '')
FROM public.statement_receipt AS r
LEFT JOIN public.accounting_entry AS e ON e.statement_receipt_id = r.id
WHERE r.source_name = $1 AND r.sha256 = $2
GROUP BY r.id, r.state`,
		statement.Snapshot.SourceName,
		statement.Snapshot.SHA256,
	).Scan(&state, &entries, &amount, &creationDate, &effectiveDate)
	if err != nil {
		t.Fatal("read statement receipt facts:", err)
	}
	if state != wantState || entries != wantEntries || amount != wantAmount {
		t.Fatalf("receipt facts = (%s, %d, %s)", state, entries, amount)
	}
	if wantEntries > 0 && (creationDate != "2018-08-02" || effectiveDate != "2018-08-01") {
		t.Fatalf("entry dates = (%s, %s)", creationDate, effectiveDate)
	}
}

func assertDatedBalance(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	accountNumber string,
	wantBefore string,
	wantEffective string,
) {
	t.Helper()
	businessTime := time.Date(2018, time.July, 31, 23, 0, 0, 0, time.UTC)
	reads, err := application.NewAccountReads(store, func() time.Time { return businessTime })
	if err != nil {
		t.Fatal(err)
	}
	before, err := reads.Balance(ctx, accountNumber)
	if err != nil || before.Balance != wantBefore {
		t.Fatalf("balance before effective date = (%+v, %v)", before, err)
	}
	businessTime = time.Date(2018, time.August, 1, 23, 0, 0, 0, time.UTC)
	effective, err := reads.Balance(ctx, accountNumber)
	if err != nil || effective.Balance != wantEffective {
		t.Fatalf("balance on effective date = (%+v, %v)", effective, err)
	}
}

func statementCounts(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	sourceName string,
) (int, int) {
	t.Helper()
	var receipts, entries int
	err := fixture.Admin.QueryRow(ctx, `
SELECT count(DISTINCT r.id)::integer, count(e.id)::integer
FROM public.statement_receipt AS r
LEFT JOIN public.accounting_entry AS e ON e.statement_receipt_id = r.id
WHERE r.source_name = $1`, sourceName).Scan(&receipts, &entries)
	if err != nil {
		t.Fatal("read statement counts:", err)
	}
	return receipts, entries
}

func installCommitFailure(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
) {
	t.Helper()
	fixture.Exec(t, ctx, `
CREATE FUNCTION public.reject_statement_commit() RETURNS trigger
LANGUAGE plpgsql AS $function$
BEGIN
    IF NEW.source_name = 'bankStatements_2018_8_5.csv' THEN
        RAISE EXCEPTION 'synthetic deferred commit failure' USING ERRCODE = '40001';
    END IF;
    RETURN NEW;
END
$function$;
CREATE CONSTRAINT TRIGGER reject_statement_commit
AFTER INSERT ON public.statement_receipt
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.reject_statement_commit();`)
}

func removeCommitFailure(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
) {
	t.Helper()
	fixture.Exec(t, ctx, `
DROP TRIGGER reject_statement_commit ON public.statement_receipt;
DROP FUNCTION public.reject_statement_commit();`)
}
