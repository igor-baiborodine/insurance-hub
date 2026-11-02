package kafka

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/application"
)

func TestWorker_SkipsMalformedAndStopsFailedFetchedBatch(t *testing.T) {
	// given
	consumer := &fakeClient{polls: []pollResult{{records: []*kgo.Record{
		{Value: []byte(`{"policy":`)},
		{
			Value: []byte(
				`{"policy":{"number":"SYN-230-001","from":"2026-01-01","to":"2026-12-31","policyHolder":"Synthetic Holder A"}}`,
			),
		},
		{Value: []byte(`{"policy":{"number":"must-not-run"}}`)},
	}}}}
	projection := &fakeProjection{err: errors.New("writer unavailable")}
	var logs bytes.Buffer
	worker := newWorker(
		consumer,
		projection,
		slog.New(slog.NewTextHandler(&logs, nil)),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	// when
	go func() { done <- worker.Run(ctx) }()
	waitForCalls(t, projection, 1)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// then
	if got := projection.events(); len(got) != 1 || got[0].Number != "SYN-230-001" {
		t.Fatalf("projected events = %+v", got)
	}
	if consumer.allowCount() == 0 {
		t.Fatal("worker did not release a blocked rebalance")
	}
	if bytes.Contains(logs.Bytes(), []byte("Synthetic Holder A")) ||
		bytes.Contains(logs.Bytes(), []byte("SYN-230-001")) {
		t.Fatalf("diagnostic exposed event payload: %s", logs.String())
	}
}

func TestWorker_ShutdownCommitsThenCloses(t *testing.T) {
	// given
	consumer := &fakeClient{}
	worker := newWorker(consumer, &fakeProjection{}, slog.Default())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// when
	err := worker.Shutdown(ctx)
	// then
	if err != nil {
		t.Fatal(err)
	}
	consumer.mu.Lock()
	defer consumer.mu.Unlock()
	if !consumer.committed || !consumer.closed || consumer.allowed == 0 {
		t.Fatalf(
			"shutdown state = committed:%t allowed:%d closed:%t",
			consumer.committed,
			consumer.allowed,
			consumer.closed,
		)
	}
}

func TestWorker_ExtractsTraceContextAndRecordsBoundedOutcome(t *testing.T) {
	// given
	consumer := &fakeClient{polls: []pollResult{{records: []*kgo.Record{{
		Headers: []kgo.RecordHeader{
			{
				Key: "traceparent",
				Value: []byte(
					"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
				),
			},
		},
		Value: []byte(`{"policy":{"number":"SYN-230-TRACE","policyHolder":"Synthetic"}}`),
	}}}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	projected := make(chan trace.SpanContext, 1)
	finished := make(chan string, 1)
	worker := newWorker(
		consumer,
		projectionFunc(func(ctx context.Context, _ application.PolicyEvent) error {
			projected <- trace.SpanContextFromContext(ctx)
			cancel()
			return nil
		}),
		slog.Default(),
	)
	worker.propagator = propagation.TraceContext{}
	worker.observe = func(ctx context.Context, eventType string) (context.Context, func(context.Context, string, error)) {
		if eventType != "registered" {
			t.Fatalf("event type = %q", eventType)
		}
		return ctx, func(_ context.Context, outcome string, _ error) { finished <- outcome }
	}

	// when
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}

	// then
	spanContext := <-projected
	if !spanContext.IsRemote() ||
		spanContext.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("extracted span context = %v", spanContext)
	}
	if outcome := <-finished; outcome != "success" {
		t.Fatalf("outcome = %q", outcome)
	}
}

func TestNewWorker_RejectsEmptyCACertificate(t *testing.T) {
	// given
	settings := Settings{
		Brokers: []string{"kafka.example:9093"}, Group: "issue-230",
		Topic: "policy-registered-issue-230", ClientID: "policy-registered-listener",
		EventType: "registered", CACert: []byte(" \n\t"),
	}

	// when
	worker, err := NewWorker(settings, &fakeProjection{}, slog.Default())

	// then
	if worker != nil || err == nil || !strings.Contains(err.Error(), "empty CA certificate") {
		t.Fatalf("NewWorker() = (%v, %v)", worker, err)
	}
}

func TestDecode_PreservesOptionalDatesAndRejectsInvalidDate(t *testing.T) {
	// given / when
	got, err := decode(
		[]byte(
			`{"policy":{"number":"P1","from":null,"to":"2026-06-30","policyHolder":"Holder"}}`,
		),
	)

	// then
	if err != nil || got.From != nil || got.To == nil || got.To.String() != "2026-06-30" {
		t.Fatalf("decoded event = %+v, error = %v", got, err)
	}
	if _, err := decode([]byte(`{"policy":{"from":"06/30/2026"}}`)); err == nil {
		t.Fatal("invalid date accepted")
	}
}

type fakeClient struct {
	mu        sync.Mutex
	polls     []pollResult
	allowed   int
	committed bool
	closed    bool
}

func (client *fakeClient) Poll(ctx context.Context, _ int) pollResult {
	client.mu.Lock()
	if len(client.polls) > 0 {
		result := client.polls[0]
		client.polls = client.polls[1:]
		client.mu.Unlock()
		return result
	}
	client.mu.Unlock()
	<-ctx.Done()
	return pollResult{errors: []error{ctx.Err()}}
}

func (*fakeClient) Ping(context.Context) error { return nil }
func (client *fakeClient) AllowRebalance() {
	client.mu.Lock()
	client.allowed++
	client.mu.Unlock()
}

func (client *fakeClient) CommitUncommittedOffsets(context.Context) error {
	client.mu.Lock()
	client.committed = true
	client.mu.Unlock()
	return nil
}

func (client *fakeClient) CloseAllowingRebalance() {
	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()
}

func (client *fakeClient) allowCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.allowed
}

type fakeProjection struct {
	mu        sync.Mutex
	projected []application.PolicyEvent
	err       error
}

type projectionFunc func(context.Context, application.PolicyEvent) error

func (function projectionFunc) ProjectPolicy(
	ctx context.Context,
	event application.PolicyEvent,
) error {
	return function(ctx, event)
}

func (projection *fakeProjection) ProjectPolicy(
	_ context.Context,
	event application.PolicyEvent,
) error {
	projection.mu.Lock()
	projection.projected = append(projection.projected, event)
	projection.mu.Unlock()
	return projection.err
}

func (projection *fakeProjection) events() []application.PolicyEvent {
	projection.mu.Lock()
	defer projection.mu.Unlock()
	return append([]application.PolicyEvent(nil), projection.projected...)
}

func waitForCalls(t *testing.T, projection *fakeProjection, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(projection.events()) >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("projection calls = %d, want %d", len(projection.events()), count)
}
