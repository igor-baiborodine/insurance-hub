package grpc_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	scaffoldv1 "github.com/igor-baiborodine/insurance-hub/templates/go-service/gen/scaffold/v1"
	transport "github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/logger"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestExampleServiceGeneratedIdentity(t *testing.T) {
	// given
	const (
		wantFile       = "scaffold/v1/example_service.proto"
		wantPackage    = "scaffold.v1"
		wantModule     = "github.com/igor-baiborodine/insurance-hub/templates/go-service"
		wantService    = "scaffold.v1.ExampleService"
		wantFullMethod = "/scaffold.v1.ExampleService/Echo"
	)
	wantGoPackage := wantModule + "/gen/scaffold/v1;scaffoldv1"

	// when
	descriptor := (&scaffoldv1.EchoRequest{}).ProtoReflect().Descriptor().ParentFile()

	// then
	if descriptor.Path() != wantFile || string(descriptor.Package()) != wantPackage {
		t.Errorf(
			"descriptor identity = (%q, %q), want (%q, %q)",
			descriptor.Path(),
			descriptor.Package(),
			wantFile,
			wantPackage,
		)
	}
	options, ok := descriptor.Options().(*descriptorpb.FileOptions)
	if !ok {
		t.Fatalf("descriptor options type = %T, want *descriptorpb.FileOptions", descriptor.Options())
	}
	if options.GetGoPackage() != wantGoPackage {
		t.Errorf("go_package = %q, want %q", options.GetGoPackage(), wantGoPackage)
	}
	service := descriptor.Services().ByName("ExampleService")
	if service == nil || string(service.FullName()) != wantService {
		t.Errorf("service descriptor = %v, want %q", service, wantService)
	}
	if scaffoldv1.ExampleService_Echo_FullMethodName != wantFullMethod {
		t.Errorf(
			"full method = %q, want %q",
			scaffoldv1.ExampleService_Echo_FullMethodName,
			wantFullMethod,
		)
	}
}

func TestExampleServiceEchoBoundaries(t *testing.T) {
	// given
	var calls atomic.Int64
	echo := func(ctx context.Context, message string) (string, error) {
		calls.Add(1)
		return transport.IdentityEcho(ctx, message)
	}
	client := newExampleClient(
		t,
		echo,
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		new(bytes.Buffer),
	)
	tests := []struct {
		name     string
		message  string
		wantCode codes.Code
		wantCall bool
	}{
		{name: "empty", message: "", wantCode: codes.InvalidArgument},
		{name: "one ASCII byte", message: "a", wantCode: codes.OK, wantCall: true},
		{name: "two-byte UTF-8", message: "é", wantCode: codes.OK, wantCall: true},
		{
			name:     "128 ASCII bytes",
			message:  strings.Repeat("a", 128),
			wantCode: codes.OK,
			wantCall: true,
		},
		{
			name:     "128 multibyte UTF-8 bytes",
			message:  strings.Repeat("é", 64),
			wantCode: codes.OK,
			wantCall: true,
		},
		{
			name:     "129 ASCII bytes",
			message:  strings.Repeat("a", 129),
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "129 bytes ending in ASCII after multibyte UTF-8",
			message:  strings.Repeat("é", 64) + "a",
			wantCode: codes.InvalidArgument,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			callsBefore := calls.Load()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			// when
			response, err := client.Echo(ctx, &scaffoldv1.EchoRequest{Message: test.message})

			// then
			if status.Code(err) != test.wantCode {
				t.Fatalf("status = %v, want %v: %v", status.Code(err), test.wantCode, err)
			}
			wantCalls := callsBefore
			if test.wantCall {
				wantCalls++
			}
			if calls.Load() != wantCalls {
				t.Errorf("handler calls = %d, want %d", calls.Load(), wantCalls)
			}
			if test.wantCode != codes.OK {
				if response != nil {
					t.Errorf("response = %v, want nil", response)
				}
				if status.Convert(err).Message() != "invalid request" {
					t.Errorf("message = %q, want invalid request", status.Convert(err).Message())
				}
				return
			}
			if err != nil {
				t.Fatalf("Echo: %v", err)
			}
			want := &scaffoldv1.EchoResponse{Message: test.message}
			if !proto.Equal(response, want) {
				t.Errorf("response = %v, want byte-identical message", response)
			}
		})
	}
}

