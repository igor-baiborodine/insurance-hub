//go:build integration

package kafka

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/jsreport"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/objectstore"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/postgres"
)

func TestEventWorkerIntegration_ConsumesDuplicateAndResumesAfterMalformedRecord(t *testing.T) {
	// given
	broker := requiredIntegrationSetting(t, "DOCUMENT_TEST_KAFKA_BROKERS")
	topic := requiredIntegrationSetting(t, "DOCUMENT_TEST_KAFKA_TOPIC")
	group := requiredIntegrationSetting(t, "DOCUMENT_TEST_KAFKA_GROUP")
	caCertificate, err := os.ReadFile(
		requiredIntegrationSetting(t, "DOCUMENT_TEST_KAFKA_CA_FILE"),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	producer, err := kgo.NewClient(integrationTLSOptions(t, broker, caCertificate)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	waitForIntegrationKafka(t, ctx, producer)
	createIntegrationTopic(t, ctx, producer, topic)
	initializeIntegrationProgress(t, ctx, producer, topic, group)

	renderer := &integrationRenderer{}
	uploader := &integrationUploader{}
	metadata := &integrationMetadata{written: make(chan struct{}, 4)}
	generation, err := application.NewGenerateDocument(renderer, uploader, metadata)
	if err != nil {
		t.Fatal(err)
	}
	settings := Settings{
		Brokers: []string{broker}, Group: group, Topic: topic,
		ClientID: "policy-registered-document-listener", CACert: caCertificate,
		EventType: "registered",
	}
	worker := newIntegrationWorker(t, ctx, settings, generation)
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Run(workerCtx) }()
	checkIntegrationWorker(t, ctx, worker)

	// when
	produceIntegrationRecord(t, ctx, producer, topic, "SYN-363", validRegistration)
	produceIntegrationRecord(t, ctx, producer, topic, "SYN-363", validRegistration)
	waitForIntegrationWrites(t, ctx, metadata.written, 2)
	stopIntegrationWorker(t, cancelWorker, workerResult, worker)

	restarted := newIntegrationWorker(t, ctx, settings, generation)
	restartCtx, cancelRestart := context.WithCancel(ctx)
	restartResult := make(chan error, 1)
	go func() { restartResult <- restarted.Run(restartCtx) }()
	checkIntegrationWorker(t, ctx, restarted)
	produceIntegrationRecord(t, ctx, producer, topic, "OTHER", validRegistration)
	produceIntegrationRecord(t, ctx, producer, topic, "SYN-363", validRegistration)
	waitForIntegrationWrites(t, ctx, metadata.written, 1)
	stopIntegrationWorker(t, cancelRestart, restartResult, restarted)

	// then
	wantKeys := []string{
		"2026/02/SYN-363.pdf",
		"2026/02/SYN-363.pdf",
		"2026/02/SYN-363.pdf",
	}
	if renderer.count() != 3 || !slices.Equal(uploader.keys(), wantKeys) {
		t.Fatalf(
			"generation effects = renders:%d uploads:%v",
			renderer.count(),
			uploader.keys(),
		)
	}
	if got := metadata.values(); len(got) != 3 {
		t.Fatalf("metadata writes = %v", got)
	}
	for _, value := range metadata.values() {
		if value != "SYN-363:2026/02/SYN-363.pdf" {
			t.Fatalf("metadata write = %q", value)
		}
	}
}

func TestEventWorkerIntegration_GeneratesRetrievableDocument(t *testing.T) {
	// This fixture uses an isolated group supplied only to the worker under test. Config loading
	// still validates the ordinary serving coordinates without acquiring that ordinary group.
	configured, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	broker := requiredIntegrationSetting(t, "DOCUMENT_TEST_KAFKA_BROKERS")
	topic := requiredIntegrationSetting(t, "DOCUMENT_TEST_KAFKA_TOPIC")
	group := requiredIntegrationSetting(t, "DOCUMENT_TEST_KAFKA_GROUP")
	caCertificate, err := os.ReadFile(
		requiredIntegrationSetting(t, "DOCUMENT_TEST_KAFKA_CA_FILE"),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	writerPool, err := pgxpool.New(ctx, configured.Events.WriterDatabase.URL.Value())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writerPool.Close)
	renderer, err := jsreport.NewClient(jsreport.Settings{
		Origin: configured.Events.JSReportOrigin, ConnectTimeout: 2 * time.Second,
		RequestTimeout: 8 * time.Second, MaximumBytes: configured.MaximumDocumentBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(renderer.Close)
	uploader, err := objectstore.NewWriter(
		configured.Events.WriterObjectStore,
		configured.Events.UploadTimeout,
		configured.MaximumDocumentBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(uploader.Close)
	metadata, err := postgres.NewWriter(
		writerPool,
		configured.Events.WriterDatabase.AcquireTimeout,
		configured.Events.WriterDatabase.QueryTimeout,
	)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := application.NewGenerateDocument(renderer, uploader, metadata)
	if err != nil {
		t.Fatal(err)
	}

	producer, err := kgo.NewClient(integrationTLSOptions(t, broker, caCertificate)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(producer.Close)
	waitForIntegrationKafka(t, ctx, producer)
	createIntegrationTopic(t, ctx, producer, topic)
	initializeIntegrationProgress(t, ctx, producer, topic, group)
	worker := newIntegrationWorker(t, ctx, Settings{
		Brokers: []string{broker}, Group: group, Topic: topic,
		ClientID: "policy-registered-document-listener", CACert: caCertificate,
		EventType: "registered",
	}, generation)
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Run(workerCtx) }()
	checkIntegrationWorker(t, ctx, worker)

	produceIntegrationRecord(t, ctx, producer, topic, "SYN-363", validRegistration)
	waitForGeneratedDocument(t, ctx, configured)
	stopIntegrationWorker(t, cancelWorker, workerResult, worker)
}

func waitForGeneratedDocument(t *testing.T, ctx context.Context, configured config.Config) {
	t.Helper()
	readerPool, err := pgxpool.New(ctx, configured.Database.URL.Value())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(readerPool.Close)
	metadata, err := postgres.NewReader(
		readerPool,
		configured.Database.AcquireTimeout,
		configured.Database.QueryTimeout,
		configured.MaximumDocumentBytes,
		configured.MaximumResponseBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := objectstore.NewReader(
		configured.ObjectStore,
		configured.MaximumDocumentBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(contents.Close)
	lookup, err := application.NewFindDocuments(
		metadata,
		contents,
		configured.MaximumResponseBytes,
		configured.MaximumConcurrentLookups,
	)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		result, lookupErr := lookup.Execute(ctx, "SYN-363")
		if lookupErr == nil && len(result.Documents) == 1 &&
			bytes.HasPrefix(result.Documents[0].Content, []byte("%PDF-")) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("generated document did not become retrievable")
}

func newIntegrationWorker(
	t *testing.T,
	ctx context.Context,
	settings Settings,
	generation *application.GenerateDocument,
) *Worker {
	t.Helper()
	worker, err := NewWorker(
		settings,
		generation,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	preflightCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := worker.Preflight(preflightCtx); err != nil {
		t.Fatalf("worker preflight: %v", err)
	}
	return worker
}

func checkIntegrationWorker(t *testing.T, ctx context.Context, worker *Worker) {
	t.Helper()
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := worker.Check(checkCtx); err != nil {
		t.Fatalf("worker readiness: %v", err)
	}
}

func stopIntegrationWorker(
	t *testing.T,
	cancel context.CancelFunc,
	result <-chan error,
	worker *Worker,
) {
	t.Helper()
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("worker run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := worker.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("worker shutdown: %v", err)
	}
}

func requiredIntegrationSetting(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func integrationTLSOptions(t *testing.T, broker string, caCertificate []byte) []kgo.Opt {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caCertificate) {
		t.Fatal("append fixture Kafka CA")
	}
	return []kgo.Opt{
		kgo.SeedBrokers(broker),
		kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}),
	}
}

func waitForIntegrationKafka(t *testing.T, ctx context.Context, client *kgo.Client) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := client.Ping(attemptCtx)
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatalf("Kafka did not become ready: %T", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func createIntegrationTopic(t *testing.T, ctx context.Context, client *kgo.Client, topic string) {
	t.Helper()
	request := kmsg.NewPtrCreateTopicsRequest()
	request.TimeoutMillis = 30_000
	request.Topics = append(request.Topics, kmsg.CreateTopicsRequestTopic{
		Topic: topic, NumPartitions: 1, ReplicationFactor: 1,
	})
	response, err := client.Request(ctx, request)
	created, ok := response.(*kmsg.CreateTopicsResponse)
	if err != nil || !ok || len(created.Topics) != 1 || created.Topics[0].ErrorCode != 0 {
		t.Fatalf("create event topic = (%T, %v)", response, err)
	}
}

func initializeIntegrationProgress(
	t *testing.T,
	ctx context.Context,
	client *kgo.Client,
	topic string,
	group string,
) {
	t.Helper()
	partition := kmsg.NewOffsetCommitRequestTopicPartition()
	partition.Partition = 0
	partition.Offset = 0
	requestTopic := kmsg.NewOffsetCommitRequestTopic()
	requestTopic.Topic = topic
	requestTopic.Partitions = append(requestTopic.Partitions, partition)
	request := kmsg.NewPtrOffsetCommitRequest()
	request.Group = group
	request.Topics = append(request.Topics, requestTopic)
	response, err := client.Request(ctx, request)
	committed, ok := response.(*kmsg.OffsetCommitResponse)
	if err != nil || !ok || len(committed.Topics) != 1 ||
		len(committed.Topics[0].Partitions) != 1 ||
		committed.Topics[0].Partitions[0].ErrorCode != 0 {
		t.Fatalf("initialize event progress = (%T, %v)", response, err)
	}
}

func produceIntegrationRecord(
	t *testing.T,
	ctx context.Context,
	producer *kgo.Client,
	topic string,
	key string,
	value string,
) {
	t.Helper()
	result := producer.ProduceSync(ctx, &kgo.Record{
		Topic: topic, Key: []byte(key), Value: []byte(value),
	})
	if err := result.FirstErr(); err != nil {
		t.Fatalf("produce event: %v", err)
	}
}

func waitForIntegrationWrites(
	t *testing.T,
	ctx context.Context,
	written <-chan struct{},
	count int,
) {
	t.Helper()
	for range count {
		select {
		case <-written:
		case <-ctx.Done():
			t.Fatal("generation effect not observed")
		}
	}
}

type integrationRenderer struct {
	mu      sync.Mutex
	renders int
}

func (renderer *integrationRenderer) Render(
	context.Context,
	domain.PolicyRegistration,
) ([]byte, error) {
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	renderer.renders++
	return []byte("%PDF-synthetic"), nil
}

func (renderer *integrationRenderer) count() int {
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	return renderer.renders
}

type integrationUploader struct {
	mu       sync.Mutex
	uploaded []string
}

func (uploader *integrationUploader) UploadPDF(
	_ context.Context,
	key string,
	_ []byte,
) error {
	uploader.mu.Lock()
	defer uploader.mu.Unlock()
	uploader.uploaded = append(uploader.uploaded, key)
	return nil
}

func (uploader *integrationUploader) keys() []string {
	uploader.mu.Lock()
	defer uploader.mu.Unlock()
	return append([]string(nil), uploader.uploaded...)
}

type integrationMetadata struct {
	mu      sync.Mutex
	inserts []string
	written chan struct{}
}

func (metadata *integrationMetadata) InsertObjectKey(
	_ context.Context,
	policyNumber string,
	objectKey string,
) error {
	metadata.mu.Lock()
	metadata.inserts = append(metadata.inserts, policyNumber+":"+objectKey)
	metadata.mu.Unlock()
	metadata.written <- struct{}{}
	return nil
}

func (metadata *integrationMetadata) values() []string {
	metadata.mu.Lock()
	defer metadata.mu.Unlock()
	return append([]string(nil), metadata.inserts...)
}
