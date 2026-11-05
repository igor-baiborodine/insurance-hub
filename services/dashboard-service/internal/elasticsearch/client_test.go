package elasticsearch

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

func TestNew_RequiresExactBoundedReadOnlyConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "missing endpoint", config: Config{Timeout: time.Second}},
		{
			name: "credentials in URL",
			config: Config{
				Endpoint: "https://user:secret@example:9200",
				Timeout:  time.Second,
			},
		},
		{
			name:   "path in URL",
			config: Config{Endpoint: "https://example:9200/path", Timeout: time.Second},
		},
		{
			name:   "unsupported scheme",
			config: Config{Endpoint: "ftp://example:9200", Timeout: time.Second},
		},
		{
			name: "wrong index",
			config: Config{
				Endpoint: "http://example:9200",
				Index:    "other",
				Timeout:  time.Second,
			},
		},
		{name: "missing timeout", config: Config{Endpoint: "http://example:9200"}},
		{
			name: "unpaired username",
			config: Config{
				Endpoint: "http://example:9200",
				Username: "reader",
				Timeout:  time.Second,
			},
		},
		{
			name: "CA over HTTP",
			config: Config{
				Endpoint: "http://example:9200",
				CACert:   []byte("ca"),
				Timeout:  time.Second,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			client, err := New(test.config)

			// then
			if err == nil || client != nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("New() = (%v, %v)", client, err)
			}
		})
	}
}

func TestClient_DoesNotRetryRejectedSearch(t *testing.T) {
	// given
	requests := 0
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			requests++
			writer.Header().Set("X-Elastic-Product", "Elasticsearch")
			writer.WriteHeader(http.StatusServiceUnavailable)
		}),
	)
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, Index: PolicyIndex, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewReader(client)
	if err != nil {
		t.Fatal(err)
	}
	from, _ := domain.ParseDate("2019-01-01")
	to, _ := domain.ParseDate("2019-12-31")

	// when
	_, err = reader.ReadTotalSales(context.Background(), application.TotalSalesQuery{
		Product: domain.AllProducts(), DateFrom: from, DateTo: to,
	})

	// then
	if !errors.Is(err, ErrSearchRejected) || requests != 1 {
		t.Fatalf("ReadTotalSales() = (%v, requests=%d)", err, requests)
	}
}

func TestClient_CheckUsesExactIndexAndVerifiedTLS(t *testing.T) {
	// given
	requests := make([]string, 0, 4)
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			requests = append(requests, request.Method+" "+request.URL.Path)
			writer.Header().Set("X-Elastic-Product", "Elasticsearch")
			switch request.URL.Path {
			case "/_resolve/index/policy_stats":
				_, _ = writer.Write(
					[]byte(
						`{"indices":[{"name":"policy_stats","attributes":["open"]}],"aliases":[],"data_streams":[]}`,
					),
				)
			case "/policy_stats/_mapping":
				_, _ = writer.Write(
					[]byte(
						`{"policy_stats":{"mappings":{"properties":{"productCode":{"type":"text","fields":{"keyword":{"type":"keyword"}}},"from":{"type":"date"},"totalPremium":{"type":"double"}}}}}`,
					),
				)
			case "/policy_stats/_field_caps":
				_, _ = writer.Write(
					[]byte(
						`{"fields":{"productCode.keyword":{"keyword":{"searchable":true,"aggregatable":true}},"from":{"date":{"searchable":true,"aggregatable":true}},"totalPremium":{"double":{"searchable":true,"aggregatable":true}}}}`,
					),
				)
			case "/policy_stats/_search":
				_, _ = writer.Write(
					[]byte(
						`{"aggregations":{"products":{"doc_count_error_upper_bound":0,"buckets":[]}}}`,
					),
				)
			default:
				t.Errorf(
					"unexpected request = %s %s",
					request.Method,
					request.URL.Path,
				)
				writer.WriteHeader(http.StatusNotFound)
			}
		}),
	)
	defer server.Close()
	certificate := pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: server.Certificate().Raw,
	})
	client, err := New(Config{
		Endpoint: server.URL, Index: PolicyIndex, CACert: certificate, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()

	// when
	err = client.Check(context.Background())
	// then
	if err != nil {
		t.Fatal(err)
	}
	wantRequests := []string{
		"GET /_resolve/index/policy_stats",
		"GET /policy_stats/_mapping",
		"POST /policy_stats/_field_caps",
		"POST /policy_stats/_search",
	}
	if strings.Join(requests, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("requests = %q, want %q", requests, wantRequests)
	}
}

func TestClient_CheckRejectsAliasAndIncompatibleState(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{
			name:     "alias",
			response: `{"indices":[],"aliases":[{"name":"policy_stats","indices":["actual"]}],"data_streams":[]}`,
		},
		{
			name:     "closed index",
			response: `{"indices":[{"name":"policy_stats","attributes":["closed"]}],"aliases":[],"data_streams":[]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writer.Header().Set("X-Elastic-Product", "Elasticsearch")
					_, _ = writer.Write([]byte(test.response))
				}),
			)
			defer server.Close()
			client, err := New(
				Config{
					Endpoint: server.URL,
					Index:    PolicyIndex,
					Timeout:  time.Second,
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			if err := client.Check(context.Background()); err == nil {
				t.Fatal("Check() accepted incompatible index identity")
			}
		})
	}
}
