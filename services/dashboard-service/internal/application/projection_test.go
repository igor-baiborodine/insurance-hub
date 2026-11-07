package application

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

type projectionWriterStub struct {
	documents map[string]domain.PolicyProjection
	err       error
	calls     int
}

func (stub *projectionWriterStub) ReplacePolicy(
	_ context.Context,
	policy domain.PolicyProjection,
) error {
	stub.calls++
	if stub.err != nil {
		return stub.err
	}
	stub.documents[policy.Number] = policy
	return nil
}

func TestProjectPolicy_MapsAllReceivedValuesAndReplacesByNumber(t *testing.T) {
	// given
	writer := &projectionWriterStub{documents: make(map[string]domain.PolicyProjection)}
	useCase, err := NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	from := projectionDate(t, "2026-01-01")
	to := projectionDate(t, "2026-12-31")
	holder := "Synthetic Holder A"
	product := "HSI"
	agent := "synthetic-agent"
	premium := projectionPremium(t, "9007199254740993.1234567")
	event := PolicyEvent{
		Number: "SYN-290-001", From: from, To: to, PolicyHolder: &holder,
		ProductCode: &product, TotalPremium: premium, AgentLogin: &agent,
	}
	replacement := event
	replacement.AgentLogin = nil

	// when
	if err := useCase.Execute(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	err = useCase.Execute(context.Background(), replacement)

	// then
	want := domain.PolicyProjection{
		Number: event.Number, From: from, To: to, PolicyHolder: &holder,
		ProductCode: &product, TotalPremium: premium, AgentLogin: nil,
	}
	if err != nil || writer.calls != 2 || len(writer.documents) != 1 ||
		!reflect.DeepEqual(writer.documents[event.Number], want) {
		t.Fatalf(
			"projection = %+v, calls=%d, error=%v; want %+v",
			writer.documents,
			writer.calls,
			err,
			want,
		)
	}
}

func TestProjectPolicy_PreservesNullAndEmptyValues(t *testing.T) {
	// given
	writer := &projectionWriterStub{documents: make(map[string]domain.PolicyProjection)}
	useCase, err := NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	empty := ""

	// when
	err = useCase.Execute(context.Background(), PolicyEvent{
		Number: "123", PolicyHolder: nil, ProductCode: &empty, AgentLogin: &empty,
	})

	// then
	got := writer.documents["123"]
	if err != nil || got.PolicyHolder != nil || got.ProductCode == nil ||
		*got.ProductCode != "" || got.AgentLogin == nil || *got.AgentLogin != "" ||
		got.TotalPremium != nil {
		t.Fatalf("null and empty projection = %+v, %v", got, err)
	}
}

func TestProjectPolicy_RejectsIdentityAndPreservesFailures(t *testing.T) {
	// given
	cause := errors.New("synthetic writer failure")
	writer := &projectionWriterStub{
		documents: make(map[string]domain.PolicyProjection),
		err:       cause,
	}
	useCase, err := NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// when
	missingErr := useCase.Execute(context.Background(), PolicyEvent{})
	canceledErr := useCase.Execute(canceled, PolicyEvent{Number: "SYN-290-CANCEL"})
	writeErr := useCase.Execute(context.Background(), PolicyEvent{Number: "SYN-290-FAIL"})
	_, constructorErr := NewProjectPolicy(nil)

	// then
	if !errors.Is(missingErr, ErrPolicyNumberRequired) ||
		!errors.Is(canceledErr, context.Canceled) || !errors.Is(writeErr, cause) ||
		!errors.Is(constructorErr, ErrPolicyProjectionWriterRequired) {
		t.Fatalf(
			"errors = %v / %v / %v / %v",
			missingErr,
			canceledErr,
			writeErr,
			constructorErr,
		)
	}
	if writer.calls != 1 {
		t.Fatalf(
			"writer calls = %d, want only uncancelled valid-identity failure",
			writer.calls,
		)
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

func projectionPremium(t *testing.T, value string) *domain.Premium {
	t.Helper()
	premium, err := domain.ParsePremium(value)
	if err != nil {
		t.Fatal(err)
	}
	return &premium
}
