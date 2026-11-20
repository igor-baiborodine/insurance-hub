// Package domain contains Pricing values independent of storage and transports.
package domain

import (
	"errors"

	shopdecimal "github.com/shopspring/decimal"
)

var ErrInvalidDecimal = errors.New("invalid decimal")

// Decimal is an exact base-10 value that retains its represented scale.
type Decimal struct{ value shopdecimal.Decimal }

func NewDecimal(text string) (Decimal, error) {
	value, err := shopdecimal.NewFromString(text)
	if err != nil {
		return Decimal{}, ErrInvalidDecimal
	}
	return Decimal{value: value}, nil
}

func (value Decimal) String() string {
	scale := -value.value.Exponent()
	if scale < 0 {
		scale = 0
	}
	return value.value.StringFixed(scale)
}

func (value Decimal) Add(other Decimal) Decimal {
	return Decimal{value: value.value.Add(other.value)}
}

func (value Decimal) Multiply(other Decimal) Decimal {
	return Decimal{value: value.value.Mul(other.value)}
}

// RoundHalfUp rounds ties away from zero, matching Java RoundingMode.HALF_UP.
func (value Decimal) RoundHalfUp(scale int32) Decimal {
	return Decimal{value: value.value.Round(scale)}
}

type Tariff struct {
	Code        string
	BaseRules   []BaseRule
	MarkupRules []MarkupRule
}

type BaseRule struct {
	CoverCode string
	Condition string
	Formula   string
}

type MarkupRule struct {
	Kind      string
	Condition string
	Factor    Decimal
}

type AnswerKind uint8

const (
	TextAnswer AnswerKind = iota + 1
	NumericAnswer
)

type Answer struct {
	QuestionCode string
	Kind         AnswerKind
	Text         string
	Number       Decimal
}

type Quote struct {
	Total  Decimal
	Covers map[string]Decimal
}
