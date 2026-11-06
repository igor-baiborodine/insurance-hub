package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

const maximumTrendBuckets = 10_000

var ErrTrendTimeZoneRequired = errors.New("sales-trends time zone is required")

// TrendReader implements the application trend port with the shared read-only client.
type TrendReader struct {
	search   searchFunc
	timeout  time.Duration
	location *time.Location
}

var _ application.SalesTrendsReader = (*TrendReader)(nil)

func NewTrendReader(client *Client, location *time.Location) (*TrendReader, error) {
	if client == nil || client.Client == nil {
		return nil, ErrClientRequired
	}
	if location == nil {
		return nil, ErrTrendTimeZoneRequired
	}
	return &TrendReader{
		search: func(ctx context.Context, index string, body io.Reader) (*esapi.Response, error) {
			return client.Search(
				client.Search.WithContext(ctx),
				client.Search.WithIndex(index),
				client.Search.WithBody(body),
			)
		},
		timeout:  client.timeout,
		location: location,
	}, nil
}

func (reader *TrendReader) ReadSalesTrends(
	ctx context.Context,
	query application.SalesTrendsQuery,
) ([]domain.PeriodSales, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	operationCtx, cancel := withOperationTimeout(ctx, reader.timeout)
	defer cancel()
	body, err := buildSalesTrendsRequest(query)
	if err != nil {
		return nil, ErrInvalidResponse
	}
	response, err := reader.search(operationCtx, PolicyIndex, bytes.NewReader(body))
	if err != nil {
		if contextErr := operationCtx.Err(); contextErr != nil {
			return nil, contextErr
		}
		if isCertificateVerificationError(err) {
			return nil, ErrSearchRejected
		}
		return nil, errors.Join(application.ErrSalesTrendsUnavailable, ErrSearchUnavailable)
	}
	if response == nil || response.Body == nil {
		return nil, ErrInvalidResponse
	}
	defer func() { _ = response.Body.Close() }()
	if response.IsError() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, ErrSearchRejected
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if contextErr := operationCtx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil || len(data) > maximumResponseBytes {
		return nil, ErrInvalidResponse
	}
	return decodeSalesTrendsResponse(data, reader.location)
}

func buildSalesTrendsRequest(query application.SalesTrendsQuery) ([]byte, error) {
	unit, err := domain.ParseAggregationUnit(string(query.AggregationUnit))
	if err != nil {
		return nil, err
	}
	must := make([]map[string]any, 0, 2)
	if query.Product.Present() {
		must = append(must, map[string]any{
			"term": map[string]any{"productCode.keyword": query.Product.Value()},
		})
	}
	must = append(must, map[string]any{
		"range": map[string]any{"from": map[string]string{
			"gte": query.DateFrom.String(),
			"lt":  query.DateTo.String(),
		}},
	})
	request := map[string]any{
		"size": 0,
		"aggs": map[string]any{"agg_filter": map[string]any{
			"filter": map[string]any{"bool": map[string]any{"must": must}},
			"aggs": map[string]any{"sales": map[string]any{
				"date_histogram": map[string]any{
					"field":             "from",
					"calendar_interval": unit.CalendarInterval(),
				},
				"aggs": map[string]any{"total_premium": map[string]any{
					"sum": map[string]string{"field": "totalPremium"},
				}},
			}},
		}},
	}
	return json.Marshal(request)
}

type salesTrendsResponse struct {
	Aggregations *struct {
		Filter *struct {
			Sales *struct {
				Buckets []struct {
					Key           *int64  `json:"key"`
					KeyAsString   *string `json:"key_as_string"`
					DocumentCount *int64  `json:"doc_count"`
					Premium       *struct {
						Value *float64 `json:"value"`
					} `json:"total_premium"`
				} `json:"buckets"`
			} `json:"sales"`
		} `json:"agg_filter"`
	} `json:"aggregations"`
}

func decodeSalesTrendsResponse(
	data []byte,
	location *time.Location,
) ([]domain.PeriodSales, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var result salesTrendsResponse
	if err := decoder.Decode(&result); err != nil {
		return nil, ErrInvalidResponse
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidResponse
	}
	if result.Aggregations == nil || result.Aggregations.Filter == nil ||
		result.Aggregations.Filter.Sales == nil ||
		len(result.Aggregations.Filter.Sales.Buckets) > maximumTrendBuckets {
		return nil, ErrInvalidResponse
	}
	buckets := make([]domain.PeriodSales, len(result.Aggregations.Filter.Sales.Buckets))
	for index, bucket := range result.Aggregations.Filter.Sales.Buckets {
		if bucket.Key == nil || bucket.KeyAsString == nil || bucket.DocumentCount == nil ||
			bucket.Premium == nil || bucket.Premium.Value == nil {
			return nil, ErrInvalidResponse
		}
		date, err := domain.DateFromEpochMillis(*bucket.Key, location)
		if err != nil {
			return nil, ErrInvalidResponse
		}
		amount, err := domain.NewTrendPremiumAmountFromFloat(*bucket.Premium.Value)
		if err != nil {
			return nil, ErrInvalidResponse
		}
		value, err := domain.NewTrendSalesValue(*bucket.DocumentCount, amount)
		if err != nil {
			return nil, ErrInvalidResponse
		}
		buckets[index] = domain.PeriodSales{
			PeriodDate: date,
			Period:     *bucket.KeyAsString,
			Value:      value,
		}
	}
	return buckets, nil
}
