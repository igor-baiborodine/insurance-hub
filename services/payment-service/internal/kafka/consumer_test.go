package kafka

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

const topic = "policy-registered"

func TestNewWorker_DefersClientAndRejectsInvalidCA(t *testing.T) {
	// given
	settings := Settings{
		Brokers: []string{"kafka.example:9093"}, Topic: topic,
		ClientID: "payment-service", CACert: testCertificate(t),
	}

	// when
	worker, err := NewWorker(settings)
	empty, emptyErr := NewWorker(Settings{
		Brokers: settings.Brokers, Topic: topic, ClientID: settings.ClientID,
		CACert: []byte("  \n"),
	})
	invalid, invalidErr := NewWorker(Settings{
		Brokers: settings.Brokers, Topic: topic, ClientID: settings.ClientID,
		CACert: []byte("private"),
	})

	// then
	if err != nil || worker.preflight == nil {
		t.Fatalf("deferred worker = (%v, %v)", worker, err)
	}
	if empty != nil || emptyErr == nil || !strings.Contains(emptyErr.Error(), "empty CA") {
		t.Fatalf("empty CA result = (%v, %v)", empty, emptyErr)
	}
	if invalid != nil || invalidErr == nil ||
		!strings.Contains(invalidErr.Error(), "invalid CA") {
		t.Fatalf("invalid CA result = (%v, %v)", invalid, invalidErr)
	}
}

func TestReadTopicPartitions_RequestsMetadataWithoutAutoCreation(t *testing.T) {
	// given
	client := &recordingRequestClient{response: &kmsg.MetadataResponse{
		Topics: []kmsg.MetadataResponseTopic{{
			Topic: kmsg.StringPtr(topic),
			Partitions: []kmsg.MetadataResponseTopicPartition{
				{Partition: 0, Leader: 1}, {Partition: 1, Leader: 2},
			},
		}},
	}}

	// when
	got, err := readTopicPartitions(context.Background(), client, topic)

	// then
	if err != nil || !slices.Equal(got, []int32{0, 1}) {
		t.Fatalf("readTopicPartitions() = (%v, %v)", got, err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d, want exactly one", len(client.requests))
	}
	request, ok := client.requests[0].(*kmsg.MetadataRequest)
	if !ok {
		t.Fatalf("request = %T, want *kmsg.MetadataRequest", client.requests[0])
	}
	if request.AllowAutoTopicCreation {
		t.Error("metadata request allows topic auto-creation")
	}
	if len(request.Topics) != 1 || request.Topics[0].Topic == nil ||
		*request.Topics[0].Topic != topic {
		t.Errorf("requested topics = %+v, want only %s", request.Topics, topic)
	}
}

func TestReadTopicPartitions_ValidatesBrokerResponseShape(t *testing.T) {
	const unknownTopicOrPartition = 3
	tests := []struct {
		name      string
		client    *recordingRequestClient
		wantError string
	}{
		{
			"missing topic",
			metadataClient(kmsg.MetadataResponseTopic{
				Topic: kmsg.StringPtr(topic), ErrorCode: unknownTopicOrPartition,
			}),
			"topic unavailable",
		},
		{
			"internal topic",
			metadataClient(kmsg.MetadataResponseTopic{
				Topic:      kmsg.StringPtr(topic),
				IsInternal: true,
				Partitions: []kmsg.MetadataResponseTopicPartition{
					{Partition: 0, Leader: 1},
				},
			}),
			"topic unavailable",
		},
		{
			"topic without partitions",
			metadataClient(kmsg.MetadataResponseTopic{Topic: kmsg.StringPtr(topic)}),
			"topic unavailable",
		},
		{
			"unavailable topic partition",
			metadataClient(kmsg.MetadataResponseTopic{
				Topic: kmsg.StringPtr(topic),
				Partitions: []kmsg.MetadataResponseTopicPartition{
					{Partition: 0, Leader: -1},
				},
			}),
			"partition unavailable",
		},
		{
			"duplicate topic partition",
			metadataClient(kmsg.MetadataResponseTopic{
				Topic: kmsg.StringPtr(topic),
				Partitions: []kmsg.MetadataResponseTopicPartition{
					{Partition: 0, Leader: 1}, {Partition: 0, Leader: 1},
				},
			}),
			"duplicate partition",
		},
		{
			"broker failure",
			&recordingRequestClient{
				err: errors.New("dial tcp kafka-0.data.svc:9093: refused"),
			},
			"topic unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			_, err := readTopicPartitions(context.Background(), test.client, topic)

			// then
			if err == nil || !strings.Contains(err.Error(), test.wantError) ||
				strings.Contains(err.Error(), "kafka-0") {
				t.Fatalf(
					"readTopicPartitions() error = %v, want %q",
					err,
					test.wantError,
				)
			}
		})
	}
}

