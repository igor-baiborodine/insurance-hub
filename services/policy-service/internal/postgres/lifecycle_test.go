package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

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
