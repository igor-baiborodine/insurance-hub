//go:build integration

package service

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	policysearchv1 "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/gen/policysearch/v1"
	policyelasticsearch "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/elasticsearch"
)

func TestSearchRuntime_UsesRealRPCGatewayAndElasticsearch(t *testing.T) {
	endpoint := os.Getenv("POLICY_SEARCH_TEST_ELASTICSEARCH_URL")
	if endpoint == "" {
		t.Fatal("POLICY_SEARCH_TEST_ELASTICSEARCH_URL is required")
	}
	fixtureRequest(t, http.MethodPut, endpoint+"/"+policyelasticsearch.PolicyIndex, `{
		"mappings":{"properties":{
			"number":{"type":"keyword"},
			"policyHolder":{"type":"text"},
			"dateFrom":{"type":"date","format":"strict_date"},
			"dateTo":{"type":"date","format":"strict_date"}
		}}
	}`)
	t.Cleanup(func() {
		fixtureRequest(
			t,
			http.MethodDelete,
			endpoint+"/"+policyelasticsearch.PolicyIndex,
			"",
		)
	})
	fixtureRequest(
		t,
		http.MethodPut,
		endpoint+"/"+policyelasticsearch.PolicyIndex+"/_doc/accepted?refresh=true",
		`{"number":"POL-229","policyHolder":"Synthetic Person","dateFrom":"2024-02-29"}`,
	)
	fixtureRequest(
		t,
		http.MethodPut,
		endpoint+"/"+policyelasticsearch.PolicyIndex+"/_doc/nulls?refresh=true",
		`{"number":"","policyHolder":"Null Fixture","dateFrom":null,"dateTo":null}`,
	)

	settings := testConfig(t)
	settings.Elasticsearch.URL = endpoint
	settings.Elasticsearch.Timeout = 5 * time.Second
	deps := productionDependencies()
	addresses := make(chan string, 3)
	deps.listen = func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- run(ctx, settings, discardLogger(), deps) }()
	grpcAddress := receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	waitForHTTPStatus(t, "http://"+healthAddress+"/readyz", http.StatusOK)

	response, err := http.Get("http://" + httpAddress + "/policies?q=Synthetic%20Person")
	if err != nil {
		t.Fatal(err)
	}
	assertResponse(
		t,
		response,
		http.StatusOK,
		`{"policies":[{"number":"POL-229","dateFrom":"2024-02-29","policyHolder":"Synthetic Person"}]}`,
	)
	nulls, err := http.Get("http://" + httpAddress + "/policies?q=Null%20Fixture")
	if err != nil {
		t.Fatal(err)
	}
	assertResponse(t, nulls, http.StatusOK, `{"policies":[{"policyHolder":"Null Fixture"}]}`)
	zero, err := http.Get("http://" + httpAddress + "/policies?q=NO_MATCH_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	assertResponse(t, zero, http.StatusOK, `{}`)
	malformed, err := http.Get("http://" + httpAddress + "/policies?q=%28")
	if err != nil {
		t.Fatal(err)
	}
	assertResponse(
		t,
		malformed,
		http.StatusInternalServerError,
		`{"message":"Internal Server Error","_links":{"self":{"href":"/policies","templated":false}}}`,
	)

	connection, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	client := policysearchv1.NewPolicySearchServiceClient(connection)
	direct, err := client.SearchPolicies(
		context.Background(),
		&policysearchv1.SearchPoliciesRequest{Q: proto.String("POL-229")},
	)
	if err != nil || len(direct.GetPolicies()) != 1 ||
		direct.GetPolicies()[0].GetNumber() != "POL-229" {
		t.Fatalf("direct search = (%v, %v)", direct, err)
	}
	empty, err := client.SearchPolicies(
		context.Background(),
		&policysearchv1.SearchPoliciesRequest{Q: proto.String("NO_MATCH_TOKEN")},
	)
	if err != nil || len(empty.GetPolicies()) != 0 {
		t.Fatalf("direct zero-hit search = (%v, %v)", empty, err)
	}
	_, err = client.SearchPolicies(
		context.Background(),
		&policysearchv1.SearchPoliciesRequest{Q: proto.String("(")},
	)
	if status.Code(err) != codes.Internal ||
		strings.Contains(status.Convert(err).Message(), "query") {
		t.Fatalf("malformed direct status = (%s, %v)", status.Code(err), err)
	}
	_ = connection.Close()
	cancel()
	if err := receiveError(t, result); err != nil {
		t.Fatalf("run = %v", err)
	}
}

func fixtureRequest(t *testing.T, method, target, body string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("%s %s returned %d: %s", method, target, response.StatusCode, payload)
	}
}
