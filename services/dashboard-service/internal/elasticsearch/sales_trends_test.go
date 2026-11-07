package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

func TestTrendReader_UsesExactCalendarQueryAndPreservesGapOrder(t *testing.T) {
	// given
	from, _ := domain.ParseDate("2020-01-01")
	to, _ := domain.ParseDate("2020-05-01")
	query := application.SalesTrendsQuery{
		Product:         domain.ExactProduct(""),
		DateFrom:        from,
		DateTo:          to,
		AggregationUnit: domain.AggregationUnitMonth,
	}
	responseBody := &trackingBody{Reader: strings.NewReader(`{
		"aggregations":{"agg_filter":{"sales":{"buckets":[
			{"key":1577836800000,"key_as_string":"2020-01-01T00:00:00.000Z",
			 "doc_count":1,"total_premium":{"value":1.005}},
			{"key":1583020800000,"key_as_string":"2020-03-01T00:00:00.000Z",
			 "doc_count":0,"total_premium":{"value":0.0}}
		]}}}}`)}
	var gotIndex string
	var gotRequest map[string]any
	reader := &TrendReader{
		index:    PolicyIndex,
		location: time.UTC,
		search: func(
			_ context.Context,
			index string,
			body io.Reader,
		) (*esapi.Response, error) {
			gotIndex = index
			if err := json.NewDecoder(body).Decode(&gotRequest); err != nil {
				t.Fatal(err)
			}
			return &esapi.Response{StatusCode: 200, Body: responseBody}, nil
		},
	}

	// when
	buckets, err := reader.ReadSalesTrends(context.Background(), query)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if gotIndex != PolicyIndex || !responseBody.closed {
		t.Fatalf("index/closed = (%q, %t)", gotIndex, responseBody.closed)
	}
	if len(buckets) != 2 || buckets[0].PeriodDate.String() != "2020-01-01" ||
		buckets[0].Period != "2020-01-01T00:00:00.000Z" ||
		buckets[0].Value.PoliciesCount() != 1 ||
		buckets[0].Value.PremiumAmount().String() != "1.01" ||
		buckets[1].PeriodDate.String() != "2020-03-01" ||
		buckets[1].Value.PoliciesCount() != 0 ||
		buckets[1].Value.PremiumAmount().String() != "0.00" {
		t.Fatalf("buckets = %#v", buckets)
	}
	wantRequest := map[string]any{
		"size": float64(0),
		"aggs": map[string]any{"agg_filter": map[string]any{
			"filter": map[string]any{"bool": map[string]any{"must": []any{
				map[string]any{"term": map[string]any{"productCode.keyword": ""}},
				map[string]any{"range": map[string]any{"from": map[string]any{
					"gte": "2020-01-01", "lt": "2020-05-01",
				}}},
			}}},
			"aggs": map[string]any{"sales": map[string]any{
				"date_histogram": map[string]any{
					"field": "from", "calendar_interval": "month",
				},
				"aggs": map[string]any{"total_premium": map[string]any{
					"sum": map[string]any{"field": "totalPremium"},
				}},
			}},
		}},
	}
	if !reflect.DeepEqual(gotRequest, wantRequest) {
		t.Fatalf("request = %#v, want %#v", gotRequest, wantRequest)
	}
}

func TestDecodeSalesTrendsResponse_UsesConfiguredZoneAndRejectsUnsafeValues(t *testing.T) {
	// given
	toronto, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"aggregations":{"agg_filter":{"sales":{"buckets":[` +
		`{"key":1710028800000,"key_as_string":"independent-label","doc_count":9223372036854775807,` +
		`"total_premium":{"value":1000.0}}]}}}}`

	// when
	buckets, err := decodeSalesTrendsResponse([]byte(valid), toronto)

	// then
	if err != nil || len(buckets) != 1 || buckets[0].PeriodDate.String() != "2024-03-09" ||
		buckets[0].Period != "independent-label" ||
		buckets[0].Value.PoliciesCount() != int64(1<<63-1) ||
		buckets[0].Value.PremiumAmount().String() != "1000.00" {
		t.Fatalf("valid decode = (%#v, %v)", buckets, err)
	}
	invalid := []string{
		`{}`,
		valid + `{}`,
		`{"aggregations":{"agg_filter":{"sales":{"buckets":[` +
			`{"key":0,"key_as_string":"label","doc_count":-1,` +
			`"total_premium":{"value":1.0}}]}}}}`,
		`{"aggregations":{"agg_filter":{"sales":{"buckets":[` +
			`{"key":0,"key_as_string":"label","doc_count":1,` +
			`"total_premium":{"value":"Infinity"}}]}}}}`,
	}
	for _, data := range invalid {
		// when
		_, err := decodeSalesTrendsResponse([]byte(data), time.UTC)

		// then
		if !errors.Is(
			err,
			ErrInvalidResponse,
		) {
			t.Fatalf("invalid response %q = %v", data, err)
		}
	}
	bucket := `{"key":0,"key_as_string":"label","doc_count":0,` +
		`"total_premium":{"value":0.0}}`
	many := `{"aggregations":{"agg_filter":{"sales":{"buckets":[` +
		strings.Repeat(bucket+",", maximumTrendBuckets) + bucket + `]}}}}`

	// when
	_, err = decodeSalesTrendsResponse([]byte(many), time.UTC)

	// then
	if !errors.Is(
		err,
		ErrInvalidResponse,
	) {
		t.Fatalf("excessive buckets = %v", err)
	}
}

func TestTrendReader_MapsCancellationAvailabilityAndRejectedResponses(t *testing.T) {
	// given
	from, _ := domain.ParseDate("2020-01-01")
	to, _ := domain.ParseDate("2021-01-01")
	query := application.SalesTrendsQuery{
		DateFrom: from, DateTo: to, AggregationUnit: domain.AggregationUnitYear,
	}
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
			name: "transport unavailable", ctx: context.Background,
			searchErr: errors.New("private transport"),
			wantErr:   application.ErrSalesTrendsUnavailable, wantCalls: 1,
		},
		{
			name: "authentication rejected", ctx: context.Background,
			response: &esapi.Response{
				StatusCode: 401, Body: io.NopCloser(strings.NewReader("private")),
			},
			wantErr: ErrSearchRejected, wantCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			calls := 0
			reader := &TrendReader{
				index:    PolicyIndex,
				location: time.UTC,
				search: func(
					context.Context,
					string,
					io.Reader,
				) (*esapi.Response, error) {
					calls++
					return test.response, test.searchErr
				},
			}

			// when
			_, err := reader.ReadSalesTrends(test.ctx(), query)

			// then
			if !errors.Is(err, test.wantErr) || calls != test.wantCalls {
				t.Fatalf("ReadSalesTrends() = (%v, calls=%d)", err, calls)
			}
		})
	}
}

func TestBuildSalesTrendsRequest_RejectsUnknownUnit(t *testing.T) {
	// given
	query := application.SalesTrendsQuery{
		AggregationUnit: domain.AggregationUnit("month"),
	}

	// when
	_, err := buildSalesTrendsRequest(query)

	// then
	if !errors.Is(err, domain.ErrInvalidAggregationUnit) {
		t.Fatalf("unknown unit = %v", err)
	}
}
