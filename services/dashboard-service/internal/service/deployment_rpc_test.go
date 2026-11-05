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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := dashboardv1.NewDashboardServiceClient(connection).GetTotalSales(
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
