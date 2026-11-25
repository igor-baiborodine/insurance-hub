//go:build integration

package kafka_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	policykafka "github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/kafka"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/testing/kafkafixture"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/testing/postgresfixture"
)

const (
	registeredTopic = "policy-registered"
	terminatedTopic = "policy-terminated"
	recoveryPolicy  = "policy-recovery"
	traceParent     = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
)

func TestRun_PolicyEventPublisherRecovery(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	database := postgresfixture.New(t, ctx)
	database.Call(t, ctx, "migrate", "version=2")
	pool, err := pgxpool.New(ctx, database.WriterURL("policy"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	broker := kafkafixture.New(t, ctx)
	consumer := newConsumer(t, broker.Address, broker.CACert)
	defer consumer.Close()
	loggerOutput := &lockedBuffer{}
	settings := policykafka.Settings{
		Brokers: []string{broker.Address}, ClientID: "policy-service",
		RegisteredTopic: registeredTopic, TerminatedTopic: terminatedTopic,
		CACert: broker.CACert, PublishTimeout: 2 * time.Second,
		Propagator: propagation.TraceContext{},
	}

	t.Run("invalid target and untrusted CA fail closed", func(t *testing.T) {
		invalid := settings
		invalid.Brokers = []string{"127.0.0.1:1"}
		invalidPublisher := newPublisher(t, invalid, store, io.Discard)
		preflightCtx, preflightCancel := context.WithTimeout(ctx, time.Second)
		defer preflightCancel()
		if err := invalidPublisher.Preflight(preflightCtx); err == nil {
			t.Fatal("preflight accepted an unavailable broker")
		}
		shutdownPublisher(t, invalidPublisher)

		untrusted := settings
		untrusted.CACert, err = x509Cert(database.CA)
		if err != nil {
			t.Fatal(err)
		}
		untrustedPublisher := newPublisher(t, untrusted, store, io.Discard)
		preflightCtx, preflightCancel = context.WithTimeout(ctx, 2*time.Second)
		defer preflightCancel()
		if err := untrustedPublisher.Preflight(preflightCtx); err == nil {
			t.Fatal("preflight trusted an unrelated CA")
		}
		shutdownPublisher(t, untrustedPublisher)
	})

	t.Run("rolled back intent produces no record", func(t *testing.T) {
		publisher := newPublisher(t, settings, store, io.Discard)
		runCtx, runCancel := context.WithCancel(ctx)
		result := runPublisher(publisher, runCtx)
		insertRolledBackIntent(t, ctx, database)
		if records := pollRecords(consumer, 750*time.Millisecond); len(records) != 0 {
			t.Fatalf("rolled back intent produced %d records", len(records))
		}
		stopPublisher(t, publisher, runCancel, result)
	})

	insertRecoveryIntents(t, ctx, database)
	rejectPublishedMarker(t, ctx, database)
	firstPublisher := newPublisher(t, settings, store, io.Discard)
	preflightCtx, preflightCancel := context.WithTimeout(ctx, 5*time.Second)
	if err := firstPublisher.Preflight(preflightCtx); err != nil {
		t.Fatalf("valid publisher preflight: %v", err)
	}
	preflightCancel()
	firstRunCtx, firstRunCancel := context.WithCancel(ctx)
	firstResult := runPublisher(firstPublisher, firstRunCtx)

	// when: Kafka acknowledges registration, but its SQL marker is rejected.
	first := receiveRecord(t, consumer, 20*time.Second)
	firstRunCancel()
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	shutdownPublisher(t, firstPublisher)
	allowPublishedMarker(t, ctx, database)
	if got := pendingCount(t, ctx, database); got != 2 {
		t.Fatalf("pending after ambiguous acknowledgement = %d, want 2", got)
	}
	if err := broker.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	recoveryPublisher := newPublisher(t, settings, store, loggerOutput)
	outageCtx, outageCancel := context.WithTimeout(ctx, time.Second)
	if err := recoveryPublisher.Check(outageCtx); err == nil {
		t.Fatal("publisher health passed during broker outage")
	}
	outageCancel()
	recoveryRunCtx, recoveryRunCancel := context.WithCancel(ctx)
	recoveryResult := runPublisher(recoveryPublisher, recoveryRunCtx)
	waitForLog(t, loggerOutput, "publish_unavailable")
	if bytes.Contains(loggerOutput.Bytes(), []byte("sensitive-holder")) {
		t.Fatal("publisher diagnostics exposed event payload")
	}
	if err := broker.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitForReady(t, ctx, recoveryPublisher)
	recovered := receiveThroughTermination(t, consumer, 30*time.Second)
	recoveryRunCancel()
	if err := <-recoveryResult; err != nil {
		t.Fatal(err)
	}
	shutdownPublisher(t, recoveryPublisher)

	// then
	all := append([]*kgo.Record{first}, recovered...)
	if len(all) < 3 {
		t.Fatalf(
			"physical records = %d, want duplicate registration and termination",
			len(all),
		)
	}
	registeredPayload := []byte(
		`{"policy":{"number":"policy-recovery","policyHolder":"sensitive-holder"}}`,
	)
	terminatedPayload := []byte(`{"policy":{"number":"policy-recovery","to":[2026,6,30]}}`)
	registrations := 0
	for _, record := range all {
		if string(record.Key) != recoveryPolicy {
			t.Fatalf("record key = %q", record.Key)
		}
		switch record.Topic {
		case registeredTopic:
			registrations++
			if !jsonEqual(record.Value, registeredPayload) ||
				!bytes.Equal(record.Value, first.Value) {
				t.Fatalf(
					"registration payload changed across retry: %s",
					record.Value,
				)
			}
		case terminatedTopic:
			if !jsonEqual(record.Value, terminatedPayload) {
				t.Fatalf("termination payload = %s", record.Value)
			}
		default:
			t.Fatalf("unexpected topic %q", record.Topic)
		}
	}
	if registrations < 2 {
		t.Fatalf(
			"registration records = %d, want an at-least-once duplicate",
			registrations,
		)
	}
	if got := header(first, "traceparent"); got != traceParent {
		t.Fatalf("traceparent = %q, want %q", got, traceParent)
	}
	carrier := propagation.MapCarrier{"traceparent": header(first, "traceparent")}
	span := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(ctx, carrier))
	if !span.IsValid() || !span.IsRemote() {
		t.Fatal("trace context was not extractable")
	}
	if got := pendingCount(t, ctx, database); got != 0 {
		t.Fatalf("pending after recovery = %d", got)
	}
}

func newPublisher(
	t *testing.T,
	settings policykafka.Settings,
	store *postgres.Store,
	output io.Writer,
) *policykafka.Publisher {
	t.Helper()
	publisher, err := policykafka.NewPublisher(
		settings,
		store,
		slog.New(slog.NewTextHandler(output, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return publisher
}

func newConsumer(t *testing.T, broker string, ca []byte) *kgo.Client {
	t.Helper()
	roots, err := x509.SystemCertPool()
	if err != nil {
		t.Fatal(err)
	}
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("append disposable Kafka CA")
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(broker),
		kgo.ConsumerGroup("policy-recovery-independent"),
		kgo.ConsumeTopics(registeredTopic, terminatedTopic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func runPublisher(publisher *policykafka.Publisher, ctx context.Context) <-chan error {
	result := make(chan error, 1)
	go func() { result <- publisher.Run(ctx) }()
	return result
}

func stopPublisher(
	t *testing.T,
	publisher *policykafka.Publisher,
	cancel context.CancelFunc,
	result <-chan error,
) {
	t.Helper()
	cancel()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	shutdownPublisher(t, publisher)
}

func shutdownPublisher(t *testing.T, publisher *policykafka.Publisher) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := publisher.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func receiveRecord(t *testing.T, consumer *kgo.Client, timeout time.Duration) *kgo.Record {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		records := pollRecords(consumer, time.Until(deadline))
		if len(records) != 0 {
			return records[0]
		}
	}
	t.Fatal("Kafka record was not observed")
	return nil
}

func receiveThroughTermination(
	t *testing.T,
	consumer *kgo.Client,
	timeout time.Duration,
) []*kgo.Record {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var records []*kgo.Record
	for time.Now().Before(deadline) {
		batch := pollRecords(consumer, time.Until(deadline))
		records = append(records, batch...)
		for _, record := range batch {
			if record.Topic == terminatedTopic {
				return records
			}
		}
	}
	t.Fatal("termination record was not observed")
	return nil
}

func pollRecords(consumer *kgo.Client, timeout time.Duration) []*kgo.Record {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return consumer.PollFetches(ctx).Records()
}

func waitForReady(t *testing.T, ctx context.Context, publisher *policykafka.Publisher) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		checkCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := publisher.Check(checkCtx)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("publisher did not recover after Kafka restart")
}

func waitForLog(t *testing.T, output *lockedBuffer, value string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if bytes.Contains(output.Bytes(), []byte(value)) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("publisher log did not contain %q", value)
}

func insertRolledBackIntent(t *testing.T, ctx context.Context, fixture *postgresfixture.Fixture) {
	t.Helper()
	tx, err := fixture.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var policyID int64
	if err := tx.QueryRow(ctx, "INSERT INTO public.policy (number) VALUES ($1) RETURNING id",
		"rolled-back-policy").Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.policy_outbox
        (intent_id, policy_id, topic, message_key, payload)
        VALUES ($1::uuid, $2, $3, $4, $5::jsonb)`,
		"11111111-1111-4111-8111-111111111111", policyID, registeredTopic,
		"rolled-back-policy", `{"policy":{"number":"rolled-back-policy"}}`); err != nil {
		t.Fatal(err)
	}
}

func insertRecoveryIntents(t *testing.T, ctx context.Context, fixture *postgresfixture.Fixture) {
	t.Helper()
	var policyID int64
	if err := fixture.Admin.QueryRow(ctx,
		"INSERT INTO public.policy (number) VALUES ($1) RETURNING id", recoveryPolicy,
	).Scan(&policyID); err != nil {
		t.Fatal(err)
	}
	for _, intent := range []struct {
		id, topic, payload, traceParent string
	}{
		{
			"22222222-2222-4222-8222-222222222222", registeredTopic,
			`{"policy":{"number":"policy-recovery","policyHolder":"sensitive-holder"}}`,
			traceParent,
		},
		{
			"33333333-3333-4333-8333-333333333333", terminatedTopic,
			`{"policy":{"number":"policy-recovery","to":[2026,6,30]}}`, "",
		},
	} {
		if _, err := fixture.Admin.Exec(
			ctx,
			`INSERT INTO public.policy_outbox
            (intent_id, policy_id, topic, message_key, payload, trace_parent)
            VALUES ($1::uuid, $2, $3, $4, $5::jsonb, NULLIF($6, ''))`,
			intent.id,
			policyID,
			intent.topic,
			recoveryPolicy,
			intent.payload,
			intent.traceParent,
		); err != nil {
			t.Fatal(err)
		}
	}
}

func rejectPublishedMarker(t *testing.T, ctx context.Context, fixture *postgresfixture.Fixture) {
	t.Helper()
	fixture.Exec(t, ctx, `CREATE FUNCTION public.reject_policy_publish_marker() RETURNS trigger
        LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic marker rejection'; END $$;
        CREATE TRIGGER reject_policy_publish_marker BEFORE UPDATE OF published_at
        ON public.policy_outbox FOR EACH ROW EXECUTE FUNCTION public.reject_policy_publish_marker();`)
}

func allowPublishedMarker(t *testing.T, ctx context.Context, fixture *postgresfixture.Fixture) {
	t.Helper()
	fixture.Exec(t, ctx, `DROP TRIGGER reject_policy_publish_marker ON public.policy_outbox;
        DROP FUNCTION public.reject_policy_publish_marker();`)
}

func pendingCount(t *testing.T, ctx context.Context, fixture *postgresfixture.Fixture) int {
	t.Helper()
	var count int
	if err := fixture.Admin.QueryRow(ctx,
		"SELECT count(*) FROM public.policy_outbox WHERE published_at IS NULL",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func header(record *kgo.Record, name string) string {
	for _, header := range record.Headers {
		if header.Key == name {
			return string(header.Value)
		}
	}
	return ""
}

func x509Cert(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return nil, errors.New("empty certificate")
	}
	return content, nil
}

func jsonEqual(first, second []byte) bool {
	var firstValue, secondValue any
	if json.Unmarshal(first, &firstValue) != nil ||
		json.Unmarshal(second, &secondValue) != nil {
		return false
	}
	return reflect.DeepEqual(firstValue, secondValue)
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (buffer *lockedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.Write(value)
}

func (buffer *lockedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return bytes.Clone(buffer.b.Bytes())
}
