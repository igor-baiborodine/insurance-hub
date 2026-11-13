// Package application owns Document lookup orchestration and the ports it consumes.
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
)

var (
	// ErrUnavailable identifies a required dependency failure without exposing its cause.
	ErrUnavailable = errors.New("document dependency unavailable")
	// ErrResponseLimit identifies lookup concurrency, per-document, or aggregate budget exhaustion.
	ErrResponseLimit = errors.New("document response limit exceeded")
)

// MetadataReader performs the exact policy-number metadata lookup consumed by FindDocuments.
type MetadataReader interface {
	FindByPolicyNumber(context.Context, string) ([]domain.StoredDocument, error)
}

// ContentReader resolves inline or object-backed stored bytes.
type ContentReader interface {
	Read(context.Context, []byte) (content []byte, present bool, err error)
}

// LookupResult carries wire-independent documents and the count omitted by compatibility rules.
type LookupResult struct {
	Documents []domain.Document
	Omitted   int
}

// FindDocuments implements the single bounded Document lookup path.
type FindDocuments struct {
	metadata         MetadataReader
	content          ContentReader
	maximumRawBytes  int
	lookupPermission chan struct{}
}

// NewFindDocuments constructs the lookup with explicit dependencies and safety bounds.
func NewFindDocuments(
	metadata MetadataReader,
	content ContentReader,
	maximumRawBytes int,
	maximumConcurrent int,
) (*FindDocuments, error) {
	if metadata == nil || content == nil {
		return nil, errors.New("create document lookup: readers are required")
	}
	if maximumRawBytes <= 0 || maximumConcurrent <= 0 {
		return nil, errors.New("create document lookup: limits must be positive")
	}
	return &FindDocuments{
		metadata: metadata, content: content, maximumRawBytes: maximumRawBytes,
		lookupPermission: make(chan struct{}, maximumConcurrent),
	}, nil
}

// Execute returns every retrievable matching row while preserving duplicates and source order.
func (lookup *FindDocuments) Execute(
	ctx context.Context,
	policyNumber string,
) (LookupResult, error) {
	select {
	case lookup.lookupPermission <- struct{}{}:
		defer func() { <-lookup.lookupPermission }()
	default:
		return LookupResult{}, ErrResponseLimit
	}

	stored, err := lookup.metadata.FindByPolicyNumber(ctx, policyNumber)
	if err != nil {
		return LookupResult{}, fmt.Errorf("find document metadata: %w", err)
	}
	result := LookupResult{Documents: make([]domain.Document, 0, len(stored))}
	total := 0
	for _, candidate := range stored {
		content, present, readErr := lookup.content.Read(ctx, candidate.Bytes)
		if readErr != nil {
			return LookupResult{}, fmt.Errorf("read document content: %w", readErr)
		}
		if !present {
			result.Omitted++
			continue
		}
		if len(content) > lookup.maximumRawBytes-total {
			return LookupResult{}, ErrResponseLimit
		}
		total += len(content)
		result.Documents = append(result.Documents, domain.Document{
			PolicyNumber: candidate.PolicyNumber,
			Content:      content,
		})
	}
	return result, nil
}
