package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
	policyelasticsearch "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/elasticsearch"
)

type projectionRuntime interface {
	ProjectPolicy(context.Context, application.PolicyEvent) error
	Check(context.Context) error
	CloseIdleConnections()
}

type productionProjection struct {
	client  *policyelasticsearch.Client
	useCase application.ProjectPolicy
}

// newProductionProjection creates a dedicated client for the writer authority supplied by
// worker composition. Query construction remains a separate client owner.
func newProductionProjection(
	settings config.Elasticsearch,
) (projectionRuntime, error) {
	client, err := newElasticsearchClient(settings)
	if err != nil {
		return nil, fmt.Errorf("initialize policy projection: %w", err)
	}
	writer, err := policyelasticsearch.NewWriter(client)
	if err != nil {
		client.CloseIdleConnections()
		return nil, errors.New(
			"initialize policy projection: configure Elasticsearch adapter",
		)
	}
	useCase, err := application.NewProjectPolicy(writer)
	if err != nil {
		client.CloseIdleConnections()
		return nil, errors.New(
			"initialize policy projection: configure application use case",
		)
	}
	return &productionProjection{client: client, useCase: useCase}, nil
}

func (projection *productionProjection) ProjectPolicy(
	ctx context.Context,
	event application.PolicyEvent,
) error {
	return projection.useCase.Execute(ctx, event)
}

func (projection *productionProjection) Check(ctx context.Context) error {
	return projection.client.Check(ctx)
}

func (projection *productionProjection) CloseIdleConnections() {
	projection.client.CloseIdleConnections()
}

var _ projectionRuntime = (*productionProjection)(nil)
