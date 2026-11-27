package application

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"
)

var accountUUID = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

func TestNewPolicyRegistrations_RequiresDependencies(t *testing.T) {
	// given
	store := &fakeRegistrationStore{}
	for _, test := range []struct {
		name  string
		store RegistrationStore
		now   func() time.Time
	}{
		{name: "missing store", now: time.Now},
		{name: "missing clock", store: store},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			registrations, err := NewPolicyRegistrations(test.store, test.now)

			// then
			if registrations != nil || err == nil {
				t.Fatalf("NewPolicyRegistrations() = (%v, %v)", registrations, err)
			}
		})
	}
}

func TestPolicyRegistrations_RegisterPolicy_AddsPaymentFacts(t *testing.T) {
	// given
	store := &fakeRegistrationStore{}
	registeredAt := time.Date(
		2026, time.January, 2, 1, 2, 3, 0,
		time.FixedZone("west", -5*60*60),
	)
	clockCalls := 0
	registrations, err := NewPolicyRegistrations(store, func() time.Time {
		clockCalls++
		return registeredAt
	})
	if err != nil {
		t.Fatal(err)
	}
	input := PolicyRegistration{
		PolicyNumber: "SYN-442-001", Premium: "120.00",
		CoverageStart: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}

	// when
	err = registrations.RegisterPolicy(t.Context(), input)

	// then
	if err != nil || len(store.registrations) != 1 {
		t.Fatalf("RegisterPolicy() = %v, stored %+v", err, store.registrations)
	}
	got := store.registrations[0]
	if got.PolicyRegistration != input || !accountUUID.MatchString(got.PolicyAccountNumber) ||
		!got.RegisteredAt.Equal(registeredAt) || got.RegisteredAt.Location() != time.UTC {
		t.Fatalf("stored registration = %+v", got)
	}
	if clockCalls != 1 {
		t.Fatalf("clock calls = %d, want 1", clockCalls)
	}
}

func TestPolicyRegistrations_RegisterPolicy_PreservesStoreFailure(t *testing.T) {
	// given
	store := &fakeRegistrationStore{err: ErrRegistrationConflict}
	registrations, err := NewPolicyRegistrations(store, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	// when
	err = registrations.RegisterPolicy(t.Context(), PolicyRegistration{})

	// then
	if !errors.Is(err, ErrRegistrationConflict) {
		t.Fatalf("RegisterPolicy() error = %v, want registration conflict", err)
	}
}

type fakeRegistrationStore struct {
	registrations []AccountRegistration
	err           error
}

func (store *fakeRegistrationStore) StoreRegistration(
	_ context.Context,
	registration AccountRegistration,
) error {
	store.registrations = append(store.registrations, registration)
	return store.err
}
