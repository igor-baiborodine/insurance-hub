package kafka

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
)

func TestPublisher_RunPreservesOrderAndTraceContext(t *testing.T) {
	// given
	traceParent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	store := &fakeIntentStore{intents: []application.EventIntent{
		{
			ID: [16]byte{1}, Topic: "policy-registered", Key: "policy-1",
			Payload:     []byte(`{"policy":{"number":"policy-1"}}`),
			TraceParent: traceParent,
		},
		{
			ID: [16]byte{2}, Topic: "policy-terminated", Key: "policy-1",
			Payload: []byte(`{"policy":{"number":"policy-1"}}`),
		},
	}}
	client := &fakeProducerClient{produced: make(chan *kgo.Record, 2)}
	observedTrace := make(chan trace.SpanContext, 2)
	publisher := requirePublisher(t, store, client, func(ctx context.Context, _ string) (
		context.Context,
		func(context.Context, string, error),
	) {
		observedTrace <- trace.SpanContextFromContext(ctx)
		return ctx, func(context.Context, string, error) {}
	})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- publisher.Run(ctx) }()

	// when
	first := receiveRecord(t, client.produced)
	second := receiveRecord(t, client.produced)
	cancel()

	// then
	if err := receiveError(t, result); err != nil {
		t.Fatal(err)
	}
	if first.Topic != "policy-registered" || second.Topic != "policy-terminated" ||
		string(first.Key) != "policy-1" || string(second.Key) != "policy-1" {
		t.Fatalf("produced order = %s/%s", first.Topic, second.Topic)
	}
	spanContext := <-observedTrace
	if !spanContext.IsRemote() ||
		spanContext.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("extracted trace = %v", spanContext)
	}
	if got := header(first, "traceparent"); got != traceParent {
		t.Fatalf("traceparent = %q, want %q", got, traceParent)
	}
	if store.published != 2 {
		t.Fatalf("published markers = %d, want 2", store.published)
	}
}

func TestPublisher_RunRetriesWithoutMarkingFailedIntent(t *testing.T) {
	// given
	store := &fakeIntentStore{
		intents: []application.EventIntent{
			{
				ID: [16]byte{
					1,
				},
				Topic:   "policy-registered",
				Key:     "policy-1",
				Payload: []byte(`{}`),
			},
		},
	}
	client := &fakeProducerClient{
		produceErrors: []error{errors.New("synthetic broker outage")},
		produced:      make(chan *kgo.Record, 2),
	}
	publisher := requirePublisher(t, store, client, nil)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- publisher.Run(ctx) }()

	// when
	first := receiveRecord(t, client.produced)
	second := receiveRecord(t, client.produced)
	cancel()

	// then
	if err := receiveError(t, result); err != nil {
		t.Fatal(err)
	}
	if first.Topic != second.Topic || store.attempts != 2 || store.published != 1 {
		t.Fatalf(
			"retry = topics %q/%q attempts %d markers %d",
			first.Topic,
			second.Topic,
			store.attempts,
			store.published,
		)
	}
}

func TestPublisher_RejectsUnknownTopicBeforeBroker(t *testing.T) {
	// given
	store := &fakeIntentStore{intents: []application.EventIntent{{Topic: "foreign"}}}
	client := &fakeProducerClient{produced: make(chan *kgo.Record, 1)}
	publisher := requirePublisher(t, store, client, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// when
	published, err := store.PublishNext(ctx, publisher.publish)

	// then
	if published || err == nil || store.published != 0 {
		t.Fatalf("unknown topic = (%v, %v), markers %d", published, err, store.published)
	}
	select {
	case record := <-client.produced:
		t.Fatalf("unexpected record: %v", record)
	default:
	}
}

func TestPublisher_ShutdownFlushesAndClosesOnce(t *testing.T) {
	// given
	client := &fakeProducerClient{}
	publisher := requirePublisher(t, &fakeIntentStore{}, client, nil)

	// when
	first := publisher.Shutdown(t.Context())
	second := publisher.Shutdown(t.Context())

	// then
	if first != nil || second != nil || client.flushes != 2 || client.closes != 1 {
		t.Fatalf(
			"shutdown = (%v, %v), flushes %d closes %d",
			first,
			second,
			client.flushes,
			client.closes,
		)
	}
}

func TestNewPublisher_RejectsEmptyCACertificate(t *testing.T) {
	// given
	settings := testSettings()
	settings.CACert = []byte(" \n\t")

	// when
	publisher, err := NewPublisher(
		settings,
		&fakeIntentStore{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	// then
	if publisher != nil || err == nil {
		t.Fatalf("NewPublisher() = (%v, %v)", publisher, err)
	}
}

func requirePublisher(
	t *testing.T,
	store application.EventIntentStore,
	client producerClient,
	observe func(context.Context, string) (
		context.Context,
		func(context.Context, string, error),
	),
) *Publisher {
	t.Helper()
	settings := testSettings()
	settings.Observe = observe
	publisher, err := newPublisher(
		settings,
		store,
		client,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return publisher
}

func testSettings() Settings {
	return Settings{
		Brokers: []string{"kafka.example:9093"}, ClientID: "policy-service",
		RegisteredTopic: "policy-registered", TerminatedTopic: "policy-terminated",
		PublishTimeout: time.Second, Propagator: propagation.TraceContext{},
	}
}

type fakeIntentStore struct {
	mu        sync.Mutex
	intents   []application.EventIntent
	attempts  int
	published int
}

func (store *fakeIntentStore) PublishNext(
	ctx context.Context,
	publish func(context.Context, application.EventIntent) error,
) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.intents) == 0 {
		return false, nil
	}
	store.attempts++
	if err := publish(ctx, store.intents[0]); err != nil {
		return false, err
	}
	store.intents = store.intents[1:]
	store.published++
	return true, nil
}

type fakeProducerClient struct {
	mu            sync.Mutex
	produceErrors []error
	produced      chan *kgo.Record
	flushes       int
	closes        int
}

func (*fakeProducerClient) Ping(context.Context) error { return nil }
func (client *fakeProducerClient) Produce(_ context.Context, record *kgo.Record) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.produced != nil {
		client.produced <- record
	}
	if len(client.produceErrors) == 0 {
		return nil
	}
	err := client.produceErrors[0]
	client.produceErrors = client.produceErrors[1:]
	return err
}

func (client *fakeProducerClient) Flush(context.Context) error {
	client.flushes++
	return nil
}
func (client *fakeProducerClient) Close() { client.closes++ }

func receiveRecord(t *testing.T, records <-chan *kgo.Record) *kgo.Record {
	t.Helper()
	select {
	case record := <-records:
		return record
	case <-time.After(2 * time.Second):
		t.Fatal("record was not produced")
		return nil
	}
}

func receiveError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("publisher did not stop")
		return nil
	}
}

func header(record *kgo.Record, name string) string {
	for _, item := range record.Headers {
		if item.Key == name {
			return string(item.Value)
		}
	}
	return ""
}
