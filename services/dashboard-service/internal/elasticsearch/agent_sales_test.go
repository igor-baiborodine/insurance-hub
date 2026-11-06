package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/elastic/go-elasticsearch/v8/esapi"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

func TestReader_ReadAgentSales_UsesExactFiltersAndClosesResponse(t *testing.T) {
	// given
	from, _ := domain.ParseDate("2019-01-01")
	to, _ := domain.ParseDate("2019-12-31")
	query := application.AgentSalesQuery{
		Agent: domain.ExactAgent(""), Product: domain.ExactProduct(" FARM "),
		DateFrom: from, DateTo: to,
	}
	responseBody := &trackingBody{Reader: strings.NewReader(`{
		"aggregations":{"agg_filter":{"count_by_agent":{"buckets":[
			{"key":"","doc_count":3,"total_premium":{"value":1.005}}
		]}}}}`)}
	var gotRequest map[string]any
	reader := &Reader{
		search: func(_ context.Context, index string, body io.Reader) (*esapi.Response, error) {
			if index != PolicyIndex {
				t.Fatalf("index = %q", index)
			}
			if err := json.NewDecoder(body).Decode(&gotRequest); err != nil {
				t.Fatal(err)
			}
			return &esapi.Response{StatusCode: 200, Body: responseBody}, nil
		},
	}

	// when
	buckets, err := reader.ReadAgentSales(context.Background(), query)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if !responseBody.closed || len(buckets) != 1 || buckets[0].AgentLogin != "" ||
		buckets[0].Value.PoliciesCount() != 3 ||
		buckets[0].Value.PremiumAmount().String() != "1.005" {
		t.Fatalf("closed/buckets = (%t, %#v)", responseBody.closed, buckets)
	}
	wantRequest := map[string]any{
		"size": float64(0),
		"aggs": map[string]any{"agg_filter": map[string]any{
			"filter": map[string]any{"bool": map[string]any{"must": []any{
				map[string]any{"term": map[string]any{"agentLogin.keyword": ""}},
				map[string]any{
					"term": map[string]any{"productCode.keyword": " FARM "},
				},
				map[string]any{"range": map[string]any{"from": map[string]any{
					"gte": "2019-01-01", "lt": "2019-12-31",
				}}},
			}}},
			"aggs": map[string]any{"count_by_agent": map[string]any{
				"terms": map[string]any{
					"field": "agentLogin.keyword", "size": float64(10),
					"order": []any{
						map[string]any{
							"_count": "desc",
						},
						map[string]any{"_key": "asc"},
					},
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

func TestAgentPremiumAmount_PreservesJavaEquivalentTokens(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  string
	}{
		{name: "integer scale", value: 3000, want: "3000.0"},
		{name: "fraction", value: 1.005, want: "1.005"},
		{name: "negative fraction", value: -1.005, want: "-1.005"},
		{name: "binary residue", value: 0.30000000000000004, want: "0.30000000000000004"},
		{name: "small exponent", value: 0.0000001, want: "1.0E-7"},
		{name: "zero", value: math.Copysign(0, -1), want: "0.0"},
		{name: "minimum", value: math.SmallestNonzeroFloat64, want: "4.9E-324"},
		{name: "maximum", value: math.MaxFloat64, want: "1.7976931348623157E+308"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			amount, err := agentPremiumAmount(test.value)

			// then
			if err != nil || amount.String() != test.want {
				t.Fatalf(
					"agentPremiumAmount(%v) = (%q, %v), want %q",
					test.value,
					amount.String(),
					err,
					test.want,
				)
			}
		})
	}
}

func TestReader_ReadAgentSales_RejectsMalformedAndUnavailableResults(t *testing.T) {
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
			name:      "unavailable",
			ctx:       context.Background,
			searchErr: errors.New("private"),
			wantErr:   application.ErrAgentSalesUnavailable,
			wantCalls: 1,
		},
		{
			name:      "missing aggregation",
			ctx:       context.Background,
			response:  responseWithBody(`{}`),
			wantErr:   ErrInvalidResponse,
			wantCalls: 1,
		},
		{
			name:      "negative count",
			ctx:       context.Background,
			response:  responseWithBody(agentResponse("agent", -1, "1.0")),
			wantErr:   ErrInvalidResponse,
			wantCalls: 1,
		},
		{
			name:      "too many buckets",
			ctx:       context.Background,
			response:  responseWithBody(manyAgentBuckets()),
			wantErr:   ErrInvalidResponse,
			wantCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			reader := &Reader{
				search: func(context.Context, string, io.Reader) (*esapi.Response, error) {
					calls++
					return test.response, test.searchErr
				},
			}

			// when
			_, err := reader.ReadAgentSales(
				test.ctx(),
				application.AgentSalesQuery{},
			)

			// then
			if !errors.Is(err, test.wantErr) || calls != test.wantCalls {
				t.Fatalf(
					"ReadAgentSales() = (error=%v, calls=%d), want (%v, %d)",
					err,
					calls,
					test.wantErr,
					test.wantCalls,
				)
			}
		})
	}
}

func responseWithBody(body string) *esapi.Response {
	return &esapi.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}

func agentResponse(agent string, count int64, amount string) string {
	return `{"aggregations":{"agg_filter":{"count_by_agent":{"buckets":[{"key":` +
		strconvQuote(agent) + `,"doc_count":` + strconvFormatInt(count) +
		`,"total_premium":{"value":` + amount + `}}]}}}}`
}

func manyAgentBuckets() string {
	buckets := make([]string, returnedBucketLimit+1)
	for index := range buckets {
		buckets[index] = `{"key":"agent","doc_count":1,"total_premium":{"value":1.0}}`
	}
	return `{"aggregations":{"agg_filter":{"count_by_agent":{"buckets":[` +
		strings.Join(buckets, ",") + `]}}}}`
}

func strconvQuote(value string) string { data, _ := json.Marshal(value); return string(data) }

func strconvFormatInt(value int64) string { return strconv.FormatInt(value, 10) }
