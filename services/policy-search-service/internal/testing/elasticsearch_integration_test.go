//go:build integration

package testing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	policyelasticsearch "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/elasticsearch"
)

func TestElasticsearchFoundation_IndexesAndReadsSyntheticDocument(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	index := fmt.Sprintf("policy-step4-%d", os.Getpid())
	createBody := `{
		"mappings": {
			"properties": {
				"number": {"type": "keyword"},
				"policyHolder": {"type": "text"},
				"dateFrom": {"type": "date", "format": "strict_date"},
				"dateTo": {"type": "date", "format": "strict_date"}
			}
		}
	}`

	// when
	createResponse, err := client.Indices.Create(
		index,
		client.Indices.Create.WithContext(ctx),
		client.Indices.Create.WithBody(strings.NewReader(createBody)),
	)
	if err != nil {
		t.Fatalf("create disposable index: %v", err)
	}
	assertResponseOK(
		t,
		createResponse.StatusCode,
		createResponse.Body,
		"create disposable index",
	)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cleanupCancel()
		response, cleanupErr := client.Indices.Delete(
			[]string{index},
			client.Indices.Delete.WithContext(cleanupCtx),
		)
		if cleanupErr != nil {
			t.Errorf("delete disposable index: %v", cleanupErr)
			return
		}
		assertResponseOK(t, response.StatusCode, response.Body, "delete disposable index")
	})
	document := `{"number":"POL-STEP4","policyHolder":"Synthetic Holder","dateFrom":"2024-02-29"}`
	indexResponse, err := client.Index(
		index,
		strings.NewReader(document),
		client.Index.WithContext(ctx),
		client.Index.WithDocumentID("step4"),
		client.Index.WithRefresh("true"),
	)
	if err != nil {
		t.Fatalf("index synthetic document: %v", err)
	}
	assertResponseOK(
		t,
		indexResponse.StatusCode,
		indexResponse.Body,
		"index synthetic document",
	)
	searchResponse, err := client.Search(
		client.Search.WithContext(ctx),
		client.Search.WithIndex(index),
		client.Search.WithBody(
			bytes.NewBufferString(`{"query":{"match_all":{}},"size":10}`),
		),
	)
	if err != nil {
		t.Fatalf("search disposable index: %v", err)
	}
	defer searchResponse.Body.Close()
	if searchResponse.IsError() {
		body, _ := io.ReadAll(io.LimitReader(searchResponse.Body, 4096))
		t.Fatalf("search disposable index returned %s: %s", searchResponse.Status(), body)
	}
	var result struct {
		Hits struct {
			Hits []struct {
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(searchResponse.Body).Decode(&result); err != nil {
		t.Fatalf("decode search result: %v", err)
	}

	// then
	if len(result.Hits.Hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(result.Hits.Hits))
	}
	if got := result.Hits.Hits[0].Source["number"]; got != "POL-STEP4" {
		t.Fatalf("got policy number %v, want POL-STEP4", got)
	}
}

func TestElasticsearchFoundation_SearchesLegacyFieldsAndCapsSelectedHits(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	createBody := `{
		"mappings": {
			"properties": {
				"number": {"type": "keyword"},
				"policyHolder": {"type": "text"},
				"dateFrom": {"type": "date", "format": "strict_date"},
				"dateTo": {"type": "date", "format": "strict_date"}
			}
		}
	}`
	createResponse, err := client.Indices.Create(
		policyelasticsearch.PolicyIndex,
		client.Indices.Create.WithContext(ctx),
		client.Indices.Create.WithBody(strings.NewReader(createBody)),
	)
	if err != nil {
		t.Fatalf("create policy index: %v", err)
	}
	assertResponseOK(t, createResponse.StatusCode, createResponse.Body, "create policy index")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)
		defer cleanupCancel()
		response, cleanupErr := client.Indices.Delete(
			[]string{policyelasticsearch.PolicyIndex},
			client.Indices.Delete.WithContext(cleanupCtx),
		)
		if cleanupErr != nil {
			t.Errorf("delete policy index: %v", cleanupErr)
			return
		}
		assertResponseOK(t, response.StatusCode, response.Body, "delete policy index")
	})

	var bulk strings.Builder
	writeBulkDocument(&bulk, "number-only", map[string]any{
		"number": "NUMBERONLYTOKEN", "policyHolder": "Different Holder",
	})
	writeBulkDocument(&bulk, "holder-only", map[string]any{
		"number": "OTHER-NUMBER", "policyHolder": "Encoded Holder HOLDERONLYTOKEN",
	})
	for index := range 105 {
		writeBulkDocument(&bulk, fmt.Sprintf("cap-%03d", index), map[string]any{
			"number":       fmt.Sprintf("CAP-%03d", index),
			"policyHolder": "capfixture",
			"dateFrom":     fmt.Sprintf("2024-01-%02d", 1+(index%28)),
		})
	}
	bulkResponse, err := client.Bulk(
		strings.NewReader(bulk.String()),
		client.Bulk.WithContext(ctx),
		client.Bulk.WithRefresh("true"),
	)
	if err != nil {
		t.Fatalf("bulk index query fixtures: %v", err)
	}
	assertBulkResponseOK(t, bulkResponse.StatusCode, bulkResponse.Body)
	searcher, err := policyelasticsearch.NewSearcher(client)
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
	selected, err := searcher.SearchPolicies(ctx, "capfixture")
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
	if len(selected) != 100 {
		t.Fatalf("selected hits = %d, want legacy cap 100", len(selected))
	}
}

func writeBulkDocument(body *strings.Builder, id string, source map[string]any) {
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

func assertBulkResponseOK(t *testing.T, status int, body io.ReadCloser) {
	t.Helper()
	defer body.Close()
	if status < 200 || status >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(body, 4096))
		t.Fatalf("bulk index query fixtures returned status %d: %s", status, payload)
	}
	var result struct {
		Errors bool `json:"errors"`
	}
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		t.Fatalf("decode bulk index response: %v", err)
	}
	if result.Errors {
		t.Fatal("bulk index query fixtures reported an item failure")
	}
}

func assertResponseOK(t *testing.T, status int, body io.ReadCloser, operation string) {
	t.Helper()
	defer body.Close()
	if status >= 200 && status < 300 {
		return
	}
	payload, _ := io.ReadAll(io.LimitReader(body, 4096))
	t.Fatalf("%s returned status %d: %s", operation, status, payload)
}
