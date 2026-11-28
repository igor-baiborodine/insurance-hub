//go:build integration

package objectstore_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/objectstore"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/miniofixture"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/postgresfixture"
)

const archiveAccount = "SYN-ARCHIVE-ACCOUNT"

var errArchiveInterrupted = errors.New("synthetic archive interruption")

func TestRun_PaymentStatementArchiveRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	database := postgresfixture.New(t, ctx)
	database.Call(t, ctx, "migrate", "version=1")
	database.Exec(t, ctx, `
INSERT INTO public.policy_account (
    policy_number, policy_account_number, registered_premium, registered_cover_from,
    created, updated
) VALUES ('SYN-ARCHIVE-POLICY', '`+archiveAccount+`', 0, '2018-01-01',
    '2018-01-01 00:00:00', '2018-01-01 00:00:00');`)
	objects := miniofixture.New(t, ctx)
	settings := archiveSettings(t, database, objects)
	pool, err := postgres.OpenPool(ctx, settings.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	receipts, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := objectstore.NewReader(settings.ObjectStore)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bucket.Close)

	t.Run("interruption after commit resumes on the next UTC day", func(t *testing.T) {
		key := "bankStatements_2018_8_2.csv"
		content := archiveStatement("1", "10.21")
		putArchiveObject(t, ctx, objects.Client, key, content)
		interrupted := &archiveFault{
			objects: bucket,
			before:  func(application.StatementSnapshot) {},
		}
		imports := archiveImports(t, receipts, interrupted, time.Date(
			2018, time.August, 2, 23, 59, 0, 0, time.UTC,
		))

		receipt, err := imports.Import(ctx, key)
		if !errors.Is(err, errArchiveInterrupted) ||
			receipt.State != application.StatementPosted {
			t.Fatalf("interrupted import = (%+v, %v)", receipt, err)
		}
		assertArchiveFacts(t, ctx, database, key, "posted", 1, 1)
		assertArchiveObject(t, ctx, objects.Client, key, content)
		assertArchiveObjectMissing(t, ctx, objects.Client, processedArchiveName(key))

		imports = archiveImports(t, receipts, bucket, time.Date(
			2018, time.August, 3, 0, 1, 0, 0, time.UTC,
		))
		receipt, err = imports.Import(ctx, key)
		if err != nil || receipt.State != application.StatementArchived {
			t.Fatalf("resumed import = (%+v, %v)", receipt, err)
		}
		assertArchiveFacts(t, ctx, database, key, "archived", 1, 1)
		assertArchiveObjectMissing(t, ctx, objects.Client, key)
		assertArchiveObject(t, ctx, objects.Client, processedArchiveName(key), content)
	})

	t.Run("interruption during processed creation retains the source", func(t *testing.T) {
		key := "bankStatements_2018_8_3.csv"
		content := archiveStatement("2", "10.21")
		putArchiveObject(t, ctx, objects.Client, key, content)
		interrupted := &archiveFault{
			objects: bucket,
			before: func(snapshot application.StatementSnapshot) {
				putArchiveObject(
					t,
					ctx,
					objects.Client,
					processedArchiveName(key),
					snapshot.Content,
				)
			},
		}

		receipt, err := archiveImports(
			t,
			receipts,
			interrupted,
			time.Now(),
		).Import(ctx, key)
		if !errors.Is(err, errArchiveInterrupted) ||
			receipt.State != application.StatementPosted {
			t.Fatalf("copy interruption = (%+v, %v)", receipt, err)
		}
		assertArchiveObject(t, ctx, objects.Client, key, content)
		assertArchiveObject(t, ctx, objects.Client, processedArchiveName(key), content)

		receipt, err = archiveImports(t, receipts, bucket, time.Now()).Import(ctx, key)
		if err != nil || receipt.State != application.StatementArchived {
			t.Fatalf("copy retry = (%+v, %v)", receipt, err)
		}
		assertArchiveFacts(t, ctx, database, key, "archived", 1, 1)
	})

	t.Run("interruption after deletion recovers from the processed copy", func(t *testing.T) {
		key := "bankStatements_2018_8_4.csv"
		content := archiveStatement("3", "10.21")
		putArchiveObject(t, ctx, objects.Client, key, content)
		interrupted := &archiveFault{objects: bucket, after: true}

		receipt, err := archiveImports(
			t,
			receipts,
			interrupted,
			time.Now(),
		).Import(ctx, key)
		if !errors.Is(err, errArchiveInterrupted) ||
			receipt.State != application.StatementPosted {
			t.Fatalf("delete interruption = (%+v, %v)", receipt, err)
		}
		assertArchiveFacts(t, ctx, database, key, "posted", 1, 1)
		assertArchiveObjectMissing(t, ctx, objects.Client, key)
		assertArchiveObject(t, ctx, objects.Client, processedArchiveName(key), content)

		receipt, err = archiveImports(t, receipts, bucket, time.Now()).Import(ctx, key)
		if err != nil || receipt.State != application.StatementArchived {
			t.Fatalf("processed-only retry = (%+v, %v)", receipt, err)
		}
		assertArchiveFacts(t, ctx, database, key, "archived", 1, 1)
	})

	t.Run("replacement before conditional delete survives for review", func(t *testing.T) {
		key := "bankStatements_2018_8_5.csv"
		accepted := archiveStatement("4", "10.21")
		changed := archiveStatement("4", "10.22")
		putArchiveObject(t, ctx, objects.Client, key, accepted)
		mutated := false
		tracingObjects := &archiveMutation{
			objects: bucket,
			before: func() {
				if !mutated {
					mutated = true
					putArchiveObject(t, ctx, objects.Client, key, changed)
				}
			},
		}

		receipt, err := archiveImports(
			t,
			receipts,
			tracingObjects,
			time.Now(),
		).Import(ctx, key)
		if !errors.Is(err, application.ErrStatementConflict) ||
			receipt.State != application.StatementPosted {
			t.Fatalf("source replacement = (%+v, %v)", receipt, err)
		}
		assertArchiveObject(t, ctx, objects.Client, key, changed)
		assertArchiveObject(t, ctx, objects.Client, processedArchiveName(key), accepted)

		receipt, err = archiveImports(t, receipts, bucket, time.Now()).Import(ctx, key)
		if err != nil || receipt.State != application.StatementReview {
			t.Fatalf("changed source retry = (%+v, %v)", receipt, err)
		}
		assertArchiveFacts(t, ctx, database, key, "review", 2, 1)
		assertArchiveObject(t, ctx, objects.Client, key, changed)
		assertArchiveObject(t, ctx, objects.Client, processedArchiveName(key), accepted)
	})

	t.Run("conflicting processed creation never overwrites bytes", func(t *testing.T) {
		key := "bankStatements_2018_8_6.csv"
		accepted := archiveStatement("5", "10.21")
		conflict := archiveStatement("5", "10.22")
		putArchiveObject(t, ctx, objects.Client, key, accepted)
		mutated := false
		tracingObjects := &archiveMutation{
			objects: bucket,
			before: func() {
				if !mutated {
					mutated = true
					putArchiveObject(
						t,
						ctx,
						objects.Client,
						processedArchiveName(key),
						conflict,
					)
				}
			},
		}

		receipt, err := archiveImports(
			t,
			receipts,
			tracingObjects,
			time.Now(),
		).Import(ctx, key)
		if !errors.Is(err, application.ErrStatementConflict) ||
			receipt.State != application.StatementPosted {
			t.Fatalf("processed conflict = (%+v, %v)", receipt, err)
		}
		assertArchiveObject(t, ctx, objects.Client, key, accepted)
		assertArchiveObject(t, ctx, objects.Client, processedArchiveName(key), conflict)

		_, err = archiveImports(t, receipts, bucket, time.Now()).Import(ctx, key)
		if !errors.Is(err, application.ErrStatementConflict) {
			t.Fatalf("processed conflict retry = %v", err)
		}
		assertArchiveFacts(t, ctx, database, key, "posted", 1, 1)
		assertArchiveObject(t, ctx, objects.Client, key, accepted)
		assertArchiveObject(t, ctx, objects.Client, processedArchiveName(key), conflict)
	})

	t.Run("failed SQL commit creates no processed copy", func(t *testing.T) {
		key := "bankStatements_2018_8_7.csv"
		content := archiveStatement("6", "10.21")
		putArchiveObject(t, ctx, objects.Client, key, content)
		installArchiveCommitFailure(t, ctx, database, key)

		receipt, err := archiveImports(t, receipts, bucket, time.Now()).Import(ctx, key)
		removeArchiveCommitFailure(t, ctx, database)
		if err == nil || receipt != (application.StatementReceipt{}) {
			t.Fatalf("failed SQL import = (%+v, %v)", receipt, err)
		}
		assertArchiveFacts(t, ctx, database, key, "", 0, 0)
		assertArchiveObject(t, ctx, objects.Client, key, content)
		assertArchiveObjectMissing(t, ctx, objects.Client, processedArchiveName(key))
	})
}

