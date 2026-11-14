package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
)

// PolicyRenderer renders one validated registration as a PDF.
type PolicyRenderer interface {
	Render(context.Context, domain.PolicyRegistration) ([]byte, error)
}

// DocumentUploader stores one generated PDF under its date-derived key.
type DocumentUploader interface {
	UploadPDF(context.Context, string, []byte) error
}

// DocumentMetadataWriter records the generated object's UTF-8 key for lookup.
type DocumentMetadataWriter interface {
	InsertObjectKey(context.Context, string, string) error
}

// GenerationResult identifies the object created by one generation attempt.
type GenerationResult struct {
	ObjectKey string
}

// GenerateDocument owns the non-transactional render, upload, then metadata sequence.
type GenerateDocument struct {
	renderer PolicyRenderer
	uploader DocumentUploader
	metadata DocumentMetadataWriter
}

// NewGenerateDocument constructs the generation use case with its consumed ports.
func NewGenerateDocument(
	renderer PolicyRenderer,
	uploader DocumentUploader,
	metadata DocumentMetadataWriter,
) (*GenerateDocument, error) {
	if renderer == nil || uploader == nil || metadata == nil {
		return nil, errors.New("create document generation: dependencies are required")
	}
	return &GenerateDocument{renderer: renderer, uploader: uploader, metadata: metadata}, nil
}

// Execute renders, uploads, and records one policy document without compensating side effects.
func (generation *GenerateDocument) Execute(
	ctx context.Context,
	registration domain.PolicyRegistration,
) (GenerationResult, error) {
	if err := ctx.Err(); err != nil {
		return GenerationResult{}, err
	}
	pdf, err := generation.renderer.Render(ctx, registration)
	if err != nil {
		return GenerationResult{}, fmt.Errorf("render policy document: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return GenerationResult{}, err
	}
	objectKey := fmt.Sprintf(
		"%04d/%02d/%s.pdf",
		registration.From().Year(),
		registration.From().Month(),
		registration.Number(),
	)
	if err := generation.uploader.UploadPDF(ctx, objectKey, pdf); err != nil {
		return GenerationResult{}, fmt.Errorf("upload policy document: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return GenerationResult{}, err
	}
	if err := generation.metadata.InsertObjectKey(
		ctx,
		registration.Number(),
		objectKey,
	); err != nil {
		return GenerationResult{}, fmt.Errorf("record policy document: %w", err)
	}
	return GenerationResult{ObjectKey: objectKey}, nil
}
