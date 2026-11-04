//go:build integration

package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

const (
	cutoverOwnership       = "issue-235-cutover-rehearsal"
	cutoverIndex           = "policy-views"
	cutoverRegisteredTopic = "policy-registered"
	cutoverTerminatedTopic = "policy-terminated"
	cutoverConsumerGroup   = "policy-search-service"
	cutoverGatewaySecret   = "issue-235-disposable-gateway-secret-with-more-than-thirty-two-bytes"
)

type cutoverEnvironment struct {
	transferEnvironment
	docker                     string
	javaTruststore             string
	legacyPolicySearchImage    string
	legacyPolicySearchIdentity string
	legacyGatewayImage         string
	legacyGatewayIdentity      string
}

type cutoverPhase struct {
	Name             string           `json:"name"`
	Owner            string           `json:"owner"`
	Members          int              `json:"members"`
	CommittedOffsets map[string]int64 `json:"committedOffsets"`
	EndOffsets       map[string]int64 `json:"endOffsets"`
	Backlog          map[string]int64 `json:"backlog"`
}

type cutoverEdgeObservation struct {
	Backend       string `json:"backend"`
	Authenticated int    `json:"authenticatedStatus"`
	Denied        int    `json:"deniedStatus"`
	ZeroHitStatus int    `json:"zeroHitStatus"`
	ZeroHitBody   string `json:"zeroHitBody"`
	FailureBody   string `json:"failureBody,omitempty"`
}

type cutoverReport struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Ownership     string                    `json:"ownership"`
	Resources     map[string]string         `json:"resources"`
	Images        map[string]string         `json:"images"`
	Starting      map[string]map[string]any `json:"startingDocuments"`
	Sequence      []transferSequenceItem    `json:"sequence"`
	Phases        []cutoverPhase            `json:"phases"`
	Edge          []cutoverEdgeObservation  `json:"edge"`
	Final         map[string]map[string]any `json:"finalDocuments"`
	Sensitivity   map[string]string         `json:"sensitivity"`
	Cleanup       string                    `json:"cleanup"`
	Limit         string                    `json:"evidenceLimit"`
}

type runningGoService struct {
	httpAddress   string
	healthAddress string
	stop          context.CancelFunc
	result        <-chan error
}

