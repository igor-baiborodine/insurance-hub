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
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	dashboardelasticsearch "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/elasticsearch"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/gateway"
	transport "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/grpc"
	dashboardkafka "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/kafka"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/telemetry"
)

const (
	eventFixtureOwnership = "dashboard-disposable"
	eventFixtureTopic     = "policy-registered"
	eventFixtureGroup     = "dashboard-service"
	eventFixtureIndex     = "policy_stats"
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
	ConsumerGroup     string `json:"consumerGroup"`
	Index             string `json:"index"`
	SyntheticIDPrefix string `json:"syntheticIdPrefix"`
}

type oracleEvents struct {
	Registration oracleEvent `json:"registration"`
}

type oracleEvent struct {
	Key              string          `json:"key"`
	Value            json.RawMessage `json:"value"`
	ExpectedDocument json.RawMessage `json:"expectedDocument"`
}

type oracleCase struct {
	ID       string   `json:"id"`
	Sequence []string `json:"sequence"`
	Expected string   `json:"expected"`
}

type eventEnvironment struct {
	Broker        string
	Elasticsearch string
	Topic         string
	ConsumerGroup string
	Index         string
	KafkaCA       []byte
}

func TestEventFixture_OracleDefinesIndependentCases(t *testing.T) {
	// given
	oracle := loadEventOracle(t)
	wantCases := []string{
		"C05-E01", "C05-E02", "C05-E03", "C05-E04", "C05-E05", "C05-E06",
		"C05-E07", "C05-E08", "C05-E09", "C05-E10", "C05-E11", "C05-E12",
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
	assertRegistrationOracle(t, oracle.Events.Registration)
}

func TestEventFixture_ProductionWorkerProjectsRegistration(t *testing.T) {
	// given
	oracle := loadEventOracle(t)
	environment := requireEventEnvironment(t, oracle)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	producer, err := kgo.NewClient(kafkaTLSOptions(t, environment)...)
	if err != nil {
		t.Fatalf("construct fixture Kafka producer: %v", err)
	}
	t.Cleanup(producer.Close)
	waitForKafka(t, ctx, producer)
	createEventTopic(t, ctx, producer, environment.Topic)
	httpClient := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   5 * time.Second,
	}
	createEventIndex(t, ctx, httpClient, environment)
	writerClient, err := dashboardelasticsearch.New(dashboardelasticsearch.Config{
		Endpoint: environment.Elasticsearch,
		Index:    environment.Index,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("construct production writer client: %v", err)
	}
	t.Cleanup(writerClient.CloseIdleConnections)
	writer, err := dashboardelasticsearch.NewWriter(writerClient)
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := application.NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := dashboardkafka.NewWorker(dashboardkafka.Settings{
		Brokers: []string{environment.Broker}, Group: environment.ConsumerGroup,
		Topic: environment.Topic, ClientID: "policy-registered-dashboard-listener",
		CACert: environment.KafkaCA, EventType: "registered",
	}, productionProjection{useCase: useCase}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("construct production registration worker: %v", err)
	}
	workerContext, cancelWorker := context.WithCancel(ctx)
	workerResult := make(chan error, 1)
	go func() { workerResult <- worker.Run(workerContext) }()
	readyContext, cancelReady := context.WithTimeout(ctx, 10*time.Second)
	if err := worker.Check(readyContext); err != nil {
		cancelReady()
		cancelWorker()
		t.Fatalf("production registration worker readiness: %v", err)
	}
	cancelReady()

	// when
	produced := producer.ProduceSync(ctx, &kgo.Record{
		Topic: environment.Topic,
		Key:   []byte(oracle.Events.Registration.Key),
		Value: oracle.Events.Registration.Value,
	})
	if err := produced.FirstErr(); err != nil {
		t.Fatalf("produce registration fixture: %v", err)
	}
	documentID := expectedDocumentNumber(t, oracle.Events.Registration.ExpectedDocument)
	stored := waitForFixtureDocument(
		t,
		ctx,
		httpClient,
		environment,
		documentID,
		oracle.Events.Registration.ExpectedDocument,
	)
	assertRegistrationQueries(
		t,
		ctx,
		environment,
		writerClient,
	)
	replacement := registrationReplacement(t, oracle.Events.Registration, 456.789, "agent-290")
	produceFixtureEvent(t, ctx, producer, environment.Topic, replacement)
	stored = waitForFixtureDocument(
		t, ctx, httpClient, environment, documentID, replacement.ExpectedDocument,
	)
	nullAgent := registrationReplacement(t, oracle.Events.Registration, 789.125, nil)
	produceRawFixtureEvent(t, ctx, producer, environment.Topic, []byte(`{"policy":`))
	produceFixtureEvent(t, ctx, producer, environment.Topic, nullAgent)
	stored = waitForFixtureDocument(
		t, ctx, httpClient, environment, documentID, nullAgent.ExpectedDocument,
	)
	cancelWorker()
	if err := <-workerResult; err != nil {
		t.Fatalf("run production registration worker: %v", err)
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := worker.Shutdown(shutdownContext); err != nil {
		t.Fatalf("shutdown production registration worker: %v", err)
	}

	// then
	if !equalJSON(t, stored, nullAgent.ExpectedDocument) {
		t.Fatal("production worker changed the independently expected document")
	}
	verifyHandlerFailureCommitParity(
		t, ctx, producer, writerClient, httpClient, environment, oracle.Events.Registration,
	)
}

type productionProjection struct{ useCase application.ProjectPolicy }

func (projection productionProjection) ProjectPolicy(
	ctx context.Context,
	event application.PolicyEvent,
) error {
	return projection.useCase.Execute(ctx, event)
}

type failingProjection struct {
	mu    sync.Mutex
	calls int
}

func (projection *failingProjection) ProjectPolicy(
	context.Context,
	application.PolicyEvent,
) error {
	projection.mu.Lock()
	projection.calls++
	projection.mu.Unlock()
	return errors.New("synthetic writer failure")
}

func (projection *failingProjection) waitForCalls(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		projection.mu.Lock()
		calls := projection.calls
		projection.mu.Unlock()
		if calls >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("projection calls did not reach %d", count)
}

func verifyHandlerFailureCommitParity(
	t *testing.T,
	ctx context.Context,
	producer *kgo.Client,
	writerClient *dashboardelasticsearch.Client,
	httpClient *http.Client,
	environment eventEnvironment,
	source oracleEvent,
) {
	t.Helper()
	first := registrationWithNumber(t, source, "SYN-FAIL-1")
	second := registrationWithNumber(t, source, "SYN-FAIL-2")
	produceFixtureEvents(t, ctx, producer, environment.Topic, first, second)
	projection := &failingProjection{}
	worker, err := dashboardkafka.NewWorker(dashboardkafka.Settings{
		Brokers: []string{environment.Broker}, Group: environment.ConsumerGroup,
		Topic: environment.Topic, ClientID: "policy-registered-dashboard-listener",
		CACert: environment.KafkaCA, EventType: "registered",
	}, projection, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	workerContext, cancelWorker := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { result <- worker.Run(workerContext) }()
	if err := worker.Check(ctx); err != nil {
		cancelWorker()
		t.Fatal(err)
	}
	projection.waitForCalls(t, 1)
	time.Sleep(300 * time.Millisecond)
	projection.mu.Lock()
	calls := projection.calls
	projection.mu.Unlock()
	if calls != 1 {
		t.Fatalf("failed fetched batch projected %d records, want 1", calls)
	}
	cancelWorker()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	if err := worker.Shutdown(shutdownContext); err != nil {
		cancelShutdown()
		t.Fatal(err)
	}
	cancelShutdown()

	writer, err := dashboardelasticsearch.NewWriter(writerClient)
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := application.NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := dashboardkafka.NewWorker(dashboardkafka.Settings{
		Brokers: []string{environment.Broker}, Group: environment.ConsumerGroup,
		Topic: environment.Topic, ClientID: "policy-registered-dashboard-listener",
		CACert: environment.KafkaCA, EventType: "registered",
	}, productionProjection{useCase: useCase}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	restartContext, cancelRestart := context.WithCancel(ctx)
	restartResult := make(chan error, 1)
	go func() { restartResult <- restarted.Run(restartContext) }()
	if err := restarted.Check(ctx); err != nil {
		cancelRestart()
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	cancelRestart()
	if err := <-restartResult; err != nil {
		t.Fatal(err)
	}
	shutdownContext, cancelShutdown = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := restarted.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	if _, found := tryReadFixtureDocument(
		t, ctx, httpClient, environment, expectedDocumentNumber(t, second.ExpectedDocument),
	); found {
		t.Fatal("graceful current-position commit replayed the failed batch remainder")
	}
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
	if err := requireJSONEnd(decoder); err != nil {
		t.Fatalf("decode event oracle: %v", err)
	}
	if oracle.SchemaVersion != 1 || oracle.Ownership != eventFixtureOwnership {
		t.Fatalf(
			"unexpected event oracle identity: version=%d ownership=%q",
			oracle.SchemaVersion,
			oracle.Ownership,
		)
	}
	if oracle.Resources.RegisteredTopic != eventFixtureTopic ||
		oracle.Resources.ConsumerGroup != eventFixtureGroup ||
		oracle.Resources.Index != eventFixtureIndex ||
		oracle.Resources.SyntheticIDPrefix != "SYN-" {
		t.Fatal(
			"event oracle resources do not use the ordinary coordinates",
		)
	}
	return oracle
}

func registrationReplacement(
	t *testing.T,
	source oracleEvent,
	premium float64,
	agent any,
) oracleEvent {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(source.Value, &envelope); err != nil {
		t.Fatal(err)
	}
	policy, ok := envelope["policy"].(map[string]any)
	if !ok {
		t.Fatal("registration policy is not an object")
	}
	policy["totalPremium"] = premium
	policy["agentLogin"] = agent
	value, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]any
	if err := json.Unmarshal(source.ExpectedDocument, &expected); err != nil {
		t.Fatal(err)
	}
	expected["totalPremium"] = premium
	expected["agentLogin"] = agent
	document, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	return oracleEvent{Key: source.Key, Value: value, ExpectedDocument: document}
}

func registrationWithNumber(t *testing.T, source oracleEvent, number string) oracleEvent {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(source.Value, &envelope); err != nil {
		t.Fatal(err)
	}
	policy, ok := envelope["policy"].(map[string]any)
	if !ok {
		t.Fatal("registration policy is not an object")
	}
	policy["number"] = number
	value, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]any
	if err := json.Unmarshal(source.ExpectedDocument, &expected); err != nil {
		t.Fatal(err)
	}
	expected["number"] = number
	document, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	return oracleEvent{Key: number, Value: value, ExpectedDocument: document}
}

func produceFixtureEvent(
	t *testing.T,
	ctx context.Context,
	producer *kgo.Client,
	topic string,
	event oracleEvent,
) {
	t.Helper()
	produceRawFixtureEvent(t, ctx, producer, topic, event.Value)
}

func produceFixtureEvents(
	t *testing.T,
	ctx context.Context,
	producer *kgo.Client,
	topic string,
	events ...oracleEvent,
) {
	t.Helper()
	records := make([]*kgo.Record, 0, len(events))
	for _, event := range events {
		records = append(records, &kgo.Record{
			Topic: topic, Key: []byte(event.Key), Value: event.Value,
		})
	}
	if err := producer.ProduceSync(ctx, records...).FirstErr(); err != nil {
		t.Fatalf("produce registration fixtures: %v", err)
	}
}

func produceRawFixtureEvent(
	t *testing.T,
	ctx context.Context,
	producer *kgo.Client,
	topic string,
	value []byte,
) {
	t.Helper()
	if err := producer.ProduceSync(ctx, &kgo.Record{Topic: topic, Value: value}).
		FirstErr(); err != nil {
		t.Fatalf("produce registration fixture: %v", err)
	}
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra json.RawMessage
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("unexpected trailing JSON value")
}

func assertRegistrationOracle(t *testing.T, event oracleEvent) {
	t.Helper()
	var envelope struct {
		Policy struct {
			Number       string          `json:"number"`
			From         []int           `json:"from"`
			To           []int           `json:"to"`
			PolicyHolder string          `json:"policyHolder"`
			ProductCode  string          `json:"productCode"`
			TotalPremium json.Number     `json:"totalPremium"`
			AgentLogin   json.RawMessage `json:"agentLogin"`
		} `json:"policy"`
	}
	decoder := json.NewDecoder(bytes.NewReader(event.Value))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("decode registration oracle: %v", err)
	}
	var expected map[string]json.RawMessage
	if err := json.Unmarshal(event.ExpectedDocument, &expected); err != nil {
		t.Fatalf("decode expected registration document: %v", err)
	}
	if len(expected) != 7 {
		t.Fatalf("expected document fields = %d, want 7", len(expected))
	}
	if envelope.Policy.Number != "SYN-001" || event.Key == envelope.Policy.Number {
		t.Fatal(
			"oracle must retain payload identity independently of a mismatched Kafka key",
		)
	}
	if !slices.Equal(envelope.Policy.From, []int{2026, 1, 1}) ||
		!slices.Equal(envelope.Policy.To, []int{2026, 12, 31}) {
		t.Fatal("oracle lost the accepted Java date arrays")
	}
	if envelope.Policy.PolicyHolder != "Synthetic Holder A" ||
		envelope.Policy.ProductCode != "HSI" ||
		envelope.Policy.TotalPremium.String() != "123.45" ||
		string(envelope.Policy.AgentLogin) != "null" {
		t.Fatal("oracle lost the accepted registration values")
	}
	if string(expected["from"]) != `"2026-01-01"` ||
		string(expected["to"]) != `"2026-12-31"` ||
		string(expected["agentLogin"]) != "null" {
		t.Fatal("expected document lost the independent date or null projection")
	}
}

func requireEventEnvironment(t *testing.T, oracle eventOracle) eventEnvironment {
	t.Helper()
	if os.Getenv("DASHBOARD_TEST_EVENT_OWNERSHIP") != eventFixtureOwnership {
		t.Fatal("DASHBOARD_TEST_EVENT_OWNERSHIP must select dashboard-disposable")
	}
	environment := eventEnvironment{
		Broker:        os.Getenv("DASHBOARD_TEST_KAFKA_BROKERS"),
		Elasticsearch: os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_URL"),
		Topic:         os.Getenv("DASHBOARD_TEST_REGISTERED_TOPIC"),
		ConsumerGroup: os.Getenv("DASHBOARD_TEST_CONSUMER_GROUP"),
		Index:         os.Getenv("DASHBOARD_TEST_EVENT_INDEX"),
	}
	caPath := os.Getenv("DASHBOARD_TEST_KAFKA_CA_FILE")
	if caPath == "" {
		t.Fatal("DASHBOARD_TEST_KAFKA_CA_FILE is required")
	}
	var err error
	environment.KafkaCA, err = os.ReadFile(caPath)
	if err != nil || !bytes.Contains(environment.KafkaCA, []byte("BEGIN CERTIFICATE")) {
		t.Fatal("DASHBOARD_TEST_KAFKA_CA_FILE must contain the owned public CA")
	}
	host, _, err := net.SplitHostPort(environment.Broker)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("DASHBOARD_TEST_KAFKA_BROKERS must be one loopback host:port")
	}
	elasticsearchURL, err := url.Parse(environment.Elasticsearch)
	if err != nil || elasticsearchURL.Scheme != "http" || elasticsearchURL.User != nil ||
		elasticsearchURL.Path != "" || elasticsearchURL.RawQuery != "" ||
		elasticsearchURL.Fragment != "" || elasticsearchURL.Port() == "" ||
		net.ParseIP(elasticsearchURL.Hostname()) == nil ||
		!net.ParseIP(elasticsearchURL.Hostname()).IsLoopback() {
		t.Fatal(
			"DASHBOARD_TEST_ELASTICSEARCH_URL must be an unauthenticated loopback HTTP URL",
		)
	}
	if environment.Topic != oracle.Resources.RegisteredTopic ||
		environment.ConsumerGroup != oracle.Resources.ConsumerGroup ||
		environment.Index != oracle.Resources.Index {
		t.Fatal("event integration resources differ from the accepted oracle")
	}
	return environment
}

func kafkaTLSOptions(t *testing.T, environment eventEnvironment) []kgo.Opt {
	t.Helper()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(environment.KafkaCA) {
		t.Fatal("append Kafka fixture CA")
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

func createEventTopic(t *testing.T, ctx context.Context, client *kgo.Client, topic string) {
	t.Helper()
	request := kmsg.NewPtrCreateTopicsRequest()
	request.TimeoutMillis = 30_000
	request.Topics = append(request.Topics, kmsg.CreateTopicsRequestTopic{
		Topic: topic, NumPartitions: 1, ReplicationFactor: 1,
	})
	response, err := client.Request(ctx, request)
	if err != nil {
		t.Fatalf("create event topic: %v", err)
	}
	created, ok := response.(*kmsg.CreateTopicsResponse)
	if !ok {
		t.Fatalf("create event topic returned %T", response)
	}
	if len(created.Topics) != 1 || created.Topics[0].ErrorCode != 0 {
		t.Fatal("create owned event topic did not return one successful result")
	}
}

func createEventIndex(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	environment eventEnvironment,
) {
	t.Helper()
	mapping := []byte(`{
  "settings":{"number_of_shards":1,"number_of_replicas":0},
  "mappings":{"dynamic":"strict","properties":{
    "number":{"type":"keyword"},
    "from":{"type":"date","format":"strict_date"},
    "to":{"type":"date","format":"strict_date"},
	    "policyHolder":{"type":"text","fields":{"keyword":{"type":"keyword"}}},
	    "productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},
	    "totalPremium":{"type":"double"},
	    "agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword"}}}
  }}
}`)
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPut,
		environment.Elasticsearch+"/"+environment.Index,
		bytes.NewReader(mapping),
	)
	if err != nil {
		t.Fatalf("build create event index request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	assertHTTPResponse(t, response, err, http.StatusOK, "create owned event index")
}

func assertRegistrationQueries(
	t *testing.T,
	ctx context.Context,
	environment eventEnvironment,
	client *dashboardelasticsearch.Client,
) {
	t.Helper()
	reader, err := dashboardelasticsearch.NewSalesReader(client, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	totalSales, err := application.NewGetTotalSales(reader)
	if err != nil {
		t.Fatal(err)
	}
	agentSales, err := application.NewGetAgentSales(reader)
	if err != nil {
		t.Fatal(err)
	}
	salesTrends, err := application.NewGetSalesTrends(reader)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	requestObserver, err := telemetry.NewRequestObserver(logger, metricnoop.NewMeterProvider())
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	server, err := transport.NewServer(
		logger,
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		transport.Settings{
			RequestTimeout:  5 * time.Second,
			MaxReceiveBytes: 1 << 20,
			MaxSendBytes:    8 << 20,
			Observer:        requestObserver,
			MeterProvider:   metricnoop.NewMeterProvider(),
		},
		totalSales,
		agentSales,
		salesTrends,
	)
	if err != nil {
		t.Fatal(err)
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		select {
		case <-serveResult:
		case <-time.After(time.Second):
			t.Error("registration query gRPC server did not stop")
		}
	})
	connection, err := grpcgo.NewClient(
		"passthrough:///dashboard-registration",
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	clientRPC := dashboardv1.NewDashboardServiceClient(connection)
	assertRegistrationRPCQueries(t, ctx, clientRPC)

	handler, err := gateway.NewHandler(clientRPC, gateway.Settings{
		RequestTimeout: 5 * time.Second,
		MaxBodyBytes:   4096,
		TracerProvider: noop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
		Propagator:     propagation.TraceContext{},
		Observer:       requestObserver,
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	assertRegistrationHTTPQueries(t, ctx, httpServer.Client(), httpServer.URL)
	if environment.Index != eventFixtureIndex {
		t.Fatal("registration queries used an unexpected index")
	}
}

func assertRegistrationRPCQueries(
	t *testing.T,
	ctx context.Context,
	client dashboardv1.DashboardServiceClient,
) {
	t.Helper()
	total, err := client.GetTotalSales(ctx, &dashboardv1.GetTotalSalesRequest{
		SaleDateFrom: "2026-01-01", SaleDateTo: "2026-02-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if total.GetTotal().GetPoliciesCount() != 1 ||
		total.GetTotal().GetPremiumAmountMinorUnits() != 12345 ||
		total.GetPerProductTotal()["HSI"].GetPoliciesCount() != 1 {
		t.Fatalf("unexpected direct total-sales result: %v", total)
	}
	agents, err := client.GetAgentSales(ctx, &dashboardv1.GetAgentSalesRequest{
		SaleDateFrom: "2026-01-01", SaleDateTo: "2026-02-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(agents.GetPerAgentTotal()) != 0 {
		t.Fatalf("null agent was attributed by direct RPC: %v", agents)
	}
	trends, err := client.GetSalesTrends(ctx, &dashboardv1.GetSalesTrendsRequest{
		SaleDateFrom: "2026-01-01", SaleDateTo: "2026-02-01",
		AggregationUnitCode: "DAY",
	})
	if err != nil {
		t.Fatal(err)
	}
	periods := trends.GetPeriodSales()
	if len(periods) != 1 || periods[0].GetPeriodDate() != "2026-01-01" ||
		periods[0].GetPoliciesCount() != 1 || periods[0].GetPremiumAmount() != "123.45" {
		t.Fatalf("unexpected direct sales-trends result: %v", trends)
	}
}

func assertRegistrationHTTPQueries(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	baseURL string,
) {
	t.Helper()
	cases := []struct {
		path   string
		body   string
		assert func(map[string]any) bool
	}{
		{
			path: "/dashboard/totalsales",
			body: `{"saleDateFrom":"2026-01-01","saleDateTo":"2026-02-01"}`,
			assert: func(value map[string]any) bool {
				total, _ := value["total"].(map[string]any)
				return total["policiesCount"] == float64(1) &&
					total["premiumAmount"] == 123.45
			},
		},
		{
			path: "/dashboard/agentssales",
			body: `{"saleDateFrom":"2026-01-01","saleDateTo":"2026-02-01"}`,
			assert: func(value map[string]any) bool {
				agents, found := value["perAgentTotal"]
				if !found {
					return len(value) == 0
				}
				result, ok := agents.(map[string]any)
				return ok && len(result) == 0
			},
		},
		{
			path: "/dashboard/trends",
			body: `{"saleDateFrom":"2026-01-01","saleDateTo":"2026-02-01",` +
				`"aggregationUnitCode":"DAY"}`,
			assert: func(value map[string]any) bool {
				periods, ok := value["periodSales"].([]any)
				if !ok || len(periods) != 1 {
					return false
				}
				period, ok := periods[0].(map[string]any)
				periodDate, dateOK := period["periodDate"].([]any)
				return ok && dateOK && len(periodDate) == 3 &&
					periodDate[0] == float64(
						2026,
					) && periodDate[1] == float64(1) &&
					periodDate[2] == float64(
						1,
					) && period["period"] == "2026-01-01" &&
					period["policiesCount"] == float64(1) &&
					period["premiumAmount"] == 123.45
			},
		},
	}
	for _, test := range cases {
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			baseURL+test.path,
			bytes.NewBufferString(test.body),
		)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		decodeErr := json.NewDecoder(response.Body).Decode(&value)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || decodeErr != nil || !test.assert(value) {
			t.Fatalf(
				"gateway query %s returned status=%d body=%v decode=%v",
				test.path,
				response.StatusCode,
				value,
				decodeErr,
			)
		}
	}
}

func expectedDocumentNumber(t *testing.T, document json.RawMessage) string {
	t.Helper()
	var expected struct {
		Number string `json:"number"`
	}
	if err := json.Unmarshal(document, &expected); err != nil || expected.Number == "" {
		t.Fatal("expected document number is required")
	}
	return expected.Number
}

func waitForFixtureDocument(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	environment eventEnvironment,
	id string,
	expected json.RawMessage,
) json.RawMessage {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		stored, found := tryReadFixtureDocument(t, ctx, client, environment, id)
		if found && equalJSON(t, stored, expected) {
			return stored
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("production worker did not project the registration")
	return nil
}

func readFixtureDocument(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	environment eventEnvironment,
	id string,
) json.RawMessage {
	t.Helper()
	endpoint := fmt.Sprintf(
		"%s/%s/_doc/%s",
		environment.Elasticsearch,
		environment.Index,
		url.PathEscape(id),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("build read fixture request: %v", err)
	}
	response, err := client.Do(request)
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
		Source json.RawMessage `json:"_source"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode fixture document: %v", err)
	}
	return result.Source
}

func tryReadFixtureDocument(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	environment eventEnvironment,
	id string,
) (json.RawMessage, bool) {
	t.Helper()
	endpoint := fmt.Sprintf(
		"%s/%s/_doc/%s",
		environment.Elasticsearch,
		environment.Index,
		url.PathEscape(id),
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("build read fixture request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("read fixture document: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, false
	}
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf(
			"read fixture document returned status %d: %s",
			response.StatusCode,
			payload,
		)
	}
	var result struct {
		Source json.RawMessage `json:"_source"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode fixture document: %v", err)
	}
	return result.Source, true
}

func assertHTTPResponse(
	t *testing.T,
	response *http.Response,
	err error,
	wantStatus int,
	activity string,
) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", activity, err)
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("%s returned status %d: %s", activity, response.StatusCode, payload)
	}
}

func equalJSON(t *testing.T, left, right []byte) bool {
	t.Helper()
	var leftValue any
	var rightValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	leftDecoder.UseNumber()
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	rightDecoder.UseNumber()
	if err := leftDecoder.Decode(&leftValue); err != nil {
		t.Fatalf("decode stored fixture JSON: %v", err)
	}
	if err := rightDecoder.Decode(&rightValue); err != nil {
		t.Fatalf("decode expected fixture JSON: %v", err)
	}
	leftCanonical, err := json.Marshal(leftValue)
	if err != nil {
		t.Fatalf("encode stored fixture JSON: %v", err)
	}
	rightCanonical, err := json.Marshal(rightValue)
	if err != nil {
		t.Fatalf("encode expected fixture JSON: %v", err)
	}
	return bytes.Equal(leftCanonical, rightCanonical)
}
