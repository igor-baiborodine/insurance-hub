//go:build integration

package testing_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	policyelasticsearch "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/elasticsearch"
	policykafka "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/kafka"
)

const (
	eventFixtureOwnership = "policy-search-disposable"
	eventFixtureIndex     = "policy-views"
)

type eventOracle struct {
	SchemaVersion int             `json:"schemaVersion"`
	Ownership     string          `json:"ownership"`
	Resources     oracleResources `json:"resources"`
	Events        oracleEvents    `json:"events"`
	Cases         []oracleCase    `json:"cases"`
}

type oracleResources struct {
	RegisteredTopic   string `json:"registeredTopic"`
	TerminatedTopic   string `json:"terminatedTopic"`
	ConsumerGroup     string `json:"consumerGroup"`
	Index             string `json:"index"`
	SyntheticIDPrefix string `json:"syntheticIdPrefix"`
}

type oracleEvents struct {
	Registration oracleEvent `json:"registration"`
	Termination  oracleEvent `json:"termination"`
}

type oracleEvent struct {
	Key              string            `json:"key"`
	Value            json.RawMessage   `json:"value"`
	ExpectedDocument map[string]string `json:"expectedDocument"`
}

type oracleCase struct {
	ID       string   `json:"id"`
	Sequence []string `json:"sequence"`
	Expected string   `json:"expected"`
}

type eventEnvironment struct {
	Broker          string
	Elasticsearch   string
	RegisteredTopic string
	TerminatedTopic string
	ConsumerGroup   string
	KafkaCA         []byte
}

func TestEventFixture_OracleDefinesIndependentCases(t *testing.T) {
	// given
	oracle := loadEventOracle(t)
	wantCases := []string{
		"C230-01",
		"C230-02",
		"C230-03-duplicate",
		"C230-03-reversed",
		"C230-04-handler-failure",
		"C230-04-malformed",
		"C230-04-no-commit",
		"C230-05",
		"C230-06",
		"C230-07",
	}

	// when
	gotCases := make([]string, 0, len(oracle.Cases))
	for _, scenario := range oracle.Cases {
		gotCases = append(gotCases, scenario.ID)
		if len(scenario.Sequence) == 0 || scenario.Expected == "" {
			t.Fatalf(
				"case %q has no independent sequence or expected outcome",
				scenario.ID,
			)
		}
	}
	slices.Sort(gotCases)
	slices.Sort(wantCases)

	// then
	if !slices.Equal(gotCases, wantCases) {
		t.Fatalf("oracle cases = %v, want %v", gotCases, wantCases)
	}
	assertOracleProjection(t, oracle.Events.Registration)
	assertOracleProjection(t, oracle.Events.Termination)
	if oracle.Events.Registration.ExpectedDocument["dateTo"] != "2026-12-31" {
		t.Fatal("registration fixture lost its independently expected received date")
	}
	if oracle.Events.Termination.ExpectedDocument["dateTo"] != "2026-06-30" {
		t.Fatal("termination fixture lost its independently expected received date")
	}
}

