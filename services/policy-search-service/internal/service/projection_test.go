package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
)

func TestProductionProjection_ConstructsDedicatedWriterRuntime(t *testing.T) {
	// given
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			response.Header().Set("X-Elastic-Product", "Elasticsearch")
			if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			_, _ = response.Write([]byte(`{"result":"created"}`))
		},
	))
	defer server.Close()
	runtime, err := newProductionProjection(config.Elasticsearch{
		URL:     server.URL,
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.CloseIdleConnections)

	// when
	err = runtime.ProjectPolicy(context.Background(), application.PolicyEvent{
		Number:       "SYN-230-001",
		PolicyHolder: "Synthetic Holder A",
	})

	// then
	if err != nil || got["number"] != "SYN-230-001" ||
		got["policyHolder"] != "Synthetic Holder A" {
		t.Fatalf("projection document = %v, %v", got, err)
	}
}

func TestProductionProjection_RejectsInvalidWriterClientSettings(t *testing.T) {
	// when
	_, err := newProductionProjection(config.Elasticsearch{
		URL:     "invalid://private-marker",
		Timeout: time.Second,
	})

	// then
	if err == nil || err.Error() !=
		"initialize policy projection: configure Elasticsearch client" {
		t.Fatalf("constructor error = %v", err)
	}
}
