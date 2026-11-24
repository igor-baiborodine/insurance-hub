package gateway

import (
	"bytes"
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
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	policyv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/policy/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/telemetry"
)

func TestHandler_EnforcesIndependentHTTPContract(t *testing.T) {
	// given
	tests := []struct {
		name, method, path, body, contentType, accept string
		wantStatus, wantCalls                         int
		wantBody                                      string
	}{
		{
			"valid",
			http.MethodPost,
			"/offers",
			`{"productCode":"TRI"}`,
			"application/json",
			"application/json",
			200,
			1,
			`{"offerNumber":"offer-1","totalPrice":78,"coversPrices":{"C1":26}}`,
		},
		{
			"valid charset",
			http.MethodPost,
			"/offers",
			`{"productCode":"é"}`,
			"application/json; charset=utf-8",
			"*/*",
			200,
			1,
			`{"offerNumber":"offer-1","totalPrice":78,"coversPrices":{"C1":26}}`,
		},
		{
			"missing value reaches RPC",
			http.MethodPost,
			"/offers",
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
			"/offers",
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
			"/offers",
			`{"productCode":"A","productCode":"B"}`,
			"application/json",
			"",
			400,
			0,
			errorBody(3, "invalid request"),
		},
		{
			"unknown member",
			http.MethodPost,
			"/offers",
			`{"productCode":"TRI","secret":"b"}`,
			"application/json",
			"",
			400,
			0,
			errorBody(3, "invalid request"),
		},
		{
			"wrong type",
			http.MethodPost,
			"/offers",
			`{"productCode":7}`,
			"application/json",
			"",
			400,
			0,
			errorBody(3, "invalid request"),
		},
		{
			"query",
			http.MethodPost,
			"/offers?secret=value",
			`{"productCode":"TRI"}`,
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
			"/offers/",
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
			"/offers",
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
			"/offers",
			`{"productCode":"TRI"}`,
			"application/json",
			"text/plain",
			406,
			0,
			errorBody(3, "not acceptable"),
		},
		{
			"zero quality response",
			http.MethodPost,
			"/offers",
			`{"productCode":"TRI"}`,
			"application/json",
			"application/json;q=0",
			406,
			0,
			errorBody(3, "not acceptable"),
		},
		{
			"parameterized response",
			http.MethodPost,
			"/offers",
			`{"productCode":"TRI"}`,
			"application/json",
			"application/json;profile=private",
			406,
			0,
			errorBody(3, "not acceptable"),
		},
		{
			"unsupported",
			http.MethodPost,
			"/offers",
			`{"productCode":"TRI"}`,
			"text/plain",
			"",
			415,
			0,
			errorBody(3, "unsupported media type"),
		},
		{
			"too large",
			http.MethodPost,
			"/offers",
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
			// when
			handler.ServeHTTP(recorder, request)
			// then
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

func TestHandler_CreateOffer_MapsLegacyRequestToRPC(t *testing.T) {
	// given
	body := `{"productCode":"TRI","policyFrom":"2017-04-16","policyTo":[2018,4,15],` +
		`"selectedCovers":["C1","C2"],"answers":[` +
		`{"type":"choice","questionCode":"DESTINATION","answer":"EUR"},` +
		`{"questionCode":"NOTE","answer":"first trip","type":"text"},` +
		`{"type":"numeric","questionCode":"NUM_OF_ADULTS","answer":1},` +
		`{"type":"numeric","questionCode":"LUGGAGE","answer":100.10}]}`
	want := &policyv1.CreateOfferRequest{
		ProductCode:    "TRI",
		PolicyFrom:     "2017-04-16",
		PolicyTo:       "2018-04-15",
		SelectedCovers: []string{"C1", "C2"},
		Answers: []*policyv1.OfferAnswer{
			{
				QuestionCode: "DESTINATION",
				Value:        &policyv1.OfferAnswer_Choice{Choice: "EUR"},
			},
			{
				QuestionCode: "NOTE",
				Value:        &policyv1.OfferAnswer_Text{Text: "first trip"},
			},
			{
				QuestionCode: "NUM_OF_ADULTS",
				Value:        &policyv1.OfferAnswer_Number{Number: "1"},
			},
			{
				QuestionCode: "LUGGAGE",
				Value:        &policyv1.OfferAnswer_Number{Number: "100.10"},
			},
		},
	}
	client := &fakeClient{offer: &policyv1.CreateOfferResponse{
		OfferNumber:  "offer-1",
		TotalPrice:   "78",
		CoversPrices: map[string]string{"C1": "26"},
	}}
	handler := newTestHandler(t, client, 4096)

	// when
	recorder := postOffer(handler, body)

	// then
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", recorder.Code, recorder.Body.String())
	}
	if got := client.lastOfferRequest(); !proto.Equal(got, want) {
		t.Errorf("RPC request = %v, want %v", got, want)
	}
}

func TestHandler_CreateOffer_SendsNumericAnswersAsPlainDecimals(t *testing.T) {
	// given
	for _, test := range []struct{ name, answer, want string }{
		{"plain decimal keeps its text", "1.50", "1.50"},
		{"positive exponent", "1e2", "100"},
		{"upper-case exponent with fraction", "2.5E+1", "25"},
		{"exponent keeping trailing zero", "1.50e1", "15.0"},
		{"negative exponent", "1.5e-3", "0.0015"},
		{"negative number", "-2.50e1", "-25.0"},
		{"leading zero mantissa", "0.5e1", "5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{}
			handler := newTestHandler(t, client, 4096)
			body := `{"productCode":"TRI","answers":[` +
				`{"type":"numeric","questionCode":"N","answer":` + test.answer + `}]}`

			// when
			recorder := postOffer(handler, body)

			// then
			got := client.lastOfferRequest().GetAnswers()[0].GetNumber()
			if recorder.Code != http.StatusOK || got != test.want {
				t.Errorf(
					"status %d, RPC number %q, want %q",
					recorder.Code,
					got,
					test.want,
				)
			}
		})
	}
}

func TestHandler_CreateOffer_RejectsNumericAnswerBeyondStoredLength(t *testing.T) {
	for _, answer := range []string{"1e300", "1e-300", "1" + strings.Repeat("0", 255)} {
		t.Run(answer[:4], func(t *testing.T) {
			// given
			client := &fakeClient{}
			handler := newTestHandler(t, client, 4096)
			body := `{"productCode":"TRI","answers":[` +
				`{"type":"numeric","questionCode":"N","answer":` + answer + `}]}`

			// when
			recorder := postOffer(handler, body)

			// then
			if recorder.Code != http.StatusBadRequest || client.callCount() != 0 {
				t.Errorf("status %d, calls %d", recorder.Code, client.callCount())
			}
		})
	}
}

func TestHandler_CreateOffer_WritesExactNumericAmounts(t *testing.T) {
	// given
	client := &fakeClient{offer: &policyv1.CreateOfferResponse{
		OfferNumber:  "5f3d2a1b-7c6e-4d8f-9a0b-1c2d3e4f5a66",
		TotalPrice:   "123.45",
		CoversPrices: map[string]string{"C2": "23.35", "C1": "100.10"},
	}}
	handler := newTestHandler(t, client, 4096)

	// when
	recorder := postOffer(handler, `{"productCode":"TRI","policyFrom":"2017-04-16",`+
		`"policyTo":"2018-04-15","selectedCovers":["C1","C2"]}`)

	// then
	want := `{"offerNumber":"5f3d2a1b-7c6e-4d8f-9a0b-1c2d3e4f5a66","totalPrice":123.45,` +
		`"coversPrices":{"C1":100.10,"C2":23.35}}`
	if recorder.Code != http.StatusOK || recorder.Body.String() != want {
		t.Errorf(
			"response = (%d, %s), want %s",
			recorder.Code,
			recorder.Body.String(),
			want,
		)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q", got)
	}
}

func TestHandler_CreateOffer_RejectsInvalidAmountWithoutLeakingIt(t *testing.T) {
	// given
	client := &fakeClient{offer: &policyv1.CreateOfferResponse{
		OfferNumber: "offer-1", TotalPrice: "not-a-number",
	}}
	handler := newTestHandler(t, client, 4096)

	// when
	recorder := postOffer(handler, `{"productCode":"TRI"}`)

	// then
	if recorder.Code != http.StatusInternalServerError ||
		strings.Contains(recorder.Body.String(), "not-a-number") {
		t.Errorf("response = (%d, %q)", recorder.Code, recorder.Body.String())
	}
}

func TestHandler_CreateOffer_RejectsMalformedLegacyRequests(t *testing.T) {
	// given
	valid := `"productCode":"TRI","policyFrom":"2017-04-16","policyTo":"2018-04-15",` +
		`"selectedCovers":["C1"]`
	for _, test := range []struct{ name, body string }{
		{"unknown member", `{` + valid + `,"agent":"x"}`},
		{"duplicate member", `{` + valid + `,"productCode":"TRI"}`},
		{"numeric answer as string", `{` + valid +
			`,"answers":[{"type":"numeric","questionCode":"N","answer":"1"}]}`},
		{"choice answer as number", `{` + valid +
			`,"answers":[{"type":"choice","questionCode":"C","answer":1}]}`},
		{"unknown answer type", `{` + valid +
			`,"answers":[{"type":"date","questionCode":"D","answer":"2017-01-01"}]}`},
		{"missing answer type", `{` + valid + `,"answers":[{"questionCode":"T","answer":"x"}]}`},
		{"unknown answer member", `{` + valid +
			`,"answers":[{"type":"text","questionCode":"T","answer":"x","extra":1}]}`},
		{"invalid date array month", `{"policyFrom":[2017,13,1]}`},
		{"short date array", `{"policyFrom":[2017,4]}`},
		{"date number", `{"policyFrom":20170416}`},
		{"trailing data", `{` + valid + `} {}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{}
			handler := newTestHandler(t, client, 4096)

			// when
			recorder := postOffer(handler, test.body)

			// then
			if recorder.Code != http.StatusBadRequest ||
				recorder.Body.String() != errorBody(3, "invalid request") ||
				client.callCount() != 0 {
				t.Errorf("response = (%d, %q), calls %d",
					recorder.Code, recorder.Body.String(), client.callCount())
			}
		})
	}
}

func TestHandler_CreateOffer_MapsUpstreamFailuresWithoutDetails(t *testing.T) {
	// given
	privateMarker := "private-upstream-marker"
	for _, test := range []struct {
		name string
		err  error
		want int
		body string
	}{
		{"invalid", status.Error(codes.InvalidArgument, privateMarker), 400, errorBody(3, "invalid request")},
		{"not found", status.Error(codes.NotFound, privateMarker), 404, errorBody(5, "not found")},
		{"unavailable", status.Error(codes.Unavailable, privateMarker), 503, errorBody(14, "service unavailable")},
		{"deadline", status.Error(codes.DeadlineExceeded, privateMarker), 504, errorBody(4, "deadline exceeded")},
		{"canceled", status.Error(codes.Canceled, privateMarker), 499, errorBody(1, "request canceled")},
		{"internal", status.Error(codes.Internal, privateMarker), 500, errorBody(13, "internal error")},
		{"unimplemented", status.Error(codes.Unimplemented, privateMarker), 500, errorBody(13, "internal error")},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{err: test.err}
			handler := newTestHandler(t, client, 4096)

			// when
			recorder := postOffer(handler, `{"productCode":"TRI"}`)

			// then
			if recorder.Code != test.want || recorder.Body.String() != test.body {
				t.Errorf(
					"response = (%d, %q)",
					recorder.Code,
					recorder.Body.String(),
				)
			}
		})
	}
}

func postOffer(handler http.Handler, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/offers", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestHandler_UsesShorterOfCallerAndServiceDeadline(t *testing.T) {
	// given
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
			request := httptest.NewRequest(http.MethodPost, "/offers", strings.NewReader(`{"productCode":"TRI"}`)).
				WithContext(ctx)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Grpc-Timeout", "999H")
			recorder := httptest.NewRecorder()
			started := time.Now()
			// when
			handler.ServeHTTP(recorder, request)
			// then
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
	// given
	started := make(chan struct{})
	client := &fakeClient{waitForContext: true, started: started}
	handler := newTestHandlerWithTimeout(t, client, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/offers", strings.NewReader(`{"productCode":"TRI"}`)).
		WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	// when
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
	// then
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
	// given
	client := &fakeClient{}
	handler := newTestHandler(t, client, 4096)
	request := httptest.NewRequest(
		http.MethodPost,
		"/offers",
		strings.NewReader(`{"productCode":"TRI"}`),
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
	// when
	handler.ServeHTTP(recorder, request)
	// then
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
	// given
	ctx := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs("authorization", "private", "principal", "forged"),
	)
	called := false
	// when
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
	// then
	if err != nil || !called {
		t.Fatalf("interceptor = (%t, %v)", called, err)
	}
}

func TestHandler_DeniesUpstreamHeadersAndTrailers(t *testing.T) {
	// given
	client := &fakeClient{responseMetadata: metadata.Pairs("private-header", "private-value")}
	handler := newTestHandler(t, client, 4096)
	request := httptest.NewRequest(
		http.MethodPost,
		"/offers",
		strings.NewReader(`{"productCode":"TRI"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("TE", "trailers")
	recorder := httptest.NewRecorder()
	// when
	handler.ServeHTTP(recorder, request)
	// then
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
	// given
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
	// when
	handler.ServeHTTP(httptest.NewRecorder(), unknown)
	malformed := httptest.NewRequest(
		http.MethodPost,
		"/offers",
		strings.NewReader(`{"productCode":"`+marker),
	)
	malformed.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), malformed)

	// then
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
	if !strings.Contains(combined, "POST /offers") ||
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
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/offers", nil))

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
	client *fakeClient,
	maxBody int,
) http.Handler {
	return newTestHandlerWithSettings(t, client, maxBody, time.Second)
}

func newTestHandlerWithTimeout(
	t *testing.T,
	client *fakeClient,
	timeout time.Duration,
) http.Handler {
	return newTestHandlerWithSettings(t, client, 4096, timeout)
}

func newTestHandlerWithSettings(
	t *testing.T,
	client *fakeClient,
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
	// PolicyServiceClient leaves the RPCs this gateway does not route unimplemented.
	policyv1.PolicyServiceClient
	mu               sync.Mutex
	calls            int
	metadata         metadata.MD
	err              error
	responseMetadata metadata.MD
	waitForContext   bool
	started          chan struct{}
	offer            *policyv1.CreateOfferResponse
	offerRequest     *policyv1.CreateOfferRequest
}

func (client *fakeClient) CreateOffer(
	ctx context.Context,
	request *policyv1.CreateOfferRequest,
	options ...grpc.CallOption,
) (*policyv1.CreateOfferResponse, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.calls++
	client.offerRequest = request
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
		select {
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-time.After(5 * time.Second):
			return nil, status.Error(codes.Internal, "fake RPC had no deadline")
		}
	}
	if request.GetProductCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if client.offer == nil {
		return &policyv1.CreateOfferResponse{
			OfferNumber:  "offer-1",
			TotalPrice:   "78",
			CoversPrices: map[string]string{"C1": "26"},
		}, nil
	}
	return client.offer, nil
}

func (client *fakeClient) lastOfferRequest() *policyv1.CreateOfferRequest {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.offerRequest
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
