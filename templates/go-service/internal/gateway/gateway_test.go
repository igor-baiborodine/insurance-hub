package gateway

import (
	"bytes"
	"context"
	"errors"
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
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	scaffoldv1 "github.com/igor-baiborodine/insurance-hub/templates/go-service/gen/scaffold/v1"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/telemetry"
)

func TestHandler_EnforcesIndependentHTTPContract(t *testing.T) {
	tests := []struct {
		name, method, path, body, contentType, accept string
		wantStatus, wantCalls                         int
		wantBody                                      string
	}{
		{
			"valid",
			http.MethodPost,
			"/v1/example/echo",
			`{"message":"hello"}`,
			"application/json",
			"application/json",
			200,
			1,
			`{"message":"hello"}`,
		},
		{
			"valid charset",
			http.MethodPost,
			"/v1/example/echo",
			`{"message":"é"}`,
			"application/json; charset=utf-8",
			"*/*",
			200,
			1,
			`{"message":"é"}`,
		},
		{
			"missing value reaches RPC",
			http.MethodPost,
			"/v1/example/echo",
			`{}`,
			"application/json",
			"",
			400,
			1,
			errorBody(3, "invalid request"),
		},
		{
			"malformed",
			http.MethodPost,
			"/v1/example/echo",
			`{`,
			"application/json",
			"",
			400,
			0,
			errorBody(3, "invalid request"),
		},
		{
			"duplicate",
			http.MethodPost,
			"/v1/example/echo",
			`{"message":"a","message":"b"}`,
			"application/json",
			"",
			400,
			0,
			errorBody(3, "invalid request"),
		},
		{
			"unknown member",
			http.MethodPost,
			"/v1/example/echo",
			`{"message":"a","secret":"b"}`,
			"application/json",
			"",
			400,
			0,
			errorBody(3, "invalid request"),
		},
		{
			"wrong type",
			http.MethodPost,
			"/v1/example/echo",
			`{"message":7}`,
			"application/json",
			"",
			400,
			0,
			errorBody(3, "invalid request"),
		},
		{
			"query",
			http.MethodPost,
			"/v1/example/echo?secret=value",
			`{"message":"a"}`,
			"application/json",
			"",
			400,
			0,
			errorBody(3, "invalid request"),
		},
		{
			"unknown route",
			http.MethodPost,
			"/private/value",
			`{}`,
			"application/json",
			"",
			404,
			0,
			errorBody(5, "not found"),
		},
		{
			"trailing slash",
			http.MethodPost,
			"/v1/example/echo/",
			`{}`,
			"application/json",
			"",
			404,
			0,
			errorBody(5, "not found"),
		},
		{
			"method",
			http.MethodGet,
			"/v1/example/echo",
			"",
			"",
			"",
			405,
			0,
			errorBody(12, "method not allowed"),
		},
		{
			"unacceptable",
			http.MethodPost,
			"/v1/example/echo",
			`{"message":"a"}`,
			"application/json",
			"text/plain",
			406,
			0,
			errorBody(3, "not acceptable"),
		},
		{
			"zero quality response",
			http.MethodPost,
			"/v1/example/echo",
			`{"message":"a"}`,
			"application/json",
			"application/json;q=0",
			406,
			0,
			errorBody(3, "not acceptable"),
		},
		{
			"parameterized response",
			http.MethodPost,
			"/v1/example/echo",
			`{"message":"a"}`,
			"application/json",
			"application/json;profile=private",
			406,
			0,
			errorBody(3, "not acceptable"),
		},
		{
			"unsupported",
			http.MethodPost,
			"/v1/example/echo",
			`{"message":"a"}`,
			"text/plain",
			"",
			415,
			0,
			errorBody(3, "unsupported media type"),
		},
		{
			"too large",
			http.MethodPost,
			"/v1/example/echo",
			strings.Repeat("x", 65),
			"application/json",
			"",
			413,
			0,
			errorBody(8, "request body too large"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{}
			handler := newTestHandler(t, client, 64)
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
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus ||
				recorder.Body.String() != test.wantBody {
				t.Errorf(
					"response = (%d, %q), want (%d, %q)",
					recorder.Code,
					recorder.Body.String(),
					test.wantStatus,
					test.wantBody,
				)
			}
			if client.callCount() != test.wantCalls {
				t.Errorf(
					"RPC calls = %d, want %d",
					client.callCount(),
					test.wantCalls,
				)
			}
		})
	}
}