func TestWorker_PreflightChecksTopicOnlyOnce(t *testing.T) {
	// given
	calls := 0
	worker := &Worker{preflight: func(context.Context) error {
		calls++
		return nil
	}}

	// when
	first := worker.Preflight(context.Background())
	second := worker.Preflight(context.Background())

	// then
	if first != nil || second != nil || calls != 1 {
		t.Fatalf("Preflight() = (%v, %v), calls = %d", first, second, calls)
	}
}

func TestWorker_PreflightFailsClosed(t *testing.T) {
	// given
	guardFailure := errors.New("guard failed")
	tests := []struct {
		name      string
		worker    *Worker
		wantError string
	}{
		{name: "nil worker", wantError: "worker is required"},
		{
			name: "topic check failure",
			worker: &Worker{
				preflight: func(context.Context) error { return guardFailure },
			},
			wantError: guardFailure.Error(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			err := test.worker.Preflight(context.Background())

			// then
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Preflight() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestWorker_PreflightUnreachableBrokerFailsWithinDeadline(t *testing.T) {
	// given
	worker, err := NewWorker(Settings{
		Brokers: []string{"127.0.0.1:1"}, Topic: topic,
		ClientID: "payment-service", CACert: testCertificate(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	const deadline = 300 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	started := time.Now()

	// when
	err = worker.Preflight(ctx)

	// then
	if err == nil || err.Error() != "preflight Payment registration worker: topic unavailable" {
		t.Fatalf("Preflight() error = %v, want the safe topic failure", err)
	}
	if elapsed := time.Since(started); elapsed > deadline+time.Second {
		t.Errorf("Preflight() took %v, want within %v", elapsed, deadline)
	}
}

func TestVerifyCommittedProgress_RequiresStableCompleteInRangeSnapshot(t *testing.T) {
	tests := []struct {
		name      string
		client    progressRequestClient
		wantError string
	}{
		{
			name: "commit at log start",
			client: progressRequestClient{
				start: 4, committed: 4, end: 8,
			},
		},
		{
			name: "commit at log end",
			client: progressRequestClient{
				start: 0, committed: 8, end: 8,
			},
		},
		{
			name: "active member",
			client: progressRequestClient{
				start: 0, committed: 4, end: 8, active: true,
			},
			wantError: "group is not inactive",
		},
		{
			name: "missing commit",
			client: progressRequestClient{
				start: 0, committed: -1, end: 8,
			},
			wantError: "missing group commit",
		},
		{
			name: "changed commit",
			client: progressRequestClient{
				start: 0, committed: 4, end: 8, secondCommitBy: 1,
			},
			wantError: "Kafka progress changed",
		},
		{
			name: "commit below log start",
			client: progressRequestClient{
				start: 4, committed: 3, end: 8,
			},
			wantError: "invalid committed progress",
		},
		{
			name: "commit above log end",
			client: progressRequestClient{
				start: 0, committed: 9, end: 8,
			},
			wantError: "invalid committed progress",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := test.client

			// when
			err := verifyCommittedProgress(
				t.Context(),
				&client,
				topic,
				"payment-qualification",
			)

			// then
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

func TestRegistrationWorker_PreflightConstructsConsumerOnlyAfterGuard(t *testing.T) {
	// given
	consumer := &fakeConsumer{}
	inspector := &progressRequestClient{start: 0, committed: 4, end: 4}
	factory := &fakeClientFactory{inspector: inspector, consumer: consumer}
	worker, err := newRegistrationWorker(
		registrationSettings(t),
		&fakeRecordHandler{},
		factory,
	)
	if err != nil {
		t.Fatal(err)
	}
	if worker.client != nil || factory.consumerCalls != 0 {
		t.Fatal("group consumer existed before preflight")
	}

	// when
	err = worker.Preflight(t.Context())
	// then
	if err != nil {
		t.Fatal(err)
	}
	if worker.client != consumer || factory.consumerCalls != 1 || !inspector.closed {
		t.Fatalf(
			"preflight state = client:%v consumer calls:%d inspector closed:%t",
			worker.client,
			factory.consumerCalls,
			inspector.closed,
		)
	}
}

func TestNewRegistrationWorker_RequiresFixedTopicAndHandler(t *testing.T) {
	// given
	settings := registrationSettings(t)
	settings.Topic = "other-topic"

	// when
	wrongTopic, wrongTopicErr := NewRegistrationWorker(settings, &RegistrationHandler{})
	missingHandler, missingHandlerErr := NewRegistrationWorker(registrationSettings(t), nil)

	// then
	if wrongTopic != nil || wrongTopicErr == nil || missingHandler != nil ||
		missingHandlerErr == nil {
		t.Fatalf(
			"invalid constructors = wrong topic:(%v, %v) missing handler:(%v, %v)",
			wrongTopic,
			wrongTopicErr,
			missingHandler,
			missingHandlerErr,
		)
	}
}

func TestRegistrationWorker_PreflightNeverConstructsConsumerWhenGuardFails(t *testing.T) {
	tests := []struct {
		name      string
		inspector *progressRequestClient
		wantError string
	}{
		{
			name: "active group",
			inspector: &progressRequestClient{
				start: 0, committed: 4, end: 4, active: true,
			},
			wantError: "group is not inactive",
		},
		{
			name: "missing commit",
			inspector: &progressRequestClient{
				start: 0, committed: -1, end: 4,
			},
			wantError: "missing group commit",
		},
		{
			name: "changed commit",
			inspector: &progressRequestClient{
				start: 0, committed: 4, end: 5, secondCommitBy: 1,
			},
			wantError: "Kafka progress changed",
		},
		{
			name: "out of range commit",
			inspector: &progressRequestClient{
				start: 0, committed: 5, end: 4,
			},
			wantError: "invalid committed progress",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			factory := &fakeClientFactory{
				inspector: test.inspector, consumer: &fakeConsumer{},
			}
			worker, err := newRegistrationWorker(
				registrationSettings(t),
				&fakeRecordHandler{},
				factory,
			)
			if err != nil {
				t.Fatal(err)
			}

			// when
			err = worker.Preflight(t.Context())

			// then
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Preflight() error = %v, want %q", err, test.wantError)
			}
			if factory.consumerCalls != 0 || !test.inspector.closed ||
				worker.client != nil {
				t.Fatalf(
					"rejected preflight = consumer calls:%d inspector closed:%t client:%v",
					factory.consumerCalls,
					test.inspector.closed,
					worker.client,
				)
			}
		})
	}
}

func TestRegistrationWorker_PreflightFailsClosedWithoutUsableClients(t *testing.T) {
	tests := []struct {
		name      string
		factory   *fakeClientFactory
		wantError string
	}{
		{
			name: "inspector construction failure",
			factory: &fakeClientFactory{
				inspectorErr: errors.New("synthetic inspector failure"),
			},
			wantError: "create Kafka inspector",
		},
		{
			name:      "missing inspector",
			factory:   &fakeClientFactory{},
			wantError: "Kafka inspector is required",
		},
		{
			name: "consumer construction failure",
			factory: &fakeClientFactory{
				inspector:   &progressRequestClient{start: 0, committed: 4, end: 4},
				consumerErr: errors.New("synthetic consumer failure"),
			},
			wantError: "create Kafka consumer",
		},
		{
			name: "missing consumer",
			factory: &fakeClientFactory{
				inspector: &progressRequestClient{start: 0, committed: 4, end: 4},
			},
			wantError: "consumer is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			worker, err := newRegistrationWorker(
				registrationSettings(t),
				&fakeRecordHandler{},
				test.factory,
			)
			if err != nil {
				t.Fatal(err)
			}

			// when
			err = worker.Preflight(t.Context())

			// then
			if err == nil || !strings.Contains(err.Error(), test.wantError) ||
				worker.client != nil {
				t.Fatalf(
					"Preflight() = error:%v client:%v, want %q without client",
					err,
					worker.client,
					test.wantError,
				)
			}
		})
	}
}

func TestRegistrationWorker_RunFailsClosed(t *testing.T) {
	t.Run("preflight required", func(t *testing.T) {
		var missing *RegistrationWorker
		if err := missing.Run(t.Context()); err == nil ||
			!strings.Contains(err.Error(), "preflight is required") {
			t.Fatalf("Run() error = %v, want preflight failure", err)
		}
	})
	t.Run("fetch failure", func(t *testing.T) {
		consumer := &fakeConsumer{polls: []pollResult{{
			errors: []error{errors.New("synthetic fetch failure")},
		}}}
		worker := &RegistrationWorker{
			client: consumer, handler: &fakeRecordHandler{}, topic: topic,
		}
		if err := worker.Run(t.Context()); err == nil ||
			!strings.Contains(err.Error(), "Kafka fetch failed") ||
			consumer.allowCount() != 1 {
			t.Fatalf(
				"Run() = %v, rebalance releases = %d",
				err,
				consumer.allowCount(),
			)
		}
	})
	t.Run("unexpected topic held", func(t *testing.T) {
		consumer := &fakeConsumer{polls: []pollResult{{records: []*kgo.Record{{
			Topic: "other-topic", Partition: 0, Offset: 4,
		}}}}}
		handler := &fakeRecordHandler{}
		worker := &RegistrationWorker{client: consumer, handler: handler, topic: topic}
		if err := worker.Run(t.Context()); !errors.Is(err, ErrRegistrationHeld) ||
			handler.callCount() != 0 || consumer.commitCount() != 0 {
			t.Fatalf(
				"Run() = %v, handler calls = %d, commits = %d",
				err,
				handler.callCount(),
				consumer.commitCount(),
			)
		}
	})
	t.Run("cancelled before poll", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		worker := &RegistrationWorker{client: &fakeConsumer{}}
		if err := worker.Run(ctx); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cancelled while polling", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		consumer := &fakeConsumer{
			polls: []pollResult{{}}, pollHook: cancel,
		}
		worker := &RegistrationWorker{client: consumer}
		if err := worker.Run(ctx); err != nil || consumer.allowCount() != 1 {
			t.Fatalf(
				"Run() = %v, rebalance releases = %d",
				err,
				consumer.allowCount(),
			)
		}
	})
}

func TestRegistrationWorker_CommitsHandledRecordBeforeContinuing(t *testing.T) {
	// given
	ctx, cancel := context.WithCancel(context.Background())
	record := &kgo.Record{
		Topic: topic, Partition: 2, Offset: 7, Key: []byte("policy"),
		Value: []byte("registration"),
	}
	consumer := &fakeConsumer{
		polls:      []pollResult{{records: []*kgo.Record{record}}},
		commitHook: cancel,
	}
	handler := &fakeRecordHandler{}
	worker := &RegistrationWorker{client: consumer, handler: handler, topic: topic}

	// when
	err := worker.Run(ctx)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if handler.callCount() != 1 || consumer.commitCount() != 1 {
		t.Fatalf(
			"handled = %d, commits = %d, want one of each",
			handler.callCount(),
			consumer.commitCount(),
		)
	}
	if committed := consumer.committedRecord(0); committed != record {
		t.Fatalf("committed record = %v, want offset 7 record", committed)
	}
	if consumer.allowCount() == 0 {
		t.Fatal("worker did not release the blocked rebalance after commit")
	}
}

func TestRegistrationWorker_HoldsRecordAcrossLaterFetchAndShutdown(t *testing.T) {
	// given
	held := &kgo.Record{Topic: topic, Partition: 0, Offset: 4}
	later := &kgo.Record{Topic: topic, Partition: 0, Offset: 5}
	consumer := &fakeConsumer{polls: []pollResult{{records: []*kgo.Record{held, later}}}}
	handler := &fakeRecordHandler{err: errors.New("invalid registration")}
	worker := &RegistrationWorker{client: consumer, handler: handler, topic: topic}

	// when
	err := worker.Run(t.Context())
	shutdownCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	shutdownErr := worker.Shutdown(shutdownCtx)

	// then
	if !errors.Is(err, ErrRegistrationHeld) || shutdownErr != nil {
		t.Fatalf("Run() = %v, Shutdown() = %v", err, shutdownErr)
	}
	if handler.callCount() != 1 || consumer.commitCount() != 0 {
		t.Fatalf(
			"held delivery = handler calls:%d commits:%d",
			handler.callCount(),
			consumer.commitCount(),
		)
	}
	if consumer.allowCount() < 2 || !consumer.isClosed() {
		t.Fatalf(
			"held shutdown = rebalance releases:%d closed:%t",
			consumer.allowCount(),
			consumer.isClosed(),
		)
	}
}

func TestRegistrationWorker_ShutdownIsBoundedWithoutFinalCommit(t *testing.T) {
	// given
	release := make(chan struct{})
	closed := make(chan struct{})
	consumer := &fakeConsumer{closeRelease: release, closeDone: closed}
	worker := &RegistrationWorker{client: consumer}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	// when
	err := worker.Shutdown(ctx)
	close(release)
	<-closed

	// then
	if !errors.Is(err, context.DeadlineExceeded) || consumer.commitCount() != 0 ||
		consumer.allowCount() == 0 {
		t.Fatalf(
			"Shutdown() = %v, commits = %d, rebalance releases = %d",
			err,
			consumer.commitCount(),
			consumer.allowCount(),
		)
	}
}

func TestRegistrationWorker_CommitFailureStopsLaterRecord(t *testing.T) {
	// given
	first := &kgo.Record{Topic: topic, Partition: 0, Offset: 4}
	later := &kgo.Record{Topic: topic, Partition: 0, Offset: 5}
	consumer := &fakeConsumer{
		polls:     []pollResult{{records: []*kgo.Record{first, later}}},
		commitErr: errors.New("synthetic commit failure"),
	}
	handler := &fakeRecordHandler{}
	worker := &RegistrationWorker{client: consumer, handler: handler, topic: topic}

	// when
	err := worker.Run(t.Context())

	// then
	if !errors.Is(err, ErrRegistrationCommitUncertain) {
		t.Fatalf("Run() error = %v, want uncertain commit", err)
	}
	if handler.callCount() != 1 || consumer.commitCount() != 1 {
		t.Fatalf(
			"uncertain delivery = handler calls:%d commit attempts:%d",
			handler.callCount(),
			consumer.commitCount(),
		)
	}
}

type progressRequestClient struct {
	start          int64
	committed      int64
	end            int64
	active         bool
	secondCommitBy int64
	commitReads    int
	closed         bool
}

func (client *progressRequestClient) Request(
	_ context.Context,
	request kmsg.Request,
) (kmsg.Response, error) {
	switch typed := request.(type) {
	case *kmsg.MetadataRequest:
		return &kmsg.MetadataResponse{Topics: []kmsg.MetadataResponseTopic{{
			Topic: kmsg.StringPtr(topic),
			Partitions: []kmsg.MetadataResponseTopicPartition{{
				Partition: 0, Leader: 1,
			}},
		}}}, nil
	case *kmsg.DescribeGroupsRequest:
		members := []kmsg.DescribeGroupsResponseGroupMember(nil)
		if client.active {
			members = append(members, kmsg.DescribeGroupsResponseGroupMember{
				MemberID: "active",
			})
		}
		return &kmsg.DescribeGroupsResponse{Groups: []kmsg.DescribeGroupsResponseGroup{{
			Group: typed.Groups[0], State: "Empty", Members: members,
		}}}, nil
	case *kmsg.OffsetFetchRequest:
		offset := client.committed
		if client.commitReads > 0 {
			offset += client.secondCommitBy
		}
		client.commitReads++
		return &kmsg.OffsetFetchResponse{Groups: []kmsg.OffsetFetchResponseGroup{{
			Group: "payment-qualification",
			Topics: []kmsg.OffsetFetchResponseGroupTopic{{
				Topic: topic,
				Partitions: []kmsg.OffsetFetchResponseGroupTopicPartition{{
					Partition: 0, Offset: offset,
				}},
			}},
		}}}, nil
	case *kmsg.ListOffsetsRequest:
		offset := client.start
		if typed.Topics[0].Partitions[0].Timestamp == -1 {
			offset = client.end
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

func (client *progressRequestClient) Close() { client.closed = true }

type fakeClientFactory struct {
	inspector     requestClient
	consumer      consumerClient
	inspectorErr  error
	consumerErr   error
	consumerCalls int
}

func (factory *fakeClientFactory) newInspector(...kgo.Opt) (requestClient, error) {
	return factory.inspector, factory.inspectorErr
}

func (factory *fakeClientFactory) newConsumer(...kgo.Opt) (consumerClient, error) {
	factory.consumerCalls++
	return factory.consumer, factory.consumerErr
}

type fakeConsumer struct {
	mu           sync.Mutex
	polls        []pollResult
	pollHook     func()
	commits      []*kgo.Record
	commitErr    error
	commitHook   func()
	allowed      int
	closed       bool
	closeRelease <-chan struct{}
	closeDone    chan<- struct{}
}

func (client *fakeConsumer) Poll(ctx context.Context, _ int) pollResult {
	client.mu.Lock()
	if len(client.polls) > 0 {
		result := client.polls[0]
		client.polls = client.polls[1:]
		hook := client.pollHook
		client.mu.Unlock()
		if hook != nil {
			hook()
		}
		return result
	}
	client.mu.Unlock()
	<-ctx.Done()
	return pollResult{errors: []error{ctx.Err()}}
}

func (client *fakeConsumer) CommitRecords(
	_ context.Context,
	records ...*kgo.Record,
) error {
	client.mu.Lock()
	client.commits = append(client.commits, records...)
	hook := client.commitHook
	err := client.commitErr
	client.mu.Unlock()
	if hook != nil {
		hook()
	}
	return err
}

func (client *fakeConsumer) AllowRebalance() {
	client.mu.Lock()
	client.allowed++
	client.mu.Unlock()
}

func (client *fakeConsumer) CloseAllowingRebalance() {
	if client.closeRelease != nil {
		<-client.closeRelease
	}
	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()
	if client.closeDone != nil {
		close(client.closeDone)
	}
}

func (client *fakeConsumer) allowCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.allowed
}

func (client *fakeConsumer) commitCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return len(client.commits)
}

func (client *fakeConsumer) committedRecord(index int) *kgo.Record {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.commits[index]
}

func (client *fakeConsumer) isClosed() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.closed
}

type fakeRecordHandler struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (handler *fakeRecordHandler) Handle(
	context.Context,
	[]byte,
	[]byte,
) error {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.calls++
	return handler.err
}

func (handler *fakeRecordHandler) callCount() int {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.calls
}

func registrationSettings(t *testing.T) RegistrationSettings {
	t.Helper()
	return RegistrationSettings{
		Settings: Settings{
			Brokers: []string{"kafka.example:9093"},
			Topic:   topic, ClientID: "payment-registration-consumer",
			CACert: testCertificate(t),
		},
		Group: "payment-qualification",
	}
}

type recordingRequestClient struct {
	requests []kmsg.Request
	response kmsg.Response
	err      error
}

func (client *recordingRequestClient) Request(
	_ context.Context,
	request kmsg.Request,
) (kmsg.Response, error) {
	client.requests = append(client.requests, request)
	return client.response, client.err
}

func (*recordingRequestClient) Close() {}

func metadataClient(item kmsg.MetadataResponseTopic) *recordingRequestClient {
	return &recordingRequestClient{
		response: &kmsg.MetadataResponse{Topics: []kmsg.MetadataResponseTopic{item}},
	}
}

func testCertificate(t *testing.T) []byte {
	t.Helper()
	server := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
}
