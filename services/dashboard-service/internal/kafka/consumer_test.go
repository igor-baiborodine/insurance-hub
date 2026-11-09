package kafka

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
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
	commitFailures := make(chan string, 1)
	worker.commitFailure = func(_ context.Context, eventType string) {
		commitFailures <- eventType
	}
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
	if eventType := <-commitFailures; eventType != "registered" {
		t.Fatalf("commit failure event type = %q", eventType)
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

func TestNewWorker_CanonicalSettingsDeferGroupConsumerUntilPreflight(t *testing.T) {
	// given
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)
	certificate := pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
	})
	server.Close()
	settings := Settings{
		Brokers:                  []string{"kafka.example:9093"},
		Group:                    "dashboard-service",
		Topic:                    "policy-registered",
		ClientID:                 "policy-registered-dashboard-listener",
		CACert:                   certificate,
		EventType:                "registered",
		RequireCommittedProgress: true,
	}

	// when
	worker, err := NewWorker(settings, &fakeProjection{}, slog.Default())
	// then
	if err != nil {
		t.Fatal(err)
	}
	if worker.client != nil || worker.preflight == nil {
		t.Fatalf(
			"canonical worker before preflight = client:%v preflight:%t",
			worker.client,
			worker.preflight != nil,
		)
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

func TestVerifyCanonicalProgress_RequiresStableCompleteInRangeSnapshot(t *testing.T) {
	tests := []struct {
		name      string
		client    progressRequestClient
		wantError string
	}{
		{
			name:   "stable committed progress",
			client: progressRequestClient{start: 0, committed: 4, end: 4},
		},
		{
			name: "active member",
			client: progressRequestClient{
				start:     0,
				committed: 4,
				end:       4,
				active:    true,
			},
			wantError: "group is not inactive",
		},
		{
			name:      "missing commit",
			client:    progressRequestClient{start: 0, committed: -1, end: 4},
			wantError: "missing group commit",
		},
		{
			name:      "out of range commit",
			client:    progressRequestClient{start: 0, committed: 5, end: 4},
			wantError: "invalid committed progress",
		},
		{
			name: "changing bounds",
			client: progressRequestClient{
				start:     0,
				committed: 4,
				end:       4,
				changeEnd: true,
			},
			wantError: "Kafka progress changed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := test.client
			err := verifyCanonicalProgress(
				context.Background(),
				&client,
				"policy-registered",
				"dashboard-service",
			)
			if test.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantError != "" &&
				(err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf(
					"verifyCanonicalProgress() error = %v, want %q",
					err,
					test.wantError,
				)
			}
		})
	}
}

func TestWorker_PreflightConstructsCanonicalConsumerOnlyAfterGuard(t *testing.T) {
	consumer := &fakeClient{}
	calls := 0
	worker := &Worker{preflight: func(context.Context) (consumerClient, error) {
		calls++
		return consumer, nil
	}}
	if worker.client != nil {
		t.Fatal("canonical consumer existed before preflight")
	}
	if err := worker.Preflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	if worker.client != consumer || calls != 1 {
		t.Fatalf("preflight client = %v, calls = %d", worker.client, calls)
	}
	if err := worker.Preflight(context.Background()); err != nil || calls != 1 {
		t.Fatalf("repeated preflight = %v, calls = %d", err, calls)
	}
}

