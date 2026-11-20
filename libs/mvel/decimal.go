package mvel

import (
	"strings"

	shopdecimal "github.com/shopspring/decimal"
)

// Decimal is an immutable exact decimal value. Its zero value represents 0.
// The internal representation is deliberately hidden from callers.
type Decimal struct {
	value shopdecimal.Decimal
}

// ParseDecimal parses a signed base-10 decimal within the evaluator's fixed budgets.
func ParseDecimal(text string) (Decimal, error) {
	if text == "" {
		return Decimal{}, decimalError(ErrSyntax, "decimal is empty")
	}

	unsigned := text
	if unsigned[0] == '-' {
		unsigned = unsigned[1:]
		if unsigned == "" {
			return Decimal{}, decimalError(ErrSyntax, "decimal has no digits")
		}
	}
	parts := strings.Split(unsigned, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		return Decimal{}, decimalError(ErrSyntax, "decimal format is invalid")
	}
	for _, part := range parts {
		for _, character := range part {
			if character < '0' || character > '9' {
				return Decimal{}, decimalError(
					ErrSyntax,
					"decimal contains a non-digit",
				)
			}
		}
	}

	scale := 0
	if len(parts) == 2 {
		scale = len(parts[1])
	}
	digits := len(parts[0]) + scale
	integerDigits := len(strings.TrimLeft(parts[0], "0"))
	if integerDigits == 0 {
		integerDigits = 1
	}
	if digits > maxDecimalDigits || integerDigits > maxIntegerDigits ||
		scale > maxDecimalScale {
		return Decimal{}, decimalError(
			ErrLimit,
			"decimal exceeds precision, magnitude, or scale",
		)
	}

	parsed, err := shopdecimal.NewFromString(text)
	if err != nil {
		return Decimal{}, decimalError(ErrSyntax, "decimal format is invalid")
	}
	return checkedDecimal(parsed)
}

// String returns the exact decimal text with its represented scale.
func (decimal Decimal) String() string {
	return normalizedDecimal(decimal.value).StringFixed(int32(decimal.Scale()))
}

// Scale returns the number of represented fractional decimal digits.
func (decimal Decimal) Scale() int {
	exponent := decimal.value.Exponent()
	if exponent >= 0 {
		return 0
	}
	return int(-exponent)
}

func checkedDecimal(value shopdecimal.Decimal) (Decimal, error) {
	value = normalizedDecimal(value)
	coefficientDigits := len(strings.TrimPrefix(value.Coefficient().String(), "-"))
	integerDigits := coefficientDigits + int(value.Exponent())
	if integerDigits < 1 {
		integerDigits = 1
	}
	if coefficientDigits > maxDecimalDigits || integerDigits > maxIntegerDigits ||
		-int(value.Exponent()) > maxDecimalScale {
		return Decimal{}, decimalError(
			ErrLimit,
			"decimal exceeds precision, magnitude, or scale",
		)
	}
	return Decimal{value: value}, nil
}

func normalizedDecimal(value shopdecimal.Decimal) shopdecimal.Decimal {
	if value == (shopdecimal.Decimal{}) {
		return shopdecimal.Zero
	}
	return value
}

func add(left, right Decimal) (Decimal, error) {
	return checkedDecimal(
		normalizedDecimal(left.value).Add(normalizedDecimal(right.value)),
	)
}

func multiply(left, right Decimal) (Decimal, error) {
	return checkedDecimal(
		normalizedDecimal(left.value).Mul(normalizedDecimal(right.value)),
	)
}

func greaterThan(left, right Decimal) bool {
	return normalizedDecimal(left.value).GreaterThan(normalizedDecimal(right.value))
}
