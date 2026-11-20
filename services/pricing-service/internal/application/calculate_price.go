// Package application owns Pricing calculations and their tariff reader port.
package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/mvel"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/domain"
)

var (
	ErrReaderRequired    = errors.New("tariff reader is required")
	ErrInvalidRequest    = errors.New("invalid pricing request")
	ErrCalculation       = errors.New("pricing calculation failed")
	ErrTariffNotFound    = errors.New("tariff not found")
	ErrTariffAmbiguous   = errors.New("ambiguous stored tariff")
	ErrTariffInvalid     = errors.New("invalid stored tariff")
	ErrReaderUnavailable = errors.New("tariff reader unavailable")
)

const percentMarkup = "perc_markup"

var evaluatorIdentifiers = map[string]struct{}{
	"AREA": {}, "NUM_OF_ADULTS": {}, "NUM_OF_CHILDREN": {}, "NUM_OF_CLAIM": {},
	"DESTINATION": {}, "FLOOD": {}, "TYP": {},
}

type TariffReader interface {
	GetTariff(context.Context, string) (tariff domain.Tariff, found bool, err error)
}

type CalculatePriceRequest struct {
	ProductCode    string
	PolicyFrom     time.Time
	PolicyTo       time.Time
	SelectedCovers []string
	Answers        []domain.Answer
}

type CalculatePrice struct{ reader TariffReader }

func NewCalculatePrice(reader TariffReader) (CalculatePrice, error) {
	if reader == nil {
		return CalculatePrice{}, ErrReaderRequired
	}
	return CalculatePrice{reader: reader}, nil
}

func (useCase CalculatePrice) Execute(
	ctx context.Context,
	request CalculatePriceRequest,
) (domain.Quote, error) {
	if err := ctx.Err(); err != nil {
		return domain.Quote{}, err
	}
	if request.ProductCode == "" {
		return domain.Quote{}, ErrInvalidRequest
	}

	tariff, found, err := useCase.reader.GetTariff(ctx, request.ProductCode)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("read tariff: %w", err)
	}
	if !found {
		return domain.Quote{}, errors.Join(ErrCalculation, ErrTariffNotFound)
	}
	if err := ctx.Err(); err != nil {
		return domain.Quote{}, err
	}

	quote, err := calculate(request, tariff)
	if err != nil {
		return domain.Quote{}, fmt.Errorf("%w: %w", ErrCalculation, err)
	}
	return quote, nil
}

type bindingKind uint8

const (
	dateBinding bindingKind = iota + 1
	coverBinding
	textBinding
	numberBinding
)

type binding struct {
	kind   bindingKind
	text   string
	number domain.Decimal
}

func calculate(request CalculatePriceRequest, tariff domain.Tariff) (domain.Quote, error) {
	context, covers, err := calculationContext(request)
	if err != nil {
		return domain.Quote{}, err
	}

	prices := make(map[string]domain.Decimal, len(covers))
	for _, cover := range covers {
		price, err := calculateBase(cover, tariff.BaseRules, context)
		if err != nil {
			return domain.Quote{}, err
		}
		prices[cover] = price
	}

	for _, rule := range tariff.MarkupRules {
		if rule.Kind != percentMarkup {
			return domain.Quote{}, errors.New("unsupported markup rule")
		}
		applies, err := predicateApplies(rule.Condition, context)
		if err != nil {
			return domain.Quote{}, err
		}
		if !applies {
			continue
		}
		for _, cover := range covers {
			prices[cover] = prices[cover].Multiply(rule.Factor).RoundHalfUp(2)
		}
	}

	total, err := domain.NewDecimal("0")
	if err != nil {
		return domain.Quote{}, err
	}
	for _, cover := range covers {
		total = total.Add(prices[cover])
	}
	return domain.Quote{Total: total, Covers: prices}, nil
}

