package domain_test

import (
	"errors"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

func TestAgentSelection_PreservesPresenceAndExactValue(t *testing.T) {
	tests := []struct {
		name        string
		selection   domain.AgentSelection
		wantPresent bool
		wantValue   string
	}{
		{name: "all agents", selection: domain.AllAgents()},
		{name: "exact empty agent", selection: domain.ExactAgent(""), wantPresent: true},
		{
			name: "exact agent bytes", selection: domain.ExactAgent(" Agent.Smith "),
			wantPresent: true, wantValue: " Agent.Smith ",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			present, value := test.selection.Present(), test.selection.Value()

			// then
			if present != test.wantPresent || value != test.wantValue {
				t.Fatalf(
					"selection = (%t, %q), want (%t, %q)",
					present,
					value,
					test.wantPresent,
					test.wantValue,
				)
			}
		})
	}
}

func TestAgentPremiumAmount_AcceptsCanonicalJavaTokens(t *testing.T) {
	accepted := []string{
		"3000.0", "1.005", "-1.005", "0.30000000000000004", "1.0E-7", "1000.0",
		"0.0", "0.00010", "0.00050", "0.0000010", "1.0E+7", "1.2E+7",
		"12345678", "12345678.9", "10000000.5", "4.9E-324",
		"1.7976931348623157E+308",
	}
	for _, token := range accepted {
		t.Run(token, func(t *testing.T) {
			// when
			amount, err := domain.NewAgentPremiumAmount(token)

			// then
			if err != nil || amount.String() != token {
				t.Fatalf(
					"NewAgentPremiumAmount(%q) = (%q, %v)",
					token,
					amount.String(),
					err,
				)
			}
		})
	}
}

func TestAgentPremiumAmount_RejectsNoncanonicalOrOutOfRangeTokens(t *testing.T) {
	rejected := []string{
		"", "0", "123", "10000000", "-0.0", "01.0", "1.230", "1.00E-7", "1e-7",
		"1.0E7", "1.20E+7", "1.0E+309", "1.0E-325", "NaN", "Infinity",
		"1.0000000000000000000", " 1.0",
	}
	for _, token := range rejected {
		t.Run(token, func(t *testing.T) {
			// when
			_, err := domain.NewAgentPremiumAmount(token)

			// then
			if !errors.Is(err, domain.ErrInvalidAgentPremium) {
				t.Fatalf("NewAgentPremiumAmount(%q) error = %v", token, err)
			}
		})
	}
}

func TestAgentPremiumAmount_FormatsCoefficientAndScaleWithBigDecimalRules(t *testing.T) {
	tests := []struct {
		name        string
		coefficient int64
		scale       int
		want        string
	}{
		{name: "small plain trailing zero", coefficient: 10, scale: 5, want: "0.00010"},
		{name: "plain integer", coefficient: 12345678, scale: 0, want: "12345678"},
		{name: "large scientific", coefficient: 10, scale: -6, want: "1.0E+7"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			amount, err := domain.NewAgentPremiumAmountFromCoefficientScale(
				test.coefficient,
				test.scale,
			)

			// then
			if err != nil || amount.String() != test.want {
				t.Fatalf(
					"NewAgentPremiumAmountFromCoefficientScale(%d, %d) = (%q, %v)",
					test.coefficient,
					test.scale,
					amount.String(),
					err,
				)
			}
		})
	}
}

func TestNewAgentSalesValue_RejectsNegativeCount(t *testing.T) {
	// given
	amount, err := domain.NewAgentPremiumAmount("0.0")
	if err != nil {
		t.Fatal(err)
	}

	// when
	_, err = domain.NewAgentSalesValue(-1, amount)

	// then
	if !errors.Is(err, domain.ErrNegativePolicyCount) {
		t.Fatalf("NewAgentSalesValue() error = %v", err)
	}
}
