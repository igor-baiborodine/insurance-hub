package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

type stubReader struct {
	buckets []domain.ProductSales
	err     error
	query   application.TotalSalesQuery
}

func (reader *stubReader) ReadTotalSales(
	_ context.Context,
	query application.TotalSalesQuery,
) ([]domain.ProductSales, error) {
	reader.query = query
	return append([]domain.ProductSales(nil), reader.buckets...), reader.err
}

func TestGetTotalSales_SumsReturnedBucketsAfterAdapterConversion(t *testing.T) {
	// given
	first, err := domain.NewSalesValue(2, domain.NewPremiumAmount(101))
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.NewSalesValue(3, domain.NewPremiumAmount(602))
	if err != nil {
		t.Fatal(err)
	}
	reader := &stubReader{buckets: []domain.ProductSales{
		{ProductCode: "A", Value: first},
		{ProductCode: "B", Value: second},
	}}
	useCase, err := application.NewGetTotalSales(reader)
	if err != nil {
		t.Fatal(err)
	}
	from, _ := domain.ParseDate("2026-01-01")
	to, _ := domain.ParseDate("2027-01-01")
	query := application.TotalSalesQuery{
		Product: domain.ExactProduct(""), DateFrom: from, DateTo: to,
	}

	// when
	got, err := useCase.Execute(context.Background(), query)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if got.Total.PoliciesCount() != 5 || got.Total.PremiumAmount().MinorUnits() != 703 {
		t.Fatalf(
			"total = (%d, %d), want (5, 703)",
			got.Total.PoliciesCount(),
			got.Total.PremiumAmount().MinorUnits(),
		)
	}
	if len(got.PerProductTotal) != 2 || reader.query.Product != query.Product {
		t.Fatalf("result/query mismatch: result=%#v query=%#v", got, reader.query)
	}
}

func TestGetTotalSales_PreservesEmptyResult(t *testing.T) {
	// given
	useCase, err := application.NewGetTotalSales(&stubReader{})
	if err != nil {
		t.Fatal(err)
	}

	// when
	got, err := useCase.Execute(context.Background(), application.TotalSalesQuery{})
	// then
	if err != nil {
		t.Fatal(err)
	}
	if got.Total.PoliciesCount() != 0 || got.Total.PremiumAmount().MinorUnits() != 0 ||
		len(got.PerProductTotal) != 0 {
		t.Fatalf("empty result = %#v", got)
	}
}

func TestGetTotalSales_RejectsDuplicateProductsAndPropagatesCancellation(t *testing.T) {
	// given
	value, err := domain.NewSalesValue(1, domain.NewPremiumAmount(100))
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := application.NewGetTotalSales(&stubReader{buckets: []domain.ProductSales{
		{ProductCode: "A", Value: value},
		{ProductCode: "A", Value: value},
	}})
	if err != nil {
		t.Fatal(err)
	}

	// when
	_, duplicateErr := useCase.Execute(context.Background(), application.TotalSalesQuery{})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, cancellationErr := useCase.Execute(canceled, application.TotalSalesQuery{})

	// then
	if !errors.Is(duplicateErr, application.ErrDuplicateProduct) {
		t.Fatalf("duplicate error = %v", duplicateErr)
	}
	if !errors.Is(cancellationErr, context.Canceled) {
		t.Fatalf("cancellation error = %v", cancellationErr)
	}
}
