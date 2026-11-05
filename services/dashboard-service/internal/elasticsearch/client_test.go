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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.Header().Set("X-Elastic-Product", "Elasticsearch")
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
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
	requests := 0
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			requests++
			if request.Method != http.MethodHead ||
				request.URL.Path != "/policy_stats" {
				t.Errorf("request = %s %s", request.Method, request.URL.Path)
			}
			writer.Header().Set("X-Elastic-Product", "Elasticsearch")
			writer.WriteHeader(http.StatusOK)
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
	if requests != 1 {
		t.Fatalf("request count = %d, want 1", requests)
	}
}
