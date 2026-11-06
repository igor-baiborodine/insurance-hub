package domain

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxAgentDecimalLength           = 64
	minAgentDecimalScale            = -400
	maxAgentDecimalScale            = 400
	minAgentDecimalAdjustedExponent = -324
	maxAgentDecimalAdjustedExponent = 308
)

var (
	ErrInvalidAgentPremium = errors.New("invalid agent premium amount")
	agentDecimalPattern    = regexp.MustCompile(
		`^(?:0\.0|-?(?:[1-9][0-9]*\.[0-9]+|0\.[0-9]*[1-9][0-9]*|[1-9]\.[0-9]+E[+-][0-9]+))$`,
	)
)

// AgentSelection distinguishes all agents from an exact agent, including an empty login.
type AgentSelection struct {
	present bool
	value   string
}

func AllAgents() AgentSelection { return AgentSelection{} }

func ExactAgent(value string) AgentSelection {
	return AgentSelection{present: true, value: value}
}

func (selection AgentSelection) Present() bool { return selection.present }

func (selection AgentSelection) Value() string { return selection.value }

// AgentPremiumAmount preserves one canonical BigDecimal.valueOf(double) token.
type AgentPremiumAmount struct{ value string }

func NewAgentPremiumAmount(value string) (AgentPremiumAmount, error) {
	if len(value) == 0 || len(value) > maxAgentDecimalLength ||
		!agentDecimalPattern.MatchString(value) || !validCanonicalAgentDecimal(value) {
		return AgentPremiumAmount{}, ErrInvalidAgentPremium
	}
	return AgentPremiumAmount{value: value}, nil
}

func (amount AgentPremiumAmount) String() string { return amount.value }

func validCanonicalAgentDecimal(value string) bool {
	mantissa, exponentText, hasExponent := strings.Cut(value, "E")
	exponent := int64(0)
	if hasExponent {
		parsed, err := strconv.ParseInt(exponentText, 10, 32)
		if err != nil {
			return false
		}
		exponent = parsed
	}
	negative := strings.HasPrefix(mantissa, "-")
	mantissa = strings.TrimPrefix(mantissa, "-")
	whole, fraction, present := strings.Cut(mantissa, ".")
	if !present {
		return false
	}
	if strings.HasSuffix(fraction, "0") && fraction != "0" {
		return false
	}
	digits := whole + fraction
	if negative {
		digits = "-" + digits
	}
	coefficient, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return false
	}
	scale := int64(len(fraction)) - exponent
	if scale < minAgentDecimalScale || scale > maxAgentDecimalScale {
		return false
	}
	precision := len(strings.TrimPrefix(digits, "-"))
	adjustedExponent := -int(scale) + precision - 1
	if adjustedExponent < minAgentDecimalAdjustedExponent ||
		adjustedExponent > maxAgentDecimalAdjustedExponent {
		return false
	}
	return formatCanonicalAgentDecimal(coefficient, int(scale)) == value
}

func formatCanonicalAgentDecimal(coefficient int64, scale int) string {
	negative := coefficient < 0
	digits := strings.TrimPrefix(strconv.FormatInt(coefficient, 10), "-")
	precision := len(digits)
	adjustedExponent := -scale + precision - 1
	var formatted string
	if scale >= 0 && adjustedExponent >= -6 {
		decimalPosition := precision - scale
		switch {
		case scale == 0:
			formatted = digits
		case decimalPosition > 0:
			formatted = digits[:decimalPosition] + "." + digits[decimalPosition:]
		default:
			formatted = "0." + strings.Repeat("0", -decimalPosition) + digits
		}
	} else {
		formatted = digits[:1] + "."
		if precision == 1 {
			formatted += "0"
		} else {
			formatted += digits[1:]
		}
		formatted += "E"
		if adjustedExponent >= 0 {
			formatted += "+"
		}
		formatted += strconv.Itoa(adjustedExponent)
	}
	if negative {
		return "-" + formatted
	}
	return formatted
}

// AgentSalesValue contains one returned agent bucket's count and exact premium token.
type AgentSalesValue struct {
	policiesCount int64
	premiumAmount AgentPremiumAmount
}

func NewAgentSalesValue(
	policiesCount int64,
	premiumAmount AgentPremiumAmount,
) (AgentSalesValue, error) {
	if policiesCount < 0 {
		return AgentSalesValue{}, ErrNegativePolicyCount
	}
	return AgentSalesValue{policiesCount: policiesCount, premiumAmount: premiumAmount}, nil
}

func (value AgentSalesValue) PoliciesCount() int64 { return value.policiesCount }

func (value AgentSalesValue) PremiumAmount() AgentPremiumAmount { return value.premiumAmount }

// AgentSales associates one returned Elasticsearch bucket with its exact agent key.
type AgentSales struct {
	AgentLogin string
	Value      AgentSalesValue
}
