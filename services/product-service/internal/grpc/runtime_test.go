package productgrpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
	transport "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

func TestProductGRPC_FinalStatusAndDomainOutcome(t *testing.T) {
	// given
	cases := []struct {
		name, wireStatus, outcome string
		code                      codes.Code
		compression, malformed    bool
		receiveLimit, sendLimit   int
		applicationError          error
		wantCalls                 int32
	}{
		{
			name:       "success",
			wireStatus: "OK",
			outcome:    "success",
			code:       codes.OK,
			wantCalls:  1,
		},
		{
			name:        "unsupported compression",
			wireStatus:  "UNIMPLEMENTED",
			outcome:     "invalid_request",
			code:        codes.Unimplemented,
			compression: true,
		},
		{
			name:       "malformed protobuf",
			wireStatus: "INTERNAL",
			outcome:    "internal_error",
			code:       codes.Internal,
			malformed:  true,
		},
		{
			name:         "receive limit",
			wireStatus:   "RESOURCE_EXHAUSTED",
			outcome:      "invalid_request",
			code:         codes.ResourceExhausted,
			receiveLimit: 1,
		},
		{
			name:       "send limit",
			wireStatus: "RESOURCE_EXHAUSTED",
			outcome:    "invalid_request",
			code:       codes.ResourceExhausted,
			sendLimit:  1,
			wantCalls:  1,
		},
		{
			name:             "not found",
			wireStatus:       "NOT_FOUND",
			outcome:          "not_found",
			code:             codes.NotFound,
			applicationError: application.ErrProductNotFound,
			wantCalls:        1,
		},
		{
			name:             "storage unavailable",
			wireStatus:       "UNAVAILABLE",
			outcome:          "storage_unavailable",
			code:             codes.Unavailable,
			applicationError: application.ErrUnavailable,
			wantCalls:        1,
		},
		{
			name:             "invalid data",
			wireStatus:       "INTERNAL",
			outcome:          "invalid_data",
			code:             codes.Internal,
			applicationError: application.ErrInvalidDefinition,
			wantCalls:        1,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// given
			var logs bytes.Buffer
			reader := sdkmetric.NewManualReader()
			metrics := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { _ = metrics.Shutdown(context.Background()) })
			recorder := newNotifyingSpanRecorder()
			traces := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { _ = traces.Shutdown(context.Background()) })
			log := logger.New(&logs, "product-service", 0)
			observer, err := telemetry.NewRequestObserver(
				log,
				traces,
				metrics,
				propagation.TraceContext{},
			)
			if err != nil {
				t.Fatal(err)
			}
			settings := defaultSettings()
			settings.Observer = observer
			if test.receiveLimit != 0 {
				settings.MaxReceiveBytes = test.receiveLimit
			}
			if test.sendLimit != 0 {
				settings.MaxSendBytes = test.sendLimit
			}
			var calls atomic.Int32
			product := richProduct(t)
			server, err := transport.NewServer(
				log,
				traces,
				propagation.TraceContext{},
				settings,
				func(context.Context) ([]domain.Product, error) { t.Error("unexpected list call"); return nil, nil },
				func(context.Context, string) (domain.Product, error) {
					calls.Add(1)
					return product, test.applicationError
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			t.Cleanup(func() {
				server.Stop()
				_ = listener.Close()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("serve: %v", err)
					}
				case <-time.After(time.Second):
					t.Error("server did not stop")
				}
			})
			options := []grpcgo.DialOption{
				grpcgo.WithTransportCredentials(insecure.NewCredentials()),
			}
			if test.compression {
				// A process-global compressor would also make the server accept it.
				//nolint:staticcheck // Client-only injection exercises server rejection.
				compressor := grpcgo.WithCompressor(
					unsupportedCompressor{},
				)
				options = append(options, compressor)
			}
			connection, err := grpcgo.NewClient(listener.Addr().String(), options...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = connection.Close() })
			var callOptions []grpcgo.CallOption
			if test.malformed {
				callOptions = append(
					callOptions,
					grpcgo.ForceCodec(malformedCodec{}),
				)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			// when
			_, err = productv1.NewProductServiceClient(connection).
				GetProduct(ctx, &productv1.GetProductRequest{Code: "private-payload"}, callOptions...)
			// then
			if status.Code(err) != test.code {
				t.Fatalf("client status = %v, want %v", status.Code(err), test.code)
			}
			recorder.waitForEnded(t, 1)
			if calls.Load() != test.wantCalls {
				t.Errorf(
					"application calls = %d, want %d",
					calls.Load(),
					test.wantCalls,
				)
			}
			var entry map[string]any
			if err := json.Unmarshal(
				bytes.TrimSpace(logs.Bytes()),
				&entry,
			); err != nil {
				t.Fatalf("expected one completion: %v: %s", err, logs.String())
			}
			if entry["rpc_status_code"] != test.wireStatus ||
				entry["outcome"] != test.outcome ||
				entry["operation"] != "get" {
				t.Errorf("completion = %v", entry)
			}
			if strings.Contains(logs.String(), "private-") {
				t.Errorf("sensitive completion: %s", logs.String())
			}
			var collected metricdata.ResourceMetrics
			if err := reader.Collect(ctx, &collected); err != nil {
				t.Fatal(err)
			}
			observations := 0
			for _, scope := range collected.ScopeMetrics {
				for _, measurement := range scope.Metrics {
					switch data := measurement.Data.(type) {
					case metricdata.Sum[int64]:
						for _, point := range data.DataPoints {
							code, _ := point.Attributes.Value(
								"rpc.response.status_code",
							)
							outcome, _ := point.Attributes.Value(
								"insurance_hub.product.outcome",
							)
							if code.AsString() != test.wireStatus ||
								outcome.AsString() != test.outcome ||
								point.Value != 1 ||
								point.Attributes.Len() != 4 {
								t.Errorf("counter = %#v", point)
							}
							observations++
						}
					case metricdata.Histogram[float64]:
						for _, point := range data.DataPoints {
							code, _ := point.Attributes.Value(
								"rpc.response.status_code",
							)
							if code.AsString() != test.wireStatus ||
								point.Count != 1 ||
								point.Attributes.Len() != 4 {
								t.Errorf("duration = %#v", point)
							}
							observations++
						}
					}
				}
			}
			if observations != 2 {
				t.Errorf(
					"metric observations = %d, want counter and duration",
					observations,
				)
			}
			spans := recorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("server spans = %d, want 1", len(spans))
			}
			attributes := map[string]string{}
			for _, attr := range spans[0].Attributes() {
				attributes[string(attr.Key)] = attr.Value.AsString()
			}
			if attributes["insurance_hub.product.outcome"] != test.outcome {
				t.Errorf(
					"server span outcome = %q",
					attributes["insurance_hub.product.outcome"],
				)
			}
		})
	}
}

type unsupportedCompressor struct{}

func (unsupportedCompressor) Type() string { return "private-unsupported-encoding" }
func (unsupportedCompressor) Do(writer io.Writer, payload []byte) error {
	_, err := writer.Write(payload)
	return err
}

type malformedCodec struct{}

func (malformedCodec) Name() string                { return "proto" }
func (malformedCodec) Marshal(any) ([]byte, error) { return []byte{0xff}, nil }
func (malformedCodec) Unmarshal([]byte, any) error { return nil }
