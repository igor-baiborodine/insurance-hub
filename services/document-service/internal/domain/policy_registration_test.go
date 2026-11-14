package domain_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
)

func TestNewPolicyRegistration_PreservesAcceptedValues(t *testing.T) {
	// given
	input := domain.PolicyRegistrationInput{
		Number:       "SYN-363",
		From:         [3]int{2026, 2, 3},
		To:           [3]int{2027, 2, 2},
		PolicyHolder: "Synthetic Holder",
		ProductCode:  "HSI",
		TotalPremium: "9007199254740993.1234567",
	}

	// when
	registration, err := domain.NewPolicyRegistration(input)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if registration.Number() != input.Number || registration.From().Parts() != input.From ||
		registration.To().Parts() != input.To ||
		registration.PolicyHolder() != input.PolicyHolder ||
		registration.ProductCode() != input.ProductCode ||
		registration.TotalPremium() != input.TotalPremium || registration.AgentLogin() != nil {
		t.Fatalf("registration did not preserve input: %#v", registration)
	}
}

func TestNewPolicyRegistration_RejectsInvalidValues(t *testing.T) {
	valid := domain.PolicyRegistrationInput{
		Number:       "SYN-363",
		From:         [3]int{2026, 2, 3},
		To:           [3]int{2027, 2, 2},
		PolicyHolder: "Synthetic Holder",
		ProductCode:  "HSI",
		TotalPremium: "1.00",
	}
	invalidUTF8 := string([]byte{utf8.RuneSelf, 0})
	oversized := strings.Repeat("x", 4*1024+1)
	tests := []struct {
		name   string
		mutate func(*domain.PolicyRegistrationInput)
	}{
		{
			"invalid policy number",
			func(input *domain.PolicyRegistrationInput) { input.Number = "bad/key" },
		},
		{
			"invalid start date",
			func(input *domain.PolicyRegistrationInput) { input.From = [3]int{2026, 2, 29} },
		},
		{
			"reversed dates",
			func(input *domain.PolicyRegistrationInput) { input.To = [3]int{2025, 2, 2} },
		},
		{
			"oversized holder",
			func(input *domain.PolicyRegistrationInput) { input.PolicyHolder = oversized },
		},
		{
			"invalid product encoding",
			func(input *domain.PolicyRegistrationInput) { input.ProductCode = invalidUTF8 },
		},
		{
			"quoted premium",
			func(input *domain.PolicyRegistrationInput) { input.TotalPremium = `"1.00"` },
		},
		{
			"nonfinite premium",
			func(input *domain.PolicyRegistrationInput) { input.TotalPremium = "NaN" },
		},
		{
			"leading-zero premium",
			func(input *domain.PolicyRegistrationInput) { input.TotalPremium = "01.00" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			input := valid
			test.mutate(&input)

			// when
			_, err := domain.NewPolicyRegistration(input)

			// then
			if err == nil {
				t.Fatal("invalid registration accepted")
			}
		})
	}
}
