package service

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	policyelasticsearch "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/elasticsearch"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

type queryRuntime interface {
	SearchPolicies(context.Context, string) ([]domain.Policy, error)
	Check(context.Context) error
	CloseIdleConnections()
}

type productionQuery struct {
	client   *policyelasticsearch.Client
	useCase  application.SearchPolicies
	observer *telemetry.DependencyObserver
}

func newProductionQuery(
	settings config.Elasticsearch,
	observer *telemetry.DependencyObserver,
) (queryRuntime, error) {
	if observer == nil {
		return nil, errors.New("initialize policy search: dependency observer is required")
	}
	client, err := newElasticsearchClient(settings)
	if err != nil {
		return nil, fmt.Errorf("initialize policy search: %w", err)
	}
	searcher, err := policyelasticsearch.NewSearcher(client)
	if err != nil {
		client.CloseIdleConnections()
		return nil, errors.New("initialize policy search: configure Elasticsearch adapter")
	}
	useCase, err := application.NewSearchPolicies(searcher)
	if err != nil {
		client.CloseIdleConnections()
		return nil, errors.New("initialize policy search: configure application use case")
	}
	return &productionQuery{client: client, useCase: useCase, observer: observer}, nil
}

func (query *productionQuery) SearchPolicies(
	ctx context.Context,
	value string,
) ([]domain.Policy, error) {
	dependencyCtx, observation := query.observer.StartElasticsearch(
		ctx,
		telemetry.ElasticsearchSearch,
	)
	policies, err := query.useCase.Execute(dependencyCtx, value)
	observation.Finish(dependencyCtx, err)
	return policies, err
}

func (query *productionQuery) Check(ctx context.Context) error { return query.client.Check(ctx) }

func (query *productionQuery) CloseIdleConnections() { query.client.CloseIdleConnections() }

var _ queryRuntime = (*productionQuery)(nil)

func newElasticsearchClient(settings config.Elasticsearch) (*policyelasticsearch.Client, error) {
	var caCertificate []byte
	if settings.CAFile != "" {
		var err error
		caCertificate, err = os.ReadFile(settings.CAFile)
		if err != nil {
			return nil, errors.New("read Elasticsearch CA file")
		}
	}
	client, err := policyelasticsearch.New(policyelasticsearch.Config{
		Endpoint: settings.URL,
		Index:    settings.Index,
		Username: settings.Username,
		Password: settings.Password,
		CACert:   caCertificate,
		Timeout:  settings.Timeout,
	})
	if err != nil {
		return nil, errors.New("configure Elasticsearch client")
	}
	return client, nil
}
