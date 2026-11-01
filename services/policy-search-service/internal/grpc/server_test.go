package grpc_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"

	policysearchv1 "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/gen/policysearch/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	transport "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/grpc"
)

func TestPolicySearchService_GeneratedIdentity(t *testing.T) {
	// given
	const (
		wantFile       = "policysearch/v1/policy_search_service.proto"
		wantPackage    = "policysearch.v1"
		wantModule     = "github.com/igor-baiborodine/insurance-hub/services/policy-search-service"
		wantService    = "policysearch.v1.PolicySearchService"
		wantFullMethod = "/policysearch.v1.PolicySearchService/SearchPolicies"
	)
	wantGoPackage := wantModule + "/gen/policysearch/v1;policysearchv1"

	// when
	descriptor := (&policysearchv1.SearchPoliciesRequest{}).ProtoReflect().
		Descriptor().
		ParentFile()

	// then
	if descriptor.Path() != wantFile || string(descriptor.Package()) != wantPackage {
		t.Fatalf("descriptor identity = (%q, %q)", descriptor.Path(), descriptor.Package())
	}
	options, ok := descriptor.Options().(*descriptorpb.FileOptions)
	if !ok || options.GetGoPackage() != wantGoPackage {
		t.Fatalf("go_package = %v", descriptor.Options())
	}
	service := descriptor.Services().ByName("PolicySearchService")
	if service == nil || string(service.FullName()) != wantService ||
		policysearchv1.PolicySearchService_SearchPolicies_FullMethodName != wantFullMethod {
		t.Fatalf("service identity = %v", service)
	}
}

func TestPolicySearchService_SearchPolicies_PreservesPresenceAndMapsPolicies(t *testing.T) {
	// given
	dateFrom, err := domain.ParseDate("2024-02-29")
	if err != nil {
		t.Fatal(err)
	}
	dateTo, err := domain.ParseDate("2025-06-01")
	if err != nil {
		t.Fatal(err)
	}
	queries := make(chan string, 2)
	search := func(_ context.Context, query string) ([]domain.Policy, error) {
		queries <- query
		return []domain.Policy{
			{
				Number: "POL-229", DateFrom: &dateFrom, DateTo: &dateTo,
				PolicyHolder: "Synthetic Holder",
			},
			{},
		}, nil
	}
	client := newPolicySearchClient(t, search, noop.NewTracerProvider(), new(bytes.Buffer))

	// when
	emptyResponse, err := client.SearchPolicies(
		context.Background(),
		&policysearchv1.SearchPoliciesRequest{Q: proto.String("")},
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.SearchPolicies(
		context.Background(),
		&policysearchv1.SearchPoliciesRequest{Q: proto.String(`number:"POL-229"`)},
	)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if <-queries != "" || <-queries != `number:"POL-229"` || len(emptyResponse.Policies) != 2 {
		t.Fatal("explicit empty or raw query was not preserved")
	}
	if len(response.Policies) != 2 {
		t.Fatalf("policies = %d", len(response.Policies))
	}
	first := response.Policies[0]
	if first.GetNumber() != "POL-229" || first.GetDateFrom() != "2024-02-29" ||
		first.GetDateTo() != "2025-06-01" || first.GetPolicyHolder() != "Synthetic Holder" {
		t.Fatalf("mapped policy = %+v", first)
	}
	if response.Policies[1].Number != nil || response.Policies[1].DateFrom != nil ||
		response.Policies[1].DateTo != nil || response.Policies[1].PolicyHolder != nil {
		t.Fatalf("empty fields gained presence: %+v", response.Policies[1])
	}
}

func TestPolicySearchService_SearchPolicies_RejectsAbsentQBeforeUseCase(t *testing.T) {
	// given
	called := false
	client := newPolicySearchClient(
		t,
		func(context.Context, string) ([]domain.Policy, error) {
			called = true
			return nil, nil
		},
		noop.NewTracerProvider(),
		new(bytes.Buffer),
	)

	// when
	response, err := client.SearchPolicies(
		context.Background(),
		&policysearchv1.SearchPoliciesRequest{},
	)

	// then
	if status.Code(err) != codes.InvalidArgument || response != nil || called {
		t.Fatalf("absent q = (%v, %v, called=%t)", response, err, called)
	}
}

func TestPolicySearchService_SearchPolicies_PreservesContextAndSanitizesFailure(t *testing.T) {
	// given
	tests := []struct {
		name string
		err  error
		code codes.Code
	}{
		{"canceled", context.Canceled, codes.Canceled},
		{"deadline", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"internal", errors.New("private-adapter-marker"), codes.Internal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			client := newPolicySearchClient(
				t,
				func(context.Context, string) ([]domain.Policy, error) { return nil, test.err },
				noop.NewTracerProvider(),
				&logs,
			)

			// when
			response, err := client.SearchPolicies(
				context.Background(),
				&policysearchv1.SearchPoliciesRequest{
					Q: proto.String("private-query-marker"),
				},
			)

			// then
			if status.Code(err) != test.code || response != nil {
				t.Fatalf("failure = (%v, %v)", response, err)
			}
			if test.code == codes.Internal &&
				status.Convert(err).Message() != "internal error" {
				t.Fatalf("internal message = %q", status.Convert(err).Message())
			}
			if strings.Contains(logs.String(), "private-") {
				t.Fatalf(
					"logs disclosed query or adapter failure: %s",
					logs.String(),
				)
			}
		})
	}
}