func TestHandler_MapsUpstreamFailuresWithoutDetails(t *testing.T) {
	privateMarker := "private-upstream-marker"
	for _, test := range []struct {
		name string
		err  error
		want int
		body string
	}{
		{"unavailable", status.Error(codes.Unavailable, privateMarker), 503, errorBody(14, "service unavailable")},
		{"deadline", status.Error(codes.DeadlineExceeded, privateMarker), 504, errorBody(4, "deadline exceeded")},
		{"canceled", status.Error(codes.Canceled, privateMarker), 499, errorBody(1, "request canceled")},
		{"resource exhausted", status.Error(codes.ResourceExhausted, privateMarker), 500, errorBody(13, "internal error")},
		{"internal", status.Error(codes.Internal, privateMarker), 500, errorBody(13, "internal error")},
		{"unknown", errors.New(privateMarker), 500, errorBody(13, "internal error")},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{err: test.err}
			handler := newTestHandler(t, client, 4096)
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/example/echo",
				strings.NewReader(`{"message":"hello"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.want || recorder.Body.String() != test.body ||
				strings.Contains(recorder.Body.String(), privateMarker) {
				t.Errorf(
					"unsafe response = (%d, %q)",
					recorder.Code,
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestHandler_PreservesMessageByteBoundaries(t *testing.T) {
	for _, test := range []struct {
		name    string
		message string
		status  int
	}{
		{"ASCII maximum", strings.Repeat("a", 128), http.StatusOK},
		{"ASCII over", strings.Repeat("a", 129), http.StatusBadRequest},
		{"UTF-8 maximum", strings.Repeat("é", 64), http.StatusOK},
		{"UTF-8 over", strings.Repeat("é", 64) + "a", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{}
			handler := newTestHandler(t, client, 4096)
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/example/echo",
				strings.NewReader(`{"message":"`+test.message+`"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status || client.callCount() != 1 {
				t.Errorf(
					"result = (status %d, calls %d), want (%d, 1)",
					recorder.Code,
					client.callCount(),
					test.status,
				)
			}
			if test.status == http.StatusOK &&
				recorder.Body.String() != `{"message":"`+test.message+`"}` {
				t.Errorf("Echo changed value: %q", recorder.Body.String())
			}
		})
	}
}

func TestHandler_UsesShorterOfCallerAndServiceDeadline(t *testing.T) {
	for _, test := range []struct {
		name          string
		callerTimeout time.Duration
		maximum       time.Duration
	}{
		{"service deadline ignores Grpc-Timeout", 0, 150 * time.Millisecond},
		{"shorter caller deadline", 10 * time.Millisecond, 100 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{waitForContext: true}
			handler := newTestHandlerWithTimeout(t, client, 40*time.Millisecond)
			ctx := context.Background()
			var cancel context.CancelFunc
			if test.callerTimeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, test.callerTimeout)
				defer cancel()
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/example/echo", strings.NewReader(`{"message":"hello"}`)).
				WithContext(ctx)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Grpc-Timeout", "999H")
			recorder := httptest.NewRecorder()
			started := time.Now()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusGatewayTimeout {
				t.Errorf("status = %d, want 504", recorder.Code)
			}
			if elapsed := time.Since(started); elapsed > test.maximum {
				t.Errorf(
					"deadline completed in %v, want within %v",
					elapsed,
					test.maximum,
				)
			}
		})
	}
}

