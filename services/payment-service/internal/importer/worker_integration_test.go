//go:build integration

package importer_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/importer"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/objectstore"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/miniofixture"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/postgresfixture"
)

const statementWorkerAccount = "SYN-WORKER-ACCOUNT"

func TestRun_PaymentStatementImportWorker(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	database := postgresfixture.New(t, ctx)
	database.Call(t, ctx, "migrate", "version=1")
	database.Exec(t, ctx, `
INSERT INTO public.policy_account (
    policy_number, policy_account_number, registered_premium, registered_cover_from,
    created, updated
) VALUES ('SYN-WORKER-POLICY', '`+statementWorkerAccount+`', 0, '2018-01-01',
    '2018-01-01 00:00:00', '2018-01-01 00:00:00');`)
	objects := miniofixture.New(t, ctx)
	settings := statementWorkerSettings(t, database, objects)
	pool, err := postgres.OpenPool(ctx, settings.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := objectstore.NewReader(settings.ObjectStore)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reader.Close)
	postings, err := application.NewStatementPostings(store, func() time.Time {
		return time.Date(2018, time.August, 2, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	imports, err := application.NewStatementImports(reader, postings, store)
	if err != nil {
		t.Fatal(err)
	}
	olderName := "bankStatements_2018_8_1.csv"
	postedName := "bankStatements_2018_8_2.csv"
	futureName := "bankStatements_2018_8_4.csv"
	olderContent := statementWorkerContent("1")
	postedContent := statementWorkerContent("2")
	putStatementObject(t, ctx, objects.Client, olderName, olderContent)
	putStatementObject(t, ctx, objects.Client, futureName, statementWorkerContent("3"))
	postedStatement := statementWorkerStatement(t, postedName, postedContent)
	postedReceipt, err := postings.Post(ctx, postedStatement)
	if err != nil || postedReceipt.State != application.StatementPosted {
		t.Fatalf("prepare posted receipt = (%+v, %v)", postedReceipt, err)
	}
	putStatementObject(t, ctx, objects.Client, "_processed_"+postedName, postedContent)

	started := make(chan string, 1)
	release := make(chan struct{})
	blocking := &blockingStatementImporter{
		next: imports, started: started, release: release,
	}
	outcomes := make(chan importer.Outcome, 2)
	businessDate := time.Date(2018, time.August, 2, 23, 59, 0, 0, time.UTC)
	ticks := make(chan time.Time, 1)
	worker, err := importer.NewWorker(
		importer.Settings{
			Interval: time.Minute,
			Now:      func() time.Time { return businessDate },
			Ticks:    ticks,
			Observe: func(
				ctx context.Context,
			) (context.Context, func(context.Context, importer.Outcome, error)) {
				return ctx, func(_ context.Context, outcome importer.Outcome, _ error) {
					outcomes <- outcome
				}
			},
		},
		reader,
		store,
		blocking,
	)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	runResult := make(chan error, 1)
	go func() { runResult <- worker.Run(runCtx) }()
	if source := receiveIntegrationSource(t, ctx, started); source != postedName {
		t.Fatalf("first recovery source = %q, want %q", source, postedName)
	}
	businessDate = time.Date(2018, time.August, 3, 0, 1, 0, 0, time.UTC)
	ticks <- businessDate

	// when
	close(release)
	firstOutcome := receiveIntegrationOutcome(t, ctx, outcomes)
	secondOutcome := receiveIntegrationOutcome(t, ctx, outcomes)
	cancelRun()
	runErr := receiveIntegrationError(t, ctx, runResult)

	// then
	if runErr != nil {
		t.Fatal(runErr)
	}
	if firstOutcome != importer.OutcomeArchived || secondOutcome != importer.OutcomeIdle {
		t.Fatalf("worker outcomes = (%q, %q)", firstOutcome, secondOutcome)
	}
	if blocking.maximum.Load() != 1 {
		t.Fatalf("maximum concurrent imports = %d", blocking.maximum.Load())
	}
	if got := blocking.sources(); fmt.Sprint(
		got,
	) != fmt.Sprint(
		[]string{postedName, olderName},
	) {
		t.Fatalf("import order = %v", got)
	}
	if got := database.Query(t, ctx, `
SELECT COUNT(*)::text || ':' ||
    COUNT(*) FILTER (WHERE state = 'archived')::text || ':' ||
    (SELECT COUNT(*)::text FROM public.accounting_entry WHERE kind = 'in_payment')
FROM public.statement_receipt;`); got != "2:2:2" {
		t.Fatalf("receipt and credit facts = %q", got)
	}
	reads, err := application.NewAccountReads(store, func() time.Time { return businessDate })
	if err != nil {
		t.Fatal(err)
	}
	balance, err := reads.Balance(ctx, statementWorkerAccount)
	if err != nil || balance.Balance != "20.42" {
		t.Fatalf("dated balance = (%+v, %v)", balance, err)
	}
	assertStatementMissing(t, ctx, objects.Client, olderName)
	assertStatementPresent(t, ctx, objects.Client, "_processed_"+olderName)
	assertStatementMissing(t, ctx, objects.Client, postedName)
	assertStatementPresent(t, ctx, objects.Client, "_processed_"+postedName)
	assertStatementPresent(t, ctx, objects.Client, futureName)
}

type blockingStatementImporter struct {
	next    importer.StatementImporter
	started chan<- string
	release <-chan struct{}
	active  atomic.Int32
	maximum atomic.Int32
	mu      sync.Mutex
	called  []string
	once    sync.Once
}

func (imports *blockingStatementImporter) Import(
	ctx context.Context,
	source string,
) (application.StatementReceipt, error) {
	current := imports.active.Add(1)
	defer imports.active.Add(-1)
	if current > imports.maximum.Load() {
		imports.maximum.Store(current)
	}
	imports.mu.Lock()
	imports.called = append(imports.called, source)
	imports.mu.Unlock()
	imports.once.Do(func() {
		imports.started <- source
		select {
		case <-imports.release:
		case <-ctx.Done():
		}
	})
	if err := ctx.Err(); err != nil {
		return application.StatementReceipt{}, err
	}
	return imports.next.Import(ctx, source)
}

func (imports *blockingStatementImporter) sources() []string {
	imports.mu.Lock()
	defer imports.mu.Unlock()
	return append([]string(nil), imports.called...)
}

func statementWorkerSettings(
	t *testing.T,
	database *postgresfixture.Fixture,
	objects *miniofixture.Fixture,
) config.Config {
	t.Helper()
	t.Setenv("PAYMENT_DATABASE_URL", database.WriterURL("payment"))
	t.Setenv("MINIO_ENDPOINT", objects.Endpoint)
	t.Setenv("MINIO_ACCESS_KEY", objects.AccessKey)
	t.Setenv("MINIO_SECRET_KEY", objects.SecretKey)
	t.Setenv("KAFKA_BROKERS", "kafka.invalid:9093")
	t.Setenv("KAFKA_CA_FILE", "/synthetic/ca.crt")
	settings, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func statementWorkerStatement(
	t *testing.T,
	name string,
	content []byte,
) application.BankStatement {
	t.Helper()
	digest := sha256.Sum256(content)
	statement, err := application.ParseBankStatement(application.StatementSnapshot{
		SourceName: name, SHA256: fmt.Sprintf("%x", digest), Content: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	return statement
}

func statementWorkerContent(id string) []byte {
	return []byte(
		"TransactionId,TransactionType,AccountingDate,AccountNumber,Amount\r\n" +
			id + ",A,2018-08-01," + statementWorkerAccount + ",10.21\r\n",
	)
}

func putStatementObject(
	t *testing.T,
	ctx context.Context,
	client *minio.Client,
	name string,
	content []byte,
) {
	t.Helper()
	if _, err := client.PutObject(
		ctx,
		"payments-import",
		name,
		bytes.NewReader(content),
		int64(len(content)),
		minio.PutObjectOptions{ContentType: "text/csv"},
	); err != nil {
		t.Fatal(err)
	}
}

func assertStatementPresent(t *testing.T, ctx context.Context, client *minio.Client, name string) {
	t.Helper()
	if _, err := client.StatObject(
		ctx,
		"payments-import",
		name,
		minio.StatObjectOptions{},
	); err != nil {
		t.Fatalf("statement object %q is missing: %v", name, err)
	}
}

func assertStatementMissing(t *testing.T, ctx context.Context, client *minio.Client, name string) {
	t.Helper()
	_, err := client.StatObject(ctx, "payments-import", name, minio.StatObjectOptions{})
	if response := minio.ToErrorResponse(err); err == nil || response.StatusCode != 404 {
		t.Fatalf("statement object %q remains: %v", name, err)
	}
}

func receiveIntegrationSource(t *testing.T, ctx context.Context, sources <-chan string) string {
	t.Helper()
	select {
	case source := <-sources:
		return source
	case <-ctx.Done():
		t.Fatal("statement import did not start")
		return ""
	}
}

func receiveIntegrationOutcome(
	t *testing.T,
	ctx context.Context,
	outcomes <-chan importer.Outcome,
) importer.Outcome {
	t.Helper()
	select {
	case outcome := <-outcomes:
		return outcome
	case <-ctx.Done():
		t.Fatal("statement import outcome was not observed")
		return ""
	}
}

func receiveIntegrationError(t *testing.T, ctx context.Context, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal("statement importer did not drain")
		return nil
	}
}
