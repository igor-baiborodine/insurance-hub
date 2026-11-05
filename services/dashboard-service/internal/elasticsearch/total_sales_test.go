package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

type trackingBody struct {
	io.Reader
	closed bool
}

func TestReader_BoundsResponseBodyAndPreservesShorterCallerDeadline(t *testing.T) {
	tests := []struct {
		name           string
		clientTimeout  time.Duration
		callerTimeout  time.Duration
		maximumElapsed time.Duration
	}{
		{
			name:           "client timeout bounds body",
			clientTimeout:  50 * time.Millisecond,
			maximumElapsed: time.Second,
		},
		{
			name:           "shorter caller deadline wins",
			clientTimeout:  time.Second,
			callerTimeout:  40 * time.Millisecond,
			maximumElapsed: 500 * time.Millisecond,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(
					func(writer http.ResponseWriter, request *http.Request) {
						writer.Header().
							Set("X-Elastic-Product", "Elasticsearch")
						writer.WriteHeader(http.StatusOK)
						writer.(http.Flusher).Flush()
						<-request.Context().Done()
					},
				),
			)
			defer server.Close()
			client, err := New(Config{
				Endpoint: server.URL,
				Index:    PolicyIndex,
				Timeout:  test.clientTimeout,
			})
			if err != nil {
				t.Fatal(err)
			}
			reader, err := NewReader(client)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if test.callerTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, test.callerTimeout)
				defer cancel()
			}

			started := time.Now()
			_, err = reader.ReadTotalSales(ctx, application.TotalSalesQuery{})
			if !errors.Is(err, context.DeadlineExceeded) ||
				time.Since(started) > test.maximumElapsed {
				t.Fatalf("ReadTotalSales() = %v after %s", err, time.Since(started))
			}
		})
	}
}

func (body *trackingBody) Close() error {
	body.closed = true
	return nil
}

func TestReader_UsesExactFilterAggregationAndClosesResponse(t *testing.T) {
	// given
	from, _ := domain.ParseDate("2019-01-01")
	to, _ := domain.ParseDate("2019-12-31")
	query := application.TotalSalesQuery{
		Product: domain.ExactProduct(""), DateFrom: from, DateTo: to,
	}
	responseBody := &trackingBody{Reader: strings.NewReader(`{
		"aggregations":{"agg_filter":{"count_by_product":{"buckets":[
			{"key":"","doc_count":3,"total_premium":{"value":6.015}}
		]}}}}`)}
	var gotIndex string
	var gotRequest map[string]any
	reader := &Reader{search: func(
		_ context.Context,
		index string,
		body io.Reader,
	) (*esapi.Response, error) {
		gotIndex = index
		if err := json.NewDecoder(body).Decode(&gotRequest); err != nil {
			t.Fatal(err)
		}
		return &esapi.Response{StatusCode: 200, Body: responseBody}, nil
	}}

	// when
	buckets, err := reader.ReadTotalSales(context.Background(), query)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if gotIndex != PolicyIndex || !responseBody.closed {
		t.Fatalf("index/closed = (%q, %t)", gotIndex, responseBody.closed)
	}
	if len(buckets) != 1 || buckets[0].ProductCode != "" ||
		buckets[0].Value.PoliciesCount() != 3 ||
		buckets[0].Value.PremiumAmount().MinorUnits() != 602 {
		t.Fatalf("buckets = %#v", buckets)
	}
	wantRequest := map[string]any{
		"size": float64(0),
		"aggs": map[string]any{
			"agg_filter": map[string]any{
				"filter": map[string]any{"bool": map[string]any{"must": []any{
					map[string]any{
						"term": map[string]any{"productCode.keyword": ""},
					},
					map[string]any{
						"range": map[string]any{"from": map[string]any{
							"gte": "2019-01-01", "lt": "2019-12-31",
						}},
					},
				}}},
				"aggs": map[string]any{
					"count_by_product": map[string]any{
						"terms": map[string]any{
							"field": "productCode.keyword",
							"size":  float64(10),
							"order": []any{
								map[string]any{"_count": "desc"},
								map[string]any{"_key": "asc"},
							},
						},
						"aggs": map[string]any{
							"total_premium": map[string]any{
								"sum": map[string]any{
									"field": "totalPremium",
								},
							},
						},
					},
				},
			},
		},
	}
	if !reflect.DeepEqual(gotRequest, wantRequest) {
		t.Fatalf("request = %#v, want %#v", gotRequest, wantRequest)
	}
}

