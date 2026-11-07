package elasticsearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"strconv"
	"time"

	"github.com/elastic/go-elasticsearch/v8/esapi"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

const (
	returnedBucketLimit  = 10
	maximumResponseBytes = 1 << 20
)

var (
	ErrClientRequired    = errors.New("elasticsearch client is required")
	ErrSearchUnavailable = errors.New("elasticsearch search unavailable")
	ErrSearchRejected    = errors.New("elasticsearch search rejected")
	ErrInvalidResponse   = errors.New("invalid elasticsearch search response")
)

type searchFunc func(context.Context, string, io.Reader) (*esapi.Response, error)

// Reader implements the application analytics ports against one exact Dashboard index profile.
type Reader struct {
	search  searchFunc
	index   string
	timeout time.Duration
}

var _ application.TotalSalesReader = (*Reader)(nil)

func NewReader(client *Client) (*Reader, error) {
	if client == nil || client.Client == nil {
		return nil, ErrClientRequired
	}
	return &Reader{
		search: func(ctx context.Context, index string, body io.Reader) (*esapi.Response, error) {
			return client.Search(
				client.Search.WithContext(ctx),
				client.Search.WithIndex(index),
				client.Search.WithBody(body),
			)
		},
		index:   client.index,
		timeout: client.timeout,
	}, nil
}

func (reader *Reader) ReadTotalSales(
	ctx context.Context,
	query application.TotalSalesQuery,
) ([]domain.ProductSales, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	operationCtx, cancel := withOperationTimeout(ctx, reader.timeout)
	defer cancel()
	body, err := buildTotalSalesRequest(query)
	if err != nil {
		return nil, ErrInvalidResponse
	}
	response, err := reader.search(operationCtx, reader.index, bytes.NewReader(body))
	if err != nil {
		if contextErr := operationCtx.Err(); contextErr != nil {
			return nil, contextErr
		}
		if isCertificateVerificationError(err) {
			return nil, ErrSearchRejected
		}
		return nil, errors.Join(application.ErrTotalSalesUnavailable, ErrSearchUnavailable)
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
	return decodeTotalSalesResponse(data)
}

func isCertificateVerificationError(err error) bool {
	var tlsError *tls.CertificateVerificationError
	if errors.As(err, &tlsError) {
		return true
	}
	var unknownAuthorityError x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthorityError) {
		return true
	}
	var hostnameError x509.HostnameError
	if errors.As(err, &hostnameError) {
		return true
	}
	var certificateInvalidError x509.CertificateInvalidError
	if errors.As(err, &certificateInvalidError) {
		return true
	}
	var systemRootsError x509.SystemRootsError
	return errors.As(err, &systemRootsError)
}

func buildTotalSalesRequest(query application.TotalSalesQuery) ([]byte, error) {
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
		"aggs": map[string]any{
			"agg_filter": map[string]any{
				"filter": map[string]any{"bool": map[string]any{"must": must}},
				"aggs": map[string]any{
					"count_by_product": map[string]any{
						"terms": map[string]any{
							"field": "productCode.keyword",
							"size":  returnedBucketLimit,
							"order": []map[string]string{
								{"_count": "desc"},
								{"_key": "asc"},
							},
						},
						"aggs": map[string]any{
							"total_premium": map[string]any{
								"sum": map[string]string{
									"field": "totalPremium",
								},
							},
						},
					},
				},
			},
		},
	}
	return json.Marshal(request)
}

type totalSalesResponse struct {
	Aggregations *struct {
		Filter *struct {
			Products *struct {
				Buckets []struct {
					Key           *string `json:"key"`
					DocumentCount *int64  `json:"doc_count"`
					Premium       *struct {
						Value *float64 `json:"value"`
					} `json:"total_premium"`
				} `json:"buckets"`
			} `json:"count_by_product"`
		} `json:"agg_filter"`
	} `json:"aggregations"`
}

func decodeTotalSalesResponse(data []byte) ([]domain.ProductSales, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var result totalSalesResponse
	if err := decoder.Decode(&result); err != nil {
		return nil, ErrInvalidResponse
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidResponse
	}
	if result.Aggregations == nil || result.Aggregations.Filter == nil ||
		result.Aggregations.Filter.Products == nil ||
		len(result.Aggregations.Filter.Products.Buckets) > returnedBucketLimit {
		return nil, ErrInvalidResponse
	}
	buckets := make([]domain.ProductSales, len(result.Aggregations.Filter.Products.Buckets))
	for index, bucket := range result.Aggregations.Filter.Products.Buckets {
		if bucket.Key == nil || bucket.DocumentCount == nil || bucket.Premium == nil ||
			bucket.Premium.Value == nil {
			return nil, ErrInvalidResponse
		}
		amount, err := premiumAmount(*bucket.Premium.Value)
		if err != nil {
			return nil, ErrInvalidResponse
		}
		value, err := domain.NewSalesValue(*bucket.DocumentCount, amount)
		if err != nil {
			return nil, ErrInvalidResponse
		}
		buckets[index] = domain.ProductSales{ProductCode: *bucket.Key, Value: value}
	}
	return buckets, nil
}

func premiumAmount(value float64) (domain.PremiumAmount, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return domain.PremiumAmount{}, ErrInvalidResponse
	}
	decimal := strconv.FormatFloat(value, 'g', -1, 64)
	rational, ok := new(big.Rat).SetString(decimal)
	if !ok {
		return domain.PremiumAmount{}, ErrInvalidResponse
	}
	scaled := new(big.Rat).Mul(rational, big.NewRat(100, 1))
	minor := new(big.Int)
	remainder := new(big.Int)
	minor.QuoRem(scaled.Num(), scaled.Denom(), remainder)
	twiceRemainder := new(big.Int).Lsh(new(big.Int).Abs(remainder), 1)
	if twiceRemainder.Cmp(scaled.Denom()) >= 0 {
		if scaled.Sign() < 0 {
			minor.Sub(minor, big.NewInt(1))
		} else {
			minor.Add(minor, big.NewInt(1))
		}
	}
	if !minor.IsInt64() {
		return domain.PremiumAmount{}, ErrInvalidResponse
	}
	return domain.NewPremiumAmount(minor.Int64()), nil
}