func archiveSettings(
	t *testing.T,
	database *postgresfixture.Fixture,
	objects *miniofixture.Fixture,
) config.Config {
	t.Helper()
	t.Setenv("PAYMENT_DATABASE_URL", database.WriterURL("payment"))
	t.Setenv("MINIO_ENDPOINT", objects.Endpoint)
	t.Setenv("MINIO_ACCESS_KEY", objects.AccessKey)
	t.Setenv("MINIO_SECRET_KEY", objects.SecretKey)
	t.Setenv("KAFKA_BROKERS", "kafka.example:9093")
	t.Setenv("KAFKA_CA_FILE", "/etc/payment-service/kafka/ca.crt")
	settings, err := config.Load()
	if err != nil {
		t.Fatal("load archive integration settings:", err)
	}
	return settings
}

func archiveImports(
	t *testing.T,
	receipts *postgres.Store,
	objects application.StatementObjects,
	now time.Time,
) *application.StatementImports {
	t.Helper()
	postings, err := application.NewStatementPostings(receipts, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	imports, err := application.NewStatementImports(objects, postings, receipts)
	if err != nil {
		t.Fatal(err)
	}
	return imports
}

func archiveStatement(id, amount string) []byte {
	return []byte(
		"TransactionId,TransactionType,AccountingDate,AccountNumber,Amount\r\n" +
			id + ",A,2018-08-01," + archiveAccount + "," + amount + "\r\n",
	)
}

func processedArchiveName(sourceName string) string { return "_processed_" + sourceName }

func putArchiveObject(
	t *testing.T,
	ctx context.Context,
	client *minio.Client,
	key string,
	content []byte,
) {
	t.Helper()
	if _, err := client.PutObject(
		ctx,
		"payments-import",
		key,
		bytes.NewReader(content),
		int64(len(content)),
		minio.PutObjectOptions{ContentType: "text/csv"},
	); err != nil {
		t.Fatal("put disposable statement object:", err)
	}
}

func assertArchiveObject(
	t *testing.T,
	ctx context.Context,
	client *minio.Client,
	key string,
	want []byte,
) {
	t.Helper()
	object, err := client.GetObject(ctx, "payments-import", key, minio.GetObjectOptions{})
	if err != nil {
		t.Fatal("get disposable statement object:", err)
	}
	defer func() { _ = object.Close() }()
	got, err := io.ReadAll(object)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("object %q mismatch: bytes=%t error=%v", key, bytes.Equal(got, want), err)
	}
}