func TestEventFixture_RealKafkaAndElasticsearchResources(t *testing.T) {
	// given
	oracle := loadEventOracle(t)
	environment := requireEventEnvironment(t, oracle)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	producer, err := kgo.NewClient(kafkaTLSOptions(t, environment)...)
	if err != nil {
		t.Fatalf("construct fixture Kafka client: %v", err)
	}
	t.Cleanup(producer.Close)
	waitForKafka(t, ctx, producer)
	createEventTopics(
		t,
		ctx,
		producer,
		environment.RegisteredTopic,
		environment.TerminatedTopic,
	)
	consumerOptions := append(kafkaTLSOptions(t, environment),
		kgo.ClientID("policy-search-disposable-observer"),
		kgo.ConsumerGroup(environment.ConsumerGroup),
		kgo.ConsumeTopics(environment.RegisteredTopic),
		kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.Balancers(kgo.RangeBalancer()),
	)
	consumer, err := kgo.NewClient(consumerOptions...)
	if err != nil {
		t.Fatalf("construct fixture Kafka group client: %v", err)
	}
	t.Cleanup(consumer.Close)
	elasticsearchClient, err := policyelasticsearch.New(policyelasticsearch.Config{
		Endpoint: environment.Elasticsearch,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("construct fixture Elasticsearch client: %v", err)
	}
	t.Cleanup(elasticsearchClient.CloseIdleConnections)
	createPolicyIndex(t, ctx, elasticsearchClient)

	// when
	produceResult := producer.ProduceSync(ctx, &kgo.Record{
		Topic: environment.RegisteredTopic,
		Key:   []byte(oracle.Events.Registration.Key),
		Value: oracle.Events.Registration.Value,
	})
	if err := produceResult.FirstErr(); err != nil {
		t.Fatalf("produce registration fixture: %v", err)
	}
	fetches := consumer.PollRecords(ctx, 1)
	if errors := fetches.Errors(); len(errors) != 0 {
		t.Fatalf("consume registration fixture: %v", errors[0].Err)
	}
	records := fetches.Records()
	indexFixtureDocument(t, ctx, environment.Elasticsearch, oracle.Events.Registration)
	stored := readFixtureDocument(
		t,
		ctx,
		environment.Elasticsearch,
		oracle.Events.Registration.Key,
	)

	// then
	if len(records) != 1 {
		t.Fatalf("consumed records = %d, want 1", len(records))
	}
	if string(records[0].Key) != oracle.Events.Registration.Key ||
		!bytes.Equal(records[0].Value, oracle.Events.Registration.Value) {
		t.Fatal("Kafka fixture changed the independent key or envelope bytes")
	}
	if !reflect.DeepEqual(stored, oracle.Events.Registration.ExpectedDocument) {
		t.Fatalf(
			"stored fixture = %v, want %v",
			stored,
			oracle.Events.Registration.ExpectedDocument,
		)
	}
	consumer.Close()
	verifyEventWorkers(t, ctx, producer, elasticsearchClient, environment, oracle)
	verifyHandlerFailureCommitParity(t, ctx, producer, elasticsearchClient, environment, oracle)
	verifyProjectionReplacement(t, ctx, elasticsearchClient, environment.Elasticsearch, oracle)
}

func verifyHandlerFailureCommitParity(
	t *testing.T,
	ctx context.Context,
	producer *kgo.Client,
	elasticsearchClient *policyelasticsearch.Client,
	environment eventEnvironment,
	oracle eventOracle,
) {
	t.Helper()
	first := eventWithNumber(t, oracle.Events.Registration, "SYN-230-FAIL-1")
	second := eventWithNumber(t, oracle.Events.Registration, "SYN-230-FAIL-2")
	result := producer.ProduceSync(ctx,
		&kgo.Record{Topic: environment.RegisteredTopic, Value: first.Value},
		&kgo.Record{Topic: environment.RegisteredTopic, Value: second.Value},
	)
	if err := result.FirstErr(); err != nil {
		t.Fatalf("produce handler-failure batch: %v", err)
	}
	projection := &failingEventProjection{}
	worker, err := policykafka.NewWorker(policykafka.Settings{
		Brokers: []string{environment.Broker}, Group: environment.ConsumerGroup,
		Topic: environment.RegisteredTopic, ClientID: "policy-registered-listener",
		EventType: "registered",
		CACert:    environment.KafkaCA,
	}, projection, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, cancelWorker := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerCtx) }()
	if err := worker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	projection.waitForCalls(t, 1)
	time.Sleep(300 * time.Millisecond)
	if calls := projection.calls(); calls != 1 {
		t.Fatalf(
			"handler failure processed %d records from the fetched batch, want 1",
			calls,
		)
	}
	cancelWorker()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	if err := worker.Shutdown(shutdownCtx); err != nil {
		cancelShutdown()
		t.Fatal(err)
	}
	cancelShutdown()
	if _, exists := tryReadFixtureDocument(ctx, environment.Elasticsearch, second.Key); exists {
		t.Fatal("unprocessed remainder was projected after the failed fetched batch")
	}

	writer, err := policyelasticsearch.NewWriter(elasticsearchClient)
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := application.NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := policykafka.NewWorker(policykafka.Settings{
		Brokers: []string{environment.Broker}, Group: environment.ConsumerGroup,
		Topic: environment.RegisteredTopic, ClientID: "policy-registered-listener",
		EventType: "registered",
		CACert:    environment.KafkaCA,
	}, eventProjection{useCase: useCase}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	restartCtx, cancelRestart := context.WithCancel(ctx)
	restartDone := make(chan error, 1)
	go func() { restartDone <- restarted.Run(restartCtx) }()
	if err := restarted.Check(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	cancelRestart()
	if err := <-restartDone; err != nil {
		t.Fatal(err)
	}
	shutdownCtx, cancelShutdown = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := restarted.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if _, exists := tryReadFixtureDocument(ctx, environment.Elasticsearch, second.Key); exists {
		t.Fatal("graceful final-position commit replayed the failed batch remainder")
	}
}

type failingEventProjection struct {
	mu    sync.Mutex
	count int
}

func (projection *failingEventProjection) ProjectPolicy(
	context.Context,
	application.PolicyEvent,
) error {
	projection.mu.Lock()
	projection.count++
	projection.mu.Unlock()
	return errors.New("synthetic writer failure")
}

func (projection *failingEventProjection) calls() int {
	projection.mu.Lock()
	defer projection.mu.Unlock()
	return projection.count
}

func (projection *failingEventProjection) waitForCalls(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if projection.calls() >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("projection calls = %d, want %d", projection.calls(), count)
}

func eventWithNumber(t *testing.T, source oracleEvent, number string) oracleEvent {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(source.Value, &payload); err != nil {
		t.Fatal(err)
	}
	policy, ok := payload["policy"].(map[string]any)
	if !ok {
		t.Fatal("oracle event policy is not an object")
	}
	policy["number"] = number
	value, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string]string, len(source.ExpectedDocument))
	for key, value := range source.ExpectedDocument {
		want[key] = value
	}
	want["number"] = number
	return oracleEvent{Key: number, Value: value, ExpectedDocument: want}
}

