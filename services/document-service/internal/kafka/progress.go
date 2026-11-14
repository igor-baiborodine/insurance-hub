package kafka

import (
	"context"
	"errors"

	"github.com/twmb/franz-go/pkg/kmsg"
)

type requestClient interface {
	Request(context.Context, kmsg.Request) (kmsg.Response, error)
	Close()
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
		return errors.New("preflight Document registration worker: Kafka progress changed")
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
				"preflight Document registration worker: invalid committed progress",
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
		return nil, errors.New("preflight Document registration worker: topic unavailable")
	}
	partitions := make([]int32, 0, len(metadata.Topics[0].Partitions))
	seen := make(map[int32]struct{}, len(metadata.Topics[0].Partitions))
	for _, partition := range metadata.Topics[0].Partitions {
		if partition.ErrorCode != 0 || partition.Leader < 0 {
			return nil, errors.New(
				"preflight Document registration worker: partition unavailable",
			)
		}
		if _, duplicate := seen[partition.Partition]; duplicate {
			return nil, errors.New(
				"preflight Document registration worker: duplicate partition",
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
		return errors.New("preflight Document registration worker: group is not inactive")
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
			"preflight Document registration worker: committed progress unavailable",
		)
	}
	committed := make(map[int32]int64, len(partitions))
	if len(fetched.Groups) > 0 {
		if len(fetched.Groups) != 1 || fetched.Groups[0].Group != group ||
			fetched.Groups[0].ErrorCode != 0 || len(fetched.Groups[0].Topics) != 1 ||
			fetched.Groups[0].Topics[0].Topic != topic {
			return nil, errors.New(
				"preflight Document registration worker: invalid group progress",
			)
		}
		for _, partition := range fetched.Groups[0].Topics[0].Partitions {
			if partition.ErrorCode != 0 || partition.Offset < 0 {
				return nil, errors.New(
					"preflight Document registration worker: missing group commit",
				)
			}
			if _, duplicate := committed[partition.Partition]; duplicate {
				return nil, errors.New(
					"preflight Document registration worker: duplicate group commit",
				)
			}
			committed[partition.Partition] = partition.Offset
		}
		if len(committed) != len(partitions) {
			return nil, errors.New(
				"preflight Document registration worker: incomplete group progress",
			)
		}
		return committed, nil
	}
	if fetched.ErrorCode != 0 || len(fetched.Topics) != 1 || fetched.Topics[0].Topic != topic {
		return nil, errors.New(
			"preflight Document registration worker: invalid group progress",
		)
	}
	for _, partition := range fetched.Topics[0].Partitions {
		if partition.ErrorCode != 0 || partition.Offset < 0 {
			return nil, errors.New(
				"preflight Document registration worker: missing group commit",
			)
		}
		if _, duplicate := committed[partition.Partition]; duplicate {
			return nil, errors.New(
				"preflight Document registration worker: duplicate group commit",
			)
		}
		committed[partition.Partition] = partition.Offset
	}
	if len(committed) != len(partitions) {
		return nil, errors.New(
			"preflight Document registration worker: incomplete group progress",
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
			"preflight Document registration worker: partition bounds unavailable",
		)
	}
	offsets := make(map[int32]int64, len(partitions))
	for _, partition := range listed.Topics[0].Partitions {
		if partition.ErrorCode != 0 || partition.Offset < 0 {
			return nil, errors.New(
				"preflight Document registration worker: partition bound failed",
			)
		}
		if _, duplicate := offsets[partition.Partition]; duplicate {
			return nil, errors.New(
				"preflight Document registration worker: duplicate partition bound",
			)
		}
		offsets[partition.Partition] = partition.Offset
	}
	if len(offsets) != len(partitions) {
		return nil, errors.New(
			"preflight Document registration worker: incomplete partition bounds",
		)
	}
	return offsets, nil
}
