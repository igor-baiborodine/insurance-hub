package service

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/jsreport"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/objectstore"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/postgres"
)

type generationRuntime interface {
	Execute(context.Context, domain.PolicyRegistration) (application.GenerationResult, error)
	Check(context.Context) error
	Close()
}

type productionGeneration struct {
	useCase  *application.GenerateDocument
	renderer *jsreport.Client
	uploader *objectstore.Writer
	pool     *pgxpool.Pool
}

func newProductionGeneration(
	ctx context.Context,
	settings config.Events,
	maximumDocumentBytes int,
) (generationRuntime, error) {
	pool, err := postgres.OpenPool(ctx, settings.WriterDatabase)
	if err != nil {
		return nil, errors.New("initialize document generation: open writer database")
	}
	closePool := true
	defer func() {
		if closePool {
			pool.Close()
		}
	}()
	metadata, err := postgres.NewWriter(
		pool,
		settings.WriterDatabase.AcquireTimeout,
		settings.WriterDatabase.QueryTimeout,
	)
	if err != nil {
		return nil, errors.New("initialize document generation: configure metadata writer")
	}
	renderer, err := jsreport.NewClient(jsreport.Settings{
		Origin: settings.JSReportOrigin, ConnectTimeout: settings.RendererConnectTimeout,
		RequestTimeout: settings.RendererRequestTimeout, MaximumBytes: maximumDocumentBytes,
	})
	if err != nil {
		return nil, errors.New("initialize document generation: configure renderer")
	}
	uploader, err := objectstore.NewWriter(
		settings.WriterObjectStore,
		settings.UploadTimeout,
		maximumDocumentBytes,
	)
	if err != nil {
		renderer.Close()
		return nil, errors.New("initialize document generation: configure object writer")
	}
	useCase, err := application.NewGenerateDocument(renderer, uploader, metadata)
	if err != nil {
		uploader.Close()
		renderer.Close()
		return nil, errors.New("initialize document generation: configure use case")
	}
	closePool = false
	return &productionGeneration{
		useCase: useCase, renderer: renderer, uploader: uploader, pool: pool,
	}, nil
}

func (generation *productionGeneration) Execute(
	ctx context.Context,
	registration domain.PolicyRegistration,
) (application.GenerationResult, error) {
	return generation.useCase.Execute(ctx, registration)
}

func (generation *productionGeneration) Check(ctx context.Context) error {
	if err := generation.pool.Ping(ctx); err != nil {
		return errors.New("check document generation: writer database unavailable")
	}
	return nil
}

func (generation *productionGeneration) Close() {
	if generation == nil {
		return
	}
	generation.uploader.Close()
	generation.renderer.Close()
	generation.pool.Close()
}

var _ generationRuntime = (*productionGeneration)(nil)
