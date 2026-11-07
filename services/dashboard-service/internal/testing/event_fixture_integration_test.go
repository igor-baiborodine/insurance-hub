//go:build integration

package testing_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

const (
	eventFixtureOwnership = "issue-290-disposable"
	eventFixtureTopic     = "policy-registered-issue-290"
	eventFixtureGroup     = "dashboard-service-issue-290"
	eventFixtureIndex     = "policy_stats_issue_290"
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

func TestEventFixture_RealKafkaAndElasticsearchResources(t *testing.T) {
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
	consumerOptions := append(
		kafkaTLSOptions(t, environment),
		kgo.ClientID("dashboard-event-fixture"),
		kgo.ConsumerGroup(environment.ConsumerGroup),
		kgo.ConsumeTopics(environment.Topic),
		kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.Balancers(kgo.RangeBalancer()),
	)
	consumer, err := kgo.NewClient(consumerOptions...)
	if err != nil {
		t.Fatalf("construct fixture Kafka consumer: %v", err)
	}
	t.Cleanup(consumer.Close)
	httpClient := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   5 * time.Second,
	}
	createEventIndex(t, ctx, httpClient, environment)

	// when
	produced := producer.ProduceSync(ctx, &kgo.Record{
		Topic: environment.Topic,
		Key:   []byte(oracle.Events.Registration.Key),
		Value: oracle.Events.Registration.Value,
	})
	if err := produced.FirstErr(); err != nil {
		t.Fatalf("produce registration fixture: %v", err)
	}
	fetches := consumer.PollRecords(ctx, 1)
	if fetchErrors := fetches.Errors(); len(fetchErrors) != 0 {
		t.Fatalf("consume registration fixture: %v", fetchErrors[0].Err)
	}
	records := fetches.Records()
	if len(records) == 1 {
		if err := consumer.CommitRecords(ctx, records...); err != nil {
			t.Fatalf("commit registration fixture: %v", err)
		}
	}
	documentID := expectedDocumentNumber(t, oracle.Events.Registration.ExpectedDocument)
	indexFixtureDocument(
		t,
		ctx,
		httpClient,
		environment,
		documentID,
		oracle.Events.Registration.ExpectedDocument,
	)
	stored := readFixtureDocument(t, ctx, httpClient, environment, documentID)

	// then
	if len(records) != 1 {
		t.Fatalf("consumed records = %d, want 1", len(records))
	}
	if string(records[0].Key) != oracle.Events.Registration.Key ||
		!bytes.Equal(records[0].Value, oracle.Events.Registration.Value) {
		t.Fatal("Kafka fixture changed the independent key or envelope bytes")
	}
	if !equalJSON(t, stored, oracle.Events.Registration.ExpectedDocument) {
		t.Fatal("Elasticsearch fixture changed the independently expected document")
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
		oracle.Resources.SyntheticIDPrefix != "SYN-290-" {
		t.Fatal(
			"event oracle resources are not the accepted issue-290 isolation coordinates",
		)
	}
	return oracle
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
	if envelope.Policy.Number != "SYN-290-001" || event.Key == envelope.Policy.Number {
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
		t.Fatal("DASHBOARD_TEST_EVENT_OWNERSHIP must select issue-290-disposable")
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
		t.Fatal("event integration resources differ from the accepted issue-290 oracle")
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
    "policyHolder":{"type":"keyword"},
    "productCode":{"type":"keyword"},
    "totalPremium":{"type":"double"},
    "agentLogin":{"type":"keyword"}
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

func indexFixtureDocument(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	environment eventEnvironment,
	id string,
	document json.RawMessage,
) {
	t.Helper()
	endpoint := fmt.Sprintf(
		"%s/%s/_doc/%s?refresh=true",
		environment.Elasticsearch,
		environment.Index,
		url.PathEscape(id),
	)
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPut,
		endpoint,
		bytes.NewReader(document),
	)
	if err != nil {
		t.Fatalf("build index fixture request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	assertHTTPResponse(t, response, err, http.StatusCreated, "index owned fixture document")
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
