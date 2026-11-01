// Package domain contains policy search values independent of storage and transports.
package domain

import (
	"errors"
	"time"
)

var ErrInvalidDate = errors.New("invalid policy date")

// Date preserves the legacy calendar-date value without adding a time zone or timestamp.
type Date struct{ value time.Time }

func ParseDate(value string) (Date, error) {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return Date{}, ErrInvalidDate
	}
	return Date{value: parsed}, nil
}

func (date Date) String() string { return date.value.Format(time.DateOnly) }

func (date Date) After(other Date) bool { return date.value.After(other.value) }

// Policy is the minimal read model returned by the policy search query.
type Policy struct {
	Number       string
	DateFrom     *Date
	DateTo       *Date
	PolicyHolder string
}
