// Package kafka owns the Payment client of the Policy registration topic.
package kafka

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

const (
	policyRegistrationTopic = "policy-registered"
	pollTimeout             = 100 * time.Millisecond
	maxPollRecords          = 1
)

var (
	// ErrRegistrationHeld reports a record that cannot safely advance Kafka progress.
	ErrRegistrationHeld = errors.New("payment registration held")
	// ErrRegistrationCommitUncertain reports that durable work could not be acknowledged.
	ErrRegistrationCommitUncertain = errors.New("payment registration commit uncertain")
)

// Settings captures the registration topic client behavior.
type Settings struct {
	Brokers  []string
	Topic    string
	ClientID string
	CACert   []byte
}

// RegistrationSettings adds the isolated consumer-group identity to the trusted client settings.
type RegistrationSettings struct {
	Settings
	Group string
}

type requestClient interface {
	Request(context.Context, kmsg.Request) (kmsg.Response, error)
	Close()
}

type pollResult struct {
	records []*kgo.Record
	errors  []error
}

type consumerClient interface {
	Poll(context.Context, int) pollResult
	CommitRecords(context.Context, ...*kgo.Record) error
	AllowRebalance()
	CloseAllowingRebalance()
}

type recordHandler interface {
	Handle(context.Context, []byte, []byte) error
}

type clientFactory interface {
	newInspector(...kgo.Opt) (requestClient, error)
	newConsumer(...kgo.Opt) (consumerClient, error)
}

type franzFactory struct{}

type franzConsumer struct{ client *kgo.Client }

func (franzFactory) newInspector(options ...kgo.Opt) (requestClient, error) {
	return kgo.NewClient(options...)
}

func (franzFactory) newConsumer(options ...kgo.Opt) (consumerClient, error) {
	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, err
	}
	return franzConsumer{client: client}, nil
}

func (client franzConsumer) Poll(ctx context.Context, limit int) pollResult {
	fetches := client.client.PollRecords(ctx, limit)
	result := pollResult{records: fetches.Records()}
	for _, fetchErr := range fetches.Errors() {
		result.errors = append(result.errors, fetchErr.Err)
	}
	return result
}

func (client franzConsumer) CommitRecords(ctx context.Context, records ...*kgo.Record) error {
	return client.client.CommitRecords(ctx, records...)
}

func (client franzConsumer) AllowRebalance() { client.client.AllowRebalance() }

func (client franzConsumer) CloseAllowingRebalance() {
	client.client.CloseAllowingRebalance()
}

// Worker owns the Payment registration topic client. It neither joins a group nor fetches,
// produces or commits offsets.
type Worker struct {
	preflight func(context.Context) error
}

// RegistrationWorker consumes one registration at a time and commits only handled records.
type RegistrationWorker struct {
	client    consumerClient
	preflight func(context.Context) (consumerClient, error)
	handler   recordHandler
	topic     string
}

// NewWorker constructs the registration topic client without contacting Kafka.
func NewWorker(settings Settings) (*Worker, error) {
	if len(settings.Brokers) == 0 || settings.Topic == "" || settings.ClientID == "" {
		return nil, errors.New("configure Payment registration worker: incomplete settings")
	}
	if len(bytes.TrimSpace(settings.CACert)) == 0 {
		return nil, errors.New(
			"configure Payment registration worker: empty CA certificate",
		)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("configure Payment registration worker: load system roots")
	}
	if !roots.AppendCertsFromPEM(settings.CACert) {
		return nil, errors.New(
			"configure Payment registration worker: invalid CA certificate",
		)
	}
	tlsSettings := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	baseOptions := []kgo.Opt{
		kgo.SeedBrokers(settings.Brokers...),
		kgo.ClientID(settings.ClientID),
		kgo.DialTLSConfig(tlsSettings),
	}
	return &Worker{preflight: func(ctx context.Context) error {
		inspector, err := kgo.NewClient(baseOptions...)
		if err != nil {
			return errors.New(
				"preflight Payment registration worker: create Kafka inspector",
			)
		}
		defer inspector.Close()
		_, err = readTopicPartitions(ctx, inspector, settings.Topic)
		return err
	}}, nil
}

// NewRegistrationWorker constructs a group consumer only after committed progress is verified.
func NewRegistrationWorker(
	settings RegistrationSettings,
	handler *RegistrationHandler,
) (*RegistrationWorker, error) {
	if handler == nil {
		return nil, errors.New(
			"configure Payment registration consumer: incomplete settings",
		)
	}
	return newRegistrationWorker(settings, handler, franzFactory{})
}

