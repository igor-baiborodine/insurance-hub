package kafka

import (
	"encoding/json"
	"os"
	"testing"
)

type compatibilityFixture struct {
	Events map[string]compatibilityWireEvent `json:"events"`
}

type compatibilityWireEvent struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

func TestDecode_AcceptsPolicyProducedRegistration(t *testing.T) {
	// given
	record := loadCompatibilityFixture(t).Events["registration"]

	// when
	registration, err := decode([]byte(record.Key), record.Value)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if registration.Number() != "SYN-442-001" ||
		registration.From().Parts() != [3]int{2026, 1, 1} ||
		registration.To().Parts() != [3]int{2026, 12, 31} ||
		registration.PolicyHolder() != "Synthetic Holder" ||
		registration.ProductCode() != "HSI" || registration.TotalPremium() != "120.00" ||
		registration.AgentLogin() != nil {
		t.Fatalf("decoded Policy registration = %+v", registration)
	}
}

func loadCompatibilityFixture(t *testing.T) compatibilityFixture {
	t.Helper()
	payload, err := os.ReadFile(
		"../../../policy-service/testdata/events/consumer-compatibility.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	var fixture compatibilityFixture
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}
