//go:build deployment

package service_test

import (
	"context"
	"os"
	"testing"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
)

func TestDeployedDirectRPC(t *testing.T) {
	client := deployedClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	assertOwnedTotalSales(t, ctx, client)
	assertOwnedAgentSales(t, ctx, client)
	assertOwnedSalesTrends(t, ctx, client)
}

func TestDeployedDirectRPCSharedSmoke(t *testing.T) {
	client := deployedClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.GetTotalSales(ctx, &dashboardv1.GetTotalSalesRequest{
		SaleDateFrom: "2019-01-01", SaleDateTo: "2020-01-01",
	}); err != nil {
		t.Fatalf("GetTotalSales: %v", err)
	}
	if _, err := client.GetAgentSales(ctx, &dashboardv1.GetAgentSalesRequest{
		SaleDateFrom: "2019-01-01", SaleDateTo: "2020-01-01",
	}); err != nil {
		t.Fatalf("GetAgentSales: %v", err)
	}
	if _, err := client.GetSalesTrends(ctx, &dashboardv1.GetSalesTrendsRequest{
		SaleDateFrom: "2019-01-01", SaleDateTo: "2020-01-01", AggregationUnitCode: "MONTH",
	}); err != nil {
		t.Fatalf("GetSalesTrends: %v", err)
	}
}

func deployedClient(t *testing.T) dashboardv1.DashboardServiceClient {
	t.Helper()
	address := os.Getenv("DASHBOARD_DEPLOYMENT_GRPC_ADDR")
	if address == "" {
		t.Fatal("DASHBOARD_DEPLOYMENT_GRPC_ADDR is required")
	}

	connection, err := grpcgo.NewClient(
		address,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("create gRPC client: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return dashboardv1.NewDashboardServiceClient(connection)
}

func assertOwnedTotalSales(
	t *testing.T,
	ctx context.Context,
	client dashboardv1.DashboardServiceClient,
) {
	t.Helper()
	response, err := client.GetTotalSales(
		ctx,
		&dashboardv1.GetTotalSalesRequest{
			SaleDateFrom: "2019-01-01",
			SaleDateTo:   "2020-01-01",
		},
	)
	if err != nil {
		t.Fatalf("GetTotalSales: %v", err)
	}

	total := response.GetTotal()
	if total.GetPoliciesCount() != 1 || total.GetPremiumAmountMinorUnits() != 100_000 {
		t.Fatalf("total = %v, want one policy and 100000 minor units", total)
	}
	perProduct := response.GetPerProductTotal()
	if len(perProduct) != 1 {
		t.Fatalf("per-product result length = %d, want 1", len(perProduct))
	}
	safe := perProduct["SAFE"]
	if safe.GetPoliciesCount() != 1 || safe.GetPremiumAmountMinorUnits() != 100_000 {
		t.Fatalf("SAFE total = %v, want one policy and 100000 minor units", safe)
	}
}

func assertOwnedAgentSales(
	t *testing.T,
	ctx context.Context,
	client dashboardv1.DashboardServiceClient,
) {
	t.Helper()
	response, err := client.GetAgentSales(ctx, &dashboardv1.GetAgentSalesRequest{
		AgentLogin: stringPointer("safe-agent"), ProductCode: stringPointer("SAFE"),
		SaleDateFrom: "2019-01-01", SaleDateTo: "2020-01-01",
	})
	if err != nil {
		t.Fatalf("GetAgentSales: %v", err)
	}
	if len(response.GetPerAgentTotal()) != 1 {
		t.Fatalf("per-agent result length = %d, want 1", len(response.GetPerAgentTotal()))
	}
	safe := response.GetPerAgentTotal()["safe-agent"]
	if safe.GetPoliciesCount() != 1 || safe.GetPremiumAmount() != "1000.0" {
		t.Fatalf("safe-agent total = %v, want one policy and exact 1000.0", safe)
	}
}

func assertOwnedSalesTrends(
	t *testing.T,
	ctx context.Context,
	client dashboardv1.DashboardServiceClient,
) {
	t.Helper()
	response, err := client.GetSalesTrends(ctx, &dashboardv1.GetSalesTrendsRequest{
		ProductCode: stringPointer("SAFE"), SaleDateFrom: "2019-01-01",
		SaleDateTo: "2020-01-01", AggregationUnitCode: "MONTH",
	})
	if err != nil {
		t.Fatalf("GetSalesTrends: %v", err)
	}
	periods := response.GetPeriodSales()
	if len(periods) != 1 {
		t.Fatalf("period-sales result length = %d, want 1", len(periods))
	}
	period := periods[0]
	if period.GetPeriodDate() != "2019-06-01" ||
		period.GetPeriod() != "2019-06-01T00:00:00.000Z" ||
		period.GetPoliciesCount() != 1 || period.GetPremiumAmount() != "1000.00" {
		t.Fatalf("sales-trends period = %v, want exact June fixture", period)
	}
}

func stringPointer(value string) *string { return &value }
