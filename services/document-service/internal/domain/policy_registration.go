package domain

import (
	"errors"
	"regexp"
	"time"
	"unicode/utf8"
)

const (
	maximumPolicyNumberBytes = 255
	maximumPolicyTextBytes   = 4 * 1024
	maximumPremiumBytes      = 256
)

var (
	policyNumberPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)
	premiumPattern      = regexp.MustCompile(
		`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`,
	)
)

// PolicyDate is one validated calendar date represented by the legacy three-integer wire shape.
type PolicyDate struct {
	year  int
	month int
	day   int
}

// PolicyRegistrationInput contains normalized values decoded from a policy registration event.
type PolicyRegistrationInput struct {
	Number       string
	From         [3]int
	To           [3]int
	PolicyHolder string
	ProductCode  string
	TotalPremium string
	AgentLogin   *string
}

// PolicyRegistration contains the validated data required to generate a policy document.
type PolicyRegistration struct {
	number       string
	from         PolicyDate
	to           PolicyDate
	policyHolder string
	productCode  string
	totalPremium string
	agentLogin   *string
}

// NewPolicyRegistration validates the normalized legacy-compatible event values.
func NewPolicyRegistration(input PolicyRegistrationInput) (PolicyRegistration, error) {
	if len(input.Number) == 0 || len(input.Number) > maximumPolicyNumberBytes ||
		!policyNumberPattern.MatchString(input.Number) {
		return PolicyRegistration{}, errors.New("invalid policy number")
	}
	from, err := newPolicyDate(input.From)
	if err != nil {
		return PolicyRegistration{}, errors.New("invalid policy start date")
	}
	to, err := newPolicyDate(input.To)
	if err != nil {
		return PolicyRegistration{}, errors.New("invalid policy end date")
	}
	if from.time().After(to.time()) {
		return PolicyRegistration{}, errors.New("policy start date exceeds end date")
	}
	if !validPolicyText(input.PolicyHolder) {
		return PolicyRegistration{}, errors.New("invalid policy holder")
	}
	if !validPolicyText(input.ProductCode) {
		return PolicyRegistration{}, errors.New("invalid product code")
	}
	if len(input.TotalPremium) == 0 || len(input.TotalPremium) > maximumPremiumBytes ||
		!premiumPattern.MatchString(input.TotalPremium) {
		return PolicyRegistration{}, errors.New("invalid total premium")
	}
	if input.AgentLogin != nil && !validPolicyText(*input.AgentLogin) {
		return PolicyRegistration{}, errors.New("invalid agent login")
	}
	var agentLogin *string
	if input.AgentLogin != nil {
		value := *input.AgentLogin
		agentLogin = &value
	}
	return PolicyRegistration{
		number:       input.Number,
		from:         from,
		to:           to,
		policyHolder: input.PolicyHolder,
		productCode:  input.ProductCode,
		totalPremium: input.TotalPremium,
		agentLogin:   agentLogin,
	}, nil
}

func newPolicyDate(parts [3]int) (PolicyDate, error) {
	value := time.Date(parts[0], time.Month(parts[1]), parts[2], 0, 0, 0, 0, time.UTC)
	if value.Year() != parts[0] || int(value.Month()) != parts[1] || value.Day() != parts[2] {
		return PolicyDate{}, errors.New("invalid calendar date")
	}
	return PolicyDate{year: parts[0], month: parts[1], day: parts[2]}, nil
}

func validPolicyText(value string) bool {
	return utf8.ValidString(value) && len(value) <= maximumPolicyTextBytes
}

func (date PolicyDate) time() time.Time {
	return time.Date(date.year, time.Month(date.month), date.day, 0, 0, 0, 0, time.UTC)
}

// Parts returns the legacy three-integer date representation.
func (date PolicyDate) Parts() [3]int { return [3]int{date.year, date.month, date.day} }

// Year returns the four-digit calendar year.
func (date PolicyDate) Year() int { return date.year }

// Month returns the one-based calendar month.
func (date PolicyDate) Month() int { return date.month }

// Number returns the policy identity.
func (registration PolicyRegistration) Number() string { return registration.number }

// From returns the policy start date.
func (registration PolicyRegistration) From() PolicyDate { return registration.from }

// To returns the policy end date.
func (registration PolicyRegistration) To() PolicyDate { return registration.to }

// PolicyHolder returns the legacy policy-holder text.
func (registration PolicyRegistration) PolicyHolder() string { return registration.policyHolder }

// ProductCode returns the legacy product code.
func (registration PolicyRegistration) ProductCode() string { return registration.productCode }

// TotalPremium returns the exact validated JSON number token.
func (registration PolicyRegistration) TotalPremium() string { return registration.totalPremium }

// AgentLogin returns a copy of the nullable legacy agent login.
func (registration PolicyRegistration) AgentLogin() *string {
	if registration.agentLogin == nil {
		return nil
	}
	value := *registration.agentLogin
	return &value
}