func TestExampleServiceEchoPreservesCancellation(t *testing.T) {
	// given
	started := make(chan struct{})
	echo := func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	client := newExampleClient(
		t,
		echo,
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		new(bytes.Buffer),
	)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)

	// when
	go func() {
		_, err := client.Echo(ctx, &scaffoldv1.EchoRequest{Message: "cancel"})
		result <- err
	}()

	// then
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	// when
	cancel()

	// then
	select {
	case err := <-result:
		if status.Code(err) != codes.Canceled {
			t.Fatalf("status = %v, want Canceled: %v", status.Code(err), err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled RPC did not return")
	}
}

func TestExampleServiceEchoPreservesDeadline(t *testing.T) {
	// given
	started := make(chan struct{})
	echo := func(ctx context.Context, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	client := newExampleClient(
		t,
		echo,
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		new(bytes.Buffer),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)

	// when
	go func() {
		_, err := client.Echo(ctx, &scaffoldv1.EchoRequest{Message: "deadline"})
		result <- err
	}()

	// then
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	select {
	case err := <-result:
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("status = %v, want DeadlineExceeded: %v", status.Code(err), err)
		}
	case <-time.After(time.Second):
		t.Fatal("deadline RPC did not return")
	}
}

func TestExampleServiceEchoMapsInternalFailureAndPropagatesTrace(t *testing.T) {
	// given
	const (
		payloadMarker = "payload-marker"
		privateMarker = "private-adapter-marker"
	)
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := tracerProvider.Shutdown(ctx); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})
	handlerContext := make(chan trace.SpanContext, 1)
	echo := func(ctx context.Context, _ string) (string, error) {
		handlerContext <- trace.SpanContextFromContext(ctx)
		return "", status.Error(codes.Unknown, privateMarker)
	}
	var logs bytes.Buffer
	propagator := propagation.TraceContext{}
	client := newExampleClient(t, echo, tracerProvider, propagator, &logs)
	parentCtx, parentSpan := tracerProvider.Tracer("test").Start(context.Background(), "parent")
	parentSpanContext := parentSpan.SpanContext()

	// when
	response, err := client.Echo(parentCtx, &scaffoldv1.EchoRequest{Message: payloadMarker})
	parentSpan.End()

	// then
	if status.Code(err) != codes.Internal {
		t.Fatalf("status = %v, want Internal: %v", status.Code(err), err)
	}
	if status.Convert(err).Message() != "internal error" {
		t.Errorf("message = %q, want internal error", status.Convert(err).Message())
	}
	if response != nil {
		t.Errorf("response = %v, want nil", response)
	}
	var serverSpanContext trace.SpanContext
	select {
	case serverSpanContext = <-handlerContext:
	case <-time.After(time.Second):
		t.Fatal("handler context was not captured")
	}
	if !serverSpanContext.IsValid() || serverSpanContext.TraceID() != parentSpanContext.TraceID() {
		t.Errorf("server trace context = %v, parent = %v", serverSpanContext, parentSpanContext)
	}
	if strings.Contains(logs.String(), payloadMarker) ||
		strings.Contains(logs.String(), privateMarker) {
		t.Fatalf("logs expose request or error marker: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"service":"go-service"`) ||
		!strings.Contains(logs.String(), `"trace_id":"`+serverSpanContext.TraceID().String()+`"`) {
		t.Errorf("logs lack service or trace identity: %s", logs.String())
	}
	var clientSpanFound, serverSpanFound bool
	for _, span := range spanRecorder.Ended() {
		if span.SpanContext().TraceID() != parentSpanContext.TraceID() {
			continue
		}
		switch span.SpanKind() {
		case trace.SpanKindClient:
			clientSpanFound = true
		case trace.SpanKindServer:
			serverSpanFound = true
		}
	}
	if !clientSpanFound || !serverSpanFound {
		t.Errorf("request spans found: client=%t server=%t", clientSpanFound, serverSpanFound)
	}
}

func TestNewServerRejectsMissingDependencies(t *testing.T) {
	// given
	log := logger.New(new(bytes.Buffer), "go-service", 0)
	tracerProvider := noop.NewTracerProvider()
	propagator := propagation.TraceContext{}
	tests := []struct {
		name              string
		loggerMissing     bool
		tracerMissing     bool
		propagatorMissing bool
		echoMissing       bool
	}{
		{name: "logger", loggerMissing: true},
		{name: "tracer provider", tracerMissing: true},
		{name: "propagator", propagatorMissing: true},
		{name: "echo handler", echoMissing: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			selectedLogger := log
			selectedTracer := trace.TracerProvider(tracerProvider)
			selectedPropagator := propagation.TextMapPropagator(propagator)
			selectedEcho := transport.Echo(transport.IdentityEcho)
			if test.loggerMissing {
				selectedLogger = nil
			}
			if test.tracerMissing {
				selectedTracer = nil
			}
			if test.propagatorMissing {
				selectedPropagator = nil
			}
			if test.echoMissing {
				selectedEcho = nil
			}

			// when
			server, err := transport.NewServer(
				selectedLogger,
				selectedTracer,
				selectedPropagator,
				selectedEcho,
			)

			// then
			if err == nil || server != nil {
				t.Fatalf("NewServer() = (%v, %v), want (nil, error)", server, err)
			}
		})
	}
}

func newExampleClient(
	t *testing.T,
	echo transport.Echo,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	logs *bytes.Buffer,
) scaffoldv1.ExampleServiceClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server, err := transport.NewServer(
		logger.New(logs, "go-service", 0),
		tracerProvider,
		propagator,
		echo,
	)
	if err != nil {
		_ = listener.Close()
		t.Fatalf("new server: %v", err)
	}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- server.Serve(listener)
	}()
	connection, err := grpcgo.NewClient(
		listener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithStatsHandler(otelgrpc.NewClientHandler(
			otelgrpc.WithTracerProvider(tracerProvider),
			otelgrpc.WithPropagators(propagator),
			otelgrpc.WithMeterProvider(metricnoop.NewMeterProvider()),
		)),
	)
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close client: %v", err)
		}
		server.Stop()
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close listener: %v", err)
		}
		select {
		case err := <-serveResult:
			if err != nil && !errors.Is(err, grpcgo.ErrServerStopped) {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
	return scaffoldv1.NewExampleServiceClient(connection)
}
