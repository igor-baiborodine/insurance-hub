package domain

import (
	"errors"
	"testing"
)

func TestDecimal_PreservesScaleAndRoundsHalfUp(t *testing.T) {
	// given
	left, err := NewDecimal("10.00")
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewDecimal("1.50")
	if err != nil {
		t.Fatal(err)
	}
	negativeTie, err := NewDecimal("-0.015")
	if err != nil {
		t.Fatal(err)
	}

	// when / then
	if left.String() != "10.00" || left.Multiply(right).RoundHalfUp(2).String() != "15.00" {
		t.Errorf("decimal scale or multiplication changed: %s", left.String())
	}
	if negativeTie.RoundHalfUp(2).String() != "-0.02" {
		t.Errorf("negative HALF_UP tie=%s", negativeTie.RoundHalfUp(2).String())
	}
	if _, err := NewDecimal("invalid"); !errors.Is(err, ErrInvalidDecimal) {
		t.Errorf("invalid decimal error=%v", err)
	}
}
