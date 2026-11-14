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

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
)

const validRegistration = `{"policy":{"number":"SYN-363","from":[2026,2,3],` +
	`"to":[2027,2,2],"policyHolder":"Synthetic Holder","productCode":"HSI",` +
	`"totalPremium":9007199254740993.1234567,"agentLogin":null}}`

func TestDecode_PreservesAcceptedRegistrationMapping(t *testing.T) {
	for _, test := range []struct {
		name    string
		key     string
		payload string
		premium string
	}{
		{"string identity and numeric premium", "SYN-363", validRegistration, "9007199254740993.1234567"},
		{
			"numeric identity and quoted premium",
			"363",
			`{"unknown":true,"policy":{"number":363,"from":[2026,2,3],` +
				`"to":[2027,2,2],"policyHolder":"Synthetic Holder","productCode":"HSI",` +
				`"totalPremium":"12.3400"}}`,
			"12.3400",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			got, err := decode([]byte(test.key), []byte(test.payload))
			// then
			if err != nil {
				t.Fatal(err)
			}
			if got.Number() != test.key || got.From().Parts() != [3]int{2026, 2, 3} ||
				got.To().Parts() != [3]int{2027, 2, 2} ||
				got.PolicyHolder() != "Synthetic Holder" || got.ProductCode() != "HSI" ||
				got.TotalPremium() != test.premium || got.AgentLogin() != nil {
				t.Fatalf("decoded registration = %+v", got)
			}
		})
	}
}

func TestDecode_RejectsInvalidRecords(t *testing.T) {
	tests := []struct {
		name    string
		key     []byte
		payload []byte
	}{
		{"missing key", nil, []byte(validRegistration)},
		{"mismatched key", []byte("OTHER"), []byte(validRegistration)},
		{"malformed envelope", []byte("SYN-363"), []byte(`{"policy":`)},
		{
			"invalid UTF-8 text",
			[]byte("SYN-363"),
			bytes.Replace(
				[]byte(validRegistration),
				[]byte("Synthetic Holder"),
				[]byte{'S', 0xff},
				1,
			),
		},
		{"missing policy", []byte("SYN-363"), []byte(`{}`)},
		{
			"missing holder",
			[]byte("SYN-363"),
			[]byte(`{"policy":{"number":"SYN-363","from":[2026,2,3],` +
				`"to":[2027,2,2],"productCode":"HSI","totalPremium":1}}`),
		},
		{
			"invalid date",
			[]byte("SYN-363"),
			[]byte(`{"policy":{"number":"SYN-363","from":[2026,2,30],` +
				`"to":[2027,2,2],"policyHolder":"holder","productCode":"HSI",` +
				`"totalPremium":1}}`),
		},
		{
			"fractional numeric identity",
			[]byte("363.1"),
			[]byte(`{"policy":{"number":363.1,"from":[2026,2,3],` +
				`"to":[2027,2,2],"policyHolder":"holder","productCode":"HSI",` +
				`"totalPremium":1}}`),
		},
		{
			"oversized record",
			[]byte("SYN-363"),
			bytes.Repeat([]byte("x"), maximumRecordBytes),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			_, err := decode(test.key, test.payload)

			// then
			if err == nil {
				t.Fatal("invalid registration accepted")
			}
		})
	}
}

func TestWorker_HandlesOneRecordWithTraceAndSafeOutcome(t *testing.T) {
	// given
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeClient{polls: []pollResult{{records: []*kgo.Record{{
		Key: []byte("SYN-363"), Value: []byte(validRegistration),
		Headers: []kgo.RecordHeader{{
			Key: "traceparent",
			Value: []byte(
				"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			),
		}},
	}}}}}
	generated := make(chan trace.SpanContext, 1)
	generator := &fakeGenerator{execute: func(
		operationCtx context.Context,
		_ domain.PolicyRegistration,
	) (application.GenerationResult, error) {
		generated <- trace.SpanContextFromContext(operationCtx)
		cancel()
		return application.GenerationResult{ObjectKey: "2026/02/SYN-363.pdf"}, nil
	}}
	finished := make(chan string, 1)
	worker := newWorker(client, generator, slog.Default())
	worker.propagator = propagation.TraceContext{}
	worker.observe = func(ctx context.Context, eventType string) (
		context.Context,
		func(context.Context, string, error),
	) {
		if eventType != "registered" {
			t.Fatalf("event type = %q", eventType)
		}
		return ctx, func(_ context.Context, outcome string, _ error) { finished <- outcome }
	}

	// when
	err := worker.Run(ctx)
	// then
	if err != nil {
		t.Fatal(err)
	}
	spanContext := <-generated
	if !spanContext.IsRemote() ||
		spanContext.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace context = %v", spanContext)
	}
	if outcome := <-finished; outcome != "success" {
		t.Fatalf("outcome = %q", outcome)
	}
	if client.pollLimit() != 1 || client.allowCount() == 0 {
		t.Fatalf(
			"poll limit = %d, rebalance releases = %d",
			client.pollLimit(),
			client.allowCount(),
		)
	}
}