func newRegistrationWorker(
	settings RegistrationSettings,
	handler recordHandler,
	factory clientFactory,
) (*RegistrationWorker, error) {
	if len(settings.Brokers) == 0 || settings.Group == "" ||
		settings.Topic != policyRegistrationTopic || settings.ClientID == "" ||
		handler == nil || factory == nil {
		return nil, errors.New(
			"configure Payment registration consumer: incomplete settings",
		)
	}
	if len(bytes.TrimSpace(settings.CACert)) == 0 {
		return nil, errors.New(
			"configure Payment registration consumer: empty CA certificate",
		)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New(
			"configure Payment registration consumer: load system roots",
		)
	}
	if !roots.AppendCertsFromPEM(settings.CACert) {
		return nil, errors.New(
			"configure Payment registration consumer: invalid CA certificate",
		)
	}
	tlsSettings := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	baseOptions := []kgo.Opt{
		kgo.SeedBrokers(settings.Brokers...),
		kgo.ClientID(settings.ClientID),
		kgo.DialTLSConfig(tlsSettings),
	}
	consumerOptions := []kgo.Opt{
		kgo.SeedBrokers(settings.Brokers...),
		kgo.ClientID(settings.ClientID),
		kgo.ConsumerGroup(settings.Group),
		kgo.ConsumeTopics(settings.Topic),
		kgo.DisableAutoCommit(),
		kgo.SessionTimeout(10 * time.Second),
		kgo.HeartbeatInterval(3 * time.Second),
		kgo.RebalanceTimeout(5 * time.Minute),
		kgo.Balancers(kgo.RangeBalancer()),
		kgo.FetchMaxWait(500 * time.Millisecond),
		kgo.BlockRebalanceOnPoll(),
		kgo.DialTLSConfig(tlsSettings),
		kgo.ConsumeResetOffset(kgo.NoResetOffset()),
	}
	worker := &RegistrationWorker{handler: handler, topic: settings.Topic}
	worker.preflight = func(ctx context.Context) (consumerClient, error) {
		inspector, err := factory.newInspector(baseOptions...)
		if err != nil {
			return nil, errors.New(
				"preflight Payment registration consumer: create Kafka inspector",
			)
		}
		if inspector == nil {
			return nil, errors.New(
				"preflight Payment registration consumer: Kafka inspector is required",
			)
		}
		if err := verifyCommittedProgress(
			ctx,
			inspector,
			settings.Topic,
			settings.Group,
		); err != nil {
			inspector.Close()
			return nil, err
		}
		inspector.Close()
		consumer, err := factory.newConsumer(consumerOptions...)
		if err != nil {
			return nil, errors.New(
				"preflight Payment registration consumer: create Kafka consumer",
			)
		}
		return consumer, nil
	}
	return worker, nil
}

// Preflight verifies the existing registration topic and its partition leaders once.
func (worker *Worker) Preflight(ctx context.Context) error {
	if worker == nil {
		return errors.New("preflight Payment registration worker: worker is required")
	}
	if worker.preflight == nil {
		return nil
	}
	if err := worker.preflight(ctx); err != nil {
		return err
	}
	worker.preflight = nil
	return nil
}

// Preflight verifies stable committed progress before constructing the group consumer.
func (worker *RegistrationWorker) Preflight(ctx context.Context) error {
	if worker == nil {
		return errors.New(
			"preflight Payment registration consumer: worker is required",
		)
	}
	if worker.preflight == nil {
		return nil
	}
	client, err := worker.preflight(ctx)
	if err != nil {
		return err
	}
	if client == nil {
		return errors.New(
			"preflight Payment registration consumer: consumer is required",
		)
	}
	worker.client = client
	worker.preflight = nil
	return nil
}

// Run handles records in fetch order and acknowledges each durable result synchronously.
func (worker *RegistrationWorker) Run(ctx context.Context) error {
	if worker == nil || worker.client == nil {
		return errors.New("run Payment registration consumer: preflight is required")
	}
	for ctx.Err() == nil {
		pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
		result := worker.client.Poll(pollCtx, maxPollRecords)
		cancel()
		if ctx.Err() != nil {
			worker.client.AllowRebalance()
			return nil
		}
		for _, fetchErr := range result.errors {
			if errors.Is(fetchErr, context.Canceled) ||
				errors.Is(fetchErr, context.DeadlineExceeded) {
				continue
			}
			worker.client.AllowRebalance()
			return errors.New("run Payment registration consumer: Kafka fetch failed")
		}
		for _, record := range result.records {
			if record.Topic != worker.topic ||
				worker.handler.Handle(ctx, record.Key, record.Value) != nil {
				worker.client.AllowRebalance()
				if ctx.Err() != nil {
					return nil
				}
				return ErrRegistrationHeld
			}
			if err := worker.client.CommitRecords(ctx, record); err != nil {
				worker.client.AllowRebalance()
				if ctx.Err() != nil {
					return nil
				}
				return ErrRegistrationCommitUncertain
			}
		}
		worker.client.AllowRebalance()
	}
	return nil
}

