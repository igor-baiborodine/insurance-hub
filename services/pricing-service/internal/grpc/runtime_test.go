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

	pricingv1 "github.com/igor-baiborodine/insurance-hub/services/pricing-service/gen/pricing/v1"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/domain"
	transport "github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/telemetry"
)

func TestServer_FinalStatusMetricsAndSafeCompletion(t *testing.T) {
	// given
	success := func(context.Context, application.CalculatePriceRequest) (domain.Quote, error) {
		return quote(t, "78", map[string]string{"C1": "26", "C2": "52"}), nil
	}
	cases := []struct {
		name, outcome, rpcStatus string
		code                     codes.Code
		sendLimit, receiveLimit  int
		calculate                transport.CalculatePrice
		request                  *pricingv1.CalculatePriceRequest
		unsupportedCompression   bool
	}{
		{
			name:      "success",
			outcome:   "success",
			rpcStatus: "OK",
			calculate: success,
			request:   validRequest(),
		},
		{
			name:      "invalid input",
			outcome:   "invalid_request",
			rpcStatus: "INVALID_ARGUMENT",
			code:      codes.InvalidArgument,
			calculate: success,
			request:   &pricingv1.CalculatePriceRequest{},
		},
		{
			name:      "send limit",
			outcome:   "invalid_request",
			rpcStatus: "RESOURCE_EXHAUSTED",
			code:      codes.ResourceExhausted,
			sendLimit: 1,
			calculate: success,
			request:   validRequest(),
		},
		{
			name:         "receive limit",
			outcome:      "invalid_request",
			rpcStatus:    "RESOURCE_EXHAUSTED",
			code:         codes.ResourceExhausted,
			receiveLimit: 1,
			calculate:    success,
			request:      validRequest(),
		},
		{
			name:      "internal failure",
			outcome:   "internal_error",
			rpcStatus: "INTERNAL",
			code:      codes.Internal,
			calculate: func(context.Context, application.CalculatePriceRequest) (domain.Quote, error) {
				return domain.Quote{}, errors.New("private-error")
			},
			request: validRequest(),
		},
		{
			name:                   "unsupported compression",
			outcome:                "invalid_request",
			rpcStatus:              "UNIMPLEMENTED",
			code:                   codes.Unimplemented,
			calculate:              success,
			request:                validRequest(),
			unsupportedCompression: true,
		},
		{
			name:      "server timeout",
			outcome:   "deadline_exceeded",
			rpcStatus: "DEADLINE_EXCEEDED",
			code:      codes.DeadlineExceeded,
			calculate: func(ctx context.Context, _ application.CalculatePriceRequest) (domain.Quote, error) {
				<-ctx.Done()
				return domain.Quote{}, ctx.Err()
			},
			request: validRequest(),
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
			observer, err := telemetry.NewRequestObserver(
				logger.New(&logs, "pricing-service", 0),
				provider,
			)
			if err != nil {
				t.Fatal(err)
			}
			settings.Observer = observer
			var options []grpcgo.DialOption
			if test.unsupportedCompression {
				//nolint:staticcheck // Client-only injection exercises server rejection.
				options = append(
					options,
					grpcgo.WithCompressor(unsupportedCompressor{}),
				)
			}
			client, stop := runtimeClient(
				t,
				settings,
				test.calculate,
				&logs,
				options...)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			// when
			_, err = client.CalculatePrice(ctx, test.request)
			stop()

			// then
			if status.Code(err) != test.code {
				t.Fatalf("code=%s want=%s: %v", status.Code(err), test.code, err)
			}
			if strings.Contains(logs.String(), "private-") {
				t.Fatalf("sensitive logs: %s", logs.String())
			}
			assertCompletion(t, logs.Bytes(), test.outcome, test.rpcStatus)
			assertMetric(t, ctx, reader, test.outcome, test.rpcStatus)
		})
	}
}

func TestServer_PreservesShorterCallerDeadline(t *testing.T) {
	// given
	deadlineSeen := make(chan time.Time, 1)
	calculate := func(ctx context.Context, _ application.CalculatePriceRequest) (domain.Quote, error) {
		deadline, _ := ctx.Deadline()
		deadlineSeen <- deadline
		<-ctx.Done()
		return domain.Quote{}, ctx.Err()
	}
	client, stop := runtimeClient(t, defaultSettings(), calculate, new(bytes.Buffer))
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	deadline, _ := ctx.Deadline()

	// when
	_, err := client.CalculatePrice(ctx, validRequest())

	// then
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("status=%v", err)
	}
	if got := <-deadlineSeen; got.After(deadline.Add(10 * time.Millisecond)) {
		t.Errorf("caller deadline extended: %v > %v", got, deadline)
	}
}

func assertCompletion(t *testing.T, data []byte, outcome, rpcStatus string) {
	t.Helper()
	completions := 0
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] == "request completed" {
			completions++
			if entry["outcome"] != outcome || entry["rpc_status_code"] != rpcStatus ||
				entry["operation"] != "calculate_price" {
				t.Errorf("completion=%v", entry)
			}
		}
	}
	if completions != 1 {
		t.Fatalf("completions=%d want=1", completions)
	}
}

func assertMetric(
	t *testing.T,
	ctx context.Context,
	reader *sdkmetric.ManualReader,
	outcome, rpcStatus string,
) {
	t.Helper()
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
					attrs["insurance_hub.service.operation"] != "calculate_price" ||
					attrs["insurance_hub.service.outcome"] != outcome ||
					attrs["rpc.response.status_code"] != rpcStatus {
					t.Errorf("unsafe or incorrect labels: %v", attrs)
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
	calculate transport.CalculatePrice,
	logs *bytes.Buffer,
	options ...grpcgo.DialOption,
) (pricingv1.PricingServiceClient, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := transport.NewServer(
		logger.New(logs, "pricing-service", 0), noop.NewTracerProvider(),
		propagation.TraceContext{}, settings, calculate,
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
		go func() { server.GracefulStop(); close(drained) }()
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
	return pricingv1.NewPricingServiceClient(connection), stop
}

type unsupportedCompressor struct{}

func (unsupportedCompressor) Type() string { return "private-unsupported-encoding" }

func (unsupportedCompressor) Do(writer io.Writer, payload []byte) error {
	_, err := writer.Write(payload)
	return err
}
