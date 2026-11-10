package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	policysearchv1 "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/gen/policysearch/v1"
)

func runCheck(ctx context.Context, settings options) error {
	if settings.address == "" {
		return errors.New("address is required")
	}
	switch settings.mode {
	case "http":
		return checkHTTP(ctx, settings.address, settings.query)
	case "grpc":
		return checkGRPC(ctx, settings.address, settings.query)
	case "health":
		return checkHealth(ctx, settings.address)
	case "tcp":
		return checkTCP(ctx, settings.address, settings.expectDenied)
	default:
		return errors.New("unsupported mode")
	}
}

func checkHTTP(ctx context.Context, address, query string) error {
	statusCode, body, err := request(ctx, address, query)
	if err != nil {
		return err
	}
	var response struct {
		Policies []struct {
			Number string `json:"number"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(body, &response); err != nil || statusCode != http.StatusOK ||
		len(response.Policies) == 0 {
		return fmt.Errorf("HTTP policy search differed: status=%d", statusCode)
	}
	return nil
}

func checkGRPC(ctx context.Context, address, query string) error {
	connection, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("connect to gRPC: %w", err)
	}
	defer func() { _ = connection.Close() }()
	response, err := policysearchv1.NewPolicySearchServiceClient(connection).SearchPolicies(
		ctx,
		&policysearchv1.SearchPoliciesRequest{Q: proto.String(query)},
	)
	if err != nil {
		return fmt.Errorf("gRPC policy search failed: %w", err)
	}
	if len(response.GetPolicies()) == 0 {
		return errors.New("gRPC policy search returned no policies")
	}
	return nil
}

func checkHealth(ctx context.Context, address string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned %d", response.StatusCode)
	}
	return nil
}

func checkTCP(ctx context.Context, address string, expectDenied bool) error {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err == nil {
		_ = connection.Close()
	}
	if expectDenied && err == nil {
		return errors.New("TCP connection was unexpectedly accepted")
	}
	if !expectDenied && err != nil {
		return fmt.Errorf("TCP connection failed: %w", err)
	}
	return nil
}

func request(ctx context.Context, address, query string) (int, []byte, error) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return 0, nil, errors.New("HTTP address is invalid")
	}
	parameters := parsed.Query()
	parameters.Set("q", query)
	parsed.RawQuery = parameters.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return 0, nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil {
		return 0, nil, err
	}
	if len(body) > 64*1024 {
		return 0, nil, errors.New("HTTP response exceeded limit")
	}
	return response.StatusCode, body, nil
}
