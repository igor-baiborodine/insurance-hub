// Command qualificationclient exercises deployed Policy Search transports from a Pod.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

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
	expectDenied := flag.Bool("expect-denied", false, "require the TCP connection to fail")
	flag.Parse()
	if *address == "" {
		return fmt.Errorf("address is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switch *mode {
	case "http":
		return checkHTTP(ctx, *address)
	case "grpc":
		return checkGRPC(ctx, *address)
	case "health":
		return checkHealth(ctx, *address)
	case "tcp":
		return checkTCP(ctx, *address, *expectDenied)
	default:
		return fmt.Errorf("unsupported mode")
	}
}

func checkHTTP(ctx context.Context, address string) error {
	statusCode, body, err := request(ctx, address, []byte(`{"message":"hello"}`))
	if err != nil {
		return err
	}
	var response map[string]string
	if statusCode != http.StatusOK || json.Unmarshal(body, &response) != nil ||
		response["message"] != "hello" {
		return fmt.Errorf("valid HTTP Echo differed")
	}
	statusCode, _, err = request(ctx, address, []byte(`{"message":""}`))
	if err != nil {
		return err
	}
	if statusCode != http.StatusBadRequest {
		return fmt.Errorf("invalid HTTP Echo returned %d", statusCode)
	}
	return nil
}

func request(ctx context.Context, address string, body []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		address,
		bytes.NewReader(body),
	)
	if err != nil {
		return 0, nil, fmt.Errorf("create HTTP request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
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

func checkGRPC(ctx context.Context, address string) error {
	connection, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("create gRPC client: %w", err)
	}
	defer func() { _ = connection.Close() }()
	client := policysearchv1.NewExampleServiceClient(connection)
	response, err := client.Echo(ctx, &policysearchv1.EchoRequest{Message: "hello"})
	if err != nil || response.GetMessage() != "hello" {
		return fmt.Errorf("valid gRPC Echo differed: %w", err)
	}
	_, err = client.Echo(ctx, &policysearchv1.EchoRequest{Message: strings.Repeat("é", 65)})
	if status.Code(err) != codes.InvalidArgument {
		return fmt.Errorf("oversized gRPC Echo returned %s", status.Code(err))
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
