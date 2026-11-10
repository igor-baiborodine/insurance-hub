//go:build integration

package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

func TestElasticsearchProjection_AcceptedWriterMatrix(t *testing.T) {
	// given
	endpoint := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_URL")
	adminUsername := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_USERNAME")
	adminPassword := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_PASSWORD")
	if endpoint == "" || adminUsername == "" || adminPassword == "" {
		t.Fatal("owned Elasticsearch endpoint and paired credentials are required")
	}
	adminRequest(t, http.MethodPut, endpoint+"/"+PolicyIndex, projectionIndexDefinition,
		adminUsername, adminPassword, http.StatusOK)
	t.Cleanup(func() {
		for _, target := range []string{
			"/_security/user/dashboard_projection_writer",
			"/_security/user/dashboard_projection_reader",
			"/_security/role/dashboard_projection_writer",
			"/_security/role/dashboard_projection_reader",
			"/" + PolicyIndex,
			"/policy_stats_other",
		} {
			integrationRequest(
				t,
				http.MethodDelete,
				endpoint+target,
				"",
				adminUsername,
				adminPassword,
			)
		}
	})
	adminRequest(
		t,
		http.MethodPut,
		endpoint+"/policy_stats_other",
		`{"settings":{"number_of_replicas":0}}`,
		adminUsername,
		adminPassword,
		http.StatusOK,
	)
	adminRequest(
		t,
		http.MethodPut,
		endpoint+"/"+PolicyIndex+"/_doc/SYN-SENTINEL?refresh=true",
		`{"number":"SYN-SENTINEL","from":null,"to":null,"policyHolder":null,"productCode":null,"totalPremium":null,"agentLogin":null}`,
		adminUsername,
		adminPassword,
		http.StatusCreated,
	)
	const writerUsername = "dashboard_projection_writer"
	const writerPassword = "dashboard-writer-password"
	const readerUsername = "dashboard_projection_reader"
	const readerPassword = "dashboard-reader-password"
	adminRequest(t, http.MethodPut, endpoint+"/_security/role/dashboard_projection_writer",
		`{"indices":[{"names":["policy_stats"],"privileges":["index"]}]}`,
		adminUsername, adminPassword, http.StatusOK)
	adminRequest(
		t,
		http.MethodPut,
		endpoint+"/_security/role/dashboard_projection_reader",
		`{"indices":[{"names":["policy_stats"],"privileges":["read","view_index_metadata"]}]}`,
		adminUsername,
		adminPassword,
		http.StatusOK,
	)
	adminRequest(t, http.MethodPut, endpoint+"/_security/user/"+writerUsername,
		`{"password":"`+writerPassword+`","roles":["dashboard_projection_writer"]}`,
		adminUsername, adminPassword, http.StatusOK)
	adminRequest(t, http.MethodPut, endpoint+"/_security/user/"+readerUsername,
		`{"password":"`+readerPassword+`","roles":["dashboard_projection_reader"]}`,
		adminUsername, adminPassword, http.StatusOK)
	writerClient, err := New(Config{
		Endpoint: endpoint, Index: PolicyIndex, Username: writerUsername,
		Password: writerPassword, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writerClient.CloseIdleConnections()
	writer, err := NewWriter(writerClient)
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := application.NewProjectPolicy(writer)
	if err != nil {
		t.Fatal(err)
	}
	readerClient, err := New(Config{
		Endpoint: endpoint, Index: PolicyIndex, Username: readerUsername,
		Password: readerPassword, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer readerClient.CloseIdleConnections()
	if err := readerClient.Check(context.Background()); err != nil {
		t.Fatalf("reader readiness = %v", err)
	}
	if err := writerClient.CheckWrite(context.Background()); err != nil {
		t.Fatalf("writer readiness = %v", err)
	}
	holder := "Synthetic Holder A"
	product := "HSI"
	agent := "synthetic-agent"
	premium, err := domain.ParsePremium("9007199254740993.1234567")
	if err != nil {
		t.Fatal(err)
	}
	from, err := domain.ParseDate("2026-01-01")
	if err != nil {
		t.Fatal(err)
	}
	to, err := domain.ParseDate("2026-12-31")
	if err != nil {
		t.Fatal(err)
	}
	event := application.PolicyEvent{
		Number: "SYN-PRECISE", From: &from, To: &to, PolicyHolder: &holder,
		ProductCode: &product, TotalPremium: &premium, AgentLogin: &agent,
	}

	// when
	if err := useCase.Execute(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	event.AgentLogin = nil
	if err := useCase.Execute(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	storedStatus, stored := integrationRequest(
		t,
		http.MethodGet,
		endpoint+"/"+PolicyIndex+"/_doc/"+event.Number,
		"",
		readerUsername,
		readerPassword,
	)
	_, indexed := integrationRequest(
		t,
		http.MethodPost,
		endpoint+"/"+PolicyIndex+"/_search",
		`{"size":1,"_source":false,"fields":["totalPremium"],"query":{"ids":{"values":["SYN-PRECISE"]}}}`,
		readerUsername,
		readerPassword,
	)
	_, sentinel := integrationRequest(
		t,
		http.MethodGet,
		endpoint+"/"+PolicyIndex+"/_doc/SYN-SENTINEL",
		"",
		readerUsername,
		readerPassword,
	)
	readerWriteStatus, _ := integrationRequest(
		t,
		http.MethodPut,
		endpoint+"/"+PolicyIndex+"/_doc/reader-denied",
		`{}`,
		readerUsername,
		readerPassword,
	)
	writerReadStatus, _ := integrationRequest(
		t,
		http.MethodGet,
		endpoint+"/"+PolicyIndex+"/_doc/"+event.Number,
		"",
		writerUsername,
		writerPassword,
	)
	otherWriteStatus, _ := integrationRequest(
		t,
		http.MethodPut,
		endpoint+"/policy_stats_other/_doc/denied",
		`{}`,
		writerUsername,
		writerPassword,
	)
	deleteStatus, _ := integrationRequest(
		t,
		http.MethodDelete,
		endpoint+"/"+PolicyIndex+"/_doc/"+event.Number,
		"",
		writerUsername,
		writerPassword,
	)
	securityStatus, _ := integrationRequest(t, http.MethodGet,
		endpoint+"/_security/user", "", writerUsername, writerPassword)

	// then
	if storedStatus != http.StatusOK || !bytes.Contains(stored, []byte(
		`"totalPremium":9007199254740993.1234567`,
	)) || !bytes.Contains(stored, []byte(`"agentLogin":null`)) {
		t.Fatalf("stored refreshed replacement = %d %s", storedStatus, stored)
	}
	var indexedResponse struct {
		Hits struct {
			Hits []struct {
				Fields map[string][]json.Number `json:"fields"`
			} `json:"hits"`
		} `json:"hits"`
	}
	decoder := json.NewDecoder(bytes.NewReader(indexed))
	decoder.UseNumber()
	if err := decoder.Decode(
		&indexedResponse,
	); err != nil ||
		len(indexedResponse.Hits.Hits) != 1 {
		t.Fatalf("decode indexed premium = %v: %s", err, indexed)
	}
	indexedPremium := indexedResponse.Hits.Hits[0].Fields["totalPremium"][0].String()
	if indexedPremium == premium.String() {
		t.Fatalf("indexed double unexpectedly preserved source token %q", indexedPremium)
	}
	if !bytes.Contains(sentinel, []byte(`"SYN-SENTINEL"`)) {
		t.Fatalf("pre-existing projection document changed: %s", sentinel)
	}
	for operation, status := range map[string]int{
		"reader write": readerWriteStatus, "writer read": writerReadStatus,
		"writer other-index write": otherWriteStatus, "writer delete": deleteStatus,
		"writer security read": securityStatus,
	} {
		if status != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", operation, status)
		}
	}
}

func integrationRequest(
	t *testing.T,
	method, target, body, username, password string,
) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, target, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth(username, password)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

const projectionIndexDefinition = `{
	"settings":{"number_of_shards":1,"number_of_replicas":0},
	"mappings":{"dynamic":"strict","properties":{
		"number":{"type":"keyword"},
		"from":{"type":"date","format":"strict_date"},
		"to":{"type":"date","format":"strict_date"},
		"policyHolder":{"type":"text","fields":{"keyword":{"type":"keyword"}}},
		"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},
		"totalPremium":{"type":"double"},
		"agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword"}}}
	}}
}`
