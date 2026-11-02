//go:build integration

package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
	policyelasticsearch "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/elasticsearch"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

const (
	transferOwnership       = "issue-234-transfer"
	transferRegisteredTopic = "policy-registered-issue-234-transfer"
	transferTerminatedTopic = "policy-terminated-issue-234-transfer"
	transferConsumerGroup   = "policy-search-service-issue-234-transfer"
)

type transferReport struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Ownership     string                    `json:"ownership"`
	Resources     map[string]string         `json:"resources"`
	Starting      map[string]map[string]any `json:"startingDocuments"`
	Sequence      []transferSequenceItem    `json:"sequence"`
	Final         map[string]map[string]any `json:"finalDocuments"`
	Offsets       map[string]int64          `json:"finalCommittedOffsets"`
	Members       map[string]int            `json:"memberCounts"`
	Sensitivity   map[string]string         `json:"sensitivity"`
	Limit         string                    `json:"evidenceLimit"`
}

type transferSequenceItem struct {
	EventType string `json:"eventType"`
	Number    string `json:"number"`
	Topic     string `json:"topic"`
	Partition int32  `json:"partition"`
	Offset    int64  `json:"offset"`
}

func TestOwnershipTransfer_PreservesPopulatedStateAndProgress(t *testing.T) {
	// given
	environment := transferEnvironmentFromProcess(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	client := newTransferKafkaClient(t, environment)
	t.Cleanup(client.Close)
	createTransferTopics(t, ctx, client)
	createTransferIndex(t, environment.elasticsearch)

	starting := map[string]map[string]any{
		"SYN-234-T-BASE-A": transferDocument("SYN-234-T-BASE-A", "2026-12-31", "Base A"),
		"SYN-234-T-BASE-B": transferDocument("SYN-234-T-BASE-B", "2026-12-31", "Base B"),
	}
	for number, document := range starting {
		putTransferDocument(t, environment.elasticsearch, number, document)
	}
	if err := validateTransferState(true, false, 0, "fixture"); err != nil {
		t.Fatal(err)
	}

	settings := testConfig(t)
	settings.ProbeTimeout = 10 * time.Second
	settings.Elasticsearch.URL = environment.elasticsearch
	settings.Elasticsearch.Timeout = 5 * time.Second
	settings.Events = config.Events{
		Enabled: true, Brokers: []string{environment.broker}, Group: transferConsumerGroup,
		RegisteredTopic: transferRegisteredTopic, TerminatedTopic: transferTerminatedTopic,
		CAFile: environment.caFile, Writer: settings.Elasticsearch,
	}
	deps := productionDependencies()
	productionProjectionFactory := deps.newProjection
	deps.newProjection = func(
		settings config.Elasticsearch,
		observer *telemetry.DependencyObserver,
	) (projectionRuntime, error) {
		projection, err := productionProjectionFactory(settings, observer)
		if err != nil {
			return nil, err
		}
		production, ok := projection.(*productionProjection)
		if !ok {
			return nil, errors.New("production projection type differs")
		}
		return &transferProjection{productionProjection: production}, nil
	}
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	serviceCtx, stopService := context.WithCancel(context.Background())
	serviceResult := make(chan error, 1)
	go func() { serviceResult <- run(serviceCtx, settings, discardLogger(), deps) }()
	_ = receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	waitForTransferReady(t, "http://"+healthAddress+"/readyz", serviceResult)
	waitForTransferMembers(t, ctx, client, 2)
	if err := validateTransferState(false, true, 2, "go"); err != nil {
		t.Fatal(err)
	}

	sequence := []struct {
		eventType string
		number    string
		topic     string
		dateTo    string
		holder    string
	}{
		{"registration", "SYN-234-T-GO-C", transferRegisteredTopic, "2026-12-31", "Go C"},
		{
			"termination",
			"SYN-234-T-BASE-A",
			transferTerminatedTopic,
			"2026-06-30",
			"Base A",
		},
		{
			"registration",
			"SYN-234-T-BASE-A",
			transferRegisteredTopic,
			"2026-12-31",
			"Base A",
		},
		{
			"termination",
			"SYN-234-T-BASE-B",
			transferTerminatedTopic,
			"2026-06-30",
			"Base B",
		},
	}
	reportItems := make([]transferSequenceItem, 0, len(sequence))
	for _, item := range sequence {
		record := produceTransferEvent(
			t,
			ctx,
			client,
			item.topic,
			item.eventType,
			item.number,
			item.dateTo,
			item.holder,
		)
		want := transferDocument(item.number, item.dateTo, item.holder)
		waitForTransferQuery(t, ctx, "http://"+httpAddress+"/policies", item.number, want)
		reportItems = append(reportItems, transferSequenceItem{
			EventType: item.eventType, Number: item.number, Topic: item.topic,
			Partition: record.Partition, Offset: record.Offset,
		})
	}
	if err := validateTransferSequence(reportItems); err != nil {
		t.Fatal(err)
	}

	// when
	stopService()
	if err := receiveError(t, serviceResult); err != nil {
		t.Fatal(err)
	}
	waitForTransferMembers(t, ctx, client, 0)
	offsets := transferCommittedOffsets(t, ctx, client)
	if err := validateTransferState(true, false, 0, "fixture"); err != nil {
		t.Fatal(err)
	}
	reverse := transferDocument("SYN-234-T-REVERSE-D", "2027-12-31", "Reverse D")
	putTransferDocument(t, environment.elasticsearch, "SYN-234-T-REVERSE-D", reverse)

	// then
	wantFinal := map[string]map[string]any{
		"SYN-234-T-BASE-A":    transferDocument("SYN-234-T-BASE-A", "2026-12-31", "Base A"),
		"SYN-234-T-BASE-B":    transferDocument("SYN-234-T-BASE-B", "2026-06-30", "Base B"),
		"SYN-234-T-GO-C":      transferDocument("SYN-234-T-GO-C", "2026-12-31", "Go C"),
		"SYN-234-T-REVERSE-D": reverse,
	}
	gotFinal := make(map[string]map[string]any, len(wantFinal))
	for number := range wantFinal {
		gotFinal[number] = getTransferDocument(t, environment.elasticsearch, number)
	}
	if !reflect.DeepEqual(gotFinal, wantFinal) {
		t.Fatalf("final documents = %#v, want %#v", gotFinal, wantFinal)
	}
	if offsets[transferRegisteredTopic] != 2 || offsets[transferTerminatedTopic] != 2 {
		t.Fatalf("committed offsets = %v, want registered=2 terminated=2", offsets)
	}
	if err := validateTransferState(true, true, 2, "go"); err == nil {
		t.Fatal("overlapping fixture and Go authority was accepted")
	}
	if err := validateTransferSequence(reportItems[:len(reportItems)-1]); err == nil {
		t.Fatal("omitted transfer event was accepted")
	}
	writeTransferReport(t, environment.report, transferReport{
		SchemaVersion: 1,
		Ownership:     transferOwnership,
		Resources: map[string]string{
			"index":           policyelasticsearch.PolicyIndex,
			"registeredTopic": transferRegisteredTopic,
			"terminatedTopic": transferTerminatedTopic,
			"consumerGroup":   transferConsumerGroup,
		},
		Starting: starting,
		Sequence: reportItems,
		Final:    gotFinal,
		Offsets:  offsets,
		Members: map[string]int{
			"fixtureStart":    0,
			"goService":       2,
			"fixtureRestored": 0,
		},
		Sensitivity: map[string]string{
			"overlappingAuthority": "rejected",
			"omittedEvent":         "rejected",
		},
		Limit: "disposable fixture-owner proxy; no Java process, normal route, shared index/group, backlog, or live authorization was exercised",
	})
}

// transferProjection retains the production writer and replaces only its security-privilege
// readiness request, which an owned security-disabled Elasticsearch fixture cannot serve.
type transferProjection struct {
	*productionProjection
}

func (projection *transferProjection) Check(ctx context.Context) error {
	return projection.client.Check(ctx)
}

func waitForTransferReady(t *testing.T, target string, serviceResult <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-serviceResult:
			t.Fatalf("service stopped before transfer readiness: %v", err)
		default:
		}
		response, err := (&http.Client{Timeout: 12 * time.Second}).Get(target)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s did not become ready", target)
}

