package application

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"
)

// ErrRegistrationConflict reports that durable facts differ from a replayed registration.
var ErrRegistrationConflict = errors.New("payment registration conflicts with stored facts")

// PolicyRegistration contains the validated facts needed to open one Payment account.
type PolicyRegistration struct {
	PolicyNumber  string
	Premium       string
	CoverageStart time.Time
}

// AccountRegistration adds Payment-owned facts to one validated Policy registration.
type AccountRegistration struct {
	PolicyRegistration
	PolicyAccountNumber string
	RegisteredAt        time.Time
}

// RegistrationStore durably creates or compares one Payment account registration.
type RegistrationStore interface {
	StoreRegistration(context.Context, AccountRegistration) error
}

// PolicyRegistrations opens Payment accounts for validated Policy registrations.
type PolicyRegistrations struct {
	registrations RegistrationStore
	now           func() time.Time
}

// NewPolicyRegistrations wires the registration operation and its UTC receipt clock.
func NewPolicyRegistrations(
	registrations RegistrationStore,
	now func() time.Time,
) (*PolicyRegistrations, error) {
	if registrations == nil || now == nil {
		return nil, errors.New("policy registrations: all dependencies are required")
	}
	return &PolicyRegistrations{registrations: registrations, now: now}, nil
}

// RegisterPolicy stores a new account or accepts a replay only after durable comparison.
func (useCase *PolicyRegistrations) RegisterPolicy(
	ctx context.Context,
	registration PolicyRegistration,
) error {
	accountNumber, err := newPolicyAccountNumber()
	if err != nil {
		return err
	}
	registeredAt := useCase.now().UTC()
	if err := useCase.registrations.StoreRegistration(ctx, AccountRegistration{
		PolicyRegistration:  registration,
		PolicyAccountNumber: accountNumber,
		RegisteredAt:        registeredAt,
	}); err != nil {
		return fmt.Errorf("register policy payment account: %w", err)
	}
	return nil
}

// newPolicyAccountNumber returns a Payment-generated random version-4 UUID.
func newPolicyAccountNumber() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate policy account number: %w", err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}
