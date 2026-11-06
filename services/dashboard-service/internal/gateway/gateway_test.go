package gateway

import (
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
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/telemetry"
)

func TestHandler_MapsAcceptedRequestsAndLegacyNumericResponses(t *testing.T) {
	// given
	for _, test := range []struct {
		name        string
		body        string
		query       string
		accept      string
		wantProduct *string
		response    *dashboardv1.GetTotalSalesResponse
		wantBody    string
	}{
		{
			name: "unknown ignored, duplicate last wins, query and accept ignored",
			body: `{"productCode":"first","aggregationUnitCode":"MONTH",` +
				`"productCode":" SAFE ","saleDateFrom":"2019-01-01",` +
				`"saleDateTo":"2020-01-01"}`,
			query:       "productCode=ignored",
			accept:      "text/plain",
			wantProduct: stringPointer(" SAFE "),
			response: &dashboardv1.GetTotalSalesResponse{
				Total: &dashboardv1.SalesValue{PoliciesCount: 5, PremiumAmountMinorUnits: 500000},
				PerProductTotal: map[string]*dashboardv1.SalesValue{
					"SAFE_FARM": {PoliciesCount: 1, PremiumAmountMinorUnits: 100000},
				},
			},
			wantBody: `{"total":{"policiesCount":5,"premiumAmount":5000.00},` +
				`"perProductTotal":{"SAFE_FARM":{"policiesCount":1,"premiumAmount":1000.00}}}`,
		},
		{
			name:     "null product means all and empty result omits map",
			body:     `{"productCode":null,"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
			response: &dashboardv1.GetTotalSalesResponse{Total: &dashboardv1.SalesValue{}},
			wantBody: `{"total":{"policiesCount":0,"premiumAmount":0}}`,
		},
		{
			name:        "empty product remains exact",
			body:        `{"productCode":"","saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
			wantProduct: stringPointer(""),
			response:    &dashboardv1.GetTotalSalesResponse{Total: &dashboardv1.SalesValue{}},
			wantBody:    `{"total":{"policiesCount":0,"premiumAmount":0}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			client := &fakeClient{response: test.response}
			handler := newTestHandler(t, client, 4096)
			request := httptest.NewRequest(
				http.MethodPost,
				totalSalesPath+map[bool]string{true: "?" + test.query}[test.query != ""],
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", "application/json; charset=UTF-8")
			request.Header.Set("Accept", test.accept)
			request.Header.Set("Authorization", "private")
			recorder := httptest.NewRecorder()

			// when
			handler.ServeHTTP(recorder, request)

			// then
			if recorder.Code != http.StatusOK ||
				recorder.Header().Get("Content-Type") != "application/json" ||
				recorder.Body.String() != test.wantBody {
				t.Fatalf(
					"response = (%d, %q, %q)",
					recorder.Code,
					recorder.Header(),
					recorder.Body.String(),
				)
			}
			if client.calls != 1 ||
				!sameStringPointer(client.request.ProductCode, test.wantProduct) ||
				client.request.GetSaleDateFrom() != "2019-01-01" ||
				client.request.GetSaleDateTo() != "2020-01-01" {
				t.Fatalf(
					"forwarded request = %+v; calls = %d",
					client.request,
					client.calls,
				)
			}
		})
	}
}

func TestHandler_MapsAgentRequestThroughGeneratedClientAndWritesExactNumber(t *testing.T) {
	// given
	client := &fakeClient{agentResponse: &dashboardv1.GetAgentSalesResponse{
		PerAgentTotal: map[string]*dashboardv1.AgentSalesValue{
			"m.smith": {PoliciesCount: 3, PremiumAmount: "1.005"},
		},
	}}
	handler := newTestHandler(t, client, 4096)
	request := httptest.NewRequest(
		http.MethodPost,
		agentSalesPath+"?ignored=value",
		strings.NewReader(
			`{"agentLogin":"first","aggregationUnitCode":"MONTH",`+
				`"agentLogin":"","productCode":" SAFE ",`+
				`"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(recorder, request)

	// then
	if recorder.Code != http.StatusOK || recorder.Body.String() !=
		`{"perAgentTotal":{"m.smith":{"policiesCount":3,"premiumAmount":1.005}}}` {
		t.Fatalf("response = (%d, %s)", recorder.Code, recorder.Body.String())
	}
	if client.agentCalls != 1 || client.agentRequest.GetAgentLogin() != "" ||
		client.agentRequest.AgentLogin == nil || client.agentRequest.GetProductCode() != " SAFE " ||
		client.agentRequest.GetSaleDateFrom() != "2019-01-01" ||
		client.agentRequest.GetSaleDateTo() != "2020-01-01" {
		t.Fatalf(
			"forwarded request/calls = (%+v, %d)",
			client.agentRequest,
			client.agentCalls,
		)
	}
}

func TestHandler_AgentEmptyAndInvalidRPCAmountsFailSafely(t *testing.T) {
	// given
	handler := newTestHandler(t, &fakeClient{
		agentResponse: &dashboardv1.GetAgentSalesResponse{
			PerAgentTotal: map[string]*dashboardv1.AgentSalesValue{},
		},
	}, 4096)
	request := httptest.NewRequest(http.MethodPost, agentSalesPath, strings.NewReader(
		`{"agentLogin":null,"productCode":null,"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	emptyRecorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(emptyRecorder, request)

	// then
	if emptyRecorder.Code != http.StatusOK ||
		emptyRecorder.Body.String() != `{"perAgentTotal":{}}` {
		t.Fatalf(
			"empty response = (%d, %s)",
			emptyRecorder.Code,
			emptyRecorder.Body.String(),
		)
	}

	for _, amount := range []string{"", "1.00E-7", "NaN"} {
		client := &fakeClient{agentResponse: &dashboardv1.GetAgentSalesResponse{
			PerAgentTotal: map[string]*dashboardv1.AgentSalesValue{
				"agent": {PoliciesCount: 1, PremiumAmount: amount},
			},
		}}
		handler = newTestHandler(t, client, 4096)
		request = httptest.NewRequest(http.MethodPost, agentSalesPath, strings.NewReader(
			`{"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
		))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()

		// when
		handler.ServeHTTP(recorder, request)

		// then
		assertError(
			t,
			recorder,
			http.StatusInternalServerError,
			`{"code":13,"message":"internal error","details":[]}`,
		)
	}
}

func TestHandler_MapsSalesTrendsRequestAndExactLegacyResponse(t *testing.T) {
	// given
	client := &fakeClient{trendResponse: &dashboardv1.GetSalesTrendsResponse{
		PeriodSales: []*dashboardv1.SalesTrendValue{
			{
				PeriodDate: "2020-02-01", Period: "independent-label",
				PoliciesCount: 9223372036854775807, PremiumAmount: "2000.00",
			},
		},
	}}
	handler := newTestHandler(t, client, 4096)
	request := httptest.NewRequest(
		http.MethodPost,
		salesTrendsPath+"?ignored=value",
		strings.NewReader(
			`{"aggregationUnitCode":"DAY","agentLogin":"ignored",`+
				`"productCode":"first","productCode":" SAFE ",`+
				`"saleDateFrom":"2020-01-01","saleDateTo":"2020-05-01",`+
				`"aggregationUnitCode":"MONTH"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(recorder, request)

	// then
	if recorder.Code != http.StatusOK || recorder.Body.String() !=
		`{"periodSales":[{"periodDate":[2020,2,1],"period":"independent-label",`+
			`"policiesCount":9223372036854775807,"premiumAmount":2000.00}]}` {
		t.Fatalf("response = (%d, %s)", recorder.Code, recorder.Body.String())
	}
	if client.trendCalls != 1 || client.trendRequest.GetProductCode() != " SAFE " ||
		client.trendRequest.ProductCode == nil ||
		client.trendRequest.GetSaleDateFrom() != "2020-01-01" ||
		client.trendRequest.GetSaleDateTo() != "2020-05-01" ||
		client.trendRequest.GetAggregationUnitCode() != "MONTH" {
		t.Fatalf(
			"forwarded request/calls = (%+v, %d)",
			client.trendRequest,
			client.trendCalls,
		)
	}
}

func TestHandler_SalesTrendsEmptyAndInvalidRPCValuesFailSafely(t *testing.T) {
	validBody := `{"saleDateFrom":"2020-01-01","saleDateTo":"2020-05-01",` +
		`"aggregationUnitCode":"MONTH"}`
	handler := newTestHandler(t, &fakeClient{
		trendResponse: &dashboardv1.GetSalesTrendsResponse{},
	}, 4096)
	request := httptest.NewRequest(
		http.MethodPost,
		salesTrendsPath,
		strings.NewReader(validBody),
	)
	request.Header.Set("Content-Type", "application/json")
	emptyRecorder := httptest.NewRecorder()

	// when
	handler.ServeHTTP(emptyRecorder, request)

	// then
	if emptyRecorder.Code != http.StatusOK || emptyRecorder.Body.String() != `{}` {
		t.Fatalf(
			"empty response = (%d, %s)",
			emptyRecorder.Code,
			emptyRecorder.Body.String(),
		)
	}

	for _, value := range []*dashboardv1.SalesTrendValue{
		nil,
		{PeriodDate: "2020-02-30", PoliciesCount: 1, PremiumAmount: "1.00"},
		{PeriodDate: "2020-02-01", PoliciesCount: -1, PremiumAmount: "1.00"},
		{PeriodDate: "2020-02-01", PoliciesCount: 1, PremiumAmount: "1.0"},
	} {
		client := &fakeClient{trendResponse: &dashboardv1.GetSalesTrendsResponse{
			PeriodSales: []*dashboardv1.SalesTrendValue{value},
		}}
		handler = newTestHandler(t, client, 4096)
		request = httptest.NewRequest(
			http.MethodPost,
			salesTrendsPath,
			strings.NewReader(validBody),
		)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		assertError(
			t,
			recorder,
			http.StatusInternalServerError,
			`{"code":13,"message":"internal error","details":[]}`,
		)
	}
}

func TestHandler_RejectsInvalidSalesTrendsScalarsBeforeRPC(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"saleDateFrom":null,"saleDateTo":"2020-05-01","aggregationUnitCode":"MONTH"}`,
		`{"saleDateFrom":"2020-01-01","saleDateTo":123,"aggregationUnitCode":"MONTH"}`,
		`{"saleDateFrom":"2020-01-01","saleDateTo":"2020-05-01"}`,
		`{"productCode":{},"saleDateFrom":"2020-01-01","saleDateTo":"2020-05-01",` +
			`"aggregationUnitCode":"MONTH"}`,
		`{"saleDateFrom":"2020-01-01","saleDateTo":"2020-05-01",` +
			`"aggregationUnitCode":null}`,
	} {
		client := &fakeClient{trendResponse: &dashboardv1.GetSalesTrendsResponse{}}
		handler := newTestHandler(t, client, 4096)
		request := httptest.NewRequest(
			http.MethodPost,
			salesTrendsPath,
			strings.NewReader(body),
		)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		assertError(
			t,
			recorder,
			http.StatusBadRequest,
			`{"code":3,"message":"invalid request","details":[]}`,
		)
		if client.trendCalls != 0 {
			t.Fatalf("invalid body reached sales-trends RPC: %s", body)
		}
	}
}

func TestHandler_RejectsInvalidAgentScalarsBeforeRPC(t *testing.T) {
	for _, body := range []string{
		`{"agentLogin":123,"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
		`{"productCode":{},"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
		`{"saleDateFrom":null,"saleDateTo":"2020-01-01"}`,
	} {
		// given
		client := &fakeClient{agentResponse: &dashboardv1.GetAgentSalesResponse{}}
		handler := newTestHandler(t, client, 4096)
		request := httptest.NewRequest(
			http.MethodPost,
			agentSalesPath,
			strings.NewReader(body),
		)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()

		// when
		handler.ServeHTTP(recorder, request)

		// then
		assertError(
			t,
			recorder,
			http.StatusBadRequest,
			`{"code":3,"message":"invalid request","details":[]}`,
		)
		if client.agentCalls != 0 {
			t.Fatalf("invalid body reached agent RPC: %s", body)
		}
	}
}

func TestHandler_RejectsInvalidBodiesBeforeRPC(t *testing.T) {
	// given
	for _, body := range []string{
		``, `null`, `{`, `{}`,
		`{"saleDateFrom":null,"saleDateTo":"2020-01-01"}`,
		`{"saleDateFrom":123,"saleDateTo":"2020-01-01"}`,
		`{"productCode":123,"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
		`{"saleDateFrom":"2019-01-01"}`,
		`{"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"} trailing`,
	} {
		// given
		client := &fakeClient{response: emptyResponse()}
		handler := newTestHandler(t, client, 4096)
		request := httptest.NewRequest(
			http.MethodPost,
			totalSalesPath,
			strings.NewReader(body),
		)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()

		// when
		handler.ServeHTTP(recorder, request)

		// then
		assertError(
			t,
			recorder,
			http.StatusBadRequest,
			`{"code":3,"message":"invalid request","details":[]}`,
		)
		if client.calls != 0 {
			t.Errorf("body %q reached RPC", body)
		}
	}
}

func TestHandler_EnforcesMediaMethodRouteAndBodyLimit(t *testing.T) {
	// given
	valid := `{"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`
	for _, test := range []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
		limit       int
		wantStatus  int
		wantBody    string
		wantAllow   string
	}{
		{
			name: "media", method: http.MethodPost, path: totalSalesPath, contentType: "text/plain",
			body: valid, limit: 4096, wantStatus: http.StatusUnsupportedMediaType,
			wantBody: `{"code":3,"message":"unsupported media type","details":[]}`,
		},
		{
			name: "agent media", method: http.MethodPost, path: agentSalesPath, contentType: "text/plain",
			body: valid, limit: 4096, wantStatus: http.StatusUnsupportedMediaType,
			wantBody: `{"code":3,"message":"unsupported media type","details":[]}`,
		},
		{
			name: "trends media", method: http.MethodPost, path: salesTrendsPath, contentType: "text/plain",
			body: valid, limit: 4096, wantStatus: http.StatusUnsupportedMediaType,
			wantBody: `{"code":3,"message":"unsupported media type","details":[]}`,
		},
		{
			name: "method", method: http.MethodGet, path: totalSalesPath, contentType: "application/json",
			limit: 4096, wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodPost,
			wantBody: `{"code":12,"message":"method not allowed","details":[]}`,
		},
		{
			name: "agent method", method: http.MethodGet, path: agentSalesPath, contentType: "application/json",
			limit: 4096, wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodPost,
			wantBody: `{"code":12,"message":"method not allowed","details":[]}`,
		},
		{
			name: "trends method", method: http.MethodGet, path: salesTrendsPath, contentType: "application/json",
			limit: 4096, wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodPost,
			wantBody: `{"code":12,"message":"method not allowed","details":[]}`,
		},
		{
			name: "route", method: http.MethodPost, path: "/dashboard/unknown", contentType: "application/json",
			limit: 4096, wantStatus: http.StatusNotFound,
			wantBody: `{"code":5,"message":"not found","details":[]}`,
		},
		{
			name: "too large", method: http.MethodPost, path: totalSalesPath, contentType: "application/json",
			body: valid + " ", limit: len(valid), wantStatus: http.StatusRequestEntityTooLarge,
			wantBody: `{"code":8,"message":"request body too large","details":[]}`,
		},
		{
			name: "agent too large", method: http.MethodPost, path: agentSalesPath, contentType: "application/json",
			body: valid + " ", limit: len(valid), wantStatus: http.StatusRequestEntityTooLarge,
			wantBody: `{"code":8,"message":"request body too large","details":[]}`,
		},
		{
			name: "trends too large", method: http.MethodPost, path: salesTrendsPath, contentType: "application/json",
			body: valid + " ", limit: len(valid), wantStatus: http.StatusRequestEntityTooLarge,
			wantBody: `{"code":8,"message":"request body too large","details":[]}`,
		},
		{
			name: "exact limit", method: http.MethodPost, path: totalSalesPath, contentType: "application/json",
			body: valid, limit: len(valid), wantStatus: http.StatusOK,
			wantBody: `{"total":{"policiesCount":0,"premiumAmount":0}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			client := &fakeClient{response: emptyResponse()}
			handler := newTestHandler(t, client, test.limit)
			request := httptest.NewRequest(
				test.method,
				test.path,
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", test.contentType)
			recorder := httptest.NewRecorder()

			// when
			handler.ServeHTTP(recorder, request)

			// then
			if recorder.Code != test.wantStatus ||
				recorder.Body.String() != test.wantBody ||
				recorder.Header().Get("Allow") != test.wantAllow {
				t.Fatalf(
					"response = (%d, %q, %q)",
					recorder.Code,
					recorder.Header(),
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestHandler_MapsRPCFailuresWithoutLeakingDetails(t *testing.T) {
	// given
	for _, test := range []struct {
		code       codes.Code
		wantStatus int
		wantBody   string
	}{
		{codes.InvalidArgument, 400, `{"code":3,"message":"invalid request","details":[]}`},
		{codes.Canceled, 499, `{"code":1,"message":"request canceled","details":[]}`},
		{codes.DeadlineExceeded, 504, `{"code":4,"message":"deadline exceeded","details":[]}`},
		{codes.Unavailable, 503, `{"code":14,"message":"service unavailable","details":[]}`},
		{codes.ResourceExhausted, 500, `{"code":13,"message":"internal error","details":[]}`},
		{codes.Internal, 500, `{"code":13,"message":"internal error","details":[]}`},
	} {
		// given
		client := &fakeClient{err: status.Error(test.code, "private-upstream-detail")}
		handler := newTestHandler(t, client, 4096)
		request := httptest.NewRequest(http.MethodPost, totalSalesPath, strings.NewReader(
			`{"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
		))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()

		// when
		handler.ServeHTTP(recorder, request)

		// then
		assertError(t, recorder, test.wantStatus, test.wantBody)
		if strings.Contains(recorder.Body.String(), "private") {
			t.Fatal("private RPC detail leaked")
		}
	}
}

func TestHandler_UsesShorterDeadlineAndPropagatesCancellation(t *testing.T) {
	// given
	for _, test := range []struct {
		name          string
		callerTimeout time.Duration
		cancel        bool
		maximum       time.Duration
		wantStatus    int
	}{
		{
			name: "service deadline", maximum: 200 * time.Millisecond,
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name: "shorter caller deadline", callerTimeout: 20 * time.Millisecond,
			maximum: 150 * time.Millisecond, wantStatus: http.StatusGatewayTimeout,
		},
		{
			name: "caller cancellation", cancel: true, maximum: 150 * time.Millisecond,
			wantStatus: 499,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			client := &fakeClient{waitForContext: true, started: make(chan struct{})}
			handler := newTestHandlerWithTimeout(t, client, 40*time.Millisecond)
			ctx := context.Background()
			cancel := context.CancelFunc(func() {})
			if test.callerTimeout > 0 {
				ctx, cancel = context.WithTimeout(ctx, test.callerTimeout)
			} else if test.cancel {
				ctx, cancel = context.WithCancel(ctx)
			}
			defer cancel()
			request := httptest.NewRequest(
				http.MethodPost,
				totalSalesPath,
				strings.NewReader(
					`{"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
				),
			).WithContext(ctx)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			done := make(chan struct{})
			started := time.Now()

			// when
			go func() {
				handler.ServeHTTP(recorder, request)
				close(done)
			}()
			select {
			case <-client.started:
			case <-time.After(time.Second):
				t.Fatal("RPC did not start")
			}
			if test.cancel {
				cancel()
			}

			// then
			select {
			case <-done:
			case <-time.After(test.maximum):
				t.Fatal("bounded request did not finish")
			}
			if recorder.Code != test.wantStatus || time.Since(started) > test.maximum {
				t.Fatalf(
					"response = %d after %s",
					recorder.Code,
					time.Since(started),
				)
			}
		})
	}
}

func TestHandler_StripsApplicationMetadataAndDeniesRPCMetadata(t *testing.T) {
	// given
	client := &fakeClient{
		response:         emptyResponse(),
		responseMetadata: metadata.Pairs("private-header", "private-value"),
	}
	handler := newTestHandler(t, client, 4096)
	request := httptest.NewRequest(
		http.MethodPost,
		totalSalesPath,
		strings.NewReader(`{"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`),
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
	for _, marker := range []string{
		"private", "forged", "203.0.113.44", "private.example", "999H",
	} {
		if strings.Contains(fmt.Sprint(client.requestMetadata), marker) {
			t.Errorf("injected marker %q escaped: %v", marker, client.requestMetadata)
		}
	}
	for name, values := range recorder.Header() {
		if strings.Contains(strings.ToLower(name), "private") ||
			strings.Contains(strings.Join(values, ","), "private-value") {
			t.Errorf("response metadata escaped: %s=%v", name, values)
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
		"/dashboard.v1.DashboardService/GetTotalSales",
		nil,
		nil,
		nil,
		func(
			ctx context.Context,
			_ string,
			_, _ any,
			_ *grpc.ClientConn,
			_ ...grpc.CallOption,
		) error {
			called = true
			outgoing, _ := metadata.FromOutgoingContext(ctx)
			if len(outgoing) != 0 {
				t.Errorf("outgoing metadata = %v, want empty", outgoing)
			}
			return nil
		},
	)

	// then
	if err != nil || !called {
		t.Fatalf("interceptor = (%t, %v)", called, err)
	}
}

func TestMarshalLegacyResponse_HandlesSignedMinorUnitExtremes(t *testing.T) {
	// given
	response := &dashboardv1.GetTotalSalesResponse{
		Total: &dashboardv1.SalesValue{
			PoliciesCount:           1,
			PremiumAmountMinorUnits: -9223372036854775808,
		},
		PerProductTotal: map[string]*dashboardv1.SalesValue{
			"x": {PoliciesCount: 1, PremiumAmountMinorUnits: 5},
		},
	}

	// when
	data, err := marshalLegacyResponse(response)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"total":{"policiesCount":1,"premiumAmount":-92233720368547758.08},`+
		`"perProductTotal":{"x":{"policiesCount":1,"premiumAmount":0.05}}}` {
		t.Fatalf("response = %s", data)
	}
}

type fakeClient struct {
	request          *dashboardv1.GetTotalSalesRequest
	response         *dashboardv1.GetTotalSalesResponse
	err              error
	calls            int
	waitForContext   bool
	started          chan struct{}
	requestMetadata  metadata.MD
	responseMetadata metadata.MD
	agentRequest     *dashboardv1.GetAgentSalesRequest
	agentResponse    *dashboardv1.GetAgentSalesResponse
	agentCalls       int
	trendRequest     *dashboardv1.GetSalesTrendsRequest
	trendResponse    *dashboardv1.GetSalesTrendsResponse
	trendCalls       int
}

func (client *fakeClient) GetTotalSales(
	ctx context.Context,
	request *dashboardv1.GetTotalSalesRequest,
	options ...grpc.CallOption,
) (*dashboardv1.GetTotalSalesResponse, error) {
	client.calls++
	client.request = request
	client.requestMetadata, _ = metadata.FromOutgoingContext(ctx)
	for _, option := range options {
		switch value := option.(type) {
		case grpc.HeaderCallOption:
			*value.HeaderAddr = client.responseMetadata.Copy()
		case grpc.TrailerCallOption:
			*value.TrailerAddr = client.responseMetadata.Copy()
		}
	}
	if client.started != nil {
		close(client.started)
	}
	if client.waitForContext {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return client.response, client.err
}

func (client *fakeClient) GetAgentSales(
	ctx context.Context,
	request *dashboardv1.GetAgentSalesRequest,
	options ...grpc.CallOption,
) (*dashboardv1.GetAgentSalesResponse, error) {
	client.agentCalls++
	client.agentRequest = request
	client.requestMetadata, _ = metadata.FromOutgoingContext(ctx)
	for _, option := range options {
		switch value := option.(type) {
		case grpc.HeaderCallOption:
			*value.HeaderAddr = client.responseMetadata.Copy()
		case grpc.TrailerCallOption:
			*value.TrailerAddr = client.responseMetadata.Copy()
		}
	}
	if client.started != nil {
		close(client.started)
	}
	if client.waitForContext {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return client.agentResponse, client.err
}

func (client *fakeClient) GetSalesTrends(
	ctx context.Context,
	request *dashboardv1.GetSalesTrendsRequest,
	options ...grpc.CallOption,
) (*dashboardv1.GetSalesTrendsResponse, error) {
	client.trendCalls++
	client.trendRequest = request
	client.requestMetadata, _ = metadata.FromOutgoingContext(ctx)
	for _, option := range options {
		switch value := option.(type) {
		case grpc.HeaderCallOption:
			*value.HeaderAddr = client.responseMetadata.Copy()
		case grpc.TrailerCallOption:
			*value.TrailerAddr = client.responseMetadata.Copy()
		}
	}
	if client.started != nil {
		close(client.started)
	}
	if client.waitForContext {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return client.trendResponse, client.err
}

func newTestHandler(
	t *testing.T,
	client dashboardv1.DashboardServiceClient,
	limit int,
) http.Handler {
	return newTestHandlerWithTimeout(t, client, time.Second, limit)
}

func newTestHandlerWithTimeout(
	t *testing.T,
	client dashboardv1.DashboardServiceClient,
	timeout time.Duration,
	limits ...int,
) http.Handler {
	t.Helper()
	limit := 4096
	if len(limits) > 0 {
		limit = limits[0]
	}
	observer, err := telemetry.NewRequestObserver(
		slog.New(slog.NewJSONHandler(io.Discard, nil)), metricnoop.NewMeterProvider(),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(client, Settings{
		RequestTimeout: timeout,
		MaxBodyBytes:   limit,
		TracerProvider: noop.NewTracerProvider(),
		MeterProvider:  metricnoop.NewMeterProvider(),
		Propagator:     propagation.TraceContext{},
		Observer:       observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func emptyResponse() *dashboardv1.GetTotalSalesResponse {
	return &dashboardv1.GetTotalSalesResponse{Total: &dashboardv1.SalesValue{}}
}

func assertError(t *testing.T, recorder *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if recorder.Code != status || recorder.Header().Get("Content-Type") != "application/json" ||
		recorder.Body.String() != body {
		t.Fatalf(
			"response = (%d, %q, %q)",
			recorder.Code,
			recorder.Header(),
			recorder.Body.String(),
		)
	}
}

func sameStringPointer(left, right *string) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func stringPointer(value string) *string { return &value }

var _ dashboardv1.DashboardServiceClient = (*fakeClient)(nil)