type transferEnvironment struct {
	broker        string
	elasticsearch string
	caFile        string
	report        string
}

func transferEnvironmentFromProcess(t *testing.T) transferEnvironment {
	t.Helper()
	if os.Getenv("POLICY_SEARCH_TEST_TRANSFER_OWNERSHIP") != transferOwnership {
		t.Fatal("transfer ownership marker differs")
	}
	environment := transferEnvironment{
		broker:        os.Getenv("POLICY_SEARCH_TEST_KAFKA_BROKERS"),
		elasticsearch: os.Getenv("POLICY_SEARCH_TEST_ELASTICSEARCH_URL"),
		caFile:        os.Getenv("POLICY_SEARCH_TEST_KAFKA_CA_FILE"),
		report:        os.Getenv("POLICY_SEARCH_TEST_TRANSFER_REPORT"),
	}
	if os.Getenv("POLICY_SEARCH_TEST_TRANSFER_REGISTERED_TOPIC") != transferRegisteredTopic ||
		os.Getenv(
			"POLICY_SEARCH_TEST_TRANSFER_TERMINATED_TOPIC",
		) != transferTerminatedTopic ||
		os.Getenv("POLICY_SEARCH_TEST_TRANSFER_CONSUMER_GROUP") != transferConsumerGroup {
		t.Fatal("transfer coordinates differ")
	}
	host, _, err := net.SplitHostPort(environment.broker)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("transfer Kafka must use one loopback address")
	}
	parsed, err := url.Parse(environment.elasticsearch)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil ||
		net.ParseIP(
			parsed.Hostname(),
		) == nil || !net.ParseIP(parsed.Hostname()).IsLoopback() {
		t.Fatal("transfer Elasticsearch must use unauthenticated loopback HTTP")
	}
	if !filepath.IsAbs(environment.report) || environment.caFile == "" {
		t.Fatal("transfer report and CA paths are required and report must be absolute")
	}
	return environment
}

