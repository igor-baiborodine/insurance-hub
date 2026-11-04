package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	policysearchv1 "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/gen/policysearch/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

func TestHandler_UsesGeneratedSearchClientAndLegacyJSON(t *testing.T) {
	client := &fakeClient{
		response: &policysearchv1.SearchPoliciesResponse{Policies: []*policysearchv1.Policy{
			{
				Number:       proto.String("POL-1"),
				DateFrom:     proto.String("2026-01-02"),
				PolicyHolder: proto.String("Synthetic Person"),
			},
			{},
		}},
	}
	handler := newTestHandler(t, client, 500*time.Millisecond)

	request := httptest.NewRequest(
		http.MethodGet,
		"/policies?q=holder%3A%22Synthetic+Person%22&q=ignored&unknown=value",
		nil,
	)
	request.Header.Set("Accept", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got, want := strings.TrimSpace(
		recorder.Body.String(),
	), `{"policies":[{"number":"POL-1","dateFrom":"2026-01-02","policyHolder":"Synthetic Person"},{}]}`; got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
	if len(client.requests) != 1 || client.requests[0].Q == nil ||
		client.requests[0].GetQ() != `holder:"Synthetic Person"` {
		t.Fatalf("requests = %#v", client.requests)
	}
}

func TestHandler_PreservesEmptyWhitespaceAndZeroResponseOmission(t *testing.T) {
	client := &fakeClient{response: &policysearchv1.SearchPoliciesResponse{}}
	handler := newTestHandler(t, client, time.Second)
	for _, target := range []string{"/policies?q=", "/policies?q=+++"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK ||
			strings.TrimSpace(recorder.Body.String()) != `{}` {
			t.Errorf("%s = (%d, %q)", target, recorder.Code, recorder.Body.String())
		}
	}
	if got := client.requests[0].GetQ(); got != "" {
		t.Errorf("empty q = %q", got)
	}
	if got := client.requests[1].GetQ(); got != "   " {
		t.Errorf("whitespace q = %q", got)
	}
}

func TestHandler_AcceptsLegacyGatewayTrailingSlash(t *testing.T) {
	client := &fakeClient{response: &policysearchv1.SearchPoliciesResponse{}}
	handler := newTestHandler(t, client, time.Second)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/policies/?q=legacy-gateway", nil),
	)

	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{}` {
		t.Fatalf(
			"trailing-slash response = (%d, %q)",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	if len(client.requests) != 1 || client.requests[0].GetQ() != "legacy-gateway" {
		t.Fatalf("requests = %#v", client.requests)
	}
}

func TestHandler_RejectsMissingAndMalformedQueryBeforeRPC(t *testing.T) {
	client := &fakeClient{response: &policysearchv1.SearchPoliciesResponse{}}
	handler := newTestHandler(t, client, time.Second)
	tests := []struct {
		target string
		body   string
	}{
		{
			"/policies",
			`{"message":"Required QueryValue [q] not specified","path":"/queryText","_links":{"self":{"href":"/policies","templated":false}}}`,
		},
		{
			"/policies?q=%zz",
			`{"message":"Bad Request","_links":{"self":{"href":"/policies","templated":false}}}`,
		},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.target, nil))
		if recorder.Code != http.StatusBadRequest ||
			strings.TrimSpace(recorder.Body.String()) != test.body {
			t.Errorf(
				"%s = (%d, %q), want 400 %s",
				test.target,
				recorder.Code,
				recorder.Body.String(),
				test.body,
			)
		}
	}
	if len(client.requests) != 0 {
		t.Fatalf("RPC calls = %d, want 0", len(client.requests))
	}
}

func TestHandler_MapsFailuresAndRoutesWithoutSensitiveDetails(t *testing.T) {
	tests := []struct {
		code       codes.Code
		wantStatus int
		message    string
	}{
		{codes.InvalidArgument, 400, "Bad Request"},
		{codes.Canceled, 499, "Request Canceled"},
		{codes.DeadlineExceeded, 504, "Gateway Timeout"},
		{codes.Unavailable, 503, "Service Unavailable"},
		{codes.Internal, 500, "Internal Server Error"},
	}
	for _, test := range tests {
		client := &fakeClient{err: status.Error(test.code, "private-query-and-credential")}
		recorder := httptest.NewRecorder()
		newTestHandler(
			t,
			client,
			time.Second,
		).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/policies?q=secret", nil))
		if recorder.Code != test.wantStatus ||
			!strings.Contains(recorder.Body.String(), `"message":"`+test.message+`"`) ||
			strings.Contains(recorder.Body.String(), "private") ||
			strings.Contains(recorder.Body.String(), "secret") {
			t.Errorf(
				"%v response = (%d, %q)",
				test.code,
				recorder.Code,
				recorder.Body.String(),
			)
		}
	}

	handler := newTestHandler(t, &fakeClient{}, time.Second)
	method := httptest.NewRecorder()
	handler.ServeHTTP(method, httptest.NewRequest(http.MethodPost, "/policies?q=x", nil))
	if method.Code != http.StatusMethodNotAllowed ||
		method.Header().Get("Allow") != http.MethodGet {
		t.Errorf("method response = (%d, %q)", method.Code, method.Header().Get("Allow"))
	}
	notFound := httptest.NewRecorder()
	handler.ServeHTTP(notFound, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if notFound.Code != http.StatusNotFound {
		t.Errorf("not found status = %d", notFound.Code)
	}
}

func TestHandler_StripsApplicationMetadataAndBoundsDeadline(t *testing.T) {
	client := &fakeClient{response: &policysearchv1.SearchPoliciesResponse{}}
	handler := newTestHandler(t, client, 75*time.Millisecond)
	request := httptest.NewRequest(http.MethodGet, "/policies?q=value", nil)
	request.Header.Set("Authorization", "Bearer private")
	request.Header.Set("Grpc-Metadata-Principal", "forged")
	request.Header.Set("Forwarded", "for=203.0.113.1")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	for _, key := range []string{"authorization", "principal", "grpc-timeout"} {
		if values := client.metadata.Get(key); len(values) != 0 {
			t.Errorf("metadata %s escaped: %v", key, values)
		}
	}
	if client.remaining <= 0 || client.remaining > 75*time.Millisecond {
		t.Errorf("remaining deadline = %v", client.remaining)
	}
}

func TestClearApplicationMetadata_DropsAllValues(t *testing.T) {
	ctx := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs("principal", "private"),
	)
	called := false
	err := ClearApplicationMetadata(
		ctx,
		"method",
		nil,
		nil,
		nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			called = true
			md, _ := metadata.FromOutgoingContext(ctx)
			if len(md) != 0 {
				t.Errorf("metadata = %v", md)
			}
			return nil
		},
	)
	if err != nil || !called {
		t.Fatalf("interceptor = (%v, %v)", called, err)
	}
}

func TestNewHandlers_RequireDependencies(t *testing.T) {
	if _, err := NewHandler(nil, Settings{}); err == nil {
		t.Fatal("invalid handler dependencies accepted")
	}
	if _, err := NewUnavailableHandler(Settings{}); err == nil {
		t.Fatal("invalid unavailable handler settings accepted")
	}
}

type fakeClient struct {
	response  *policysearchv1.SearchPoliciesResponse
	err       error
	requests  []*policysearchv1.SearchPoliciesRequest
	metadata  metadata.MD
	remaining time.Duration
}

func (client *fakeClient) SearchPolicies(
	ctx context.Context,
	request *policysearchv1.SearchPoliciesRequest,
	_ ...grpc.CallOption,
) (*policysearchv1.SearchPoliciesResponse, error) {
	client.requests = append(
		client.requests,
		proto.Clone(request).(*policysearchv1.SearchPoliciesRequest),
	)
	client.metadata, _ = metadata.FromOutgoingContext(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		client.remaining = time.Until(deadline)
	}
	if client.err != nil {
		return nil, client.err
	}
	if client.response == nil {
		return &policysearchv1.SearchPoliciesResponse{}, nil
	}
	return client.response, nil
}

func newTestHandler(
	t *testing.T,
	client policysearchv1.PolicySearchServiceClient,
	timeout time.Duration,
) http.Handler {
	t.Helper()
	meter := metricnoop.NewMeterProvider()
	tracer := tracenoop.NewTracerProvider()
	observer, err := telemetry.NewRequestObserver(
		slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		meter,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(client, Settings{
		RequestTimeout: timeout,
		MaxBodyBytes:   4096,
		TracerProvider: tracer,
		MeterProvider:  meter,
		Propagator:     propagation.TraceContext{},
		Observer:       observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestLegacyErrorJSONShape(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeLegacyError(recorder, 400, "message", "/path", "/policies")
	var value map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value["message"] != "message" || value["path"] != "/path" {
		t.Fatalf("value = %#v", value)
	}
}
