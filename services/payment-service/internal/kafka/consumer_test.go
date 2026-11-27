package kafka

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

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
