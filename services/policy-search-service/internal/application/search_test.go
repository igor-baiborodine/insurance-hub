package application

import (
	"context"
	"errors"
	"math/rand"
	"slices"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
)

type searcherStub struct {
	policies []domain.Policy
	err      error
	query    string
}

func (stub *searcherStub) SearchPolicies(
	_ context.Context,
	query string,
) ([]domain.Policy, error) {
	stub.query = query
	return stub.policies, stub.err
}

func TestSearchPolicies_OrdersSelectedHitsByDescendingDateWithStableNulls(t *testing.T) {
	// given
	selected := []domain.Policy{
		{Number: "older", DateFrom: date(t, "2023-01-01")},
		{Number: "null-first"},
		{Number: "z-equal-first", DateFrom: date(t, "2025-06-01")},
		{Number: "newest", DateFrom: date(t, "2026-07-01")},
		{Number: "a-equal-second", DateFrom: date(t, "2025-06-01")},
		{Number: "null-second"},
	}
	reader := &searcherStub{policies: selected}
	useCase, err := NewSearchPolicies(reader)
	if err != nil {
		t.Fatal(err)
	}

	// when
	got, err := useCase.Execute(context.Background(), `holder:"Synthetic Person"`)
	// then
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"newest", "z-equal-first", "a-equal-second", "older", "null-first", "null-second",
	}
	if reader.query != `holder:"Synthetic Person"` || !slices.Equal(numbers(got), want) {
		t.Fatalf(
			"query=%q order=%v, want query preserved and order=%v",
			reader.query,
			numbers(got),
			want,
		)
	}
	if !slices.Equal(numbers(selected), []string{
		"older", "null-first", "z-equal-first", "newest", "a-equal-second", "null-second",
	}) {
		t.Fatal("use case mutated the searcher's selected-hit slice")
	}
}

func TestSearchPolicies_DoesNotReselectAfterOrdering(t *testing.T) {
	// given
	selected := make([]domain.Policy, 101)
	for index := range selected {
		selected[index] = domain.Policy{Number: string(rune(index + 1))}
	}
	useCase, err := NewSearchPolicies(&searcherStub{policies: selected})
	if err != nil {
		t.Fatal(err)
	}

	// when
	ordered, err := useCase.Execute(context.Background(), "*")

	// then
	if err != nil || len(ordered) != len(selected) {
		t.Fatalf("ordered length = %d, error = %v", len(ordered), err)
	}
}

func TestSearchPolicies_PropertyPreservesMultisetAndEqualDateOrder(t *testing.T) {
	// given
	random := rand.New(rand.NewSource(229))
	for sample := range 200 {
		selected := make([]domain.Policy, 1+random.Intn(100))
		for index := range selected {
			selected[index].Number = string(rune(index + 1))
			if random.Intn(5) != 0 {
				selected[index].DateFrom = date(t, []string{
					"2022-01-01", "2023-01-01", "2024-01-01", "2025-01-01",
				}[random.Intn(4)])
			}
		}
		reader := &searcherStub{policies: selected}
		useCase, err := NewSearchPolicies(reader)
		if err != nil {
			t.Fatal(err)
		}

		// when
		ordered, err := useCase.Execute(context.Background(), "*")
		// then
		if err != nil {
			t.Fatalf("sample %d: %v", sample, err)
		}
		assertStableDescendingPermutation(t, sample, selected, ordered)
	}
}

func TestSearchPolicies_PreservesCancellationAndInspectableFailure(t *testing.T) {
	// given
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cause := errors.New("synthetic reader failure")
	failed, err := NewSearchPolicies(&searcherStub{err: cause})
	if err != nil {
		t.Fatal(err)
	}

	// when
	_, canceledErr := failed.Execute(canceled, "*")
	_, searchErr := failed.Execute(context.Background(), "*")

	// then
	if !errors.Is(canceledErr, context.Canceled) || !errors.Is(searchErr, cause) {
		t.Fatalf("errors = %v / %v", canceledErr, searchErr)
	}
	if _, err := NewSearchPolicies(nil); !errors.Is(err, ErrSearcherRequired) {
		t.Fatalf("NewSearchPolicies(nil) error = %v", err)
	}
}

func date(t *testing.T, value string) *domain.Date {
	t.Helper()
	parsed, err := domain.ParseDate(value)
	if err != nil {
		t.Fatal(err)
	}
	return &parsed
}

func numbers(policies []domain.Policy) []string {
	result := make([]string, len(policies))
	for index := range policies {
		result[index] = policies[index].Number
	}
	return result
}

func assertStableDescendingPermutation(
	t *testing.T,
	sample int,
	selected []domain.Policy,
	ordered []domain.Policy,
) {
	t.Helper()
	if len(ordered) != len(selected) {
		t.Fatalf("sample %d: length=%d, want %d", sample, len(ordered), len(selected))
	}
	positions := make(map[string]int, len(selected))
	seen := make(map[string]bool, len(selected))
	for index, policy := range selected {
		positions[policy.Number] = index
	}
	for index, policy := range ordered {
		if seen[policy.Number] {
			t.Fatalf("sample %d: duplicate %q", sample, policy.Number)
		}
		seen[policy.Number] = true
		if index == 0 {
			continue
		}
		previous := ordered[index-1]
		switch {
		case previous.DateFrom == nil && policy.DateFrom != nil:
			t.Fatalf("sample %d: non-null date follows null", sample)
		case sameDate(previous.DateFrom, policy.DateFrom) &&
			positions[previous.Number] > positions[policy.Number]:
			t.Fatalf("sample %d: equal-date input order changed", sample)
		case previous.DateFrom != nil && policy.DateFrom != nil &&
			policy.DateFrom.After(*previous.DateFrom):
			t.Fatalf("sample %d: dates are not descending", sample)
		}
	}
	if len(seen) != len(positions) {
		t.Fatalf("sample %d: selected-hit multiset changed", sample)
	}
}

func sameDate(left, right *domain.Date) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.String() == right.String()
}
