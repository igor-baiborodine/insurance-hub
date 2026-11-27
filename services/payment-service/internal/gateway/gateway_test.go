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

	paymentv1 "github.com/igor-baiborodine/insurance-hub/services/payment-service/gen/payment/v1"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/telemetry"
)

func TestHandler_AccountReadsUseLegacyJSONShapes(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		// given
		client := &fakeClient{
			list: &paymentv1.ListAccountsResponse{Accounts: []*paymentv1.Account{}},
		}
		handler := newTestHandler(t, client)
		recorder := httptest.NewRecorder()

		// when
		handler.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "/payment/accounts", nil),
		)

		// then
		if recorder.Code != http.StatusOK || recorder.Body.String() != "[]" {
			t.Fatalf(
				"response = (%d, %q), want (200, [])",
				recorder.Code,
				recorder.Body.String(),
			)
		}
	})

	t.Run("populated list", func(t *testing.T) {
		// given
		client := &fakeClient{
			list: &paymentv1.ListAccountsResponse{Accounts: []*paymentv1.Account{{
				PolicyAccountNumber: "payment-1",
				PolicyNumber:        "policy-1",
				Created:             1704164645006,
				Updated:             1704251045006,
			}}},
		}
		handler := newTestHandler(t, client)
		recorder := httptest.NewRecorder()

		// when
		handler.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "/payment/accounts", nil),
		)

		// then
		const want = `[{"policyAccountNumber":"payment-1","policyNumber":"policy-1","created":1704164645006,"updated":1704251045006}]`
		if recorder.Code != http.StatusOK || recorder.Body.String() != want {
			t.Fatalf(
				"response = (%d, %q), want (200, %q)",
				recorder.Code,
				recorder.Body.String(),
				want,
			)
		}
	})

	t.Run("dated balance", func(t *testing.T) {
		// given
		client := &fakeClient{detail: &paymentv1.GetAccountBalanceResponse{
			Account: &paymentv1.AccountBalance{
				PolicyAccountNumber: "payment-1",
				PolicyNumber:        "policy-1",
				Balance:             "12.3400",
				Created:             1704164645006,
				Updated:             1704251045006,
			},
		}}
		handler := newTestHandler(t, client)
		recorder := httptest.NewRecorder()

		// when
		handler.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "/payment/accounts/payment-1", nil),
		)

		// then
		const want = `{"policyAccountNumber":"payment-1","policyNumber":"policy-1","balance":12.3400,"created":1704164645006,"updated":1704251045006}`
		if recorder.Code != http.StatusOK || recorder.Body.String() != want ||
			client.accountNumber != "payment-1" {
			t.Fatalf(
				"response = (%d, %q), requested = %q, want (200, %q, payment-1)",
				recorder.Code,
				recorder.Body.String(),
				client.accountNumber,
				want,
			)
		}
	})
}

func TestHandler_EnforcesAccountRouteAdmission(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		accept string
		status int
	}{
		{
			name:   "list query",
			method: http.MethodGet,
			target: "/payment/accounts?x=1",
			status: 400,
		},
		{
			name:   "detail query",
			method: http.MethodGet,
			target: "/payment/accounts/a?x=1",
			status: 400,
		},
		{
			name:   "list accept",
			method: http.MethodGet,
			target: "/payment/accounts",
			accept: "text/plain",
			status: 406,
		},
		{
			name:   "wrong list method",
			method: http.MethodPost,
			target: "/payment/accounts",
			status: 405,
		},
		{
			name:   "wrong detail method",
			method: http.MethodPost,
			target: "/payment/accounts/a",
			status: 405,
		},
		{
			name:   "trailing slash",
			method: http.MethodGet,
			target: "/payment/accounts/",
			status: 404,
		},
		{
			name:   "nested detail",
			method: http.MethodGet,
			target: "/payment/accounts/a/b",
			status: 404,
		},
		{name: "unknown", method: http.MethodGet, target: "/unknown", status: 404},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			client := &fakeClient{}
			handler := newTestHandler(t, client)
			request := httptest.NewRequest(test.method, test.target, nil)
			request.Header.Set("Accept", test.accept)
			recorder := httptest.NewRecorder()

			// when
			handler.ServeHTTP(recorder, request)

			// then
			if recorder.Code != test.status {
				t.Fatalf(
					"status = %d, want %d: %s",
					recorder.Code,
					test.status,
					recorder.Body.String(),
				)
			}
			if client.calls != 0 {
				t.Errorf("client calls = %d, want zero", client.calls)
			}
		})
	}
}

