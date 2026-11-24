package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

type consumerCompatibilityFixture struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Ownership     string                  `json:"ownership"`
	ProducerOrder []string                `json:"producerOrder"`
	Events        map[string]fixtureEvent `json:"events"`
}

type fixtureEvent struct {
	Topic string          `json:"topic"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

func TestOutboxIntent_EncodesTheKeyedPolicyEnvelope(t *testing.T) {
	// given
	event := domain.PolicyEvent{
		IntentID:     [16]byte{9},
		Topic:        "policy-registered",
		PolicyNumber: "policy-1",
		From:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		To:           time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		PolicyHolder: "Synthetic Holder",
		ProductCode:  "HSI",
		TotalPremium: "120.10",
	}

	// when
	intent, err := outboxIntent(context.Background(), event)

	// then
	want := `{"policy":{"number":"policy-1","from":[2026,1,1],"to":[2026,12,31],` +
		`"policyHolder":"Synthetic Holder","productCode":"HSI","totalPremium":120.10}}`
	if err != nil || string(intent.Payload) != want || intent.Key != "policy-1" ||
		intent.Topic != "policy-registered" || intent.ID != event.IntentID {
		t.Errorf(
			"intent = %+v / %s (%v), want payload %s",
			intent,
			intent.Payload,
			err,
			want,
		)
	}
}

func TestConsumerCompatibilityFixture_MatchesProducedRecordsAndIndependentWireOracle(
	t *testing.T,
) {
	// given
	fixture := loadConsumerCompatibilityFixture(t)
	events := map[string]domain.PolicyEvent{
		"registration": {
			IntentID:     [16]byte{1},
			Topic:        domain.TopicPolicyRegistered,
			PolicyNumber: "SYN-442-001",
			From:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			To:           time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
			PolicyHolder: "Synthetic Holder",
			ProductCode:  "HSI",
			TotalPremium: "120.00",
		},
		"termination": {
			IntentID: [16]byte{2}, Topic: domain.TopicPolicyTerminated,
			PolicyNumber: "SYN-442-001",
			From:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			To:           time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
			PolicyHolder: "Synthetic Holder", ProductCode: "HSI", TotalPremium: "59.51",
		},
	}

	// when / then
	if fixture.SchemaVersion != 1 || fixture.Ownership != "policy-service" ||
		len(fixture.ProducerOrder) != 2 {
		t.Fatalf("fixture identity or producer order differs: %+v", fixture)
	}
	for index, name := range fixture.ProducerOrder {
		wire := fixture.Events[name]
		intent, err := outboxIntent(context.Background(), events[name])
		if err != nil {
			t.Fatal(err)
		}
		want := compactJSON(t, wire.Value)
		if intent.Topic != wire.Topic || intent.Key != wire.Key ||
			!bytes.Equal(intent.Payload, want) {
			t.Fatalf(
				"record %d (%s) = %s/%s %s, want %s/%s %s",
				index,
				name,
				intent.Topic,
				intent.Key,
				intent.Payload,
				wire.Topic,
				wire.Key,
				want,
			)
		}
		if err := assertIndependentWire(name, wire); err != nil {
			t.Fatalf("record %d (%s): %v", index, name, err)
		}
	}

	mutatedStart := fixture.Events["termination"]
	mutatedStart.Value = bytes.Replace(
		mutatedStart.Value,
		[]byte("[\n            2026,\n            1,\n            1\n          ]"),
		[]byte("[2026, 7, 1]"),
		1,
	)
	if err := assertIndependentWire("termination", mutatedStart); err == nil {
		t.Fatal("July 1 termination event start passed the independent wire oracle")
	}
	quotedPremium := fixture.Events["registration"]
	quotedPremium.Value = bytes.Replace(
		quotedPremium.Value, []byte("120.00"), []byte(`"120.00"`), 1,
	)
	if err := assertIndependentWire("registration", quotedPremium); err == nil {
		t.Fatal("quoted registration premium passed the independent wire oracle")
	}
}

func loadConsumerCompatibilityFixture(t *testing.T) consumerCompatibilityFixture {
	t.Helper()
	payload, err := os.ReadFile("../../testdata/events/consumer-compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture consumerCompatibilityFixture
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func compactJSON(t *testing.T, value []byte) []byte {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, value); err != nil {
		t.Fatal(err)
	}
	return compact.Bytes()
}

func assertIndependentWire(name string, wire fixtureEvent) error {
	var envelope struct {
		Policy struct {
			Number       string          `json:"number"`
			From         [3]int          `json:"from"`
			To           [3]int          `json:"to"`
			PolicyHolder string          `json:"policyHolder"`
			ProductCode  string          `json:"productCode"`
			TotalPremium json.RawMessage `json:"totalPremium"`
			AgentLogin   json.RawMessage `json:"agentLogin"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(wire.Value, &envelope); err != nil {
		return err
	}
	wantTo, wantPremium := [3]int{2026, 12, 31}, "120.00"
	if name == "termination" {
		wantTo, wantPremium = [3]int{2026, 6, 30}, "59.51"
	}
	policy := envelope.Policy
	if wire.Key != "SYN-442-001" || policy.Number != wire.Key ||
		policy.From != [3]int{2026, 1, 1} || policy.To != wantTo ||
		policy.PolicyHolder != "Synthetic Holder" || policy.ProductCode != "HSI" ||
		string(policy.TotalPremium) != wantPremium || policy.AgentLogin != nil {
		return errors.New("wire contract differs")
	}
	return nil
}
