package service

import (
	"context"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/telemetry"
)

type observedMetadataReader struct {
	reader   application.MetadataReader
	observer *telemetry.DependencyObserver
}

func newObservedMetadataReader(
	reader application.MetadataReader,
	observer *telemetry.DependencyObserver,
) application.MetadataReader {
	return &observedMetadataReader{reader: reader, observer: observer}
}

func (reader *observedMetadataReader) FindByPolicyNumber(
	ctx context.Context,
	policyNumber string,
) ([]domain.StoredDocument, error) {
	dependencyCtx, observation := reader.observer.Start(ctx, telemetry.DependencyPostgreSQL)
	result, err := reader.reader.FindByPolicyNumber(dependencyCtx, policyNumber)
	observation.Finish(dependencyCtx, telemetry.DependencyResultSuccess, err)
	return result, err
}

type observedContentReader struct {
	reader   application.ContentReader
	observer *telemetry.DependencyObserver
}

func newObservedContentReader(
	reader application.ContentReader,
	observer *telemetry.DependencyObserver,
) application.ContentReader {
	return &observedContentReader{reader: reader, observer: observer}
}

func (reader *observedContentReader) Read(
	ctx context.Context,
	stored []byte,
) ([]byte, bool, error) {
	dependencyCtx, observation := reader.observer.Start(ctx, telemetry.DependencyObjectStore)
	content, present, err := reader.reader.Read(dependencyCtx, stored)
	result := telemetry.DependencyResultSuccess
	if !present && err == nil {
		result = telemetry.DependencyResultOmitted
	}
	observation.Finish(dependencyCtx, result, err)
	return content, present, err
}
