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

	paymentv1 "github.com/igor-baiborodine/insurance-hub/services/payment-service/gen/payment/v1"
)

func TestDeployedDirectRPC_ListAccounts(t *testing.T) {
	// given
	address := os.Getenv("PAYMENT_SMOKE_GRPC_ADDR")
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" || port == "" {
		t.Fatal("Payment smoke requires a loopback address")
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
	result, err := paymentv1.NewPaymentServiceClient(conn).ListAccounts(
		ctx,
		&paymentv1.ListAccountsRequest{},
	)

	// then
	if err != nil || result == nil {
		t.Fatalf("direct Payment account list = (%v, %v)", result, err)
	}
}
