package productgrpc_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
	transport "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

func TestProductGRPC_W3CParentAndB3IsolationOnActualListener(t *testing.T) {
	// given
	propagationProvider, err := telemetry.New(
		context.Background(),
		"product-service",
		config.Telemetry{},
	)
	if err != nil {
		t.Fatal(err)
	}
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	observer, err := telemetry.NewRequestObserver(
		logger.New(io.Discard, "product-service", 0), tracerProvider,
		noop.NewMeterProvider(), propagationProvider.GRPCPropagator(),
	)
	if err != nil {
		t.Fatal(err)
	}
	server, err := transport.NewServer(
		logger.New(io.Discard, "product-service", 0),
		tracerProvider,
		propagationProvider.GRPCPropagator(),
		transport.Settings{
			RequestTimeout: time.Second, MaxReceiveBytes: 1 << 20,
			MaxSendBytes: 8 << 20, Observer: observer,
		},
		func(context.Context) ([]domain.Product, error) { return nil, nil },
		func(context.Context, string) (domain.Product, error) { return domain.Product{}, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("gRPC server stopped with error: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("gRPC server did not stop within one second")
		}
	})
	connection, err := grpcgo.NewClient(listener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := productv1.NewProductServiceClient(connection)
	const remoteTrace = "11111111111111111111111111111111"
	const remoteSpan = "2222222222222222"
	call := func(t *testing.T, ctx context.Context) {
		t.Helper()
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if _, err := client.ListProducts(
			ctx,
			&productv1.ListProductsRequest{},
		); err != nil {
			t.Fatalf("ListProducts: %v", err)
		}
	}
	cases := []struct {
		name    string
		headers metadata.MD
		check   func(*testing.T, []sdktrace.ReadOnlySpan)
	}{
		{
			name: "sampled w3c joins remote parent",
			headers: metadata.Pairs(
				"traceparent",
				"00-"+remoteTrace+"-"+remoteSpan+"-01",
			),
			check: func(t *testing.T, spans []sdktrace.ReadOnlySpan) {
				t.Helper()
				if len(spans) != 1 {
					t.Fatalf("sampled W3C spans = %d, want 1", len(spans))
				}
				parent := spans[0].Parent()
				if parent.TraceID().String() != remoteTrace ||
					parent.SpanID().
						String() !=
						remoteSpan || !parent.IsRemote() {
					t.Errorf(
						"sampled W3C parent = %s/%s remote=%t, want %s/%s remote=true",
						parent.TraceID(),
						parent.SpanID(),
						parent.IsRemote(),
						remoteTrace,
						remoteSpan,
					)
				}
			},
		},
		{
			name: "unsampled w3c records no span",
			headers: metadata.Pairs(
				"traceparent",
				"00-"+remoteTrace+"-"+remoteSpan+"-00",
			),
			check: func(t *testing.T, spans []sdktrace.ReadOnlySpan) {
				t.Helper()
				if len(spans) != 0 {
					t.Errorf("unsampled W3C spans = %d, want 0", len(spans))
				}
			},
		},
		{
			name: "b3 only starts a new trace",
			headers: metadata.Pairs(
				"x-b3-traceid",
				remoteTrace,
				"x-b3-spanid",
				remoteSpan,
				"x-b3-sampled",
				"1",
			),
			check: func(t *testing.T, spans []sdktrace.ReadOnlySpan) {
				t.Helper()
				if len(spans) != 1 {
					t.Fatalf("B3-only spans = %d, want 1", len(spans))
				}
				if spans[0].Parent().IsValid() ||
					spans[0].SpanContext().TraceID().String() == remoteTrace {
					t.Errorf(
						"B3-only parent = %s, trace = %s; want a new root trace",
						spans[0].Parent().
							SpanID(),
						spans[0].SpanContext().TraceID(),
					)
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// given
			before := len(recorder.Ended())
			ctx := metadata.NewOutgoingContext(context.Background(), test.headers)

			// when
			call(t, ctx)

			// then
			spans := recorder.Ended()[before:]
			test.check(t, spans)
		})
	}
}