func TestCutoverRehearsal_TransfersAndReturnsActualJavaOwnership(t *testing.T) {
	// given
	environment := cutoverEnvironmentFromProcess(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client := newTransferKafkaClient(t, environment.transferEnvironment)
	t.Cleanup(client.Close)
	createCutoverTopics(t, ctx, client)
	createCutoverIndex(t, environment.elasticsearch)

	starting := map[string]map[string]any{
		"SYN-235-BASE-A": cutoverDocument("SYN-235-BASE-A", "2026-12-31", "Base A"),
		"SYN-235-BASE-B": cutoverDocument("SYN-235-BASE-B", "2026-12-31", "Base B"),
	}
	for number, document := range starting {
		putCutoverDocument(t, environment.elasticsearch, number, document)
	}

	sequence := make([]transferSequenceItem, 0, 14)
	phases := make([]cutoverPhase, 0, 8)
	edge := make([]cutoverEdgeObservation, 0, 3)

	javaName, javaAddress := startLegacyPolicySearch(t, ctx, environment, "forward")
	waitForCutoverMembers(t, ctx, client, 2)
	javaInitial := []cutoverEvent{
		{
			"registration",
			"SYN-235-JAVA-C",
			cutoverRegisteredTopic,
			0,
			"2026-12-31",
			"Java C",
		},
		{
			"termination",
			"SYN-235-BASE-A",
			cutoverTerminatedTopic,
			1,
			"2026-06-30",
			"Base A",
		},
		{
			"registration",
			"SYN-235-JAVA-C",
			cutoverRegisteredTopic,
			0,
			"2026-12-31",
			"Java C",
		},
		{
			"termination",
			"SYN-235-BASE-B",
			cutoverTerminatedTopic,
			0,
			"2026-05-31",
			"Base B",
		},
		{
			"registration",
			"SYN-235-BASE-B",
			cutoverRegisteredTopic,
			1,
			"2026-12-31",
			"Base B",
		},
	}
	sequence = append(sequence, runCutoverEvents(t, ctx, client, javaAddress, javaInitial)...)
	javaOffsets := waitForCutoverDrain(t, ctx, client)
	phases = append(phases, captureCutoverPhase(t, ctx, client, "java-established", "java", 2))

	queryOnly := startCutoverGoService(t, environment, false)
	waitForTransferQuery(t, ctx, queryOnly.httpAddress+"/policies", "SYN-235-JAVA-C",
		cutoverDocument("SYN-235-JAVA-C", "2026-12-31", "Java C"))
	waitForCutoverMembers(t, ctx, client, 2)
	phases = append(phases, captureCutoverPhase(t, ctx, client, "go-query-only", "java", 2))

	javaGateway, javaGatewayAddress := startLegacyGateway(
		t,
		ctx,
		environment,
		"java",
		javaAddress,
		"SYN-235-JAVA-C",
	)
	javaEdge := observeGateway(
		t,
		ctx,
		javaGatewayAddress,
		"SYN-235-JAVA-C",
		http.StatusInternalServerError,
	)
	javaEdge.Backend = "java"
	edge = append(edge, javaEdge)
	goGateway, goGatewayAddress := startLegacyGateway(
		t,
		ctx,
		environment,
		"go",
		queryOnly.httpAddress,
		"SYN-235-JAVA-C",
	)
	goEdge := observeGateway(
		t,
		ctx,
		goGatewayAddress,
		"SYN-235-JAVA-C",
		http.StatusInternalServerError,
	)
	goEdge.Backend = "go-query-only"
	edge = append(edge, goEdge)
	stopGoService(t, queryOnly)
	stopContainer(t, environment.docker, goGateway)

	// when: quiesce Java, append a backlog, and transfer the unchanged group to Go.
	stopContainer(t, environment.docker, javaName)
	waitForCutoverMembers(t, ctx, client, 0)
	failure := waitForGatewayFallback(t, ctx, javaGatewayAddress)
	edge[0].FailureBody = failure
	stopContainer(t, environment.docker, javaGateway)
	if got := cutoverCommittedOffsets(t, ctx, client); !reflect.DeepEqual(got, javaOffsets) {
		t.Fatalf(
			"offsets changed while Java was quiesced: got=%v want=%v",
			got,
			javaOffsets,
		)
	}

	backlog := []cutoverEvent{
		{
			"registration",
			"SYN-235-BACKLOG-D",
			cutoverRegisteredTopic,
			0,
			"2027-12-31",
			"Backlog D",
		},
		{
			"termination",
			"SYN-235-BASE-A",
			cutoverTerminatedTopic,
			1,
			"2026-04-30",
			"Base A",
		},
	}
	sequence = append(sequence, produceCutoverEvents(t, ctx, client, backlog)...)
	backlogPhase := captureCutoverPhase(t, ctx, client, "handover-backlog", "none", 0)
	if totalBacklog(backlogPhase.Backlog) != int64(len(backlog)) {
		t.Fatalf("handover backlog = %v, want %d", backlogPhase.Backlog, len(backlog))
	}
	phases = append(phases, backlogPhase)

	goOwner := startCutoverGoService(t, environment, true)
	waitForCutoverMembers(t, ctx, client, 2)
	goStartOffsets := waitForCutoverDrain(t, ctx, client)
	waitForCutoverQuery(t, ctx, goOwner.httpAddress, backlog)
	assertOffsetsNeverRegressed(t, javaOffsets, goStartOffsets)
	phases = append(phases, captureCutoverPhase(t, ctx, client, "go-owner", "go", 2))

	goEvents := []cutoverEvent{
		{"registration", "SYN-235-GO-E", cutoverRegisteredTopic, 1, "2027-12-31", "Go E"},
		{
			"termination",
			"SYN-235-BASE-B",
			cutoverTerminatedTopic,
			0,
			"2026-03-31",
			"Base B",
		},
	}
	sequence = append(
		sequence,
		runCutoverEvents(t, ctx, client, goOwner.httpAddress, goEvents)...)
	waitForCutoverDrain(t, ctx, client)
	stopGoService(t, goOwner)
	waitForCutoverMembers(t, ctx, client, 0)
	goOffsets := cutoverCommittedOffsets(t, ctx, client)
	phases = append(phases, captureCutoverPhase(t, ctx, client, "go-drained", "none", 0))

	returnBacklog := []cutoverEvent{
		{
			"registration",
			"SYN-235-RETURN-F",
			cutoverRegisteredTopic,
			0,
			"2028-12-31",
			"Return F",
		},
		{"termination", "SYN-235-GO-E", cutoverTerminatedTopic, 1, "2026-02-28", "Go E"},
	}
	sequence = append(sequence, produceCutoverEvents(t, ctx, client, returnBacklog)...)
	returnPhase := captureCutoverPhase(t, ctx, client, "return-backlog", "none", 0)
	if totalBacklog(returnPhase.Backlog) != int64(len(returnBacklog)) {
		t.Fatalf("return backlog = %v, want %d", returnPhase.Backlog, len(returnBacklog))
	}
	phases = append(phases, returnPhase)

	returnedJavaName, returnedJavaAddress := startLegacyPolicySearch(
		t,
		ctx,
		environment,
		"return",
	)
	waitForCutoverMembers(t, ctx, client, 2)
	returnedOffsets := waitForCutoverDrain(t, ctx, client)
	waitForCutoverQuery(t, ctx, returnedJavaAddress, returnBacklog)
	assertOffsetsNeverRegressed(t, goOffsets, returnedOffsets)
	phases = append(phases, captureCutoverPhase(t, ctx, client, "java-restored", "java", 2))

	returnedEvents := []cutoverEvent{
		{
			"registration",
			"SYN-235-RETURN-G",
			cutoverRegisteredTopic,
			1,
			"2028-12-31",
			"Return G",
		},
		{
			"termination",
			"SYN-235-RETURN-F",
			cutoverTerminatedTopic,
			0,
			"2026-01-31",
			"Return F",
		},
	}
	sequence = append(
		sequence,
		runCutoverEvents(t, ctx, client, returnedJavaAddress, returnedEvents)...)
	finalOffsets := waitForCutoverDrain(t, ctx, client)
	returnGateway, returnGatewayAddress := startLegacyGateway(
		t, ctx, environment, "return", returnedJavaAddress, "SYN-235-RETURN-F",
	)
	returnEdge := observeGateway(
		t,
		ctx,
		returnGatewayAddress,
		"SYN-235-RETURN-F",
		http.StatusInternalServerError,
	)
	returnEdge.Backend = "java-restored"
	edge = append(edge, returnEdge)
	stopContainer(t, environment.docker, returnGateway)
	stopContainer(t, environment.docker, returnedJavaName)
	waitForCutoverMembers(t, ctx, client, 0)
	phases = append(phases, captureCutoverPhase(t, ctx, client, "return-drained", "none", 0))

	// then
	wantFinal := map[string]map[string]any{
		"SYN-235-BASE-A": cutoverDocument("SYN-235-BASE-A", "2026-04-30", "Base A"),
		"SYN-235-BASE-B": cutoverDocument("SYN-235-BASE-B", "2026-03-31", "Base B"),
		"SYN-235-JAVA-C": cutoverDocument("SYN-235-JAVA-C", "2026-12-31", "Java C"),
		"SYN-235-BACKLOG-D": cutoverDocument(
			"SYN-235-BACKLOG-D",
			"2027-12-31",
			"Backlog D",
		),
		"SYN-235-GO-E":     cutoverDocument("SYN-235-GO-E", "2026-02-28", "Go E"),
		"SYN-235-RETURN-F": cutoverDocument("SYN-235-RETURN-F", "2026-01-31", "Return F"),
		"SYN-235-RETURN-G": cutoverDocument("SYN-235-RETURN-G", "2028-12-31", "Return G"),
	}
	gotFinal := make(map[string]map[string]any, len(wantFinal))
	for number := range wantFinal {
		gotFinal[number] = getCutoverDocument(t, environment.elasticsearch, number)
	}
	if !reflect.DeepEqual(gotFinal, wantFinal) {
		t.Fatalf("final documents = %#v, want %#v", gotFinal, wantFinal)
	}
	if err := validateTransferState(true, true, 2, "go"); err == nil {
		t.Fatal("competing Java and Go authority was accepted")
	}
	if len(sequence) < 2 ||
		validateCutoverSequence(sequence[:len(sequence)-1], finalOffsets) == nil {
		t.Fatal("omitted cutover event was accepted")
	}
	if err := validateCutoverSequence(sequence, finalOffsets); err != nil {
		t.Fatal(err)
	}

	writeCutoverReport(t, environment.report, cutoverReport{
		SchemaVersion: 2,
		Ownership:     cutoverOwnership,
		Resources: map[string]string{
			"index":           cutoverIndex,
			"registeredTopic": cutoverRegisteredTopic,
			"terminatedTopic": cutoverTerminatedTopic,
			"consumerGroup":   cutoverConsumerGroup,
		},
		Images: map[string]string{
			"legacyPolicySearch": environment.legacyPolicySearchIdentity,
			"legacyGateway":      environment.legacyGatewayIdentity,
		},
		Starting: starting,
		Sequence: sequence,
		Phases:   phases,
		Edge:     edge,
		Final:    gotFinal,
		Sensitivity: map[string]string{
			"overlappingAuthority":     "rejected",
			"omittedEvent":             "rejected",
			"missingGatewayCredential": "rejected",
			"invalidKafkaTrust":        "covered by the event fixture suite",
		},
		Cleanup: "owned containers are registered for bounded forced cleanup after normal stops",
		Limit:   "disposable loopback Kafka/Elasticsearch with actual legacy images and browser-shaped HTTP; no live local-dev/QA mutation or interactive browser UI",
	})
}

type cutoverEvent struct {
	eventType string
	number    string
	topic     string
	partition int32
	dateTo    string
	holder    string
}

func cutoverEnvironmentFromProcess(t *testing.T) cutoverEnvironment {
	t.Helper()
	if os.Getenv("POLICY_SEARCH_TEST_CUTOVER_OWNERSHIP") != cutoverOwnership ||
		os.Getenv(
			"POLICY_SEARCH_TEST_CUTOVER_REGISTERED_TOPIC",
		) != cutoverRegisteredTopic ||
		os.Getenv(
			"POLICY_SEARCH_TEST_CUTOVER_TERMINATED_TOPIC",
		) != cutoverTerminatedTopic ||
		os.Getenv("POLICY_SEARCH_TEST_CUTOVER_CONSUMER_GROUP") != cutoverConsumerGroup {
		t.Fatal("cutover coordinates differ")
	}
	environment := cutoverEnvironment{
		transferEnvironment: transferEnvironment{
			broker:        os.Getenv("POLICY_SEARCH_TEST_KAFKA_BROKERS"),
			elasticsearch: os.Getenv("POLICY_SEARCH_TEST_ELASTICSEARCH_URL"),
			caFile:        os.Getenv("POLICY_SEARCH_TEST_KAFKA_CA_FILE"),
			report:        os.Getenv("POLICY_SEARCH_TEST_TRANSFER_REPORT"),
		},
		docker:         os.Getenv("POLICY_SEARCH_TEST_DOCKER"),
		javaTruststore: os.Getenv("POLICY_SEARCH_TEST_JAVA_TRUSTSTORE"),
		legacyPolicySearchImage: os.Getenv(
			"POLICY_SEARCH_TEST_LEGACY_POLICY_SEARCH_IMAGE",
		),
		legacyPolicySearchIdentity: os.Getenv(
			"POLICY_SEARCH_TEST_LEGACY_POLICY_SEARCH_IDENTITY",
		),
		legacyGatewayImage:    os.Getenv("POLICY_SEARCH_TEST_LEGACY_GATEWAY_IMAGE"),
		legacyGatewayIdentity: os.Getenv("POLICY_SEARCH_TEST_LEGACY_GATEWAY_IDENTITY"),
	}
	for name, value := range map[string]string{
		"broker": environment.broker, "elasticsearch": environment.elasticsearch,
		"caFile": environment.caFile, "report": environment.report, "docker": environment.docker,
		"javaTruststore":             environment.javaTruststore,
		"legacyPolicySearchImage":    environment.legacyPolicySearchImage,
		"legacyPolicySearchIdentity": environment.legacyPolicySearchIdentity,
		"legacyGatewayImage":         environment.legacyGatewayImage,
		"legacyGatewayIdentity":      environment.legacyGatewayIdentity,
	} {
		if value == "" {
			t.Fatalf("cutover environment %s is required", name)
		}
	}
	if !filepath.IsAbs(environment.report) || !filepath.IsAbs(environment.javaTruststore) {
		t.Fatal("cutover report and Java truststore must be absolute")
	}
	return environment
}

func createCutoverTopics(t *testing.T, ctx context.Context, client *kgo.Client) {
	t.Helper()
	request := kmsg.NewPtrCreateTopicsRequest()
	request.Topics = []kmsg.CreateTopicsRequestTopic{
		{Topic: cutoverRegisteredTopic, NumPartitions: 2, ReplicationFactor: 1},
		{Topic: cutoverTerminatedTopic, NumPartitions: 2, ReplicationFactor: 1},
	}
	response, err := request.RequestWith(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	for _, topic := range response.Topics {
		if topic.ErrorCode != 0 {
			t.Fatalf("create cutover topic %s: code=%d", topic.Topic, topic.ErrorCode)
		}
	}
}

func createCutoverIndex(t *testing.T, endpoint string) {
	t.Helper()
	fixtureRequest(t, http.MethodPut, endpoint+"/"+cutoverIndex, `{
		"settings":{"number_of_shards":1,"number_of_replicas":0},
		"mappings":{"properties":{"number":{"type":"keyword"},"policyHolder":{"type":"text"},"dateFrom":{"type":"date","format":"strict_date"},"dateTo":{"type":"date","format":"strict_date"}}}
	}`)
	t.Cleanup(func() { fixtureRequest(t, http.MethodDelete, endpoint+"/"+cutoverIndex, "") })
}

func cutoverDocument(number, dateTo, holder string) map[string]any {
	return map[string]any{
		"number":       number,
		"dateFrom":     "2026-01-01",
		"dateTo":       dateTo,
		"policyHolder": holder,
	}
}

func putCutoverDocument(t *testing.T, endpoint, number string, document map[string]any) {
	t.Helper()
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	fixtureRequest(
		t,
		http.MethodPut,
		endpoint+"/"+cutoverIndex+"/_doc/"+url.PathEscape(number)+"?refresh=true",
		string(payload),
	)
}

func getCutoverDocument(t *testing.T, endpoint, number string) map[string]any {
	t.Helper()
	response, err := http.Get(endpoint + "/" + cutoverIndex + "/_doc/" + url.PathEscape(number))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get cutover document %s returned %d", number, response.StatusCode)
	}
	var result struct {
		Source map[string]any `json:"_source"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result.Source
}

func startCutoverGoService(
	t *testing.T,
	environment cutoverEnvironment,
	workers bool,
) runningGoService {
	t.Helper()
	settings := testConfig(t)
	settings.ProbeTimeout = 10 * time.Second
	settings.Elasticsearch.URL = environment.elasticsearch
	settings.Elasticsearch.Index = cutoverIndex
	settings.Elasticsearch.Timeout = 5 * time.Second
	settings.Events = config.Events{
		Enabled:         workers,
		Brokers:         []string{environment.broker},
		Group:           cutoverConsumerGroup,
		RegisteredTopic: cutoverRegisteredTopic,
		TerminatedTopic: cutoverTerminatedTopic,
		CAFile:          environment.caFile,
		Writer:          settings.Elasticsearch,
	}
	deps := productionDependencies()
	productionProjectionFactory := deps.newProjection
	deps.newProjection = func(settings config.Elasticsearch, observer *telemetry.DependencyObserver) (projectionRuntime, error) {
		projection, err := productionProjectionFactory(settings, observer)
		if err != nil {
			return nil, err
		}
		production, ok := projection.(*productionProjection)
		if !ok {
			return nil, fmt.Errorf("production projection type differs")
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
	serviceCtx, stop := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- run(serviceCtx, settings, discardLogger(), deps) }()
	_ = receiveString(t, addresses)
	httpAddress := "http://" + receiveString(t, addresses)
	healthAddress := "http://" + receiveString(t, addresses)
	waitForTransferReady(t, healthAddress+"/readyz", result)
	return runningGoService{
		httpAddress:   httpAddress,
		healthAddress: healthAddress,
		stop:          stop,
		result:        result,
	}
}

func stopGoService(t *testing.T, service runningGoService) {
	t.Helper()
	service.stop()
	if err := receiveError(t, service.result); err != nil {
		t.Fatal(err)
	}
}

func startLegacyPolicySearch(
	t *testing.T,
	ctx context.Context,
	environment cutoverEnvironment,
	phase string,
) (string, string) {
	t.Helper()
	port := freeLoopbackPort(t)
	name := fmt.Sprintf("issue235-java-%s-%d-%d", phase, os.Getpid(), port)
	_, kafkaPort, err := net.SplitHostPort(environment.broker)
	if err != nil {
		t.Fatal(err)
	}
	elasticsearchURL, err := url.Parse(environment.elasticsearch)
	if err != nil {
		t.Fatal(err)
	}
	runDocker(
		t,
		ctx,
		environment.docker,
		"run",
		"--detach",
		"--name",
		name,
		"--network",
		"host",
		"--volume",
		environment.javaTruststore+":/issue235/truststore.p12:ro",
		"--env",
		"MICRONAUT_SERVER_HOST=127.0.0.1",
		"--env",
		fmt.Sprintf("MICRONAUT_SERVER_PORT=%d", port),
		"--env",
		"TRACING_ZIPKIN_ENABLED=false",
		"--env",
		"ELASTIC_SCHEME=http",
		"--env",
		"ELASTIC_HOST=127.0.0.1",
		"--env",
		"ELASTIC_PORT="+elasticsearchURL.Port(),
		"--env",
		"KAFKA_HOST=127.0.0.1",
		"--env",
		"KAFKA_PORT="+kafkaPort,
		"--env",
		"KAFKA_SECURITY_PROTOCOL=SSL",
		"--env",
		"KAFKA_SSL_TRUSTSTORE_LOCATION=/issue235/truststore.p12",
		"--env",
		"KAFKA_SSL_TRUSTSTORE_PASSWORD=changeit",
		"--env",
		"KAFKA_SSL_TRUSTSTORE_TYPE=PKCS12",
		environment.legacyPolicySearchImage,
	)
	t.Cleanup(func() {
		if t.Failed() {
			logContext, logCancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer logCancel()
			if output, err := exec.CommandContext(logContext, environment.docker, "logs", "--tail", "120", name).
				CombinedOutput(); err == nil {
				t.Logf("legacy Policy Search logs for %s:\n%s", phase, output)
			}
		}
		removeContainer(environment.docker, name)
	})
	address := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForHTTP(t, ctx, address+"/policies?q=NO_MATCH", http.StatusOK, "")
	return name, address
}

func startLegacyGateway(
	t *testing.T,
	ctx context.Context,
	environment cutoverEnvironment,
	phase, backend, probe string,
) (string, string) {
	t.Helper()
	port := freeLoopbackPort(t)
	name := fmt.Sprintf("issue235-gateway-%s-%d-%d", phase, os.Getpid(), port)
	directory := t.TempDir()
	configuration := fmt.Sprintf(`micronaut:
  server:
    host: 127.0.0.1
    port: %d
  http:
    services:
      policy-search-service:
        urls:
          - %s
  security:
    token:
      jwt:
        signatures:
          secret:
            generator:
              secret: %s
tracing:
  zipkin:
    enabled: false
`, port, backend, cutoverGatewaySecret)
	configurationPath := filepath.Join(directory, "application.yml")
	if err := os.WriteFile(configurationPath, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	runDocker(t, ctx, environment.docker,
		"run", "--detach", "--name", name, "--network", "host",
		"--volume", configurationPath+":/issue235/application.yml:ro",
		"--env", "MICRONAUT_CONFIG_FILES=/issue235/application.yml",
		environment.legacyGatewayImage,
	)
	t.Cleanup(func() {
		if t.Failed() {
			logContext, logCancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer logCancel()
			if output, err := exec.CommandContext(logContext, environment.docker, "logs", "--tail", "120", name).
				CombinedOutput(); err == nil {
				t.Logf("legacy gateway logs for %s:\n%s", phase, output)
			}
		}
		removeContainer(environment.docker, name)
	})
	address := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitForHTTP(
		t,
		ctx,
		address+"/api/policies?q="+url.QueryEscape(`number:"`+probe+`"`),
		http.StatusOK,
		cutoverJWT(t),
	)
	return name, address
}

func runDocker(t *testing.T, ctx context.Context, docker string, arguments ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, docker, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func stopContainer(t *testing.T, docker, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, docker, "stop", "--time", "10", name)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("stop container %s: %v: %s", name, err, output)
	}
}

func removeContainer(docker, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, docker, "rm", "--force", name).Run()
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func cutoverJWT(t *testing.T) string {
	t.Helper()
	encode := base64.RawURLEncoding.EncodeToString
	header := encode([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(map[string]any{
		"sub":   "synthetic-agent",
		"roles": []string{"CAR"},
		"iss":   "auth-service",
		"iat":   time.Now().Unix(),
		"nbf":   time.Now().Add(-time.Minute).Unix(),
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := encode(payloadBytes)
	unsigned := header + "." + payload
	signer := hmac.New(sha256.New, []byte(cutoverGatewaySecret))
	_, _ = signer.Write([]byte(unsigned))
	return unsigned + "." + encode(signer.Sum(nil))
}

func waitForHTTP(t *testing.T, ctx context.Context, target string, status int, token string) {
	t.Helper()
	deadline := time.Now().Add(35 * time.Second)
	last := "no response"
	for time.Now().Before(deadline) && ctx.Err() == nil {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := (&http.Client{Timeout: 8 * time.Second}).Do(request)
		if err == nil {
			last = fmt.Sprintf("status=%d", response.StatusCode)
			_ = response.Body.Close()
			if response.StatusCode == status {
				return
			}
		} else {
			last = err.Error()
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s did not return %d; last=%s", target, status, last)
}

func browserRequest(t *testing.T, ctx context.Context, address, query, token string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		address+"/api/policies?q="+url.QueryEscape(query),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "issue-235-browser-rehearsal")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body json.RawMessage
	if err := json.NewDecoder(response.Body).
		Decode(&body); err != nil &&
		response.StatusCode == http.StatusOK {
		t.Fatal(err)
	}
	return response.StatusCode, string(body)
}

func observeGateway(
	t *testing.T,
	ctx context.Context,
	address, number string,
	wantZeroStatus int,
) cutoverEdgeObservation {
	t.Helper()
	token := cutoverJWT(t)
	status, body := browserRequest(t, ctx, address, `number:"`+number+`"`, token)
	if status != http.StatusOK || !strings.Contains(body, number) {
		t.Fatalf("authenticated gateway query status=%d body=%s", status, body)
	}
	denied, _ := browserRequest(t, ctx, address, `number:"`+number+`"`, "")
	if denied != http.StatusUnauthorized {
		t.Fatalf("unauthenticated gateway status=%d", denied)
	}
	zeroStatus, zeroBody := browserRequest(t, ctx, address, "NO_MATCH_235", token)
	if zeroStatus != wantZeroStatus || (zeroStatus == http.StatusOK && zeroBody != "{}") {
		t.Fatalf(
			"gateway zero-hit status=%d body=%s, want status=%d",
			zeroStatus,
			zeroBody,
			wantZeroStatus,
		)
	}
	return cutoverEdgeObservation{
		Authenticated: status,
		Denied:        denied,
		ZeroHitStatus: zeroStatus,
		ZeroHitBody:   zeroBody,
	}
}

func waitForGatewayFallback(t *testing.T, ctx context.Context, address string) string {
	t.Helper()
	status, body := browserRequest(t, ctx, address, "BACKEND_UNAVAILABLE_235", cutoverJWT(t))
	if status != http.StatusOK || body != "{}" {
		t.Fatalf("gateway fallback status=%d body=%s", status, body)
	}
	return body
}

func produceCutoverEvents(
	t *testing.T,
	ctx context.Context,
	client *kgo.Client,
	events []cutoverEvent,
) []transferSequenceItem {
	t.Helper()
	items := make([]transferSequenceItem, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(map[string]any{"policy": map[string]any{
			"number": event.number, "from": "2026-01-01", "to": event.dateTo,
			"policyHolder": event.holder, "productCode": "SYNTHETIC",
			"totalPremium": 235.01, "agentLogin": "issue-235-agent",
		}})
		if err != nil {
			t.Fatal(err)
		}
		record := &kgo.Record{
			Topic:     event.topic,
			Partition: event.partition,
			Key:       []byte(event.number),
			Value:     payload,
			Headers: []kgo.RecordHeader{
				{Key: "event-type", Value: []byte(event.eventType)},
			},
		}
		results := client.ProduceSync(ctx, record)
		if err := results.FirstErr(); err != nil {
			t.Fatal(err)
		}
		produced := results[0].Record
		items = append(items, transferSequenceItem{
			EventType: event.eventType, Number: event.number,
			Topic: event.topic, Partition: produced.Partition, Offset: produced.Offset,
		})
	}
	return items
}

func runCutoverEvents(
	t *testing.T,
	ctx context.Context,
	client *kgo.Client,
	address string,
	events []cutoverEvent,
) []transferSequenceItem {
	t.Helper()
	items := make([]transferSequenceItem, 0, len(events))
	for _, event := range events {
		items = append(
			items,
			produceCutoverEvents(t, ctx, client, []cutoverEvent{event})...)
		waitForCutoverQuery(t, ctx, address, []cutoverEvent{event})
	}
	return items
}

func waitForCutoverQuery(t *testing.T, ctx context.Context, address string, events []cutoverEvent) {
	t.Helper()
	for _, event := range events {
		waitForTransferQuery(t, ctx, address+"/policies", event.number,
			cutoverDocument(event.number, event.dateTo, event.holder))
	}
}

func cutoverCommittedOffsets(
	t *testing.T,
	ctx context.Context,
	client *kgo.Client,
) map[string]int64 {
	t.Helper()
	request := kmsg.NewPtrOffsetFetchRequest()
	request.Version = 7
	request.Group = cutoverConsumerGroup
	response, err := request.RequestWith(ctx, client)
	if err != nil || response.ErrorCode != 0 {
		t.Fatalf("fetch cutover offsets: response=%v err=%v", response, err)
	}
	offsets := map[string]int64{}
	for _, topic := range response.Topics {
		for _, partition := range topic.Partitions {
			if partition.ErrorCode != 0 {
				t.Fatalf("cutover topic offsets = %+v", topic)
			}
			offsets[offsetKey(topic.Topic, partition.Partition)] = partition.Offset
		}
	}
	return offsets
}

func cutoverEndOffsets(t *testing.T, ctx context.Context, client *kgo.Client) map[string]int64 {
	t.Helper()
	request := kmsg.NewPtrListOffsetsRequest()
	request.ReplicaID = -1
	for _, topic := range []string{cutoverRegisteredTopic, cutoverTerminatedTopic} {
		request.Topics = append(request.Topics, kmsg.ListOffsetsRequestTopic{
			Topic: topic,
			Partitions: []kmsg.ListOffsetsRequestTopicPartition{
				{Partition: 0, CurrentLeaderEpoch: -1, Timestamp: -1},
				{Partition: 1, CurrentLeaderEpoch: -1, Timestamp: -1},
			},
		})
	}
	response, err := request.RequestWith(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]int64{}
	for _, topic := range response.Topics {
		for _, partition := range topic.Partitions {
			if partition.ErrorCode != 0 {
				t.Fatalf(
					"list end offset for %s/%d: code=%d",
					topic.Topic,
					partition.Partition,
					partition.ErrorCode,
				)
			}
			result[offsetKey(topic.Topic, partition.Partition)] = partition.Offset
		}
	}
	return result
}

func offsetKey(
	topic string,
	partition int32,
) string {
	return fmt.Sprintf("%s/%d", topic, partition)
}

func cutoverBacklog(committed, ends map[string]int64) map[string]int64 {
	backlog := make(map[string]int64, len(ends))
	for key, end := range ends {
		offset := committed[key]
		if offset < 0 {
			offset = 0
		}
		backlog[key] = end - offset
	}
	return backlog
}

func totalBacklog(backlog map[string]int64) int64 {
	var total int64
	for _, value := range backlog {
		total += value
	}
	return total
}

func waitForCutoverDrain(t *testing.T, ctx context.Context, client *kgo.Client) map[string]int64 {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		committed := cutoverCommittedOffsets(t, ctx, client)
		ends := cutoverEndOffsets(t, ctx, client)
		if len(committed) == 4 && totalBacklog(cutoverBacklog(committed, ends)) == 0 {
			return committed
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf(
		"cutover group did not drain: committed=%v ends=%v",
		cutoverCommittedOffsets(t, ctx, client),
		cutoverEndOffsets(t, ctx, client),
	)
	return nil
}

func waitForCutoverMembers(t *testing.T, ctx context.Context, client *kgo.Client, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	last := "no response"
	for time.Now().Before(deadline) && ctx.Err() == nil {
		request := kmsg.NewPtrDescribeGroupsRequest()
		request.Version = 4
		request.Groups = []string{cutoverConsumerGroup}
		response, err := request.RequestWith(ctx, client)
		last = fmt.Sprintf("response=%+v err=%v", response, err)
		if err == nil && len(response.Groups) == 1 &&
			len(response.Groups[0].Members) == want {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	list := kmsg.NewPtrListGroupsRequest()
	groups, err := list.RequestWith(ctx, client)
	t.Fatalf(
		"cutover group member count did not become %d; last %s; groups=%+v err=%v",
		want,
		last,
		groups,
		err,
	)
}

func captureCutoverPhase(
	t *testing.T,
	ctx context.Context,
	client *kgo.Client,
	name, owner string,
	members int,
) cutoverPhase {
	t.Helper()
	committed := cutoverCommittedOffsets(t, ctx, client)
	ends := cutoverEndOffsets(t, ctx, client)
	return cutoverPhase{
		Name: name, Owner: owner, Members: members, CommittedOffsets: committed,
		EndOffsets: ends, Backlog: cutoverBacklog(committed, ends),
	}
}

func assertOffsetsNeverRegressed(t *testing.T, before, after map[string]int64) {
	t.Helper()
	for key, offset := range before {
		if after[key] < offset {
			t.Fatalf("offset %s regressed from %d to %d", key, offset, after[key])
		}
	}
}

func validateCutoverSequence(sequence []transferSequenceItem, finalOffsets map[string]int64) error {
	if len(sequence) != 13 {
		return fmt.Errorf("cutover sequence length=%d, want 13", len(sequence))
	}
	seen := map[string]int64{}
	for _, item := range sequence {
		key := offsetKey(item.Topic, item.Partition)
		if previous, ok := seen[key]; ok && item.Offset != previous+1 {
			return fmt.Errorf(
				"non-contiguous offset for %s: previous=%d current=%d",
				key,
				previous,
				item.Offset,
			)
		}
		seen[key] = item.Offset
	}
	for key, last := range seen {
		if finalOffsets[key] != last+1 {
			return fmt.Errorf(
				"final committed offset %s=%d, want %d",
				key,
				finalOffsets[key],
				last+1,
			)
		}
	}
	return nil
}

func writeCutoverReport(t *testing.T, path string, report cutoverReport) {
	t.Helper()
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), environmentSecretMarker()) ||
		strings.Contains(string(payload), cutoverGatewaySecret) {
		t.Fatal("cutover report contains a prohibited marker")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