func TestWorker_PreflightFailsClosedWithoutUsableConsumer(t *testing.T) {
	guardFailure := errors.New("guard failed")
	tests := []struct {
		name      string
		worker    *Worker
		wantError string
	}{
		{name: "nil worker", wantError: "worker is required"},
		{
			name: "guard failure",
			worker: &Worker{preflight: func(context.Context) (consumerClient, error) {
				return nil, guardFailure
			}},
			wantError: guardFailure.Error(),
		},
		{
			name: "missing consumer",
			worker: &Worker{preflight: func(context.Context) (consumerClient, error) {
				return nil, nil
			}},
			wantError: "consumer is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.worker.Preflight(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Preflight() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestCanonicalProgressReadersValidateBrokerResponseShape(t *testing.T) {
	topic := "policy-registered"
	t.Run("legacy committed offsets", func(t *testing.T) {
		client := staticRequestClient{response: &kmsg.OffsetFetchResponse{
			Topics: []kmsg.OffsetFetchResponseTopic{{
				Topic: topic,
				Partitions: []kmsg.OffsetFetchResponseTopicPartition{{
					Partition: 0, Offset: 4,
				}},
			}},
		}}
		got, err := readCommittedOffsets(
			context.Background(), client, topic, "dashboard-service", []int32{0},
		)
		if err != nil || got[0] != 4 {
			t.Fatalf("readCommittedOffsets() = (%v, %v)", got, err)
		}
	})
	t.Run("duplicate topic partition", func(t *testing.T) {
		client := staticRequestClient{response: &kmsg.MetadataResponse{
			Topics: []kmsg.MetadataResponseTopic{{
				Topic: &topic,
				Partitions: []kmsg.MetadataResponseTopicPartition{
					{Partition: 0, Leader: 1},
					{Partition: 0, Leader: 1},
				},
			}},
		}}
		if _, err := readTopicPartitions(context.Background(), client, topic); err == nil ||
			!strings.Contains(err.Error(), "duplicate partition") {
			t.Fatalf("readTopicPartitions() error = %v", err)
		}
	})
	t.Run("unavailable topic partition", func(t *testing.T) {
		client := staticRequestClient{response: &kmsg.MetadataResponse{
			Topics: []kmsg.MetadataResponseTopic{{
				Topic: &topic,
				Partitions: []kmsg.MetadataResponseTopicPartition{{
					Partition: 0, Leader: -1,
				}},
			}},
		}}
		if _, err := readTopicPartitions(context.Background(), client, topic); err == nil ||
			!strings.Contains(err.Error(), "partition unavailable") {
			t.Fatalf("readTopicPartitions() error = %v", err)
		}
	})
	t.Run("duplicate partition bound", func(t *testing.T) {
		client := staticRequestClient{response: &kmsg.ListOffsetsResponse{
			Topics: []kmsg.ListOffsetsResponseTopic{{
				Topic: topic,
				Partitions: []kmsg.ListOffsetsResponseTopicPartition{
					{Partition: 0, Offset: 0},
					{Partition: 0, Offset: 4},
				},
			}},
		}}
		if _, err := readListOffsets(
			context.Background(), client, topic, []int32{0}, -1,
		); err == nil || !strings.Contains(err.Error(), "duplicate partition bound") {
			t.Fatalf("readListOffsets() error = %v", err)
		}
	})
	t.Run("failed partition bound", func(t *testing.T) {
		client := staticRequestClient{response: &kmsg.ListOffsetsResponse{
			Topics: []kmsg.ListOffsetsResponseTopic{{
				Topic: topic,
				Partitions: []kmsg.ListOffsetsResponseTopicPartition{{
					Partition: 0, Offset: -1,
				}},
			}},
		}}
		if _, err := readListOffsets(
			context.Background(), client, topic, []int32{0}, -1,
		); err == nil || !strings.Contains(err.Error(), "partition bound failed") {
			t.Fatalf("readListOffsets() error = %v", err)
		}
	})
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

type progressRequestClient struct {
	start     int64
	committed int64
	end       int64
	active    bool
	changeEnd bool
	endReads  int
}

type staticRequestClient struct {
	response kmsg.Response
	err      error
}

func (client staticRequestClient) Request(context.Context, kmsg.Request) (kmsg.Response, error) {
	return client.response, client.err
}

func (staticRequestClient) Close() {}

func (client *progressRequestClient) Request(
	_ context.Context,
	request kmsg.Request,
) (kmsg.Response, error) {
	topic := "policy-registered"
	switch typed := request.(type) {
	case *kmsg.MetadataRequest:
		return &kmsg.MetadataResponse{Topics: []kmsg.MetadataResponseTopic{
			{
				Topic: &topic,
				Partitions: []kmsg.MetadataResponseTopicPartition{
					{Partition: 0, Leader: 1},
				},
			},
		}}, nil
	case *kmsg.DescribeGroupsRequest:
		members := []kmsg.DescribeGroupsResponseGroupMember(nil)
		if client.active {
			members = append(
				members,
				kmsg.DescribeGroupsResponseGroupMember{MemberID: "active"},
			)
		}
		return &kmsg.DescribeGroupsResponse{Groups: []kmsg.DescribeGroupsResponseGroup{{
			Group: typed.Groups[0], State: "Empty", Members: members,
		}}}, nil
	case *kmsg.OffsetFetchRequest:
		return &kmsg.OffsetFetchResponse{Groups: []kmsg.OffsetFetchResponseGroup{{
			Group: "dashboard-service",
			Topics: []kmsg.OffsetFetchResponseGroupTopic{{
				Topic: topic,
				Partitions: []kmsg.OffsetFetchResponseGroupTopicPartition{{
					Partition: 0, Offset: client.committed,
				}},
			}},
		}}}, nil
	case *kmsg.ListOffsetsRequest:
		offset := client.start
		if typed.Topics[0].Partitions[0].Timestamp == -1 {
			offset = client.end
			if client.changeEnd && client.endReads > 0 {
				offset++
			}
			client.endReads++
		}
		return &kmsg.ListOffsetsResponse{Topics: []kmsg.ListOffsetsResponseTopic{{
			Topic: topic,
			Partitions: []kmsg.ListOffsetsResponseTopicPartition{{
				Partition: 0, Offset: offset,
			}},
		}}}, nil
	default:
		return nil, errors.New("unexpected request")
	}
}

func (*progressRequestClient) Close() {}

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
