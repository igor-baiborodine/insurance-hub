package domain_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

func TestPolicy_Terminate_ScalesTheAnnualCoverByElapsedDays(t *testing.T) {
	// given
	policy := coveredPolicy(
		day(2026, 1, 1),
		day(2026, 12, 31),
		domain.Cover{Code: "C1", Price: "120.00"},
	)

	// when
	terminal, err := policy.Terminate(time.Date(2026, 6, 30, 23, 0, 0, 0, time.UTC))

	// then
	// 181 of 365 days: 120 × 0.49589041095890410959 = 59.5068… → 59.51.
	want := domain.PolicyVersion{
		Number:        2,
		ProductCode:   "HSI",
		AccountNumber: "account-1",
		Holder: domain.PolicyHolder{
			FirstName: "Synthetic",
			LastName:  "Holder",
			TaxID:     "00000",
		},
		CoverFrom:    day(2026, 1, 1),
		CoverTo:      day(2026, 6, 30),
		VersionFrom:  day(2026, 7, 1),
		VersionTo:    day(2026, 12, 31),
		TotalPremium: "59.51",
		Covers:       []domain.Cover{{Code: "C1", Price: "59.51"}},
	}
	if err != nil || !reflect.DeepEqual(terminal, want) {
		t.Errorf("terminal = %+v (%v), want %+v", terminal, err, want)
	}
}

func TestPolicy_Terminate_RoundsEachCoverBeforeSumming(t *testing.T) {
	// given
	policy := coveredPolicy(day(2026, 6, 30), day(2026, 7, 1),
		domain.Cover{Code: "C1", Price: "0.05"}, domain.Cover{Code: "C2", Price: "0.05"})

	// when
	terminal, err := policy.Terminate(day(2026, 6, 30))

	// then
	// Each 0.025 rounds to 0.03; rounding the 0.10 total first would give 0.05.
	if err != nil || terminal.TotalPremium != "0.06" ||
		!reflect.DeepEqual(terminal.Covers, []domain.Cover{
			{Code: "C1", Price: "0.03"}, {Code: "C2", Price: "0.03"},
		}) {
		t.Errorf("terminal = %+v (%v), want two 0.03 covers totaling 0.06", terminal, err)
	}
}

func TestPolicy_Terminate_AcceptsBothCoverageEndpoints(t *testing.T) {
	for _, test := range []struct {
		name    string
		date    time.Time
		premium string
	}{
		{"first covered day", day(2026, 1, 1), "0.33"},
		{"last covered day", day(2026, 12, 31), "120.00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			policy := coveredPolicy(day(2026, 1, 1), day(2026, 12, 31),
				domain.Cover{Code: "C1", Price: "120.00"})

			// when
			terminal, err := policy.Terminate(test.date)

			// then
			if err != nil || terminal.TotalPremium != test.premium {
				t.Errorf(
					"terminal = %+v (%v), want premium %s",
					terminal,
					err,
					test.premium,
				)
			}
		})
	}
}

func TestPolicy_Terminate_RejectsDatesOutsideCoverage(t *testing.T) {
	for _, test := range []struct {
		name string
		date time.Time
	}{
		{"before coverage", day(2025, 12, 31)},
		{"after coverage", day(2027, 1, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			policy := coveredPolicy(day(2026, 1, 1), day(2026, 12, 31),
				domain.Cover{Code: "C1", Price: "120.00"})

			// when
			_, err := policy.Terminate(test.date)

			// then
			if !errors.Is(err, domain.ErrTerminationOutsideCoverage) {
				t.Errorf(
					"error = %v, want %v",
					err,
					domain.ErrTerminationOutsideCoverage,
				)
			}
		})
	}
}

func TestPolicy_Terminate_UsesTheLatestVersion(t *testing.T) {
	// given
	policy := coveredPolicy(day(2026, 1, 1), day(2026, 12, 31),
		domain.Cover{Code: "C1", Price: "120.00"})
	policy.Versions = append(policy.Versions, domain.PolicyVersion{
		Number: 2, CoverFrom: day(2026, 1, 1), CoverTo: day(2026, 3, 31),
		VersionFrom: day(2026, 4, 1), VersionTo: day(2026, 12, 31), TotalPremium: "29.59",
		Covers: []domain.Cover{{Code: "C1", Price: "29.59"}},
	})

	// when
	_, err := policy.Terminate(day(2026, 6, 30))

	// then
	if !errors.Is(err, domain.ErrTerminationOutsideCoverage) {
		t.Errorf("error = %v, want the shortened version 2 coverage to apply", err)
	}
}

func TestTerminationEvent_AnnouncesTheShortenedCoverage(t *testing.T) {
	// given
	policy := coveredPolicy(day(2026, 1, 1), day(2026, 12, 31),
		domain.Cover{Code: "C1", Price: "120.00"})
	terminal, err := policy.Terminate(day(2026, 6, 30))
	if err != nil {
		t.Fatal(err)
	}

	// when
	event := domain.TerminationEvent([16]byte{7}, "policy-1", terminal)

	// then
	want := domain.PolicyEvent{
		IntentID: [16]byte{7}, Topic: "policy-terminated", PolicyNumber: "policy-1",
		From: day(2026, 1, 1), To: day(2026, 6, 30), PolicyHolder: "Synthetic Holder",
		ProductCode: "HSI", TotalPremium: "59.51",
	}
	if event != want {
		t.Errorf("event = %+v, want %+v", event, want)
	}
}

func coveredPolicy(from, to time.Time, covers ...domain.Cover) domain.Policy {
	return domain.Policy{
		Number: "policy-1",
		Versions: []domain.PolicyVersion{
			{
				Number:        1,
				ProductCode:   "HSI",
				AccountNumber: "account-1",
				Holder: domain.PolicyHolder{
					FirstName: "Synthetic",
					LastName:  "Holder",
					TaxID:     "00000",
				},
				CoverFrom:   from,
				CoverTo:     to,
				VersionFrom: from,
				VersionTo:   to,
				Covers:      covers,
			},
		},
	}
}

func day(year int, month time.Month, date int) time.Time {
	return time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
}
