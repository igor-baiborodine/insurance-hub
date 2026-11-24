package grpc_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

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
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"

	policyv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/policy/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
	transport "github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/grpc"
)

func TestPolicyService_GeneratedIdentity(t *testing.T) {
	// given
	const (
		wantFile       = "policy/v1/policy_service.proto"
		wantPackage    = "policy.v1"
		wantModule     = "github.com/igor-baiborodine/insurance-hub/services/policy-service"
		wantService    = "policy.v1.PolicyService"
		wantFullMethod = "/policy.v1.PolicyService/CreateOffer"
	)
	wantGoPackage := wantModule + "/gen/policy/v1;policyv1"

	// when
	descriptor := (&policyv1.CreateOfferRequest{}).ProtoReflect().Descriptor().ParentFile()

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
		t.Fatalf(
			"descriptor options type = %T, want *descriptorpb.FileOptions",
			descriptor.Options(),
		)
	}
	if options.GetGoPackage() != wantGoPackage {
		t.Errorf("go_package = %q, want %q", options.GetGoPackage(), wantGoPackage)
	}
	service := descriptor.Services().ByName("PolicyService")
	if service == nil || string(service.FullName()) != wantService {
		t.Errorf("service descriptor = %v, want %q", service, wantService)
	}
	if policyv1.PolicyService_CreateOffer_FullMethodName != wantFullMethod {
		t.Errorf(
			"full method = %q, want %q",
			policyv1.PolicyService_CreateOffer_FullMethodName,
			wantFullMethod,
		)
	}
}

func TestPolicyService_CreateOffer_PreservesCancellation(t *testing.T) {
	// given
	started := make(chan struct{})
	createOffer := func(ctx context.Context, _ domain.OfferRequest) (domain.Offer, error) {
		close(started)
		<-ctx.Done()
		return domain.Offer{}, ctx.Err()
	}
	client := newTracedPolicyClient(
		t,
		createOffer,
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		new(bytes.Buffer),
	)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)

	// when
	go func() {
		_, err := client.CreateOffer(ctx, validOfferRequest())
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

func TestPolicyService_CreateOffer_PreservesDeadline(t *testing.T) {
	// given
	started := make(chan struct{})
	createOffer := func(ctx context.Context, _ domain.OfferRequest) (domain.Offer, error) {
		close(started)
		<-ctx.Done()
		return domain.Offer{}, ctx.Err()
	}
	client := newTracedPolicyClient(
		t,
		createOffer,
		noop.NewTracerProvider(),
		propagation.TraceContext{},
		new(bytes.Buffer),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)

	// when
	go func() {
		_, err := client.CreateOffer(ctx, validOfferRequest())
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

func TestPolicyService_CreateOffer_MapsInternalFailureAndPropagatesTrace(t *testing.T) {
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
	createOffer := func(ctx context.Context, _ domain.OfferRequest) (domain.Offer, error) {
		handlerContext <- trace.SpanContextFromContext(ctx)
		return domain.Offer{}, status.Error(codes.Unknown, privateMarker)
	}
	var logs bytes.Buffer
	propagator := propagation.TraceContext{}
	client := newTracedPolicyClient(t, createOffer, tracerProvider, propagator, &logs)
	parentCtx, parentSpan := tracerProvider.Tracer("test").Start(context.Background(), "parent")
	parentSpanContext := parentSpan.SpanContext()

	// when
	response, err := client.CreateOffer(parentCtx, offerRequestFor(payloadMarker))
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
	if !serverSpanContext.IsValid() ||
		serverSpanContext.TraceID() != parentSpanContext.TraceID() {
		t.Errorf(
			"server trace context = %v, parent = %v",
			serverSpanContext,
			parentSpanContext,
		)
	}
	if strings.Contains(logs.String(), payloadMarker) ||
		strings.Contains(logs.String(), privateMarker) {
		t.Fatalf("logs expose request or error marker: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"service":"policy-service"`) ||
		!strings.Contains(
			logs.String(),
			`"trace_id":"`+serverSpanContext.TraceID().String()+`"`,
		) {
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
		t.Errorf(
			"request spans found: client=%t server=%t",
			clientSpanFound,
			serverSpanFound,
		)
	}
}

func TestNewServer_RejectsMissingDependencies(t *testing.T) {
	// given
	log := logger.New(new(bytes.Buffer), "policy-service", 0)
	tracerProvider := noop.NewTracerProvider()
	propagator := propagation.TraceContext{}
	tests := []struct {
		name              string
		loggerMissing     bool
		tracerMissing     bool
		propagatorMissing bool
		offerMissing      bool
	}{
		{name: "logger", loggerMissing: true},
		{name: "tracer provider", tracerMissing: true},
		{name: "propagator", propagatorMissing: true},
		{name: "offer handler", offerMissing: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			selectedLogger := log
			selectedTracer := trace.TracerProvider(tracerProvider)
			selectedPropagator := propagation.TextMapPropagator(propagator)
			selectedOffer := transport.CreateOffer(application.UnavailableOffers)
			if test.loggerMissing {
				selectedLogger = nil
			}
			if test.tracerMissing {
				selectedTracer = nil
			}
			if test.propagatorMissing {
				selectedPropagator = nil
			}
			if test.offerMissing {
				selectedOffer = nil
			}

			// when
			server, err := transport.NewServer(
				selectedLogger,
				selectedTracer,
				selectedPropagator,
				defaultSettings(),
				transport.Handlers{CreateOffer: selectedOffer},
			)

			// then
			if err == nil || server != nil {
				t.Fatalf("NewServer() = (%v, %v), want (nil, error)", server, err)
			}
		})
	}
}

func newTracedPolicyClient(
	t *testing.T,
	createOffer transport.CreateOffer,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	logs *bytes.Buffer,
) policyv1.PolicyServiceClient {
	t.Helper()
	return newHandlersClient(t, transport.Handlers{CreateOffer: createOffer}, tracerProvider,
		propagator, logs)
}

func newHandlersClient(
	t *testing.T,
	handlers transport.Handlers,
	tracerProvider trace.TracerProvider,
	propagator propagation.TextMapPropagator,
	logs *bytes.Buffer,
) policyv1.PolicyServiceClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server, err := transport.NewServer(
		logger.New(logs, "policy-service", 0),
		tracerProvider,
		propagator,
		defaultSettings(),
		handlers,
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
	return policyv1.NewPolicyServiceClient(connection)
}

func defaultSettings() transport.Settings {
	return transport.Settings{
		RequestTimeout:  time.Second,
		MaxReceiveBytes: 1048576,
		MaxSendBytes:    8388608,
	}
}