func assertArchiveObjectMissing(
	t *testing.T,
	ctx context.Context,
	client *minio.Client,
	key string,
) {
	t.Helper()
	_, err := client.StatObject(ctx, "payments-import", key, minio.StatObjectOptions{})
	if response := minio.ToErrorResponse(err); err == nil || response.StatusCode != 404 {
		t.Fatalf("object %q remains: %v", key, err)
	}
}

func assertArchiveFacts(
	t *testing.T,
	ctx context.Context,
	database *postgresfixture.Fixture,
	sourceName string,
	wantState string,
	wantReceipts int,
	wantEntries int,
) {
	t.Helper()
	var state string
	var receipts, entries int
	err := database.Admin.QueryRow(ctx, `
SELECT COALESCE(max(r.state), ''), count(DISTINCT r.id)::integer, count(e.id)::integer
FROM public.statement_receipt AS r
LEFT JOIN public.accounting_entry AS e ON e.statement_receipt_id = r.id
WHERE r.source_name = $1`, sourceName).Scan(&state, &receipts, &entries)
	if err != nil || state != wantState || receipts != wantReceipts || entries != wantEntries {
		t.Fatalf("archive facts = (%q, %d, %d, %v), want (%q, %d, %d)",
			state, receipts, entries, err, wantState, wantReceipts, wantEntries)
	}
}