func verifyEventWorkers(
	t *testing.T,
	ctx context.Context,
	producer *kgo.Client,
	elasticsearchClient *policyelasticsearch.Client,
	environment eventEnvironment,
	oracle eventOracle,
) {
	t.Helper()
	writer, err := policyelasticsearch.NewWriter(elasticsearchClient)
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := application.NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	projection := eventProjection{useCase: useCase}
	workers := make([]*policykafka.Worker, 0, 2)
	for _, settings := range []policykafka.Settings{
		{Brokers: []string{environment.Broker}, Group: environment.ConsumerGroup, Topic: environment.RegisteredTopic, ClientID: "policy-registered-listener", EventType: "registered", CACert: environment.KafkaCA},
		{Brokers: []string{environment.Broker}, Group: environment.ConsumerGroup, Topic: environment.TerminatedTopic, ClientID: "policy-terminated-listener", EventType: "terminated", CACert: environment.KafkaCA},
	} {
		worker, workerErr := policykafka.NewWorker(settings, projection, slog.Default())
		if workerErr != nil {
			t.Fatal(workerErr)
		}
		workers = append(workers, worker)
	}
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	results := make(chan error, len(workers))
	for _, worker := range workers {
		go func(item *policykafka.Worker) { results <- item.Run(workerCtx) }(worker)
	}
	for _, worker := range workers {
		if workerErr := worker.Check(ctx); workerErr != nil {
			t.Fatal(workerErr)
		}
	}
	produceEvent(t, ctx, producer, environment.RegisteredTopic, oracle.Events.Registration)
	waitForFixtureDocument(t, ctx, environment.Elasticsearch, oracle.Events.Registration)
	produceEvent(t, ctx, producer, environment.TerminatedTopic, oracle.Events.Termination)
	waitForFixtureDocument(t, ctx, environment.Elasticsearch, oracle.Events.Termination)
	produceRawEvent(t, ctx, producer, environment.RegisteredTopic, []byte(`{"policy":`))
	produceEvent(t, ctx, producer, environment.RegisteredTopic, oracle.Events.Registration)
	waitForFixtureDocument(t, ctx, environment.Elasticsearch, oracle.Events.Registration)
	cancelWorkers()
	for range workers {
		if workerErr := <-results; workerErr != nil {
			t.Fatalf("run event worker: %v", workerErr)
		}
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	for _, worker := range workers {
		if workerErr := worker.Shutdown(shutdownCtx); workerErr != nil {
			t.Fatalf("shutdown event worker: %v", workerErr)
		}
	}
}

type eventProjection struct{ useCase application.ProjectPolicy }

func (projection eventProjection) ProjectPolicy(
	ctx context.Context,
	event application.PolicyEvent,
) error {
	return projection.useCase.Execute(ctx, event)
}

func produceEvent(
	t *testing.T,
	ctx context.Context,
	producer *kgo.Client,
	topic string,
	event oracleEvent,
) {
	t.Helper()
	produceRawEvent(t, ctx, producer, topic, event.Value)
}

func produceRawEvent(
	t *testing.T,
	ctx context.Context,
	producer *kgo.Client,
	topic string,
	value []byte,
) {
	t.Helper()
	result := producer.ProduceSync(ctx, &kgo.Record{Topic: topic, Value: value})
	if err := result.FirstErr(); err != nil {
		t.Fatalf("produce worker fixture: %v", err)
	}
}

func waitForFixtureDocument(
	t *testing.T,
	ctx context.Context,
	elasticsearchURL string,
	event oracleEvent,
) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		stored, ok := tryReadFixtureDocument(ctx, elasticsearchURL, event.Key)
		if ok && reflect.DeepEqual(stored, event.ExpectedDocument) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("worker did not project expected document for %s", event.Key)
}

