package mvel_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/libs/mvel"
)

func TestDecimalExpression_EvaluatesIndependentExamples(t *testing.T) {
	for _, test := range []struct {
		name      string
		source    string
		bindings  mvel.Bindings
		want      string
		wantScale int
	}{
		{
			name:   "TRI EUR ternary",
			source: "(NUM_OF_ADULTS) * (DESTINATION == 'EUR' ? 26.00B : 34.00B)",
			bindings: mvel.Bindings{
				"NUM_OF_ADULTS": mustNumber(t, "2"), "DESTINATION": mvel.Text("EUR"),
			},
			want: "52.00", wantScale: 2,
		},
		{
			name:   "TRI WORLD ternary",
			source: "(NUM_OF_ADULTS) * (DESTINATION == 'EUR' ? 26.00B : 34.00B)",
			bindings: mvel.Bindings{
				"NUM_OF_ADULTS": mustNumber(t, "2"), "DESTINATION": mvel.Text("WORLD"),
			},
			want: "68.00", wantScale: 2,
		},
		{
			name:   "TRI addition",
			source: "(NUM_OF_ADULTS + NUM_OF_CHILDREN) * 26.00B",
			bindings: mvel.Bindings{
				"NUM_OF_ADULTS": mustNumber(t, "1"), "NUM_OF_CHILDREN": mustNumber(t, "2"),
			},
			want: "78.00", wantScale: 2,
		},
		{
			name:     "HSI fractional area",
			source:   "AREA * 0.25B",
			bindings: mvel.Bindings{"AREA": mustNumber(t, "0.5")},
			want:     "0.125", wantScale: 3,
		},
		{
			name:   "FAI constant",
			source: "10B",
			want:   "10", wantScale: 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			expression, err := mvel.CompileDecimal(test.source)
			if err != nil {
				t.Fatal(err)
			}

			// when
			got, err := expression.Evaluate(test.bindings)

			// then
			if err != nil || got.String() != test.want ||
				got.Scale() != test.wantScale {
				t.Fatalf(
					"evaluation = %q scale %d, error %v; want %q scale %d",
					got.String(), got.Scale(), err, test.want, test.wantScale,
				)
			}
		})
	}
}

func TestPredicate_EvaluatesThresholdsAndConcurrentBindings(t *testing.T) {
	// given
	flood, err := mvel.CompilePredicate("FLOOD == 'YES'")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := mvel.CompilePredicate("NUM_OF_CLAIM > 2")
	if err != nil {
		t.Fatal(err)
	}

	// when / then
	assertPredicate(t, claims, mvel.Bindings{"NUM_OF_CLAIM": mustNumber(t, "2")}, false)
	assertPredicate(t, claims, mvel.Bindings{"NUM_OF_CLAIM": mustNumber(t, "3")}, true)

	var group sync.WaitGroup
	for index := range 100 {
		group.Add(1)
		go func(want bool) {
			defer group.Done()
			input := "NO"
			if want {
				input = "YES"
			}
			got, evaluateErr := flood.Evaluate(mvel.Bindings{"FLOOD": mvel.Text(input)})
			if evaluateErr != nil || got != want {
				t.Errorf(
					"concurrent predicate = %v, error %v; want %v",
					got,
					evaluateErr,
					want,
				)
			}
		}(index%2 == 0)
	}
	group.Wait()
}

