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
	dependencyCtx, observation := reader.observer.StartElasticsearch(ctx)
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
	dependencyCtx, observation := reader.observer.StartElasticsearch(ctx)
	result, err := reader.reader.ReadAgentSales(dependencyCtx, query)
	observation.Finish(dependencyCtx, err)
	return result, err
}
