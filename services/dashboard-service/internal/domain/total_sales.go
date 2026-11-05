// Package domain contains Dashboard business values independent of storage and transports.
package domain

import (
	"errors"
	"time"
)

const (
	maxInt64 = int64(1<<63 - 1)
	minInt64 = -maxInt64 - 1
)

var (
	ErrInvalidDate         = errors.New("invalid sales date")
	ErrNegativePolicyCount = errors.New("policy count must not be negative")
	ErrSalesValueOverflow  = errors.New("sales value overflow")
)

// Date preserves a calendar date without adding a time zone or timestamp.
type Date struct{ value time.Time }

func ParseDate(value string) (Date, error) {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return Date{}, ErrInvalidDate
	}
	return Date{value: parsed}, nil
}

func (date Date) String() string { return date.value.Format(time.DateOnly) }

// ProductSelection distinguishes ALL from an exact product, including an empty product code.
type ProductSelection struct {
	present bool
	value   string
}

func AllProducts() ProductSelection { return ProductSelection{} }

func ExactProduct(value string) ProductSelection {
	return ProductSelection{present: true, value: value}
}

func (selection ProductSelection) Present() bool { return selection.present }

func (selection ProductSelection) Value() string { return selection.value }

// PremiumAmount stores an exact two-place amount as minor units.
type PremiumAmount struct{ minorUnits int64 }

func NewPremiumAmount(minorUnits int64) PremiumAmount {
	return PremiumAmount{minorUnits: minorUnits}
}

func (amount PremiumAmount) MinorUnits() int64 { return amount.minorUnits }

func (amount PremiumAmount) Add(other PremiumAmount) (PremiumAmount, error) {
	if (other.minorUnits > 0 && amount.minorUnits > maxInt64-other.minorUnits) ||
		(other.minorUnits < 0 && amount.minorUnits < minInt64-other.minorUnits) {
		return PremiumAmount{}, ErrSalesValueOverflow
	}
	return NewPremiumAmount(amount.minorUnits + other.minorUnits), nil
}

// SalesValue contains one exact count and two-place amount pair.
type SalesValue struct {
	policiesCount int64
	premiumAmount PremiumAmount
}

func NewSalesValue(policiesCount int64, premiumAmount PremiumAmount) (SalesValue, error) {
	if policiesCount < 0 {
		return SalesValue{}, ErrNegativePolicyCount
	}
	return SalesValue{policiesCount: policiesCount, premiumAmount: premiumAmount}, nil
}

func (value SalesValue) PoliciesCount() int64 { return value.policiesCount }

func (value SalesValue) PremiumAmount() PremiumAmount { return value.premiumAmount }

func (value SalesValue) Add(other SalesValue) (SalesValue, error) {
	if value.policiesCount > maxInt64-other.policiesCount {
		return SalesValue{}, ErrSalesValueOverflow
	}
	amount, err := value.premiumAmount.Add(other.premiumAmount)
	if err != nil {
		return SalesValue{}, err
	}
	return NewSalesValue(value.policiesCount+other.policiesCount, amount)
}

// ProductSales associates one returned Elasticsearch bucket with its exact product key.
type ProductSales struct {
	ProductCode string
	Value       SalesValue
}
