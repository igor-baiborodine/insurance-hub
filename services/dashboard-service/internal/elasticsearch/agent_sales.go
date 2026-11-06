package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

var _ application.AgentSalesReader = (*Reader)(nil)

func (reader *Reader) ReadAgentSales(
	ctx context.Context,
	query application.AgentSalesQuery,
) ([]domain.AgentSales, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	operationCtx, cancel := withOperationTimeout(ctx, reader.timeout)
	defer cancel()
	body, err := buildAgentSalesRequest(query)
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
		return nil, errors.Join(application.ErrAgentSalesUnavailable, ErrSearchUnavailable)
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
	return decodeAgentSalesResponse(data)
}

func buildAgentSalesRequest(query application.AgentSalesQuery) ([]byte, error) {
	must := make([]map[string]any, 0, 3)
	if query.Agent.Present() {
		must = append(must, map[string]any{
			"term": map[string]any{"agentLogin.keyword": query.Agent.Value()},
		})
	}
	if query.Product.Present() {
		must = append(must, map[string]any{
			"term": map[string]any{"productCode.keyword": query.Product.Value()},
		})
	}
	must = append(must, map[string]any{
		"range": map[string]any{"from": map[string]string{
			"gte": query.DateFrom.String(), "lt": query.DateTo.String(),
		}},
	})
	request := map[string]any{
		"size": 0,
		"aggs": map[string]any{"agg_filter": map[string]any{
			"filter": map[string]any{"bool": map[string]any{"must": must}},
			"aggs": map[string]any{"count_by_agent": map[string]any{
				"terms": map[string]any{
					"field": "agentLogin.keyword",
					"size":  returnedBucketLimit,
					"order": []map[string]string{
						{"_count": "desc"},
						{"_key": "asc"},
					},
				},
				"aggs": map[string]any{"total_premium": map[string]any{
					"sum": map[string]string{"field": "totalPremium"},
				}},
			}},
		}},
	}
	return json.Marshal(request)
}

type agentSalesResponse struct {
	Aggregations *struct {
		Filter *struct {
			Agents *struct {
				Buckets []struct {
					Key           *string `json:"key"`
					DocumentCount *int64  `json:"doc_count"`
					Premium       *struct {
						Value *float64 `json:"value"`
					} `json:"total_premium"`
				} `json:"buckets"`
			} `json:"count_by_agent"`
		} `json:"agg_filter"`
	} `json:"aggregations"`
}

func decodeAgentSalesResponse(data []byte) ([]domain.AgentSales, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var result agentSalesResponse
	if err := decoder.Decode(&result); err != nil {
		return nil, ErrInvalidResponse
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidResponse
	}
	if result.Aggregations == nil || result.Aggregations.Filter == nil ||
		result.Aggregations.Filter.Agents == nil ||
		len(result.Aggregations.Filter.Agents.Buckets) > returnedBucketLimit {
		return nil, ErrInvalidResponse
	}
	buckets := make([]domain.AgentSales, len(result.Aggregations.Filter.Agents.Buckets))
	for index, bucket := range result.Aggregations.Filter.Agents.Buckets {
		if bucket.Key == nil || bucket.DocumentCount == nil || bucket.Premium == nil ||
			bucket.Premium.Value == nil {
			return nil, ErrInvalidResponse
		}
		amount, err := agentPremiumAmount(*bucket.Premium.Value)
		if err != nil {
			return nil, ErrInvalidResponse
		}
		value, err := domain.NewAgentSalesValue(*bucket.DocumentCount, amount)
		if err != nil {
			return nil, ErrInvalidResponse
		}
		buckets[index] = domain.AgentSales{AgentLogin: *bucket.Key, Value: value}
	}
	return buckets, nil
}

func agentPremiumAmount(value float64) (domain.AgentPremiumAmount, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return domain.AgentPremiumAmount{}, ErrInvalidResponse
	}
	if value == 0 {
		return domain.NewAgentPremiumAmount("0.0")
	}
	negative := math.Signbit(value)
	absolute := math.Abs(value)
	raw := strconv.FormatFloat(absolute, 'e', -1, 64)
	if absolute == math.SmallestNonzeroFloat64 {
		raw = "4.9e-324"
	}
	mantissa, exponentText, present := strings.Cut(raw, "e")
	if !present {
		return domain.AgentPremiumAmount{}, ErrInvalidResponse
	}
	exponent, err := strconv.Atoi(exponentText)
	if err != nil {
		return domain.AgentPremiumAmount{}, ErrInvalidResponse
	}
	whole, fraction, hasPoint := strings.Cut(mantissa, ".")
	digits := whole + fraction
	var scale int
	if exponent >= -3 && exponent < 7 {
		position := exponent + 1
		switch {
		case position <= 0:
			scale = len(digits) - position
		case position >= len(digits):
			digits += strings.Repeat("0", position-len(digits)) + "0"
			scale = 1
		default:
			scale = len(digits) - position
		}
	} else {
		if !hasPoint {
			digits += "0"
		}
		scale = len(digits) - 1 - exponent
	}
	coefficient, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return domain.AgentPremiumAmount{}, ErrInvalidResponse
	}
	if negative {
		coefficient = -coefficient
	}
	return domain.NewAgentPremiumAmountFromCoefficientScale(coefficient, scale)
}
