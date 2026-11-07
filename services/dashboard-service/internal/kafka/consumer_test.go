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

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
)

func TestWorker_SkipsMalformedAndStopsFailedFetchedBatch(t *testing.T) {
	// given
	consumer := &fakeClient{polls: []pollResult{{records: []*kgo.Record{
		{Value: []byte(`{"policy":`)},
		{
			Value: []byte(
				`{"policy":{"number":"SYN-290-001","from":[2026,1,1],"to":[2026,12,31],"policyHolder":"Synthetic Holder A","productCode":"HSI","totalPremium":123.456700,"agentLogin":null}}`,
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
	if got := projection.events(); len(got) != 1 || got[0].Number != "SYN-290-001" {
		t.Fatalf("projected events = %+v", got)
	}
	if consumer.allowCount() == 0 {
		t.Fatal("worker did not release a blocked rebalance")
	}
	if bytes.Contains(logs.Bytes(), []byte("Synthetic Holder A")) ||
		bytes.Contains(logs.Bytes(), []byte("SYN-290-001")) {
		t.Fatalf("diagnostic exposed event payload: %s", logs.String())
	}
}

func TestWorker_MissingPolicyStopsFetchedBatch(t *testing.T) {
	for name, payload := range map[string]string{
		"missing policy": `{}`,
		"null policy":    `{"policy":null}`,
		"missing number": `{"policy":{"productCode":"HSI"}}`,
		"null number":    `{"policy":{"number":null}}`,
		"empty number":   `{"policy":{"number":""}}`,
	} {
		t.Run(name, func(t *testing.T) {
			// given
			consumer := &fakeClient{polls: []pollResult{{records: []*kgo.Record{
				{Value: []byte(payload)},
				{Value: []byte(`{"policy":{"number":"must-not-run"}}`)},
			}}}}
			projection := &fakeProjection{}
			worker := newWorker(consumer, projection, slog.Default())
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)

			// when
			go func() { done <- worker.Run(ctx) }()
			waitForAllows(t, consumer, 1)
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}

			// then
			if got := projection.events(); len(got) != 0 {
				t.Fatalf("projected events = %+v", got)
			}
		})
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

func TestWorker_ShutdownBoundsCommitAndCloseFailures(t *testing.T) {
	// given
	release := make(chan struct{})
	commitFailure := errors.New("synthetic commit failure")
	consumer := &fakeClient{commitErr: commitFailure, closeRelease: release}
	worker := newWorker(consumer, &fakeProjection{}, slog.Default())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	// when
	err := worker.Shutdown(ctx)
	close(release)

	// then
	if !errors.Is(err, commitFailure) || !errors.Is(err, context.DeadlineExceeded) ||
		consumer.allowCount() == 0 {
		t.Fatalf(
			"bounded shutdown error = %v, rebalance releases = %d",
			err,
			consumer.allowCount(),
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
		Value: []byte(`{"policy":{"number":"SYN-290-TRACE","policyHolder":"Synthetic"}}`),
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
		Brokers:   []string{"kafka.example:9093"},
		Group:     "issue-290",
		Topic:     "policy-registered-issue-290",
		ClientID:  "policy-registered-dashboard-listener",
		EventType: "registered",
		CACert:    []byte(" \n\t"),
	}

	// when
	worker, err := NewWorker(settings, &fakeProjection{}, slog.Default())

	// then
	if worker != nil || err == nil || !strings.Contains(err.Error(), "empty CA certificate") {
		t.Fatalf("NewWorker() = (%v, %v)", worker, err)
	}
}

func TestWorker_CheckRequiresStableAssignment(t *testing.T) {
	// given
	worker := newWorker(&fakeClient{}, &fakeProjection{}, slog.Default())
	worker.assigned.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	go func() {
		time.Sleep(20 * time.Millisecond)
		worker.assigned.Store(true)
		time.Sleep(50 * time.Millisecond)
		worker.assigned.Store(false)
		time.Sleep(20 * time.Millisecond)
		worker.assigned.Store(true)
	}()

	// when
	err := worker.Check(ctx)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 300*time.Millisecond {
		t.Fatalf("readiness returned after unstable assignment in %s", elapsed)
	}
}

func TestDecode_PreservesSevenReceivedValues(t *testing.T) {
	// given / when
	got, err := decode(
		[]byte(
			`{"policy":{"number":29001,"from":null,"to":[2026,6,30],"policyHolder":"","productCode":"HSI","totalPremium":"9007199254740993.1234567","agentLogin":null,"ignored":"value"}}`,
		),
	)

	// then
	if err != nil || got.Number != "29001" || got.From != nil || got.To == nil ||
		got.To.String() != "2026-06-30" || got.PolicyHolder == nil ||
		*got.PolicyHolder != "" || got.ProductCode == nil || *got.ProductCode != "HSI" ||
		got.TotalPremium == nil || got.TotalPremium.String() != "9007199254740993.1234567" ||
		got.AgentLogin != nil {
		t.Fatalf("decoded event = %+v, error = %v", got, err)
	}
	for name, payload := range map[string]string{
		"string date":     `{"policy":{"number":"P1","from":"2026-01-01"}}`,
		"invalid date":    `{"policy":{"number":"P1","from":[2026,2,30]}}`,
		"short date":      `{"policy":{"number":"P1","from":[2026,1]}}`,
		"invalid premium": `{"policy":{"number":"P1","totalPremium":1e400}}`,
		"object number":   `{"policy":{"number":{}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decode([]byte(payload)); err == nil {
				t.Fatalf("invalid payload accepted: %s", payload)
			}
		})
	}
}

type fakeClient struct {
	mu           sync.Mutex
	polls        []pollResult
	allowed      int
	committed    bool
	closed       bool
	commitErr    error
	closeRelease <-chan struct{}
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
	return client.commitErr
}

func (client *fakeClient) CloseAllowingRebalance() {
	if client.closeRelease != nil {
		<-client.closeRelease
	}
	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()
}

func (client *fakeClient) allowCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.allowed
}

func waitForAllows(t *testing.T, client *fakeClient, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if client.allowCount() >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("rebalance releases = %d, want %d", client.allowCount(), count)
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
