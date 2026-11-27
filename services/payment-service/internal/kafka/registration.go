package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
)

var (
	errMalformedRegistration = errors.New("malformed policy registration")
	errMissingPolicyNumber   = errors.New("missing policy number")
	errPolicyKeyMismatch     = errors.New("policy registration key mismatch")
	errInvalidPremium        = errors.New("invalid policy registration premium")
	errInvalidCoverageStart  = errors.New("invalid policy registration coverage start")
	numeric19Scale2Limit     = new(big.Int).Exp(big.NewInt(10), big.NewInt(19), nil)
)

type registrationOperation interface {
	RegisterPolicy(context.Context, application.PolicyRegistration) error
}

// RegistrationHandler validates Policy's registration contract before invoking Payment.
type RegistrationHandler struct {
	operation registrationOperation
}

// NewRegistrationHandler wires the validated registration input to Payment's application boundary.
func NewRegistrationHandler(operation registrationOperation) (*RegistrationHandler, error) {
	if operation == nil {
		return nil, errors.New(
			"configure policy registration handler: operation is required",
		)
	}
	return &RegistrationHandler{operation: operation}, nil
}

// Handle validates one keyed registration without exposing its contents in failures.
func (handler *RegistrationHandler) Handle(ctx context.Context, key, value []byte) error {
	registration, err := decodeRegistration(key, value)
	if err != nil {
		return err
	}
	if err := handler.operation.RegisterPolicy(ctx, registration); err != nil {
		return fmt.Errorf("register policy payment account: %w", err)
	}
	return nil
}

type registrationEnvelope struct {
	Policy *registrationPolicy `json:"policy"`
}

type registrationPolicy struct {
	Number       string      `json:"number"`
	From         []int       `json:"from"`
	TotalPremium json.Number `json:"totalPremium"`
}

func decodeRegistration(key, value []byte) (application.PolicyRegistration, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var envelope registrationEnvelope
	if err := decoder.Decode(&envelope); err != nil || envelope.Policy == nil {
		return application.PolicyRegistration{}, errMalformedRegistration
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return application.PolicyRegistration{}, errMalformedRegistration
	}

	policy := envelope.Policy
	if strings.TrimSpace(policy.Number) == "" {
		return application.PolicyRegistration{}, errMissingPolicyNumber
	}
	if string(key) != policy.Number {
		return application.PolicyRegistration{}, errPolicyKeyMismatch
	}
	premium, err := exactNumeric19Scale2(policy.TotalPremium)
	if err != nil {
		return application.PolicyRegistration{}, err
	}
	coverageStart, err := calendarDate(policy.From)
	if err != nil {
		return application.PolicyRegistration{}, err
	}
	return application.PolicyRegistration{
		PolicyNumber: policy.Number, Premium: premium, CoverageStart: coverageStart,
	}, nil
}

func exactNumeric19Scale2(number json.Number) (string, error) {
	text := number.String()
	value, ok := new(big.Rat).SetString(text)
	if !ok {
		return "", errInvalidPremium
	}
	scaled := new(big.Rat).Mul(value, big.NewRat(100, 1))
	if scaled.Denom().Cmp(big.NewInt(1)) != 0 {
		return "", errInvalidPremium
	}
	absCents := new(big.Int).Abs(scaled.Num())
	if absCents.Cmp(numeric19Scale2Limit) >= 0 {
		return "", errInvalidPremium
	}
	return text, nil
}

func calendarDate(parts []int) (time.Time, error) {
	if len(parts) != 3 || parts[0] < 1 {
		return time.Time{}, errInvalidCoverageStart
	}
	year, month, day := parts[0], time.Month(parts[1]), parts[2]
	date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if date.Year() != year || date.Month() != month || date.Day() != day {
		return time.Time{}, errInvalidCoverageStart
	}
	return date, nil
}
