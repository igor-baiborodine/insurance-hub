package domain

import (
	"errors"
	"math"
	"math/big"
	"math/bits"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxTrendPremiumLength = 313

var (
	ErrInvalidAggregationUnit = errors.New("invalid sales-trend aggregation unit")
	ErrInvalidTrendPremium    = errors.New("invalid trend premium amount")
	trendPremiumPattern       = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)\.[0-9]{2}$`)
	trendNumberPattern        = regexp.MustCompile(
		`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`,
	)
	maximumTrendCoefficient = mustRoundTrendNumber("1.7976931348623157E+308")
)

// AggregationUnit identifies one Elasticsearch calendar histogram interval.
type AggregationUnit string

const (
	AggregationUnitDay   AggregationUnit = "DAY"
	AggregationUnitWeek  AggregationUnit = "WEEK"
	AggregationUnitMonth AggregationUnit = "MONTH"
	AggregationUnitYear  AggregationUnit = "YEAR"
)

func ParseAggregationUnit(value string) (AggregationUnit, error) {
	unit := AggregationUnit(value)
	switch unit {
	case AggregationUnitDay, AggregationUnitWeek, AggregationUnitMonth, AggregationUnitYear:
		return unit, nil
	default:
		return "", ErrInvalidAggregationUnit
	}
}

func (unit AggregationUnit) CalendarInterval() string {
	return strings.ToLower(string(unit))
}

// TrendPremiumAmount preserves the accepted canonical plain scale-two decimal.
type TrendPremiumAmount struct{ value string }

func NewTrendPremiumAmount(value string) (TrendPremiumAmount, error) {
	if len(value) > maxTrendPremiumLength || !trendPremiumPattern.MatchString(value) ||
		value == "-0.00" {
		return TrendPremiumAmount{}, ErrInvalidTrendPremium
	}
	digits := strings.ReplaceAll(value, ".", "")
	coefficient, ok := new(big.Int).SetString(digits, 10)
	if !ok || new(big.Int).Abs(coefficient).Cmp(maximumTrendCoefficient) > 0 {
		return TrendPremiumAmount{}, ErrInvalidTrendPremium
	}
	return TrendPremiumAmount{value: value}, nil
}

// NewTrendPremiumAmountFromFloat reproduces the current legacy JDK 17 Double.toString token
// before applying exact decimal HALF_UP rounding to two fraction digits.
func NewTrendPremiumAmountFromFloat(value float64) (TrendPremiumAmount, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return TrendPremiumAmount{}, ErrInvalidTrendPremium
	}
	return newTrendPremiumAmountFromNumber(java17DoubleToken(value))
}

func (amount TrendPremiumAmount) String() string { return amount.value }

// DateFromEpochMillis converts a histogram key through the configured legacy-compatible zone.
func DateFromEpochMillis(epochMillis int64, location *time.Location) (Date, error) {
	if location == nil {
		return Date{}, ErrInvalidDate
	}
	return ParseDate(time.UnixMilli(epochMillis).In(location).Format(time.DateOnly))
}

// TrendSalesValue contains one bucket's exact count and canonical scale-two premium.
type TrendSalesValue struct {
	policiesCount int64
	premiumAmount TrendPremiumAmount
}

func NewTrendSalesValue(
	policiesCount int64,
	premiumAmount TrendPremiumAmount,
) (TrendSalesValue, error) {
	if policiesCount < 0 {
		return TrendSalesValue{}, ErrNegativePolicyCount
	}
	return TrendSalesValue{policiesCount: policiesCount, premiumAmount: premiumAmount}, nil
}

func (value TrendSalesValue) PoliciesCount() int64 { return value.policiesCount }

func (value TrendSalesValue) PremiumAmount() TrendPremiumAmount { return value.premiumAmount }

// PeriodSales preserves the backend label independently from the zone-derived calendar date.
type PeriodSales struct {
	PeriodDate Date
	Period     string
	Value      TrendSalesValue
}

func newTrendPremiumAmountFromNumber(value string) (TrendPremiumAmount, error) {
	if len(value) == 0 || len(value) > 32 || !trendNumberPattern.MatchString(value) {
		return TrendPremiumAmount{}, ErrInvalidTrendPremium
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return TrendPremiumAmount{}, ErrInvalidTrendPremium
	}
	rational, ok := trendDecimalRational(value)
	if !ok {
		return TrendPremiumAmount{}, ErrInvalidTrendPremium
	}
	return NewTrendPremiumAmount(formatTrendScaleTwo(roundTrendRationalHalfUp(rational)))
}

func mustRoundTrendNumber(value string) *big.Int {
	rational, ok := trendDecimalRational(value)
	if !ok {
		panic("invalid maximum trend number")
	}
	return new(big.Int).Abs(roundTrendRationalHalfUp(rational))
}

func roundTrendRationalHalfUp(value *big.Rat) *big.Int {
	scaled := new(big.Rat).Mul(value, big.NewRat(100, 1))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(scaled.Num(), scaled.Denom(), remainder)
	if new(big.Int).Lsh(new(big.Int).Abs(remainder), 1).Cmp(scaled.Denom()) >= 0 {
		if scaled.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		} else {
			quotient.Add(quotient, big.NewInt(1))
		}
	}
	return quotient
}

func trendDecimalRational(value string) (*big.Rat, bool) {
	mantissa, exponentText, hasExponent := strings.Cut(strings.ToLower(value), "e")
	exponent := 0
	if hasExponent {
		parsed, err := strconv.Atoi(exponentText)
		if err != nil || parsed < -400 || parsed > 400 {
			return nil, false
		}
		exponent = parsed
	}
	negative := strings.HasPrefix(mantissa, "-")
	mantissa = strings.TrimPrefix(mantissa, "-")
	whole, fraction, hasPoint := strings.Cut(mantissa, ".")
	if !hasPoint {
		fraction = ""
	}
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return new(big.Rat), true
	}
	coefficient, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, false
	}
	if negative {
		coefficient.Neg(coefficient)
	}
	scale := len(fraction) - exponent
	if scale <= 0 {
		coefficient.Mul(coefficient, trendPowerOfTen(-scale))
		return new(big.Rat).SetInt(coefficient), true
	}
	return new(big.Rat).SetFrac(coefficient, trendPowerOfTen(scale)), true
}

func java17DoubleToken(value float64) string {
	if value == 0 {
		return "0.0"
	}
	negative := math.Signbit(value)
	absolute := math.Abs(value)
	if absolute == math.SmallestNonzeroFloat64 {
		if negative {
			return "-4.9E-324"
		}
		return "4.9E-324"
	}
	rawBits := math.Float64bits(absolute)
	fractBits := rawBits & ((uint64(1) << 52) - 1)
	binExp := int((rawBits >> 52) & 0x7ff)
	nSignificantBits := 53
	if binExp == 0 {
		leadingZeros := bits.LeadingZeros64(fractBits)
		shift := leadingZeros - 11
		fractBits <<= shift
		binExp = 1 - shift
		nSignificantBits = 64 - leadingZeros
	} else {
		fractBits |= uint64(1) << 52
	}
	binExp -= 1023
	nFractBits := 53 - bits.TrailingZeros64(fractBits)
	nTinyBits := max(0, nFractBits-binExp-1)
	if nTinyBits == 0 && binExp >= -21 && binExp <= 62 {
		insignificant := 0
		if binExp > nSignificantBits {
			insignificant = insignificantTrendDigitsForPowerOfTwo(
				binExp - nSignificantBits - 1,
			)
		}
		if binExp >= 52 {
			fractBits <<= binExp - 52
		} else {
			fractBits >>= 52 - binExp
		}
		token := developJava17LongDigits(fractBits, insignificant)
		if negative {
			return "-" + token
		}
		return token
	}
	token := java17HardToken(fractBits, binExp, nSignificantBits)
	if negative {
		return "-" + token
	}
	return token
}

func java17HardToken(fractBits uint64, binExp, nSignificantBits int) string {
	tailZeros := bits.TrailingZeros64(fractBits)
	nFractBits := 53 - tailZeros
	nTinyBits := max(0, nFractBits-binExp-1)
	d2Bits := (uint64(1023) << 52) | (fractBits & ((uint64(1) << 52) - 1))
	d2 := math.Float64frombits(d2Bits)
	decExp := int(math.Floor(
		(d2-1.5)*0.289529654 + 0.176091259 + float64(binExp)*0.301029995663981,
	))
	b5 := max(0, -decExp)
	b2 := b5 + nTinyBits + binExp
	s5 := max(0, decExp)
	s2 := s5 + nTinyBits
	m5 := b5
	m2 := b2 - nSignificantBits
	fractBits >>= tailZeros
	b2 -= nFractBits - 1
	common2 := min(b2, s2)
	b2 -= common2
	s2 -= common2
	m2 -= common2
	if nFractBits == 1 {
		m2--
	}
	if m2 < 0 {
		b2 -= m2
		s2 -= m2
		m2 = 0
	}
	bBits := nFractBits + b2 + trendPowerFiveBits(b5)
	tenSBits := s2 + 1 + trendPowerFiveBits(s5+1)
	if bBits < 32 && tenSBits < 32 {
		b := int32(fractBits) * int32(trendPowerFiveUint64(b5)) << b2
		s := int32(trendPowerFiveUint64(s5)) << s2
		m := int32(trendPowerFiveUint64(m5)) << m2
		return java17IntDigits(decExp, b, s, m)
	}
	if bBits < 64 && tenSBits < 64 {
		b := int64(fractBits*trendPowerFiveUint64(b5)) << b2
		s := int64(trendPowerFiveUint64(s5)) << s2
		m := int64(trendPowerFiveUint64(m5)) << m2
		return java17LongDigits(decExp, b, s, m)
	}
	b := scaledTrendInteger(new(big.Int).SetUint64(fractBits), b5, b2)
	s := scaledTrendInteger(big.NewInt(1), s5, s2)
	m := scaledTrendInteger(big.NewInt(1), m5, m2)
	tenS := new(big.Int).Mul(new(big.Int).Set(s), big.NewInt(10))
	digits := make([]byte, 0, 20)
	q, remainder := new(big.Int), new(big.Int)
	q.QuoRem(b, s, remainder)
	b.Mul(remainder, big.NewInt(10))
	m.Mul(m, big.NewInt(10))
	low := b.Cmp(m) < 0
	high := new(big.Int).Add(new(big.Int).Set(b), m).Cmp(tenS) >= 0
	if q.Sign() == 0 && !high {
		decExp--
	} else {
		digits = append(digits, byte('0'+q.Int64()))
	}
	if decExp < -3 || decExp >= 8 {
		low, high = false, false
	}
	for !low && !high {
		q.QuoRem(b, s, remainder)
		b.Mul(remainder, big.NewInt(10))
		m.Mul(m, big.NewInt(10))
		low = b.Cmp(m) < 0
		high = new(big.Int).Add(new(big.Int).Set(b), m).Cmp(tenS) >= 0
		digits = append(digits, byte('0'+q.Int64()))
	}
	if high {
		roundUp := !low
		if low {
			difference := new(
				big.Int,
			).Sub(new(big.Int).Lsh(new(big.Int).Set(b), 1), tenS)
			roundUp = difference.Sign() > 0 ||
				(difference.Sign() == 0 && (digits[len(digits)-1]-'0')%2 != 0)
		}
		if roundUp {
			decExp = incrementJava17Digits(digits, decExp)
		}
	}
	return javaFormatDigits(string(digits), decExp+1)
}

func trendPowerFiveBits(exponent int) int {
	if exponent == 0 {
		return 0
	}
	return new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(exponent)), nil).BitLen()
}

func trendPowerFiveUint64(exponent int) uint64 {
	value := uint64(1)
	for range exponent {
		value *= 5
	}
	return value
}

func java17IntDigits(decExp int, b, s, m int32) string {
	tenS := s * 10
	digits := make([]byte, 0, 20)
	q := b / s
	b = 10 * (b % s)
	m *= 10
	low := b < m
	high := b+m > tenS
	if q == 0 && !high {
		decExp--
	} else {
		digits = append(digits, byte('0'+q))
	}
	if decExp < -3 || decExp >= 8 {
		low, high = false, false
	}
	for !low && !high {
		q = b / s
		b = 10 * (b % s)
		m *= 10
		if m > 0 {
			low = b < m
			high = b+m > tenS
		} else {
			low, high = true, true
		}
		digits = append(digits, byte('0'+q))
	}
	return finishJava17Digits(digits, decExp, low, high, int64(b<<1)-int64(tenS))
}

func java17LongDigits(decExp int, b, s, m int64) string {
	tenS := s * 10
	digits := make([]byte, 0, 20)
	q := b / s
	b = 10 * (b % s)
	m *= 10
	low := b < m
	high := b+m > tenS
	if q == 0 && !high {
		decExp--
	} else {
		digits = append(digits, byte('0'+q))
	}
	if decExp < -3 || decExp >= 8 {
		low, high = false, false
	}
	for !low && !high {
		q = b / s
		b = 10 * (b % s)
		m *= 10
		if m > 0 {
			low = b < m
			high = b+m > tenS
		} else {
			low, high = true, true
		}
		digits = append(digits, byte('0'+q))
	}
	return finishJava17Digits(digits, decExp, low, high, (b<<1)-tenS)
}

func finishJava17Digits(digits []byte, decExp int, low, high bool, difference int64) string {
	if high && (!low || difference > 0 ||
		(difference == 0 && (digits[len(digits)-1]-'0')%2 != 0)) {
		decExp = incrementJava17Digits(digits, decExp)
	}
	return javaFormatDigits(string(digits), decExp+1)
}

func incrementJava17Digits(digits []byte, decExp int) int {
	index := len(digits) - 1
	for index > 0 && digits[index] == '9' {
		digits[index] = '0'
		index--
	}
	if digits[index] == '9' {
		digits[index] = '1'
		return decExp + 1
	}
	digits[index]++
	return decExp
}

func scaledTrendInteger(value *big.Int, power5, power2 int) *big.Int {
	result := new(big.Int).Set(value)
	if power5 > 0 {
		result.Mul(result, new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(power5)), nil))
	}
	if power2 > 0 {
		result.Lsh(result, uint(power2))
	}
	return result
}

func insignificantTrendDigitsForPowerOfTwo(exponent int) int {
	if exponent <= 1 || exponent >= 64 {
		return 0
	}
	value := new(big.Int).Lsh(big.NewInt(1), uint(exponent))
	return len(value.String()) - 1
}

func developJava17LongDigits(value uint64, insignificant int) string {
	decimalExponent := 0
	if insignificant != 0 {
		power := uint64(1)
		for range insignificant {
			power *= 10
		}
		residue := value % power
		value /= power
		decimalExponent += insignificant
		if residue >= power/2 {
			value++
		}
	}
	for value%10 == 0 {
		decimalExponent++
		value /= 10
	}
	digits := strconv.FormatUint(value, 10)
	decimalExponent += len(digits)
	return javaFormatDigits(digits, decimalExponent)
}

func javaFormatDigits(digits string, decimalExponent int) string {
	switch {
	case decimalExponent > 0 && decimalExponent < 8:
		if len(digits) < decimalExponent {
			return digits + strings.Repeat("0", decimalExponent-len(digits)) + ".0"
		}
		if len(digits) == decimalExponent {
			return digits + ".0"
		}
		return digits[:decimalExponent] + "." + digits[decimalExponent:]
	case decimalExponent <= 0 && decimalExponent > -3:
		return "0." + strings.Repeat("0", -decimalExponent) + digits
	default:
		fraction := "0"
		if len(digits) > 1 {
			fraction = digits[1:]
		}
		return digits[:1] + "." + fraction + "E" + strconv.Itoa(decimalExponent-1)
	}
}

func trendPowerOfTen(exponent int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
}

func formatTrendScaleTwo(coefficient *big.Int) string {
	negative := coefficient.Sign() < 0
	digits := new(big.Int).Abs(coefficient).String()
	if len(digits) < 3 {
		digits = strings.Repeat("0", 3-len(digits)) + digits
	}
	result := digits[:len(digits)-2] + "." + digits[len(digits)-2:]
	if negative {
		return "-" + result
	}
	return result
}
