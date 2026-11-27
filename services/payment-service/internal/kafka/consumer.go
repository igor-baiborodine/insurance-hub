// Package kafka owns the Payment client of the Policy registration topic.
package kafka

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// Settings captures the registration topic client behavior.
type Settings struct {
	Brokers  []string
	Topic    string
	ClientID string
	CACert   []byte
}

type requestClient interface {
	Request(context.Context, kmsg.Request) (kmsg.Response, error)
	Close()
}

// Worker owns the Payment registration topic client. It neither joins a group nor fetches,
// produces or commits offsets.
type Worker struct {
	preflight func(context.Context) error
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