func tryReadFixtureDocument(
	ctx context.Context,
	elasticsearchURL string,
	number string,
) (map[string]string, bool) {
	endpoint := fmt.Sprintf(
		"%s/%s/_doc/%s",
		elasticsearchURL,
		eventFixtureIndex,
		url.PathEscape(number),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, false
	}
	var result struct {
		Source map[string]string `json:"_source"`
	}
	if json.NewDecoder(response.Body).Decode(&result) != nil {
		return nil, false
	}
	return result.Source, true
}

func verifyProjectionReplacement(
	t *testing.T,
	ctx context.Context,
	client *policyelasticsearch.Client,
	elasticsearchURL string,
	oracle eventOracle,
) {
	t.Helper()
	writer, err := policyelasticsearch.NewWriter(client)
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := application.NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	registration := oracleProjectionEvent(t, oracle.Events.Registration)
	termination := oracleProjectionEvent(t, oracle.Events.Termination)
	sequence := []struct {
		name  string
		event application.PolicyEvent
		want  map[string]string
	}{
		{"registration", registration, oracle.Events.Registration.ExpectedDocument},
		{"termination", termination, oracle.Events.Termination.ExpectedDocument},
		{"duplicate termination", termination, oracle.Events.Termination.ExpectedDocument},
		{
			"registration after termination",
			registration,
			oracle.Events.Registration.ExpectedDocument,
		},
	}
	for _, step := range sequence {
		if step.name == "termination" {
			indexStaleFixtureDocument(t, ctx, elasticsearchURL, registration)
		}
		if err := useCase.Execute(ctx, step.event); err != nil {
			t.Fatalf("project %s: %v", step.name, err)
		}
		refreshPolicyIndex(t, ctx, client)
		stored := readFixtureDocument(t, ctx, elasticsearchURL, step.event.Number)
		if !reflect.DeepEqual(stored, step.want) {
			t.Fatalf("%s document = %v, want %v", step.name, stored, step.want)
		}
	}
	if count := countFixtureDocuments(t, ctx, elasticsearchURL); count != 1 {
		t.Fatalf("projected document count = %d, want 1", count)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := useCase.Execute(canceled, termination); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled projection error = %v", err)
	}
}

func indexStaleFixtureDocument(
	t *testing.T,
	ctx context.Context,
	elasticsearchURL string,
	event application.PolicyEvent,
) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"number":       event.Number,
		"dateFrom":     event.From.String(),
		"dateTo":       event.To.String(),
		"policyHolder": event.PolicyHolder,
		"stale":        "must-disappear-on-replacement",
	})
	if err != nil {
		t.Fatalf("encode stale fixture document: %v", err)
	}
	endpoint := fmt.Sprintf(
		"%s/%s/_doc/%s?refresh=true",
		elasticsearchURL,
		eventFixtureIndex,
		url.PathEscape(event.Number),
	)
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPut,
		endpoint,
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("build stale fixture request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("index stale fixture document: %v", err)
	}
	assertElasticsearchResponseOK(
		t,
		response.StatusCode,
		response.Body,
		"index stale fixture document",
	)
}

