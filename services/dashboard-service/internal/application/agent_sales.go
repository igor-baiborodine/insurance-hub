package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

var (
	ErrAgentSalesReaderRequired = errors.New("agent-sales reader is required")
	ErrAgentSalesUnavailable    = errors.New("agent-sales dependency unavailable")
	ErrDuplicateAgent           = errors.New("duplicate agent-sales agent")
)

// AgentSalesQuery is the transport-independent agent aggregation request.
type AgentSalesQuery struct {
	Agent    domain.AgentSelection
	Product  domain.ProductSelection
	DateFrom domain.Date
	DateTo   domain.Date
}

// AgentSalesReader returns only the selected Elasticsearch agent buckets.
type AgentSalesReader interface {
	ReadAgentSales(context.Context, AgentSalesQuery) ([]domain.AgentSales, error)
}

// AgentSalesResult contains the returned per-agent values without a synthetic total.
type AgentSalesResult struct {
	PerAgentTotal map[string]domain.AgentSalesValue
}

type GetAgentSales struct{ reader AgentSalesReader }

func NewGetAgentSales(reader AgentSalesReader) (GetAgentSales, error) {
	if reader == nil {
		return GetAgentSales{}, ErrAgentSalesReaderRequired
	}
	return GetAgentSales{reader: reader}, nil
}

func (useCase GetAgentSales) Execute(
	ctx context.Context,
	query AgentSalesQuery,
) (AgentSalesResult, error) {
	if err := ctx.Err(); err != nil {
		return AgentSalesResult{}, err
	}
	buckets, err := useCase.reader.ReadAgentSales(ctx, query)
	if err != nil {
		return AgentSalesResult{}, fmt.Errorf("get agent sales: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return AgentSalesResult{}, err
	}
	perAgent := make(map[string]domain.AgentSalesValue, len(buckets))
	for _, bucket := range buckets {
		if _, duplicate := perAgent[bucket.AgentLogin]; duplicate {
			return AgentSalesResult{}, ErrDuplicateAgent
		}
		perAgent[bucket.AgentLogin] = bucket.Value
	}
	return AgentSalesResult{PerAgentTotal: perAgent}, nil
}
