// Package domain holds Policy business values and their invariants.
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"
)

// OfferStatusNew is the status of every newly created offer.
const OfferStatusNew = "NEW"

// storedTextLimit is the character limit of stored answers and cover codes.
const storedTextLimit = 255

var (
	// ErrInvalidOffer reports an offer request that cannot form a stored offer.
	ErrInvalidOffer = errors.New("invalid offer")
	// ErrInvalidQuote reports a quote whose amounts cannot be stored exactly.
	ErrInvalidQuote = errors.New("invalid quote")
)

// amountPattern accepts exact decimals that fit numeric(19, 2) without rounding.
var amountPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,16})(\.[0-9]{1,2})?$`)

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

// OfferRequest is what an agent asks to have priced and offered.
type OfferRequest struct {
	ProductCode    string
	PolicyFrom     time.Time
	PolicyTo       time.Time
	SelectedCovers []string
	Answers        []Answer
}

// Quote is Pricing's total and per-cover prices as exact decimal text.
type Quote struct {
	TotalPrice  string
	CoverPrices map[string]string
}

// Offer is a priced offer. Dates are calendar dates; amounts are exact decimal text.
type Offer struct {
	Number       string
	ProductCode  string
	PolicyFrom   time.Time
	PolicyTo     time.Time
	CreationDate time.Time
	Status       string
	Answers      map[string]string
	TotalPrice   string
	CoverPrices  map[string]string
}

// AnswerValues keys answer values by question code, as the offer stores them. A repeated
// question code is invalid.
func AnswerValues(answers []Answer) (map[string]string, error) {
	values := make(map[string]string, len(answers))
	for _, answer := range answers {
		if _, repeated := values[answer.QuestionCode]; repeated {
			return nil, fmt.Errorf("%w: repeated question code", ErrInvalidOffer)
		}
		values[answer.QuestionCode] = answer.Value
	}
	return values, nil
}

// NewOffer creates a NEW offer for the request, priced by quote, on the business date.
func NewOffer(
	number string,
	request OfferRequest,
	quote Quote,
	creationDate time.Time,
) (Offer, error) {
	answers, err := AnswerValues(request.Answers)
	if err != nil {
		return Offer{}, err
	}
	if !amountPattern.MatchString(quote.TotalPrice) {
		return Offer{}, fmt.Errorf("%w: total price", ErrInvalidQuote)
	}
	covers := make(map[string]string, len(quote.CoverPrices))
	for code, price := range quote.CoverPrices {
		if code == "" || utf8.RuneCountInString(code) > storedTextLimit ||
			!amountPattern.MatchString(price) {
			return Offer{}, fmt.Errorf("%w: cover price", ErrInvalidQuote)
		}
		covers[code] = price
	}
	return Offer{
		Number:       number,
		ProductCode:  request.ProductCode,
		PolicyFrom:   calendarDate(request.PolicyFrom),
		PolicyTo:     calendarDate(request.PolicyTo),
		CreationDate: calendarDate(creationDate),
		Status:       OfferStatusNew,
		Answers:      answers,
		TotalPrice:   quote.TotalPrice,
		CoverPrices:  covers,
	}, nil
}

// calendarDate keeps the date as observed in the value's own location.
func calendarDate(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
