package domain_test

import (
	"errors"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

func TestProductSelection_PreservesPresenceAndExactValue(t *testing.T) {
	tests := []struct {
		name        string
		selection   domain.ProductSelection
		wantPresent bool
		wantValue   string
	}{
		{name: "all products", selection: domain.AllProducts()},
		{
			name:        "exact empty product",
			selection:   domain.ExactProduct(""),
			wantPresent: true,
		},
		{
			name:        "exact product bytes",
			selection:   domain.ExactProduct(" SAFE_farm "),
			wantPresent: true,
			wantValue:   " SAFE_farm ",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			present, value := test.selection.Present(), test.selection.Value()

			// then
			if present != test.wantPresent || value != test.wantValue {
				t.Fatalf(
					"selection = (%t, %q), want (%t, %q)",
					present,
					value,
					test.wantPresent,
					test.wantValue,
				)
			}
		})
	}
}

func TestSalesValue_AddsCheckedCountAndAmount(t *testing.T) {
	// given
	left, err := domain.NewSalesValue(2, domain.NewPremiumAmount(101))
	if err != nil {
		t.Fatal(err)
	}
	right, err := domain.NewSalesValue(3, domain.NewPremiumAmount(-202))
	if err != nil {
		t.Fatal(err)
	}

	// when
	got, err := left.Add(right)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if got.PoliciesCount() != 5 || got.PremiumAmount().MinorUnits() != -101 {
		t.Fatalf(
			"sum = (%d, %d), want (5, -101)",
			got.PoliciesCount(),
			got.PremiumAmount().MinorUnits(),
		)
	}
}

func TestSalesValue_RejectsInvalidOrOverflowingValues(t *testing.T) {
	// given
	maximum, err := domain.NewSalesValue(1<<63-1, domain.NewPremiumAmount(1<<63-1))
	if err != nil {
		t.Fatal(err)
	}
	one, err := domain.NewSalesValue(1, domain.NewPremiumAmount(1))
	if err != nil {
		t.Fatal(err)
	}

	// when / then
	if _, err := domain.NewSalesValue(
		-1,
		domain.NewPremiumAmount(0),
	); !errors.Is(
		err,
		domain.ErrNegativePolicyCount,
	) {
		t.Fatalf("negative count error = %v", err)
	}
	if _, err := maximum.Add(one); !errors.Is(err, domain.ErrSalesValueOverflow) {
		t.Fatalf("positive overflow error = %v", err)
	}
	minimum := domain.NewPremiumAmount(-1 << 63)
	if _, err := minimum.Add(
		domain.NewPremiumAmount(-1),
	); !errors.Is(
		err,
		domain.ErrSalesValueOverflow,
	) {
		t.Fatalf("negative overflow error = %v", err)
	}
}

func TestParseDate_RequiresCalendarDate(t *testing.T) {
	// when
	valid, validErr := domain.ParseDate("2026-11-05")
	_, invalidErr := domain.ParseDate("2026-02-30")

	// then
	if validErr != nil || valid.String() != "2026-11-05" {
		t.Fatalf("valid date = (%q, %v)", valid.String(), validErr)
	}
	if !errors.Is(invalidErr, domain.ErrInvalidDate) {
		t.Fatalf("invalid date error = %v", invalidErr)
	}
}
