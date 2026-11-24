package domain

import (
	"errors"
	"fmt"
	"math/big"
	"time"
)

const (
	// correctionFactorScale is the decimal scale of the elapsed-coverage factor.
	correctionFactorScale = 20
	// amountScale is the decimal scale of every stored amount.
	amountScale = 2
)

// ErrTerminationOutsideCoverage reports a termination date outside the latest coverage.
var ErrTerminationOutsideCoverage = errors.New("termination date outside coverage")

// Terminate returns the terminal version that ends coverage on the termination date. Coverage
// keeps its start and ends on that date; the new details apply from the following day until the
// original validity end. Each cover is scaled by the covered share of the original period
// (HALF_UP at scale 20) and rounded HALF_UP to cents; the premium is the sum of those covers.
func (policy Policy) Terminate(terminationDate time.Time) (PolicyVersion, error) {
	latest, err := policy.LatestVersion()
	if err != nil {
		return PolicyVersion{}, err
	}
	date := calendarDate(terminationDate)
	coverFrom, coverTo := calendarDate(latest.CoverFrom), calendarDate(latest.CoverTo)
	if date.Before(coverFrom) || date.After(coverTo) {
		return PolicyVersion{}, fmt.Errorf(
			"%w: %s",
			ErrTerminationOutsideCoverage,
			policy.Number,
		)
	}
	factor := roundHalfUp(
		new(
			big.Rat,
		).SetFrac64(inclusiveDays(coverFrom, date), inclusiveDays(coverFrom, coverTo)),
		correctionFactorScale,
	)
	total := new(big.Rat)
	covers := make([]Cover, 0, len(latest.Covers))
	for _, cover := range latest.Covers {
		price, ok := new(big.Rat).SetString(cover.Price)
		if !ok {
			return PolicyVersion{}, fmt.Errorf(
				"terminate policy: invalid cover price %q",
				cover.Price,
			)
		}
		corrected := roundHalfUp(price.Mul(price, factor), amountScale)
		total.Add(total, corrected)
		covers = append(
			covers,
			Cover{Code: cover.Code, Price: corrected.FloatString(amountScale)},
		)
	}
	return PolicyVersion{
		Number:        latest.Number + 1,
		ProductCode:   latest.ProductCode,
		Holder:        latest.Holder,
		AccountNumber: latest.AccountNumber,
		CoverFrom:     coverFrom,
		CoverTo:       date,
		VersionFrom:   date.AddDate(0, 0, 1),
		VersionTo:     calendarDate(latest.VersionTo),
		TotalPremium:  total.FloatString(amountScale),
		Covers:        covers,
	}, nil
}

// TerminationEvent announces the terminal version with its shortened coverage and premium.
func TerminationEvent(intentID [16]byte, number string, terminal PolicyVersion) PolicyEvent {
	return PolicyEvent{
		IntentID:     intentID,
		Topic:        TopicPolicyTerminated,
		PolicyNumber: number,
		From:         terminal.CoverFrom,
		To:           terminal.CoverTo,
		PolicyHolder: terminal.Holder.FullName(),
		ProductCode:  terminal.ProductCode,
		TotalPremium: terminal.TotalPremium,
	}
}

// inclusiveDays counts the calendar days from start to end, both included.
func inclusiveDays(start, end time.Time) int64 {
	return int64(end.Sub(start).Hours()/24) + 1
}

// roundHalfUp rounds to the decimal scale, with ties away from zero.
func roundHalfUp(value *big.Rat, scale int) *big.Rat {
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	scaled := new(big.Rat).Mul(value, new(big.Rat).SetInt(unit))
	numerator, denominator := scaled.Num(), scaled.Denom()
	quotient, remainder := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	if new(big.Int).Mul(new(big.Int).Abs(remainder), big.NewInt(2)).
		Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(int64(numerator.Sign())))
	}
	return new(big.Rat).SetFrac(quotient, unit)
}
