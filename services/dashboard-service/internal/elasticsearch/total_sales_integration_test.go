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
	client, err := New(Config{
		Endpoint: endpoint, Index: PolicyIndex, Username: username, Password: password,
		Timeout: 5 * time.Second,
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
	wrongPassword, err := New(Config{
		Endpoint: endpoint,
		Index:    PolicyIndex,
		Username: username,
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
				`"mappings":{"properties":{"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"from":{"type":"date"},"totalPremium":{"type":"double"}}}}`,
		},
		{
			name:  "non-aggregatable product keyword",
			index: PolicyIndex,
			definition: `{"settings":{"number_of_replicas":0},"mappings":{"properties":{` +
				`"productCode":{"type":"text","fields":{"keyword":{"type":"keyword","doc_values":false}}},` +
				`"from":{"type":"date"},"totalPremium":{"type":"double"}}}}`,
		},
		{
			name:  "missing premium field",
			index: PolicyIndex,
			definition: `{"settings":{"number_of_replicas":0},"mappings":{"properties":{` +
				`"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"from":{"type":"date"}}}}`,
		},
		{
			name:  "wrong premium type",
			index: PolicyIndex,
			definition: `{"settings":{"number_of_replicas":0},"mappings":{"properties":{` +
				`"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"from":{"type":"date"},"totalPremium":{"type":"keyword"}}}}`,
		},
		{
			name:  "date rejects date-only input",
			index: PolicyIndex,
			definition: `{"settings":{"number_of_replicas":0},"mappings":{"properties":{` +
				`"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},` +
				`"from":{"type":"date","format":"strict_date_time"},"totalPremium":{"type":"double"}}}}`,
		},
		{
			name:       "closed index",
			index:      PolicyIndex,
			closeIndex: true,
			definition: `{"settings":{"number_of_replicas":0},"mappings":{"properties":{` +
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
{"productCode":"SAFE_FARM","from":"2018-01-01","totalPremium":1000.0}
{"index":{"_id":"safe-2"}}
{"productCode":"SAFE_FARM","from":"2018-02-01","totalPremium":1000.0}
{"index":{"_id":"safe-3"}}
{"productCode":"SAFE_FARM","from":"2018-03-01","totalPremium":1000.0}
{"index":{"_id":"safe-4"}}
{"productCode":"SAFE_FARM","from":"2018-04-01","totalPremium":1000.0}
{"index":{"_id":"safe-5"}}
{"productCode":"SAFE_FARM","from":"2018-05-01","totalPremium":1000.0}
{"index":{"_id":"safe-upper"}}
{"productCode":"SAFE_FARM","from":"2018-12-31","totalPremium":1000.0}
{"index":{"_id":"a-1"}}
{"productCode":"A","from":"2019-01-01","totalPremium":1.005}
{"index":{"_id":"a-2"}}
{"productCode":"A","from":"2019-02-01","totalPremium":1.005}
{"index":{"_id":"a-missing-premium"}}
{"productCode":"A","from":"2019-03-01"}
{"index":{"_id":"b-1"}}
{"productCode":"B","from":"2019-02-01","totalPremium":2.005}
{"index":{"_id":"b-2"}}
{"productCode":"B","from":"2019-03-01","totalPremium":2.005}
{"index":{"_id":"b-3"}}
{"productCode":"B","from":"2019-04-01","totalPremium":2.005}
{"index":{"_id":"c-1"}}
{"productCode":"C","from":"2019-02-01","totalPremium":3.0}
{"index":{"_id":"c-2"}}
{"productCode":"C","from":"2019-03-01","totalPremium":3.0}
{"index":{"_id":"empty-product"}}
{"productCode":"","from":"2019-02-01","totalPremium":4.0}
{"index":{"_id":"d"}}
{"productCode":"D","from":"2019-02-01","totalPremium":4.0}
{"index":{"_id":"e"}}
{"productCode":"E","from":"2019-02-01","totalPremium":5.0}
{"index":{"_id":"f"}}
{"productCode":"F","from":"2019-02-01","totalPremium":6.0}
{"index":{"_id":"g"}}
{"productCode":"G","from":"2019-02-01","totalPremium":7.0}
{"index":{"_id":"h"}}
{"productCode":"H","from":"2019-02-01","totalPremium":8.0}
{"index":{"_id":"i"}}
{"productCode":"I","from":"2019-02-01","totalPremium":9.0}
{"index":{"_id":"j"}}
{"productCode":"J","from":"2019-02-01","totalPremium":10.0}
{"index":{"_id":"k"}}
{"productCode":"K","from":"2019-02-01","totalPremium":11.0}
{"index":{"_id":"l"}}
{"productCode":"L","from":"2019-02-01","totalPremium":12.0}
{"index":{"_id":"m"}}
{"productCode":"M","from":"2019-02-01","totalPremium":13.0}
{"index":{"_id":"missing-product"}}
{"from":"2019-02-01","totalPremium":999.0}
{"index":{"_id":"upper-z"}}
{"productCode":"Z","from":"2019-12-31","totalPremium":1000.0}
`) + "\n"
