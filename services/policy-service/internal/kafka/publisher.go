// Package kafka publishes durable Policy outbox intents.
package kafka

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
)

const retryInterval = 250 * time.Millisecond

// Settings captures the explicit Policy producer contract.
type Settings struct {
	Brokers         []string
	ClientID        string
	RegisteredTopic string
	TerminatedTopic string
	CACert          []byte
	PublishTimeout  time.Duration
	Propagator      propagation.TextMapPropagator
	Observe         func(context.Context, string) (
		context.Context,
		func(context.Context, string, error),
	)
}

type producerClient interface {
	Ping(context.Context) error
	Produce(context.Context, *kgo.Record) error
	Flush(context.Context) error
	Close()
}

type franzClient struct{ client *kgo.Client }

func (client franzClient) Ping(ctx context.Context) error { return client.client.Ping(ctx) }
func (client franzClient) Produce(ctx context.Context, record *kgo.Record) error {
	return client.client.ProduceSync(ctx, record).FirstErr()
}
func (client franzClient) Flush(ctx context.Context) error { return client.client.Flush(ctx) }
func (client franzClient) Close()                          { client.client.Close() }

// Publisher owns the broker client and drains eligible outbox intents until cancellation.
type Publisher struct {
	settings Settings
	store    application.EventIntentStore
	client   producerClient
	logger   *slog.Logger
	close    sync.Once
}

// NewPublisher constructs the TLS producer without contacting the broker.
func NewPublisher(
	settings Settings,
	store application.EventIntentStore,
	logger *slog.Logger,
) (*Publisher, error) {
	if err := validateSettings(settings, store, logger); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(settings.CACert)) == 0 {
		return nil, errors.New("configure Policy event publisher: empty CA certificate")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("configure Policy event publisher: load system roots")
	}
	if !roots.AppendCertsFromPEM(settings.CACert) {
		return nil, errors.New("configure Policy event publisher: invalid CA certificate")
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(settings.Brokers...),
		kgo.ClientID(settings.ClientID),
		kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		// Uncompressed batches stay readable by consumers without native codec libraries.
		kgo.ProducerBatchCompression(kgo.NoCompression()),
	)
	if err != nil {
		return nil, errors.New("configure Policy event publisher: create Kafka client")
	}
	return newPublisher(settings, store, franzClient{client: client}, logger)
}

func newPublisher(
	settings Settings,
	store application.EventIntentStore,
	client producerClient,
	logger *slog.Logger,
) (*Publisher, error) {
	if err := validateSettings(settings, store, logger); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("configure Policy event publisher: client is required")
	}
	return &Publisher{settings: settings, store: store, client: client, logger: logger}, nil
}

func validateSettings(
	settings Settings,
	store application.EventIntentStore,
	logger *slog.Logger,
) error {
	if len(settings.Brokers) == 0 || settings.ClientID == "" ||
		settings.RegisteredTopic == "" || settings.TerminatedTopic == "" ||
		settings.RegisteredTopic == settings.TerminatedTopic ||
		settings.PublishTimeout <= 0 || store == nil || logger == nil {
		return errors.New("configure Policy event publisher: incomplete settings")
	}
	return nil
}

// Preflight verifies the configured TLS broker before the service becomes ready.
func (publisher *Publisher) Preflight(ctx context.Context) error {
	if publisher == nil || publisher.client == nil {
		return errors.New("preflight Policy event publisher: publisher is required")
	}
	if err := publisher.client.Ping(ctx); err != nil {
		return errors.New("preflight Policy event publisher: Kafka unavailable")
	}
	return nil
}

// Check verifies broker availability for readiness without exposing broker details.
func (publisher *Publisher) Check(ctx context.Context) error {
	if publisher == nil || publisher.client == nil {
		return errors.New("check Policy event publisher: publisher is required")
	}
	if err := publisher.client.Ping(ctx); err != nil {
		return errors.New("check Policy event publisher: Kafka unavailable")
	}
	return nil
}

// Run publishes pending intents and retries bounded attempts until cancellation.
func (publisher *Publisher) Run(ctx context.Context) error {
	if publisher == nil || publisher.store == nil || publisher.client == nil {
		return errors.New("run Policy event publisher: publisher is required")
	}
	for {
		published, err := publisher.store.PublishNext(ctx, publisher.publish)
		if err != nil && ctx.Err() == nil {
			publisher.logger.WarnContext(
				ctx,
				"policy event publication deferred",
				slog.String("reason", "publish_unavailable"),
			)
		}
		if ctx.Err() != nil {
			return nil
		}
		if !published || err != nil {
			timer := time.NewTimer(retryInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}
}

func (publisher *Publisher) publish(
	ctx context.Context,
	intent application.EventIntent,
) error {
	eventType, ok := publisher.eventType(intent.Topic)
	if !ok {
		return errors.New("publish Policy event intent: topic is not configured")
	}
	carrier := propagation.MapCarrier{}
	if intent.TraceParent != "" {
		carrier.Set("traceparent", intent.TraceParent)
	}
	if intent.TraceState != "" {
		carrier.Set("tracestate", intent.TraceState)
	}
	if publisher.settings.Propagator != nil {
		ctx = publisher.settings.Propagator.Extract(ctx, carrier)
	}
	finish := func(context.Context, string, error) {}
	if publisher.settings.Observe != nil {
		ctx, finish = publisher.settings.Observe(ctx, eventType)
	}
	recordCarrier := propagation.MapCarrier{}
	if publisher.settings.Propagator != nil {
		publisher.settings.Propagator.Inject(ctx, recordCarrier)
	}
	record := &kgo.Record{
		Topic: intent.Topic,
		Key:   []byte(intent.Key),
		Value: intent.Payload,
	}
	for _, name := range []string{"traceparent", "tracestate"} {
		if value := recordCarrier.Get(name); value != "" {
			record.Headers = append(
				record.Headers,
				kgo.RecordHeader{Key: name, Value: []byte(value)},
			)
		}
	}
	publishCtx, cancel := context.WithTimeout(ctx, publisher.settings.PublishTimeout)
	err := publisher.client.Produce(publishCtx, record)
	cancel()
	outcome := "success"
	if err != nil {
		outcome = "error"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = "cancelled"
		}
		err = fmt.Errorf("publish Policy %s event: %w", eventType, err)
	}
	finish(ctx, outcome, err)
	return err
}

func (publisher *Publisher) eventType(topic string) (string, bool) {
	switch topic {
	case publisher.settings.RegisteredTopic:
		return "registered", true
	case publisher.settings.TerminatedTopic:
		return "terminated", true
	default:
		return "unknown", false
	}
}

// Shutdown flushes acknowledged work within the caller's deadline, then closes the client once.
func (publisher *Publisher) Shutdown(ctx context.Context) error {
	if publisher == nil || publisher.client == nil {
		return nil
	}
	err := publisher.client.Flush(ctx)
	publisher.close.Do(publisher.client.Close)
	if err != nil {
		return fmt.Errorf("flush Policy event publisher: %w", err)
	}
	return nil
}
