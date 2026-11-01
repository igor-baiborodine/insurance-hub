package kafka

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

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
