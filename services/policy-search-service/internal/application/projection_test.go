package application

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
)

type policyViewWriterStub struct {
	documents map[string]domain.Policy
	err       error
	calls     int
}

func (stub *policyViewWriterStub) ReplacePolicy(
	_ context.Context,
	policy domain.Policy,
) error {
	stub.calls++
	if stub.err != nil {
		return stub.err
	}
	stub.documents[policy.Number] = policy
	return nil
}

func TestProjectPolicy_MapsReceivedValuesAndReplacesByNumber(t *testing.T) {
	// given
	writer := &policyViewWriterStub{documents: make(map[string]domain.Policy)}
	useCase, err := NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	registration := PolicyEvent{
		Number:       "SYN-230-001",
		From:         projectionDate(t, "2026-01-01"),
		To:           projectionDate(t, "2026-12-31"),
		PolicyHolder: "Synthetic Holder A",
	}
	termination := registration
	termination.To = projectionDate(t, "2026-06-30")

	// when
	for _, event := range []PolicyEvent{
		registration,
		termination,
		termination,
		registration,
	} {
		if err := useCase.Execute(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}

	// then
	want := domain.Policy{
		Number:       "SYN-230-001",
		DateFrom:     projectionDate(t, "2026-01-01"),
		DateTo:       projectionDate(t, "2026-12-31"),
		PolicyHolder: "Synthetic Holder A",
	}
	if writer.calls != 4 || len(writer.documents) != 1 ||
		!reflect.DeepEqual(writer.documents[registration.Number], want) {
		t.Fatalf(
			"calls=%d documents=%+v, want one final registration document %+v",
			writer.calls,
			writer.documents,
			want,
		)
	}
}

func TestProjectPolicy_PreservesOptionalDates(t *testing.T) {
	// given
	writer := &policyViewWriterStub{documents: make(map[string]domain.Policy)}
	useCase, err := NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	event := PolicyEvent{Number: "SYN-230-NULL", PolicyHolder: "Synthetic Holder"}

	// when
	err = useCase.Execute(context.Background(), event)

	// then
	got := writer.documents[event.Number]
	if err != nil || got.DateFrom != nil || got.DateTo != nil {
		t.Fatalf("project optional dates = %+v, %v", got, err)
	}
}

func TestProjectPolicy_PreservesCancellationAndInspectableFailure(t *testing.T) {
	// given
	cause := errors.New("synthetic writer failure")
	writer := &policyViewWriterStub{
		documents: make(map[string]domain.Policy),
		err:       cause,
	}
	useCase, err := NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// when
	canceledErr := useCase.Execute(canceled, PolicyEvent{Number: "SYN-230-CANCEL"})
	writeErr := useCase.Execute(
		context.Background(),
		PolicyEvent{Number: "SYN-230-FAIL"},
	)
	_, constructorErr := NewProjectPolicy(nil)

	// then
	if !errors.Is(canceledErr, context.Canceled) || !errors.Is(writeErr, cause) ||
		!errors.Is(constructorErr, ErrPolicyViewWriterRequired) {
		t.Fatalf("errors = %v / %v / %v", canceledErr, writeErr, constructorErr)
	}
	if writer.calls != 1 {
		t.Fatalf("writer calls = %d, want only the uncancelled failure", writer.calls)
	}
}

func projectionDate(t *testing.T, value string) *domain.Date {
	t.Helper()
	date, err := domain.ParseDate(value)
	if err != nil {
		t.Fatal(err)
	}
	return &date
}
