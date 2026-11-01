//go:build integration

package testing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	policyelasticsearch "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/elasticsearch"
)

func TestElasticsearchQuery_UsesAcceptedSelectionAndOrdering(t *testing.T) {
	// given
	endpoint := os.Getenv("POLICY_SEARCH_TEST_ELASTICSEARCH_URL")
	if endpoint == "" {
		t.Fatal("POLICY_SEARCH_TEST_ELASTICSEARCH_URL is required")
	}
	client, err := policyelasticsearch.New(policyelasticsearch.Config{
		Endpoint: endpoint,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("construct Elasticsearch client: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	createPolicyIndex(t, ctx, client)
	indexQueryFixtures(t, ctx, client)
	searcher, err := policyelasticsearch.NewSearcher(client)
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := application.NewSearchPolicies(searcher)
	if err != nil {
		t.Fatal(err)
	}

	// when
	numberMatches, err := searcher.SearchPolicies(ctx, "NUMBERONLYTOKEN")
	if err != nil {
		t.Fatal(err)
	}
	holderMatches, err := searcher.SearchPolicies(ctx, `"Encoded Holder"`)
	if err != nil {
		t.Fatal(err)
	}
	emptyMatches, err := searcher.SearchPolicies(ctx, "NO_MATCH_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	ignoredFieldMatches, err := searcher.SearchPolicies(ctx, "IGNORED_FIELD_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := searcher.SearchPolicies(ctx, "capfixture")
	if err != nil {
		t.Fatal(err)
	}
	ordered, err := useCase.Execute(ctx, "orderfixture")
	// then
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(policyNumbers(numberMatches), []string{"NUMBERONLYTOKEN"}) {
		t.Fatalf("number-field matches = %v", policyNumbers(numberMatches))
	}
	if !slices.Equal(policyNumbers(holderMatches), []string{"OTHER-NUMBER"}) {
		t.Fatalf("holder-field matches = %v", policyNumbers(holderMatches))
	}
	if len(emptyMatches) != 0 || len(ignoredFieldMatches) != 0 {
		t.Fatalf(
			"empty matches = %d, ignored-field matches = %d",
			len(emptyMatches),
			len(ignoredFieldMatches),
		)
	}
	if len(selected) != 100 {
		t.Fatalf("selected hits = %d, want accepted cap 100", len(selected))
	}
	assertDescendingStableSelectedHits(t, ordered)
}

func createPolicyIndex(t *testing.T, ctx context.Context, client *policyelasticsearch.Client) {
	t.Helper()
	const mapping = `{
		"mappings": {
			"properties": {
				"number": {"type": "keyword"},
				"policyHolder": {"type": "text"},
				"dateFrom": {"type": "date", "format": "strict_date"},
				"dateTo": {"type": "date", "format": "strict_date"}
			}
		}
	}`
	response, err := client.Indices.Create(
		policyelasticsearch.PolicyIndex,
		client.Indices.Create.WithContext(ctx),
		client.Indices.Create.WithBody(strings.NewReader(mapping)),
	)
	if err != nil {
		t.Fatalf("create policy index: %v", err)
	}
	assertElasticsearchResponseOK(t, response.StatusCode, response.Body, "create policy index")
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, cleanupErr := client.Indices.Delete(
			[]string{policyelasticsearch.PolicyIndex},
			client.Indices.Delete.WithContext(cleanupCtx),
		)
		if cleanupErr != nil {
			t.Errorf("delete policy index: %v", cleanupErr)
			return
		}
		assertElasticsearchResponseOK(
			t,
			response.StatusCode,
			response.Body,
			"delete policy index",
		)
	})
}

func indexQueryFixtures(t *testing.T, ctx context.Context, client *policyelasticsearch.Client) {
	t.Helper()
	var bulk strings.Builder
	writeBulkPolicy(&bulk, "number-only", map[string]any{
		"number": "NUMBERONLYTOKEN", "policyHolder": "Different Holder",
	})
	writeBulkPolicy(&bulk, "holder-only", map[string]any{
		"number": "OTHER-NUMBER", "policyHolder": "Encoded Holder HOLDERONLYTOKEN",
	})
	writeBulkPolicy(&bulk, "ignored-field", map[string]any{
		"number":       "VISIBLE",
		"policyHolder": "Different Holder",
		"ignored":      "IGNORED_FIELD_TOKEN",
	})
	for index, fixture := range []map[string]any{
		{"number": "older", "policyHolder": "orderfixture", "dateFrom": "2023-01-01"},
		{"number": "null-first", "policyHolder": "orderfixture"},
		{"number": "z-equal-first", "policyHolder": "orderfixture", "dateFrom": "2025-06-01"},
		{"number": "newest", "policyHolder": "orderfixture", "dateFrom": "2026-07-01"},
		{"number": "a-equal-second", "policyHolder": "orderfixture", "dateFrom": "2025-06-01"},
		{"number": "null-second", "policyHolder": "orderfixture"},
	} {
		writeBulkPolicy(&bulk, fmt.Sprintf("order-%d", index), fixture)
	}
	for index := range 105 {
		writeBulkPolicy(&bulk, fmt.Sprintf("cap-%03d", index), map[string]any{
			"number":       fmt.Sprintf("CAP-%03d", index),
			"policyHolder": "capfixture",
			"dateFrom":     fmt.Sprintf("2024-01-%02d", 1+(index%28)),
		})
	}
	response, err := client.Bulk(
		strings.NewReader(bulk.String()),
		client.Bulk.WithContext(ctx),
		client.Bulk.WithRefresh("true"),
	)
	if err != nil {
		t.Fatalf("bulk index query fixtures: %v", err)
	}
	defer response.Body.Close()
	if response.IsError() {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("bulk index query fixtures returned %s: %s", response.Status(), payload)
	}
	var result struct {
		Errors bool `json:"errors"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode bulk response: %v", err)
	}
	if result.Errors {
		t.Fatal("bulk index query fixtures reported an item failure")
	}
}

func writeBulkPolicy(body *strings.Builder, id string, source map[string]any) {
	metadata, err := json.Marshal(map[string]any{
		"index": map[string]any{"_index": policyelasticsearch.PolicyIndex, "_id": id},
	})
	if err != nil {
		panic(err)
	}
	payload, err := json.Marshal(source)
	if err != nil {
		panic(err)
	}
	body.Write(metadata)
	body.WriteByte('\n')
	body.Write(payload)
	body.WriteByte('\n')
}

func policyNumbers(policies []domain.Policy) []string {
	result := make([]string, len(policies))
	for index := range policies {
		result[index] = policies[index].Number
	}
	return result
}

func assertDescendingStableSelectedHits(t *testing.T, ordered []domain.Policy) {
	t.Helper()
	want := []string{
		"newest", "z-equal-first", "a-equal-second", "older", "null-first", "null-second",
	}
	if got := policyNumbers(ordered); !slices.Equal(got, want) {
		t.Fatalf("ordered policy numbers = %v, want %v", got, want)
	}
}

func assertElasticsearchResponseOK(
	t *testing.T,
	status int,
	body io.ReadCloser,
	operation string,
) {
	t.Helper()
	defer body.Close()
	if status >= 200 && status < 300 {
		return
	}
	payload, _ := io.ReadAll(io.LimitReader(body, 4096))
	t.Fatalf("%s returned status %d: %s", operation, status, payload)
}
