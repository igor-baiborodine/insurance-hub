package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/domain"
)

type tariffReaderFake struct {
	tariff domain.Tariff
	found  bool
	err    error
	ctx    context.Context
	code   string
	runs   int
}

func (reader *tariffReaderFake) GetTariff(
	ctx context.Context,
	code string,
) (domain.Tariff, bool, error) {
	reader.ctx = ctx
	reader.code = code
	reader.runs++
	return reader.tariff, reader.found, reader.err
}

func TestCalculatePrice_PreservesBindingPrecedenceAndFirstMatch(t *testing.T) {
	// given
	reader := &tariffReaderFake{found: true, tariff: domain.Tariff{
		Code: "TEST",
		BaseRules: []domain.BaseRule{
			{CoverCode: "AREA", Formula: "AREA * 1B"},
			{CoverCode: "C1", Formula: "10B"},
			{CoverCode: "C1", Formula: "99B"},
		},
	}}
	useCase, err := NewCalculatePrice(reader)
	if err != nil {
		t.Fatal(err)
	}
	request := CalculatePriceRequest{
		ProductCode:    "TEST",
		PolicyFrom:     time.Date(2017, 4, 16, 0, 0, 0, 0, time.UTC),
		PolicyTo:       time.Date(2018, 4, 15, 0, 0, 0, 0, time.UTC),
		SelectedCovers: []string{"AREA", "C1", "C1"},
		Answers: []domain.Answer{
			{QuestionCode: "AREA", Kind: domain.NumericAnswer, Number: decimal(t, "2")},
			{QuestionCode: "UNRELATED", Kind: domain.TextAnswer, Text: "ignored"},
			{QuestionCode: "B", Kind: domain.NumericAnswer, Number: decimal(t, "7")},
		},
	}

	// when
	quote, err := useCase.Execute(context.Background(), request)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if reader.code != "TEST" || reader.runs != 1 {
		t.Errorf("reader: code=%q runs=%d", reader.code, reader.runs)
	}
	assertQuote(t, quote, "12", map[string]string{"AREA": "2", "C1": "10"})
}

func TestCalculatePrice_AppliesOrderedMarkupAndHalfUpRounding(t *testing.T) {
	// given
	tests := []struct {
		name    string
		tariff  domain.Tariff
		request CalculatePriceRequest
		total   string
		cover   string
		price   string
	}{
		{
			name: "HSI accepted order",
			tariff: domain.Tariff{
				BaseRules: []domain.BaseRule{
					{CoverCode: "C2", Formula: "AREA * 0.25B"},
				},
				MarkupRules: []domain.MarkupRule{
					{
						Kind:      percentMarkup,
						Condition: "FLOOD == 'YES'",
						Factor:    decimal(t, "1.50"),
					},
					{
						Kind:      percentMarkup,
						Condition: "NUM_OF_CLAIM > 1",
						Factor:    decimal(t, "1.25"),
					},
				},
			},
			request: request("C2",
				numberAnswer(t, "AREA", "1"),
				textAnswer("FLOOD", "YES"),
				numberAnswer(t, "NUM_OF_CLAIM", "2"),
			),
			total: "0.48", cover: "C2", price: "0.48",
		},
		{
			name: "HSI reverse-order witness",
			tariff: domain.Tariff{
				BaseRules: []domain.BaseRule{
					{CoverCode: "C2", Formula: "AREA * 0.25B"},
				},
				MarkupRules: []domain.MarkupRule{
					{
						Kind:      percentMarkup,
						Condition: "NUM_OF_CLAIM > 1",
						Factor:    decimal(t, "1.25"),
					},
					{
						Kind:      percentMarkup,
						Condition: "FLOOD == 'YES'",
						Factor:    decimal(t, "1.50"),
					},
				},
			},
			request: request("C2",
				numberAnswer(t, "AREA", "1"),
				textAnswer("FLOOD", "YES"),
				numberAnswer(t, "NUM_OF_CLAIM", "2"),
			),
			total: "0.47", cover: "C2", price: "0.47",
		},
		{
			name: "FAI sequential factors",
			tariff: domain.Tariff{
				BaseRules: []domain.BaseRule{{CoverCode: "C1", Formula: "10B"}},
				MarkupRules: []domain.MarkupRule{
					{
						Kind:      percentMarkup,
						Condition: "FLOOD == 'YES'",
						Factor:    decimal(t, "1.50"),
					},
					{
						Kind:      percentMarkup,
						Condition: "NUM_OF_CLAIM > 2",
						Factor:    decimal(t, "2.00"),
					},
				},
			},
			request: request("C1",
				textAnswer("FLOOD", "YES"),
				numberAnswer(t, "NUM_OF_CLAIM", "3"),
			),
			total: "30.00", cover: "C1", price: "30.00",
		},
		{
			name: "CAR below threshold",
			tariff: domain.Tariff{
				BaseRules: []domain.BaseRule{{CoverCode: "C1", Formula: "100B"}},
				MarkupRules: []domain.MarkupRule{
					{
						Kind:      percentMarkup,
						Condition: "NUM_OF_CLAIM > 2",
						Factor:    decimal(t, "50.00"),
					},
				},
			},
			request: request("C1", numberAnswer(t, "NUM_OF_CLAIM", "2")),
			total:   "100", cover: "C1", price: "100",
		},
		{
			name: "CAR factor fifty",
			tariff: domain.Tariff{
				BaseRules: []domain.BaseRule{{CoverCode: "C1", Formula: "100B"}},
				MarkupRules: []domain.MarkupRule{
					{
						Kind:      percentMarkup,
						Condition: "NUM_OF_CLAIM > 2",
						Factor:    decimal(t, "50.00"),
					},
				},
			},
			request: request("C1", numberAnswer(t, "NUM_OF_CLAIM", "3")),
			total:   "5000.00", cover: "C1", price: "5000.00",
		},
		{
			name: "negative HALF_UP tie",
			tariff: domain.Tariff{
				BaseRules: []domain.BaseRule{
					{CoverCode: "C1", Formula: "AREA * 1B"},
				},
				MarkupRules: []domain.MarkupRule{
					{Kind: percentMarkup, Factor: decimal(t, "1.00")},
				},
			},
			request: request("C1", numberAnswer(t, "AREA", "-0.015")),
			total:   "-0.02", cover: "C1", price: "-0.02",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &tariffReaderFake{found: true, tariff: test.tariff}
			useCase, err := NewCalculatePrice(reader)
			if err != nil {
				t.Fatal(err)
			}

			// when
			quote, err := useCase.Execute(context.Background(), test.request)
			// then
			if err != nil {
				t.Fatal(err)
			}
			assertQuote(t, quote, test.total, map[string]string{test.cover: test.price})
		})
	}
}