func calculationContext(
	request CalculatePriceRequest,
) (map[string]binding, []string, error) {
	values := map[string]binding{
		"policyFrom": {kind: dateBinding, text: request.PolicyFrom.Format(time.DateOnly)},
		"policyTo":   {kind: dateBinding, text: request.PolicyTo.Format(time.DateOnly)},
	}
	covers := make([]string, 0, len(request.SelectedCovers))
	seenCovers := make(map[string]struct{}, len(request.SelectedCovers))
	for _, cover := range request.SelectedCovers {
		values[cover] = binding{kind: coverBinding}
		if _, seen := seenCovers[cover]; seen {
			continue
		}
		seenCovers[cover] = struct{}{}
		covers = append(covers, cover)
	}

	seenAnswers := make(map[string]struct{}, len(request.Answers))
	for _, answer := range request.Answers {
		if _, duplicate := seenAnswers[answer.QuestionCode]; duplicate {
			return nil, nil, errors.New("duplicate answer code")
		}
		seenAnswers[answer.QuestionCode] = struct{}{}
		switch answer.Kind {
		case domain.TextAnswer:
			values[answer.QuestionCode] = binding{kind: textBinding, text: answer.Text}
		case domain.NumericAnswer:
			values[answer.QuestionCode] = binding{
				kind: numberBinding, number: answer.Number,
			}
		default:
			return nil, nil, errors.New("unsupported answer kind")
		}
	}
	return values, covers, nil
}

func calculateBase(
	cover string,
	rules []domain.BaseRule,
	context map[string]binding,
) (domain.Decimal, error) {
	for _, rule := range rules {
		if rule.CoverCode != cover {
			continue
		}
		applies, err := predicateApplies(rule.Condition, context)
		if err != nil {
			return domain.Decimal{}, err
		}
		if !applies {
			continue
		}
		expression, err := mvel.CompileDecimal(rule.Formula)
		if err != nil {
			return domain.Decimal{}, fmt.Errorf("compile base formula: %w", err)
		}
		bindings, err := expressionBindings(rule.Formula, context)
		if err != nil {
			return domain.Decimal{}, err
		}
		value, err := expression.Evaluate(bindings)
		if err != nil {
			return domain.Decimal{}, fmt.Errorf("evaluate base formula: %w", err)
		}
		price, err := domain.NewDecimal(value.String())
		if err != nil {
			return domain.Decimal{}, err
		}
		// Java evaluates base formulas in binary floating point, so its exact results carry no
		// trailing fractional zeros: 1 * 26.00B is 26 and 100 * 1.25B is 125.
		return price.TrimTrailingZeros(), nil
	}
	return domain.Decimal{}, errors.New("selected cover has no matching base rule")
}

func predicateApplies(source *string, context map[string]binding) (bool, error) {
	if source == nil || *source == "" {
		return true, nil
	}
	predicate, err := mvel.CompilePredicate(*source)
	if err != nil {
		return false, fmt.Errorf("compile predicate: %w", err)
	}
	bindings, err := expressionBindings(*source, context)
	if err != nil {
		return false, err
	}
	applies, err := predicate.Evaluate(bindings)
	if err != nil {
		return false, fmt.Errorf("evaluate predicate: %w", err)
	}
	return applies, nil
}

func expressionBindings(source string, context map[string]binding) (mvel.Bindings, error) {
	bindings := make(mvel.Bindings)
	for _, identifier := range expressionIdentifiers(source) {
		value, present := context[identifier]
		if !present {
			continue
		}
		switch value.kind {
		case textBinding:
			bindings[identifier] = mvel.Text(value.text)
		case numberBinding:
			decimal, err := mvel.ParseDecimal(value.number.String())
			if err != nil {
				return nil, fmt.Errorf("convert numeric binding: %w", err)
			}
			bindings[identifier] = mvel.Number(decimal)
		default:
			return nil, errors.New("expression references an incompatible binding")
		}
	}
	return bindings, nil
}

func expressionIdentifiers(source string) []string {
	identifiers := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	for offset := 0; offset < len(source); {
		if source[offset] == '\'' {
			offset++
			for offset < len(source) && source[offset] != '\'' {
				offset++
			}
			if offset < len(source) {
				offset++
			}
			continue
		}
		if !identifierStart(source[offset]) {
			offset++
			continue
		}
		start := offset
		offset++
		for offset < len(source) && identifierPart(source[offset]) {
			offset++
		}
		identifier := source[start:offset]
		if _, allowed := evaluatorIdentifiers[identifier]; !allowed {
			continue
		}
		if _, duplicate := seen[identifier]; duplicate {
			continue
		}
		seen[identifier] = struct{}{}
		identifiers = append(identifiers, identifier)
	}
	return identifiers
}

func identifierStart(character byte) bool {
	return character == '_' || character >= 'A' && character <= 'Z' ||
		character >= 'a' && character <= 'z'
}

func identifierPart(character byte) bool {
	return identifierStart(character) || character >= '0' && character <= '9'
}