func TestWorker_HandlerFailureDoesNotLatchReadiness(t *testing.T) {
	// given
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeClient{polls: []pollResult{{records: []*kgo.Record{
		{Key: []byte("SYN-363"), Value: []byte(validRegistration)},
	}}}}
	firstHandled := make(chan struct{})
	generator := &fakeGenerator{execute: func(
		context.Context,
		domain.PolicyRegistration,
	) (application.GenerationResult, error) {
		close(firstHandled)
		cancel()
		return application.GenerationResult{}, errors.New("private dependency failure")
	}}
	worker := newWorker(client, generator, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	done := make(chan error, 1)

	// when
	go func() { done <- worker.Run(ctx) }()
	<-firstHandled
	runErr := <-done
	readinessCtx, cancelReadiness := context.WithTimeout(context.Background(), time.Second)
	defer cancelReadiness()
	readinessErr := worker.Check(readinessCtx)

	// then
	if readinessErr != nil {
		t.Fatalf("readiness error = %v", readinessErr)
	}
	if runErr != nil {
		t.Fatalf("worker run error = %v", runErr)
	}
}

func TestWorker_HandlerFailureStopsFetchedBatch(t *testing.T) {
	// A real poll is capped at one record; this defensive case preserves the established
	// Policy Search and Dashboard rule if a client ever returns a larger batch.
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeClient{polls: []pollResult{{records: []*kgo.Record{
		{Key: []byte("SYN-363"), Value: []byte(validRegistration)},
		{Key: []byte("SYN-363"), Value: []byte(validRegistration)},
	}}}}
	calls := 0
	worker := newWorker(client, &fakeGenerator{execute: func(
		context.Context,
		domain.PolicyRegistration,
	) (application.GenerationResult, error) {
		calls++
		cancel()
		return application.GenerationResult{}, errors.New("private dependency failure")
	}}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))

	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("failed fetched batch handler calls = %d, want 1", calls)
	}
}

func TestWorker_CheckRequiresReachableKafkaAndStableAssignment(t *testing.T) {
	t.Run("Kafka unavailable", func(t *testing.T) {
		client := &fakeClient{pingErr: errors.New("private broker failure")}
		worker := newWorker(
			client,
			&fakeGenerator{},
			slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		)
		err := worker.Check(context.Background())
		if err == nil || !strings.Contains(err.Error(), "Kafka unavailable") {
			t.Fatalf("readiness error = %v", err)
		}
	})

	t.Run("stable assignment", func(t *testing.T) {
		worker := newWorker(&fakeClient{}, &fakeGenerator{}, slog.Default())
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
		if err := worker.Check(ctx); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed < 300*time.Millisecond {
			t.Fatalf("readiness returned after unstable assignment in %s", elapsed)
		}
	})

	t.Run("assignment unavailable", func(t *testing.T) {
		worker := newWorker(
			&fakeClient{},
			&fakeGenerator{},
			slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		)
		worker.assigned.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := worker.Check(ctx)
		if err == nil || !strings.Contains(err.Error(), "group assignment unavailable") {
			t.Fatalf("readiness error = %v", err)
		}
	})
}

func TestKafkaHeaderCarrier_UpdatesAndListsFixedHeaders(t *testing.T) {
	carrier := kafkaHeaderCarrier{{Key: "traceparent", Value: []byte("before")}}
	carrier.Set("traceparent", "after")
	carrier.Set("missing", "ignored")
	if got := carrier.Get("traceparent"); got != "after" {
		t.Fatalf("traceparent = %q", got)
	}
	if keys := carrier.Keys(); len(keys) != 1 || keys[0] != "traceparent" {
		t.Fatalf("keys = %v", keys)
	}
}

