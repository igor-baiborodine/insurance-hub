package grpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
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

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"

	scaffoldv1 "github.com/igor-baiborodine/insurance-hub/templates/go-service/gen/scaffold/v1"
	transport "github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/telemetry"
)

func TestServer_FinalStatusMetricsAndSafeCompletion(t *testing.T) {
	// given
	cases := []struct {
		name, message, outcome, status string
		code                           codes.Code
		sendLimit, receiveLimit        int
		echo                           transport.Echo
		unsupportedCompression         bool
	}{
		{
			name:    "success",
			message: "private-payload",
			outcome: "success",
			status:  "OK",
			echo:    transport.IdentityEcho,
		},
		{
			name:    "invalid input",
			outcome: "invalid_request",
			status:  "INVALID_ARGUMENT",
			code:    codes.InvalidArgument,
			echo:    transport.IdentityEcho,
		},
		{
			name:      "send limit",
			message:   "private-payload",
			outcome:   "invalid_request",
			status:    "RESOURCE_EXHAUSTED",
			code:      codes.ResourceExhausted,
			sendLimit: 1,
			echo:      transport.IdentityEcho,
		},
		{
			name:         "receive limit",
			message:      "private-payload",
			outcome:      "invalid_request",
			status:       "RESOURCE_EXHAUSTED",
			code:         codes.ResourceExhausted,
			receiveLimit: 1,
			echo:         transport.IdentityEcho,
		},
		{
			name:    "internal failure",
			message: "private-payload",
			outcome: "internal_error",
			status:  "INTERNAL",
			code:    codes.Internal,
			echo:    func(context.Context, string) (string, error) { return "", errors.New("private-error") },
		},
		{
			name:                   "unsupported compression",
			message:                "private-payload",
			outcome:                "invalid_request",
			status:                 "UNIMPLEMENTED",
			code:                   codes.Unimplemented,
			echo:                   transport.IdentityEcho,
			unsupportedCompression: true,
		},
		{
			name:    "server timeout",
			message: "private-payload",
			outcome: "deadline_exceeded",
			status:  "DEADLINE_EXCEEDED",
			code:    codes.DeadlineExceeded,
			echo:    func(ctx context.Context, _ string) (string, error) { <-ctx.Done(); return "", ctx.Err() },
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// given
			settings := defaultSettings()
			settings.RequestTimeout = 30 * time.Millisecond
			if test.sendLimit != 0 {
				settings.MaxSendBytes = test.sendLimit
			}
			if test.receiveLimit != 0 {
				settings.MaxReceiveBytes = test.receiveLimit
			}
			var logs bytes.Buffer
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			log := logger.New(&logs, "go-service", 0)
			observer, err := telemetry.NewRequestObserver(log, provider)
			if err != nil {
				t.Fatal(err)
			}
			settings.Observer = observer
			var options []grpcgo.DialOption
			if test.unsupportedCompression {
				// The legacy client-only compressor sends an unsupported encoding without
				// registering a process-global compressor that the server would also accept.
				//nolint:staticcheck // Client-only injection is needed to test server rejection.
				compressor := grpcgo.WithCompressor(
					unsupportedCompressor{},
				)
				options = append(options, compressor)
			}
			client, stop := runtimeClient(t, settings, test.echo, &logs, options...)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			// when
			_, err = client.Echo(ctx, &scaffoldv1.EchoRequest{Message: test.message})
			stop()
			// then
			if status.Code(err) != test.code {
				t.Fatalf("code=%s want=%s: %v", status.Code(err), test.code, err)
			}
			if strings.Contains(logs.String(), "private-") {
				t.Fatalf("sensitive logs: %s", logs.String())
			}
			completions := 0
			for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
				var entry map[string]any
				if err := json.Unmarshal(line, &entry); err != nil {
					t.Fatal(err)
				}
				if entry["msg"] == "request completed" {
					completions++
					if entry["outcome"] != test.outcome ||
						entry["rpc_status_code"] != test.status {
						t.Errorf("completion=%v", entry)
					}
				}
			}
			if completions != 1 {
				t.Fatalf("completions=%d want=1", completions)
			}
			var result metricdata.ResourceMetrics
			if err := reader.Collect(ctx, &result); err != nil {
				t.Fatal(err)
			}
			count := int64(0)
			for _, scope := range result.ScopeMetrics {
				for _, measurement := range scope.Metrics {
					if measurement.Name != "insurance_hub.service.requests" {
						continue
					}
					for _, point := range measurement.Data.(metricdata.Sum[int64]).DataPoints {
						count += point.Value
						attrs := map[string]string{}
						for _, attr := range point.Attributes.ToSlice() {
							attrs[string(attr.Key)] = attr.Value.AsString()
						}
						if len(attrs) != 4 ||
							attrs["insurance_hub.service.operation"] != "echo" ||
							attrs["insurance_hub.service.outcome"] != test.outcome ||
							attrs["rpc.response.status_code"] != test.status {
							t.Errorf(
								"unsafe or incorrect labels: %v",
								attrs,
							)
						}
					}
				}
			}
			if count != 1 {
				t.Errorf("request count=%d want=1", count)
			}
		})
	}
}

func TestServer_PreservesShorterCallerDeadline(t *testing.T) {
	// given
	deadlineSeen := make(chan time.Time, 1)
	echo := func(ctx context.Context, _ string) (string, error) {
		deadline, _ := ctx.Deadline()
		deadlineSeen <- deadline
		<-ctx.Done()
		return "", ctx.Err()
	}
	client, stop := runtimeClient(t, defaultSettings(), echo, new(bytes.Buffer))
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	deadline, _ := ctx.Deadline()
	// when
	_, err := client.Echo(ctx, &scaffoldv1.EchoRequest{Message: "hello"})
	// then
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("status=%v", err)
	}
	select {
	case got := <-deadlineSeen:
		// gRPC encodes a relative deadline, allowing a small transport rounding tolerance.
		if got.After(deadline.Add(10 * time.Millisecond)) {
			t.Errorf("caller deadline extended: %v > %v", got, deadline)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not receive request")
	}
}

func runtimeClient(
	t *testing.T,
	settings transport.Settings,
	echo transport.Echo,
	logs *bytes.Buffer,
	options ...grpcgo.DialOption,
) (scaffoldv1.ExampleServiceClient, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := transport.NewServer(
		logger.New(logs, "go-service", 0),
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		settings,
		echo,
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
		drained := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(time.Second):
			server.Stop()
			t.Fatal("server handlers did not drain")
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	}
	t.Cleanup(stop)
	return scaffoldv1.NewExampleServiceClient(connection), stop
}

type unsupportedCompressor struct{}

func (unsupportedCompressor) Type() string { return "private-unsupported-encoding" }

func (unsupportedCompressor) Do(writer io.Writer, payload []byte) error {
	_, err := writer.Write(payload)
	return err
}
