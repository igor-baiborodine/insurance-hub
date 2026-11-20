//go:build deployment

package service_test

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pricingv1 "github.com/igor-baiborodine/insurance-hub/services/pricing-service/gen/pricing/v1"
)

func TestDeployedDirectRPC_CalculatePrice(t *testing.T) {
	// given
	address := os.Getenv("PRICING_SMOKE_GRPC_ADDR")
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" || port == "" {
		t.Fatal("Pricing smoke requires a loopback address")
	}
	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// when
	result, err := pricingv1.NewPricingServiceClient(conn).CalculatePrice(
		ctx,
		&pricingv1.CalculatePriceRequest{
			ProductCode: "TRI", PolicyFrom: "2017-04-16", PolicyTo: "2018-04-15",
			SelectedCovers: []string{"C1", "C2"},
			Answers: []*pricingv1.Answer{
				{
					QuestionCode: "NUM_OF_ADULTS",
					Value:        &pricingv1.Answer_Number{Number: "1"},
				},
				{
					QuestionCode: "NUM_OF_CHILDREN",
					Value:        &pricingv1.Answer_Number{Number: "1"},
				},
				{
					QuestionCode: "DESTINATION",
					Value:        &pricingv1.Answer_Text{Text: "EUR"},
				},
			},
		},
	)

	// then
	if err != nil || result.GetTotalPrice() != "78" ||
		result.GetCoversPrices()["C1"] != "26" || result.GetCoversPrices()["C2"] != "52" {
		t.Fatalf("direct Pricing quote = (%v, %v)", result, err)
	}
}
