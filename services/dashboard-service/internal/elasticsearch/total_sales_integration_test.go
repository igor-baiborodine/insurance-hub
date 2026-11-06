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

func TestElasticsearchReader_AcceptedTrendOracle(t *testing.T) {
	// given — fixtures replay the independently accepted Q03 interval and amount vectors.
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
		acceptedTrendOracleFixture,
		username,
		password,
		http.StatusOK,
	)
	readerUsername := "dashboard_trend_reader"
	readerPassword := "issue-289-reader-password"
	adminRequest(
		t,
		http.MethodPut,
		endpoint+"/_security/role/dashboard_trend_reader",
		`{"indices":[{"names":["policy_stats"],"privileges":["read","view_index_metadata"]}]}`,
		username,
		password,
		http.StatusOK,
	)
	adminRequest(
		t,
		http.MethodPut,
		endpoint+"/_security/user/"+readerUsername,
		`{"password":"`+readerPassword+`","roles":["dashboard_trend_reader"]}`,
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
	reader, err := NewTrendReader(client, time.FixedZone("GMT", 0))
	if err != nil {
		t.Fatal(err)
	}
	useCase, err := application.NewGetSalesTrends(reader)
	if err != nil {
		t.Fatal(err)
	}
	adminRequest(
		t,
		http.MethodPut,
		endpoint+"/"+PolicyIndex+"/_doc/denied?refresh=true",
		`{"productCode":"DENIED","from":"2020-01-01","totalPremium":1}`,
		readerUsername,
		readerPassword,
		http.StatusForbidden,
	)

	// when
	day := executeSalesTrends(
		t, useCase, domain.ExactProduct("CAL_DAY"), "2020-02-01", "2020-04-01",
		domain.AggregationUnitDay,
	)
	week := executeSalesTrends(
		t, useCase, domain.ExactProduct("CAL_WEEK"), "2024-12-01", "2025-02-01",
		domain.AggregationUnitWeek,
	)
	month := executeSalesTrends(
		t, useCase, domain.ExactProduct("CAL_MONTH"), "2020-01-01", "2020-05-01",
		domain.AggregationUnitMonth,
	)
	year := executeSalesTrends(
		t, useCase, domain.ExactProduct("CAL_YEAR"), "2019-01-01", "2022-01-01",
		domain.AggregationUnitYear,
	)
	five := executeSalesTrends(
		t, useCase, domain.AllProducts(), "2019-01-01", "2019-12-31",
		domain.AggregationUnitMonth,
	)
	house := executeSalesTrends(
		t, useCase, domain.ExactProduct("SAFE_HOUSE"), "2019-01-01", "2019-12-31",
		domain.AggregationUnitMonth,
	)
	decimals := executeSalesTrends(
		t, useCase, domain.ExactProduct("DECIMAL"), "2022-01-01", "2022-07-01",
		domain.AggregationUnitMonth,
	)
	empty := executeSalesTrends(
		t, useCase, domain.ExactProduct("missing"), "2020-01-01", "2021-01-01",
		domain.AggregationUnitMonth,
	)
	equal := executeSalesTrends(
		t, useCase, domain.AllProducts(), "2020-01-01", "2020-01-01",
		domain.AggregationUnitMonth,
	)
	reversed := executeSalesTrends(
		t, useCase, domain.AllProducts(), "2020-02-01", "2020-01-01",
		domain.AggregationUnitMonth,
	)
	_, nonfiniteErr := useCase.Execute(context.Background(), salesTrendsQuery(
		t, domain.ExactProduct("NONFINITE"), "2023-01-01", "2023-02-01",
		domain.AggregationUnitMonth,
	))

	// then
	assertTrendVector(t, day, []trendExpectation{
		{"2020-02-28T00:00:00.000Z", "2020-02-28", 1, "1.00"},
		{"2020-02-29T00:00:00.000Z", "2020-02-29", 1, "2.00"},
		{"2020-03-01T00:00:00.000Z", "2020-03-01", 1, "3.00"},
	})
	assertTrendVector(t, week, []trendExpectation{
		{"2024-12-23T00:00:00.000Z", "2024-12-23", 1, "1.00"},
		{"2024-12-30T00:00:00.000Z", "2024-12-30", 2, "5.00"},
		{"2025-01-06T00:00:00.000Z", "2025-01-06", 1, "4.00"},
	})
	assertTrendVector(t, month, []trendExpectation{
		{"2020-01-01T00:00:00.000Z", "2020-01-01", 1, "1.00"},
		{"2020-02-01T00:00:00.000Z", "2020-02-01", 1, "2.00"},
		{"2020-03-01T00:00:00.000Z", "2020-03-01", 0, "0.00"},
		{"2020-04-01T00:00:00.000Z", "2020-04-01", 1, "4.00"},
	})
	assertTrendVector(t, year, []trendExpectation{
		{"2019-01-01T00:00:00.000Z", "2019-01-01", 1, "1.00"},
		{"2020-01-01T00:00:00.000Z", "2020-01-01", 2, "5.00"},
		{"2021-01-01T00:00:00.000Z", "2021-01-01", 1, "4.00"},
	})
	assertTrendCountsAndAmounts(t, five, []int64{1, 2, 1, 1}, []string{
		"1000.00", "2000.00", "1000.00", "1000.00",
	})
	assertTrendCountsAndAmounts(t, house, []int64{1, 1}, []string{"1000.00", "1000.00"})
	assertTrendCountsAndAmounts(
		t,
		decimals,
		[]int64{1, 1, 1, 1, 2, 2},
		[]string{"1.01", "-1.01", "0.00", "1000.00", "0.30", "2.00"},
	)
	if len(empty.PeriodSales) != 0 || len(equal.PeriodSales) != 0 ||
		len(reversed.PeriodSales) != 0 {
		t.Fatalf(
			"empty/equal/reversed sizes = %d/%d/%d",
			len(empty.PeriodSales),
			len(equal.PeriodSales),
			len(reversed.PeriodSales),
		)
	}
	if !errors.Is(nonfiniteErr, ErrInvalidResponse) {
		t.Fatalf("non-finite aggregate = %v", nonfiniteErr)
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

func executeSalesTrends(
	t *testing.T,
	useCase application.GetSalesTrends,
	product domain.ProductSelection,
	from, to string,
	unit domain.AggregationUnit,
) application.SalesTrendsResult {
	t.Helper()
	result, err := useCase.Execute(
		context.Background(),
		salesTrendsQuery(t, product, from, to, unit),
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func salesTrendsQuery(
	t *testing.T,
	product domain.ProductSelection,
	from, to string,
	unit domain.AggregationUnit,
) application.SalesTrendsQuery {
	t.Helper()
	dateFrom, err := domain.ParseDate(from)
	if err != nil {
		t.Fatal(err)
	}
	dateTo, err := domain.ParseDate(to)
	if err != nil {
		t.Fatal(err)
	}
	return application.SalesTrendsQuery{
		Product: product, DateFrom: dateFrom, DateTo: dateTo, AggregationUnit: unit,
	}
}

type trendExpectation struct {
	period string
	date   string
	count  int64
	amount string
}

func assertTrendVector(
	t *testing.T,
	result application.SalesTrendsResult,
	want []trendExpectation,
) {
	t.Helper()
	if len(result.PeriodSales) != len(want) {
		t.Fatalf("trend period count = %d, want %d", len(result.PeriodSales), len(want))
	}
	for index, expected := range want {
		actual := result.PeriodSales[index]
		if actual.Period != expected.period ||
			actual.PeriodDate.String() != expected.date ||
			actual.Value.PoliciesCount() != expected.count ||
			actual.Value.PremiumAmount().String() != expected.amount {
			t.Fatalf("trend period %d = %#v, want %#v", index, actual, expected)
		}
	}
}

func assertTrendCountsAndAmounts(
	t *testing.T,
	result application.SalesTrendsResult,
	wantCounts []int64,
	wantAmounts []string,
) {
	t.Helper()
	if len(result.PeriodSales) != len(wantCounts) || len(wantCounts) != len(wantAmounts) {
		t.Fatalf(
			"trend lengths = periods:%d counts:%d amounts:%d",
			len(result.PeriodSales),
			len(wantCounts),
			len(wantAmounts),
		)
	}
	for index, period := range result.PeriodSales {
		if period.Value.PoliciesCount() != wantCounts[index] ||
			period.Value.PremiumAmount().String() != wantAmounts[index] {
			t.Fatalf("trend period %d = %#v", index, period)
		}
	}
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

var acceptedTrendOracleFixture = strings.TrimSpace(`
{"index":{"_id":"five-1"}}
{"productCode":"SAFE_HOUSE","from":"2019-01-01","totalPremium":1000}
{"index":{"_id":"five-2"}}
{"productCode":"SAFE_HOUSE","from":"2019-02-01","totalPremium":1000}
{"index":{"_id":"five-3"}}
{"productCode":"SAFE_CAR","from":"2019-02-02","totalPremium":1000}
{"index":{"_id":"five-4"}}
{"productCode":"SAFE_CAR","from":"2019-03-01","totalPremium":1000}
{"index":{"_id":"five-5"}}
{"productCode":"SAFE_FARM","from":"2019-04-01","totalPremium":1000}
{"index":{"_id":"day-1"}}
{"productCode":"CAL_DAY","from":"2020-02-28","totalPremium":1}
{"index":{"_id":"day-2"}}
{"productCode":"CAL_DAY","from":"2020-02-29","totalPremium":2}
{"index":{"_id":"day-3"}}
{"productCode":"CAL_DAY","from":"2020-03-01","totalPremium":3}
{"index":{"_id":"week-1"}}
{"productCode":"CAL_WEEK","from":"2024-12-29","totalPremium":1}
{"index":{"_id":"week-2"}}
{"productCode":"CAL_WEEK","from":"2024-12-30","totalPremium":2}
{"index":{"_id":"week-3"}}
{"productCode":"CAL_WEEK","from":"2025-01-05","totalPremium":3}
{"index":{"_id":"week-4"}}
{"productCode":"CAL_WEEK","from":"2025-01-06","totalPremium":4}
{"index":{"_id":"month-1"}}
{"productCode":"CAL_MONTH","from":"2020-01-31","totalPremium":1}
{"index":{"_id":"month-2"}}
{"productCode":"CAL_MONTH","from":"2020-02-29","totalPremium":2}
{"index":{"_id":"month-3"}}
{"productCode":"CAL_MONTH","from":"2020-04-01","totalPremium":4}
{"index":{"_id":"year-1"}}
{"productCode":"CAL_YEAR","from":"2019-12-31","totalPremium":1}
{"index":{"_id":"year-2"}}
{"productCode":"CAL_YEAR","from":"2020-01-01","totalPremium":2}
{"index":{"_id":"year-3"}}
{"productCode":"CAL_YEAR","from":"2020-12-31","totalPremium":3}
{"index":{"_id":"year-4"}}
{"productCode":"CAL_YEAR","from":"2021-01-01","totalPremium":4}
{"index":{"_id":"decimal-positive"}}
{"productCode":"DECIMAL","from":"2022-01-01","totalPremium":1.005}
{"index":{"_id":"decimal-negative"}}
{"productCode":"DECIMAL","from":"2022-02-01","totalPremium":-1.005}
{"index":{"_id":"decimal-zero"}}
{"productCode":"DECIMAL","from":"2022-03-01","totalPremium":0}
{"index":{"_id":"decimal-whole"}}
{"productCode":"DECIMAL","from":"2022-04-01","totalPremium":1000}
{"index":{"_id":"decimal-fraction-a"}}
{"productCode":"DECIMAL","from":"2022-05-01","totalPremium":0.1}
{"index":{"_id":"decimal-fraction-b"}}
{"productCode":"DECIMAL","from":"2022-05-02","totalPremium":0.2}
{"index":{"_id":"decimal-missing"}}
{"productCode":"DECIMAL","from":"2022-06-01"}
{"index":{"_id":"decimal-present"}}
{"productCode":"DECIMAL","from":"2022-06-02","totalPremium":2}
{"index":{"_id":"nonfinite-a"}}
{"productCode":"NONFINITE","from":"2023-01-01","totalPremium":1.7e308}
{"index":{"_id":"nonfinite-b"}}
{"productCode":"NONFINITE","from":"2023-01-02","totalPremium":1.7e308}
`) + "\n"