func TestCompile_RejectsUnsupportedKindsAndLimits(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		kind   string
		want   error
	}{
		{"malformed", "AREA *", "decimal", mvel.ErrSyntax},
		{"method call", "AREA.String()", "decimal", mvel.ErrUnsupported},
		{"function call", "TRIM(TYP)", "predicate", mvel.ErrUnsupported},
		{"subtraction", "AREA - 1B", "decimal", mvel.ErrUnsupported},
		{"unknown identifier", "UNKNOWN > 1", "predicate", mvel.ErrUnsupported},
		{"wrong result kind", "FLOOD == 'YES'", "decimal", mvel.ErrResultKind},
		{"source bytes", strings.Repeat(" ", 255) + "1B", "decimal", mvel.ErrLimit},
		{"nesting", strings.Repeat("(", 13) + "1B" + strings.Repeat(")", 13), "decimal", mvel.ErrLimit},
		{"ternary nesting", nestedTernary(13), "decimal", mvel.ErrLimit},
		{"precision", strings.Repeat("9", 39) + "B", "decimal", mvel.ErrLimit},
		{"magnitude", strings.Repeat("9", 21) + "B", "decimal", mvel.ErrLimit},
		{"scale", "0." + strings.Repeat("1", 19) + "B", "decimal", mvel.ErrLimit},
		{"nodes", strings.Repeat("1B+", 32) + "1B", "decimal", mvel.ErrLimit},
		// 129 tokens but only 63 nodes, so the token budget alone rejects it.
		{"tokens", strings.Repeat("(1)+", 31) + "((1))", "decimal", mvel.ErrLimit},
		{"empty", "", "decimal", mvel.ErrSyntax},
		{"invalid UTF-8", "TYP == '\xff'", "predicate", mvel.ErrSyntax},
		{"unterminated string", "TYP == 'APT", "predicate", mvel.ErrSyntax},
		{"unmatched parenthesis", "1B)", "decimal", mvel.ErrSyntax},
		{"ternary without colon", "FLOOD == 'YES' ? 1B", "decimal", mvel.ErrSyntax},
		{"trailing token", "1B 2B", "decimal", mvel.ErrSyntax},
		{"equality on decimals", "AREA == 1B", "predicate", mvel.ErrResultKind},
		{"comparison on strings", "TYP > 'APT'", "predicate", mvel.ErrResultKind},
		{"arithmetic on strings", "TYP + 1B", "decimal", mvel.ErrResultKind},
		{"ternary string branch", "FLOOD == 'YES' ? 'A' : 1B", "decimal", mvel.ErrResultKind},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			var err error
			if test.kind == "decimal" {
				_, err = mvel.CompileDecimal(test.source)
			} else {
				_, err = mvel.CompilePredicate(test.source)
			}

			// then
			if !errors.Is(err, test.want) {
				t.Fatalf("compile error = %v, want category %v", err, test.want)
			}
		})
	}
}

func TestEvaluate_RejectsBindingsWithoutDisclosingValues(t *testing.T) {
	// given
	expression, err := mvel.CompileDecimal("AREA * 0.25B")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := strings.Repeat("S", 33)

	for _, test := range []struct {
		name     string
		bindings mvel.Bindings
		want     error
	}{
		{"missing", nil, mvel.ErrBinding},
		{"wrong type", mvel.Bindings{"AREA": mvel.Text(sentinel)}, mvel.ErrBinding},
		{
			"extra",
			mvel.Bindings{"AREA": mustNumber(t, "1"), "FLOOD": mvel.Text(sentinel)},
			mvel.ErrBinding,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			_, err := expression.Evaluate(test.bindings)

			// then
			if !errors.Is(err, test.want) {
				t.Fatalf("evaluation error = %v, want category %v", err, test.want)
			}
			if strings.Contains(err.Error(), sentinel) {
				t.Fatalf("error disclosed binding value: %v", err)
			}
		})
	}
}

func TestEvaluate_EnforcesStringAndIntermediateDecimalLimits(t *testing.T) {
	// given
	predicate, err := mvel.CompilePredicate("FLOOD == 'YES'")
	if err != nil {
		t.Fatal(err)
	}
	expression, err := mvel.CompileDecimal("AREA * 99999999999999999999B")
	if err != nil {
		t.Fatal(err)
	}

	// when
	_, atStringLimitErr := predicate.Evaluate(mvel.Bindings{
		"FLOOD": mvel.Text(strings.Repeat("Y", 32)),
	})
	_, beyondStringLimitErr := predicate.Evaluate(mvel.Bindings{
		"FLOOD": mvel.Text(strings.Repeat("Y", 33)),
	})
	_, intermediateErr := expression.Evaluate(mvel.Bindings{
		"AREA": mustNumber(t, "99999999999999999999"),
	})

	// then
	if atStringLimitErr != nil {
		t.Fatalf("string at limit: %v", atStringLimitErr)
	}
	if !errors.Is(beyondStringLimitErr, mvel.ErrLimit) {
		t.Fatalf("string beyond limit error = %v, want limit error", beyondStringLimitErr)
	}
	if !errors.Is(intermediateErr, mvel.ErrLimit) {
		t.Fatalf("intermediate result error = %v, want limit error", intermediateErr)
	}
}

