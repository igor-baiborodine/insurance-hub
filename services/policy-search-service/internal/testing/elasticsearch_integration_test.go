//go:build integration

package testing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

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

func assertResponseOK(t *testing.T, status int, body io.ReadCloser, operation string) {
	t.Helper()
	defer body.Close()
	if status >= 200 && status < 300 {
		return
	}
	payload, _ := io.ReadAll(io.LimitReader(body, 4096))
	t.Fatalf("%s returned status %d: %s", operation, status, payload)
}