// Shutdown releases blocked rebalances and closes without committing fetched offsets.
func (worker *RegistrationWorker) Shutdown(ctx context.Context) error {
	if worker == nil || worker.client == nil {
		return nil
	}
	worker.client.AllowRebalance()
	closed := make(chan struct{})
	go func() {
		worker.client.CloseAllowingRebalance()
		close(closed)
	}()
	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type partitionProgress struct {
	start     int64
	committed int64
	end       int64
}

func verifyCommittedProgress(
	ctx context.Context,
	client requestClient,
	topic string,
	group string,
) error {
	first, err := readCommittedProgress(ctx, client, topic, group)
	if err != nil {
		return err
	}
	second, err := readCommittedProgress(ctx, client, topic, group)
	if err != nil {
		return err
	}
	if !committedProgressStable(first, second) {
		return errors.New(
			"preflight Payment registration consumer: Kafka progress changed",
		)
	}
	return nil
}

func committedProgressStable(
	first map[int32]partitionProgress,
	second map[int32]partitionProgress,
) bool {
	if len(first) != len(second) {
		return false
	}
	for partition, before := range first {
		after, present := second[partition]
		if !present || before.start != after.start || before.committed != after.committed ||
			after.end < before.end {
			return false
		}
	}
	return true
}

func readCommittedProgress(
	ctx context.Context,
	client requestClient,
	topic string,
	group string,
) (map[int32]partitionProgress, error) {
	partitions, err := readTopicPartitions(ctx, client, topic)
	if err != nil {
		return nil, err
	}
	if err := requireInactiveGroup(ctx, client, group); err != nil {
		return nil, err
	}
	committed, err := readCommittedOffsets(ctx, client, topic, group, partitions)
	if err != nil {
		return nil, err
	}
	starts, err := readListOffsets(ctx, client, topic, partitions, -2)
	if err != nil {
		return nil, err
	}
	ends, err := readListOffsets(ctx, client, topic, partitions, -1)
	if err != nil {
		return nil, err
	}
	progress := make(map[int32]partitionProgress, len(partitions))
	for _, partition := range partitions {
		commit, committedPresent := committed[partition]
		start, startPresent := starts[partition]
		end, endPresent := ends[partition]
		if !committedPresent || !startPresent || !endPresent || commit < start ||
			commit > end {
			return nil, errors.New(
				"preflight Payment registration consumer: invalid committed progress",
			)
		}
		progress[partition] = partitionProgress{start: start, committed: commit, end: end}
	}
	return progress, nil
}

func readTopicPartitions(
	ctx context.Context,
	client requestClient,
	topic string,
) ([]int32, error) {
	request := kmsg.NewPtrMetadataRequest()
	request.AllowAutoTopicCreation = false
	request.Topics = []kmsg.MetadataRequestTopic{{Topic: kmsg.StringPtr(topic)}}
	response, err := client.Request(ctx, request)
	metadata, ok := response.(*kmsg.MetadataResponse)
	if err != nil || !ok || metadata == nil || len(metadata.Topics) != 1 ||
		metadata.Topics[0].Topic == nil || *metadata.Topics[0].Topic != topic ||
		metadata.Topics[0].ErrorCode != 0 || metadata.Topics[0].IsInternal ||
		len(metadata.Topics[0].Partitions) == 0 {
		return nil, errors.New("preflight Payment registration worker: topic unavailable")
	}
	partitions := make([]int32, 0, len(metadata.Topics[0].Partitions))
	seen := make(map[int32]struct{}, len(metadata.Topics[0].Partitions))
	for _, partition := range metadata.Topics[0].Partitions {
		if partition.ErrorCode != 0 || partition.Leader < 0 {
			return nil, errors.New(
				"preflight Payment registration worker: partition unavailable",
			)
		}
		if _, duplicate := seen[partition.Partition]; duplicate {
			return nil, errors.New(
				"preflight Payment registration worker: duplicate partition",
			)
		}
		seen[partition.Partition] = struct{}{}
		partitions = append(partitions, partition.Partition)
	}
	return partitions, nil
}

func requireInactiveGroup(ctx context.Context, client requestClient, group string) error {
	request := kmsg.NewPtrDescribeGroupsRequest()
	request.Groups = []string{group}
	response, err := client.Request(ctx, request)
	described, ok := response.(*kmsg.DescribeGroupsResponse)
	if err != nil || !ok || described == nil || len(described.Groups) != 1 ||
		described.Groups[0].Group != group || described.Groups[0].ErrorCode != 0 ||
		len(described.Groups[0].Members) != 0 {
		return errors.New(
			"preflight Payment registration consumer: group is not inactive",
		)
	}
	return nil
}

func readCommittedOffsets(
	ctx context.Context,
	client requestClient,
	topic string,
	group string,
	partitions []int32,
) (map[int32]int64, error) {
	request := kmsg.NewPtrOffsetFetchRequest()
	request.Group = group
	request.Topics = []kmsg.OffsetFetchRequestTopic{{Topic: topic, Partitions: partitions}}
	request.Groups = []kmsg.OffsetFetchRequestGroup{{
		Group:  group,
		Topics: []kmsg.OffsetFetchRequestGroupTopic{{Topic: topic, Partitions: partitions}},
	}}
	request.RequireStable = true
	response, err := client.Request(ctx, request)
	fetched, ok := response.(*kmsg.OffsetFetchResponse)
	if err != nil || !ok || fetched == nil {
		return nil, errors.New(
			"preflight Payment registration consumer: committed progress unavailable",
		)
	}
	committed := make(map[int32]int64, len(partitions))
	if len(fetched.Groups) > 0 {
		if len(fetched.Groups) != 1 || fetched.Groups[0].Group != group ||
			fetched.Groups[0].ErrorCode != 0 || len(fetched.Groups[0].Topics) != 1 ||
			fetched.Groups[0].Topics[0].Topic != topic {
			return nil, errors.New(
				"preflight Payment registration consumer: invalid group progress",
			)
		}
		for _, partition := range fetched.Groups[0].Topics[0].Partitions {
			if partition.ErrorCode != 0 || partition.Offset < 0 {
				return nil, errors.New(
					"preflight Payment registration consumer: missing group commit",
				)
			}
			if _, duplicate := committed[partition.Partition]; duplicate {
				return nil, errors.New(
					"preflight Payment registration consumer: duplicate group commit",
				)
			}
			committed[partition.Partition] = partition.Offset
		}
		if len(committed) != len(partitions) {
			return nil, errors.New(
				"preflight Payment registration consumer: incomplete group progress",
			)
		}
		return committed, nil
	}
	if fetched.ErrorCode != 0 || len(fetched.Topics) != 1 ||
		fetched.Topics[0].Topic != topic {
		return nil, errors.New(
			"preflight Payment registration consumer: invalid group progress",
		)
	}
	for _, partition := range fetched.Topics[0].Partitions {
		if partition.ErrorCode != 0 || partition.Offset < 0 {
			return nil, errors.New(
				"preflight Payment registration consumer: missing group commit",
			)
		}
		if _, duplicate := committed[partition.Partition]; duplicate {
			return nil, errors.New(
				"preflight Payment registration consumer: duplicate group commit",
			)
		}
		committed[partition.Partition] = partition.Offset
	}
	if len(committed) != len(partitions) {
		return nil, errors.New(
			"preflight Payment registration consumer: incomplete group progress",
		)
	}
	return committed, nil
}

func readListOffsets(
	ctx context.Context,
	client requestClient,
	topic string,
	partitions []int32,
	timestamp int64,
) (map[int32]int64, error) {
	requested := make([]kmsg.ListOffsetsRequestTopicPartition, 0, len(partitions))
	for _, partition := range partitions {
		item := kmsg.NewListOffsetsRequestTopicPartition()
		item.Partition = partition
		item.Timestamp = timestamp
		requested = append(requested, item)
	}
	request := kmsg.NewPtrListOffsetsRequest()
	request.Topics = []kmsg.ListOffsetsRequestTopic{{Topic: topic, Partitions: requested}}
	response, err := client.Request(ctx, request)
	listed, ok := response.(*kmsg.ListOffsetsResponse)
	if err != nil || !ok || listed == nil || len(listed.Topics) != 1 ||
		listed.Topics[0].Topic != topic {
		return nil, errors.New(
			"preflight Payment registration consumer: partition bounds unavailable",
		)
	}
	offsets := make(map[int32]int64, len(partitions))
	for _, partition := range listed.Topics[0].Partitions {
		if partition.ErrorCode != 0 || partition.Offset < 0 {
			return nil, errors.New(
				"preflight Payment registration consumer: partition bound failed",
			)
		}
		if _, duplicate := offsets[partition.Partition]; duplicate {
			return nil, errors.New(
				"preflight Payment registration consumer: duplicate partition bound",
			)
		}
		offsets[partition.Partition] = partition.Offset
	}
	if len(offsets) != len(partitions) {
		return nil, errors.New(
			"preflight Payment registration consumer: incomplete partition bounds",
		)
	}
	return offsets, nil
}
