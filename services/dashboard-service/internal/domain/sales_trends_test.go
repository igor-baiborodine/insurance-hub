package domain

import (
	"bufio"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTrendPremiumAmount_JavaDifferentialVectors(t *testing.T) {
	path := os.Getenv("DASHBOARD_JAVA_TREND_VECTOR_FILE")
	if path == "" {
		t.Skip("DASHBOARD_JAVA_TREND_VECTOR_FILE is not supplied")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) != 3 {
			t.Fatalf("invalid Java vector %q", scanner.Text())
		}
		bits, err := strconv.ParseUint(parts[0], 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		amount, err := NewTrendPremiumAmountFromFloat(math.Float64frombits(bits))
		if err != nil || amount.String() != parts[2] {
			t.Fatalf(
				"bits=%s Java=%s amount=(%q, %v), want %q",
				parts[0],
				parts[1],
				amount.String(),
				err,
				parts[2],
			)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count < 99_900 {
		t.Fatalf("Java differential covered only %d vectors", count)
	}
}

func TestAggregationUnit_AcceptsOnlyExactCalendarUnits(t *testing.T) {
	for _, value := range []string{"DAY", "WEEK", "MONTH", "YEAR"} {
		unit, err := ParseAggregationUnit(value)
		if err != nil || unit.CalendarInterval() != strings.ToLower(value) {
			t.Fatalf("ParseAggregationUnit(%q) = (%q, %v)", value, unit, err)
		}
	}
	for _, value := range []string{"", "day", "WEEKS", " MONTH"} {
		if _, err := ParseAggregationUnit(
			value,
		); !errors.Is(
			err,
			ErrInvalidAggregationUnit,
		) {
			t.Fatalf("ParseAggregationUnit(%q) = %v", value, err)
		}
	}
}

func TestTrendPremiumAmount_PreservesAcceptedJavaRoundingAndRange(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{1.005, "1.01"},
		{-1.005, "-1.01"},
		{0, "0.00"},
		{1000, "1000.00"},
		{0.1 + 0.2, "0.30"},
		{math.Float64frombits(0xc386b53d953b2ed0), "-204535218846423552.00"},
		{math.Float64frombits(0x43986e25cb906043), "440096515144814780.00"},
	}
	for _, test := range tests {
		amount, err := NewTrendPremiumAmountFromFloat(test.value)
		if err != nil || amount.String() != test.want {
			t.Fatalf(
				"NewTrendPremiumAmountFromFloat(%g) = (%q, %v), want %q",
				test.value,
				amount.String(),
				err,
				test.want,
			)
		}
	}
	maximum, err := NewTrendPremiumAmountFromFloat(math.MaxFloat64)
	if err != nil || len(maximum.String()) != 312 {
		t.Fatalf("maximum = (%q, %v)", maximum.String(), err)
	}
	minimum, err := NewTrendPremiumAmountFromFloat(-math.MaxFloat64)
	if err != nil || len(minimum.String()) != 313 {
		t.Fatalf("minimum = (%q, %v)", minimum.String(), err)
	}
}

func TestTrendPremiumAmount_RejectsNonCanonicalAndNonFiniteValues(t *testing.T) {
	for _, value := range []string{"", "1", "1.0", "01.00", "+1.00", "-0.00", "1e2"} {
		if _, err := NewTrendPremiumAmount(value); !errors.Is(err, ErrInvalidTrendPremium) {
			t.Fatalf("NewTrendPremiumAmount(%q) = %v", value, err)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := NewTrendPremiumAmountFromFloat(
			value,
		); !errors.Is(
			err,
			ErrInvalidTrendPremium,
		) {
			t.Fatalf("NewTrendPremiumAmountFromFloat(%v) = %v", value, err)
		}
	}
}

func TestDateFromEpochMillis_UsesConfiguredZoneIndependentlyFromLabel(t *testing.T) {
	toronto, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		epoch int64
		want  string
	}{
		{1710028800000, "2024-03-09"},
		{1730592000000, "2024-11-02"},
	} {
		date, err := DateFromEpochMillis(test.epoch, toronto)
		if err != nil || date.String() != test.want {
			t.Fatalf(
				"DateFromEpochMillis(%d) = (%q, %v)",
				test.epoch,
				date.String(),
				err,
			)
		}
	}
	if _, err := DateFromEpochMillis(0, nil); !errors.Is(err, ErrInvalidDate) {
		t.Fatalf("nil location = %v", err)
	}
}

func TestTrendSalesValue_RejectsNegativeCounts(t *testing.T) {
	amount, err := NewTrendPremiumAmount("0.00")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewTrendSalesValue(-1, amount); !errors.Is(err, ErrNegativePolicyCount) {
		t.Fatalf("negative count = %v", err)
	}
	value, err := NewTrendSalesValue(math.MaxInt64, amount)
	if err != nil || value.PoliciesCount() != math.MaxInt64 {
		t.Fatalf("maximum count = (%d, %v)", value.PoliciesCount(), err)
	}
}
