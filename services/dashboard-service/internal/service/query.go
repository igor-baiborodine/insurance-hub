package service

import (
	"context"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/telemetry"
)

type observedTotalSalesReader struct {
	reader   application.TotalSalesReader
	observer *telemetry.DependencyObserver
}

func newObservedTotalSalesReader(
	reader application.TotalSalesReader,
	observer *telemetry.DependencyObserver,
) application.TotalSalesReader {
	return &observedTotalSalesReader{reader: reader, observer: observer}
}

func (reader *observedTotalSalesReader) ReadTotalSales(
	ctx context.Context,
	query application.TotalSalesQuery,
) ([]domain.ProductSales, error) {
	dependencyCtx, observation := reader.observer.StartElasticsearch(
		ctx,
		telemetry.OperationTotalSales,
	)
	result, err := reader.reader.ReadTotalSales(dependencyCtx, query)
	observation.Finish(dependencyCtx, err)
	return result, err
}

type observedAgentSalesReader struct {
	reader   application.AgentSalesReader
	observer *telemetry.DependencyObserver
}

func newObservedAgentSalesReader(
	reader application.AgentSalesReader,
	observer *telemetry.DependencyObserver,
) application.AgentSalesReader {
	return &observedAgentSalesReader{reader: reader, observer: observer}
}

func (reader *observedAgentSalesReader) ReadAgentSales(
	ctx context.Context,
	query application.AgentSalesQuery,
) ([]domain.AgentSales, error) {
	dependencyCtx, observation := reader.observer.StartElasticsearch(
		ctx,
		telemetry.OperationAgentSales,
	)
	result, err := reader.reader.ReadAgentSales(dependencyCtx, query)
	observation.Finish(dependencyCtx, err)
	return result, err
}

type observedSalesTrendsReader struct {
	reader   application.SalesTrendsReader
	observer *telemetry.DependencyObserver
}

func newObservedSalesTrendsReader(
	reader application.SalesTrendsReader,
	observer *telemetry.DependencyObserver,
) application.SalesTrendsReader {
	return &observedSalesTrendsReader{reader: reader, observer: observer}
}

func (reader *observedSalesTrendsReader) ReadSalesTrends(
	ctx context.Context,
	query application.SalesTrendsQuery,
) ([]domain.PeriodSales, error) {
	dependencyCtx, observation := reader.observer.StartElasticsearch(
		ctx,
		telemetry.OperationSalesTrends,
	)
	result, err := reader.reader.ReadSalesTrends(dependencyCtx, query)
	observation.Finish(dependencyCtx, err)
	return result, err
}
