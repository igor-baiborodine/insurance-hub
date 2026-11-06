package application

import (
	"context"
	"errors"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

type salesTrendsReaderFunc func(
	context.Context,
	SalesTrendsQuery,
) ([]domain.PeriodSales, error)

func (reader salesTrendsReaderFunc) ReadSalesTrends(
	ctx context.Context,
	query SalesTrendsQuery,
) ([]domain.PeriodSales, error) {
	return reader(ctx, query)
}

func TestGetSalesTrends_PreservesReaderOrderAndValues(t *testing.T) {
	// given
	date, err := domain.ParseDate("2020-03-01")
	if err != nil {
		t.Fatal(err)
	}
	amount, err := domain.NewTrendPremiumAmount("0.00")
	if err != nil {
		t.Fatal(err)
	}
	value, err := domain.NewTrendSalesValue(0, amount)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PeriodSales{{
		PeriodDate: date,
		Period:     "2020-03-01T00:00:00.000Z",
		Value:      value,
	}}
	useCase, err := NewGetSalesTrends(salesTrendsReaderFunc(func(
		_ context.Context,
		_ SalesTrendsQuery,
	) ([]domain.PeriodSales, error) {
		return want, nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	// when
	result, err := useCase.Execute(context.Background(), SalesTrendsQuery{})

	// then
	if err != nil || len(result.PeriodSales) != 1 ||
		result.PeriodSales[0].Period != want[0].Period {
		t.Fatalf("Execute() = (%#v, %v)", result, err)
	}
}

func TestGetSalesTrends_PropagatesCancellationAndWrapsDependencyFailure(t *testing.T) {
	dependencyErr := errors.New("dependency failed")
	useCase, err := NewGetSalesTrends(salesTrendsReaderFunc(func(
		_ context.Context,
		_ SalesTrendsQuery,
	) ([]domain.PeriodSales, error) {
		return nil, dependencyErr
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := useCase.Execute(
		context.Background(),
		SalesTrendsQuery{},
	); !errors.Is(
		err,
		dependencyErr,
	) {
		t.Fatalf("dependency failure = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := useCase.Execute(ctx, SalesTrendsQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestNewGetSalesTrends_RequiresReader(t *testing.T) {
	if _, err := NewGetSalesTrends(nil); !errors.Is(err, ErrSalesTrendsReaderRequired) {
		t.Fatalf("NewGetSalesTrends(nil) = %v", err)
	}
}