func TestLimits_AcceptsDocumentedBoundaries(t *testing.T) {
	for _, source := range []string{
		strings.Repeat(" ", 254) + "1B",
		strings.Repeat("(", 12) + "1B" + strings.Repeat(")", 12),
		nestedTernary(12),
		strings.Repeat("9", 20) + "B",
		"0." + strings.Repeat("1", 18) + "B",
		strings.Repeat("1B+", 31) + "1B",
		// Exactly 64 nodes: three in the condition, one ternary, 30 leaves plus 29 additions
		// in the true branch and one leaf in the false branch.
		"FLOOD=='Y'?" + strings.Repeat("1+", 29) + "1:1",
		// Valid expressions always have an odd token count, so 127 is the largest accepted.
		strings.Repeat("(1)+", 31) + "(1)",
	} {
		if _, err := mvel.CompileDecimal(source); err != nil {
			t.Errorf("compile at documented boundary: %v", err)
		}
	}

	// given
	allBindingsSource := "(DESTINATION == 'EUR' ? NUM_OF_ADULTS : NUM_OF_CHILDREN) + AREA + " +
		"NUM_OF_CLAIM + (TYP == 'APT' ? 1B : 2B) + (FLOOD == 'YES' ? 1B : 2B)"
	expression, err := mvel.CompileDecimal(allBindingsSource)
	if err != nil {
		t.Fatal(err)
	}
	bindings := mvel.Bindings{
		"AREA":            mustNumber(t, "1"),
		"DESTINATION":     mvel.Text("EUR"),
		"FLOOD":           mvel.Text("YES"),
		"NUM_OF_ADULTS":   mustNumber(t, "1"),
		"NUM_OF_CHILDREN": mustNumber(t, "1"),
		"NUM_OF_CLAIM":    mustNumber(t, "1"),
		"TYP":             mvel.Text("APT"),
	}

	// when
	got, err := expression.Evaluate(bindings)

	// then
	if err != nil || got.String() != "5" {
		t.Fatalf("seven-binding result = %s, error %v; want 5", got.String(), err)
	}

	// when
	bindings["EXTRA"] = mustNumber(t, "1")
	_, beyondCountErr := expression.Evaluate(bindings)

	// then
	if !errors.Is(beyondCountErr, mvel.ErrLimit) {
		t.Fatalf("eight-binding error = %v, want limit error", beyondCountErr)
	}
}

func nestedTernary(depth int) string {
	result := "1B"
	for range depth {
		result = "FLOOD=='Y'?1B:" + result
	}
	return result
}

func TestDecimal_PreservesSignedValueAndScale(t *testing.T) {
	// when
	got, err := mvel.ParseDecimal("-0.015")

	// then
	if err != nil || got.String() != "-0.015" || got.Scale() != 3 {
		t.Fatalf(
			"decimal = %s scale %d, error %v; want -0.015 scale 3",
			got,
			got.Scale(),
			err,
		)
	}
}

func TestCompile_ReportsSourcePosition(t *testing.T) {
	// when
	_, err := mvel.CompileDecimal("AREA *")

	// then
	if !errors.Is(err, mvel.ErrSyntax) || !strings.Contains(err.Error(), "at byte 6") {
		t.Fatalf("compile error = %v, want syntax error at byte 6", err)
	}
}

func TestParseDecimal_RejectsMalformedText(t *testing.T) {
	for _, text := range []string{"", "-", "1.", ".5", "1.2.3", "+1", "1e3", "1_000"} {
		t.Run(text, func(t *testing.T) {
			// when
			_, err := mvel.ParseDecimal(text)

			// then
			if !errors.Is(err, mvel.ErrSyntax) {
				t.Fatalf(
					"ParseDecimal(%q) error = %v, want syntax error",
					text,
					err,
				)
			}
		})
	}
}

func TestDecimal_ZeroValueIsUsable(t *testing.T) {
	// given
	expression, err := mvel.CompileDecimal("AREA + 1B")
	if err != nil {
		t.Fatal(err)
	}

	// when
	got, err := expression.Evaluate(mvel.Bindings{"AREA": mvel.Number(mvel.Decimal{})})

	// then
	if err != nil || got.String() != "1" || (mvel.Decimal{}).String() != "0" {
		t.Fatalf("zero-value evaluation = %s, error %v; want 1", got, err)
	}
}

func mustNumber(t *testing.T, text string) mvel.Value {
	t.Helper()
	decimal, err := mvel.ParseDecimal(text)
	if err != nil {
		t.Fatal(err)
	}
	return mvel.Number(decimal)
}

func assertPredicate(t *testing.T, predicate *mvel.Predicate, bindings mvel.Bindings, want bool) {
	t.Helper()
	got, err := predicate.Evaluate(bindings)
	if err != nil || got != want {
		t.Fatalf("predicate = %v, error %v; want %v", got, err, want)
	}
}
