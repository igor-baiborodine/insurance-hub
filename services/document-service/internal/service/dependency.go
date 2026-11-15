package service

import (
	"context"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/objectstore"
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
	dependencyCtx, observation := reader.observer.Start(
		ctx,
		telemetry.OperationFindDocuments,
		telemetry.DependencyPostgreSQL,
	)
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
	if !objectstore.IsObjectKey(stored) {
		return reader.reader.Read(ctx, stored)
	}
	dependencyCtx, observation := reader.observer.Start(
		ctx,
		telemetry.OperationFindDocuments,
		telemetry.DependencyObjectStore,
	)
	content, present, err := reader.reader.Read(dependencyCtx, stored)
	result := telemetry.DependencyResultSuccess
	if !present && err == nil {
		result = telemetry.DependencyResultFetchFailed
	}
	observation.Finish(dependencyCtx, result, err)
	return content, present, err
}

type observedPolicyRenderer struct {
	renderer application.PolicyRenderer
	observer *telemetry.DependencyObserver
}

func newObservedPolicyRenderer(
	renderer application.PolicyRenderer,
	observer *telemetry.DependencyObserver,
) application.PolicyRenderer {
	return &observedPolicyRenderer{renderer: renderer, observer: observer}
}

func (renderer *observedPolicyRenderer) Render(
	ctx context.Context,
	registration domain.PolicyRegistration,
) ([]byte, error) {
	dependencyCtx, observation := renderer.observer.Start(
		ctx,
		telemetry.OperationGenerateDocument,
		telemetry.DependencyJSReport,
	)
	pdf, err := renderer.renderer.Render(dependencyCtx, registration)
	observation.Finish(dependencyCtx, telemetry.DependencyResultSuccess, err)
	return pdf, err
}

type observedDocumentUploader struct {
	uploader application.DocumentUploader
	observer *telemetry.DependencyObserver
}

func newObservedDocumentUploader(
	uploader application.DocumentUploader,
	observer *telemetry.DependencyObserver,
) application.DocumentUploader {
	return &observedDocumentUploader{uploader: uploader, observer: observer}
}

func (uploader *observedDocumentUploader) UploadPDF(
	ctx context.Context,
	objectKey string,
	pdf []byte,
) error {
	dependencyCtx, observation := uploader.observer.Start(
		ctx,
		telemetry.OperationGenerateDocument,
		telemetry.DependencyObjectStore,
	)
	err := uploader.uploader.UploadPDF(dependencyCtx, objectKey, pdf)
	observation.Finish(dependencyCtx, telemetry.DependencyResultSuccess, err)
	return err
}

type observedDocumentMetadataWriter struct {
	writer   application.DocumentMetadataWriter
	observer *telemetry.DependencyObserver
}

func newObservedDocumentMetadataWriter(
	writer application.DocumentMetadataWriter,
	observer *telemetry.DependencyObserver,
) application.DocumentMetadataWriter {
	return &observedDocumentMetadataWriter{writer: writer, observer: observer}
}

func (writer *observedDocumentMetadataWriter) InsertObjectKey(
	ctx context.Context,
	policyNumber string,
	objectKey string,
) error {
	dependencyCtx, observation := writer.observer.Start(
		ctx,
		telemetry.OperationGenerateDocument,
		telemetry.DependencyPostgreSQL,
	)
	err := writer.writer.InsertObjectKey(dependencyCtx, policyNumber, objectKey)
	observation.Finish(dependencyCtx, telemetry.DependencyResultSuccess, err)
	return err
}