func TestWorker_InvalidRecordIsConsumedWithoutCallingGenerator(t *testing.T) {
	// given
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeClient{polls: []pollResult{{records: []*kgo.Record{{
		Key: []byte("OTHER"), Value: []byte(validRegistration),
	}}}}}
	generator := &fakeGenerator{execute: func(
		context.Context,
		domain.PolicyRegistration,
	) (application.GenerationResult, error) {
		t.Fatal("invalid registration reached generation")
		return application.GenerationResult{}, nil
	}}
	worker := newWorker(client, generator, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	worker.observe = func(ctx context.Context, _ string) (
		context.Context,
		func(context.Context, string, error),
	) {
		return ctx, func(_ context.Context, outcome string, _ error) {
			if outcome != "malformed" {
				t.Errorf("outcome = %q", outcome)
			}
			cancel()
		}
	}

	// when
	err := worker.Run(ctx)
	// then
	if err != nil {
		t.Fatalf("invalid record handling = %v", err)
	}
}

func TestWorker_ShutdownCommitsReleasesAndCloses(t *testing.T) {
	// given
	client := &fakeClient{}
	worker := newWorker(client, &fakeGenerator{}, slog.Default())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// when
	err := worker.Shutdown(ctx)

	// then
	if err != nil || !client.committedState() || !client.closedState() ||
		client.allowCount() == 0 {
		t.Fatalf(
			"shutdown = committed:%t closed:%t allowed:%d err:%v",
			client.committedState(),
			client.closedState(),
			client.allowCount(),
			err,
		)
	}
}

func TestWorker_ShutdownBoundsCommitAndCloseFailures(t *testing.T) {
	// given
	release := make(chan struct{})
	commitFailure := errors.New("synthetic commit failure")
	client := &fakeClient{commitErr: commitFailure, closeRelease: release}
	worker := newWorker(client, &fakeGenerator{}, slog.Default())
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
		client.allowCount() == 0 {
		t.Fatalf(
			"bounded shutdown error = %v, rebalance releases = %d",
			err,
			client.allowCount(),
		)
	}
	if eventType := <-commitFailures; eventType != "registered" {
		t.Fatalf("commit failure event type = %q", eventType)
	}
}

func TestNewWorker_DefersConsumerAndRejectsInvalidCA(t *testing.T) {
	// given
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)
	certificate := pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw},
	)
	server.Close()
	settings := Settings{
		Brokers: []string{"kafka.example:9093"}, Group: "document-service",
		Topic: "policy-registered", ClientID: "policy-registered-document-listener",
		CACert: certificate, EventType: "registered",
	}

	// when
	worker, err := NewWorker(settings, &fakeGenerator{}, slog.Default())
	invalid, invalidErr := NewWorker(
		Settings{
			Brokers:   settings.Brokers,
			Group:     settings.Group,
			Topic:     settings.Topic,
			ClientID:  settings.ClientID,
			EventType: settings.EventType,
			CACert:    []byte("private"),
		},
		&fakeGenerator{},
		slog.Default(),
	)

	// then
	if err != nil || worker.client != nil || worker.preflight == nil {
		t.Fatalf("deferred worker = (%v, %v)", worker, err)
	}
	if invalid != nil || invalidErr == nil ||
		!strings.Contains(invalidErr.Error(), "invalid CA") {
		t.Fatalf("invalid CA result = (%v, %v)", invalid, invalidErr)
	}
}

func TestCommittedProgressStable_AllowsOnlyGrowingLogEnd(t *testing.T) {
	// given
	first := map[int32]partitionProgress{0: {start: 1, committed: 2, end: 3}}

	// when / then
	if !committedProgressStable(
		first,
		map[int32]partitionProgress{0: {start: 1, committed: 2, end: 4}},
	) {
		t.Fatal("growing log end rejected")
	}
	if committedProgressStable(
		first,
		map[int32]partitionProgress{0: {start: 1, committed: 3, end: 3}},
	) {
		t.Fatal("changed committed progress accepted")
	}
}

func TestVerifyCommittedProgress_RequiresStableCompleteInRangeSnapshot(t *testing.T) {
	tests := []struct {
		name      string
		client    progressRequestClient
		wantError string
	}{
		{
			name:   "stable committed progress",
			client: progressRequestClient{committed: 4, end: 4},
		},
		{
			name:      "active member",
			client:    progressRequestClient{committed: 4, end: 4, active: true},
			wantError: "group is not inactive",
		},
		{
			name:      "missing commit",
			client:    progressRequestClient{committed: -1, end: 4},
			wantError: "missing group commit",
		},
		{
			name:      "out of range commit",
			client:    progressRequestClient{committed: 5, end: 4},
			wantError: "invalid committed progress",
		},
		{
			name:   "growing log end",
			client: progressRequestClient{committed: 4, end: 4, secondEndBy: 1},
		},
		{
			name:      "decreasing log end",
			client:    progressRequestClient{committed: 4, end: 5, secondEndBy: -1},
			wantError: "Kafka progress changed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := test.client
			err := verifyCommittedProgress(
				context.Background(),
				&client,
				"policy-registered",
				"document-service",
			)
			if test.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantError != "" &&
				(err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf(
					"verifyCommittedProgress() error = %v, want %q",
					err,
					test.wantError,
				)
			}
		})
	}
}

