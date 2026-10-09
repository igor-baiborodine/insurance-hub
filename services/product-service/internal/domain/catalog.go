// Package domain contains Product catalog values independent of storage and transports.
package domain

import (
	"errors"
	"regexp"
)

var (
	ErrInvalidDecimal      = errors.New("invalid decimal")
	ErrInvalidQuestionKind = errors.New("invalid question kind")
	decimalPattern         = regexp.MustCompile(
		`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`,
	)
)

// Decimal preserves the original finite JSON number token, including trailing scale.
// Its zero value is invalid; use NewDecimal for a present amount.
type Decimal struct{ lexeme string }

func NewDecimal(lexeme string) (Decimal, error) {
	if !decimalPattern.MatchString(lexeme) {
		return Decimal{}, ErrInvalidDecimal
	}
	return Decimal{lexeme: lexeme}, nil
}

func (d Decimal) String() string { return d.lexeme }

type Product struct {
	Code               string
	Name               string
	Image              string
	Description        string
	Covers             []Cover
	Questions          []Question
	MaxNumberOfInsured int32
	Icon               string
}

type Cover struct {
	Code        string
	Name        string
	Description string
	Optional    bool
	// Nil means absent; a present zero retains its original scale.
	SumInsured *Decimal
}

type Choice struct {
	Code  string
	Label string
}

// QuestionKind is closed to the three stored Product question variants.
type QuestionKind interface{ questionKind() }

type (
	ChoiceQuestion  struct{ Choices []Choice }
	DateQuestion    struct{}
	NumericQuestion struct{}
)

func (ChoiceQuestion) questionKind()  {}
func (DateQuestion) questionKind()    {}
func (NumericQuestion) questionKind() {}

type Question struct {
	Code  string
	Index int32
	Text  string
	kind  QuestionKind
}

func NewQuestion(code string, index int32, text string, kind QuestionKind) (Question, error) {
	switch kind.(type) {
	case ChoiceQuestion, DateQuestion, NumericQuestion:
		return Question{Code: code, Index: index, Text: text, kind: kind}, nil
	default:
		return Question{}, ErrInvalidQuestionKind
	}
}

func (q Question) Kind() QuestionKind { return q.kind }
