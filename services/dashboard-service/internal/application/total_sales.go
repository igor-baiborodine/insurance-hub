// Package application owns Dashboard use cases and the storage ports they consume.
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

var (
	ErrTotalSalesReaderRequired = errors.New("total-sales reader is required")
	ErrDuplicateProduct         = errors.New("duplicate total-sales product")
)

// TotalSalesQuery is the transport-independent read request.
type TotalSalesQuery struct {
	Product  domain.ProductSelection
	DateFrom domain.Date
	DateTo   domain.Date
}

// TotalSalesReader returns only the selected Elasticsearch product buckets.
type TotalSalesReader interface {
	ReadTotalSales(context.Context, TotalSalesQuery) ([]domain.ProductSales, error)
}

// TotalSalesResult contains the checked total and returned per-product values.
type TotalSalesResult struct {
	Total           domain.SalesValue
	PerProductTotal map[string]domain.SalesValue
}

// GetTotalSales coordinates the read and derives totals from returned buckets only.
type GetTotalSales struct{ reader TotalSalesReader }

func NewGetTotalSales(reader TotalSalesReader) (GetTotalSales, error) {
	if reader == nil {
		return GetTotalSales{}, ErrTotalSalesReaderRequired
	}
	return GetTotalSales{reader: reader}, nil
}

func (useCase GetTotalSales) Execute(
	ctx context.Context,
	query TotalSalesQuery,
) (TotalSalesResult, error) {
	if err := ctx.Err(); err != nil {
		return TotalSalesResult{}, err
	}
	buckets, err := useCase.reader.ReadTotalSales(ctx, query)
	if err != nil {
		return TotalSalesResult{}, fmt.Errorf("get total sales: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return TotalSalesResult{}, err
	}

	total, err := domain.NewSalesValue(0, domain.NewPremiumAmount(0))
	if err != nil {
		return TotalSalesResult{}, err
	}
	perProduct := make(map[string]domain.SalesValue, len(buckets))
	for _, bucket := range buckets {
		if _, duplicate := perProduct[bucket.ProductCode]; duplicate {
			return TotalSalesResult{}, ErrDuplicateProduct
		}
		perProduct[bucket.ProductCode] = bucket.Value
		total, err = total.Add(bucket.Value)
		if err != nil {
			return TotalSalesResult{}, fmt.Errorf("get total sales: %w", err)
		}
	}
	return TotalSalesResult{Total: total, PerProductTotal: perProduct}, nil
}