func TestHandler_PropagatesCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	client := &fakeClient{waitForContext: true, started: started}
	handler := newTestHandlerWithTimeout(t, client, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/v1/example/echo", strings.NewReader(`{"message":"hello"}`)).
		WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("RPC did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled request did not finish")
	}
	if recorder.Code != 499 || recorder.Body.String() != errorBody(1, "request canceled") {
		t.Errorf("canceled response = (%d, %q)", recorder.Code, recorder.Body.String())
	}
}

func TestHandler_StripsIdentityAndTimeoutHeaders(t *testing.T) {
	client := &fakeClient{}
	handler := newTestHandler(t, client, 4096)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/example/echo",
		strings.NewReader(`{"message":"hello"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer private")
	request.Header.Set("Grpc-Timeout", "999H")
	request.Header.Set("Grpc-Metadata-Principal", "forged")
	request.Header.Set("Forwarded", "for=203.0.113.44")
	request.Header.Set("X-Forwarded-For", "203.0.113.44")
	request.Header.Set("X-Forwarded-Host", "private.example")
	request.Header.Set("X-Real-IP", "203.0.113.44")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	for _, marker := range []string{"private", "forged", "203.0.113.44", "private.example", "999H"} {
		if strings.Contains(fmt.Sprint(client.lastMetadata()), marker) {
			t.Errorf(
				"injected marker %q escaped in metadata: %v",
				marker,
				client.lastMetadata(),
			)
		}
	}
}

func TestClearApplicationMetadata_DropsAllApplicationValues(t *testing.T) {
	ctx := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs("authorization", "private", "principal", "forged"),
	)
	called := false
	err := ClearApplicationMetadata(
		ctx,
		"/example",
		nil,
		nil,
		nil,
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			called = true
			md, _ := metadata.FromOutgoingContext(ctx)
			if len(md) != 0 {
				t.Errorf("outgoing metadata = %v, want empty", md)
			}
			return nil
		},
	)
	if err != nil || !called {
		t.Fatalf("interceptor = (%t, %v)", called, err)
	}
}

func TestHandler_DeniesUpstreamHeadersAndTrailers(t *testing.T) {
	client := &fakeClient{responseMetadata: metadata.Pairs("private-header", "private-value")}
	handler := newTestHandler(t, client, 4096)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/example/echo",
		strings.NewReader(`{"message":"hello"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("TE", "trailers")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	for name, values := range recorder.Header() {
		if strings.Contains(strings.ToLower(name), "private") ||
			strings.Contains(fmt.Sprint(values), "private-value") {
			t.Errorf("response metadata escaped: %s=%v", name, values)
		}
	}
}

func TestHandler_HTTPOnlyFailuresUseBoundedSignalsWithoutSensitiveValues(t *testing.T) {
	const marker = "private-sensitive-marker"
	var logs bytes.Buffer
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	observer, err := telemetry.NewRequestObserver(
		slog.New(slog.NewJSONHandler(&logs, nil)), meterProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(&fakeClient{}, Settings{
		RequestTimeout: time.Second, MaxBodyBytes: 4096,
		TracerProvider: tracerProvider, MeterProvider: meterProvider,
		Propagator: propagation.TraceContext{}, Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	unknown := httptest.NewRequest(http.MethodGet, "/"+marker+"?value="+marker, nil)
	unknown.Header.Set("X-Custom", marker)
	handler.ServeHTTP(httptest.NewRecorder(), unknown)
	malformed := httptest.NewRequest(
		http.MethodPost,
		"/v1/example/echo",
		strings.NewReader(`{"message":"`+marker),
	)
	malformed.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), malformed)

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	combined := logs.String() + fmt.Sprint(metrics)
	for _, span := range spanRecorder.Ended() {
		combined += span.Name() + fmt.Sprint(span.Attributes(), span.Events())
	}
	if strings.Contains(combined, marker) {
		t.Fatalf("sensitive marker escaped into telemetry: %s", combined)
	}
	if !strings.Contains(combined, "POST /v1/example/echo") ||
		!strings.Contains(combined, "unmatched") ||
		!strings.Contains(combined, "insurance_hub.service.requests") {
		t.Errorf("missing bounded HTTP failure signals: %s", combined)
	}
}

func TestUnavailableHandler_WritesFixedEnvelopeAndTelemetry(t *testing.T) {
	// given
	var logs bytes.Buffer
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	observer, err := telemetry.NewRequestObserver(
		slog.New(slog.NewJSONHandler(&logs, nil)), meterProvider,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewUnavailableHandler(Settings{
		RequestTimeout: time.Second, MaxBodyBytes: 4096,
		TracerProvider: tracerProvider, MeterProvider: meterProvider,
		Propagator: propagation.TraceContext{}, Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/example/echo", nil))

	// then
	if recorder.Code != http.StatusServiceUnavailable ||
		recorder.Header().Get("Content-Type") != "application/json" ||
		recorder.Body.String() != errorBody(int(codes.Unavailable), "service unavailable") {
		t.Fatalf(
			"unavailable response = (%d, %q, %q)",
			recorder.Code,
			recorder.Header().Get("Content-Type"),
			recorder.Body.String(),
		)
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	combined := logs.String() + fmt.Sprint(metrics)
	for _, span := range spanRecorder.Ended() {
		combined += span.Name() + fmt.Sprint(span.Attributes())
	}
	if !strings.Contains(combined, "unmatched") ||
		!strings.Contains(combined, "insurance_hub.service.requests") {
		t.Errorf("missing drain-rejection telemetry: %s", combined)
	}
}

func newTestHandler(
	t *testing.T,
	client scaffoldv1.ExampleServiceClient,
	maxBody int,
) http.Handler {
	return newTestHandlerWithSettings(t, client, maxBody, time.Second)
}

func newTestHandlerWithTimeout(
	t *testing.T,
	client scaffoldv1.ExampleServiceClient,
	timeout time.Duration,
) http.Handler {
	return newTestHandlerWithSettings(t, client, 4096, timeout)
}

func newTestHandlerWithSettings(
	t *testing.T,
	client scaffoldv1.ExampleServiceClient,
	maxBody int,
	timeout time.Duration,
) http.Handler {
	t.Helper()
	meter := metricnoop.NewMeterProvider()
	observer, err := telemetry.NewRequestObserver(
		slog.New(slog.NewTextHandler(io.Discard, nil)), meter,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(client, Settings{
		RequestTimeout: timeout, MaxBodyBytes: maxBody,
		TracerProvider: noop.NewTracerProvider(), MeterProvider: meter,
		Propagator: propagation.TraceContext{}, Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

type fakeClient struct {
	mu               sync.Mutex
	calls            int
	metadata         metadata.MD
	err              error
	responseMetadata metadata.MD
	waitForContext   bool
	started          chan struct{}
}

func (client *fakeClient) Echo(
	ctx context.Context,
	request *scaffoldv1.EchoRequest,
	options ...grpc.CallOption,
) (*scaffoldv1.EchoResponse, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.calls++
	client.metadata, _ = metadata.FromOutgoingContext(ctx)
	for _, option := range options {
		switch selected := option.(type) {
		case grpc.HeaderCallOption:
			*selected.HeaderAddr = client.responseMetadata.Copy()
		case grpc.TrailerCallOption:
			*selected.TrailerAddr = client.responseMetadata.Copy()
		}
	}
	if client.err != nil {
		return nil, client.err
	}
	if client.waitForContext {
		if client.started != nil {
			close(client.started)
		}
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if request.GetMessage() == "" || len([]byte(request.GetMessage())) > 128 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	return &scaffoldv1.EchoResponse{Message: request.GetMessage()}, nil
}

func (client *fakeClient) callCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.calls
}

func (client *fakeClient) lastMetadata() metadata.MD {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.metadata.Copy()
}

func errorBody(code int, message string) string {
	return fmt.Sprintf(`{"code":%d,"message":"%s","details":[]}`, code, message)
}
