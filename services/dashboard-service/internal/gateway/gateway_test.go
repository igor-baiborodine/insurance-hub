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

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/telemetry"
)

func TestHandler_MapsAcceptedRequestsAndLegacyNumericResponses(t *testing.T) {
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

			handler.ServeHTTP(recorder, request)

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

func TestHandler_RejectsInvalidBodiesBeforeRPC(t *testing.T) {
	for _, body := range []string{
		``, `null`, `{`, `{}`,
		`{"saleDateFrom":null,"saleDateTo":"2020-01-01"}`,
		`{"saleDateFrom":123,"saleDateTo":"2020-01-01"}`,
		`{"productCode":123,"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
		`{"saleDateFrom":"2019-01-01"}`,
		`{"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"} trailing`,
	} {
		client := &fakeClient{response: emptyResponse()}
		handler := newTestHandler(t, client, 4096)
		request := httptest.NewRequest(
			http.MethodPost,
			totalSalesPath,
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
		if client.calls != 0 {
			t.Errorf("body %q reached RPC", body)
		}
	}
}

func TestHandler_EnforcesMediaMethodRouteAndBodyLimit(t *testing.T) {
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
			name: "method", method: http.MethodGet, path: totalSalesPath, contentType: "application/json",
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
			name: "exact limit", method: http.MethodPost, path: totalSalesPath, contentType: "application/json",
			body: valid, limit: len(valid), wantStatus: http.StatusOK,
			wantBody: `{"total":{"policiesCount":0,"premiumAmount":0}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{response: emptyResponse()}
			handler := newTestHandler(t, client, test.limit)
			request := httptest.NewRequest(
				test.method,
				test.path,
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", test.contentType)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
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
		client := &fakeClient{err: status.Error(test.code, "private-upstream-detail")}
		handler := newTestHandler(t, client, 4096)
		request := httptest.NewRequest(http.MethodPost, totalSalesPath, strings.NewReader(
			`{"saleDateFrom":"2019-01-01","saleDateTo":"2020-01-01"}`,
		))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		assertError(t, recorder, test.wantStatus, test.wantBody)
		if strings.Contains(recorder.Body.String(), "private") {
			t.Fatal("private RPC detail leaked")
		}
	}
}

func TestMarshalLegacyResponse_HandlesSignedMinorUnitExtremes(t *testing.T) {
	response := &dashboardv1.GetTotalSalesResponse{
		Total: &dashboardv1.SalesValue{
			PoliciesCount:           1,
			PremiumAmountMinorUnits: -9223372036854775808,
		},
		PerProductTotal: map[string]*dashboardv1.SalesValue{
			"x": {PoliciesCount: 1, PremiumAmountMinorUnits: 5},
		},
	}
	data, err := marshalLegacyResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"total":{"policiesCount":1,"premiumAmount":-92233720368547758.08},`+
		`"perProductTotal":{"x":{"policiesCount":1,"premiumAmount":0.05}}}` {
		t.Fatalf("response = %s", data)
	}
}

type fakeClient struct {
	request  *dashboardv1.GetTotalSalesRequest
	response *dashboardv1.GetTotalSalesResponse
	err      error
	calls    int
}

func (client *fakeClient) GetTotalSales(
	_ context.Context,
	request *dashboardv1.GetTotalSalesRequest,
	_ ...grpc.CallOption,
) (*dashboardv1.GetTotalSalesResponse, error) {
	client.calls++
	client.request = request
	return client.response, client.err
}

func newTestHandler(
	t *testing.T,
	client dashboardv1.DashboardServiceClient,
	limit int,
) http.Handler {
	t.Helper()
	observer, err := telemetry.NewRequestObserver(
		slog.New(slog.NewJSONHandler(io.Discard, nil)), metricnoop.NewMeterProvider(),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(client, Settings{
		RequestTimeout: time.Second,
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
