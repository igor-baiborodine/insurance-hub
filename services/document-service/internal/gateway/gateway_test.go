package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	documentv1 "github.com/igor-baiborodine/insurance-hub/services/document-service/gen/document/v1"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/telemetry"
)

type fakeClient struct {
	find func(context.Context, *documentv1.FindDocumentsRequest) (*documentv1.FindDocumentsResponse, error)
}

func (client *fakeClient) FindDocuments(
	ctx context.Context,
	request *documentv1.FindDocumentsRequest,
	_ ...grpc.CallOption,
) (*documentv1.FindDocumentsResponse, error) {
	return client.find(ctx, request)
}

func TestHandler_ServesAcceptedLookupEnvelope(t *testing.T) {
	// given
	client := &fakeClient{find: func(
		_ context.Context,
		request *documentv1.FindDocumentsRequest,
	) (*documentv1.FindDocumentsResponse, error) {
		if request.GetPolicyNumber() != "SYN 362" {
			t.Fatalf("policy number = %q", request.GetPolicyNumber())
		}
		return &documentv1.FindDocumentsResponse{Documents: []*documentv1.Document{
			{PolicyNumber: request.GetPolicyNumber(), Content: []byte("%PDF-362")},
			{PolicyNumber: request.GetPolicyNumber(), Content: []byte{}},
		}}, nil
	}}
	handler := newTestHandler(t, client)
	request := httptest.NewRequest(http.MethodGet, "/documents/SYN%20362", nil)
	response := httptest.NewRecorder()

	// when
	handler.ServeHTTP(response, request)

	// then
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	want := `{"documents":[{"policyNumber":"SYN 362","content":"JVBERi0zNjI="},{"policyNumber":"SYN 362","content":""}]}`
	if strings.TrimSpace(response.Body.String()) != want {
		t.Fatalf("body = %s", response.Body)
	}
}

func TestHandler_PreservesEmptyCollectionAndSafeFailures(t *testing.T) {
	// given
	tests := []struct {
		name       string
		failure    error
		statusCode int
		body       string
	}{
		{"empty", nil, http.StatusOK, `{"documents":[]}`},
		{
			"unavailable",
			status.Error(codes.Unavailable, "private-marker"),
			http.StatusServiceUnavailable,
			`{"code":14,"message":"service unavailable","details":[]}`,
		},
		{
			"limit",
			status.Error(codes.ResourceExhausted, "private-marker"),
			http.StatusTooManyRequests,
			`{"code":8,"message":"response limit exceeded","details":[]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{find: func(
				context.Context,
				*documentv1.FindDocumentsRequest,
			) (*documentv1.FindDocumentsResponse, error) {
				if test.failure != nil {
					return nil, test.failure
				}
				return &documentv1.FindDocumentsResponse{}, nil
			}}
			handler := newTestHandler(t, client)
			response := httptest.NewRecorder()

			// when
			handler.ServeHTTP(
				response,
				httptest.NewRequest(http.MethodGet, "/documents/SYN-362", nil),
			)

			// then
			if response.Code != test.statusCode ||
				strings.TrimSpace(response.Body.String()) != test.body {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}
		})
	}
}

func TestHandler_RejectsOversizedJSONResponse(t *testing.T) {
	client := &fakeClient{find: func(
		context.Context,
		*documentv1.FindDocumentsRequest,
	) (*documentv1.FindDocumentsResponse, error) {
		return &documentv1.FindDocumentsResponse{Documents: []*documentv1.Document{{
			PolicyNumber: "SYN-362",
			Content:      []byte("oversized"),
		}}}, nil
	}}
	handler := newTestHandlerWithResponseLimit(t, client, 32)
	response := httptest.NewRecorder()

	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/documents/SYN-362", nil),
	)

	if response.Code != http.StatusTooManyRequests ||
		strings.TrimSpace(response.Body.String()) !=
			`{"code":8,"message":"response limit exceeded","details":[]}` {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}

func TestHandler_RejectsUnsupportedRoutesAndDecodedSlash(t *testing.T) {
	// given
	handler := newTestHandler(t, &fakeClient{find: func(
		context.Context,
		*documentv1.FindDocumentsRequest,
	) (*documentv1.FindDocumentsResponse, error) {
		t.Fatal("unexpected RPC call")
		return nil, nil
	}})
	tests := []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/documents", http.StatusNotFound},
		{http.MethodPost, "/documents/SYN-362", http.StatusMethodNotAllowed},
		{http.MethodGet, "/documents/SYN%2F362", http.StatusNotFound},
		{http.MethodGet, "/documents/SYN-362?extra=true", http.StatusBadRequest},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()

		// when
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))

		// then
		if response.Code != test.status {
			t.Errorf(
				"%s %s status = %d, want %d",
				test.method,
				test.path,
				response.Code,
				test.status,
			)
		}
	}
}

func newTestHandler(t *testing.T, client documentv1.DocumentServiceClient) http.Handler {
	return newTestHandlerWithResponseLimit(t, client, 1024*1024)
}

func newTestHandlerWithResponseLimit(
	t *testing.T,
	client documentv1.DocumentServiceClient,
	maximumResponseBytes int,
) http.Handler {
	t.Helper()
	observer, err := telemetry.NewRequestObserver(
		slog.New(slog.NewJSONHandler(io.Discard, nil)), metricnoop.NewMeterProvider(),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(client, Settings{
		RequestTimeout:       time.Second,
		MaximumResponseBytes: maximumResponseBytes,
		TracerProvider:       noop.NewTracerProvider(),
		MeterProvider:        metricnoop.NewMeterProvider(),
		Propagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
		),
		Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