func oracleProjectionEvent(t *testing.T, event oracleEvent) application.PolicyEvent {
	t.Helper()
	var envelope struct {
		Policy struct {
			Number       string  `json:"number"`
			From         *string `json:"from"`
			To           *string `json:"to"`
			PolicyHolder string  `json:"policyHolder"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(event.Value, &envelope); err != nil {
		t.Fatalf("decode oracle projection event: %v", err)
	}
	return application.PolicyEvent{
		Number:       envelope.Policy.Number,
		From:         parseOracleDate(t, envelope.Policy.From),
		To:           parseOracleDate(t, envelope.Policy.To),
		PolicyHolder: envelope.Policy.PolicyHolder,
	}
}

func parseOracleDate(t *testing.T, value *string) *domain.Date {
	t.Helper()
	if value == nil {
		return nil
	}
	date, err := domain.ParseDate(*value)
	if err != nil {
		t.Fatalf("parse oracle date: %v", err)
	}
	return &date
}

func refreshPolicyIndex(
	t *testing.T,
	ctx context.Context,
	client *policyelasticsearch.Client,
) {
	t.Helper()
	response, err := client.Indices.Refresh(
		client.Indices.Refresh.WithContext(ctx),
		client.Indices.Refresh.WithIndex(policyelasticsearch.PolicyIndex),
	)
	if err != nil {
		t.Fatalf("refresh policy index: %v", err)
	}
	assertElasticsearchResponseOK(t, response.StatusCode, response.Body, "refresh policy index")
}

func countFixtureDocuments(
	t *testing.T,
	ctx context.Context,
	elasticsearchURL string,
) int {
	t.Helper()
	endpoint := fmt.Sprintf("%s/%s/_count", elasticsearchURL, eventFixtureIndex)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("build fixture count request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("count fixture documents: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf(
			"count fixture documents returned status %d: %s",
			response.StatusCode,
			payload,
		)
	}
	var result struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode fixture document count: %v", err)
	}
	return result.Count
}

func loadEventOracle(t *testing.T) eventOracle {
	t.Helper()
	payload, err := os.ReadFile("../../testdata/events/oracle.json")
	if err != nil {
		t.Fatalf("read event oracle: %v", err)
	}
	var oracle eventOracle
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&oracle); err != nil {
		t.Fatalf("decode event oracle: %v", err)
	}
	if oracle.SchemaVersion != 1 || oracle.Ownership != eventFixtureOwnership {
		t.Fatalf(
			"unexpected event oracle identity: version=%d ownership=%q",
			oracle.SchemaVersion,
			oracle.Ownership,
		)
	}
	if oracle.Resources.RegisteredTopic != "policy-registered" ||
		oracle.Resources.TerminatedTopic != "policy-terminated" ||
		oracle.Resources.ConsumerGroup != "policy-search-service" ||
		oracle.Resources.Index != eventFixtureIndex ||
		oracle.Resources.SyntheticIDPrefix != "SYN-230-" {
		t.Fatalf(
			"event oracle resources are not the ordinary service coordinates",
		)
	}
	return oracle
}

func assertOracleProjection(t *testing.T, event oracleEvent) {
	t.Helper()
	var envelope struct {
		Policy struct {
			Number       string `json:"number"`
			From         string `json:"from"`
			To           string `json:"to"`
			PolicyHolder string `json:"policyHolder"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(event.Value, &envelope); err != nil {
		t.Fatalf("decode oracle envelope: %v", err)
	}
	projected := map[string]string{
		"number":       envelope.Policy.Number,
		"dateFrom":     envelope.Policy.From,
		"dateTo":       envelope.Policy.To,
		"policyHolder": envelope.Policy.PolicyHolder,
	}
	if event.Key != envelope.Policy.Number {
		t.Fatalf(
			"oracle key %q does not match policy number %q",
			event.Key,
			envelope.Policy.Number,
		)
	}
	if !reflect.DeepEqual(projected, event.ExpectedDocument) {
		t.Fatalf("oracle projection = %v, want %v", projected, event.ExpectedDocument)
	}
}

func requireEventEnvironment(t *testing.T, oracle eventOracle) eventEnvironment {
	t.Helper()
	if os.Getenv("POLICY_SEARCH_TEST_EVENT_OWNERSHIP") != eventFixtureOwnership {
		t.Fatal("POLICY_SEARCH_TEST_EVENT_OWNERSHIP must select the disposable owner")
	}
	environment := eventEnvironment{
		Broker:          os.Getenv("POLICY_SEARCH_TEST_KAFKA_BROKERS"),
		Elasticsearch:   os.Getenv("POLICY_SEARCH_TEST_ELASTICSEARCH_URL"),
		RegisteredTopic: os.Getenv("POLICY_SEARCH_TEST_REGISTERED_TOPIC"),
		TerminatedTopic: os.Getenv("POLICY_SEARCH_TEST_TERMINATED_TOPIC"),
		ConsumerGroup:   os.Getenv("POLICY_SEARCH_TEST_CONSUMER_GROUP"),
	}
	caPath := os.Getenv("POLICY_SEARCH_TEST_KAFKA_CA_FILE")
	if caPath == "" {
		t.Fatal("POLICY_SEARCH_TEST_KAFKA_CA_FILE is required")
	}
	var err error
	environment.KafkaCA, err = os.ReadFile(caPath)
	if err != nil || !bytes.Contains(environment.KafkaCA, []byte("BEGIN CERTIFICATE")) {
		t.Fatal("POLICY_SEARCH_TEST_KAFKA_CA_FILE must contain the owned public CA")
	}
	host, _, err := net.SplitHostPort(environment.Broker)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("POLICY_SEARCH_TEST_KAFKA_BROKERS must be one loopback host:port")
	}
	elasticsearchURL, err := url.Parse(environment.Elasticsearch)
	if err != nil || elasticsearchURL.Scheme != "http" || elasticsearchURL.User != nil ||
		elasticsearchURL.Path != "" || net.ParseIP(elasticsearchURL.Hostname()) == nil ||
		!net.ParseIP(elasticsearchURL.Hostname()).IsLoopback() {
		t.Fatal(
			"POLICY_SEARCH_TEST_ELASTICSEARCH_URL must be an unauthenticated loopback HTTP URL",
		)
	}
	if environment.RegisteredTopic != oracle.Resources.RegisteredTopic ||
		environment.TerminatedTopic != oracle.Resources.TerminatedTopic ||
		environment.ConsumerGroup != oracle.Resources.ConsumerGroup {
		t.Fatal("event integration resources differ from the ordinary oracle")
	}
	return environment
}

func kafkaTLSOptions(t *testing.T, environment eventEnvironment) []kgo.Opt {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(environment.KafkaCA) {
		t.Fatal("append Kafka test CA")
	}
	return []kgo.Opt{
		kgo.SeedBrokers(environment.Broker),
		kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}),
	}
}