func TestPremiumAmount_UsesDecimalHalfUpAndRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name      string
		value     float64
		wantMinor int64
		wantErr   bool
	}{
		{name: "positive half", value: 1.005, wantMinor: 101},
		{name: "negative half", value: -1.005, wantMinor: -101},
		{name: "ordinary", value: 6.015, wantMinor: 602},
		{name: "positive overflow", value: math.MaxFloat64, wantErr: true},
		{name: "not a number", value: math.NaN(), wantErr: true},
		{name: "infinity", value: math.Inf(1), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			amount, err := premiumAmount(test.value)

			// then
			if (err != nil) != test.wantErr || amount.MinorUnits() != test.wantMinor {
				t.Fatalf(
					"premiumAmount(%v) = (%d, %v)",
					test.value,
					amount.MinorUnits(),
					err,
				)
			}
		})
	}
}

func TestReader_MapsCancellationAvailabilityAndUnsafeResponses(t *testing.T) {
	// given
	tests := []struct {
		name      string
		ctx       func() context.Context
		response  *esapi.Response
		searchErr error
		wantErr   error
		wantCalls int
	}{
		{
			name: "canceled before search",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantErr: context.Canceled,
		},
		{
			name: "transport unavailable",
			ctx:  context.Background,
			searchErr: errors.New(
				"private transport",
			),
			wantErr:   ErrSearchUnavailable,
			wantCalls: 1,
		},
		{
			name: "authentication rejected",
			ctx:  context.Background,
			response: &esapi.Response{
				StatusCode: 401,
				Body:       io.NopCloser(strings.NewReader("private")),
			},
			wantErr:   ErrSearchRejected,
			wantCalls: 1,
		},
		{
			name: "malformed response",
			ctx:  context.Background,
			response: &esapi.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("{}")),
			},
			wantErr:   ErrInvalidResponse,
			wantCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			reader := &Reader{search: func(
				context.Context,
				string,
				io.Reader,
			) (*esapi.Response, error) {
				calls++
				return test.response, test.searchErr
			}}

			// when
			_, err := reader.ReadTotalSales(test.ctx(), application.TotalSalesQuery{})

			// then
			if !errors.Is(err, test.wantErr) || calls != test.wantCalls {
				t.Fatalf("ReadTotalSales() = (%v, calls=%d)", err, calls)
			}
		})
	}
}

func TestReader_MapsUntrustedTLSAsRejectedDependencyFailure(t *testing.T) {
	// given
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-Elastic-Product", "Elasticsearch")
			_, _ = writer.Write(
				[]byte(
					`{"aggregations":{"agg_filter":{"count_by_product":{"buckets":[]}}}}`,
				),
			)
		}),
	)
	defer server.Close()
	client, err := New(Config{
		Endpoint: server.URL,
		Index:    PolicyIndex,
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	reader, err := NewReader(client)
	if err != nil {
		t.Fatal(err)
	}
	from, _ := domain.ParseDate("2019-01-01")
	to, _ := domain.ParseDate("2020-01-01")

	// when
	_, err = reader.ReadTotalSales(context.Background(), application.TotalSalesQuery{
		Product:  domain.AllProducts(),
		DateFrom: from,
		DateTo:   to,
	})

	// then
	if !errors.Is(err, ErrSearchRejected) ||
		errors.Is(err, application.ErrTotalSalesUnavailable) {
		t.Fatalf("ReadTotalSales() error = %v, want rejected non-unavailable failure", err)
	}
}