func TestHandler_MissingAccountUsesSafeLegacyEnvelopeAndStableTelemetry(t *testing.T) {
	// given
	const marker = "private-account-marker"
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
	client := &fakeClient{detailErr: status.Error(codes.NotFound, marker)}
	handler, err := NewHandler(client, Settings{
		RequestTimeout: time.Second, MaxBodyBytes: 4096,
		TracerProvider: tracerProvider, MeterProvider: meterProvider,
		Propagator: propagation.TraceContext{}, Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/payment/accounts/"+marker, nil),
	)

	// then
	const want = `{"_links":{"self":{"href":"/payment/accounts/private-account-marker","templated":false}},"message":"Internal Server Error"}`
	if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != want {
		t.Fatalf(
			"response = (%d, %q), want (500, %q)",
			recorder.Code,
			recorder.Body.String(),
			want,
		)
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	combined := logs.String() + fmt.Sprint(metrics)
	for _, span := range spanRecorder.Ended() {
		combined += span.Name() + fmt.Sprint(span.Attributes(), span.Events())
	}
	if strings.Contains(combined, marker) {
		t.Fatalf("account identifier escaped into telemetry: %s", combined)
	}
	if !strings.Contains(combined, accountDetailRoute) ||
		!strings.Contains(combined, "get_account_balance") {
		t.Errorf("missing stable account signals: %s", combined)
	}
}

func TestClearApplicationMetadata_DropsCallerValues(t *testing.T) {
	// given
	ctx := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs("authorization", "private-token", "principal", "forged"),
	)
	var seen metadata.MD
	invoker := func(
		ctx context.Context,
		_ string,
		_, _ any,
		_ *grpc.ClientConn,
		_ ...grpc.CallOption,
	) error {
		seen, _ = metadata.FromOutgoingContext(ctx)
		return nil
	}

	// when
	err := ClearApplicationMetadata(
		ctx,
		"/payment.v1.PaymentService/ListAccounts",
		nil,
		nil,
		nil,
		invoker,
	)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 0 {
		t.Errorf("metadata = %v, want empty", seen)
	}
}

func TestUnavailableHandler_WritesFixedEnvelope(t *testing.T) {
	// given
	handler := newUnavailableTestHandler(t)
	recorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/payment/accounts", nil))

	// then
	const want = `{"code":14,"message":"service unavailable","details":[]}`
	if recorder.Code != http.StatusServiceUnavailable || recorder.Body.String() != want {
		t.Fatalf(
			"response = (%d, %q), want (503, %q)",
			recorder.Code,
			recorder.Body.String(),
			want,
		)
	}
}

func newTestHandler(t *testing.T, client paymentv1.PaymentServiceClient) http.Handler {
	t.Helper()
	settings := testSettings(t)
	handler, err := NewHandler(client, settings)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func newUnavailableTestHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := NewUnavailableHandler(testSettings(t))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func testSettings(t *testing.T) Settings {
	t.Helper()
	meter := metricnoop.NewMeterProvider()
	observer, err := telemetry.NewRequestObserver(
		slog.New(slog.NewTextHandler(io.Discard, nil)), meter,
	)
	if err != nil {
		t.Fatal(err)
	}
	return Settings{
		RequestTimeout: time.Second, MaxBodyBytes: 4096,
		TracerProvider: noop.NewTracerProvider(), MeterProvider: meter,
		Propagator: propagation.TraceContext{}, Observer: observer,
	}
}

type fakeClient struct {
	list          *paymentv1.ListAccountsResponse
	listErr       error
	detail        *paymentv1.GetAccountBalanceResponse
	detailErr     error
	accountNumber string
	calls         int
}

func (client *fakeClient) ListAccounts(
	context.Context,
	*paymentv1.ListAccountsRequest,
	...grpc.CallOption,
) (*paymentv1.ListAccountsResponse, error) {
	client.calls++
	if client.list == nil && client.listErr == nil {
		return &paymentv1.ListAccountsResponse{Accounts: []*paymentv1.Account{}}, nil
	}
	return client.list, client.listErr
}

func (client *fakeClient) GetAccountBalance(
	_ context.Context,
	request *paymentv1.GetAccountBalanceRequest,
	_ ...grpc.CallOption,
) (*paymentv1.GetAccountBalanceResponse, error) {
	client.calls++
	client.accountNumber = request.GetAccountNumber()
	return client.detail, client.detailErr
}
