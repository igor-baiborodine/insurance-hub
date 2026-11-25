package kafka

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
)

type compatibilityFixture struct {
	ProducerOrder []string                          `json:"producerOrder"`
	Events        map[string]compatibilityWireEvent `json:"events"`
}

type compatibilityWireEvent struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

type compatibilityWriter struct{ policies []domain.Policy }

func (writer *compatibilityWriter) ReplacePolicy(
	_ context.Context,
	policy domain.Policy,
) error {
	writer.policies = append(writer.policies, policy)
	return nil
}

func TestDecodeAndProjection_AcceptPolicyProducedLifecycleInProducerOrder(t *testing.T) {
	// given
	fixture := loadCompatibilityFixture(t)
	writer := &compatibilityWriter{}
	projection, err := application.NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}

	// when
	for _, name := range fixture.ProducerOrder {
		record := fixture.Events[name]
		event, decodeErr := decode(record.Value)
		if decodeErr != nil {
			t.Fatalf("decode %s: %v", name, decodeErr)
		}
		if record.Key != event.Number {
			t.Fatalf("%s key %q differs from policy %q", name, record.Key, event.Number)
		}
		if projectErr := projection.Execute(
			context.Background(),
			event,
		); projectErr != nil {
			t.Fatalf("project %s: %v", name, projectErr)
		}
	}

	// then
	if len(writer.policies) != 2 || writer.policies[0].DateFrom == nil ||
		writer.policies[0].DateFrom.String() != "2026-01-01" ||
		writer.policies[0].DateTo == nil || writer.policies[0].DateTo.String() != "2026-12-31" ||
		writer.policies[1].DateFrom == nil || writer.policies[1].DateFrom.String() != "2026-01-01" ||
		writer.policies[1].DateTo == nil || writer.policies[1].DateTo.String() != "2026-06-30" {
		t.Fatalf("projected Policy lifecycle = %+v", writer.policies)
	}
}

func loadCompatibilityFixture(t *testing.T) compatibilityFixture {
	t.Helper()
	payload, err := os.ReadFile(
		"testdata/policy-lifecycle.json",
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
