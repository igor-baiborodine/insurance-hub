package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

var (
	ErrSalesTrendsReaderRequired = errors.New("sales-trends reader is required")
	ErrSalesTrendsUnavailable    = errors.New("sales-trends dependency unavailable")
)

// SalesTrendsQuery is the transport-independent calendar aggregation request.
type SalesTrendsQuery struct {
	Product         domain.ProductSelection
	DateFrom        domain.Date
	DateTo          domain.Date
	AggregationUnit domain.AggregationUnit
}

// SalesTrendsReader returns ordered Elasticsearch calendar buckets.
type SalesTrendsReader interface {
	ReadSalesTrends(context.Context, SalesTrendsQuery) ([]domain.PeriodSales, error)
}

type SalesTrendsResult struct {
	PeriodSales []domain.PeriodSales
}

type GetSalesTrends struct{ reader SalesTrendsReader }

func NewGetSalesTrends(reader SalesTrendsReader) (GetSalesTrends, error) {
	if reader == nil {
		return GetSalesTrends{}, ErrSalesTrendsReaderRequired
	}
	return GetSalesTrends{reader: reader}, nil
}

func (useCase GetSalesTrends) Execute(
	ctx context.Context,
	query SalesTrendsQuery,
) (SalesTrendsResult, error) {
	if err := ctx.Err(); err != nil {
		return SalesTrendsResult{}, err
	}
	periods, err := useCase.reader.ReadSalesTrends(ctx, query)
	if err != nil {
		return SalesTrendsResult{}, fmt.Errorf("get sales trends: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return SalesTrendsResult{}, err
	}
	return SalesTrendsResult{PeriodSales: periods}, nil
}
