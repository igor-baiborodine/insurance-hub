package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

var (
	ErrPolicyProjectionWriterRequired = errors.New("policy projection writer is required")
	ErrPolicyNumberRequired           = errors.New("policy number is required")
)

// PolicyEvent contains the received registration values accepted for Dashboard projection.
type PolicyEvent struct {
	Number       string
	From         *domain.Date
	To           *domain.Date
	PolicyHolder *string
	ProductCode  *string
	TotalPremium *domain.Premium
	AgentLogin   *string
}

// PolicyProjectionWriter replaces one number-keyed Dashboard projection document.
type PolicyProjectionWriter interface {
	ReplacePolicy(context.Context, domain.PolicyProjection) error
}

// ProjectPolicy maps a received registration into the Dashboard read model.
type ProjectPolicy struct{ writer PolicyProjectionWriter }

func NewProjectPolicy(writer PolicyProjectionWriter) (ProjectPolicy, error) {
	if writer == nil {
		return ProjectPolicy{}, ErrPolicyProjectionWriterRequired
	}
	return ProjectPolicy{writer: writer}, nil
}

func (useCase ProjectPolicy) Execute(ctx context.Context, event PolicyEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.Number == "" {
		return ErrPolicyNumberRequired
	}
	projection := domain.PolicyProjection{
		Number: event.Number, From: event.From, To: event.To,
		PolicyHolder: event.PolicyHolder, ProductCode: event.ProductCode,
		TotalPremium: event.TotalPremium, AgentLogin: event.AgentLogin,
	}
	if err := useCase.writer.ReplacePolicy(ctx, projection); err != nil {
		return fmt.Errorf("project policy: %w", err)
	}
	return ctx.Err()
}
