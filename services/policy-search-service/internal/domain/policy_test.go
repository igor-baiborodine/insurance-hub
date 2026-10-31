package domain

import (
	"errors"
	"testing"
)

func TestParseDate_PreservesCalendarDateAndRejectsOtherRepresentations(t *testing.T) {
	// given
	valid := "2024-02-29"
	invalid := []string{"", "2024-02-30", "2024-02-29T00:00:00Z"}

	// when
	date, err := ParseDate(valid)

	// then
	if err != nil || date.String() != valid {
		t.Fatalf("ParseDate(%q) = %q, %v", valid, date.String(), err)
	}
	for _, value := range invalid {
		if _, err := ParseDate(value); !errors.Is(err, ErrInvalidDate) {
			t.Errorf("ParseDate(%q) error = %v, want ErrInvalidDate", value, err)
		}
	}
}
