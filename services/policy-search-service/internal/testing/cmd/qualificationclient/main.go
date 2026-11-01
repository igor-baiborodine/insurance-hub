// Command qualificationclient exercises deployed Policy Search transports from a Pod.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	policysearchv1 "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/gen/policysearch/v1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "", "http, grpc, health, or tcp")
	address := flag.String("address", "", "URL or host:port")
	query := flag.String(
		"query",
		"Synthetic Person",
		"policy query used by HTTP and gRPC checks",
	)
	expectDenied := flag.Bool("expect-denied", false, "require the TCP connection to fail")
	flag.Parse()
	if *address == "" {
		return fmt.Errorf("address is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch *mode {
	case "http":
		return checkHTTP(ctx, *address, *query)
	case "grpc":
		return checkGRPC(ctx, *address, *query)
	case "health":
		return checkHealth(ctx, *address)
	case "tcp":
		return checkTCP(ctx, *address, *expectDenied)
	default:
		return fmt.Errorf("unsupported mode")
	}
}

func checkHTTP(ctx context.Context, address, query string) error {
	statusCode, body, err := request(ctx, address, query)
	if err != nil {
		return err
	}
	var response policysearchv1.SearchPoliciesResponse
	if statusCode != http.StatusOK || json.Unmarshal(body, &response) != nil ||
		len(response.GetPolicies()) == 0 {
		return fmt.Errorf("HTTP policy search differed")
	}
	statusCode, _, err = request(ctx, address, "")
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK {
		return fmt.Errorf("empty HTTP policy query returned %d", statusCode)
	}
	return nil
}

func request(ctx context.Context, address, query string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		address+"?q="+url.QueryEscape(query),
		nil,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("create HTTP request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("send HTTP request: %w", err)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		_ = response.Body.Close()
		return 0, nil, fmt.Errorf("read HTTP response: %w", err)
	}
	if err := response.Body.Close(); err != nil {
		return 0, nil, fmt.Errorf("close HTTP response: %w", err)
	}
	return response.StatusCode, payload, nil
}

func checkGRPC(ctx context.Context, address, query string) error {
	connection, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("create gRPC client: %w", err)
	}
	defer func() { _ = connection.Close() }()
	client := policysearchv1.NewPolicySearchServiceClient(connection)
	response, err := client.SearchPolicies(
		ctx,
		&policysearchv1.SearchPoliciesRequest{Q: proto.String(query)},
	)
	if err != nil || len(response.GetPolicies()) == 0 {
		return fmt.Errorf("gRPC policy search differed: %w", err)
	}
	return nil
}

func checkHealth(ctx context.Context, address string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("send health request: %w", err)
	}
	if err := response.Body.Close(); err != nil {
		return fmt.Errorf("close health response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned %d", response.StatusCode)
	}
	return nil
}

func checkTCP(ctx context.Context, address string, expectDenied bool) error {
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if expectDenied {
		if err == nil {
			_ = connection.Close()
			return fmt.Errorf("excluded TCP caller connected")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("allowed TCP caller failed: %w", err)
	}
	return connection.Close()
}
