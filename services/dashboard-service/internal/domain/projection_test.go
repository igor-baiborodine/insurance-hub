package domain

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestPremium_PreservesAcceptedReceivedToken(t *testing.T) {
	tests := []string{
		"0", "-0.0", "123.456700", "9007199254740993.1234567",
		"1.7976931348623157e308", "4.9406564584124654e-324",
	}
	for _, token := range tests {
		t.Run(token, func(t *testing.T) {
			// when
			premium, err := ParsePremium(token)
			encoded, marshalErr := json.Marshal(premium)

			// then
			if err != nil || marshalErr != nil || string(encoded) != token {
				t.Fatalf(
					"premium token = %q, %v / %v; want %q",
					encoded,
					err,
					marshalErr,
					token,
				)
			}
		})
	}
}

func TestPremium_RejectsInvalidOrOutOfRangeToken(t *testing.T) {
	tests := []string{
		"", "NaN", "+1", "01", "1.", ".1", "1.7976931348623159e308", "1e-400",
	}
	for _, token := range tests {
		t.Run(token, func(t *testing.T) {
			// when
			_, err := ParsePremium(token)

			// then
			if !errors.Is(err, ErrInvalidPremium) {
				t.Fatalf("ParsePremium(%q) error = %v", token, err)
			}
		})
	}
}
