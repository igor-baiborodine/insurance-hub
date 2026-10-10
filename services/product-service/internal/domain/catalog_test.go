package domain

import (
	"errors"
	"testing"
)

func TestDecimal_PreservesPresencePrecisionAndScale(t *testing.T) {
	// given
	valid := []string{
		"0",
		"0.00",
		"12.3400",
		"12345678901234567890.123456789",
		"-0.000",
		"1e+20",
	}
	invalid := []string{"", "+1", "01", "1.", ".1", "NaN", "Infinity", " 1", "1\n", "1e"}

	// when
	for _, token := range valid {
		decimal, err := NewDecimal(token)
		// then
		if err != nil || decimal.String() != token {
			t.Errorf("decimal %q: value=%q error=%v", token, decimal.String(), err)
		}
	}
	for _, token := range invalid {
		_, err := NewDecimal(token)
		// then
		if !errors.Is(err, ErrInvalidDecimal) {
			t.Errorf("decimal %q: expected invalid decimal, got %v", token, err)
		}
	}
	zero, _ := NewDecimal("0.00")
	if (&Cover{}).SumInsured != nil ||
		(&Cover{SumInsured: &zero}).SumInsured.String() != "0.00" {
		t.Error("absent and present zero amounts must remain distinct")
	}
}

func TestQuestion_RequiresOneKnownValueVariant(t *testing.T) {
	// given
	variants := []QuestionKind{
		ChoiceQuestion{Choices: []Choice{{Code: "first"}, {Code: "second"}}},
		DateQuestion{},
		NumericQuestion{},
	}

	// when
	for _, variant := range variants {
		question, err := NewQuestion("question", 0, "text", variant)
		// then
		if err != nil {
			t.Errorf("construct %T: %v", variant, err)
			continue
		}
		if choice, ok := question.Kind().(ChoiceQuestion); ok {
			if len(choice.Choices) != 2 || choice.Choices[0].Code != "first" ||
				choice.Choices[1].Code != "second" {
				t.Error("choice order changed")
			}
		}
	}
	if _, err := NewQuestion(
		"question",
		0,
		"text",
		nil,
	); !errors.Is(
		err,
		ErrInvalidQuestionKind,
	) {
		t.Errorf("missing variant: %v", err)
	}
	var typedNil *ChoiceQuestion
	if _, err := NewQuestion(
		"question",
		0,
		"text",
		typedNil,
	); !errors.Is(
		err,
		ErrInvalidQuestionKind,
	) {
		t.Errorf("pointer variant: %v", err)
	}
}