func TestPolicySearchService_SearchPolicies_DoesNotExtendCallerDeadline(t *testing.T) {
	// given
	deadlineSeen := make(chan time.Time, 1)
	search := func(ctx context.Context, _ string) ([]domain.Policy, error) {
		deadline, _ := ctx.Deadline()
		deadlineSeen <- deadline
		<-ctx.Done()
		return nil, ctx.Err()
	}
	client := newPolicySearchClient(t, search, noop.NewTracerProvider(), new(bytes.Buffer))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	wantDeadline, _ := ctx.Deadline()

	// when
	_, err := client.SearchPolicies(
		ctx,
		&policysearchv1.SearchPoliciesRequest{Q: proto.String("*")},
	)

	// then
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("deadline status = %v", err)
	}
	if got := <-deadlineSeen; got.After(wantDeadline.Add(10 * time.Millisecond)) {
		t.Fatalf("caller deadline extended: %v > %v", got, wantDeadline)
	}
}

func TestNewServer_RejectsMissingDependencies(t *testing.T) {
	// given
	log := logger.New(new(bytes.Buffer), "policy-search-service", 0)
	tracerProvider := noop.NewTracerProvider()
	propagator := propagation.TraceContext{}
	search := transport.SearchPolicies(
		func(context.Context, string) ([]domain.Policy, error) { return nil, nil },
	)
	tests := []struct {
		name       string
		logger     *slog.Logger
		tracer     trace.TracerProvider
		propagator propagation.TextMapPropagator
		search     transport.SearchPolicies
	}{
		{"logger", nil, tracerProvider, propagator, search},
		{"tracer", log, nil, propagator, search},
		{"propagator", log, tracerProvider, nil, search},
		{"search", log, tracerProvider, propagator, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, err := transport.NewServer(
				test.logger,
				test.tracer,
				test.propagator,
				defaultSettings(),
				test.search,
			)
			if err == nil || server != nil {
				t.Fatalf("NewServer() = (%v, %v)", server, err)
			}
		})
	}
}

func newPolicySearchClient(
	t *testing.T,
	search transport.SearchPolicies,
	tracerProvider trace.TracerProvider,
	logs *bytes.Buffer,
) policysearchv1.PolicySearchServiceClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := transport.NewServer(
		logger.New(logs, "policy-search-service", 0),
		tracerProvider,
		propagation.TraceContext{},
		defaultSettings(),
		search,
	)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	connection, err := grpcgo.NewClient(
		listener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
	})
	return policysearchv1.NewPolicySearchServiceClient(connection)
}

func defaultSettings() transport.Settings {
	return transport.Settings{
		RequestTimeout:  time.Second,
		MaxReceiveBytes: 1024 * 1024,
		MaxSendBytes:    8 * 1024 * 1024,
	}
}