func TestWorker_PreflightConstructsConsumerOnlyAfterGuard(t *testing.T) {
	consumer := &fakeClient{}
	calls := 0
	worker := &Worker{preflight: func(context.Context) (consumerClient, error) {
		calls++
		return consumer, nil
	}}
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

func TestCommittedProgressReadersValidateBrokerResponseShape(t *testing.T) {
	topic := "policy-registered"
	t.Run("legacy committed offsets", func(t *testing.T) {
		client := staticRequestClient{response: &kmsg.OffsetFetchResponse{
			Topics: []kmsg.OffsetFetchResponseTopic{
				{
					Topic: topic,
					Partitions: []kmsg.OffsetFetchResponseTopicPartition{
						{Partition: 0, Offset: 4},
					},
				},
			},
		}}
		got, err := readCommittedOffsets(
			context.Background(), client, topic, "document-service", []int32{0},
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
					{Partition: 0, Leader: 1}, {Partition: 0, Leader: 1},
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
			Topics: []kmsg.MetadataResponseTopic{
				{
					Topic: &topic,
					Partitions: []kmsg.MetadataResponseTopicPartition{
						{Partition: 0, Leader: -1},
					},
				},
			},
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
					{Partition: 0, Offset: 0}, {Partition: 0, Offset: 4},
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
			Topics: []kmsg.ListOffsetsResponseTopic{
				{
					Topic: topic,
					Partitions: []kmsg.ListOffsetsResponseTopicPartition{
						{Partition: 0, Offset: -1},
					},
				},
			},
		}}
		if _, err := readListOffsets(
			context.Background(), client, topic, []int32{0}, -1,
		); err == nil || !strings.Contains(err.Error(), "partition bound failed") {
			t.Fatalf("readListOffsets() error = %v", err)
		}
	})
}

type fakeGenerator struct {
	execute func(context.Context, domain.PolicyRegistration) (application.GenerationResult, error)
}

type progressRequestClient struct {
	start       int64
	committed   int64
	end         int64
	active      bool
	secondEndBy int64
	endReads    int
}

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
			Group: "document-service",
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
			if client.endReads > 0 {
				offset += client.secondEndBy
			}
			client.endReads++
		}
		return &kmsg.ListOffsetsResponse{Topics: []kmsg.ListOffsetsResponseTopic{
			{
				Topic: topic,
				Partitions: []kmsg.ListOffsetsResponseTopicPartition{
					{Partition: 0, Offset: offset},
				},
			},
		}}, nil
	default:
		return nil, errors.New("unexpected request")
	}
}

func (*progressRequestClient) Close() {}

type staticRequestClient struct {
	response kmsg.Response
	err      error
}

func (client staticRequestClient) Request(context.Context, kmsg.Request) (kmsg.Response, error) {
	return client.response, client.err
}

func (staticRequestClient) Close() {}

func (generator *fakeGenerator) Execute(
	ctx context.Context,
	registration domain.PolicyRegistration,
) (application.GenerationResult, error) {
	if generator.execute == nil {
		return application.GenerationResult{}, nil
	}
	return generator.execute(ctx, registration)
}

type fakeClient struct {
	mu             sync.Mutex
	polls          []pollResult
	pollCount      int
	secondPollGate <-chan struct{}
	limit          int
	allowed        int
	committed      bool
	closed         bool
	pingErr        error
	commitErr      error
	closeRelease   <-chan struct{}
}

func (client *fakeClient) Poll(ctx context.Context, limit int) pollResult {
	client.mu.Lock()
	client.limit = limit
	client.pollCount++
	pollCount := client.pollCount
	gate := client.secondPollGate
	if len(client.polls) > 0 {
		result := client.polls[0]
		client.polls = client.polls[1:]
		client.mu.Unlock()
		if pollCount == 2 && gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return pollResult{errors: []error{ctx.Err()}}
			}
		}
		return result
	}
	client.mu.Unlock()
	<-ctx.Done()
	return pollResult{errors: []error{ctx.Err()}}
}

func (client *fakeClient) Ping(context.Context) error { return client.pingErr }

func (client *fakeClient) AllowRebalance() {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.allowed++
}

func (client *fakeClient) CommitUncommittedOffsets(context.Context) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.committed = true
	return client.commitErr
}

func (client *fakeClient) CloseAllowingRebalance() {
	if client.closeRelease != nil {
		<-client.closeRelease
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	client.closed = true
}

func (client *fakeClient) pollLimit() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.limit
}

func (client *fakeClient) allowCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.allowed
}

func (client *fakeClient) committedState() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.committed
}

func (client *fakeClient) closedState() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.closed
}
