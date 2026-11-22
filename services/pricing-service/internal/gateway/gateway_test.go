package gateway

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pricingv1 "github.com/igor-baiborodine/insurance-hub/services/pricing-service/gen/pricing/v1"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/telemetry"
)

const validHTTPBody = `{"productCode":"TRI","policyFrom":"2017-04-16","policyTo":"2018-04-15","selectedCovers":["C1","C2"],"answers":[{"questionCode":"NUM_OF_ADULTS","answer":1.00},{"questionCode":"DESTINATION","answer":"1.00"}]}`

func TestHandler_MapsJavaRequestAndNumericResponse(t *testing.T) {
	// given
	client := &fakeClient{response: &pricingv1.CalculatePriceResponse{
		TotalPrice:   "172.50",
		CoversPrices: map[string]string{"C1": "118.75", "C2": "23.75", "C3": "30"},
	}}
	handler := newTestHandler(t, client, 4096)
	request := httptest.NewRequest(
		http.MethodPost,
		"/pricing/calculate",
		strings.NewReader(validHTTPBody),
	)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Accept", "*/*")
	recorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(recorder, request)

	// then
	if recorder.Code != http.StatusOK || recorder.Body.String() !=
		`{"totalPrice":172.50,"coversPrices":{"C1":118.75,"C2":23.75,"C3":30}}` {
		t.Fatalf("response = (%d, %s)", recorder.Code, recorder.Body.String())
	}
	mapped := client.lastRequest()
	if mapped.GetProductCode() != "TRI" || len(mapped.GetSelectedCovers()) != 2 ||
		len(mapped.GetAnswers()) != 2 {
		t.Fatalf("mapped request = %v", mapped)
	}
	if mapped.GetAnswers()[0].GetNumber() != "1.00" || mapped.GetAnswers()[0].GetText() != "" ||
		mapped.GetAnswers()[1].GetText() != "1.00" || mapped.GetAnswers()[1].GetNumber() != "" {
		t.Errorf("answer types were not preserved: %v", mapped.GetAnswers())
	}
}

func TestHandler_MapsJacksonDateArraysToRPCDates(t *testing.T) {
	// given
	client := &fakeClient{response: &pricingv1.CalculatePriceResponse{
		TotalPrice:   "78",
		CoversPrices: map[string]string{"C1": "26", "C2": "52"},
	}}
	handler := newTestHandler(t, client, 4096)
	body := strings.NewReplacer(
		`"2017-04-16"`, `[2017,4,16]`,
		`"2018-04-15"`, `[2018,4,15]`,
	).Replace(validHTTPBody)
	request := httptest.NewRequest(
		http.MethodPost,
		"/pricing/calculate",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(recorder, request)

	// then
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = (%d, %s)", recorder.Code, recorder.Body.String())
	}
	mapped := client.lastRequest()
	if mapped.GetPolicyFrom() != "2017-04-16" || mapped.GetPolicyTo() != "2018-04-15" {
		t.Errorf("mapped dates = (%q, %q)", mapped.GetPolicyFrom(), mapped.GetPolicyTo())
	}
}

func TestHandler_EnforcesIndependentHTTPContract(t *testing.T) {
	// given
	tests := []struct {
		name, method, path, body, contentType, accept string
		maximum, status, calls                        int
		response                                      string
	}{
		{
			name:        "malformed",
			method:      http.MethodPost,
			path:        "/pricing/calculate",
			body:        `{`,
			contentType: "application/json",
			maximum:     4096,
			status:      400,
			response:    errorBody(3, "invalid request"),
		},
		{
			name:        "duplicate root member",
			method:      http.MethodPost,
			path:        "/pricing/calculate",
			body:        `{"productCode":"TRI","productCode":"HSI"}`,
			contentType: "application/json",
			maximum:     4096,
			status:      400,
			response:    errorBody(3, "invalid request"),
		},
		{
			name:        "duplicate answer member",
			method:      http.MethodPost,
			path:        "/pricing/calculate",
			body:        `{"answers":[{"questionCode":"Q","answer":1,"answer":2}]}`,
			contentType: "application/json",
			maximum:     4096,
			status:      400,
			response:    errorBody(3, "invalid request"),
		},
		{
			name:        "unknown member",
			method:      http.MethodPost,
			path:        "/pricing/calculate",
			body:        `{"private":true}`,
			contentType: "application/json",
			maximum:     4096,
			status:      400,
			response:    errorBody(3, "invalid request"),
		},
		{
			name:        "boolean answer",
			method:      http.MethodPost,
			path:        "/pricing/calculate",
			body:        `{"answers":[{"questionCode":"Q","answer":true}]}`,
			contentType: "application/json",
			maximum:     4096,
			status:      400,
			response:    errorBody(3, "invalid request"),
		},
		{
			name:        "short date array",
			method:      http.MethodPost,
			path:        "/pricing/calculate",
			body:        strings.Replace(validHTTPBody, `"2017-04-16"`, `[2017,4]`, 1),
			contentType: "application/json",
			maximum:     4096,
			status:      400,
			response:    errorBody(3, "invalid request"),
		},
		{
			name:   "fractional date array",
			method: http.MethodPost,
			path:   "/pricing/calculate",
			body: strings.Replace(
				validHTTPBody,
				`"2017-04-16"`,
				`[2017,4,16.0]`,
				1,
			),
			contentType: "application/json",
			maximum:     4096,
			status:      400,
			response:    errorBody(3, "invalid request"),
		},
		{
			name:   "out-of-range date array",
			method: http.MethodPost,
			path:   "/pricing/calculate",
			body: strings.Replace(
				validHTTPBody,
				`"2017-04-16"`,
				`[2017,13,1]`,
				1,
			),
			contentType: "application/json",
			maximum:     4096,
			status:      400,
			response:    errorBody(3, "invalid request"),
		},
		{
			name:   "text date array",
			method: http.MethodPost,
			path:   "/pricing/calculate",
			body: strings.Replace(
				validHTTPBody,
				`"2017-04-16"`,
				`["2017",4,16]`,
				1,
			),
			contentType: "application/json",
			maximum:     4096,
			status:      400,
			response:    errorBody(3, "invalid request"),
		},
		{
			name:        "query",
			method:      http.MethodPost,
			path:        "/pricing/calculate?secret=value",
			body:        validHTTPBody,
			contentType: "application/json",
			maximum:     4096,
			status:      400,
			response:    errorBody(3, "invalid request"),
		},
		{
			name:        "unknown route",
			method:      http.MethodPost,
			path:        "/private/value",
			body:        `{}`,
			contentType: "application/json",
			maximum:     4096,
			status:      404,
			response:    errorBody(5, "not found"),
		},
		{
			name:        "trailing slash",
			method:      http.MethodPost,
			path:        "/pricing/calculate/",
			body:        `{}`,
			contentType: "application/json",
			maximum:     4096,
			status:      404,
			response:    errorBody(5, "not found"),
		},
		{
			name:     "method",
			method:   http.MethodGet,
			path:     "/pricing/calculate",
			maximum:  4096,
			status:   405,
			response: errorBody(12, "method not allowed"),
		},
		{
			name:        "unacceptable",
			method:      http.MethodPost,
			path:        "/pricing/calculate",
			body:        validHTTPBody,
			contentType: "application/json",
			accept:      "text/plain",
			maximum:     4096,
			status:      406,
			response:    errorBody(3, "not acceptable"),
		},
		{
			name:        "unsupported media",
			method:      http.MethodPost,
			path:        "/pricing/calculate",
			body:        validHTTPBody,
			contentType: "text/plain",
			maximum:     4096,
			status:      415,
			response:    errorBody(3, "unsupported media type"),
		},
		{
			name:        "too large",
			method:      http.MethodPost,
			path:        "/pricing/calculate",
			body:        validHTTPBody,
			contentType: "application/json",
			maximum:     8,
			status:      413,
			response:    errorBody(8, "request body too large"),
		},
		{
			name:        "missing required values reaches RPC",
			method:      http.MethodPost,
			path:        "/pricing/calculate",
			body:        `{}`,
			contentType: "application/json",
			maximum:     4096,
			status:      400,
			calls:       1,
			response:    errorBody(3, "invalid request"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			client := &fakeClient{}
			handler := newTestHandler(t, client, test.maximum)
			request := httptest.NewRequest(
				test.method,
				test.path,
				strings.NewReader(test.body),
			)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.accept != "" {
				request.Header.Set("Accept", test.accept)
			}
			recorder := httptest.NewRecorder()

			// when
			handler.ServeHTTP(recorder, request)

			// then
			if recorder.Code != test.status || recorder.Body.String() != test.response {
				t.Fatalf(
					"response = (%d, %q)",
					recorder.Code,
					recorder.Body.String(),
				)
			}
			if client.callCount() != test.calls {
				t.Errorf("calls = %d, want %d", client.callCount(), test.calls)
			}
		})
	}
}

func TestHandler_MapsUpstreamFailuresWithoutQuote(t *testing.T) {
	// given
	tests := []struct {
		name, message string
		code          codes.Code
		status        int
	}{
		{
			name:    "invalid",
			code:    codes.InvalidArgument,
			status:  400,
			message: "invalid request",
		},
		{name: "canceled", code: codes.Canceled, status: 499, message: "request canceled"},
		{
			name:    "deadline",
			code:    codes.DeadlineExceeded,
			status:  504,
			message: "deadline exceeded",
		},
		{
			name:    "unavailable",
			code:    codes.Unavailable,
			status:  503,
			message: "service unavailable",
		},
		{name: "internal", code: codes.Internal, status: 500, message: "internal error"},
		{
			name:    "resource exhausted",
			code:    codes.ResourceExhausted,
			status:  500,
			message: "internal error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			client := &fakeClient{
				err: status.Error(test.code, "private upstream details"),
			}
			handler := newTestHandler(t, client, 4096)
			request := httptest.NewRequest(
				http.MethodPost,
				"/pricing/calculate",
				strings.NewReader(validHTTPBody),
			)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			// when
			handler.ServeHTTP(recorder, request)

			// then
			wantCode := test.code
			if test.code == codes.ResourceExhausted {
				wantCode = codes.Internal
			}
			if recorder.Code != test.status ||
				recorder.Body.String() != errorBody(int(wantCode), test.message) ||
				strings.Contains(recorder.Body.String(), "private") ||
				strings.Contains(recorder.Body.String(), "totalPrice") {
				t.Fatalf(
					"response = (%d, %s)",
					recorder.Code,
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestHandler_UsesBoundedContextAndStripsApplicationHeaders(t *testing.T) {
	// given
	client := &fakeClient{waitForContext: true, started: make(chan struct{})}
	handler := newTestHandlerWithTimeout(t, client, 30*time.Millisecond)
	request := httptest.NewRequest(
		http.MethodPost,
		"/pricing/calculate",
		strings.NewReader(validHTTPBody),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "private-token")
	request.Header.Set("Grpc-Metadata-Private", "private-value")
	request.Header.Set("X-Forwarded-For", "private-address")
	recorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(recorder, request)

	// then
	if recorder.Code != http.StatusGatewayTimeout ||
		recorder.Body.String() != errorBody(4, "deadline exceeded") {
		t.Fatalf("response = (%d, %s)", recorder.Code, recorder.Body.String())
	}
	for key, values := range client.lastMetadata() {
		for _, value := range values {
			if strings.Contains(value, "private") {
				t.Errorf("private metadata escaped in %s: %v", key, values)
			}
		}
	}
}

func TestClearApplicationMetadata_DropsAllValues(t *testing.T) {
	// given
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("private", "value"))
	var captured metadata.MD

	// when
	err := ClearApplicationMetadata(
		ctx,
		"method",
		nil,
		nil,
		nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			captured, _ = metadata.FromOutgoingContext(ctx)
			return nil
		},
	)

	// then
	if err != nil || len(captured) != 0 {
		t.Fatalf("result = (%v, %v)", captured, err)
	}
}

func TestHandler_InvalidUpstreamDecimalReturnsSafeError(t *testing.T) {
	// given
	client := &fakeClient{response: &pricingv1.CalculatePriceResponse{
		TotalPrice: "private-invalid", CoversPrices: map[string]string{"C1": "1"},
	}}
	handler := newTestHandler(t, client, 4096)
	request := httptest.NewRequest(
		http.MethodPost,
		"/pricing/calculate",
		strings.NewReader(validHTTPBody),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(recorder, request)

	// then
	if recorder.Code != http.StatusInternalServerError ||
		recorder.Body.String() != errorBody(13, "internal error") {
		t.Fatalf("response = (%d, %s)", recorder.Code, recorder.Body.String())
	}
}

func TestUnavailableHandler_WritesFixedEnvelope(t *testing.T) {
	// given
	settings := testSettings(t, time.Second, 4096)
	handler, err := NewUnavailableHandler(settings)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/pricing/calculate", nil))

	// then
	if recorder.Code != 503 || recorder.Body.String() != errorBody(14, "service unavailable") {
		t.Fatalf("response = (%d, %s)", recorder.Code, recorder.Body.String())
	}
}

func newTestHandler(t *testing.T, client pricingv1.PricingServiceClient, maxBody int) http.Handler {
	return newTestHandlerWithSettings(t, client, maxBody, time.Second)
}

func newTestHandlerWithTimeout(
	t *testing.T,
	client pricingv1.PricingServiceClient,
	timeout time.Duration,
) http.Handler {
	return newTestHandlerWithSettings(t, client, 4096, timeout)
}

func newTestHandlerWithSettings(
	t *testing.T,
	client pricingv1.PricingServiceClient,
	maxBody int,
	timeout time.Duration,
) http.Handler {
	t.Helper()
	handler, err := NewHandler(client, testSettings(t, timeout, maxBody))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func testSettings(t *testing.T, timeout time.Duration, maxBody int) Settings {
	t.Helper()
	meter := metricnoop.NewMeterProvider()
	observer, err := telemetry.NewRequestObserver(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		meter,
	)
	if err != nil {
		t.Fatal(err)
	}
	return Settings{
		RequestTimeout: timeout, MaxBodyBytes: maxBody,
		TracerProvider: noop.NewTracerProvider(), MeterProvider: meter,
		Propagator: propagation.TraceContext{}, Observer: observer,
	}
}

type fakeClient struct {
	mu             sync.Mutex
	calls          int
	request        *pricingv1.CalculatePriceRequest
	metadata       metadata.MD
	err            error
	response       *pricingv1.CalculatePriceResponse
	waitForContext bool
	started        chan struct{}
}

func (client *fakeClient) CalculatePrice(
	ctx context.Context,
	request *pricingv1.CalculatePriceRequest,
	_ ...grpc.CallOption,
) (*pricingv1.CalculatePriceResponse, error) {
	client.mu.Lock()
	client.calls++
	client.request = request
	client.metadata, _ = metadata.FromOutgoingContext(ctx)
	wait := client.waitForContext
	started := client.started
	failure := client.err
	response := client.response
	client.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	if wait {
		if started != nil {
			close(started)
		}
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if request.GetProductCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if response == nil {
		return &pricingv1.CalculatePriceResponse{
			TotalPrice:   "78",
			CoversPrices: map[string]string{"C1": "26", "C2": "52"},
		}, nil
	}
	return response, nil
}

func (client *fakeClient) callCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.calls
}

func (client *fakeClient) lastRequest() *pricingv1.CalculatePriceRequest {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.request
}

func (client *fakeClient) lastMetadata() metadata.MD {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.metadata.Copy()
}

func errorBody(code int, message string) string {
	return fmt.Sprintf(`{"code":%d,"message":"%s","details":[]}`, code, message)
}
