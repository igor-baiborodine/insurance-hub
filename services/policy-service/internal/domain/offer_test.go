package domain_test

import (
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

func TestNewOffer_CreatesNewOfferOnBusinessDate(t *testing.T) {
	// given
	businessZone := time.FixedZone("UTC+1", 60*60)
	request := domain.OfferRequest{
		ProductCode: "TRI",
		PolicyFrom:  time.Date(2017, 4, 16, 0, 0, 0, 0, time.UTC),
		PolicyTo:    time.Date(2018, 4, 15, 0, 0, 0, 0, time.UTC),
		Answers: []domain.Answer{
			{QuestionCode: "DESTINATION", Kind: domain.ChoiceAnswer, Value: "EUR"},
			{QuestionCode: "NUM_OF_ADULTS", Kind: domain.NumericAnswer, Value: "1.50"},
		},
	}
	quote := domain.Quote{
		TotalPrice:  "123.45",
		CoverPrices: map[string]string{"C1": "100.10", "C2": "23.35"},
	}
	// 00:30 on 24 November in the business zone is still 23 November in UTC.
	businessTime := time.Date(2026, 11, 24, 0, 30, 0, 0, businessZone)

	// when
	offer, err := domain.NewOffer("offer-1", request, quote, businessTime)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if offer.Number != "offer-1" || offer.ProductCode != "TRI" || offer.Status != "NEW" ||
		offer.TotalPrice != "123.45" ||
		offer.CreationDate != time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC) ||
		offer.PolicyFrom != time.Date(2017, 4, 16, 0, 0, 0, 0, time.UTC) ||
		!maps.Equal(offer.CoverPrices, quote.CoverPrices) ||
		!maps.Equal(
			offer.Answers,
			map[string]string{"DESTINATION": "EUR", "NUM_OF_ADULTS": "1.50"},
		) {
		t.Errorf("offer = %+v", offer)
	}
}

func TestNewOffer_RejectsQuotesThatCannotBeStoredExactly(t *testing.T) {
	for _, test := range []struct {
		name  string
		quote domain.Quote
	}{
		{"three decimals", domain.Quote{TotalPrice: "1.005"}},
		{"exponent", domain.Quote{TotalPrice: "1e2"}},
		{"eighteen integer digits", domain.Quote{TotalPrice: "123456789012345678"}},
		{"empty total", domain.Quote{}},
		{"invalid cover price", domain.Quote{TotalPrice: "1", CoverPrices: map[string]string{"C1": "1.5x"}}},
		{"empty cover code", domain.Quote{TotalPrice: "1", CoverPrices: map[string]string{"": "1"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			_, err := domain.NewOffer(
				"offer-1",
				domain.OfferRequest{},
				test.quote,
				time.Now(),
			)

			// then
			if !errors.Is(err, domain.ErrInvalidQuote) {
				t.Errorf("error = %v, want invalid quote", err)
			}
		})
	}
}

func TestAnswerValues_RejectsRepeatedQuestionCode(t *testing.T) {
	// given
	answers := []domain.Answer{
		{QuestionCode: "DESTINATION", Kind: domain.ChoiceAnswer, Value: "EUR"},
		{QuestionCode: "DESTINATION", Kind: domain.TextAnswer, Value: "PLN"},
	}

	// when
	_, err := domain.AnswerValues(answers)

	// then
	if !errors.Is(err, domain.ErrInvalidOffer) {
		t.Errorf("error = %v, want invalid offer", err)
	}
}
