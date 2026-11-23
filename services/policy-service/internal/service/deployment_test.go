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

	policyv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/policy/v1"
)

func TestDeployedDirectRPC_Echo(t *testing.T) {
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
	result, err := policyv1.NewExampleServiceClient(conn).Echo(
		ctx,
		&policyv1.EchoRequest{Message: "hello"},
	)

	// then
	if err != nil || result.GetMessage() != "hello" {
		t.Fatalf("direct Policy Echo = (%v, %v)", result, err)
	}
}
