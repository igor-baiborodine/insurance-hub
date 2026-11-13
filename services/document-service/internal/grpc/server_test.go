package grpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	documentv1 "github.com/igor-baiborodine/insurance-hub/services/document-service/gen/document/v1"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/telemetry"
)

func TestNewServer_UsesGeneratedValidationAndInstrumentedRPC(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	meter := metricnoop.NewMeterProvider()
	observer, err := telemetry.NewRequestObserver(logger, meter)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(
		logger,
		tracenoop.NewTracerProvider(),
		propagation.TraceContext{},
		Settings{
			RequestTimeout: time.Second, MaxReceiveBytes: 1024, MaxSendBytes: 1024,
			MaximumResponseBytes: 1024, Observer: observer, MeterProvider: meter,
		},
		findDocumentsExecutorFunc(func(
			_ context.Context,
			policyNumber string,
		) (application.LookupResult, error) {
			return application.LookupResult{Documents: []domain.Document{{
				PolicyNumber: policyNumber,
				Content:      []byte("safe"),
			}}}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpcgo.NewClient(
		"passthrough:///bufnet",
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := documentv1.NewDocumentServiceClient(connection)
	if _, err := client.FindDocuments(
		context.Background(),
		&documentv1.FindDocumentsRequest{},
	); status.Code(
		err,
	) != codes.InvalidArgument {
		t.Fatalf("invalid status = %v", status.Code(err))
	}
	response, err := client.FindDocuments(
		context.Background(),
		&documentv1.FindDocumentsRequest{PolicyNumber: "SYN-362"},
	)
	if err != nil || string(response.GetDocuments()[0].GetContent()) != "safe" {
		t.Fatalf("response = (%v, %v)", response, err)
	}
}

func TestDocumentServer_FindDocuments_MapsApplicationResult(t *testing.T) {
	// given
	server := newTestDocumentServer(t, 1024, func(
		_ context.Context,
		policyNumber string,
	) (application.LookupResult, error) {
		return application.LookupResult{Documents: []domain.Document{
			{PolicyNumber: policyNumber, Content: []byte("%PDF-362")},
			{PolicyNumber: policyNumber, Content: []byte{}},
		}}, nil
	})

	// when
	response, err := server.FindDocuments(
		context.Background(),
		&documentv1.FindDocumentsRequest{PolicyNumber: "SYN-362"},
	)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetDocuments()) != 2 ||
		string(response.GetDocuments()[0].GetContent()) != "%PDF-362" ||
		response.GetDocuments()[1].GetContent() == nil {
		t.Fatalf("response = %+v", response)
	}
}

func TestDocumentServer_FindDocuments_UsesStableSafeErrors(t *testing.T) {
	// given
	tests := []struct {
		name    string
		failure error
		code    codes.Code
		message string
	}{
		{
			"unavailable",
			application.ErrUnavailable,
			codes.Unavailable,
			"service unavailable",
		},
		{
			"deadline",
			context.DeadlineExceeded,
			codes.DeadlineExceeded,
			context.DeadlineExceeded.Error(),
		},
		{"cancelled", context.Canceled, codes.Canceled, context.Canceled.Error()},
		{
			"limit",
			application.ErrResponseLimit,
			codes.ResourceExhausted,
			"response limit exceeded",
		},
		{"internal", errors.New("private-marker"), codes.Internal, "internal error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newTestDocumentServer(t, 1024, func(
				context.Context,
				string,
			) (application.LookupResult, error) {
				return application.LookupResult{}, test.failure
			})

			// when
			_, err := server.FindDocuments(
				context.Background(),
				&documentv1.FindDocumentsRequest{PolicyNumber: "SYN-362"},
			)

			// then
			if status.Code(err) != test.code ||
				status.Convert(err).Message() != test.message {
				t.Fatalf("status = %v", status.Convert(err))
			}
		})
	}
}

func TestDocumentServer_FindDocuments_RejectsEmptyInputAndOversizeResponse(t *testing.T) {
	// given
	server := newTestDocumentServer(t, 1, func(
		context.Context,
		string,
	) (application.LookupResult, error) {
		return application.LookupResult{Documents: []domain.Document{{
			PolicyNumber: "SYN-362", Content: []byte("too-large"),
		}}}, nil
	})

	// when
	_, emptyErr := server.FindDocuments(
		context.Background(),
		&documentv1.FindDocumentsRequest{},
	)
	_, limitErr := server.FindDocuments(
		context.Background(),
		&documentv1.FindDocumentsRequest{PolicyNumber: "SYN-362"},
	)

	// then
	if status.Code(emptyErr) != codes.InvalidArgument {
		t.Errorf("empty status = %v", status.Convert(emptyErr))
	}
	if status.Code(limitErr) != codes.ResourceExhausted {
		t.Errorf("limit status = %v", status.Convert(limitErr))
	}
}

func newTestDocumentServer(
	t *testing.T,
	maximumResponseBytes int,
	find func(context.Context, string) (application.LookupResult, error),
) *documentServer {
	t.Helper()
	return &documentServer{
		logger:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		requestTimeout: time.Second, maximumResponseBytes: maximumResponseBytes,
		find: findDocumentsExecutorFunc(find),
	}
}

type findDocumentsExecutorFunc func(
	context.Context,
	string,
) (application.LookupResult, error)

func (function findDocumentsExecutorFunc) Execute(
	ctx context.Context,
	policyNumber string,
) (application.LookupResult, error) {
	return function(ctx, policyNumber)
}
