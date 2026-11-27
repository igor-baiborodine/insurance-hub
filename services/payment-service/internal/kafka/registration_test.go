package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
)

func TestRegistrationHandler_HandlesPolicyProducerFixture(t *testing.T) {
	// given
	key, value := policyRegistrationFixture(t)
	operation := &fakeRegistrationOperation{}
	handler, err := NewRegistrationHandler(operation)
	if err != nil {
		t.Fatal(err)
	}

	// when
	err = handler.Handle(t.Context(), key, value)
	// then
	if err != nil {
		t.Fatal(err)
	}
	wantDate := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if len(operation.registrations) != 1 {
		t.Fatalf("registration calls = %d, want 1", len(operation.registrations))
	}
	got := operation.registrations[0]
	if got.PolicyNumber != "SYN-442-001" || got.Premium != "120.00" ||
		!got.CoverageStart.Equal(wantDate) {
		t.Fatalf("registration = %+v, want producer fixture facts", got)
	}
}

func TestRegistrationHandler_RejectsInvalidContractBeforeOperation(t *testing.T) {
	// given
	fixtureKey, fixtureValue := policyRegistrationFixture(t)
	tests := []struct {
		name      string
		key       []byte
		value     []byte
		wantError error
	}{
		{
			name:      "mismatched key",
			key:       []byte("different-policy"),
			value:     fixtureValue,
			wantError: errPolicyKeyMismatch,
		},
		{
			name: "malformed JSON", key: fixtureKey, value: []byte(`{"policy":`),
			wantError: errMalformedRegistration,
		},
		{
			name: "missing policy number",
			key:  nil,
			value: []byte(
				`{"policy":{"number":"","from":[2026,1,1],"totalPremium":120.00}}`,
			),
			wantError: errMissingPolicyNumber,
		},
		{
			name: "impossible coverage date", key: fixtureKey,
			value:     registrationValue("SYN-442-001", "2026,2,30", "120.00"),
			wantError: errInvalidCoverageStart,
		},
		{
			name: "premium requires rounding", key: fixtureKey,
			value:     registrationValue("SYN-442-001", "2026,1,1", "120.001"),
			wantError: errInvalidPremium,
		},
		{
			name: "premium exceeds precision", key: fixtureKey,
			value: registrationValue(
				"SYN-442-001", "2026,1,1", "100000000000000000.00",
			),
			wantError: errInvalidPremium,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation := &fakeRegistrationOperation{}
			handler, err := NewRegistrationHandler(operation)
			if err != nil {
				t.Fatal(err)
			}

			// when
			err = handler.Handle(t.Context(), test.key, test.value)

			// then
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Handle() error = %v, want %v", err, test.wantError)
			}
			if len(operation.registrations) != 0 {
				t.Fatalf(
					"registration calls = %d, want 0",
					len(operation.registrations),
				)
			}
			if strings.Contains(err.Error(), "SYN-442-001") ||
				strings.Contains(err.Error(), "120") {
				t.Fatalf("safe error exposed registration contents: %v", err)
			}
		})
	}
}

func TestNewRegistrationHandler_RequiresOperation(t *testing.T) {
	// when
	handler, err := NewRegistrationHandler(nil)

	// then
	if handler != nil || err == nil {
		t.Fatalf("NewRegistrationHandler(nil) = (%v, %v)", handler, err)
	}
}

type fakeRegistrationOperation struct {
	registrations []application.PolicyRegistration
}

func (operation *fakeRegistrationOperation) RegisterPolicy(
	_ context.Context,
	registration application.PolicyRegistration,
) error {
	operation.registrations = append(operation.registrations, registration)
	return nil
}

func policyRegistrationFixture(t *testing.T) ([]byte, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "policy-service", "testdata", "events",
		"consumer-compatibility.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Events map[string]struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		} `json:"events"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	registration, ok := fixture.Events["registration"]
	if !ok {
		t.Fatal("Policy compatibility fixture has no registration event")
	}
	return []byte(registration.Key), registration.Value
}

func registrationValue(number, date, premium string) []byte {
	return []byte(`{"policy":{"number":"` + number + `","from":[` + date +
		`],"totalPremium":` + premium + `}}`)
}
