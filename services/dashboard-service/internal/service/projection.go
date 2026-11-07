package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/config"
	dashboardelasticsearch "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/elasticsearch"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/telemetry"
)

type projectionRuntime interface {
	ProjectPolicy(context.Context, application.PolicyEvent) error
	Check(context.Context) error
	CloseIdleConnections()
}

type productionProjection struct {
	client   *dashboardelasticsearch.Client
	useCase  application.ProjectPolicy
	observer *telemetry.DependencyObserver
}

func newProductionProjection(
	settings config.Elasticsearch,
	observer *telemetry.DependencyObserver,
) (projectionRuntime, error) {
	if observer == nil {
		return nil, errors.New(
			"initialize policy projection: dependency observer is required",
		)
	}
	var caCertificate []byte
	if settings.CAFile != "" {
		var err error
		caCertificate, err = readBoundedFile(settings.CAFile, 1<<20)
		if err != nil {
			return nil, errors.New(
				"initialize policy projection: load Elasticsearch CA certificate",
			)
		}
	}
	client, err := dashboardelasticsearch.New(dashboardelasticsearch.Config{
		Endpoint: settings.URL, Index: settings.Index,
		Username: settings.Username, Password: settings.Password,
		CACert: caCertificate, Timeout: settings.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize policy projection: %w", err)
	}
	writer, err := dashboardelasticsearch.NewWriter(client)
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
	return &productionProjection{client: client, useCase: useCase, observer: observer}, nil
}

func (projection *productionProjection) ProjectPolicy(
	ctx context.Context,
	event application.PolicyEvent,
) error {
	dependencyCtx, observation := projection.observer.StartElasticsearch(
		ctx,
		telemetry.OperationReplace,
	)
	err := projection.useCase.Execute(dependencyCtx, event)
	observation.Finish(dependencyCtx, err)
	return err
}

func (projection *productionProjection) Check(ctx context.Context) error {
	return projection.client.CheckWrite(ctx)
}

func (projection *productionProjection) CloseIdleConnections() {
	projection.client.CloseIdleConnections()
}

var _ projectionRuntime = (*productionProjection)(nil)
