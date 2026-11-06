//go:build integration

package elasticsearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

func TestElasticsearchReader_AcceptedAggregationMatrix(t *testing.T) {
	// given
	endpoint := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_URL")
	username := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_USERNAME")
	password := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_PASSWORD")
	if endpoint == "" || username == "" || password == "" {
		t.Fatal("owned Elasticsearch endpoint and paired credentials are required")
	}
	adminRequest(t, http.MethodPut, endpoint+"/"+PolicyIndex, `{
		"settings":{"number_of_shards":1,"number_of_replicas":0},
		"mappings":{"properties":{
			"agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword"}}},
			"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},
			"from":{"type":"date"},
			"totalPremium":{"type":"double"}
		}}
	}`, username, password, http.StatusOK)
	t.Cleanup(func() {
		request, err := http.NewRequest(http.MethodDelete, endpoint+"/"+PolicyIndex, nil)
		if err == nil {
			request.SetBasicAuth(username, password)
			response, requestErr := (&http.Client{Timeout: 10 * time.Second}).Do(
				request,
			)
			if requestErr == nil {
				_ = response.Body.Close()
			}
		}
	})
	adminRequest(
		t,
		http.MethodPost,
		endpoint+"/"+PolicyIndex+"/_bulk?refresh=true",
		integrationFixture,
		username,
		password,
		http.StatusOK,
	)
	readerUsername := "dashboard_reader"
	readerPassword := "issue-288-reader-password"
	adminRequest(
		t,
		http.MethodPut,
		endpoint+"/_security/role/dashboard_reader",
		`{"indices":[{"names":["policy_stats"],"privileges":["read","view_index_metadata"]}]}`,
		username,
		password,
		http.StatusOK,
	)
	adminRequest(
		t,
		http.MethodPut,
		endpoint+"/_security/user/"+readerUsername,
		`{"password":"`+readerPassword+`","roles":["dashboard_reader"]}`,
		username,
		password,
		http.StatusOK,
	)
	client, err := New(Config{
		Endpoint: endpoint,
		Index:    PolicyIndex,
		Username: readerUsername,
		Password: readerPassword,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	reader, err := NewReader(client)
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := application.NewGetTotalSales(reader)
	if err != nil {
		t.Fatal(err)
	}
	agentUseCase, err := application.NewGetAgentSales(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Check(context.Background()); err != nil {
		t.Fatalf("compatible readiness = %v", err)
	}

	// when
	known := executeTotalSales(t, useCase, domain.AllProducts(), "2018-01-01", "2018-12-31")
	all := executeTotalSales(t, useCase, domain.AllProducts(), "2019-01-01", "2019-12-31")
	product := executeTotalSales(
		t,
		useCase,
		domain.ExactProduct("B"),
		"2019-01-01",
		"2019-12-31",
	)
	emptyProduct := executeTotalSales(
		t,
		useCase,
		domain.ExactProduct(""),
		"2019-01-01",
		"2019-12-31",
	)
	empty := executeTotalSales(t, useCase, domain.AllProducts(), "2020-01-01", "2021-01-01")
	agents := executeAgentSales(
		t,
		agentUseCase,
		domain.AllAgents(),
		domain.AllProducts(),
		"2019-01-01",
		"2019-12-31",
	)
	agent := executeAgentSales(
		t, agentUseCase, domain.ExactAgent("m.smith"), domain.AllProducts(),
		"2019-01-01", "2019-12-31",
	)
	intersection := executeAgentSales(
		t, agentUseCase, domain.AllAgents(), domain.ExactProduct("B"),
		"2019-01-01", "2019-12-31",
	)
	emptyAgent := executeAgentSales(
		t, agentUseCase, domain.ExactAgent(""), domain.AllProducts(),
		"2019-01-01", "2019-12-31",
	)
	equalDates := executeAgentSales(
		t,
		agentUseCase,
		domain.AllAgents(),
		domain.AllProducts(),
		"2019-06-01",
		"2019-06-01",
	)
	reversedDates := executeAgentSales(
		t,
		agentUseCase,
		domain.AllAgents(),
		domain.AllProducts(),
		"2019-07-01",
		"2019-06-01",
	)
	wrongPassword, err := New(Config{
		Endpoint: endpoint,
		Index:    PolicyIndex,
		Username: readerUsername,
		Password: "wrong-password",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongReader, err := NewReader(wrongPassword)
	if err != nil {
		t.Fatal(err)
	}
	_, authErr := wrongReader.ReadTotalSales(
		context.Background(),
		totalSalesQuery(t, domain.AllProducts(), "2019-01-01", "2019-12-31"),
	)
	adminRequest(
		t,
		http.MethodDelete,
		endpoint+"/"+PolicyIndex,
		"",
		username,
		password,
		http.StatusOK,
	)
	_, absentErr := reader.ReadTotalSales(
		context.Background(),
		totalSalesQuery(t, domain.AllProducts(), "2019-01-01", "2019-12-31"),
	)
	absentCheckErr := client.Check(context.Background())

	// then
	assertTotal(t, known, 5, 500000, 1)
	assertTotal(t, all, 15, 5703, 10)
	assertTotal(t, product, 3, 602, 1)
	assertTotal(t, emptyProduct, 1, 400, 1)
	assertTotal(t, empty, 0, 0, 0)
	if all.PerProductTotal["A"].PoliciesCount() != 3 ||
		all.PerProductTotal["A"].PremiumAmount().MinorUnits() != 201 {
		t.Fatalf("missing-premium bucket A = %#v", all.PerProductTotal["A"])
	}
	if _, present := all.PerProductTotal["Z"]; present {
		t.Fatal("exclusive upper-bound product Z was returned")
	}
	if _, present := all.PerProductTotal["J"]; present {
		t.Fatal("over-limit product J was returned")
	}
	assertAgentValue(t, agents, "m.smith", 5, "8.01")
	assertAgentValue(t, agents, "a.smith", 3, "6.015")
	assertAgentValue(t, agent, "m.smith", 5, "8.01")
	assertAgentValue(t, intersection, "a.smith", 3, "6.015")
	assertAgentValue(t, emptyAgent, "", 1, "4.0")
	if len(agents.PerAgentTotal) != returnedBucketLimit ||
		len(equalDates.PerAgentTotal) != 0 || len(reversedDates.PerAgentTotal) != 0 {
		t.Fatalf(
			"agent selection = (all=%d, equal=%d, reversed=%d)",
			len(agents.PerAgentTotal),
			len(equalDates.PerAgentTotal),
			len(reversedDates.PerAgentTotal),
		)
	}
	if _, present := agents.PerAgentTotal["z-agent"]; present {
		t.Fatal("over-limit agent z-agent was returned")
	}
	if _, present := agents.PerAgentTotal["missing-agent"]; present {
		t.Fatal("missing agent attribution created a bucket")
	}
	if !errors.Is(authErr, ErrSearchRejected) || !errors.Is(absentErr, ErrSearchRejected) ||
		absentCheckErr == nil {
		t.Fatalf(
			"auth/absent errors = (%v, %v, readiness=%v)",
			authErr,
			absentErr,
			absentCheckErr,
		)
	}
}

func TestElasticsearchReader_AcceptedAgentOracle(t *testing.T) {
	// given — fixtures replay C03-E01–E04/E07 and independent JDK 14 decimal boundaries.
	endpoint := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_URL")
	username := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_USERNAME")
	password := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_PASSWORD")
	if endpoint == "" || username == "" || password == "" {
		t.Fatal("owned Elasticsearch endpoint and paired credentials are required")
	}
	adminRequest(t, http.MethodPut, endpoint+"/"+PolicyIndex, `{
		"settings":{"number_of_shards":1,"number_of_replicas":0},
		"mappings":{"properties":{
			"agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword"}}},
			"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},
			"from":{"type":"date"},
			"totalPremium":{"type":"double"}
		}}
	}`, username, password, http.StatusOK)
	t.Cleanup(func() {
		request, err := http.NewRequest(http.MethodDelete, endpoint+"/"+PolicyIndex, nil)
		if err == nil {
			request.SetBasicAuth(username, password)
			response, requestErr := (&http.Client{Timeout: 10 * time.Second}).Do(
				request,
			)
			if requestErr == nil {
				_ = response.Body.Close()
			}
		}
	})
	adminRequest(
		t,
		http.MethodPost,
		endpoint+"/"+PolicyIndex+"/_bulk?refresh=true",
		acceptedAgentOracleFixture,
		username,
		password,
		http.StatusOK,
	)
	readerUsername := "dashboard_reader"
	readerPassword := "issue-288-reader-password"
	adminRequest(
		t,
		http.MethodPut,
		endpoint+"/_security/role/dashboard_reader",
		`{"indices":[{"names":["policy_stats"],"privileges":["read","view_index_metadata"]}]}`,
		username,
		password,
		http.StatusOK,
	)
	adminRequest(
		t,
		http.MethodPut,
		endpoint+"/_security/user/"+readerUsername,
		`{"password":"`+readerPassword+`","roles":["dashboard_reader"]}`,
		username,
		password,
		http.StatusOK,
	)
	client, err := New(Config{
		Endpoint: endpoint, Index: PolicyIndex, Username: readerUsername,
		Password: readerPassword, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	reader, err := NewReader(client)
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := application.NewGetAgentSales(reader)
	if err != nil {
		t.Fatal(err)
	}

	// when
	all := executeAgentSales(
		t, useCase, domain.AllAgents(), domain.AllProducts(),
		"2019-01-01", "2019-12-31",
	)
	house := executeAgentSales(
		t, useCase, domain.AllAgents(), domain.ExactProduct("SAFE_HOUSE"),
		"2019-01-01", "2019-12-31",
	)
	mSmith := executeAgentSales(
		t, useCase, domain.ExactAgent("m.smith"), domain.AllProducts(),
		"2019-01-01", "2019-12-31",
	)
	mSmithCar := executeAgentSales(
		t, useCase, domain.ExactAgent("m.smith"), domain.ExactProduct("SAFE_CAR"),
		"2019-01-01", "2019-12-31",
	)
	firstQuarter := executeAgentSales(
		t, useCase, domain.AllAgents(), domain.AllProducts(),
		"2019-01-01", "2019-03-31",
	)
	february := executeAgentSales(
		t, useCase, domain.AllAgents(), domain.AllProducts(),
		"2019-02-01", "2019-03-01",
	)
	buckets := executeAgentSales(
		t, useCase, domain.AllAgents(), domain.ExactProduct("BUCKETS"),
		"2020-01-01", "2021-01-01",
	)
	boundaries := executeAgentSales(
		t, useCase, domain.AllAgents(), domain.ExactProduct("BOUNDARIES"),
		"2023-01-01", "2024-01-01",
	)

	// then
	if len(all.PerAgentTotal) != 2 || len(house.PerAgentTotal) != 2 ||
		len(mSmith.PerAgentTotal) != 1 || len(mSmithCar.PerAgentTotal) != 1 ||
		len(firstQuarter.PerAgentTotal) != 2 || len(february.PerAgentTotal) != 2 {
		t.Fatal("accepted five-policy oracle returned an unexpected agent set")
	}
	assertAgentValue(t, all, "m.smith", 3, "3000.0")
	assertAgentValue(t, all, "a.smith", 2, "2000.0")
	assertAgentValue(t, house, "m.smith", 1, "1000.0")
	assertAgentValue(t, house, "a.smith", 1, "1000.0")
	assertAgentValue(t, mSmith, "m.smith", 3, "3000.0")
	assertAgentValue(t, mSmithCar, "m.smith", 1, "1000.0")
	assertAgentValue(t, firstQuarter, "m.smith", 2, "2000.0")
	assertAgentValue(t, firstQuarter, "a.smith", 2, "2000.0")
	assertAgentValue(t, february, "m.smith", 1, "1000.0")
	assertAgentValue(t, february, "a.smith", 1, "1000.0")

	wantBuckets := map[string]struct {
		count  int64
		amount string
	}{
		"alpha":   {count: 3, amount: "3.0"},
		"beta":    {count: 3, amount: "3.0"},
		"gamma":   {count: 2, amount: "2.0"},
		"":        {count: 1, amount: "1.0"},
		"delta":   {count: 1, amount: "1.0"},
		"epsilon": {count: 1, amount: "1.0"},
		"eta":     {count: 1, amount: "1.0"},
		"iota":    {count: 1, amount: "1.0"},
		"kappa":   {count: 1, amount: "1.0"},
		"lambda":  {count: 1, amount: "1.0"},
	}
	if len(buckets.PerAgentTotal) != len(wantBuckets) {
		t.Fatalf("bucket count = %d, want %d", len(buckets.PerAgentTotal), len(wantBuckets))
	}
	for agent, want := range wantBuckets {
		assertAgentValue(t, buckets, agent, want.count, want.amount)
	}
	assertAgentValue(t, boundaries, "small", 1, "0.00010")
	assertAgentValue(t, boundaries, "large", 1, "12345678.9")
}

func TestElasticsearchReadiness_RejectsIncompatibleState(t *testing.T) {
	endpoint := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_URL")
	username := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_USERNAME")
	password := os.Getenv("DASHBOARD_TEST_ELASTICSEARCH_PASSWORD")
	if endpoint == "" || username == "" || password == "" {
		t.Fatal("owned Elasticsearch endpoint and paired credentials are required")
	}
	client, err := New(Config{
		Endpoint: endpoint, Index: PolicyIndex, Username: username, Password: password,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()

	tests := []struct {
		name       string
		index      string
		definition string
		closeIndex bool
	}{
		{
			name:  "alias",
			index: "policy_stats_actual",
			definition: `{"settings":{"number_of_replicas":0},"aliases":{"policy_stats":{}},` +
				`"mappings":{"properties":{"agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"from":{"type":"date"},"totalPremium":{"type":"double"}}}}`,
		},
		{
			name:  "non-aggregatable agent keyword",
			index: PolicyIndex,
			definition: `{"settings":{"number_of_replicas":0},"mappings":{"properties":{` +
				`"agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword","doc_values":false}}},` +
				`"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"from":{"type":"date"},"totalPremium":{"type":"double"}}}}`,
		},
		{
			name:  "non-aggregatable product keyword",
			index: PolicyIndex,
			definition: `{"settings":{"number_of_replicas":0},"mappings":{"properties":{` +
				`"agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"productCode":{"type":"text","fields":{"keyword":{"type":"keyword","doc_values":false}}},` +
				`"from":{"type":"date"},"totalPremium":{"type":"double"}}}}`,
		},
		{
			name:  "missing premium field",
			index: PolicyIndex,
			definition: `{"settings":{"number_of_replicas":0},"mappings":{"properties":{` +
				`"agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"from":{"type":"date"}}}}`,
		},
		{
			name:  "wrong premium type",
			index: PolicyIndex,
			definition: `{"settings":{"number_of_replicas":0},"mappings":{"properties":{` +
				`"agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"from":{"type":"date"},"totalPremium":{"type":"keyword"}}}}`,
		},
		{
			name:  "date rejects date-only input",
			index: PolicyIndex,
			definition: `{"settings":{"number_of_replicas":0},"mappings":{"properties":{` +
				`"agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"from":{"type":"date","format":"strict_date_time"},"totalPremium":{"type":"double"}}}}`,
		},
		{
			name:       "closed index",
			index:      PolicyIndex,
			closeIndex: true,
			definition: `{"settings":{"number_of_replicas":0},"mappings":{"properties":{` +
				`"agentLogin":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"from":{"type":"date"},"totalPremium":{"type":"double"}}}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adminRequest(
				t, http.MethodPut, endpoint+"/"+test.index, test.definition,
				username, password, http.StatusOK,
			)
			t.Cleanup(func() {
				adminRequest(
					t, http.MethodDelete, endpoint+"/"+test.index, "",
					username, password, http.StatusOK,
				)
			})
			if test.closeIndex {
				adminRequest(
					t, http.MethodPost, endpoint+"/"+test.index+"/_close", "",
					username, password, http.StatusOK,
				)
			}

			if err := client.Check(context.Background()); err == nil {
				t.Fatal("Check() accepted incompatible Elasticsearch state")
			}
		})
	}
}

func adminRequest(
	t *testing.T,
	method, target, body, username, password string,
	wantStatus int,
) {
	t.Helper()
	request, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth(username, password)
	if body != "" {
		if strings.Contains(target, "_bulk") {
			request.Header.Set("Content-Type", "application/x-ndjson")
		} else {
			request.Header.Set("Content-Type", "application/json")
		}
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
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s = %d: %s", method, target, response.StatusCode, data)
	}
	if strings.Contains(target, "_bulk") {
		var result struct {
			Errors bool `json:"errors"`
		}
		if err := json.Unmarshal(data, &result); err != nil || result.Errors {
			t.Fatalf("bulk result = %s, error = %v", data, err)
		}
	}
}

func executeTotalSales(
	t *testing.T,
	useCase application.GetTotalSales,
	product domain.ProductSelection,
	from, to string,
) application.TotalSalesResult {
	t.Helper()
	result, err := useCase.Execute(context.Background(), totalSalesQuery(t, product, from, to))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func totalSalesQuery(
	t *testing.T,
	product domain.ProductSelection,
	from, to string,
) application.TotalSalesQuery {
	t.Helper()
	dateFrom, err := domain.ParseDate(from)
	if err != nil {
		t.Fatal(err)
	}
	dateTo, err := domain.ParseDate(to)
	if err != nil {
		t.Fatal(err)
	}
	return application.TotalSalesQuery{Product: product, DateFrom: dateFrom, DateTo: dateTo}
}

func executeAgentSales(
	t *testing.T,
	useCase application.GetAgentSales,
	agent domain.AgentSelection,
	product domain.ProductSelection,
	from, to string,
) application.AgentSalesResult {
	t.Helper()
	dateFrom, err := domain.ParseDate(from)
	if err != nil {
		t.Fatal(err)
	}
	dateTo, err := domain.ParseDate(to)
	if err != nil {
		t.Fatal(err)
	}
	result, err := useCase.Execute(context.Background(), application.AgentSalesQuery{
		Agent: agent, Product: product, DateFrom: dateFrom, DateTo: dateTo,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertAgentValue(
	t *testing.T,
	result application.AgentSalesResult,
	agent string,
	wantCount int64,
	wantAmount string,
) {
	t.Helper()
	value, present := result.PerAgentTotal[agent]
	if !present || value.PoliciesCount() != wantCount ||
		value.PremiumAmount().String() != wantAmount {
		t.Fatalf(
			"agent %q = (%#v, %t), want (%d, %s)",
			agent,
			value,
			present,
			wantCount,
			wantAmount,
		)
	}
}

func assertTotal(
	t *testing.T,
	result application.TotalSalesResult,
	wantCount, wantMinor int64,
	wantProducts int,
) {
	t.Helper()
	if result.Total.PoliciesCount() != wantCount ||
		result.Total.PremiumAmount().MinorUnits() != wantMinor ||
		len(result.PerProductTotal) != wantProducts {
		t.Fatalf(
			"total = (%d, %d, products=%d), want (%d, %d, products=%d)",
			result.Total.PoliciesCount(),
			result.Total.PremiumAmount().MinorUnits(),
			len(result.PerProductTotal),
			wantCount,
			wantMinor,
			wantProducts,
		)
	}
}

var integrationFixture = strings.TrimSpace(`
{"index":{"_id":"safe-1"}}
{"agentLogin":"safe-agent","productCode":"SAFE_FARM","from":"2018-01-01","totalPremium":1000.0}
{"index":{"_id":"safe-2"}}
{"agentLogin":"safe-agent","productCode":"SAFE_FARM","from":"2018-02-01","totalPremium":1000.0}
{"index":{"_id":"safe-3"}}
{"agentLogin":"safe-agent","productCode":"SAFE_FARM","from":"2018-03-01","totalPremium":1000.0}
{"index":{"_id":"safe-4"}}
{"agentLogin":"safe-agent","productCode":"SAFE_FARM","from":"2018-04-01","totalPremium":1000.0}
{"index":{"_id":"safe-5"}}
{"agentLogin":"safe-agent","productCode":"SAFE_FARM","from":"2018-05-01","totalPremium":1000.0}
{"index":{"_id":"safe-upper"}}
{"agentLogin":"safe-agent","productCode":"SAFE_FARM","from":"2018-12-31","totalPremium":1000.0}
{"index":{"_id":"a-1"}}
{"agentLogin":"m.smith","productCode":"A","from":"2019-01-01","totalPremium":1.005}
{"index":{"_id":"a-2"}}
{"agentLogin":"m.smith","productCode":"A","from":"2019-02-01","totalPremium":1.005}
{"index":{"_id":"a-missing-premium"}}
{"agentLogin":"m.smith","productCode":"A","from":"2019-03-01"}
{"index":{"_id":"b-1"}}
{"agentLogin":"a.smith","productCode":"B","from":"2019-02-01","totalPremium":2.005}
{"index":{"_id":"b-2"}}
{"agentLogin":"a.smith","productCode":"B","from":"2019-03-01","totalPremium":2.005}
{"index":{"_id":"b-3"}}
{"agentLogin":"a.smith","productCode":"B","from":"2019-04-01","totalPremium":2.005}
{"index":{"_id":"c-1"}}
{"agentLogin":"m.smith","productCode":"C","from":"2019-02-01","totalPremium":3.0}
{"index":{"_id":"c-2"}}
{"agentLogin":"m.smith","productCode":"C","from":"2019-03-01","totalPremium":3.0}
{"index":{"_id":"empty-product"}}
{"agentLogin":"","productCode":"","from":"2019-02-01","totalPremium":4.0}
{"index":{"_id":"d"}}
{"agentLogin":"d-agent","productCode":"D","from":"2019-02-01","totalPremium":4.0}
{"index":{"_id":"e"}}
{"agentLogin":"e-agent","productCode":"E","from":"2019-02-01","totalPremium":5.0}
{"index":{"_id":"f"}}
{"agentLogin":"f-agent","productCode":"F","from":"2019-02-01","totalPremium":6.0}
{"index":{"_id":"g"}}
{"agentLogin":"g-agent","productCode":"G","from":"2019-02-01","totalPremium":7.0}
{"index":{"_id":"h"}}
{"agentLogin":"h-agent","productCode":"H","from":"2019-02-01","totalPremium":8.0}
{"index":{"_id":"i"}}
{"agentLogin":"i-agent","productCode":"I","from":"2019-02-01","totalPremium":9.0}
{"index":{"_id":"j"}}
{"agentLogin":"j-agent","productCode":"J","from":"2019-02-01","totalPremium":10.0}
{"index":{"_id":"k"}}
{"agentLogin":"k-agent","productCode":"K","from":"2019-02-01","totalPremium":11.0}
{"index":{"_id":"l"}}
{"agentLogin":"l-agent","productCode":"L","from":"2019-02-01","totalPremium":12.0}
{"index":{"_id":"m"}}
{"agentLogin":"z-agent","productCode":"M","from":"2019-02-01","totalPremium":13.0}
{"index":{"_id":"missing-product"}}
{"from":"2019-02-01","totalPremium":999.0}
{"index":{"_id":"upper-z"}}
{"agentLogin":"upper-agent","productCode":"Z","from":"2019-12-31","totalPremium":1000.0}
`) + "\n"

var acceptedAgentOracleFixture = strings.TrimSpace(`
{"index":{"_id":"111-001"}}
{"agentLogin":"m.smith","productCode":"SAFE_HOUSE","from":"2019-01-01","totalPremium":1000}
{"index":{"_id":"111-002"}}
{"agentLogin":"a.smith","productCode":"SAFE_HOUSE","from":"2019-02-01","totalPremium":1000}
{"index":{"_id":"111-003"}}
{"agentLogin":"m.smith","productCode":"SAFE_CAR","from":"2019-02-01","totalPremium":1000}
{"index":{"_id":"111-004"}}
{"agentLogin":"a.smith","productCode":"SAFE_CAR","from":"2019-03-01","totalPremium":1000}
{"index":{"_id":"111-005"}}
{"agentLogin":"m.smith","productCode":"SAFE_FARM","from":"2019-04-01","totalPremium":1000}
{"index":{"_id":"bucket-alpha-1"}}
{"agentLogin":"alpha","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-alpha-2"}}
{"agentLogin":"alpha","productCode":"BUCKETS","from":"2020-01-02","totalPremium":1}
{"index":{"_id":"bucket-alpha-3"}}
{"agentLogin":"alpha","productCode":"BUCKETS","from":"2020-01-03","totalPremium":1}
{"index":{"_id":"bucket-beta-1"}}
{"agentLogin":"beta","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-beta-2"}}
{"agentLogin":"beta","productCode":"BUCKETS","from":"2020-01-02","totalPremium":1}
{"index":{"_id":"bucket-beta-3"}}
{"agentLogin":"beta","productCode":"BUCKETS","from":"2020-01-03","totalPremium":1}
{"index":{"_id":"bucket-gamma-1"}}
{"agentLogin":"gamma","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-gamma-2"}}
{"agentLogin":"gamma","productCode":"BUCKETS","from":"2020-01-02","totalPremium":1}
{"index":{"_id":"bucket-empty"}}
{"agentLogin":"","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-delta"}}
{"agentLogin":"delta","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-epsilon"}}
{"agentLogin":"epsilon","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-eta"}}
{"agentLogin":"eta","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-iota"}}
{"agentLogin":"iota","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-kappa"}}
{"agentLogin":"kappa","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-lambda"}}
{"agentLogin":"lambda","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-mu"}}
{"agentLogin":"mu","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-theta"}}
{"agentLogin":"theta","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-zeta"}}
{"agentLogin":"zeta","productCode":"BUCKETS","from":"2020-01-01","totalPremium":1}
{"index":{"_id":"bucket-missing-agent"}}
{"productCode":"BUCKETS","from":"2020-01-01","totalPremium":999}
{"index":{"_id":"bucket-null-agent"}}
{"agentLogin":null,"productCode":"BUCKETS","from":"2020-01-01","totalPremium":999}
{"index":{"_id":"boundary-small"}}
{"agentLogin":"small","productCode":"BOUNDARIES","from":"2023-01-01","totalPremium":0.0001}
{"index":{"_id":"boundary-large"}}
{"agentLogin":"large","productCode":"BOUNDARIES","from":"2023-01-01","totalPremium":12345678.9}
`) + "\n"