func newTransferKafkaClient(t *testing.T, environment transferEnvironment) *kgo.Client {
	t.Helper()
	pem, err := os.ReadFile(environment.caFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("append transfer Kafka CA")
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(environment.broker),
		kgo.ClientID("issue-234-transfer-observer"),
		kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func createTransferTopics(t *testing.T, ctx context.Context, client *kgo.Client) {
	t.Helper()
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	request := kmsg.NewPtrCreateTopicsRequest()
	request.Topics = []kmsg.CreateTopicsRequestTopic{
		{Topic: transferRegisteredTopic, NumPartitions: 1, ReplicationFactor: 1},
		{Topic: transferTerminatedTopic, NumPartitions: 1, ReplicationFactor: 1},
	}
	response, err := request.RequestWith(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	for _, topic := range response.Topics {
		if topic.ErrorCode != 0 {
			t.Fatalf("create transfer topic %s: code=%d", topic.Topic, topic.ErrorCode)
		}
	}
}

func createTransferIndex(t *testing.T, endpoint string) {
	t.Helper()
	fixtureRequest(t, http.MethodPut, endpoint+"/"+policyelasticsearch.PolicyIndex, `{
		"settings":{"number_of_shards":1,"number_of_replicas":0},
		"mappings":{"properties":{"number":{"type":"keyword"},"policyHolder":{"type":"text"},"dateFrom":{"type":"date","format":"strict_date"},"dateTo":{"type":"date","format":"strict_date"}}}
	}`)
	t.Cleanup(func() {
		fixtureRequest(
			t,
			http.MethodDelete,
			endpoint+"/"+policyelasticsearch.PolicyIndex,
			"",
		)
	})
}

func transferDocument(number, dateTo, holder string) map[string]any {
	return map[string]any{
		"number": number, "dateFrom": "2026-01-01", "dateTo": dateTo,
		"policyHolder": holder,
	}
}

func putTransferDocument(t *testing.T, endpoint, number string, document map[string]any) {
	t.Helper()
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	fixtureRequest(
		t,
		http.MethodPut,
		endpoint+"/"+policyelasticsearch.PolicyIndex+"/_doc/"+url.PathEscape(
			number,
		)+"?refresh=true",
		string(payload),
	)
}

func getTransferDocument(t *testing.T, endpoint, number string) map[string]any {
	t.Helper()
	response, err := http.Get(
		endpoint + "/" + policyelasticsearch.PolicyIndex + "/_doc/" + url.PathEscape(
			number,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get transfer document %s returned %d", number, response.StatusCode)
	}
	var result struct {
		Source map[string]any `json:"_source"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result.Source
}

func produceTransferEvent(
	t *testing.T,
	ctx context.Context,
	client *kgo.Client,
	topic string,
	eventType string,
	number string,
	dateTo string,
	holder string,
) *kgo.Record {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"policy": map[string]any{
		"number": number, "from": "2026-01-01", "to": dateTo, "policyHolder": holder,
		"productCode": "SYNTHETIC", "totalPremium": 234.01, "agentLogin": "issue-234-agent",
	}})
	if err != nil {
		t.Fatal(err)
	}
	record := &kgo.Record{
		Topic:   topic,
		Key:     []byte(number),
		Value:   payload,
		Headers: []kgo.RecordHeader{{Key: "event-type", Value: []byte(eventType)}},
	}
	result := client.ProduceSync(ctx, record)
	if err := result.FirstErr(); err != nil {
		t.Fatal(err)
	}
	return result[0].Record
}

func waitForTransferQuery(
	t *testing.T,
	ctx context.Context,
	endpoint string,
	number string,
	want map[string]any,
) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			endpoint+"?q="+url.QueryEscape(`number:"`+number+`"`),
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			var result struct {
				Policies []map[string]any `json:"policies"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&result)
			_ = response.Body.Close()
			if decodeErr == nil && response.StatusCode == http.StatusOK &&
				len(result.Policies) == 1 &&
				reflect.DeepEqual(result.Policies[0], want) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("generated query route did not return %s = %#v", number, want)
}

func waitForTransferMembers(t *testing.T, ctx context.Context, client *kgo.Client, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		request := kmsg.NewPtrDescribeGroupsRequest()
		request.Version = 4
		request.Groups = []string{transferConsumerGroup}
		response, err := request.RequestWith(ctx, client)
		if err == nil && len(response.Groups) == 1 &&
			len(response.Groups[0].Members) == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("transfer group member count did not become %d", want)
}

func transferCommittedOffsets(
	t *testing.T,
	ctx context.Context,
	client *kgo.Client,
) map[string]int64 {
	t.Helper()
	request := kmsg.NewPtrOffsetFetchRequest()
	request.Version = 7
	request.Group = transferConsumerGroup
	response, err := request.RequestWith(ctx, client)
	if err != nil || response.ErrorCode != 0 {
		t.Fatalf("fetch transfer offsets: response=%v err=%v", response, err)
	}
	offsets := map[string]int64{}
	for _, topic := range response.Topics {
		if len(topic.Partitions) != 1 || topic.Partitions[0].Partition != 0 ||
			topic.Partitions[0].ErrorCode != 0 {
			t.Fatalf("transfer topic offsets = %+v", topic)
		}
		offsets[topic.Topic] = topic.Partitions[0].Offset
	}
	return offsets
}

func validateTransferState(fixtureLease, goLease bool, members int, route string) error {
	if fixtureLease == goLease {
		return errors.New("exactly one writer lease must be active")
	}
	if goLease && (members != 2 || route != "go") {
		return errors.New("Go owner requires two worker members and the Go route")
	}
	if fixtureLease && (members != 0 || route != "fixture") {
		return errors.New("fixture owner requires zero group members and the fixture route")
	}
	return nil
}

func validateTransferSequence(items []transferSequenceItem) error {
	want := []string{
		"registration:SYN-234-T-GO-C",
		"termination:SYN-234-T-BASE-A",
		"registration:SYN-234-T-BASE-A",
		"termination:SYN-234-T-BASE-B",
	}
	got := make([]string, len(items))
	for index, item := range items {
		got[index] = item.EventType + ":" + item.Number
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("transfer sequence = %v, want %v", got, want)
	}
	return nil
}

func writeTransferReport(t *testing.T, path string, report transferReport) {
	t.Helper()
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), environmentSecretMarker()) {
		t.Fatal("transfer report contains a prohibited marker")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func environmentSecretMarker() string {
	return "private-key"
}
