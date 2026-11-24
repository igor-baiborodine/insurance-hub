// Package domain holds Policy business values and their invariants.
package domain

// AnswerKind identifies the legacy questionnaire answer variant.
type AnswerKind int

const (
	ChoiceAnswer AnswerKind = iota + 1
	TextAnswer
	NumericAnswer
)

// Answer is one questionnaire answer. A numeric value keeps its exact decimal text.
type Answer struct {
	QuestionCode string
	Kind         AnswerKind
	Value        string
}

// Quote is Pricing's total and per-cover prices as exact decimal text.
type Quote struct {
	TotalPrice  string
	CoverPrices map[string]string
}
