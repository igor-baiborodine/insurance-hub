package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
)

var ErrPolicyViewWriterRequired = errors.New("policy view writer is required")

// PolicyEvent contains the received event values used by the legacy projection.
type PolicyEvent struct {
	Number       string
	From         *domain.Date
	To           *domain.Date
	PolicyHolder string
}

// PolicyViewWriter replaces one number-keyed policy search document.
type PolicyViewWriter interface {
	ReplacePolicy(context.Context, domain.Policy) error
}

// ProjectPolicy maps a received policy event into the search read model.
type ProjectPolicy struct{ writer PolicyViewWriter }

func NewProjectPolicy(writer PolicyViewWriter) (ProjectPolicy, error) {
	if writer == nil {
		return ProjectPolicy{}, ErrPolicyViewWriterRequired
	}
	return ProjectPolicy{writer: writer}, nil
}

func (useCase ProjectPolicy) Execute(ctx context.Context, event PolicyEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	policy := domain.Policy{
		Number:       event.Number,
		DateFrom:     event.From,
		DateTo:       event.To,
		PolicyHolder: event.PolicyHolder,
	}
	if err := useCase.writer.ReplacePolicy(ctx, policy); err != nil {
		return fmt.Errorf("project policy: %w", err)
	}
	return ctx.Err()
}
