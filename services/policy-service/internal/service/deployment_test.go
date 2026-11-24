//go:build deployment

package service_test

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	policyv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/policy/v1"
)

// TestDeployedDirectRPC_CreateOffer reaches the deployed gRPC listener with a request that schema
// validation rejects, so it never calls a provider or writes an offer.
func TestDeployedDirectRPC_CreateOffer(t *testing.T) {
	// given
	address := os.Getenv("POLICY_SMOKE_GRPC_ADDR")
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" || port == "" {
		t.Fatal("Policy smoke requires a loopback address")
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
	result, err := policyv1.NewPolicyServiceClient(conn).CreateOffer(
		ctx,
		&policyv1.CreateOfferRequest{},
	)

	// then
	if result != nil || status.Code(err) != codes.InvalidArgument ||
		status.Convert(err).Message() != "invalid request" {
		t.Fatalf("direct invalid Policy offer = (%v, %v)", result, err)
	}
}
