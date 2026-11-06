package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

type stubAgentReader struct {
	buckets []domain.AgentSales
	err     error
	query   application.AgentSalesQuery
}

func (reader *stubAgentReader) ReadAgentSales(
	_ context.Context,
	query application.AgentSalesQuery,
) ([]domain.AgentSales, error) {
	reader.query = query
	return append([]domain.AgentSales(nil), reader.buckets...), reader.err
}

func TestGetAgentSales_PreservesExactBucketsAndQuery(t *testing.T) {
	// given
	amount, _ := domain.NewAgentPremiumAmount("1.005")
	value, _ := domain.NewAgentSalesValue(2, amount)
	reader := &stubAgentReader{buckets: []domain.AgentSales{{AgentLogin: "", Value: value}}}
	useCase, err := application.NewGetAgentSales(reader)
	if err != nil {
		t.Fatal(err)
	}
	from, _ := domain.ParseDate("2026-01-01")
	to, _ := domain.ParseDate("2027-01-01")
	query := application.AgentSalesQuery{
		Agent: domain.ExactAgent(""), Product: domain.ExactProduct(" SAFE "),
		DateFrom: from, DateTo: to,
	}

	// when
	got, err := useCase.Execute(context.Background(), query)
	// then
	if err != nil {
		t.Fatal(err)
	}
	gotValue, present := got.PerAgentTotal[""]
	if !present || gotValue.PoliciesCount() != 2 ||
		gotValue.PremiumAmount().String() != "1.005" ||
		reader.query != query {
		t.Fatalf("result/query mismatch: result=%#v query=%#v", got, reader.query)
	}
}

func TestGetAgentSales_PreservesEmptyAndRejectsDuplicateAgents(t *testing.T) {
	// given
	amount, _ := domain.NewAgentPremiumAmount("0.0")
	value, _ := domain.NewAgentSalesValue(1, amount)
	emptyUseCase, _ := application.NewGetAgentSales(&stubAgentReader{})
	duplicateUseCase, _ := application.NewGetAgentSales(
		&stubAgentReader{buckets: []domain.AgentSales{
			{AgentLogin: "agent", Value: value}, {AgentLogin: "agent", Value: value},
		}},
	)

	// when
	empty, emptyErr := emptyUseCase.Execute(context.Background(), application.AgentSalesQuery{})
	_, duplicateErr := duplicateUseCase.Execute(
		context.Background(),
		application.AgentSalesQuery{},
	)

	// then
	if emptyErr != nil || len(empty.PerAgentTotal) != 0 {
		t.Fatalf("empty result = (%#v, %v)", empty, emptyErr)
	}
	if !errors.Is(duplicateErr, application.ErrDuplicateAgent) {
		t.Fatalf("duplicate error = %v", duplicateErr)
	}
}
