package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

var (
	// ErrOfferNotFound reports that no stored offer has the submitted number.
	ErrOfferNotFound = errors.New("offer not found")
	// ErrPolicyNotFound reports that no stored policy has the submitted number.
	ErrPolicyNotFound = errors.New("policy not found")
	// ErrPoliciesUnavailable reports that this instance has no storage for policies.
	ErrPoliciesUnavailable = errors.New("policy storage is not configured")
)

// PolicyWrites reads and writes Policy aggregates inside one transaction.
type PolicyWrites interface {
	// LoadOffer returns the stored offer or ErrOfferNotFound.
	LoadOffer(ctx context.Context, number string) (domain.Offer, error)
	// AddPolicy stores a new policy with its versions and the event intent it causes.
	AddPolicy(ctx context.Context, policy domain.Policy, event domain.PolicyEvent) error
	// LoadPolicy returns the complete policy or ErrPolicyNotFound.
	LoadPolicy(ctx context.Context, number string) (domain.Policy, error)
	// AppendVersion adds a version and its covers to the policy with the event intent it causes.
	AppendVersion(
		ctx context.Context,
		number string,
		version domain.PolicyVersion,
		event domain.PolicyEvent,
	) error
}

// PolicyRepository owns the transactions of policy commands and reads complete policies.
type PolicyRepository interface {
	// WithinTransaction commits every write made by work together, or none of them.
	WithinTransaction(ctx context.Context, work func(context.Context, PolicyWrites) error) error
	// LoadPolicy returns the policy with every version and cover, or ErrPolicyNotFound.
	LoadPolicy(ctx context.Context, number string) (domain.Policy, error)
}

// IssuePolicyCommand asks to issue the offer for the holder on behalf of the agent.
type IssuePolicyCommand struct {
	OfferNumber string
	Holder      domain.PolicyHolder
	AgentLogin  string
}

// IssuePolicy issues a policy from a stored offer at its saved price.
type IssuePolicy struct {
	policies PolicyRepository
	now      func() time.Time
	zone     *time.Location
}

// NewIssuePolicy wires the use case. now and zone define the business date of the command.
func NewIssuePolicy(
	policies PolicyRepository,
	now func() time.Time,
	zone *time.Location,
) (*IssuePolicy, error) {
	if policies == nil || now == nil || zone == nil {
		return nil, errors.New("issue policy use case: all dependencies are required")
	}
	return &IssuePolicy{policies: policies, now: now, zone: zone}, nil
}

// Execute loads the offer, issues version 1 and stores it with one registration intent in a
// single transaction. It returns the policy number only after the commit.
func (useCase *IssuePolicy) Execute(
	ctx context.Context,
	command IssuePolicyCommand,
) (string, error) {
	businessDate := useCase.now().In(useCase.zone)
	identifiers, err := randomUUIDs(3)
	if err != nil {
		return "", fmt.Errorf("issue policy: generate identifiers: %w", err)
	}
	number, accountNumber := formatUUID(identifiers[0]), formatUUID(identifiers[1])
	err = useCase.policies.WithinTransaction(
		ctx,
		func(ctx context.Context, writes PolicyWrites) error {
			offer, err := writes.LoadOffer(ctx, command.OfferNumber)
			if err != nil {
				return err
			}
			policy, err := domain.IssuePolicy(
				number,
				accountNumber,
				offer,
				command.Holder,
				command.AgentLogin,
				businessDate,
			)
			if err != nil {
				return err
			}
			event, err := domain.RegistrationEvent(identifiers[2], policy)
			if err != nil {
				return err
			}
			return writes.AddPolicy(ctx, policy, event)
		},
	)
	if err != nil {
		return "", fmt.Errorf("issue policy: %w", err)
	}
	return number, nil
}

// PolicyDetails is a policy number with its latest version.
type PolicyDetails struct {
	Number string
	Latest domain.PolicyVersion
}

// GetPolicy reads a policy's latest version.
type GetPolicy struct {
	policies PolicyRepository
}

// NewGetPolicy wires the use case.
func NewGetPolicy(policies PolicyRepository) (*GetPolicy, error) {
	if policies == nil {
		return nil, errors.New("get policy use case: repository is required")
	}
	return &GetPolicy{policies: policies}, nil
}

// Execute returns the details of the policy's highest version number.
func (useCase *GetPolicy) Execute(ctx context.Context, number string) (PolicyDetails, error) {
	policy, err := useCase.policies.LoadPolicy(ctx, number)
	if err != nil {
		return PolicyDetails{}, fmt.Errorf("get policy: %w", err)
	}
	latest, err := policy.LatestVersion()
	if err != nil {
		return PolicyDetails{}, fmt.Errorf("get policy: %w", err)
	}
	return PolicyDetails{Number: policy.Number, Latest: latest}, nil
}

// TerminatePolicy ends a policy's coverage on the business date.
type TerminatePolicy struct {
	policies PolicyRepository
	now      func() time.Time
	zone     *time.Location
}

// NewTerminatePolicy wires the use case. now and zone define the termination date.
func NewTerminatePolicy(
	policies PolicyRepository,
	now func() time.Time,
	zone *time.Location,
) (*TerminatePolicy, error) {
	if policies == nil || now == nil || zone == nil {
		return nil, errors.New("terminate policy use case: all dependencies are required")
	}
	return &TerminatePolicy{policies: policies, now: now, zone: zone}, nil
}

// Execute loads the complete policy, appends its terminal version and stores one termination
// intent in a single transaction. Earlier versions are kept.
func (useCase *TerminatePolicy) Execute(ctx context.Context, number string) (string, error) {
	terminationDate := useCase.now().In(useCase.zone)
	intentID, err := randomUUID()
	if err != nil {
		return "", fmt.Errorf("terminate policy: generate intent: %w", err)
	}
	err = useCase.policies.WithinTransaction(
		ctx,
		func(ctx context.Context, writes PolicyWrites) error {
			policy, err := writes.LoadPolicy(ctx, number)
			if err != nil {
				return err
			}
			terminal, err := policy.Terminate(terminationDate)
			if err != nil {
				return err
			}
			return writes.AppendVersion(ctx, policy.Number, terminal,
				domain.TerminationEvent(intentID, policy.Number, terminal))
		},
	)
	if err != nil {
		return "", fmt.Errorf("terminate policy: %w", err)
	}
	return number, nil
}

// UnavailableIssuance is the issue operation of an instance without storage.
func UnavailableIssuance(context.Context, IssuePolicyCommand) (string, error) {
	return "", ErrPoliciesUnavailable
}

// UnavailableTermination is the terminate operation of an instance without storage.
func UnavailableTermination(context.Context, string) (string, error) {
	return "", ErrPoliciesUnavailable
}

// UnavailablePolicyDetails is the lookup operation of an instance without storage.
func UnavailablePolicyDetails(context.Context, string) (PolicyDetails, error) {
	return PolicyDetails{}, ErrPoliciesUnavailable
}

func randomUUIDs(count int) ([][16]byte, error) {
	values := make([][16]byte, count)
	for index := range values {
		value, err := randomUUID()
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	return values, nil
}
