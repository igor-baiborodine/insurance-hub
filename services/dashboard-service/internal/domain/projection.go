package domain

import (
	"errors"
	"math"
	"regexp"
	"strconv"
)

var (
	ErrInvalidPremium = errors.New("invalid policy premium")
	jsonNumberPattern = regexp.MustCompile(
		`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`,
	)
)

// Premium preserves the accepted received JSON number token without a binary-float round trip.
type Premium struct{ token string }

func ParsePremium(token string) (Premium, error) {
	if !jsonNumberPattern.MatchString(token) {
		return Premium{}, ErrInvalidPremium
	}
	numeric, err := strconv.ParseFloat(token, 64)
	if err != nil || math.IsInf(numeric, 0) || (numeric == 0 && !isZeroToken(token)) {
		return Premium{}, ErrInvalidPremium
	}
	return Premium{token: token}, nil
}

func (premium Premium) String() string { return premium.token }

func (premium Premium) MarshalJSON() ([]byte, error) {
	if !jsonNumberPattern.MatchString(premium.token) {
		return nil, ErrInvalidPremium
	}
	return []byte(premium.token), nil
}

func isZeroToken(token string) bool {
	for _, character := range token {
		if character >= '1' && character <= '9' {
			return false
		}
	}
	return true
}

// PolicyProjection is the complete seven-field Dashboard registration read model.
type PolicyProjection struct {
	Number       string
	From         *Date
	To           *Date
	PolicyHolder *string
	ProductCode  *string
	TotalPremium *Premium
	AgentLogin   *string
}
