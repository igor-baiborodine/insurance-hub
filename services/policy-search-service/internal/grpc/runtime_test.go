package grpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"

	policysearchv1 "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/gen/policysearch/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	transport "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

func TestServer_FinalStatusMetricsAndSafeCompletion(t *testing.T) {
	// given
	tests := []struct {
		name, outcome, status  string
		code                   codes.Code
		query                  *string
		sendLimit              int
		receiveLimit           int
		search                 transport.SearchPolicies
		unsupportedCompression bool
	}{
		{
			name: "success", outcome: "success", status: "OK",
			query: proto.String("private-query"),
			search: func(context.Context, string) ([]domain.Policy, error) {
				return []domain.Policy{{Number: "POL-229"}}, nil
			},
		},
		{
			name:    "absent q",
			outcome: "invalid_request",
			status:  "INVALID_ARGUMENT",
			code:    codes.InvalidArgument,
			search:  func(context.Context, string) ([]domain.Policy, error) { return nil, nil },
		},
		{
			name:      "send limit",
			outcome:   "invalid_request",
			status:    "RESOURCE_EXHAUSTED",
			code:      codes.ResourceExhausted,
			query:     proto.String("private-query"),
			sendLimit: 1,
			search: func(context.Context, string) ([]domain.Policy, error) {
				return []domain.Policy{{Number: "POL-229"}}, nil
			},
		},
		{
			name:         "receive limit",
			outcome:      "invalid_request",
			status:       "RESOURCE_EXHAUSTED",
			code:         codes.ResourceExhausted,
			query:        proto.String("private-query"),
			receiveLimit: 1,
			search:       func(context.Context, string) ([]domain.Policy, error) { return nil, nil },
		},
		{
			name: "internal", outcome: "internal_error", status: "INTERNAL",
			code: codes.Internal, query: proto.String("private-query"),
			search: func(context.Context, string) ([]domain.Policy, error) {
				return nil, errors.New("private-error")
			},
		},
		{
			name:                   "unsupported compression",
			outcome:                "invalid_request",
			status:                 "UNIMPLEMENTED",
			code:                   codes.Unimplemented,
			query:                  proto.String("private-query"),
			search:                 func(context.Context, string) ([]domain.Policy, error) { return nil, nil },
			unsupportedCompression: true,
		},
		{
			name: "timeout", outcome: "deadline_exceeded", status: "DEADLINE_EXCEEDED",
			code: codes.DeadlineExceeded, query: proto.String("private-query"),
			search: func(ctx context.Context, _ string) ([]domain.Policy, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings := defaultSettings()
			settings.RequestTimeout = 30 * time.Millisecond
			if test.sendLimit != 0 {
				settings.MaxSendBytes = test.sendLimit
			}
			if test.receiveLimit != 0 {
				settings.MaxReceiveBytes = test.receiveLimit
			}
			var logs lockedBuffer
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			observer, err := telemetry.NewRequestObserver(
				logger.New(&logs, "policy-search-service", 0),
				provider,
			)
			if err != nil {
				t.Fatal(err)
			}
			settings.Observer = observer
			var options []grpcgo.DialOption
			if test.unsupportedCompression {
				//nolint:staticcheck // Client-only compressor verifies server rejection.
				options = append(
					options,
					grpcgo.WithCompressor(unsupportedCompressor{}),
				)
			}
			client, stop := runtimeClient(t, settings, test.search, &logs, options...)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			// when
			_, err = client.SearchPolicies(
				ctx,
				&policysearchv1.SearchPoliciesRequest{Q: test.query},
			)
			stop()

			// then
			if status.Code(err) != test.code {
				t.Fatalf("code=%s want=%s: %v", status.Code(err), test.code, err)
			}
			if strings.Contains(logs.String(), "private-") {
				t.Fatalf("sensitive logs: %s", logs.String())
			}
			assertRuntimeCompletion(t, &logs, test.outcome, test.status)
			assertRuntimeCounter(t, reader, ctx, test.outcome, test.status)
		})
	}
}

func assertRuntimeCompletion(t *testing.T, logs *lockedBuffer, outcome, code string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.String(), `"msg":"request completed"`) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	completions := 0
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] == "request completed" {
			completions++
			if entry["outcome"] != outcome || entry["rpc_status_code"] != code ||
				entry["operation"] != "search" {
				t.Errorf("completion=%v", entry)
			}
		}
	}
	if completions != 1 {
		t.Fatalf("completions=%d want=1", completions)
	}
}

func assertRuntimeCounter(
	t *testing.T,
	reader *sdkmetric.ManualReader,
	ctx context.Context,
	outcome, code string,
) {
	t.Helper()
	var result metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &result); err != nil {
		t.Fatal(err)
	}
	count := int64(0)
	for _, scope := range result.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			if measurement.Name != "insurance_hub.policy_search.requests" {
				continue
			}
			for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
				count += point.Value
				attrs := map[string]string{}
				for _, attr := range point.Attributes.ToSlice() {
					attrs[string(attr.Key)] = attr.Value.AsString()
				}
				if attrs["insurance_hub.policy_search.operation"] != "search" ||
					attrs["insurance_hub.policy_search.outcome"] != outcome ||
					attrs["rpc.response.status_code"] != code {
					t.Errorf("incorrect labels: %v", attrs)
				}
			}
		}
	}
	if count != 1 {
		t.Errorf("request count=%d want=1", count)
	}
}

func runtimeClient(
	t *testing.T,
	settings transport.Settings,
	search transport.SearchPolicies,
	logs *lockedBuffer,
	options ...grpcgo.DialOption,
) (policysearchv1.PolicySearchServiceClient, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := transport.NewServer(
		logger.New(logs, "policy-search-service", 0),
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		settings,
		search,
	)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	options = append(options, grpcgo.WithTransportCredentials(insecure.NewCredentials()))
	connection, err := grpcgo.NewClient(listener.Addr().String(), options...)
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = connection.Close()
		server.Stop()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	}
	t.Cleanup(stop)
	return policysearchv1.NewPolicySearchServiceClient(connection), stop
}

type unsupportedCompressor struct{}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func (buffer *lockedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return bytes.Clone(buffer.buffer.Bytes())
}

func (unsupportedCompressor) Type() string { return "private-unsupported-encoding" }

func (unsupportedCompressor) Do(writer io.Writer, payload []byte) error {
	_, err := writer.Write(payload)
	return err
}