func TestCalculatePrice_PreservesCancellationAndReaderFailures(t *testing.T) {
	// given
	storageFailure := errors.New("storage failure")
	reader := &tariffReaderFake{err: errors.Join(ErrReaderUnavailable, storageFailure)}
	useCase, err := NewCalculatePrice(reader)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	// when / then
	_, err = useCase.Execute(ctx, request("C1"))
	if !errors.Is(err, ErrReaderUnavailable) || !errors.Is(err, storageFailure) {
		t.Errorf("reader failure classification: %v", err)
	}
	if reader.ctx != ctx || reader.runs != 1 {
		t.Errorf(
			"reader context or runs changed: context=%v runs=%d",
			reader.ctx,
			reader.runs,
		)
	}
	cancel()
	_, err = useCase.Execute(ctx, request("C1"))
	if !errors.Is(err, context.Canceled) || reader.runs != 1 {
		t.Errorf("canceled request: error=%v runs=%d", err, reader.runs)
	}
}

func TestCalculatePrice_RejectsIncompleteCalculation(t *testing.T) {
	validTariff := domain.Tariff{
		BaseRules: []domain.BaseRule{{CoverCode: "C1", Formula: "AREA * 1B"}},
	}
	tests := []struct {
		name    string
		reader  *tariffReaderFake
		request CalculatePriceRequest
		target  error
	}{
		{
			name:   "duplicate answer",
			reader: &tariffReaderFake{found: true, tariff: validTariff},
			request: request("C1",
				numberAnswer(t, "AREA", "1"), numberAnswer(t, "AREA", "2"),
			),
			target: ErrCalculation,
		},
		{
			name:    "missing referenced answer",
			reader:  &tariffReaderFake{found: true, tariff: validTariff},
			request: request("C1"),
			target:  ErrCalculation,
		},
		{
			name:    "mistyped referenced answer",
			reader:  &tariffReaderFake{found: true, tariff: validTariff},
			request: request("C1", textAnswer("AREA", "one")),
			target:  ErrCalculation,
		},
		{
			name:    "unmatched cover",
			reader:  &tariffReaderFake{found: true, tariff: validTariff},
			request: request("C1", "UNKNOWN", numberAnswer(t, "AREA", "1")),
			target:  ErrCalculation,
		},
		{
			name:    "unknown tariff",
			reader:  &tariffReaderFake{},
			request: request("C1", numberAnswer(t, "AREA", "1")),
			target:  ErrTariffNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useCase, err := NewCalculatePrice(test.reader)
			if err != nil {
				t.Fatal(err)
			}

			// when
			quote, err := useCase.Execute(context.Background(), test.request)

			// then
			if !errors.Is(err, test.target) {
				t.Errorf("error=%v, want classification %v", err, test.target)
			}
			if quote.Covers != nil || quote.Total.String() != "0" {
				t.Errorf("rejected quote must be empty: %+v", quote)
			}
		})
	}
}

func request(cover string, values ...any) CalculatePriceRequest {
	covers := []string{cover}
	answers := make([]domain.Answer, 0, len(values))
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			covers = append(covers, typed)
		case domain.Answer:
			answers = append(answers, typed)
		}
	}
	return CalculatePriceRequest{
		ProductCode: "TEST", SelectedCovers: covers, Answers: answers,
	}
}

func numberAnswer(t *testing.T, code, value string) domain.Answer {
	t.Helper()
	return domain.Answer{
		QuestionCode: code,
		Kind:         domain.NumericAnswer,
		Number:       decimal(t, value),
	}
}

func textAnswer(code, value string) domain.Answer {
	return domain.Answer{QuestionCode: code, Kind: domain.TextAnswer, Text: value}
}

func decimal(t *testing.T, value string) domain.Decimal {
	t.Helper()
	parsed, err := domain.NewDecimal(value)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", value, err)
	}
	return parsed
}

func assertQuote(
	t *testing.T,
	quote domain.Quote,
	total string,
	covers map[string]string,
) {
	t.Helper()
	if quote.Total.String() != total || len(quote.Covers) != len(covers) {
		t.Errorf("quote=%+v total=%s", quote, quote.Total.String())
		return
	}
	for code, expected := range covers {
		if quote.Covers[code].String() != expected {
			t.Errorf(
				"cover %s=%s, want %s",
				code,
				quote.Covers[code].String(),
				expected,
			)
		}
	}
}