func waitForKafka(t *testing.T, ctx context.Context, client *kgo.Client) {
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

func createEventTopics(t *testing.T, ctx context.Context, client *kgo.Client, topics ...string) {
	t.Helper()
	request := kmsg.NewPtrCreateTopicsRequest()
	request.TimeoutMillis = 30_000
	for _, topic := range topics {
		request.Topics = append(request.Topics, kmsg.CreateTopicsRequestTopic{
			Topic:             topic,
			NumPartitions:     1,
			ReplicationFactor: 1,
		})
	}
	response, err := client.Request(ctx, request)
	if err != nil {
		t.Fatalf("create event topics: %v", err)
	}
	created, ok := response.(*kmsg.CreateTopicsResponse)
	if !ok {
		t.Fatalf("create event topics returned %T", response)
	}
	for _, topic := range created.Topics {
		if topic.ErrorCode != 0 {
			t.Fatalf(
				"create owned topic %q returned Kafka error code %d",
				topic.Topic,
				topic.ErrorCode,
			)
		}
	}
}

func indexFixtureDocument(
	t *testing.T,
	ctx context.Context,
	elasticsearchURL string,
	event oracleEvent,
) {
	t.Helper()
	payload, err := json.Marshal(event.ExpectedDocument)
	if err != nil {
		t.Fatalf("encode fixture document: %v", err)
	}
	endpoint := fmt.Sprintf(
		"%s/%s/_doc/%s?refresh=true",
		elasticsearchURL,
		eventFixtureIndex,
		url.PathEscape(event.Key),
	)
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPut,
		endpoint,
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("build fixture index request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("index fixture document: %v", err)
	}
	assertElasticsearchResponseOK(
		t,
		response.StatusCode,
		response.Body,
		"index fixture document",
	)
}

func readFixtureDocument(
	t *testing.T,
	ctx context.Context,
	elasticsearchURL string,
	id string,
) map[string]string {
	t.Helper()
	endpoint := fmt.Sprintf(
		"%s/%s/_doc/%s",
		elasticsearchURL,
		eventFixtureIndex,
		url.PathEscape(id),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("build fixture read request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("read fixture document: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf(
			"read fixture document returned status %d: %s",
			response.StatusCode,
			payload,
		)
	}
	var result struct {
		Source map[string]string `json:"_source"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode fixture document: %v", err)
	}
	return result.Source
}