func installArchiveCommitFailure(
	t *testing.T,
	ctx context.Context,
	database *postgresfixture.Fixture,
	sourceName string,
) {
	t.Helper()
	database.Exec(t, ctx, fmt.Sprintf(`
CREATE FUNCTION public.reject_archive_statement_commit() RETURNS trigger
LANGUAGE plpgsql AS $function$
BEGIN
    IF NEW.source_name = '%s' THEN
        RAISE EXCEPTION 'synthetic deferred commit failure' USING ERRCODE = '40001';
    END IF;
    RETURN NEW;
END
$function$;
CREATE CONSTRAINT TRIGGER reject_archive_statement_commit
AFTER INSERT ON public.statement_receipt
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.reject_archive_statement_commit();`, sourceName))
}

func removeArchiveCommitFailure(
	t *testing.T,
	ctx context.Context,
	database *postgresfixture.Fixture,
) {
	t.Helper()
	database.Exec(t, ctx, `
DROP TRIGGER reject_archive_statement_commit ON public.statement_receipt;
DROP FUNCTION public.reject_archive_statement_commit();`)
}

type archiveFault struct {
	objects application.StatementObjects
	before  func(application.StatementSnapshot)
	after   bool
}

func (fault *archiveFault) ReadStatement(
	ctx context.Context,
	key string,
) (application.StatementSnapshot, error) {
	return fault.objects.ReadStatement(ctx, key)
}

func (fault *archiveFault) ReadProcessedStatement(
	ctx context.Context,
	key string,
) (application.StatementSnapshot, error) {
	return fault.objects.ReadProcessedStatement(ctx, key)
}

func (fault *archiveFault) ArchiveStatement(
	ctx context.Context,
	snapshot application.StatementSnapshot,
) error {
	if fault.before != nil {
		fault.before(snapshot)
		return errArchiveInterrupted
	}
	if fault.after {
		if err := fault.objects.ArchiveStatement(ctx, snapshot); err != nil {
			return err
		}
		return errArchiveInterrupted
	}
	return fault.objects.ArchiveStatement(ctx, snapshot)
}

type archiveMutation struct {
	objects application.StatementObjects
	before  func()
}

func (mutation *archiveMutation) ReadStatement(
	ctx context.Context,
	key string,
) (application.StatementSnapshot, error) {
	return mutation.objects.ReadStatement(ctx, key)
}

func (mutation *archiveMutation) ReadProcessedStatement(
	ctx context.Context,
	key string,
) (application.StatementSnapshot, error) {
	return mutation.objects.ReadProcessedStatement(ctx, key)
}

func (mutation *archiveMutation) ArchiveStatement(
	ctx context.Context,
	snapshot application.StatementSnapshot,
) error {
	if mutation.before != nil {
		mutation.before()
	}
	return mutation.objects.ArchiveStatement(ctx, snapshot)
}
