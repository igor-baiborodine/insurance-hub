//go:build integration

package testing_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
	paymentkafka "github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/kafka"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/telemetry"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/kafkafixture"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/postgresfixture"
)

const (
	kafkaTopic = "policy-registered"
	kafkaGroup = "payment-service-qualification"
)

type compatibilityEvent struct {
	Topic string          `json:"topic"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

type compatibilityFixture struct {
	SchemaVersion int      `json:"schemaVersion"`
	Ownership     string   `json:"ownership"`
	ProducerOrder []string `json:"producerOrder"`
	Events        struct {
		Registration compatibilityEvent `json:"registration"`
		Termination  compatibilityEvent `json:"termination"`
	} `json:"events"`
}

type kafkaFixture struct {
	*kafkafixture.Fixture
	client *kgo.Client
}

func TestRun_PaymentRegistrationWorker(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	kafka := newKafkaFixture(t, ctx)
	registration := loadCompatibilityRegistration(t)
	commitOffset(t, ctx, kafka.client, 0)

	database := postgresfixture.New(t, ctx)
	database.Call(t, ctx, "migrate", "version=1")
	pool, err := postgres.OpenPool(ctx, writerSettings(t, database))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	registrations, err := application.NewPolicyRegistrations(store, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	observer, err := telemetry.NewRegistrationObserver(
		slog.New(slog.NewTextHandler(&diagnostics, nil)),
		metricnoop.NewMeterProvider(),
		tracenoop.NewTracerProvider(),
	)
	if err != nil {
		t.Fatal(err)
	}

	// when
	produce(t, ctx, kafka.client, registration)
	worker := qualificationWorker(t, kafka, registrations, observer)
	runWorkerUntilOffset(t, ctx, worker, kafka.client, 1)

	// then
	first := readRegistration(t, ctx, database, registration.Key)
	if first.premium != "120.00" || first.coverageStart != "2026-01-01" ||
		first.amount != "120.00" || first.entries != 1 {
		t.Fatalf("first durable registration = %+v", first)
	}
	assertPrivateReads(t, ctx, store, first.accountNumber)

	t.Run("database rollback retains progress for retry", func(t *testing.T) {
		// given
		rollback := changedPolicyNumber(registration, "SYN-442-ROLLBACK")
		database.Exec(
			t,
			ctx,
			"REVOKE INSERT ON public.accounting_entry FROM go_payment_writer",
		)
		produce(t, ctx, kafka.client, rollback)
		worker := qualificationWorker(t, kafka, registrations, observer)

		// when
		runErr := runWorker(t, ctx, worker)
		database.Exec(
			t,
			ctx,
			"GRANT INSERT ON public.accounting_entry TO go_payment_writer",
		)

		// then
		if !errors.Is(runErr, paymentkafka.ErrRegistrationHeld) {
			t.Fatalf("rollback worker error = %v", runErr)
		}
		assertOffset(t, ctx, kafka.client, 1)
		assertRegistrationCounts(t, ctx, database, rollback.Key, 0, 0)
		retry := qualificationWorker(t, kafka, registrations, observer)
		runWorkerUntilOffset(t, ctx, retry, kafka.client, 2)
		assertRegistrationCounts(t, ctx, database, rollback.Key, 1, 1)
	})

	t.Run(
		"restart after database commit preserves identity before acknowledgement",
		func(t *testing.T) {
			// given
			interrupted := changedPolicyNumber(registration, "SYN-442-INTERRUPTED")
			produce(t, ctx, kafka.client, interrupted)
			operation := &cancelAfterSuccess{next: registrations}
			handler, err := paymentkafka.NewRegistrationHandler(operation)
			if err != nil {
				t.Fatal(err)
			}
			worker := newQualificationWorker(t, kafka, handler, observer)
			runCtx, cancelRun := context.WithCancel(ctx)
			operation.cancel = cancelRun
			if err := worker.Preflight(runCtx); err != nil {
				t.Fatal(err)
			}

			// when
			runErr := worker.Run(runCtx)
			shutdownWorker(t, worker)

			// then
			if runErr != nil {
				t.Fatalf("interrupted worker error = %v", runErr)
			}
			assertOffset(t, ctx, kafka.client, 2)
			durable := readRegistration(t, ctx, database, interrupted.Key)
			restarted := qualificationWorker(t, kafka, registrations, observer)
			runWorkerUntilOffset(t, ctx, restarted, kafka.client, 3)
			if replayed := readRegistration(
				t,
				ctx,
				database,
				interrupted.Key,
			); replayed != durable {
				t.Fatal("unacknowledged replay changed account identity or debit")
			}

			produce(t, ctx, kafka.client, interrupted)
			identical := qualificationWorker(t, kafka, registrations, observer)
			runWorkerUntilOffset(t, ctx, identical, kafka.client, 4)
			if replayed := readRegistration(
				t,
				ctx,
				database,
				interrupted.Key,
			); replayed != durable {
				t.Fatal("same registration at another offset changed durable state")
			}
		},
	)

	t.Run("held record survives rebalance shutdown and restart", func(t *testing.T) {
		// given
		held := changedPremium(registration, "121.00")
		later := changedPolicyNumber(registration, "SYN-442-LATER")
		produce(t, ctx, kafka.client, held, later)
		started := make(chan struct{})
		release := make(chan struct{})
		operation := &blockingFailure{
			next: registrations, started: started, release: release,
		}
		handler, err := paymentkafka.NewRegistrationHandler(operation)
		if err != nil {
			t.Fatal(err)
		}
		worker := newQualificationWorker(t, kafka, handler, observer)
		if err := worker.Preflight(ctx); err != nil {
			t.Fatal(err)
		}
		runResult := make(chan error, 1)
		go func() { runResult <- worker.Run(ctx) }()
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("held registration did not reach storage comparison")
		}
		rebalance := groupConsumer(t, kafka)
		rebalanceCtx, cancelRebalance := context.WithCancel(ctx)
		rebalancedRecords := make(chan []*kgo.Record, 1)
		rebalanceResult := make(chan struct{})
		go func() {
			for rebalanceCtx.Err() == nil {
				fetches := rebalance.PollFetches(rebalanceCtx)
				if records := fetches.Records(); len(records) > 0 {
					rebalancedRecords <- records
					break
				}
			}
			close(rebalanceResult)
		}()

		// when
		close(release)
		runErr := receiveWorkerResult(t, ctx, runResult)
		shutdownWorker(t, worker)
		var reassigned []*kgo.Record
		select {
		case reassigned = <-rebalancedRecords:
		case <-ctx.Done():
			t.Fatal("held partition was not reassigned after worker shutdown")
		}
		cancelRebalance()
		rebalance.CloseAllowingRebalance()
		<-rebalanceResult
		waitForInactiveGroup(t, ctx, kafka.client)

		// then
		if !errors.Is(runErr, paymentkafka.ErrRegistrationHeld) {
			t.Fatalf("held worker error = %v", runErr)
		}
		if reassigned[0].Offset != 4 {
			t.Fatalf("reassigned first offset = %d, want 4", reassigned[0].Offset)
		}
		assertOffset(t, ctx, kafka.client, 4)
		assertRegistrationCounts(t, ctx, database, later.Key, 0, 0)
		restarted := qualificationWorker(t, kafka, registrations, observer)
		if err := runWorker(t, ctx, restarted); !errors.Is(
			err,
			paymentkafka.ErrRegistrationHeld,
		) {
			t.Fatalf("restarted held worker error = %v", err)
		}
		assertOffset(t, ctx, kafka.client, 4)
	})

	for _, private := range []string{
		registration.Key,
		"SYN-442-ROLLBACK",
		"SYN-442-INTERRUPTED",
		"SYN-442-LATER",
		"120.00",
		"121.00",
	} {
		if strings.Contains(diagnostics.String(), private) {
			t.Fatalf("registration diagnostics expose private value %q", private)
		}
	}
}

type registrationOperation interface {
	RegisterPolicy(context.Context, application.PolicyRegistration) error
}

func writerSettings(t *testing.T, fixture *postgresfixture.Fixture) config.Database {
	t.Helper()
	t.Setenv("PAYMENT_DATABASE_URL", fixture.WriterURL("payment"))
	t.Setenv("MINIO_ENDPOINT", "http://minio.example:9000")
	t.Setenv("MINIO_ACCESS_KEY", "access")
	t.Setenv("MINIO_SECRET_KEY", "secret")
	t.Setenv("KAFKA_BROKERS", "kafka.example:9093")
	t.Setenv("KAFKA_CA_FILE", "/etc/payment-service/kafka/ca.crt")
	settings, err := config.Load()
	if err != nil {
		t.Fatal("load disposable writer settings:", err)
	}
	return settings.Database
}

type cancelAfterSuccess struct {
	next   registrationOperation
	cancel context.CancelFunc
}

func (operation *cancelAfterSuccess) RegisterPolicy(
	ctx context.Context,
	registration application.PolicyRegistration,
) error {
	if err := operation.next.RegisterPolicy(ctx, registration); err != nil {
		return err
	}
	operation.cancel()
	return nil
}

type blockingFailure struct {
	next    registrationOperation
	started chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (operation *blockingFailure) RegisterPolicy(
	ctx context.Context,
	registration application.PolicyRegistration,
) error {
	err := operation.next.RegisterPolicy(ctx, registration)
	if err == nil {
		return nil
	}
	operation.once.Do(func() { close(operation.started) })
	select {
	case <-operation.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return err
}

func qualificationWorker(
	t *testing.T,
	kafka *kafkaFixture,
	registrations registrationOperation,
	observer *telemetry.RegistrationObserver,
) *paymentkafka.RegistrationWorker {
	t.Helper()
	handler, err := paymentkafka.NewRegistrationHandler(registrations)
	if err != nil {
		t.Fatal(err)
	}
	return newQualificationWorker(t, kafka, handler, observer)
}

func newQualificationWorker(
	t *testing.T,
	kafka *kafkaFixture,
	handler *paymentkafka.RegistrationHandler,
	observer *telemetry.RegistrationObserver,
) *paymentkafka.RegistrationWorker {
	t.Helper()
	worker, err := paymentkafka.NewRegistrationWorker(
		paymentkafka.RegistrationSettings{
			Settings: paymentkafka.Settings{
				Brokers:  []string{kafka.Address},
				Topic:    kafkaTopic,
				ClientID: "payment-registration-qualification",
				CACert:   kafka.CACert,
			},
			Group: kafkaGroup, Observe: observer.Start,
			CommitFailure: observer.RecordCommitFailure,
		},
		handler,
	)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func runWorker(
	t *testing.T,
	ctx context.Context,
	worker *paymentkafka.RegistrationWorker,
) error {
	t.Helper()
	if err := worker.Preflight(ctx); err != nil {
		t.Fatal(err)
	}
	err := worker.Run(ctx)
	shutdownWorker(t, worker)
	return err
}

func runWorkerUntilOffset(
	t *testing.T,
	ctx context.Context,
	worker *paymentkafka.RegistrationWorker,
	inspector *kgo.Client,
	want int64,
) {
	t.Helper()
	if err := worker.Preflight(ctx); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { result <- worker.Run(runCtx) }()
	waitForOffset(t, ctx, inspector, want)
	cancel()
	if err := receiveWorkerResult(t, ctx, result); err != nil {
		t.Fatal(err)
	}
	shutdownWorker(t, worker)
}

func shutdownWorker(t *testing.T, worker *paymentkafka.RegistrationWorker) {
	t.Helper()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := worker.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func receiveWorkerResult(t *testing.T, ctx context.Context, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal("registration worker did not stop")
		return nil
	}
}

func groupConsumer(t *testing.T, fixture *kafkaFixture) *kgo.Client {
	t.Helper()
	options := kafkaOptions(t, fixture)
	options = append(options,
		kgo.ConsumerGroup(kafkaGroup),
		kgo.ConsumeTopics(kafkaTopic),
		kgo.ConsumeResetOffset(kgo.NoResetOffset()),
	)
	client, err := kgo.NewClient(options...)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func newKafkaFixture(t *testing.T, ctx context.Context) *kafkaFixture {
	t.Helper()
	fixture := &kafkaFixture{Fixture: kafkafixture.New(t, ctx)}
	client, err := kgo.NewClient(kafkaOptions(t, fixture)...)
	if err != nil {
		t.Fatal(err)
	}
	fixture.client = client
	t.Cleanup(client.Close)
	return fixture
}

func kafkaOptions(t *testing.T, fixture *kafkaFixture) []kgo.Opt {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(fixture.CACert) {
		t.Fatal("append disposable Kafka CA")
	}
	return []kgo.Opt{
		kgo.SeedBrokers(fixture.Address),
		kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}),
	}
}

func commitOffset(t *testing.T, ctx context.Context, client *kgo.Client, offset int64) {
	t.Helper()
	request := kmsg.NewPtrOffsetCommitRequest()
	request.Group = kafkaGroup
	request.Topics = []kmsg.OffsetCommitRequestTopic{
		{
			Topic: kafkaTopic,
			Partitions: []kmsg.OffsetCommitRequestTopicPartition{
				{Partition: 0, Offset: offset},
			},
		},
	}
	response, err := client.Request(ctx, request)
	committed, ok := response.(*kmsg.OffsetCommitResponse)
	if err != nil || !ok || len(committed.Topics) != 1 ||
		len(committed.Topics[0].Partitions) != 1 ||
		committed.Topics[0].Partitions[0].ErrorCode != 0 {
		t.Fatalf("initialize disposable group progress: %v", err)
	}
}

func produce(t *testing.T, ctx context.Context, client *kgo.Client, events ...compatibilityEvent) {
	t.Helper()
	records := make([]*kgo.Record, 0, len(events))
	for _, event := range events {
		records = append(records, &kgo.Record{
			Topic: event.Topic, Key: []byte(event.Key), Value: event.Value,
		})
	}
	if err := client.ProduceSync(ctx, records...).FirstErr(); err != nil {
		t.Fatal(err)
	}
}

func loadCompatibilityRegistration(t *testing.T) compatibilityEvent {
	t.Helper()
	payload, err := os.ReadFile(
		"../../../policy-service/testdata/events/consumer-compatibility.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	var fixture compatibilityFixture
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	registration := fixture.Events.Registration
	if fixture.SchemaVersion != 1 || fixture.Ownership != "policy-service" ||
		len(fixture.ProducerOrder) != 2 || fixture.ProducerOrder[0] != "registration" ||
		registration.Topic != kafkaTopic || registration.Key != "SYN-442-001" ||
		!bytes.Contains(registration.Value, []byte(`"totalPremium": 120.00`)) {
		t.Fatal(
			"Policy compatibility fixture differs from the accepted registration oracle",
		)
	}
	return registration
}

func changedPolicyNumber(source compatibilityEvent, number string) compatibilityEvent {
	return compatibilityEvent{
		Topic: source.Topic, Key: number,
		Value: bytes.ReplaceAll(source.Value, []byte(source.Key), []byte(number)),
	}
}

func changedPremium(source compatibilityEvent, premium string) compatibilityEvent {
	return compatibilityEvent{
		Topic: source.Topic, Key: source.Key,
		Value: bytes.Replace(source.Value, []byte("120.00"), []byte(premium), 1),
	}
}

func waitForOffset(t *testing.T, ctx context.Context, client *kgo.Client, want int64) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if offset := readOffset(t, ctx, client); offset == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("consumer progress did not reach %d", want)
}

func assertOffset(t *testing.T, ctx context.Context, client *kgo.Client, want int64) {
	t.Helper()
	if got := readOffset(t, ctx, client); got != want {
		t.Fatalf("committed offset = %d, want %d", got, want)
	}
}

func readOffset(t *testing.T, ctx context.Context, client *kgo.Client) int64 {
	t.Helper()
	request := kmsg.NewPtrOffsetFetchRequest()
	request.Group = kafkaGroup
	request.Topics = []kmsg.OffsetFetchRequestTopic{{Topic: kafkaTopic, Partitions: []int32{0}}}
	request.Groups = []kmsg.OffsetFetchRequestGroup{
		{
			Group: kafkaGroup,
			Topics: []kmsg.OffsetFetchRequestGroupTopic{
				{Topic: kafkaTopic, Partitions: []int32{0}},
			},
		},
	}
	request.RequireStable = true
	response, err := client.Request(ctx, request)
	fetched, ok := response.(*kmsg.OffsetFetchResponse)
	if err != nil || !ok {
		t.Fatalf("read committed progress: %v", err)
	}
	if len(fetched.Groups) == 1 && len(fetched.Groups[0].Topics) == 1 &&
		len(fetched.Groups[0].Topics[0].Partitions) == 1 {
		return fetched.Groups[0].Topics[0].Partitions[0].Offset
	}
	if len(fetched.Topics) == 1 && len(fetched.Topics[0].Partitions) == 1 {
		return fetched.Topics[0].Partitions[0].Offset
	}
	t.Fatal("committed progress response is incomplete")
	return -1
}

func waitForInactiveGroup(t *testing.T, ctx context.Context, client *kgo.Client) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if groupMembers(t, ctx, client) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("consumer group remained active")
}

func groupMembers(t *testing.T, ctx context.Context, client *kgo.Client) int {
	t.Helper()
	request := kmsg.NewPtrDescribeGroupsRequest()
	request.Groups = []string{kafkaGroup}
	response, err := client.Request(ctx, request)
	described, ok := response.(*kmsg.DescribeGroupsResponse)
	if err != nil || !ok || len(described.Groups) != 1 || described.Groups[0].ErrorCode != 0 {
		t.Fatalf("describe disposable group: %v", err)
	}
	return len(described.Groups[0].Members)
}

type durableRegistration struct {
	accountNumber string
	premium       string
	coverageStart string
	amount        string
	entries       int
}

func readRegistration(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	policyNumber string,
) durableRegistration {
	t.Helper()
	var result durableRegistration
	err := fixture.Admin.QueryRow(ctx, `
SELECT a.policy_account_number, a.registered_premium::text,
    a.registered_cover_from::text, min(e.amount)::text, count(e.id)::integer
FROM public.policy_account AS a
JOIN public.accounting_entry AS e ON e.policy_account_id = a.id
WHERE a.policy_number = $1
GROUP BY a.id, a.policy_account_number, a.registered_premium, a.registered_cover_from`,
		policyNumber,
	).Scan(
		&result.accountNumber,
		&result.premium,
		&result.coverageStart,
		&result.amount,
		&result.entries,
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertRegistrationCounts(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	policyNumber string,
	wantAccounts int,
	wantEntries int,
) {
	t.Helper()
	var accounts, entries int
	err := fixture.Admin.QueryRow(ctx, `
SELECT count(DISTINCT a.id)::integer, count(e.id)::integer
FROM public.policy_account AS a
LEFT JOIN public.accounting_entry AS e ON e.policy_account_id = a.id
WHERE a.policy_number = $1`, policyNumber).Scan(&accounts, &entries)
	if err != nil {
		t.Fatal(err)
	}
	if accounts != wantAccounts || entries != wantEntries {
		t.Fatalf(
			"registration counts = (%d, %d), want (%d, %d)",
			accounts,
			entries,
			wantAccounts,
			wantEntries,
		)
	}
}

func assertPrivateReads(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	accountNumber string,
) {
	t.Helper()
	businessDate := time.Date(2025, time.December, 31, 0, 0, 0, 0, time.UTC)
	reads, err := application.NewAccountReads(store, func() time.Time { return businessDate })
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := reads.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, account := range accounts {
		if account.PolicyAccountNumber == accountNumber {
			found = true
		}
	}
	if !found {
		t.Fatal("registered account is missing from private reads")
	}
	before, err := reads.Balance(ctx, accountNumber)
	if err != nil || before.Balance != "0.00" {
		t.Fatalf("balance before coverage = (%s, %v)", before.Balance, err)
	}
	businessDate = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	effective, err := reads.Balance(ctx, accountNumber)
	if err != nil || effective.Balance != "-120.00" {
		t.Fatalf("balance at coverage = (%s, %v)", effective.Balance, err)
	}
}
