package kafka

import (
	"encoding/json"
	"os"
	"testing"
)

type compatibilityFixture struct {
	ProducerOrder []string                          `json:"producerOrder"`
	Events        map[string]compatibilityWireEvent `json:"events"`
}

type compatibilityWireEvent struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

func TestDecode_AcceptsPolicyProducedRegistration(t *testing.T) {
	// given
	fixture := loadCompatibilityFixture(t)
	record := fixture.Events["registration"]

	// when
	event, err := decode(record.Value)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if record.Key != event.Number || event.From == nil || event.From.String() != "2026-01-01" ||
		event.To == nil || event.To.String() != "2026-12-31" || event.PolicyHolder == nil ||
		*event.PolicyHolder != "Synthetic Holder" || event.ProductCode == nil ||
		*event.ProductCode != "HSI" || event.TotalPremium == nil ||
		event.TotalPremium.String() != "120.00" || event.AgentLogin != nil {
		t.Fatalf("decoded Policy registration = %+v", event)
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
